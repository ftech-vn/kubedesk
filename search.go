package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// SearchHit is an object that matches a search, in one cluster.
type SearchHit struct {
	Cluster     string `json:"cluster"`
	ClusterName string `json:"clusterName"`
	GVR         GVR    `json:"gvr"`
	Kind        string `json:"kind"`
	Namespace   string `json:"namespace"`
	Name        string `json:"name"`
	// Detail is a short state: a pod's status, a workload's images.
	Detail string `json:"detail,omitempty"`
	// Error is set, alone, when a cluster could not be searched.
	Error string `json:"error,omitempty"`
}

// SearchQuery is a search across clusters.
type SearchQuery struct {
	// Text holds words that match names, and filters: image:nginx,
	// label:app=web (or app=web), kind:pod, ns:shop, node:n1,
	// is:failing, status:CrashLoopBackOff.
	Text string `json:"text"`
	// Clusters to search; none is every cluster shown.
	Clusters []string `json:"clusters,omitempty"`
}

// searchKinds are the kinds a search reads, with the names people use.
var searchKinds = []struct {
	gvr     GVR
	kind    string
	aliases []string
}{
	{GVR{Version: "v1", Resource: "pods"}, "Pod", []string{"pod", "pods", "po"}},
	{GVR{Group: "apps", Version: "v1", Resource: "deployments"}, "Deployment", []string{"deployment", "deployments", "deploy"}},
	{GVR{Group: "apps", Version: "v1", Resource: "statefulsets"}, "StatefulSet", []string{"statefulset", "statefulsets", "sts"}},
	{GVR{Group: "apps", Version: "v1", Resource: "daemonsets"}, "DaemonSet", []string{"daemonset", "daemonsets", "ds"}},
	{GVR{Group: "batch", Version: "v1", Resource: "cronjobs"}, "CronJob", []string{"cronjob", "cronjobs", "cj"}},
	{GVR{Version: "v1", Resource: "services"}, "Service", []string{"service", "services", "svc"}},
	{GVR{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}, "Ingress", []string{"ingress", "ingresses", "ing"}},
	{GVR{Version: "v1", Resource: "configmaps"}, "ConfigMap", []string{"configmap", "configmaps", "cm"}},
	{GVR{Version: "v1", Resource: "nodes"}, "Node", []string{"node", "nodes", "no"}},
	{GVR{Version: "v1", Resource: "namespaces"}, "Namespace", []string{"namespace", "namespaces", "ns"}},
}

type parsedQuery struct {
	words, images, kinds []string
	labels               map[string]string
	namespace, node      string
	status               string
	failing              bool
}

func parseQuery(text string) parsedQuery {
	q := parsedQuery{labels: map[string]string{}}
	for _, f := range strings.Fields(text) {
		key, val, ok := strings.Cut(f, ":")
		if !ok {
			if k, v, eq := strings.Cut(f, "="); eq && k != "" {
				q.labels[k] = v
				continue
			}
			q.words = append(q.words, strings.ToLower(f))
			continue
		}
		switch strings.ToLower(key) {
		case "image", "img":
			q.images = append(q.images, strings.ToLower(val))
		case "kind", "k":
			q.kinds = append(q.kinds, strings.ToLower(val))
		case "label", "l":
			k, v, _ := strings.Cut(val, "=")
			q.labels[k] = v
		case "ns", "namespace":
			q.namespace = val
		case "node":
			q.node = strings.ToLower(val)
		case "is":
			q.failing = q.failing || val == "failing" || val == "broken" || val == "unhealthy"
			if val == "crashloop" || val == "crashing" {
				q.status = "crashloop"
			}
		case "status":
			q.status = strings.ToLower(val)
		default:
			q.words = append(q.words, strings.ToLower(f))
		}
	}
	return q
}

func (q parsedQuery) wantsKind(kind string, aliases []string) bool {
	if len(q.kinds) == 0 {
		// Filters of pods, such as status and node, limit the search to them.
		if q.failing || q.status != "" || q.node != "" {
			return kind == "Pod"
		}
		if len(q.images) > 0 {
			return kind == "Pod" || kind == "Deployment" || kind == "StatefulSet" || kind == "DaemonSet" || kind == "CronJob"
		}
		return true
	}
	for _, k := range q.kinds {
		if slices.Contains(aliases, k) || strings.EqualFold(k, kind) {
			return true
		}
	}
	return false
}

// searchCache keeps the lists of a cluster for a moment, so that refining a
// search does not read them again.
type searchCache struct {
	mu    sync.Mutex
	lists map[string]cachedList
}

type cachedList struct {
	at    time.Time
	items []unstructured.Unstructured
}

var searches = &searchCache{lists: map[string]cachedList{}}

func (sc *searchCache) list(ctx context.Context, k *Kube, cluster string, gvr GVR) ([]unstructured.Unstructured, error) {
	key := cluster + "|" + gvr.Group + "/" + gvr.Resource
	sc.mu.Lock()
	c, ok := sc.lists[key]
	sc.mu.Unlock()
	if ok && time.Since(c.at) < 30*time.Second {
		return c.items, nil
	}
	r, err := k.resource(cluster, gvr, "")
	if err != nil {
		return nil, err
	}
	l, err := r.List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	sc.mu.Lock()
	sc.lists[key] = cachedList{at: time.Now(), items: l.Items}
	sc.mu.Unlock()
	return l.Items, nil
}

// Search looks for objects in every cluster at once, and sends the hits of
// each cluster as it answers.
func (k *Kube) Search(ctx context.Context, q SearchQuery, hits *mygo.Channel[[]SearchHit]) error {
	return k.search(ctx, q, hits.Send)
}

