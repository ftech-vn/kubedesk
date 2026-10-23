package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	fakediscovery "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
)

// snapshotFormat names the files Kube Desk writes namespace snapshots in.
const snapshotFormat = "kubedesk-snapshot"

// SourceSnapshot is a namespace snapshot opened in the app, read-only.
const SourceSnapshot SourceKind = "snapshot"

// SnapshotOptions chooses what a snapshot holds besides the objects.
type SnapshotOptions struct {
	// Logs adds the last lines of each container, and of its previous run.
	Logs bool `json:"logs"`
	// Changes adds the timeline of the namespace, when it is watched.
	Changes bool `json:"changes"`
}

// SnapshotResult is what an export wrote.
type SnapshotResult struct {
	// Path is where the snapshot was saved; "" when the user canceled.
	Path    string   `json:"path"`
	Objects int      `json:"objects"`
	Logs    int      `json:"logs"`
	Bytes   int64    `json:"bytes"`
	Skipped []string `json:"skipped"`
}

// snapshotData is a namespace as it was: its objects, events, logs and
// changes. Secret values are masked, and tokens in logs.
type snapshotData struct {
	Format        string              `json:"format"`
	Version       int                 `json:"version"`
	Created       string              `json:"created"`
	Cluster       string              `json:"cluster"`
	ServerVersion string              `json:"serverVersion"`
	Namespace     string              `json:"namespace"`
	Resources     []APIResource       `json:"resources"`
	Objects       map[string][]Object `json:"objects"`
	Logs          map[string]string   `json:"logs"`
	Changes       []Change            `json:"changes"`
	Skipped       []string            `json:"skipped,omitempty"`
}

// snapshotSkip are kinds that only make a snapshot bigger.
var snapshotSkip = map[string]bool{
	"controllerrevisions": true, "leases": true, "endpoints": true, "csistoragecapacities": true,
}

const (
	snapshotLogLines = 300
	snapshotLogBytes = 24 << 20
)

func logKey(pod, container string, previous bool) string {
	k := pod + "/" + container
	if previous {
		k += "/previous"
	}
	return k
}

// buildSnapshot reads a namespace: every kind it has, the nodes its pods
// run on, its events, and optionally logs and changes.
func buildSnapshot(ctx context.Context, cn *conn, clusterName, namespace string, opts SnapshotOptions, changes func() []Change) (*snapshotData, error) {
	s := &snapshotData{Format: snapshotFormat, Version: 1, Created: time.Now().UTC().Format(time.RFC3339), Cluster: clusterName, Namespace: namespace, Objects: map[string][]Object{}, Logs: map[string]string{}}
	if v, err := cn.cs.Discovery().ServerVersion(); err == nil {
		s.ServerVersion = v.GitVersion
	}
	lists, err := cn.disc.ServerPreferredResources()
	if err != nil && len(lists) == 0 {
		return nil, errors.New(friendlyError(err))
	}
	add := func(r APIResource, items []unstructured.Unstructured) {
		if len(items) == 0 {
			return
		}
		key := gvrKey(r.Group, r.Version, r.Resource)
		for i := range items {
			o := items[i].Object
			unstructured.RemoveNestedField(o, "metadata", "managedFields")
			maskSecret(o)
			s.Objects[key] = append(s.Objects[key], o)
		}
		s.Resources = append(s.Resources, r)
	}
	for _, l := range lists {
		gv, err := schema.ParseGroupVersion(l.GroupVersion)
		if err != nil {
			continue
		}
		for _, ar := range l.APIResources {
			if !ar.Namespaced || strings.Contains(ar.Name, "/") || snapshotSkip[ar.Name] || !containsFold(ar.Verbs, "list") {
				continue
			}
			// Events are kept once, from the core API.
			if ar.Name == "events" && gv.Group != "" {
				continue
			}
			r := APIResource{Group: gv.Group, Version: gv.Version, Resource: ar.Name, Kind: ar.Kind, Namespaced: true, Verbs: []string{"get", "list", "watch"}, ShortNames: ar.ShortNames}
			list, err := cn.dyn.Resource(schema.GroupVersionResource{Group: gv.Group, Version: gv.Version, Resource: ar.Name}).Namespace(namespace).List(ctx, metav1.ListOptions{})
			if err != nil {
				s.Skipped = append(s.Skipped, ar.Name)
				continue
			}
			add(r, list.Items)
		}
	}
	// The namespace itself, and the nodes its pods run on.
	if ns, err := cn.dyn.Resource(schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}).Get(ctx, namespace, metav1.GetOptions{}); err == nil {
		add(APIResource{Version: "v1", Resource: "namespaces", Kind: "Namespace", Verbs: []string{"get", "list", "watch"}}, []unstructured.Unstructured{*ns})
	}
	nodes := map[string]bool{}
	for _, p := range s.Objects[gvrKey("", "v1", "pods")] {
		if n, _, _ := unstructured.NestedString(p, "spec", "nodeName"); n != "" {
			nodes[n] = true
		}
	}
	var nodeItems []unstructured.Unstructured
	for n := range nodes {
		if o, err := cn.dyn.Resource(schema.GroupVersionResource{Version: "v1", Resource: "nodes"}).Get(ctx, n, metav1.GetOptions{}); err == nil {
			nodeItems = append(nodeItems, *o)
		}
	}
	sort.Slice(nodeItems, func(i, j int) bool { return nodeItems[i].GetName() < nodeItems[j].GetName() })
	add(APIResource{Version: "v1", Resource: "nodes", Kind: "Node", Verbs: []string{"get", "list", "watch"}}, nodeItems)

	if opts.Logs {
		red := newRedactor()
		total := 0
		for _, o := range s.Objects[gvrKey("", "v1", "pods")] {
			var p corev1.Pod
			if fromUnstructured(o, &p) != nil {
				continue
			}
			restarted := map[string]bool{}
			for _, st := range append(append([]corev1.ContainerStatus{}, p.Status.InitContainerStatuses...), p.Status.ContainerStatuses...) {
				restarted[st.Name] = st.RestartCount > 0
			}
			for _, c := range append(append([]corev1.Container{}, p.Spec.InitContainers...), p.Spec.Containers...) {
				for _, previous := range []bool{false, true} {
					if previous && !restarted[c.Name] || total > snapshotLogBytes {
						continue
					}
					tail := int64(snapshotLogLines)
					stream, err := openLogs(ctx, cn.cs, namespace, p.Name, &corev1.PodLogOptions{Container: c.Name, TailLines: &tail, Previous: previous, Timestamps: true})
					if err != nil {
						continue
					}
					b, _ := io.ReadAll(io.LimitReader(stream, 4<<20))
					stream.Close()
					if len(b) == 0 {
						continue
					}
					text := red.apply(string(b))
					total += len(text)
					s.Logs[logKey(p.Name, c.Name, previous)] = text
				}
			}
		}
	}
	if opts.Changes && changes != nil {
		s.Changes = changes()
	}
	return s, nil
}

