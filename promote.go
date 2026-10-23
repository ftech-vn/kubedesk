package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// PromoteRequest copies an object from one cluster to another.
type PromoteRequest struct {
	From      string `json:"from"`
	To        string `json:"to"`
	GVR       GVR    `json:"gvr"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	// TargetNamespace is where it goes; "" keeps its namespace.
	TargetNamespace string `json:"targetNamespace,omitempty"`
	// Images replaces the images of containers, by container name.
	Images map[string]string `json:"images,omitempty"`
	// WithConfig copies the config maps its pods use; WithSecrets the
	// secrets too, values included.
	WithConfig  bool `json:"withConfig,omitempty"`
	WithSecrets bool `json:"withSecrets,omitempty"`
}

// PromoteContainer is a container of the object, and its image.
type PromoteContainer struct {
	Name  string `json:"name"`
	Image string `json:"image"`
}

// PromotePlan is what promoting would do in the target cluster.
type PromotePlan struct {
	Diffs      []ObjectDiff       `json:"diffs"`
	Containers []PromoteContainer `json:"containers"`
	// ConfigMaps and Secrets are what the pods use, which can come along.
	ConfigMaps []string `json:"configMaps"`
	Secrets    []string `json:"secrets"`
	Notes      []string `json:"notes"`
}

// promoteManager names promotions in the target cluster's records.
const promoteManager = "kubedesk-promote"

// promotable strips what a cluster fills in, so that an object can be
// created in another: status, identifiers, ownership, allocated addresses
// and controller bookkeeping.
func promotable(o map[string]any, namespace string) map[string]any {
	c := (&unstructured.Unstructured{Object: o}).DeepCopy().Object
	unstructured.RemoveNestedField(c, "status")
	for _, f := range []string{"managedFields", "resourceVersion", "uid", "creationTimestamp", "generation", "selfLink", "ownerReferences", "finalizers", "deletionTimestamp", "deletionGracePeriodSeconds", "generateName"} {
		unstructured.RemoveNestedField(c, "metadata", f)
	}
	if namespace != "" {
		_ = unstructured.SetNestedField(c, namespace, "metadata", "namespace")
	}
	if ann, ok, _ := unstructured.NestedStringMap(c, "metadata", "annotations"); ok {
		for k := range ann {
			if k == "kubectl.kubernetes.io/last-applied-configuration" || k == "deployment.kubernetes.io/revision" ||
				strings.HasPrefix(k, "pv.kubernetes.io/") || strings.HasPrefix(k, "volume.beta.kubernetes.io/") || strings.HasPrefix(k, "volume.kubernetes.io/") {
				delete(ann, k)
			}
		}
		if len(ann) == 0 {
			unstructured.RemoveNestedField(c, "metadata", "annotations")
		} else {
			_ = unstructured.SetNestedStringMap(c, ann, "metadata", "annotations")
		}
	}
	switch c["kind"] {
	case "Service":
		for _, f := range []string{"clusterIP", "clusterIPs", "healthCheckNodePort"} {
			unstructured.RemoveNestedField(c, "spec", f)
		}
		if ports, ok, _ := unstructured.NestedSlice(c, "spec", "ports"); ok {
			for _, p := range ports {
				if m, ok := p.(map[string]any); ok {
					delete(m, "nodePort")
				}
			}
			_ = unstructured.SetNestedSlice(c, ports, "spec", "ports")
		}
	case "PersistentVolumeClaim":
		unstructured.RemoveNestedField(c, "spec", "volumeName")
	case "Job":
		// The selector and its labels are made by the cluster.
		unstructured.RemoveNestedField(c, "spec", "selector")
		unstructured.RemoveNestedField(c, "spec", "manualSelector")
		for _, l := range []string{"controller-uid", "batch.kubernetes.io/controller-uid", "job-name", "batch.kubernetes.io/job-name"} {
			unstructured.RemoveNestedField(c, "spec", "template", "metadata", "labels", l)
		}
	}
	unstructured.RemoveNestedField(c, "spec", "template", "metadata", "labels", "pod-template-hash")
	unstructured.RemoveNestedField(c, "spec", "template", "metadata", "creationTimestamp")
	return c
}

// templateOf is the pod spec of an object that has one.
func templateOf(o map[string]any) (*corev1.PodSpec, []string) {
	paths := [][]string{{"spec", "template", "spec"}, {"spec", "jobTemplate", "spec", "template", "spec"}, {"spec"}}
	if o["kind"] != "Pod" {
		paths = paths[:2]
	} else {
		paths = paths[2:]
	}
	for _, p := range paths {
		m, ok, _ := unstructured.NestedMap(o, p...)
		if !ok {
			continue
		}
		var spec corev1.PodSpec
		if fromUnstructured(m, &spec) == nil && len(spec.Containers) > 0 {
			return &spec, p
		}
	}
	return nil, nil
}

// podSpecRefs are the config maps and secrets a pod spec uses.
func podSpecRefs(spec *corev1.PodSpec) (cms, secrets []string) {
	seen := map[string]bool{}
	add := func(list *[]string, kind, name string) {
		if name == "" || name == "kube-root-ca.crt" || seen[kind+name] {
			return
		}
		seen[kind+name] = true
		*list = append(*list, name)
	}
	for _, v := range spec.Volumes {
		if v.ConfigMap != nil {
			add(&cms, "cm", v.ConfigMap.Name)
		}
		if v.Secret != nil {
			add(&secrets, "s", v.Secret.SecretName)
		}
		if v.Projected != nil {
			for _, src := range v.Projected.Sources {
				if src.ConfigMap != nil {
					add(&cms, "cm", src.ConfigMap.Name)
				}
				if src.Secret != nil {
					add(&secrets, "s", src.Secret.Name)
				}
			}
		}
	}
	for _, c := range append(append([]corev1.Container{}, spec.InitContainers...), spec.Containers...) {
		for _, e := range c.EnvFrom {
			if e.ConfigMapRef != nil {
				add(&cms, "cm", e.ConfigMapRef.Name)
			}
			if e.SecretRef != nil {
				add(&secrets, "s", e.SecretRef.Name)
			}
		}
		for _, e := range c.Env {
			if e.ValueFrom == nil {
				continue
			}
			if e.ValueFrom.ConfigMapKeyRef != nil {
				add(&cms, "cm", e.ValueFrom.ConfigMapKeyRef.Name)
			}
			if e.ValueFrom.SecretKeyRef != nil {
				add(&secrets, "s", e.ValueFrom.SecretKeyRef.Name)
			}
		}
	}
	for _, s := range spec.ImagePullSecrets {
		add(&secrets, "s", s.Name)
	}
	sort.Strings(cms)
	sort.Strings(secrets)
	return cms, secrets
}

// promoteManifest reads the object, and what comes along, from the source
// cluster, and writes them as one document for the target.
func (k *Kube) promoteManifest(ctx context.Context, req PromoteRequest) (string, *PromotePlan, error) {
	if req.From == req.To {
		return "", nil, errors.New("choose another cluster to promote to")
	}
	src, err := k.resource(req.From, req.GVR, req.Namespace)
	if err != nil {
		return "", nil, err
	}
	o, err := src.Get(ctx, req.Name, metav1.GetOptions{})
	if err != nil {
		return "", nil, errors.New(friendlyError(err))
	}
	if o.GetKind() == "Secret" {
		if t, _, _ := unstructured.NestedString(o.Object, "type"); t == string(corev1.SecretTypeServiceAccountToken) {
			return "", nil, errors.New("service account tokens belong to their cluster: they cannot be promoted")
		}
	}
	target := req.TargetNamespace
	if target == "" {
		target = req.Namespace
	}
	plan := &PromotePlan{Diffs: []ObjectDiff{}, Containers: []PromoteContainer{}, ConfigMaps: []string{}, Secrets: []string{}, Notes: []string{}}
	main := promotable(o.Object, target)
	if spec, path := templateOf(main); spec != nil {
		for _, c := range spec.Containers {
			plan.Containers = append(plan.Containers, PromoteContainer{Name: c.Name, Image: c.Image})
		}
		// Images change in place, leaving the rest as the source has it.
		for _, field := range []string{"containers", "initContainers"} {
			list, ok, _ := unstructured.NestedSlice(main, append(path, field)...)
			if !ok {
				continue
			}
			for _, x := range list {
				if m, ok := x.(map[string]any); ok {
					if img := strings.TrimSpace(req.Images[fmt.Sprint(m["name"])]); img != "" {
						m["image"] = img
					}
				}
			}
			_ = unstructured.SetNestedSlice(main, list, append(path, field)...)
		}
		plan.ConfigMaps, plan.Secrets = podSpecRefs(spec)
	}

	docs := []map[string]any{}
	if req.Namespace != "" {
		if cn, err := k.clusters.conn(req.To); err == nil {
			if _, err := cn.cs.CoreV1().Namespaces().Get(ctx, target, metav1.GetOptions{}); apierrors.IsNotFound(err) {
				docs = append(docs, map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": target}})
				plan.Notes = append(plan.Notes, fmt.Sprintf("Namespace %s does not exist in the target cluster: it will be created", target))
			}
		}
	}
	bring := func(kind, resource string, names []string) {
		r, err := k.resource(req.From, GVR{Version: "v1", Resource: resource}, req.Namespace)
		if err != nil {
			return
		}
		for _, n := range names {
			x, err := r.Get(ctx, n, metav1.GetOptions{})
			if err != nil {
				plan.Notes = append(plan.Notes, fmt.Sprintf("%s %s is not in the source cluster", kind, n))
				continue
			}
			if t, _, _ := unstructured.NestedString(x.Object, "type"); t == string(corev1.SecretTypeServiceAccountToken) {
				continue
			}
			docs = append(docs, promotable(x.Object, target))
		}
	}
	if req.WithConfig {
		bring("ConfigMap", "configmaps", plan.ConfigMaps)
	}
	if req.WithSecrets {
		bring("Secret", "secrets", plan.Secrets)
	}
	docs = append(docs, main)
	var b strings.Builder
	for _, d := range docs {
		y, err := yaml.Marshal(d)
		if err != nil {
			return "", nil, err
		}
		b.WriteString("---\n")
		b.Write(y)
	}
	return b.String(), plan, nil
}

// PlanPromote works out, without changing anything, what copying an object
// to another cluster would do there. Secret values are masked.
func (k *Kube) PlanPromote(ctx context.Context, req PromoteRequest) (PromotePlan, error) {
	manifest, plan, err := k.promoteManifest(ctx, req)
	if err != nil {
		return PromotePlan{}, err
	}
	target := req.TargetNamespace
	if target == "" {
		target = req.Namespace
	}
	diffs, err := k.PlanApply(ctx, req.To, manifest, target)
	if err != nil {
		return *plan, errors.New(friendlyError(err))
	}
	plan.Diffs = diffs
	if !req.WithSecrets && len(plan.Secrets) > 0 {
		missing := []string{}
		if r, err := k.resource(req.To, GVR{Version: "v1", Resource: "secrets"}, target); err == nil {
			for _, s := range plan.Secrets {
				if _, err := r.Get(ctx, s, metav1.GetOptions{}); apierrors.IsNotFound(err) {
					missing = append(missing, s)
				}
			}
		}
		if len(missing) > 0 {
			plan.Notes = append(plan.Notes, "The target cluster lacks secrets the pods need: "+strings.Join(missing, ", "))
		}
	}
	return *plan, nil
}

// Promote copies an object, and the config chosen, to another cluster.
func (k *Kube) Promote(ctx context.Context, req PromoteRequest) ([]string, error) {
	if err := k.clusters.writable(req.To); err != nil {
		return nil, err
	}
	manifest, _, err := k.promoteManifest(ctx, req)
	if err != nil {
		return nil, err
	}
	target := req.TargetNamespace
	if target == "" {
		target = req.Namespace
	}
	return k.Apply(withManager(ctx, promoteManager), req.To, manifest, target)
}