func (k *Kube) search(ctx context.Context, q SearchQuery, send func([]SearchHit) error) error {
	ids := q.Clusters
	if len(ids) == 0 {
		for _, c := range k.clusters.List().Clusters {
			if !c.Hidden {
				ids = append(ids, c.ID)
			}
		}
	}
	pq := parseQuery(q.Text)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			found := k.searchCluster(ctx, id, pq)
			mu.Lock()
			defer mu.Unlock()
			if len(found) > 0 && ctx.Err() == nil {
				_ = send(found)
			}
		}(id)
	}
	wg.Wait()
	return nil
}

const maxHitsPerCluster = 300

func (k *Kube) searchCluster(ctx context.Context, id string, q parsedQuery) []SearchHit {
	name := k.clusters.name(id)
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var hits []SearchHit
	var firstErr error
	for _, sk := range searchKinds {
		if !q.wantsKind(sk.kind, sk.aliases) {
			continue
		}
		items, err := searches.list(ctx, k, id, sk.gvr)
		if err != nil {
			if firstErr == nil && !strings.Contains(err.Error(), "forbidden") {
				firstErr = err
			}
			if ctx.Err() != nil {
				break
			}
			continue
		}
		for i := range items {
			o := &items[i]
			if detail, ok := matchObject(sk.kind, o, q); ok {
				hits = append(hits, SearchHit{Cluster: id, ClusterName: name, GVR: sk.gvr, Kind: sk.kind, Namespace: o.GetNamespace(), Name: o.GetName(), Detail: detail})
				if len(hits) >= maxHitsPerCluster {
					return hits
				}
			}
		}
	}
	if len(hits) == 0 && firstErr != nil {
		return []SearchHit{{Cluster: id, ClusterName: name, Error: friendlyError(firstErr)}}
	}
	return hits
}

// matchObject tells whether an object matches, and what to show of it.
func matchObject(kind string, o *unstructured.Unstructured, q parsedQuery) (string, bool) {
	lname := strings.ToLower(o.GetName())
	for _, w := range q.words {
		if !strings.Contains(lname, w) && !strings.Contains(strings.ToLower(o.GetNamespace()), w) {
			return "", false
		}
	}
	if q.namespace != "" && o.GetNamespace() != q.namespace {
		return "", false
	}
	l := o.GetLabels()
	for k, v := range q.labels {
		if lv, ok := l[k]; !ok || (v != "" && lv != v) {
			return "", false
		}
	}
	images := imagesOf(kind, o.Object)
	for _, want := range q.images {
		if !slices.ContainsFunc(images, func(img string) bool { return strings.Contains(strings.ToLower(img), want) }) {
			return "", false
		}
	}
	detail := strings.Join(images, ", ")
	if kind == "Pod" {
		var p corev1.Pod
		if err := fromUnstructured(o.Object, &p); err != nil {
			return "", false
		}
		if q.node != "" && !strings.Contains(strings.ToLower(p.Spec.NodeName), q.node) {
			return "", false
		}
		reason, bad := podReason(&p)
		if q.failing && !bad {
			return "", false
		}
		if q.status != "" {
			r := strings.ToLower(reason)
			if q.status == "crashloop" {
				if r != "crashloopbackoff" {
					return "", false
				}
			} else if !strings.Contains(r, q.status) {
				return "", false
			}
		}
		detail = reason
		if p.Spec.NodeName != "" {
			detail += " · " + p.Spec.NodeName
		}
		if len(q.images) > 0 {
			detail += " · " + strings.Join(images, ", ")
		}
	}
	if kind == "Node" || kind == "Namespace" || kind == "Service" || kind == "ConfigMap" || kind == "Ingress" {
		detail = ""
	}
	return detail, true
}

// podReason is the status kubectl shows for a pod, and whether it is a
// problem.
func podReason(p *corev1.Pod) (string, bool) {
	if p.DeletionTimestamp != nil {
		return "Terminating", false
	}
	for _, cs := range append(append([]corev1.ContainerStatus{}, p.Status.InitContainerStatuses...), p.Status.ContainerStatuses...) {
		if w := cs.State.Waiting; w != nil && w.Reason != "" && w.Reason != "PodInitializing" {
			return w.Reason, badWaiting[w.Reason]
		}
		if t := cs.State.Terminated; t != nil && t.ExitCode != 0 && p.Status.Phase != corev1.PodSucceeded {
			return orDefault(t.Reason, fmt.Sprintf("ExitCode:%d", t.ExitCode)), true
		}
	}
	switch p.Status.Phase {
	case corev1.PodFailed:
		return orDefault(p.Status.Reason, "Failed"), true
	case corev1.PodRunning:
		if !ready(p) {
			return "Running, not ready", true
		}
	case corev1.PodPending:
		for _, c := range p.Status.Conditions {
			if c.Type == corev1.PodScheduled && c.Reason == "Unschedulable" {
				return "Unschedulable", true
			}
		}
	}
	return string(p.Status.Phase), false
}

// imagesOf lists the images of a pod or of the pods a workload makes.
func imagesOf(kind string, o map[string]any) []string {
	var path []string
	switch kind {
	case "Pod":
		path = []string{"spec"}
	case "Deployment", "StatefulSet", "DaemonSet":
		path = []string{"spec", "template", "spec"}
	case "CronJob":
		path = []string{"spec", "jobTemplate", "spec", "template", "spec"}
	default:
		return nil
	}
	var out []string
	for _, field := range []string{"initContainers", "containers"} {
		list, _, _ := unstructured.NestedSlice(o, append(slices.Clone(path), field)...)
		for _, c := range list {
			if m, ok := c.(map[string]any); ok {
				if img, ok := m["image"].(string); ok {
					out = append(out, img)
				}
			}
		}
	}
	return out
}
