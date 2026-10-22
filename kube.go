package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo"
	batchv1 "k8s.io/api/batch/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/yaml"
)

// Object is a Kubernetes object as the API returns it.
type Object = map[string]any

// GVR names a kind of resource: its API group, version and plural name.
type GVR struct {
	Group    string `json:"group"`
	Version  string `json:"version"`
	Resource string `json:"resource"`
}

func (g GVR) schema() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: g.Group, Version: g.Version, Resource: g.Resource}
}

// WatchEventType is the kind of a WatchEvent.
type WatchEventType string

const (
	// WatchSync replaces the whole list with Items.
	WatchSync     WatchEventType = "SYNC"
	WatchAdded    WatchEventType = "ADDED"
	WatchModified WatchEventType = "MODIFIED"
	WatchDeleted  WatchEventType = "DELETED"
	// WatchError reports a failure; the watch retries by itself.
	WatchError WatchEventType = "ERROR"
)

// WatchEvent is a change of a watched list.
type WatchEvent struct {
	Type   WatchEventType `json:"type"`
	Object Object         `json:"object,omitempty"`
	Items  []Object       `json:"items,omitempty"`
	Error  string         `json:"error,omitempty"`
}

// APIResource is a kind of resource the cluster serves.
type APIResource struct {
	Group      string   `json:"group"`
	Version    string   `json:"version"`
	Resource   string   `json:"resource"`
	Kind       string   `json:"kind"`
	Namespaced bool     `json:"namespaced"`
	Verbs      []string `json:"verbs"`
	ShortNames []string `json:"shortNames,omitempty"`
}

// Kube reads and changes the resources of the clusters.
type Kube struct {
	clusters *Clusters
	timeline *Timeline
}

func (k *Kube) resource(cluster string, gvr GVR, namespace string) (dynamic.ResourceInterface, error) {
	cn, err := k.clusters.conn(cluster)
	if err != nil {
		return nil, err
	}
	r := cn.dyn.Resource(gvr.schema())
	if namespace != "" {
		return r.Namespace(namespace), nil
	}
	return r, nil
}

// trim drops what the UI never shows, to keep messages small.
func trim(o *unstructured.Unstructured) Object {
	unstructured.RemoveNestedField(o.Object, "metadata", "managedFields")
	return o.Object
}

// List returns the objects of a resource, in a namespace or, with "", in
// all of them.
func (k *Kube) List(ctx context.Context, cluster string, gvr GVR, namespace string) ([]Object, error) {
	r, err := k.resource(cluster, gvr, namespace)
	if err != nil {
		return nil, err
	}
	list, err := r.List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, errors.New(friendlyError(err))
	}
	out := make([]Object, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, trim(&list.Items[i]))
	}
	return out, nil
}

// Watch streams a list of objects and its changes until the page stops
// listening: a SYNC event with every object first, and again whenever the
// watch had to start over, then each change.
func (k *Kube) Watch(ctx context.Context, cluster string, gvr GVR, namespace string, events *mygo.Channel[WatchEvent]) error {
	r, err := k.resource(cluster, gvr, namespace)
	if err != nil {
		return err
	}
	backoff := time.Second
	for {
		list, err := r.List(ctx, metav1.ListOptions{})
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			if events.Send(WatchEvent{Type: WatchError, Error: friendlyError(err)}) != nil {
				return nil
			}
			if !sleep(ctx, backoff) {
				return nil
			}
			backoff = min(backoff*2, 30*time.Second)
			continue
		}
		backoff = time.Second
		items := make([]Object, 0, len(list.Items))
		for i := range list.Items {
			items = append(items, trim(&list.Items[i]))
		}
		if events.Send(WatchEvent{Type: WatchSync, Items: items}) != nil {
			return nil
		}
		rv := list.GetResourceVersion()
		for {
			relist, stop := k.watchFrom(ctx, r, &rv, events)
			if stop {
				return nil
			}
			if relist {
				break
			}
			if !sleep(ctx, time.Second) {
				return nil
			}
		}
	}
}