func gvrKey(group, version, resource string) string { return group + "/" + version + "/" + resource }

func (s *snapshotData) count() int {
	n := 0
	for _, l := range s.Objects {
		n += len(l)
	}
	return n
}

func (s *snapshotData) write(w io.Writer) error {
	zw := gzip.NewWriter(w)
	if err := json.NewEncoder(zw).Encode(s); err != nil {
		return err
	}
	return zw.Close()
}

func readSnapshot(path string) (*snapshotData, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, errors.New("not a Kube Desk snapshot")
	}
	var s snapshotData
	if err := json.NewDecoder(io.LimitReader(zr, 1<<30)).Decode(&s); err != nil || s.Format != snapshotFormat {
		return nil, errors.New("not a Kube Desk snapshot")
	}
	if s.Version > 1 {
		return nil, errors.New("this snapshot is from a newer Kube Desk: update the app to open it")
	}
	return &s, nil
}

// ExportSnapshot saves a namespace to a .kubedesk file, which Kube Desk
// opens without access to the cluster.
func (k *Kube) ExportSnapshot(ctx context.Context, cluster, namespace string, opts SnapshotOptions) (SnapshotResult, error) {
	if namespace == "" {
		return SnapshotResult{}, errors.New("choose a namespace to take a snapshot of")
	}
	cn, err := k.clusters.conn(cluster)
	if err != nil {
		return SnapshotResult{}, err
	}
	if cn.snapshot != nil {
		return SnapshotResult{}, errors.New("this is a snapshot already")
	}
	name := k.clusters.name(cluster)
	s, err := buildSnapshot(ctx, cn, name, namespace, opts, func() []Change {
		list, _ := k.timeline.Query(TimelineQuery{Cluster: cluster, Namespace: namespace, Limit: 2000})
		return list
	})
	if err != nil {
		return SnapshotResult{}, err
	}
	var buf bytes.Buffer
	if err := s.write(&buf); err != nil {
		return SnapshotResult{}, err
	}
	file := fmt.Sprintf("%s-%s-%s.kubedesk", safeName(name), safeName(namespace), time.Now().Format("20060102-1504"))
	path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: mygo.CallerWindow(ctx), Title: "Save the snapshot", DefaultPath: file, Filters: []mygo.FileFilter{{Name: "Kube Desk snapshot", Extensions: []string{"kubedesk"}}}})
	if err != nil || path == "" {
		return SnapshotResult{}, err
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		return SnapshotResult{}, err
	}
	return SnapshotResult{Path: path, Objects: s.count(), Logs: len(s.Logs), Bytes: int64(buf.Len()), Skipped: s.Skipped}, nil
}

func safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-.")
}

// ---- Opening snapshots ----

func (c *Clusters) snapshotsDir() string { return filepath.Join(c.dir, "snapshots") }

// snapshotCache keeps the snapshots read, by path and modification.
var snapshotCache = struct {
	sync.Mutex
	byPath map[string]*snapshotData
}{byPath: map[string]*snapshotData{}}

func loadSnapshot(s source) (*snapshotData, error) {
	stamp := s.stamp()
	snapshotCache.Lock()
	defer snapshotCache.Unlock()
	if d, ok := snapshotCache.byPath[stamp]; ok {
		return d, nil
	}
	d, err := readSnapshot(s.key)
	if err != nil {
		return nil, err
	}
	snapshotCache.byPath[stamp] = d
	return d, nil
}

// snapshotCluster is how a snapshot shows among the clusters.
func snapshotCluster(s source, d *snapshotData) Cluster {
	created := d.Created
	if t, err := time.Parse(time.RFC3339, d.Created); err == nil {
		created = t.Local().Format("Jan 2 15:04")
	}
	return Cluster{
		ID:         clusterID(s.key, "snapshot"),
		Name:       fmt.Sprintf("%s · %s", d.Cluster, d.Namespace),
		Context:    "snapshot " + created,
		Server:     fmt.Sprintf("snapshot of %s, taken %s", d.Cluster, created),
		Namespace:  d.Namespace,
		Source:     s.key,
		SourceKind: SourceSnapshot,
		ReadOnly:   true,
	}
}

// OpenSnapshot asks for .kubedesk files and opens them, each as a
// read-only cluster.
func (c *Clusters) OpenSnapshot(ctx context.Context) ([]Cluster, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:      mygo.CallerWindow(ctx),
		Title:       "Open a namespace snapshot",
		ButtonLabel: "Open",
		Filters:     []mygo.FileFilter{{Name: "Kube Desk snapshot", Extensions: []string{"kubedesk"}}},
		Multiple:    true,
	})
	if err != nil {
		return nil, err
	}
	var out []Cluster
	for _, p := range paths {
		cl, err := c.addSnapshot(p)
		if err != nil {
			return out, fmt.Errorf("%s: %w", filepath.Base(p), err)
		}
		out = append(out, cl)
	}
	return out, nil
}

// addSnapshot copies a snapshot into the app's folder, so that it stays
// open when the file moves, and returns its cluster.
func (c *Clusters) addSnapshot(path string) (Cluster, error) {
	d, err := readSnapshot(path)
	if err != nil {
		return Cluster{}, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Cluster{}, err
	}
	sum := sha1.Sum(b)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := os.MkdirAll(c.snapshotsDir(), 0o700); err != nil {
		return Cluster{}, err
	}
	dst := filepath.Join(c.snapshotsDir(), hex.EncodeToString(sum[:8])+".kubedesk")
	if err := os.WriteFile(dst, b, 0o600); err != nil {
		return Cluster{}, err
	}
	s := source{key: dst, kind: SourceSnapshot, files: []string{dst}}
	cl := snapshotCluster(s, d)
	c.store.Order = append(slices.DeleteFunc(slices.Clone(c.store.Order), func(x string) bool { return x == cl.ID }), cl.ID)
	_ = c.save()
	return cl, nil
}

