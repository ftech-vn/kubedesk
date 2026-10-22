package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

// ChangeType is what happened to an object.
type ChangeType string

const (
	ChangeCreated ChangeType = "created"
	ChangeUpdated ChangeType = "updated"
	ChangeDeleted ChangeType = "deleted"
	// ChangeStatus is a change of what an object is doing, not of what it
	// was asked to do: a pod that restarted, a node that became ready.
	ChangeStatus ChangeType = "status"
	// ChangeEvent is a warning event of the cluster.
	ChangeEvent ChangeType = "event"
)

// Change is something that happened in a watched namespace.
type Change struct {
	ID        string     `json:"id"`
	Time      time.Time  `json:"time"`
	Cluster   string     `json:"cluster"`
	Kind      string     `json:"kind"`
	Namespace string     `json:"namespace"`
	Name      string     `json:"name"`
	Type      ChangeType `json:"type"`
	// Summary says what changed, a line per change.
	Summary []string `json:"summary"`
	Warning bool     `json:"warning,omitempty"`
	// Actor is the field manager that made the change, such as kubectl,
	// helm or a controller, when the object says.
	Actor string `json:"actor,omitempty"`
	// Before and After are YAML of what the object asked for, for diffs.
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
	// Offline marks changes made while Kube Desk was not running, found
	// when it started again.
	Offline bool `json:"offline,omitempty"`
}

// TimelineQuery selects changes.
type TimelineQuery struct {
	Cluster   string `json:"cluster"`
	Namespace string `json:"namespace,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Name      string `json:"name,omitempty"`
	// Since is RFC 3339; "" is the last 24 hours.
	Since string `json:"since,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

// TimelineChanged tells pages that a cluster has new changes.
var TimelineChanged = mygo.NewEvent[string]("timeline:changed")

// timelineDays is how long changes are kept.
const timelineDays = 7

// Timeline keeps the changes of watched namespaces on disk, a JSON Lines
// file per cluster and day.
type Timeline struct {
	mu   sync.Mutex
	dir  string
	seq  int64
	tell func(cluster string)
	// snapshots are what the app last saw of each object, by cluster, to
	// find what changed while it was not running.
	snapshots map[string]map[string]snapshot
	dirty     map[string]bool
	saveTimer *time.Timer
	notifyAt  map[string]*time.Timer
}

// snapshot is what the timeline remembers of an object between runs.
type snapshot struct {
	Hash string `json:"hash"`
	Spec string `json:"spec,omitempty"`
}

func newTimeline() *Timeline {
	return &Timeline{
		snapshots: map[string]map[string]snapshot{},
		dirty:     map[string]bool{},
		notifyAt:  map[string]*time.Timer{},
		tell:      func(cluster string) { TimelineChanged.Broadcast(cluster) },
	}
}

// init sets where changes go, and drops those older than timelineDays.
func (t *Timeline) init(dir string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.dir = filepath.Join(dir, "timeline")
	_ = os.MkdirAll(t.dir, 0o700)
	cutoff := time.Now().AddDate(0, 0, -timelineDays).Format("2006-01-02")
	clusters, _ := os.ReadDir(t.dir)
	for _, c := range clusters {
		files, _ := os.ReadDir(filepath.Join(t.dir, c.Name()))
		for _, f := range files {
			if strings.HasSuffix(f.Name(), ".jsonl") && strings.TrimSuffix(f.Name(), ".jsonl") < cutoff {
				_ = os.Remove(filepath.Join(t.dir, c.Name(), f.Name()))
			}
		}
	}
}

func (t *Timeline) clusterDir(cluster string) string {
	return filepath.Join(t.dir, cluster)
}

// add records changes, and tells pages a moment later.
func (t *Timeline) add(changes ...Change) {
	if len(changes) == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.dir == "" {
		return
	}
	byFile := map[string][]Change{}
	for i := range changes {
		t.seq++
		c := &changes[i]
		if c.Time.IsZero() {
			c.Time = time.Now()
		}
		c.ID = fmt.Sprintf("%d-%d", c.Time.UnixNano(), t.seq)
		path := filepath.Join(t.clusterDir(c.Cluster), c.Time.Format("2006-01-02")+".jsonl")
		byFile[path] = append(byFile[path], *c)
	}
	for path, list := range byFile {
		_ = os.MkdirAll(filepath.Dir(path), 0o700)
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			continue
		}
		w := bufio.NewWriter(f)
		for _, c := range list {
			b, _ := json.Marshal(c)
			w.Write(b)
			w.WriteByte('\n')
		}
		w.Flush()
		f.Close()
	}
	cluster := changes[0].Cluster
	if t.notifyAt[cluster] == nil {
		t.notifyAt[cluster] = time.AfterFunc(500*time.Millisecond, func() {
			t.mu.Lock()
			delete(t.notifyAt, cluster)
			tell := t.tell
			t.mu.Unlock()
			tell(cluster)
		})
	}
}

