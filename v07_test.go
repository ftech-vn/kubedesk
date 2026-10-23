package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery/cached/memory"
	fakediscovery "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	clientscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/restmapper"
)

// fakeConn is a connection to a fake cluster with the objects given.
func fakeConn(t *testing.T, objs ...runtime.Object) *conn {
	t.Helper()
	cs := fake.NewSimpleClientset(objs...)
	fd := cs.Discovery().(*fakediscovery.FakeDiscovery)
	fd.Resources = []*metav1.APIResourceList{
		{GroupVersion: "v1", APIResources: []metav1.APIResource{
			{Name: "pods", Kind: "Pod", Namespaced: true, Verbs: []string{"get", "list", "watch"}},
			{Name: "services", Kind: "Service", Namespaced: true, Verbs: []string{"get", "list", "watch"}},
			{Name: "secrets", Kind: "Secret", Namespaced: true, Verbs: []string{"get", "list", "watch"}},
			{Name: "configmaps", Kind: "ConfigMap", Namespaced: true, Verbs: []string{"get", "list", "watch"}},
			{Name: "events", Kind: "Event", Namespaced: true, Verbs: []string{"get", "list", "watch"}},
			{Name: "namespaces", Kind: "Namespace", Verbs: []string{"get", "list", "watch"}},
			{Name: "nodes", Kind: "Node", Verbs: []string{"get", "list", "watch"}},
		}},
		{GroupVersion: "apps/v1", APIResources: []metav1.APIResource{
			{Name: "deployments", Kind: "Deployment", Namespaced: true, Verbs: []string{"get", "list", "watch"}},
		}},
	}
	listKinds := map[schema.GroupVersionResource]string{}
	for _, l := range fd.Resources {
		gv, _ := schema.ParseGroupVersion(l.GroupVersion)
		for _, r := range l.APIResources {
			listKinds[gv.WithResource(r.Name)] = r.Kind + "List"
		}
	}
	var dyn []runtime.Object
	for _, o := range objs {
		m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(o)
		if err != nil {
			t.Fatal(err)
		}
		u := &unstructured.Unstructured{Object: m}
		gvks, _, _ := clientscheme.Scheme.ObjectKinds(o)
		u.SetGroupVersionKind(gvks[0])
		dyn = append(dyn, u)
	}
	disc := memory.NewMemCacheClient(fd)
	return &conn{cs: cs, dyn: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds, dyn...), disc: disc, mapper: restmapper.NewDeferredDiscoveryRESTMapper(disc)}
}

