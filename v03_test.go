package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

func int32p(i int32) *int32 { return &i }

func demoObjects() []runtime.Object {
	labels := map[string]string{"app": "api"}
	ctrl := true
	rs := metav1.OwnerReference{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "api-5d8f", UID: "rs1", Controller: &ctrl}
	pod := func(name, node string, cpu string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "shop", Labels: labels, OwnerReferences: []metav1.OwnerReference{rs}},
			Spec: corev1.PodSpec{NodeName: node, Containers: []corev1.Container{{Name: "api", Image: "api:1", Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(cpu), corev1.ResourceMemory: resource.MustParse("256Mi")},
			}}},
				Volumes: []corev1.Volume{{Name: "creds", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "api-creds"}}}}},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		}
	}
	node := func(name string, cpu string) *corev1.Node {
		return &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Status: corev1.NodeStatus{
				Allocatable: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(cpu), corev1.ResourceMemory: resource.MustParse("8Gi")},
				Conditions:  []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
			},
		}
	}
	lonely := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "debug", Namespace: "shop"},
		Spec:       corev1.PodSpec{NodeName: "n1", Containers: []corev1.Container{{Name: "sh", Image: "busybox"}}, Volumes: []corev1.Volume{{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}}},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
	return []runtime.Object{
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "shop", Labels: map[string]string{"app.kubernetes.io/managed-by": "Helm"}, Annotations: map[string]string{"meta.helm.sh/release-name": "shop"}},
			Spec:       appsv1.DeploymentSpec{Replicas: int32p(2), Selector: &metav1.LabelSelector{MatchLabels: labels}},
		},
		pod("api-1", "n1", "500m"), pod("api-2", "n2", "500m"), lonely,
		node("n1", "1"), node("n2", "600m"),
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "shop"}, Spec: corev1.ServiceSpec{Selector: labels}},
		&networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "shop", Namespace: "shop"}, Spec: networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{
			Host: "shop.example.com",
			IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{Paths: []networkingv1.HTTPIngressPath{{
				Path: "/api", Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: "api"}},
			}}}},
		}}}},
		&policyv1.PodDisruptionBudget{
			ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "shop"},
			Spec:       policyv1.PodDisruptionBudgetSpec{MinAvailable: &intstr.IntOrString{IntVal: 2}, Selector: &metav1.LabelSelector{MatchLabels: labels}},
			Status:     policyv1.PodDisruptionBudgetStatus{DisruptionsAllowed: 0},
		},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "api-creds", Namespace: "shop"}},
	}
}

func impactTexts(t *testing.T, cs kubernetes.Interface, req ImpactRequest) string {
	t.Helper()
	items, err := impactOf(context.Background(), cs, req)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, it := range items {
		lines = append(lines, string(it.Severity)+": "+it.Text)
	}
	return strings.Join(lines, "\n")
}

func TestImpact(t *testing.T) {
	cs := fake.NewSimpleClientset(demoObjects()...)
	for _, tc := range []struct {
		name string
		req  ImpactRequest
		want []string
	}{
		{"delete deployment", ImpactRequest{Action: ImpactDelete, Kind: "Deployment", Namespace: "shop", Name: "api"},
			[]string{"danger: 2 pods are deleted", "Service api loses all its endpoints", "Ingress shop stops working for shop.example.com/api", "Helm manages it (release shop)"}},
		{"delete a pod of a controller", ImpactRequest{Action: ImpactDelete, Kind: "Pod", Namespace: "shop", Name: "api-1"},
			[]string{"info: ReplicaSet api-5d8f makes a new pod", "Deleting bypasses disruption budget api", "Service api keeps 1 other pod"}},
		{"delete a lonely pod", ImpactRequest{Action: ImpactDelete, Kind: "Pod", Namespace: "shop", Name: "debug"},
			[]string{"danger: No controller owns this pod", "emptyDir"}},
		{"scale to zero", ImpactRequest{Action: ImpactScale, Kind: "Deployment", Namespace: "shop", Name: "api", Replicas: 0},
			[]string{"danger: All 2 pods stop", "Service api loses all its endpoints"}},
		{"scale down below the budget", ImpactRequest{Action: ImpactScale, Kind: "Deployment", Namespace: "shop", Name: "api", Replicas: 1},
			[]string{"1 pod is removed", "Disruption budget api asks for at least 2"}},
		{"scale up past the capacity", ImpactRequest{Action: ImpactScale, Kind: "Deployment", Namespace: "shop", Name: "api", Replicas: 6},
			[]string{"4 more pods ask for 2 CPU", "some may stay pending"}},
		{"drain", ImpactRequest{Action: ImpactDrain, Name: "n1"},
			[]string{"2 pods are evicted", "Pod shop/debug has no controller", "Disruption budget shop/api allows 0 disruptions", "may not fit on the other nodes"}},
		{"delete a secret in use", ImpactRequest{Action: ImpactDelete, Kind: "Secret", Namespace: "shop", Name: "api-creds"},
			[]string{"Used by 2 pods of replicaset api: they keep running, but fail to start again"}},
		{"delete the namespace", ImpactRequest{Action: ImpactDelete, Kind: "Namespace", Name: "shop"},
			[]string{"Everything in it is deleted: 3 pods, 1 deployment, 1 service, 1 secret"}},
	} {
		got := impactTexts(t, cs, tc.req)
		for _, w := range tc.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: missing %q in\n%s", tc.name, w, got)
			}
		}
	}
}