// Query returns changes, newest first.
func (t *Timeline) Query(q TimelineQuery) ([]Change, error) {
	since := time.Now().Add(-24 * time.Hour)
	if q.Since != "" {
		s, err := time.Parse(time.RFC3339, q.Since)
		if err != nil {
			return nil, fmt.Errorf("since: %w", err)
		}
		since = s
	}
	limit := q.Limit
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	t.mu.Lock()
	dir := t.clusterDir(q.Cluster)
	t.mu.Unlock()
	files, _ := os.ReadDir(dir)
	var names []string
	first := since.Format("2006-01-02")
	for _, f := range files {
		day := strings.TrimSuffix(f.Name(), ".jsonl")
		if strings.HasSuffix(f.Name(), ".jsonl") && day >= first {
			names = append(names, f.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	out := []Change{}
	for _, name := range names {
		var day []Change
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
		for sc.Scan() {
			var c Change
			if json.Unmarshal(sc.Bytes(), &c) != nil || c.Time.Before(since) {
				continue
			}
			if (q.Namespace != "" && c.Namespace != q.Namespace) || (q.Kind != "" && c.Kind != q.Kind) || (q.Name != "" && c.Name != q.Name) {
				continue
			}
			day = append(day, c)
		}
		f.Close()
		for i := len(day) - 1; i >= 0 && len(out) < limit; i-- {
			out = append(out, day[i])
		}
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// ---- Snapshots between runs ----

func (t *Timeline) snapshotPath(cluster string) string {
	return filepath.Join(t.clusterDir(cluster), "snapshot.json")
}

// loadSnapshots reads what the last run saw of a cluster.
func (t *Timeline) loadSnapshots(cluster string) map[string]snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	if s, ok := t.snapshots[cluster]; ok {
		return s
	}
	s := map[string]snapshot{}
	if b, err := os.ReadFile(t.snapshotPath(cluster)); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	t.snapshots[cluster] = s
	return s
}

// lookup returns what the last run saw of an object.
func (t *Timeline) lookup(cluster, key string) (snapshot, bool) {
	t.loadSnapshots(cluster)
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.snapshots[cluster][key]
	return s, ok
}

// remember keeps what the app saw of an object, written a little later.
func (t *Timeline) remember(cluster, key string, s *snapshot) {
	t.mu.Lock()
	defer t.mu.Unlock()
	m := t.snapshots[cluster]
	if m == nil {
		m = map[string]snapshot{}
		t.snapshots[cluster] = m
	}
	if s == nil {
		delete(m, key)
	} else {
		m[key] = *s
	}
	t.dirty[cluster] = true
	if t.saveTimer == nil {
		t.saveTimer = time.AfterFunc(5*time.Second, t.saveSnapshots)
	}
}

func (t *Timeline) saveSnapshots() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.saveTimer = nil
	for cluster := range t.dirty {
		b, err := json.Marshal(t.snapshots[cluster])
		if err != nil {
			continue
		}
		_ = os.MkdirAll(t.clusterDir(cluster), 0o700)
		tmp := t.snapshotPath(cluster) + ".tmp"
		if os.WriteFile(tmp, b, 0o600) == nil {
			_ = os.Rename(tmp, t.snapshotPath(cluster))
		}
	}
	t.dirty = map[string]bool{}
}

// ---- What changed ----

// tracked describes an object for the timeline: its kind, and what it asks
// for (spec), which changes are compared on.
type tracked struct {
	kind  string
	meta  metav1.Object
	spec  any
	keep  bool // keep the spec's YAML for diffs
	extra func(old any) []string
}

// trackedOf describes the objects the timeline follows; ok is false for others.
func trackedOf(obj any) (tracked, bool) {
	switch o := obj.(type) {
	case *appsv1.Deployment:
		return tracked{kind: "Deployment", meta: o, spec: o.Spec, keep: true}, true
	case *appsv1.StatefulSet:
		return tracked{kind: "StatefulSet", meta: o, spec: o.Spec, keep: true}, true
	case *appsv1.DaemonSet:
		return tracked{kind: "DaemonSet", meta: o, spec: o.Spec, keep: true}, true
	case *corev1.Service:
		spec := o.Spec.DeepCopy()
		spec.ClusterIP, spec.ClusterIPs = "", nil
		return tracked{kind: "Service", meta: o, spec: spec, keep: true}, true
	case *networkingv1.Ingress:
		return tracked{kind: "Ingress", meta: o, spec: o.Spec, keep: true}, true
	case *batchv1.CronJob:
		return tracked{kind: "CronJob", meta: o, spec: o.Spec, keep: true}, true
	case *autoscalingv2.HorizontalPodAutoscaler:
		return tracked{kind: "HorizontalPodAutoscaler", meta: o, spec: o.Spec, keep: true}, true
	case *corev1.ConfigMap:
		return tracked{kind: "ConfigMap", meta: o, spec: map[string]any{"data": o.Data, "binaryData": keysOf(o.BinaryData)}, keep: true}, true
	case *corev1.Secret:
		// Never the values: their hashes tell what changed.
		return tracked{kind: "Secret", meta: o, spec: map[string]any{"type": o.Type, "data": hashes(o.Data)}}, true
	}
	return tracked{}, false
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func hashes(m map[string][]byte) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		sum := sha256.Sum256(v)
		out[k] = hex.EncodeToString(sum[:6])
	}
	return out
}

func objKey(kind string, m metav1.Object) string {
	return kind + "/" + m.GetNamespace() + "/" + m.GetName()
}

func specOf(tr tracked) (hash string, specYAML string) {
	b, _ := json.Marshal(tr.spec)
	sum := sha256.Sum256(b)
	if tr.keep {
		y, _ := yaml.JSONToYAML(b)
		specYAML = string(y)
	}
	return hex.EncodeToString(sum[:8]), specYAML
}

// actorOf returns the manager of the newest change of an object.
func actorOf(m metav1.Object) string {
	var newest metav1.ManagedFieldsEntry
	for _, f := range m.GetManagedFields() {
		if f.Time == nil || f.Subresource == "status" {
			continue
		}
		if newest.Time == nil || f.Time.After(newest.Time.Time) {
			newest = f
		}
	}
	return newest.Manager
}

// specChange compares what an object asks for, before and after.
func specChange(cluster string, oldObj, newObj any) *Change {
	nt, ok := trackedOf(newObj)
	if !ok {
		return nil
	}
	ot, _ := trackedOf(oldObj)
	oh, oy := specOf(ot)
	nh, ny := specOf(nt)
	if oh == nh {
		return nil
	}
	c := &Change{
		Cluster: cluster, Kind: nt.kind, Namespace: nt.meta.GetNamespace(), Name: nt.meta.GetName(),
		Type: ChangeUpdated, Actor: actorOf(nt.meta), Before: oy, After: ny,
		Summary: describeChange(oldObj, newObj, ot.spec, nt.spec),
	}
	return c
}

// describeChange says in a few lines what changed in a spec.
func describeChange(oldObj, newObj any, oldSpec, newSpec any) []string {
	var out []string
	if oc, nc := podTemplateOf(oldObj), podTemplateOf(newObj); oc != nil && nc != nil {
		out = append(out, templateChanges(oc, nc)...)
	}
	if o, n := replicasOf(oldObj), replicasOf(newObj); o != n && o >= 0 && n >= 0 {
		out = append(out, fmt.Sprintf("replicas %d → %d", o, n))
	}
	switch n := newObj.(type) {
	case *corev1.ConfigMap:
		out = append(out, mapChanges("key", oldObj.(*corev1.ConfigMap).Data, n.Data)...)
	case *corev1.Secret:
		out = append(out, mapChanges("key", hashes(oldObj.(*corev1.Secret).Data), hashes(n.Data))...)
	case *batchv1.CronJob:
		o := oldObj.(*batchv1.CronJob)
		if o.Spec.Schedule != n.Spec.Schedule {
			out = append(out, fmt.Sprintf("schedule %q → %q", o.Spec.Schedule, n.Spec.Schedule))
		}
		if ptrBool(o.Spec.Suspend) != ptrBool(n.Spec.Suspend) {
			out = append(out, map[bool]string{true: "suspended", false: "resumed"}[ptrBool(n.Spec.Suspend)])
		}
	case *autoscalingv2.HorizontalPodAutoscaler:
		o := oldObj.(*autoscalingv2.HorizontalPodAutoscaler)
		if ptrInt32(o.Spec.MinReplicas) != ptrInt32(n.Spec.MinReplicas) || o.Spec.MaxReplicas != n.Spec.MaxReplicas {
			out = append(out, fmt.Sprintf("min/max replicas %d–%d → %d–%d", ptrInt32(o.Spec.MinReplicas), o.Spec.MaxReplicas, ptrInt32(n.Spec.MinReplicas), n.Spec.MaxReplicas))
		}
	}
	if len(out) == 0 {
		out = append(out, changedPaths(oldSpec, newSpec, 6)...)
	}
	if len(out) == 0 {
		out = []string{"spec changed"}
	}
	return out
}

func ptrBool(b *bool) bool {
	return b != nil && *b
}

func ptrInt32(i *int32) int32 {
	if i == nil {
		return 1
	}
	return *i
}

func podTemplateOf(obj any) *corev1.PodTemplateSpec {
	switch o := obj.(type) {
	case *appsv1.Deployment:
		return &o.Spec.Template
	case *appsv1.StatefulSet:
		return &o.Spec.Template
	case *appsv1.DaemonSet:
		return &o.Spec.Template
	}
	return nil
}

func replicasOf(obj any) int32 {
	switch o := obj.(type) {
	case *appsv1.Deployment:
		return ptrInt32(o.Spec.Replicas)
	case *appsv1.StatefulSet:
		return ptrInt32(o.Spec.Replicas)
	}
	return -1
}

// templateChanges lists what changed in the pods a workload makes:
// images, environment variables (names only), resources.
func templateChanges(o, n *corev1.PodTemplateSpec) []string {
	var out []string
	old := map[string]corev1.Container{}
	for _, c := range o.Spec.Containers {
		old[c.Name] = c
	}
	for _, c := range n.Spec.Containers {
		p, ok := old[c.Name]
		if !ok {
			out = append(out, fmt.Sprintf("container %s added (%s)", c.Name, c.Image))
			continue
		}
		delete(old, c.Name)
		if p.Image != c.Image {
			out = append(out, fmt.Sprintf("image %s: %s → %s", c.Name, p.Image, c.Image))
		}
		if !reflect.DeepEqual(p.Resources, c.Resources) {
			out = append(out, fmt.Sprintf("resources of %s: %s → %s", c.Name, resourcesLine(p.Resources), resourcesLine(c.Resources)))
		}
		oe, ne := envMap(p.Env), envMap(c.Env)
		for _, line := range mapChanges("env", oe, ne) {
			out = append(out, line+" in "+c.Name)
		}
		if !reflect.DeepEqual(p.Command, c.Command) || !reflect.DeepEqual(p.Args, c.Args) {
			out = append(out, "command of "+c.Name+" changed")
		}
	}
	for name := range old {
		out = append(out, "container "+name+" removed")
	}
	if o.Annotations["kubectl.kubernetes.io/restartedAt"] != n.Annotations["kubectl.kubernetes.io/restartedAt"] {
		out = append(out, "rollout restart")
	}
	return out
}

func resourcesLine(r corev1.ResourceRequirements) string {
	part := func(l corev1.ResourceList) string {
		var s []string
		if v, ok := l[corev1.ResourceCPU]; ok {
			s = append(s, "cpu "+v.String())
		}
		if v, ok := l[corev1.ResourceMemory]; ok {
			s = append(s, "mem "+v.String())
		}
		return strings.Join(s, ", ")
	}
	req, lim := part(r.Requests), part(r.Limits)
	switch {
	case req == "" && lim == "":
		return "none"
	case lim == "":
		return "requests " + req
	case req == "":
		return "limits " + lim
	}
	return "requests " + req + " / limits " + lim
}

// envMap gives each variable a fingerprint: changed values show as changed
// without their values, which may be secret.
func envMap(env []corev1.EnvVar) map[string]string {
	out := map[string]string{}
	for _, e := range env {
		b, _ := json.Marshal(e)
		sum := sha256.Sum256(b)
		out[e.Name] = hex.EncodeToString(sum[:6])
	}
	return out
}

// mapChanges lists the keys added, removed and changed.
func mapChanges(what string, o, n map[string]string) []string {
	var added, removed, changed []string
	for k, v := range n {
		if ov, ok := o[k]; !ok {
			added = append(added, k)
		} else if ov != v {
			changed = append(changed, k)
		}
	}
	for k := range o {
		if _, ok := n[k]; !ok {
			removed = append(removed, k)
		}
	}
	var out []string
	for _, x := range []struct {
		verb string
		keys []string
	}{{"added", added}, {"changed", changed}, {"removed", removed}} {
		if len(x.keys) == 0 {
			continue
		}
		sort.Strings(x.keys)
		noun := what
		if len(x.keys) > 1 {
			noun += "s"
		}
		out = append(out, fmt.Sprintf("%s %s %s", noun, strings.Join(limitList(x.keys, 6), ", "), x.verb))
	}
	return out
}

func limitList(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return append(s[:n:n], fmt.Sprintf("+%d more", len(s)-n))
}

// changedPaths lists the fields that differ between two specs.
func changedPaths(o, n any, max int) []string {
	var om, nm any
	ob, _ := json.Marshal(o)
	nb, _ := json.Marshal(n)
	_ = json.Unmarshal(ob, &om)
	_ = json.Unmarshal(nb, &nm)
	var paths []string
	var walk func(path string, a, b any)
	walk = func(path string, a, b any) {
		if len(paths) > max {
			return
		}
		am, aok := a.(map[string]any)
		bm, bok := b.(map[string]any)
		if aok && bok {
			keys := map[string]bool{}
			for k := range am {
				keys[k] = true
			}
			for k := range bm {
				keys[k] = true
			}
			sorted := make([]string, 0, len(keys))
			for k := range keys {
				sorted = append(sorted, k)
			}
			sort.Strings(sorted)
			for _, k := range sorted {
				walk(strings.TrimPrefix(path+"."+k, "."), am[k], bm[k])
			}
			return
		}
		if !reflect.DeepEqual(a, b) {
			paths = append(paths, path+" changed")
		}
	}
	walk("", om, nm)
	if len(paths) > max {
		paths = append(paths[:max], "and more")
	}
	return paths
}

// podChanges lists what a pod did between two states: started, became
// ready or not, restarted, failed.
func podChanges(cluster string, o, n *corev1.Pod) []Change {
	var lines []string
	warning := false
	if o.Status.Phase != n.Status.Phase && n.Status.Phase != "" {
		lines = append(lines, fmt.Sprintf("phase %s → %s", orDefault(string(o.Status.Phase), "none"), n.Status.Phase))
		warning = warning || n.Status.Phase == corev1.PodFailed
	}
	if ready(o) != ready(n) && n.Status.Phase == corev1.PodRunning {
		if ready(n) {
			lines = append(lines, "became ready")
		} else {
			lines = append(lines, "became not ready")
			warning = true
		}
	}
	old := map[string]corev1.ContainerStatus{}
	for _, cs := range o.Status.ContainerStatuses {
		old[cs.Name] = cs
	}
	for _, cs := range n.Status.ContainerStatuses {
		p := old[cs.Name]
		if cs.RestartCount > p.RestartCount {
			why := ""
			if t := cs.LastTerminationState.Terminated; t != nil {
				why = fmt.Sprintf(": %s, exit code %d", orDefault(t.Reason, "Error"), t.ExitCode)
			}
			lines = append(lines, fmt.Sprintf("container %s restarted (%d)%s", cs.Name, cs.RestartCount, why))
			warning = true
		}
		pw, nw := "", ""
		if p.State.Waiting != nil {
			pw = p.State.Waiting.Reason
		}
		if cs.State.Waiting != nil {
			nw = cs.State.Waiting.Reason
		}
		if nw != pw && badWaiting[nw] {
			lines = append(lines, fmt.Sprintf("container %s: %s", cs.Name, nw))
			warning = true
		}
	}
	if len(lines) == 0 {
		return nil
	}
	return []Change{{Cluster: cluster, Kind: "Pod", Namespace: n.Namespace, Name: n.Name, Type: ChangeStatus, Summary: lines, Warning: warning}}
}

func ready(p *corev1.Pod) bool {
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// nodeChanges lists a node becoming ready or not, and cordons.
func nodeChanges(cluster string, o, n *corev1.Node) []Change {
	var lines []string
	warning := false
	nodeReady := func(n *corev1.Node) string {
		for _, c := range n.Status.Conditions {
			if c.Type == corev1.NodeReady {
				return string(c.Status)
			}
		}
		return ""
	}
	if a, b := nodeReady(o), nodeReady(n); a != b {
		switch b {
		case "True":
			lines = append(lines, "became ready")
		case "Unknown":
			lines = append(lines, "stopped reporting (unreachable)")
			warning = true
		default:
			lines = append(lines, "became not ready")
			warning = true
		}
	}
	if o.Spec.Unschedulable != n.Spec.Unschedulable {
		lines = append(lines, map[bool]string{true: "cordoned", false: "uncordoned"}[n.Spec.Unschedulable])
	}
	if len(lines) == 0 {
		return nil
	}
	return []Change{{Cluster: cluster, Kind: "Node", Name: n.Name, Type: ChangeStatus, Summary: lines, Warning: warning, Actor: actorOf(n)}}
}