// watchFrom watches from resource version *rv until the watch ends. It
// reports whether the list must be read again, and whether to stop.
func (k *Kube) watchFrom(ctx context.Context, r dynamic.ResourceInterface, rv *string, events *mygo.Channel[WatchEvent]) (relist, stop bool) {
	w, err := r.Watch(ctx, metav1.ListOptions{ResourceVersion: *rv, AllowWatchBookmarks: true})
	if ctx.Err() != nil {
		return false, true
	}
	if err != nil {
		return true, !sleep(ctx, 2*time.Second)
	}
	defer w.Stop()
	for {
		select {
		case <-ctx.Done():
			return false, true
		case ev, ok := <-w.ResultChan():
			if !ok {
				return false, false
			}
			switch ev.Type {
			case watch.Error:
				if status, ok := ev.Object.(*metav1.Status); ok && status.Code == 410 {
					return true, false
				}
				return true, false
			case watch.Bookmark:
				if o, ok := ev.Object.(*unstructured.Unstructured); ok {
					*rv = o.GetResourceVersion()
				}
			case watch.Added, watch.Modified, watch.Deleted:
				o, ok := ev.Object.(*unstructured.Unstructured)
				if !ok {
					continue
				}
				*rv = o.GetResourceVersion()
				if events.Send(WatchEvent{Type: WatchEventType(ev.Type), Object: trim(o)}) != nil {
					return false, true
				}
			}
		}
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// Get returns one object.
func (k *Kube) Get(ctx context.Context, cluster string, gvr GVR, namespace, name string) (Object, error) {
	r, err := k.resource(cluster, gvr, namespace)
	if err != nil {
		return nil, err
	}
	o, err := r.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return trim(o), nil
}

// GetYAML returns one object as YAML, without its managed fields.
func (k *Kube) GetYAML(ctx context.Context, cluster string, gvr GVR, namespace, name string) (string, error) {
	o, err := k.Get(ctx, cluster, gvr, namespace, name)
	if err != nil {
		return "", err
	}
	b, err := yaml.Marshal(o)
	return string(b), err
}

// Apply creates the objects of a YAML document, or replaces those that
// exist, like kubectl apply without its merge. Objects without a namespace
// go into namespace when their kind has namespaces. It returns what it did
// to each object.
func (k *Kube) Apply(ctx context.Context, cluster, manifest, namespace string) ([]string, error) {
	if err := k.clusters.writable(cluster); err != nil {
		return nil, err
	}
	cn, err := k.clusters.conn(cluster)
	if err != nil {
		return nil, err
	}
	dec := utilyaml.NewYAMLOrJSONDecoder(strings.NewReader(manifest), 4096)
	var results []string
	for {
		var raw map[string]any
		if err := dec.Decode(&raw); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return results, fmt.Errorf("invalid YAML: %w", err)
		}
		if len(raw) == 0 {
			continue
		}
		obj := &unstructured.Unstructured{Object: raw}
		gvk := obj.GroupVersionKind()
		if gvk.Kind == "" || gvk.Version == "" {
			return results, errors.New("every object needs apiVersion and kind")
		}
		if obj.GetKind() == "List" {
			return results, errors.New("lists are not supported: put each object in its own document")
		}
		mapping, err := cn.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			cn.mapper.Reset()
			if mapping, err = cn.mapper.RESTMapping(gvk.GroupKind(), gvk.Version); err != nil {
				return results, err
			}
		}
		var r dynamic.ResourceInterface = cn.dyn.Resource(mapping.Resource)
		if mapping.Scope.Name() == "namespace" {
			if obj.GetNamespace() == "" {
				ns := namespace
				if ns == "" {
					ns = "default"
				}
				obj.SetNamespace(ns)
			}
			r = cn.dyn.Resource(mapping.Resource).Namespace(obj.GetNamespace())
		}
		label := strings.ToLower(gvk.Kind) + "/" + obj.GetName()
		existing, err := r.Get(ctx, obj.GetName(), metav1.GetOptions{})
		switch {
		case apierrors.IsNotFound(err):
			if _, err := r.Create(ctx, obj, metav1.CreateOptions{FieldManager: "k8s-client"}); err != nil {
				return results, fmt.Errorf("%s: %w", label, err)
			}
			results = append(results, label+" created")
		case err != nil:
			return results, fmt.Errorf("%s: %w", label, err)
		default:
			if obj.GetResourceVersion() == "" {
				obj.SetResourceVersion(existing.GetResourceVersion())
			}
			if _, err := r.Update(ctx, obj, metav1.UpdateOptions{FieldManager: "k8s-client"}); err != nil {
				return results, fmt.Errorf("%s: %w", label, err)
			}
			results = append(results, label+" configured")
		}
	}
	if len(results) == 0 {
		return nil, errors.New("the document has no objects")
	}
	return results, nil
}