func shopObjects() []runtime.Object {
	replicas := int32(2)
	return []runtime.Object{
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "shop"}},
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}},
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "unrelated"}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "shop", UID: "d1", ResourceVersion: "9"}, Spec: appsv1.DeploymentSpec{Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web", "pod-template-hash": "abc"}}, Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "web", Image: "web:1.2", EnvFrom: []corev1.EnvFromSource{{ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "web-config"}}}},
					Env: []corev1.EnvVar{{Name: "DB", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "web-db"}, Key: "url"}}}}}},
			}}}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "web-1", Namespace: "shop", Labels: map[string]string{"app": "web"}}, Spec: corev1.PodSpec{NodeName: "n1", Containers: []corev1.Container{{Name: "web", Image: "web:1.2"}}},
			Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{Name: "web", RestartCount: 2}}}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "shop"}, Spec: corev1.ServiceSpec{ClusterIP: "10.0.0.7", ClusterIPs: []string{"10.0.0.7"}, Selector: map[string]string{"app": "web"}, Ports: []corev1.ServicePort{{Port: 80, NodePort: 30080}}, Type: corev1.ServiceTypeNodePort}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "web-config", Namespace: "shop"}, Data: map[string]string{"MODE": "live"}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "web-db", Namespace: "shop"}, Data: map[string][]byte{"url": []byte("postgres://u:hunter2@db")}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "elsewhere", Namespace: "other"}},
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	src := fakeConn(t, shopObjects()...)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	d, err := buildSnapshot(ctx, src, "prod", "shop", SnapshotOptions{Logs: true, Changes: true}, func() []Change {
		return []Change{{Time: now.Add(-time.Hour), Namespace: "shop", Kind: "Deployment", Name: "web", Summary: []string{"image web:1.1 → web:1.2"}}}
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(d.Objects[gvrKey("", "v1", "nodes")]); n != 1 {
		t.Errorf("%d nodes, want only the pods' one", n)
	}
	if _, ok := d.Logs["web-1/web/previous"]; !ok {
		t.Errorf("no logs of the previous run: %v", d.Logs)
	}
	var buf bytes.Buffer
	if err := d.write(&buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "hunter2") {
		t.Fatal("a secret value is in the file")
	}
	file := filepath.Join(t.TempDir(), "shop.kubedesk")
	_ = os.WriteFile(file, buf.Bytes(), 0o600)

	c := newTestClusters(t)
	cl, err := c.addSnapshot(file)
	if err != nil {
		t.Fatal(err)
	}
	if !cl.ReadOnly || cl.SourceKind != SourceSnapshot || cl.Namespace != "shop" || !strings.HasPrefix(cl.Name, "prod · shop") {
		t.Fatalf("cluster: %+v", cl)
	}
	k := &Kube{clusters: c, timeline: newTimeline()}
	k.timeline.external = c.snapshotChanges
	if st := c.Test(ctx, cl.ID); !st.OK {
		t.Fatalf("test: %+v", st)
	}
	pods, err := k.List(ctx, cl.ID, GVR{Version: "v1", Resource: "pods"}, "shop")
	if err != nil || len(pods) != 1 {
		t.Fatalf("pods: %v %d", err, len(pods))
	}
	deps, err := k.List(ctx, cl.ID, GVR{Group: "apps", Version: "v1", Resource: "deployments"}, "")
	if err != nil || len(deps) != 1 {
		t.Fatalf("deployments: %v %d", err, len(deps))
	}
	res, err := k.Resources(ctx, cl.ID)
	if err != nil || len(res) < 5 {
		t.Errorf("resources: %v %d", err, len(res))
	}
	cn, _ := c.conn(cl.ID)
	// Lists are watched: the watch of a snapshot opens, and stays quiet.
	r, _ := k.resource(cl.ID, GVR{Version: "v1", Resource: "pods"}, "shop")
	w, err := r.Watch(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	w.Stop()
	if ov, err := clusterOverview(ctx, cn.cs); err != nil || len(ov.Namespaces) == 0 {
		t.Errorf("overview: %v %+v", err, ov)
	}
	stream, err := openLogs(ctx, cn.cs, "shop", "web-1", &corev1.PodLogOptions{Container: "web", Previous: true})
	if err != nil {
		t.Fatal(err)
	}
	stream.Close()
	if _, err := k.Apply(ctx, cl.ID, "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: x}\n", "shop"); err == nil {
		t.Error("a snapshot was changed")
	}
	if _, err := k.StartPortForward(cl.ID, "shop", "web-1", 80, 0); err == nil {
		t.Error("a port of a snapshot was forwarded")
	}
	changes, err := k.timeline.Query(TimelineQuery{Cluster: cl.ID})
	if err != nil || len(changes) != 1 {
		t.Errorf("changes: %v %v", err, changes)
	}
	if err := c.Remove(cl.ID); err != nil {
		t.Fatal(err)
	}
	for _, x := range c.List().Clusters {
		if x.ID == cl.ID {
			t.Error("the snapshot is still open")
		}
	}
}

func TestPromote(t *testing.T) {
	c := newTestClusters(t)
	c.conns["src"] = fakeConn(t, shopObjects()...)
	c.conns["dst"] = fakeConn(t, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "other"}})
	k := &Kube{clusters: c, timeline: newTimeline()}
	ctx := context.Background()
	req := PromoteRequest{From: "src", To: "dst", GVR: GVR{Group: "apps", Version: "v1", Resource: "deployments"}, Kind: "Deployment", Namespace: "shop", Name: "web", Images: map[string]string{"web": "web:1.3"}, WithConfig: true}
	plan, err := k.PlanPromote(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Containers) != 1 || plan.Containers[0].Image != "web:1.2" || strings.Join(plan.ConfigMaps, ",") != "web-config" || strings.Join(plan.Secrets, ",") != "web-db" {
		t.Errorf("plan: %+v", plan)
	}
	notes := strings.Join(plan.Notes, "\n")
	if !strings.Contains(notes, "Namespace shop does not exist") || !strings.Contains(notes, "lacks secrets the pods need: web-db") {
		t.Errorf("notes: %s", notes)
	}
	kinds := []string{}
	for _, d := range plan.Diffs {
		kinds = append(kinds, d.Kind+":"+d.Action)
		if d.Kind == "Deployment" && (!strings.Contains(d.After, "image: web:1.3") || strings.Contains(d.After, "pod-template-hash") || strings.Contains(d.After, "resourceVersion") || strings.Contains(d.After, "uid:")) {
			t.Errorf("deployment:\n%s", d.After)
		}
	}
	if strings.Join(kinds, ",") != "Namespace:create,ConfigMap:create,Deployment:create" {
		t.Errorf("diffs: %v", kinds)
	}

	if _, err := k.Promote(ctx, req); err != nil {
		t.Fatal(err)
	}
	dst := c.conns["dst"]
	d, err := dst.cs.AppsV1().Deployments("shop").Get(ctx, "web", metav1.GetOptions{})
	if err != nil {
		// The fake dynamic and typed clients keep separate objects.
		u, err2 := dst.dyn.Resource(schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}).Namespace("shop").Get(ctx, "web", metav1.GetOptions{})
		if err2 != nil {
			t.Fatalf("not promoted: %v / %v", err, err2)
		}
		img, _, _ := unstructured.NestedSlice(u.Object, "spec", "template", "spec", "containers")
		if img[0].(map[string]any)["image"] != "web:1.3" {
			t.Errorf("image: %v", img)
		}
	} else if d.Spec.Template.Spec.Containers[0].Image != "web:1.3" {
		t.Errorf("image %s", d.Spec.Template.Spec.Containers[0].Image)
	}

	// A service loses its allocated addresses; a secret its values only
	// when asked.
	svc, _, err := k.promoteManifest(ctx, PromoteRequest{From: "src", To: "dst", GVR: GVR{Version: "v1", Resource: "services"}, Kind: "Service", Namespace: "shop", Name: "web"})
	if err != nil || strings.Contains(svc, "clusterIP") || strings.Contains(svc, "nodePort") || !strings.Contains(svc, "port: 80") {
		t.Errorf("service (%v):\n%s", err, svc)
	}
	c.store.Prefs["dst"] = ClusterPrefs{ReadOnly: true}
	if _, err := k.Promote(ctx, req); err == nil {
		t.Error("promoted to a read-only cluster")
	}
	if _, err := k.PlanPromote(ctx, PromoteRequest{From: "src", To: "src", GVR: req.GVR, Namespace: "shop", Name: "web"}); err == nil {
		t.Error("promoted to the same cluster")
	}
}