func TestDescribeChange(t *testing.T) {
	old := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "api"}, Spec: appsv1.DeploymentSpec{Replicas: int32p(2), Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{
		Name: "api", Image: "api:1", Env: []corev1.EnvVar{{Name: "A", Value: "1"}, {Name: "GONE", Value: "x"}},
	}}}}}}
	neu := old.DeepCopy()
	neu.Spec.Replicas = int32p(3)
	neu.Spec.Template.Spec.Containers[0].Image = "api:2"
	neu.Spec.Template.Spec.Containers[0].Env = []corev1.EnvVar{{Name: "A", Value: "2"}, {Name: "NEW", Value: "y"}}
	c := specChange("c", old, neu)
	if c == nil {
		t.Fatal("no change")
	}
	got := strings.Join(c.Summary, "\n")
	for _, w := range []string{"image api: api:1 → api:2", "replicas 2 → 3", "env NEW added in api", "env A changed in api", "env GONE removed in api"} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in\n%s", w, got)
		}
	}
	if strings.Contains(got, "1") && strings.Contains(got, "\"2\"") {
		t.Errorf("env values leaked: %s", got)
	}
	if !strings.Contains(c.Before, "api:1") || !strings.Contains(c.After, "api:2") {
		t.Errorf("before/after YAML: %q / %q", c.Before, c.After)
	}
	if specChange("c", old, old.DeepCopy()) != nil {
		t.Error("an unchanged spec made a change")
	}

	s1 := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "s"}, Data: map[string][]byte{"password": []byte("hunter2")}}
	s2 := s1.DeepCopy()
	s2.Data["password"] = []byte("hunter3")
	sc := specChange("c", s1, s2)
	if sc == nil || !strings.Contains(strings.Join(sc.Summary, ""), "key password changed") {
		t.Fatalf("secret change: %+v", sc)
	}
	if sc.Before != "" || sc.After != "" || strings.Contains(strings.Join(sc.Summary, ""), "hunter") {
		t.Fatalf("a secret's values reached the timeline: %+v", sc)
	}
}

func TestRedactor(t *testing.T) {
	r := newRedactor()
	in := strings.Join([]string{
		"Authorization: Bearer abcdef0123456789abcdef",
		"connecting to postgres://shop:s3cretpass@db:5432/shop",
		`{"password": "hunter2hunter2", "user": "ada"}`,
		"using key AKIAIOSFODNN7EXAMPLE",
		"token=ghp_abcdefghijklmnopqrstuvwxyz0123",
		"jwt eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",
		"GET /healthz 200",
	}, "\n")
	out := r.apply(in)
	for _, leak := range []string{"abcdef0123456789", "s3cretpass", "hunter2", "AKIAIOSFODNN7EXAMPLE", "ghp_abc", "dozjgNry"} {
		if strings.Contains(out, leak) {
			t.Errorf("%q survived:\n%s", leak, out)
		}
	}
	for _, keep := range []string{"Bearer [redacted]", "postgres://shop:[redacted]@db", `"user": "ada"`, "GET /healthz 200"} {
		if !strings.Contains(out, keep) {
			t.Errorf("missing %q in:\n%s", keep, out)
		}
	}
}

func TestReport(t *testing.T) {
	cs := fake.NewSimpleClientset(demoObjects()...)
	r := &reporter{ctx: context.Background(), cs: cs, redact: newRedactor()}
	r.header("prod", "v1.34", "Deployment", "shop", "api")
	if err := r.object("Deployment", "shop", "api"); err != nil {
		t.Fatal(err)
	}
	md := r.b.String()
	for _, w := range []string{"# Deployment shop/api", "Pods:** 0 ready of 2 (2 wanted)", "## Pod api-1", "| api | `api:1` |", "Logs of api"} {
		if !strings.Contains(md, w) {
			t.Errorf("missing %q in\n%s", w, md)
		}
	}
}