// snapshotChanges filters the changes a snapshot holds, as Timeline.Query
// does, with times counted from when the snapshot was taken.
func snapshotChanges(d *snapshotData, q TimelineQuery) ([]Change, error) {
	taken, err := time.Parse(time.RFC3339, d.Created)
	if err != nil {
		taken = time.Now()
	}
	since := taken.Add(-24 * time.Hour)
	if q.Since != "" {
		s, err := time.Parse(time.RFC3339, q.Since)
		if err != nil {
			return nil, fmt.Errorf("since: %w", err)
		}
		since = s.Add(-time.Since(taken))
	}
	limit := q.Limit
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	out := []Change{}
	for _, c := range d.Changes {
		if c.Time.Before(since) || (q.Namespace != "" && c.Namespace != q.Namespace) || (q.Kind != "" && c.Kind != q.Kind) || (q.Name != "" && c.Name != q.Name) {
			continue
		}
		c.Cluster = q.Cluster
		out = append(out, c)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// snapshotLogs are the logs of the snapshots whose clientsets serve them.
var snapshotLogs sync.Map // kubernetes.Interface → *snapshotData

// snapshotConn serves a snapshot through fake clients, so that every view
// of the app reads it as it reads a cluster.
func snapshotConn(cl Cluster, s source, d *snapshotData) (*conn, error) {
	var typed, dyn []runtime.Object
	listKinds := map[schema.GroupVersionResource]string{}
	var apiLists []*metav1.APIResourceList
	byGV := map[string]*metav1.APIResourceList{}
	for _, r := range d.Resources {
		gv := schema.GroupVersion{Group: r.Group, Version: r.Version}.String()
		l := byGV[gv]
		if l == nil {
			l = &metav1.APIResourceList{GroupVersion: gv}
			byGV[gv] = l
			apiLists = append(apiLists, l)
		}
		l.APIResources = append(l.APIResources, metav1.APIResource{Name: r.Resource, Kind: r.Kind, Namespaced: r.Namespaced, Verbs: r.Verbs, ShortNames: r.ShortNames})
		listKinds[schema.GroupVersionResource{Group: r.Group, Version: r.Version, Resource: r.Resource}] = r.Kind + "List"
		for _, o := range d.Objects[gvrKey(r.Group, r.Version, r.Resource)] {
			u := &unstructured.Unstructured{Object: o}
			dyn = append(dyn, u.DeepCopy())
			gvk := u.GroupVersionKind()
			if t, err := scheme.Scheme.New(gvk); err == nil {
				if runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, t) == nil {
					typed = append(typed, t)
				}
			}
		}
	}
	cs := fake.NewSimpleClientset(typed...)
	fd := cs.Discovery().(*fakediscovery.FakeDiscovery)
	fd.Resources = apiLists
	fd.FakedServerVersion = &version.Info{GitVersion: d.ServerVersion}
	dc := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds, dyn...)
	disc := memory.NewMemCacheClient(discovery.DiscoveryInterface(fd))
	snapshotLogs.Store(kubernetes.Interface(cs), d)
	return &conn{
		cluster:  cl,
		cfg:      &rest.Config{Host: "snapshot"},
		cs:       cs,
		dyn:      dc,
		disc:     disc,
		mapper:   restmapper.NewDeferredDiscoveryRESTMapper(disc),
		src:      s,
		stamp:    s.stamp(),
		snapshot: d,
	}, nil
}

// openLogs opens the logs of a container, from the cluster, or from the
// snapshot a clientset serves.
func openLogs(ctx context.Context, cs kubernetes.Interface, namespace, pod string, po *corev1.PodLogOptions) (io.ReadCloser, error) {
	if v, ok := snapshotLogs.Load(cs); ok {
		d := v.(*snapshotData)
		container := po.Container
		if container == "" {
			for k := range d.Logs {
				if strings.HasPrefix(k, pod+"/") {
					container = strings.Split(k, "/")[1]
					break
				}
			}
		}
		text, ok := d.Logs[logKey(pod, container, po.Previous)]
		if !ok {
			return nil, errors.New("this snapshot has no logs of " + pod + "/" + container)
		}
		if po.TailLines != nil {
			lines := strings.SplitAfter(text, "\n")
			if n := int(*po.TailLines); n > 0 && len(lines) > n+1 {
				text = strings.Join(lines[len(lines)-n-1:], "")
			}
		}
		if !po.Timestamps {
			var b strings.Builder
			for _, l := range strings.SplitAfter(text, "\n") {
				if _, rest, ok := strings.Cut(l, " "); ok {
					l = rest
				}
				b.WriteString(l)
			}
			text = b.String()
		}
		return io.NopCloser(strings.NewReader(text)), nil
	}
	return cs.CoreV1().Pods(namespace).GetLogs(pod, po).Stream(ctx)
}

// errSnapshot is the error of what a snapshot cannot do.
var errSnapshot = errors.New("this is a snapshot of a namespace: it cannot be changed, and has no live pods")

// snapshotChanges serves the timeline of a snapshot cluster; ok is false
// for other clusters.
func (c *Clusters) snapshotChanges(id string, q TimelineQuery) ([]Change, bool, error) {
	c.mu.Lock()
	snap := c.snapshots[id]
	c.mu.Unlock()
	if !snap {
		return nil, false, nil
	}
	cn, err := c.conn(id)
	if err != nil {
		return nil, true, err
	}
	list, err := snapshotChanges(cn.snapshot, q)
	return list, true, err
}

// SnapshotOpened is sent when a snapshot was opened from the Finder.
var SnapshotOpened = mygo.NewEvent[Cluster]("snapshot:opened")

// openSnapshotFile opens a snapshot the Finder hands the app, and makes it
// the cluster shown.
func (c *Clusters) openSnapshotFile(path string) {
	cl, err := c.addSnapshot(path)
	if err != nil {
		_, _ = mygo.Dialog.Message(mygo.MessageOptions{Type: mygo.MessageError, Message: "Could not open " + filepath.Base(path), Detail: err.Error()})
		return
	}
	_ = c.SetActive(cl.ID)
	SnapshotOpened.Broadcast(cl)
}

// RevealFile shows a file in the Finder.
func (k *Kube) RevealFile(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("not a file")
	}
	return exec.Command("open", "-R", path).Start()
}
