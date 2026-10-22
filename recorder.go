package main

import (
	"fmt"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
)

// recorder turns the informer events of a watched cluster into changes of
// the timeline.
type recorder struct {
	timeline *Timeline
	cluster  string
	scopes   []string // the namespaces watched; "" is all of them

	mu     sync.Mutex
	synced bool
	seen   map[string]bool
}

func newRecorder(t *Timeline, cluster string, scopes []string) *recorder {
	return &recorder{timeline: t, cluster: cluster, scopes: scopes, seen: map[string]bool{}}
}

func (rc *recorder) isSynced() bool {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return rc.synced
}

// watch records the changes of the objects of an informer.
func (rc *recorder) watch(inf cache.SharedIndexInformer) {
	_, _ = inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj any) { rc.added(obj) },
		UpdateFunc: func(oldObj, newObj any) { rc.updated(oldObj, newObj) },
		DeleteFunc: func(obj any) {
			if d, ok := obj.(cache.DeletedFinalStateUnknown); ok {
				obj = d.Obj
			}
			rc.deleted(obj)
		},
	})
}

func (rc *recorder) added(obj any) {
	if ev, ok := obj.(*corev1.Event); ok {
		if rc.isSynced() && ev.Type == corev1.EventTypeWarning {
			rc.timeline.add(eventChange(rc.cluster, ev))
		}
		return
	}
	if p, ok := obj.(*corev1.Pod); ok {
		// Pods of controllers come and go with their workloads; only pods
		// made by hand are worth a line when they appear.
		if rc.isSynced() && metav1.GetControllerOf(p) == nil {
			rc.timeline.add(Change{Cluster: rc.cluster, Kind: "Pod", Namespace: p.Namespace, Name: p.Name, Type: ChangeCreated, Summary: []string{"created"}, Actor: actorOf(p)})
		}
		return
	}
	tr, ok := trackedOf(obj)
	if !ok {
		return
	}
	key := objKey(tr.kind, tr.meta)
	hash, spec := specOf(tr)
	if rc.isSynced() {
		rc.timeline.add(Change{Cluster: rc.cluster, Kind: tr.kind, Namespace: tr.meta.GetNamespace(), Name: tr.meta.GetName(), Type: ChangeCreated, Summary: []string{"created"}, Actor: actorOf(tr.meta), After: spec})
	} else {
		// The first list of a run: compare with what the last run saw.
		rc.mu.Lock()
		rc.seen[key] = true
		rc.mu.Unlock()
		prev, had := rc.timeline.lookup(rc.cluster, key)
		switch {
		case had && prev.Hash != hash:
			rc.timeline.add(Change{
				Cluster: rc.cluster, Kind: tr.kind, Namespace: tr.meta.GetNamespace(), Name: tr.meta.GetName(),
				Type: ChangeUpdated, Offline: true, Actor: actorOf(tr.meta), Before: prev.Spec, After: spec,
				Time:    changedAt(tr.meta),
				Summary: []string{"changed while Kube Desk was closed"},
			})
		case !had && rc.hasBaseline(tr.meta.GetNamespace()):
			rc.timeline.add(Change{
				Cluster: rc.cluster, Kind: tr.kind, Namespace: tr.meta.GetNamespace(), Name: tr.meta.GetName(),
				Type: ChangeCreated, Offline: true, Actor: actorOf(tr.meta), After: spec,
				Time:    tr.meta.GetCreationTimestamp().Time,
				Summary: []string{"created while Kube Desk was closed"},
			})
		}
	}
	rc.timeline.remember(rc.cluster, key, &snapshot{Hash: hash, Spec: spec})
}

func (rc *recorder) updated(oldObj, newObj any) {
	switch n := newObj.(type) {
	case *corev1.Pod:
		if o, ok := oldObj.(*corev1.Pod); ok {
			rc.timeline.add(podChanges(rc.cluster, o, n)...)
		}
		return
	case *corev1.Node:
		if o, ok := oldObj.(*corev1.Node); ok {
			rc.timeline.add(nodeChanges(rc.cluster, o, n)...)
		}
		return
	case *corev1.Event:
		return
	}
	if c := specChange(rc.cluster, oldObj, newObj); c != nil {
		rc.timeline.add(*c)
	}
	if tr, ok := trackedOf(newObj); ok {
		hash, spec := specOf(tr)
		rc.timeline.remember(rc.cluster, objKey(tr.kind, tr.meta), &snapshot{Hash: hash, Spec: spec})
	}
}