// Delete deletes an object, and what it owns in the background.
func (k *Kube) Delete(ctx context.Context, cluster string, gvr GVR, namespace, name string, force bool) error {
	if err := k.clusters.writable(cluster); err != nil {
		return err
	}
	r, err := k.resource(cluster, gvr, namespace)
	if err != nil {
		return err
	}
	policy := metav1.DeletePropagationBackground
	opts := metav1.DeleteOptions{PropagationPolicy: &policy}
	if force {
		var zero int64
		opts.GracePeriodSeconds = &zero
	}
	return r.Delete(ctx, name, opts)
}

func (k *Kube) patch(ctx context.Context, cluster string, gvr GVR, namespace, name string, patch any) error {
	if err := k.clusters.writable(cluster); err != nil {
		return err
	}
	r, err := k.resource(cluster, gvr, namespace)
	if err != nil {
		return err
	}
	b, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	_, err = r.Patch(ctx, name, types.MergePatchType, b, metav1.PatchOptions{FieldManager: "k8s-client"})
	return err
}

// Scale sets the replicas of a deployment, stateful set or replica set.
func (k *Kube) Scale(ctx context.Context, cluster string, gvr GVR, namespace, name string, replicas int) error {
	if replicas < 0 {
		return errors.New("replicas cannot be negative")
	}
	return k.patch(ctx, cluster, gvr, namespace, name, map[string]any{"spec": map[string]any{"replicas": replicas}})
}

// Restart restarts the pods of a deployment, stateful set or daemon set one
// by one, like kubectl rollout restart.
func (k *Kube) Restart(ctx context.Context, cluster string, gvr GVR, namespace, name string) error {
	return k.patch(ctx, cluster, gvr, namespace, name, map[string]any{
		"spec": map[string]any{"template": map[string]any{"metadata": map[string]any{"annotations": map[string]any{
			"kubectl.kubernetes.io/restartedAt": time.Now().Format(time.RFC3339),
		}}}},
	})
}

// SetSuspended suspends or resumes a cron job.
func (k *Kube) SetSuspended(ctx context.Context, cluster, namespace, name string, suspend bool) error {
	return k.patch(ctx, cluster, GVR{Group: "batch", Version: "v1", Resource: "cronjobs"}, namespace, name,
		map[string]any{"spec": map[string]any{"suspend": suspend}})
}

// TriggerCronJob runs a cron job now, in a job of its template, and returns
// the job's name.
func (k *Kube) TriggerCronJob(ctx context.Context, cluster, namespace, name string) (string, error) {
	if err := k.clusters.writable(cluster); err != nil {
		return "", err
	}
	cn, err := k.clusters.conn(cluster)
	if err != nil {
		return "", err
	}
	cj, err := cn.cs.BatchV1().CronJobs(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	jobName := fmt.Sprintf("%s-manual-%d", name, time.Now().Unix()%100000)
	if len(jobName) > 63 {
		jobName = jobName[len(jobName)-63:]
	}
	tmpl := cj.Spec.JobTemplate.DeepCopy()
	annotations := map[string]string{"cronjob.kubernetes.io/instantiate": "manual"}
	for k, v := range tmpl.Annotations {
		annotations[k] = v
	}
	controller := true
	newJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:        jobName,
			Namespace:   namespace,
			Labels:      tmpl.Labels,
			Annotations: annotations,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "batch/v1", Kind: "CronJob", Name: cj.Name, UID: cj.UID, Controller: &controller,
			}},
		},
		Spec: tmpl.Spec,
	}
	if _, err := cn.cs.BatchV1().Jobs(namespace).Create(ctx, newJob, metav1.CreateOptions{FieldManager: "k8s-client"}); err != nil {
		return "", err
	}
	return jobName, nil
}

