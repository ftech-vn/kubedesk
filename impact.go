package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

// ImpactAction is a change whose impact the app can preview.
type ImpactAction string

const (
	ImpactDelete  ImpactAction = "delete"
	ImpactScale   ImpactAction = "scale"
	ImpactDrain   ImpactAction = "drain"
	ImpactRestart ImpactAction = "restart"
	ImpactCordon  ImpactAction = "cordon"
)

// ImpactRequest names a change to preview.
type ImpactRequest struct {
	Action    ImpactAction `json:"action"`
	Kind      string       `json:"kind"`
	Namespace string       `json:"namespace"`
	Name      string       `json:"name"`
	// Replicas is the count a scale asks for.
	Replicas int `json:"replicas,omitempty"`
}

// ImpactSeverity says how much an effect matters.
type ImpactSeverity string

const (
	ImpactDanger  ImpactSeverity = "danger"
	ImpactWarning ImpactSeverity = "warning"
	ImpactInfo    ImpactSeverity = "info"
)

// ImpactItem is one effect of a change, possibly on another object.
type ImpactItem struct {
	Severity  ImpactSeverity `json:"severity"`
	Text      string         `json:"text"`
	Kind      string         `json:"kind,omitempty"`
	Namespace string         `json:"namespace,omitempty"`
	Name      string         `json:"name,omitempty"`
}

// Impact previews what a change would do to the cluster beyond its object:
// services left without endpoints, ingresses without backends, disruption
// budgets broken, data lost, pods that would not fit elsewhere. It reads the
// cluster and changes nothing.
func (k *Kube) Impact(ctx context.Context, cluster string, req ImpactRequest) ([]ImpactItem, error) {
	cn, err := k.clusters.conn(cluster)
	if err != nil {
		return nil, err
	}
	items, err := impactOf(ctx, cn.cs, req)
	if err != nil {
		return nil, fmt.Errorf("could not work out the impact: %s", friendlyError(err))
	}
	return items, nil
}

type impact struct {
	ctx   context.Context
	cs    kubernetes.Interface
	items []ImpactItem
	pods  map[string][]corev1.Pod
}

func (im *impact) add(sev ImpactSeverity, kind, ns, name, format string, args ...any) {
	im.items = append(im.items, ImpactItem{Severity: sev, Text: fmt.Sprintf(format, args...), Kind: kind, Namespace: ns, Name: name})
}