// TestTimelineRecorder runs the watchlist on a fake cluster: changes while
// it runs are recorded, and so are those made while the app was closed.
func TestTimelineRecorder(t *testing.T) {
	c := newTestClusters(t)
	dir := t.TempDir()
	cs := fake.NewSimpleClientset(demoObjects()...)
	run := func() (*Alerts, *Timeline) {
		tl := newTimeline()
		tl.tell = func(string) {}
		tl.init(dir)
		a := newAlerts(c, tl, func() {})
		a.publish = func(AlertsState) {}
		a.deliver = func(mygo.NotificationOptions) {}
		a.client = func(string) (kubernetes.Interface, error) { return cs, nil }
		if err := a.SetWatched("fake", "shop", true); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "the first sync", 20*time.Second, func() bool { return len(a.State().Syncing) == 0 })
		return a, tl
	}
	ctx := context.Background()
	a, tl := run()

	d, _ := cs.AppsV1().Deployments("shop").Get(ctx, "api", metav1.GetOptions{})
	d.Spec.Replicas = int32p(4)
	if _, err := cs.AppsV1().Deployments("shop").Update(ctx, d, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.CoreV1().ConfigMaps("shop").Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "flags"}, Data: map[string]string{"beta": "on"}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	find := func(tl *Timeline, pred func(Change) bool) *Change {
		list, _ := tl.Query(TimelineQuery{Cluster: "fake"})
		for i := range list {
			if pred(list[i]) {
				return &list[i]
			}
		}
		return nil
	}
	waitFor(t, "the scale and the config map in the timeline", 10*time.Second, func() bool {
		return find(tl, func(c Change) bool { return c.Kind == "Deployment" && strings.Contains(strings.Join(c.Summary, ""), "replicas 2 → 4") }) != nil &&
			find(tl, func(c Change) bool { return c.Kind == "ConfigMap" && c.Type == ChangeCreated }) != nil
	})

	// Quit, change things, start again.
	_ = a.SetWatched("fake", "shop", false)
	tl.saveSnapshots()
	_ = c.setWatched("fake", "shop", false)
	time.Sleep(200 * time.Millisecond)
	d, _ = cs.AppsV1().Deployments("shop").Get(ctx, "api", metav1.GetOptions{})
	d.Spec.Template.Spec.Containers = []corev1.Container{{Name: "api", Image: "api:2"}}
	_, _ = cs.AppsV1().Deployments("shop").Update(ctx, d, metav1.UpdateOptions{})
	_ = cs.CoreV1().ConfigMaps("shop").Delete(ctx, "flags", metav1.DeleteOptions{})
	_, _ = cs.CoreV1().Services("shop").Create(ctx, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "new-svc"}}, metav1.CreateOptions{})

	_, tl2 := run()
	waitFor(t, "the offline changes", 10*time.Second, func() bool {
		upd := find(tl2, func(c Change) bool { return c.Offline && c.Kind == "Deployment" && c.Type == ChangeUpdated })
		del := find(tl2, func(c Change) bool { return c.Offline && c.Kind == "ConfigMap" && c.Type == ChangeDeleted })
		add := find(tl2, func(c Change) bool { return c.Offline && c.Kind == "Service" && c.Name == "new-svc" })
		return upd != nil && strings.Contains(upd.After, "api:2") && del != nil && add != nil
	})
}

func TestClusterOverview(t *testing.T) {
	cs := fake.NewSimpleClientset(demoObjects()...)
	ov, err := clusterOverview(context.Background(), cs)
	if err != nil {
		t.Fatal(err)
	}
	if len(ov.Nodes) != 2 || ov.Nodes[0].State != "Ready" {
		t.Errorf("nodes: %+v", ov.Nodes)
	}
	var shop *OverviewNamespace
	for i := range ov.Namespaces {
		if ov.Namespaces[i].Name == "shop" {
			shop = &ov.Namespaces[i]
		}
	}
	if shop == nil || shop.Pods != 3 || shop.ByStatus["Running, not ready"] != 3 {
		t.Errorf("namespace shop: %+v", shop)
	}
	if len(ov.NotReady) != 1 || !strings.Contains(ov.NotReady[0], "deployment shop/api: 0/2 ready") {
		t.Errorf("not ready: %v", ov.NotReady)
	}
}