func (rc *recorder) deleted(obj any) {
	switch o := obj.(type) {
	case *corev1.Event:
		return
	case *corev1.Pod:
		if metav1.GetControllerOf(o) == nil {
			rc.timeline.add(Change{Cluster: rc.cluster, Kind: "Pod", Namespace: o.Namespace, Name: o.Name, Type: ChangeDeleted, Summary: []string{"deleted"}})
		}
		return
	case *corev1.Node:
		rc.timeline.add(Change{Cluster: rc.cluster, Kind: "Node", Name: o.Name, Type: ChangeDeleted, Summary: []string{"removed from the cluster"}, Warning: true})
		return
	}
	tr, ok := trackedOf(obj)
	if !ok {
		return
	}
	_, spec := specOf(tr)
	rc.timeline.add(Change{Cluster: rc.cluster, Kind: tr.kind, Namespace: tr.meta.GetNamespace(), Name: tr.meta.GetName(), Type: ChangeDeleted, Summary: []string{"deleted"}, Before: spec})
	rc.timeline.remember(rc.cluster, objKey(tr.kind, tr.meta), nil)
}

// hasBaseline reports whether an earlier run saw a namespace, so that
// objects it lacks were created since.
func (rc *recorder) hasBaseline(ns string) bool {
	_, all := rc.timeline.lookup(rc.cluster, baselineKey(""))
	_, one := rc.timeline.lookup(rc.cluster, baselineKey(ns))
	return all || one
}

func baselineKey(ns string) string {
	return "@scope/" + ns
}

// markSynced ends the first list: objects the last run saw that are gone
// were deleted while the app was closed. It then remembers the scopes.
func (rc *recorder) markSynced() {
	rc.mu.Lock()
	rc.synced = true
	seen := rc.seen
	rc.seen = nil
	rc.mu.Unlock()
	rc.timeline.loadSnapshots(rc.cluster)
	inScope := func(ns string) bool {
		for _, s := range rc.scopes {
			if s == "" || s == ns {
				return true
			}
		}
		return false
	}
	var gone []Change
	var forget []string
	rc.timeline.mu.Lock()
	snaps := rc.timeline.snapshots[rc.cluster]
	for key, prev := range snaps {
		if strings.HasPrefix(key, "@") {
			continue
		}
		parts := strings.SplitN(key, "/", 3)
		if len(parts) != 3 || !inScope(parts[1]) || seen[key] || !rc.hasBaselineLocked(snaps, parts[1]) {
			continue
		}
		gone = append(gone, Change{
			Cluster: rc.cluster, Kind: parts[0], Namespace: parts[1], Name: parts[2],
			Type: ChangeDeleted, Offline: true, Before: prev.Spec,
			Summary: []string{"deleted while Kube Desk was closed"},
		})
		forget = append(forget, key)
	}
	rc.timeline.mu.Unlock()
	rc.timeline.add(gone...)
	for _, key := range forget {
		rc.timeline.remember(rc.cluster, key, nil)
	}
	for _, s := range rc.scopes {
		rc.timeline.remember(rc.cluster, baselineKey(s), &snapshot{Hash: time.Now().Format(time.RFC3339)})
	}
}

func (rc *recorder) hasBaselineLocked(snaps map[string]snapshot, ns string) bool {
	_, all := snaps[baselineKey("")]
	_, one := snaps[baselineKey(ns)]
	return all || one
}

// changedAt is when an object was last changed, from its managed fields.
func changedAt(m metav1.Object) time.Time {
	var t time.Time
	for _, f := range m.GetManagedFields() {
		if f.Time != nil && f.Subresource != "status" && f.Time.After(t) {
			t = f.Time.Time
		}
	}
	if t.IsZero() {
		return time.Now()
	}
	return t
}

// eventChange records a warning event of the cluster.
func eventChange(cluster string, ev *corev1.Event) Change {
	t := ev.LastTimestamp.Time
	if t.IsZero() {
		t = ev.EventTime.Time
	}
	if t.IsZero() || time.Since(t) > time.Hour {
		t = time.Now()
	}
	msg := ev.Reason
	if ev.Message != "" {
		msg = fmt.Sprintf("%s: %s", ev.Reason, truncate(ev.Message, 240))
	}
	return Change{
		Cluster: cluster, Kind: ev.InvolvedObject.Kind, Namespace: ev.InvolvedObject.Namespace, Name: ev.InvolvedObject.Name,
		Type: ChangeEvent, Warning: true, Summary: []string{msg}, Time: t, Actor: ev.Source.Component,
	}
}