func (im *impact) podsIn(ns string) ([]corev1.Pod, error) {
	if p, ok := im.pods[ns]; ok {
		return p, nil
	}
	list, err := im.cs.CoreV1().Pods(ns).List(im.ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	im.pods[ns] = list.Items
	return list.Items, nil
}

func impactOf(ctx context.Context, cs kubernetes.Interface, req ImpactRequest) ([]ImpactItem, error) {
	im := &impact{ctx: ctx, cs: cs, pods: map[string][]corev1.Pod{}}
	var err error
	switch req.Action {
	case ImpactDelete:
		err = im.delete(req)
	case ImpactScale:
		err = im.scale(req)
	case ImpactDrain:
		err = im.drain(req.Name, true)
	case ImpactCordon:
		err = im.drain(req.Name, false)
	case ImpactRestart:
		err = im.restart(req)
	}
	order := map[ImpactSeverity]int{ImpactDanger: 0, ImpactWarning: 1, ImpactInfo: 2}
	sort.SliceStable(im.items, func(i, j int) bool { return order[im.items[i].Severity] < order[im.items[j].Severity] })
	return im.items, err
}

// workload reads the selector and the pods of a workload.
func (im *impact) workload(kind, ns, name string) (sel labels.Selector, obj metav1.Object, replicas int32, pods []corev1.Pod, err error) {
	var ls *metav1.LabelSelector
	switch kind {
	case "Deployment":
		d, e := im.cs.AppsV1().Deployments(ns).Get(im.ctx, name, metav1.GetOptions{})
		if e != nil {
			return nil, nil, 0, nil, e
		}
		ls, obj, replicas = d.Spec.Selector, d, ptrInt32(d.Spec.Replicas)
	case "StatefulSet":
		d, e := im.cs.AppsV1().StatefulSets(ns).Get(im.ctx, name, metav1.GetOptions{})
		if e != nil {
			return nil, nil, 0, nil, e
		}
		ls, obj, replicas = d.Spec.Selector, d, ptrInt32(d.Spec.Replicas)
	case "DaemonSet":
		d, e := im.cs.AppsV1().DaemonSets(ns).Get(im.ctx, name, metav1.GetOptions{})
		if e != nil {
			return nil, nil, 0, nil, e
		}
		ls, obj, replicas = d.Spec.Selector, d, d.Status.DesiredNumberScheduled
	case "ReplicaSet":
		d, e := im.cs.AppsV1().ReplicaSets(ns).Get(im.ctx, name, metav1.GetOptions{})
		if e != nil {
			return nil, nil, 0, nil, e
		}
		ls, obj, replicas = d.Spec.Selector, d, ptrInt32(d.Spec.Replicas)
	case "Job":
		d, e := im.cs.BatchV1().Jobs(ns).Get(im.ctx, name, metav1.GetOptions{})
		if e != nil {
			return nil, nil, 0, nil, e
		}
		ls, obj = d.Spec.Selector, d
	default:
		return nil, nil, 0, nil, nil
	}
	sel, err = metav1.LabelSelectorAsSelector(ls)
	if err != nil {
		return nil, nil, 0, nil, err
	}
	all, err := im.podsIn(ns)
	if err != nil {
		return nil, nil, 0, nil, err
	}
	for _, p := range all {
		if sel.Matches(labels.Set(p.Labels)) && p.DeletionTimestamp == nil {
			pods = append(pods, p)
		}
	}
	return sel, obj, replicas, pods, nil
}

func (im *impact) delete(req ImpactRequest) error {
	ns, name := req.Namespace, req.Name
	switch req.Kind {
	case "Pod":
		p, err := im.cs.CoreV1().Pods(ns).Get(im.ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if ref := metav1.GetControllerOf(p); ref != nil {
			im.add(ImpactInfo, ref.Kind, ns, ref.Name, "%s %s makes a new pod to replace it", ref.Kind, ref.Name)
		} else {
			im.add(ImpactDanger, "", "", "", "No controller owns this pod: once deleted, it does not come back")
		}
		im.localData([]corev1.Pod{*p})
		if err := im.budgetsFor(ns, []corev1.Pod{*p}, "Deleting bypasses"); err != nil {
			return err
		}
		return im.endpointsLost(ns, []corev1.Pod{*p})
	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job":
		_, obj, _, pods, err := im.workload(req.Kind, ns, name)
		if err != nil {
			return err
		}
		if len(pods) > 0 {
			im.add(ImpactDanger, "", "", "", "%d pod%s %s deleted", len(pods), plural(len(pods)), isAre(len(pods)))
		}
		im.managedBy(obj)
		im.localData(pods)
		if err := im.endpointsLost(ns, pods); err != nil {
			return err
		}
		if err := im.autoscalersOf(req.Kind, ns, name, "is left without a target"); err != nil {
			return err
		}
		if req.Kind == "StatefulSet" {
			if sts, ok := obj.(*appsv1.StatefulSet); ok && len(sts.Spec.VolumeClaimTemplates) > 0 {
				im.add(ImpactInfo, "", "", "", "Its persistent volume claims are kept: delete them to free their storage")
			}
		}
		return nil
	case "Service":
		return im.ingressesUsing(ns, map[string]bool{name: true}, "loses its backend")
	case "ConfigMap", "Secret":
		return im.podsUsing(req.Kind, ns, name)
	case "PersistentVolumeClaim":
		return im.claim(ns, name)
	case "Namespace":
		return im.namespace(name)
	case "Ingress":
		in, err := im.cs.NetworkingV1().Ingresses(ns).Get(im.ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		var hosts []string
		for _, r := range in.Spec.Rules {
			hosts = append(hosts, orDefault(r.Host, "*"))
		}
		if len(hosts) > 0 {
			im.add(ImpactDanger, "", "", "", "Requests to %s are no longer routed", strings.Join(hosts, ", "))
		}
	case "Node":
		all, err := im.podsIn("")
		if err != nil {
			return err
		}
		n := 0
		for _, p := range all {
			if p.Spec.NodeName == name {
				n++
			}
		}
		im.add(ImpactWarning, "", "", "", "The %d pods on it lose their node; drain it first to move them in order", n)
	}
	return nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

// managedBy warns about objects a tool keeps in sync, which may bring them
// back or break.
func (im *impact) managedBy(obj metav1.Object) {
	if obj == nil {
		return
	}
	l, a := obj.GetLabels(), obj.GetAnnotations()
	switch {
	case a["argocd.argoproj.io/tracking-id"] != "" || l["app.kubernetes.io/instance"] != "" && l["argocd.argoproj.io/instance"] != "":
		im.add(ImpactWarning, "", "", "", "Argo CD manages it: it may recreate it, or mark the app out of sync")
	case l["app.kubernetes.io/managed-by"] == "Helm":
		im.add(ImpactWarning, "", "", "", "Helm manages it (release %s): the release will be out of sync", a["meta.helm.sh/release-name"])
	case l["kustomize.toolkit.fluxcd.io/name"] != "" || l["helm.toolkit.fluxcd.io/name"] != "":
		im.add(ImpactWarning, "", "", "", "Flux manages it: it will put it back")
	}
}

// localData warns about data that lives with the pods.
func (im *impact) localData(pods []corev1.Pod) {
	n := 0
	for _, p := range pods {
		for _, v := range p.Spec.Volumes {
			if v.EmptyDir != nil {
				n++
				break
			}
		}
	}
	if n > 0 {
		im.add(ImpactWarning, "", "", "", "%d pod%s keep data in emptyDir volumes, which is lost", n, plural(n))
	}
}

// endpointsLost finds services whose only pods are those going away, and
// the ingresses that send traffic to them.
func (im *impact) endpointsLost(ns string, leaving []corev1.Pod) error {
	if len(leaving) == 0 {
		return nil
	}
	gone := map[string]bool{}
	for _, p := range leaving {
		gone[p.Name] = true
	}
	svcs, err := im.cs.CoreV1().Services(ns).List(im.ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	all, err := im.podsIn(ns)
	if err != nil {
		return err
	}
	lost := map[string]bool{}
	for _, s := range svcs.Items {
		if len(s.Spec.Selector) == 0 {
			continue
		}
		sel := labels.SelectorFromSet(s.Spec.Selector)
		served, remaining := 0, 0
		for _, p := range all {
			if !sel.Matches(labels.Set(p.Labels)) || p.DeletionTimestamp != nil {
				continue
			}
			if gone[p.Name] {
				served++
			} else {
				remaining++
			}
		}
		if served > 0 && remaining == 0 {
			lost[s.Name] = true
			im.add(ImpactDanger, "Service", ns, s.Name, "Service %s loses all its endpoints", s.Name)
		} else if served > 0 {
			im.add(ImpactInfo, "Service", ns, s.Name, "Service %s keeps %d other pod%s", s.Name, remaining, plural(remaining))
		}
	}
	return im.ingressesUsing(ns, lost, "stops working")
}

func (im *impact) ingressesUsing(ns string, services map[string]bool, what string) error {
	if len(services) == 0 {
		return nil
	}
	ings, err := im.cs.NetworkingV1().Ingresses(ns).List(im.ctx, metav1.ListOptions{})
	if err != nil {
		return nil // ingresses are optional
	}
	for _, in := range ings.Items {
		var routes []string
		for _, r := range in.Spec.Rules {
			if r.HTTP == nil {
				continue
			}
			for _, p := range r.HTTP.Paths {
				if p.Backend.Service != nil && services[p.Backend.Service.Name] {
					routes = append(routes, orDefault(r.Host, "*")+orDefault(p.Path, "/"))
				}
			}
		}
		if d := in.Spec.DefaultBackend; d != nil && d.Service != nil && services[d.Service.Name] {
			routes = append(routes, "default backend")
		}
		if len(routes) > 0 {
			im.add(ImpactDanger, "Ingress", ns, in.Name, "Ingress %s %s for %s", in.Name, what, strings.Join(limitList(routes, 4), ", "))
		}
	}
	return nil
}

func (im *impact) autoscalersOf(kind, ns, name, what string) error {
	hpas, err := im.cs.AutoscalingV2().HorizontalPodAutoscalers(ns).List(im.ctx, metav1.ListOptions{})
	if err != nil {
		return nil
	}
	for _, h := range hpas.Items {
		if h.Spec.ScaleTargetRef.Kind == kind && h.Spec.ScaleTargetRef.Name == name {
			im.add(ImpactWarning, "HorizontalPodAutoscaler", ns, h.Name, "Autoscaler %s %s (it keeps %d–%d replicas)", h.Name, what, ptrInt32(h.Spec.MinReplicas), h.Spec.MaxReplicas)
		}
	}
	return nil
}

// budgetsFor finds the disruption budgets that cover pods and allow fewer
// disruptions than the pods.
func (im *impact) budgetsFor(ns string, pods []corev1.Pod, verb string) error {
	pdbs, err := im.cs.PolicyV1().PodDisruptionBudgets(ns).List(im.ctx, metav1.ListOptions{})
	if err != nil {
		return nil
	}
	for _, b := range pdbs.Items {
		n := podsOfBudget(&b, pods)
		if n == 0 {
			continue
		}
		if int(b.Status.DisruptionsAllowed) < n {
			im.add(ImpactWarning, "PodDisruptionBudget", b.Namespace, b.Name, "%s disruption budget %s, which allows %d disruption%s now", verb, b.Name, b.Status.DisruptionsAllowed, plural(int(b.Status.DisruptionsAllowed)))
		}
	}
	return nil
}

func podsOfBudget(b *policyv1.PodDisruptionBudget, pods []corev1.Pod) int {
	sel, err := metav1.LabelSelectorAsSelector(b.Spec.Selector)
	if err != nil || sel.Empty() {
		return 0
	}
	n := 0
	for _, p := range pods {
		if p.Namespace == b.Namespace && sel.Matches(labels.Set(p.Labels)) {
			n++
		}
	}
	return n
}

// podsUsing finds the pods that need a config map or secret, by workload.
func (im *impact) podsUsing(kind, ns, name string) error {
	all, err := im.podsIn(ns)
	if err != nil {
		return err
	}
	owners := map[string]int{}
	for _, p := range all {
		if usesRef(&p, kind, name) {
			owner := "pod " + p.Name
			if ref := metav1.GetControllerOf(&p); ref != nil {
				owner = strings.ToLower(ref.Kind) + " " + ownerName(ref)
			}
			owners[owner]++
		}
	}
	if len(owners) == 0 {
		im.add(ImpactInfo, "", "", "", "No pod in %s uses it", ns)
	}
	keys := make([]string, 0, len(owners))
	for k := range owners {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, o := range keys {
		im.add(ImpactDanger, "", "", "", "Used by %d pod%s of %s: they keep running, but fail to start again", owners[o], plural(owners[o]), o)
	}
	if kind == "Secret" {
		ings, err := im.cs.NetworkingV1().Ingresses(ns).List(im.ctx, metav1.ListOptions{})
		if err == nil {
			for _, in := range ings.Items {
				for _, t := range in.Spec.TLS {
					if t.SecretName == name {
						im.add(ImpactDanger, "Ingress", ns, in.Name, "Ingress %s uses it as the TLS certificate of %s", in.Name, strings.Join(t.Hosts, ", "))
					}
				}
			}
		}
	}
	return nil
}

// ownerName drops the template hash from the names of replica sets, so
// that pods read as their deployment's.
func ownerName(ref *metav1.OwnerReference) string {
	if ref.Kind == "ReplicaSet" {
		if i := strings.LastIndex(ref.Name, "-"); i > 0 {
			return ref.Name[:i]
		}
	}
	return ref.Name
}

func usesRef(p *corev1.Pod, kind, name string) bool {
	isCM := kind == "ConfigMap"
	for _, v := range p.Spec.Volumes {
		if isCM && v.ConfigMap != nil && v.ConfigMap.Name == name || !isCM && v.Secret != nil && v.Secret.SecretName == name {
			return true
		}
		if v.Projected != nil {
			for _, s := range v.Projected.Sources {
				if isCM && s.ConfigMap != nil && s.ConfigMap.Name == name || !isCM && s.Secret != nil && s.Secret.Name == name {
					return true
				}
			}
		}
	}
	if !isCM {
		for _, s := range p.Spec.ImagePullSecrets {
			if s.Name == name {
				return true
			}
		}
	}
	for _, c := range append(append([]corev1.Container{}, p.Spec.InitContainers...), p.Spec.Containers...) {
		for _, e := range c.EnvFrom {
			if isCM && e.ConfigMapRef != nil && e.ConfigMapRef.Name == name || !isCM && e.SecretRef != nil && e.SecretRef.Name == name {
				return true
			}
		}
		for _, e := range c.Env {
			if e.ValueFrom == nil {
				continue
			}
			if isCM && e.ValueFrom.ConfigMapKeyRef != nil && e.ValueFrom.ConfigMapKeyRef.Name == name || !isCM && e.ValueFrom.SecretKeyRef != nil && e.ValueFrom.SecretKeyRef.Name == name {
				return true
			}
		}
	}
	return false
}

func (im *impact) claim(ns, name string) error {
	pvc, err := im.cs.CoreV1().PersistentVolumeClaims(ns).Get(im.ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	all, err := im.podsIn(ns)
	if err != nil {
		return err
	}
	for _, p := range all {
		for _, v := range p.Spec.Volumes {
			if v.PersistentVolumeClaim != nil && v.PersistentVolumeClaim.ClaimName == name {
				im.add(ImpactWarning, "Pod", ns, p.Name, "Pod %s mounts it: the claim is deleted once the pod is gone", p.Name)
			}
		}
	}
	if pvc.Spec.VolumeName != "" {
		pv, err := im.cs.CoreV1().PersistentVolumes().Get(im.ctx, pvc.Spec.VolumeName, metav1.GetOptions{})
		if err == nil && pv.Spec.PersistentVolumeReclaimPolicy == corev1.PersistentVolumeReclaimDelete {
			im.add(ImpactDanger, "PersistentVolume", "", pv.Name, "Its volume %s is deleted with it (reclaim policy Delete): the data is gone", pv.Name)
		} else if err == nil {
			im.add(ImpactInfo, "PersistentVolume", "", pv.Name, "Its volume %s is kept (reclaim policy %s)", pv.Name, pv.Spec.PersistentVolumeReclaimPolicy)
		}
	}
	return nil
}

func (im *impact) namespace(name string) error {
	pods, err := im.podsIn(name)
	if err != nil {
		return err
	}
	var parts []string
	count := func(n int, what string) {
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s%s", n, what, plural(n)))
		}
	}
	count(len(pods), "pod")
	if l, err := im.cs.AppsV1().Deployments(name).List(im.ctx, metav1.ListOptions{}); err == nil {
		count(len(l.Items), "deployment")
	}
	if l, err := im.cs.AppsV1().StatefulSets(name).List(im.ctx, metav1.ListOptions{}); err == nil {
		count(len(l.Items), "stateful set")
	}
	if l, err := im.cs.CoreV1().Services(name).List(im.ctx, metav1.ListOptions{}); err == nil {
		count(len(l.Items), "service")
	}
	if l, err := im.cs.CoreV1().Secrets(name).List(im.ctx, metav1.ListOptions{}); err == nil {
		count(len(l.Items), "secret")
	}
	if l, err := im.cs.CoreV1().PersistentVolumeClaims(name).List(im.ctx, metav1.ListOptions{}); err == nil {
		count(len(l.Items), "volume claim")
		for _, c := range l.Items {
			if c.Spec.VolumeName == "" {
				continue
			}
			if pv, err := im.cs.CoreV1().PersistentVolumes().Get(im.ctx, c.Spec.VolumeName, metav1.GetOptions{}); err == nil && pv.Spec.PersistentVolumeReclaimPolicy == corev1.PersistentVolumeReclaimDelete {
				im.add(ImpactDanger, "PersistentVolumeClaim", name, c.Name, "The data of %s is deleted (reclaim policy Delete)", c.Name)
			}
		}
	}
	if len(parts) > 0 {
		im.add(ImpactDanger, "", "", "", "Everything in it is deleted: %s", strings.Join(parts, ", "))
	} else {
		im.add(ImpactInfo, "", "", "", "It holds no pods, workloads, services, secrets or volumes")
	}
	return nil
}

func (im *impact) scale(req ImpactRequest) error {
	_, obj, current, pods, err := im.workload(req.Kind, req.Namespace, req.Name)
	if err != nil || obj == nil {
		return err
	}
	want := int32(req.Replicas)
	switch {
	case want == current:
		im.add(ImpactInfo, "", "", "", "It already has %d replica%s", current, plural(int(current)))
	case want == 0:
		im.add(ImpactDanger, "", "", "", "All %d pod%s stop", len(pods), plural(len(pods)))
		if err := im.endpointsLost(req.Namespace, pods); err != nil {
			return err
		}
	case want < current:
		n := int(current - want)
		im.add(ImpactWarning, "", "", "", "%d pod%s %s removed", n, plural(n), isAre(n))
		if err := im.minAvailable(req.Namespace, pods, int(want)); err != nil {
			return err
		}
	default:
		if err := im.capacityFor(pods, int(want-current)); err != nil {
			return err
		}
	}
	if want != current {
		if err := im.autoscalersOf(req.Kind, req.Namespace, req.Name, "will set the replicas back"); err != nil {
			return err
		}
	}
	return nil
}

// minAvailable warns when fewer pods than a budget needs would remain.
func (im *impact) minAvailable(ns string, pods []corev1.Pod, remain int) error {
	pdbs, err := im.cs.PolicyV1().PodDisruptionBudgets(ns).List(im.ctx, metav1.ListOptions{})
	if err != nil {
		return nil
	}
	for _, b := range pdbs.Items {
		n := podsOfBudget(&b, pods)
		if n == 0 || b.Spec.MinAvailable == nil {
			continue
		}
		need, err := intstr.GetScaledValueFromIntOrPercent(b.Spec.MinAvailable, n, true)
		if err == nil && remain < need {
			im.add(ImpactWarning, "PodDisruptionBudget", ns, b.Name, "Disruption budget %s asks for at least %d available pods", b.Name, need)
		}
	}
	return nil
}

// capacityFor checks that the cluster has room for more pods like these.
func (im *impact) capacityFor(pods []corev1.Pod, more int) error {
	if len(pods) == 0 || more <= 0 {
		return nil
	}
	cpu, mem := requestsOf(&pods[0])
	free, err := im.freeCapacity("")
	if err != nil {
		return err
	}
	var fcpu, fmem int64
	for _, f := range free {
		fcpu += f.cpu
		fmem += f.mem
	}
	needCPU, needMem := cpu*int64(more), mem*int64(more)
	if needCPU > fcpu || needMem > fmem {
		im.add(ImpactWarning, "", "", "", "%d more pods ask for %s CPU and %s memory; the nodes have %s CPU and %s memory free: some may stay pending",
			more, milli(needCPU), bytesStr(needMem), milli(fcpu), bytesStr(fmem))
	} else {
		im.add(ImpactInfo, "", "", "", "%d more pods fit: they ask for %s CPU and %s memory of the %s CPU and %s memory free", more, milli(needCPU), bytesStr(needMem), milli(fcpu), bytesStr(fmem))
	}
	return nil
}

type nodeRoom struct {
	name     string
	cpu, mem int64
}

// freeCapacity is what the schedulable, ready nodes but one have left of
// what they can allocate, after the requests of their pods.
func (im *impact) freeCapacity(except string) ([]nodeRoom, error) {
	nodes, err := im.cs.CoreV1().Nodes().List(im.ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	all, err := im.podsIn("")
	if err != nil {
		return nil, err
	}
	used := map[string][2]int64{}
	for _, p := range all {
		if p.Spec.NodeName == "" || p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}
		c, m := requestsOf(&p)
		u := used[p.Spec.NodeName]
		used[p.Spec.NodeName] = [2]int64{u[0] + c, u[1] + m}
	}
	var out []nodeRoom
	for _, n := range nodes.Items {
		if n.Name == except || n.Spec.Unschedulable || !nodeIsReady(&n) {
			continue
		}
		u := used[n.Name]
		out = append(out, nodeRoom{
			name: n.Name,
			cpu:  n.Status.Allocatable.Cpu().MilliValue() - u[0],
			mem:  n.Status.Allocatable.Memory().Value() - u[1],
		})
	}
	return out, nil
}

func nodeIsReady(n *corev1.Node) bool {
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// requestsOf sums the CPU (millicores) and memory (bytes) a pod asks for.
func requestsOf(p *corev1.Pod) (cpu, mem int64) {
	for _, c := range p.Spec.Containers {
		cpu += c.Resources.Requests.Cpu().MilliValue()
		mem += c.Resources.Requests.Memory().Value()
	}
	return cpu, mem
}

func milli(m int64) string {
	return resource.NewMilliQuantity(m, resource.DecimalSI).String()
}

func bytesStr(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1fGi", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%dMi", b>>20)
	}
	return fmt.Sprintf("%dKi", b>>10)
}

// drain previews draining a node, or only cordoning it.
func (im *impact) drain(node string, evict bool) error {
	all, err := im.podsIn("")
	if err != nil {
		return err
	}
	var on, evicted []corev1.Pod
	daemons := 0
	for _, p := range all {
		if p.Spec.NodeName != node || p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}
		on = append(on, p)
		if _, mirror := p.Annotations["kubernetes.io/config.mirror"]; mirror {
			continue
		}
		if ref := metav1.GetControllerOf(&p); ref != nil && ref.Kind == "DaemonSet" {
			daemons++
			continue
		}
		evicted = append(evicted, p)
	}
	if !evict {
		im.add(ImpactInfo, "", "", "", "Its %d pod%s keep running; no new pods are scheduled on it", len(on), plural(len(on)))
		return nil
	}
	im.add(ImpactInfo, "", "", "", "%d pod%s %s evicted, and their controllers start them on other nodes", len(evicted), plural(len(evicted)), isAre(len(evicted)))
	if daemons > 0 {
		im.add(ImpactInfo, "", "", "", "%d daemon set pod%s stay", daemons, plural(daemons))
	}
	for _, p := range evicted {
		if metav1.GetControllerOf(&p) == nil {
			im.add(ImpactDanger, "Pod", p.Namespace, p.Name, "Pod %s/%s has no controller: evicting it deletes it for good", p.Namespace, p.Name)
		}
	}
	im.localData(evicted)
	// Disruption budgets that cannot let all of their pods here go.
	byNS := map[string][]corev1.Pod{}
	for _, p := range evicted {
		byNS[p.Namespace] = append(byNS[p.Namespace], p)
	}
	for ns, pods := range byNS {
		pdbs, err := im.cs.PolicyV1().PodDisruptionBudgets(ns).List(im.ctx, metav1.ListOptions{})
		if err != nil {
			continue
		}
		for _, b := range pdbs.Items {
			if n := podsOfBudget(&b, pods); n > int(b.Status.DisruptionsAllowed) {
				im.add(ImpactDanger, "PodDisruptionBudget", ns, b.Name, "Disruption budget %s/%s allows %d disruption%s, and %d of its pods are here: the drain will stop at them", ns, b.Name, b.Status.DisruptionsAllowed, plural(int(b.Status.DisruptionsAllowed)), n)
			}
		}
	}
	// Do the evicted pods fit on the other nodes? First fit, largest first,
	// by requests alone: a hint, not the scheduler.
	room, err := im.freeCapacity(node)
	if err != nil {
		return err
	}
	if len(room) == 0 && len(evicted) > 0 {
		im.add(ImpactDanger, "", "", "", "No other node is ready and schedulable: the evicted pods stay pending")
		return nil
	}
	sort.Slice(evicted, func(i, j int) bool {
		ci, _ := requestsOf(&evicted[i])
		cj, _ := requestsOf(&evicted[j])
		return ci > cj
	})
	var homeless []string
	for _, p := range evicted {
		c, m := requestsOf(&p)
		placed := false
		for i := range room {
			if room[i].cpu >= c && room[i].mem >= m && fitsSelector(&p, room[i].name, im) {
				room[i].cpu -= c
				room[i].mem -= m
				placed = true
				break
			}
		}
		if !placed {
			homeless = append(homeless, p.Namespace+"/"+p.Name)
		}
	}
	if len(homeless) > 0 {
		im.add(ImpactDanger, "", "", "", "%d pod%s may not fit on the other nodes and stay pending: %s", len(homeless), plural(len(homeless)), strings.Join(limitList(homeless, 5), ", "))
	} else if len(evicted) > 0 {
		im.add(ImpactInfo, "", "", "", "The other %d node%s have room for them, by their requests", len(room), plural(len(room)))
	}
	return nil
}

// fitsSelector checks a pod's node selector against a node's labels.
func fitsSelector(p *corev1.Pod, node string, im *impact) bool {
	if len(p.Spec.NodeSelector) == 0 {
		return true
	}
	n, err := im.cs.CoreV1().Nodes().Get(im.ctx, node, metav1.GetOptions{})
	if err != nil {
		return true
	}
	return labels.SelectorFromSet(p.Spec.NodeSelector).Matches(labels.Set(n.Labels))
}

func (im *impact) restart(req ImpactRequest) error {
	switch req.Kind {
	case "Deployment":
		d, err := im.cs.AppsV1().Deployments(req.Namespace).Get(im.ctx, req.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if d.Spec.Strategy.Type == appsv1.RecreateDeploymentStrategyType {
			im.add(ImpactDanger, "", "", "", "Its strategy is Recreate: all pods stop before new ones start, so it is down for a while")
		} else {
			im.add(ImpactInfo, "", "", "", "Pods are replaced a few at a time; the old ones stop once new ones are ready")
		}
	case "StatefulSet":
		s, err := im.cs.AppsV1().StatefulSets(req.Namespace).Get(im.ctx, req.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if ptrInt32(s.Spec.Replicas) == 1 {
			im.add(ImpactWarning, "", "", "", "It has a single replica: it is down while its pod restarts")
		} else {
			im.add(ImpactInfo, "", "", "", "Pods restart one at a time, from the last one")
		}
	case "DaemonSet":
		im.add(ImpactInfo, "", "", "", "The pod of each node restarts in turn")
	}
	return nil
}