// Cordon marks a node unschedulable, or schedulable again.
func (k *Kube) Cordon(ctx context.Context, cluster, node string, unschedulable bool) error {
	return k.patch(ctx, cluster, GVR{Version: "v1", Resource: "nodes"}, "", node,
		map[string]any{"spec": map[string]any{"unschedulable": unschedulable}})
}

// Drain cordons a node and evicts its pods, but those of daemon sets and
// mirror pods, respecting disruption budgets. It returns the pods evicted.
func (k *Kube) Drain(ctx context.Context, cluster, node string) ([]string, error) {
	if err := k.clusters.writable(cluster); err != nil {
		return nil, err
	}
	cn, err := k.clusters.conn(cluster)
	if err != nil {
		return nil, err
	}
	if err := k.Cordon(ctx, cluster, node, true); err != nil {
		return nil, err
	}
	pods, err := cn.cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{FieldSelector: "spec.nodeName=" + node})
	if err != nil {
		return nil, err
	}
	var evicted []string
	var failed []string
	for _, p := range pods.Items {
		if _, mirror := p.Annotations["kubernetes.io/config.mirror"]; mirror {
			continue
		}
		if ref := metav1.GetControllerOf(&p); ref != nil && ref.Kind == "DaemonSet" {
			continue
		}
		if p.Status.Phase == "Succeeded" || p.Status.Phase == "Failed" {
			continue
		}
		err := cn.cs.PolicyV1().Evictions(p.Namespace).Evict(ctx, &policyv1.Eviction{
			ObjectMeta: metav1.ObjectMeta{Name: p.Name, Namespace: p.Namespace},
		})
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s/%s: %v", p.Namespace, p.Name, err))
			continue
		}
		evicted = append(evicted, p.Namespace+"/"+p.Name)
	}
	if len(failed) > 0 {
		return evicted, errors.New("some pods could not be evicted:\n" + strings.Join(failed, "\n"))
	}
	return evicted, nil
}

// Resources returns the kinds of resources the cluster serves, in their
// preferred versions.
func (k *Kube) Resources(ctx context.Context, cluster string) ([]APIResource, error) {
	cn, err := k.clusters.conn(cluster)
	if err != nil {
		return nil, err
	}
	cn.disc.Invalidate()
	lists, err := cn.disc.ServerPreferredResources()
	if err != nil && len(lists) == 0 {
		return nil, errors.New(friendlyError(err))
	}
	var out []APIResource
	for _, l := range lists {
		gv, err := schema.ParseGroupVersion(l.GroupVersion)
		if err != nil {
			continue
		}
		for _, r := range l.APIResources {
			if strings.Contains(r.Name, "/") {
				continue // subresources
			}
			out = append(out, APIResource{
				Group: gv.Group, Version: gv.Version, Resource: r.Name, Kind: r.Kind,
				Namespaced: r.Namespaced, Verbs: r.Verbs, ShortNames: r.ShortNames,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		return out[i].Kind < out[j].Kind
	})
	return out, nil
}

// ClusterInfo describes a cluster's API server.
type ClusterInfo struct {
	Version string `json:"version"`
	Server  string `json:"server"`
}

// Info returns the version and address of a cluster.
func (k *Kube) Info(ctx context.Context, cluster string) (ClusterInfo, error) {
	cn, err := k.clusters.conn(cluster)
	if err != nil {
		return ClusterInfo{}, err
	}
	v, err := serverVersion(ctx, cn)
	if err != nil {
		return ClusterInfo{}, errors.New(friendlyError(err))
	}
	return ClusterInfo{Version: v, Server: cn.cfg.Host}, nil
}

// ToYAML formats an object as YAML, for templates the page builds.
func (k *Kube) ToYAML(o Object) (string, error) {
	b, err := yaml.Marshal(o)
	return string(bytes.TrimSpace(b)) + "\n", err
}