func TestRunbooks(t *testing.T) {
	c := newTestClusters(t)
	added, err := c.AddKubeconfig("prod-eu", testKubeconfig)
	if err != nil {
		t.Fatal(err)
	}
	id := added[0].ID
	r := &Runbooks{clusters: c}
	if f := r.Folder(); !f.Default {
		t.Errorf("folder: %+v", f)
	}
	mk := func(title string, targets ...RunbookTarget) Runbook {
		t.Helper()
		rb, err := r.Save(Runbook{Title: title, Targets: targets, Body: "1. Check the logs\n\n[Restart](kubedesk:restart)\n"})
		if err != nil {
			t.Fatal(err)
		}
		return rb
	}
	one := mk("Ledger sync fails", RunbookTarget{Namespace: "payments", Kind: "Deployment", Name: "ledger-*"})
	ns := mk("Payments namespace", RunbookTarget{Namespace: "payments"})
	mk("Other cluster", RunbookTarget{Cluster: "staging*", Namespace: "payments"})
	mk("Any pod", RunbookTarget{Kind: "Pod"})
	if one.ID != "ledger-sync-fails.md" || one.Targets[0].Name != "ledger-*" || !strings.Contains(one.Body, "kubedesk:restart") {
		t.Errorf("saved: %+v", one)
	}
	// Front matter survives a hand edit; a file without it still reads.
	p, _ := r.pathOf("notes/plain.md")
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, []byte("# Plain notes\n\nhello"), 0o644)
	all, _ := r.List()
	if len(all) != 5 {
		t.Fatalf("%d runbooks", len(all))
	}

	got, _ := r.For(id, "payments", "Deployment", "ledger-sync")
	titles := []string{}
	for _, rb := range got {
		titles = append(titles, rb.Title)
	}
	if strings.Join(titles, ",") != "Ledger sync fails,Payments namespace" {
		t.Errorf("for the deployment: %v", titles)
	}
	got, _ = r.For(id, "payments", "", "")
	if len(got) != 1 || got[0].ID != ns.ID {
		t.Errorf("for the namespace: %+v", got)
	}
	// The cluster matches by its context, which other machines share.
	b := mk("By context", RunbookTarget{Cluster: "default", Namespace: "payments"})
	got, _ = r.For(id, "payments", "", "")
	if len(got) != 2 {
		t.Errorf("by context: %+v", got)
	}
	_ = r.Delete(b.ID)

	if _, err := r.Get("../../etc/passwd.md"); err == nil {
		t.Error("read outside the folder")
	}
	if _, err := r.Save(Runbook{ID: "../x.md", Title: "x"}); err == nil {
		t.Error("wrote outside the folder")
	}
	again, _ := r.Save(Runbook{Title: "Ledger sync fails"})
	if again.ID != "ledger-sync-fails-2.md" {
		t.Errorf("second with the same title: %s", again.ID)
	}

	// Agents read them.
	ag := newAgents(&Kube{clusters: c, timeline: newTimeline()}, c, func() {})
	text, _, err := ag.dispatch(context.Background(), "test", "get_runbooks", toolArgs{Cluster: id, Namespace: "payments", Kind: "deployments", Name: "ledger-sync"}, ag.assistantPolicy())
	if err != nil || !strings.Contains(text, "# Runbook: Ledger sync fails") || !strings.Contains(text, "Check the logs") {
		t.Errorf("tool (%v):\n%s", err, text)
	}
}
