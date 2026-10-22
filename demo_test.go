package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestDemoCluster runs the watchlist, the logs of a workload and read-only
// clusters against a throwaway cluster: the current context of $KUBECONFIG.
// It creates and deletes a namespace of its own. Run it with K8S_DEMO=1, never
// against a cluster you care about.
func TestDemoCluster(t *testing.T) {
	if os.Getenv("K8S_DEMO") == "" {
		t.Skip("set K8S_DEMO=1, and KUBECONFIG to a throwaway cluster")
	}
	c := newClusters()
	c.dir = t.TempDir()
	c.store.Prefs = map[string]ClusterPrefs{}
	c.store.Namespaces = map[string]string{}
	if err := os.MkdirAll(c.storedDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	var id string
	for _, cl := range c.List().Clusters {
		if cl.Current {
			id = cl.ID
		}
	}
	if id == "" {
		t.Fatal("no current context")
	}
	cn, err := c.conn(id)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ns := fmt.Sprintf("kd-test-%d", time.Now().Unix()%100000)
	if _, err := cn.cs.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cn.cs.CoreV1().Namespaces().Delete(context.Background(), ns, metav1.DeleteOptions{}) })

	// ---- The watchlist ----
	var mu sync.Mutex
	var notes []mygo.NotificationOptions
	tl := newTimeline()
	tl.tell = func(string) {}
	tl.init(t.TempDir())
	a := newAlerts(c, tl, func() {})
	a.publish = func(AlertsState) {}
	a.deliver = func(o mygo.NotificationOptions) {
		mu.Lock()
		notes = append(notes, o)
		mu.Unlock()
	}
	if err := a.SetWatched(id, ns, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the watchlist to sync", 60*time.Second, func() bool { return len(a.State().Syncing) == 0 })

	deploy := func(name string, replicas int32, script string) {
		labels := map[string]string{"app": name}
		_, err := cn.cs.AppsV1().Deployments(ns).Create(ctx, &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Selector: &metav1.LabelSelector{MatchLabels: labels},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: labels},
					Spec: corev1.PodSpec{Containers: []corev1.Container{{
						Name: "main", Image: "busybox:1.36", Command: []string{"sh", "-c", script},
					}}},
				},
			},
		}, metav1.CreateOptions{})
		if err != nil {
			t.Fatal(err)
		}
	}
	deploy("crasher", 1, "echo boom; exit 1")
	deploy("web", 2, `i=0; while true; do i=$((i+1)); echo "hello $i from $HOSTNAME"; sleep 0.3; done`)

	waitFor(t, "a CrashLoopBackOff alert", 180*time.Second, func() bool {
		for _, al := range a.State().Alerts {
			if al.Reason == "CrashLoopBackOff" && strings.HasPrefix(al.Name, "crasher-") {
				return true
			}
		}
		return false
	})
	waitFor(t, "its notification", 10*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, n := range notes {
			if strings.Contains(n.Title, "CrashLoopBackOff") && strings.Contains(n.Body, "crasher-") {
				return true
			}
		}
		return false
	})
	mu.Lock()
	t.Logf("notifications: %+v", notes)
	mu.Unlock()

	// ---- The logs of a workload ----
	waitFor(t, "the web pods to run", 120*time.Second, func() bool {
		pods, err := cn.cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: "app=web"})
		if err != nil {
			return false
		}
		running := 0
		for _, p := range pods.Items {
			if p.Status.Phase == corev1.PodRunning {
				running++
			}
		}
		return running == 2
	})
	time.Sleep(2 * time.Second)
	k := &Kube{clusters: c, timeline: tl}
	lctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	var batches [][]LogLine
	err = k.workloadLogs(lctx, id, ns, map[string]string{"app": "web"}, "", LogOptions{TailLines: 20, Follow: true}, func(b []LogLine) error {
		batches = append(batches, b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	pods := map[string]int{}
	total := 0
	for _, b := range batches {
		for _, l := range b {
			if !l.Note {
				pods[l.Pod]++
				total++
			}
		}
	}
	if len(pods) != 2 {
		t.Fatalf("lines came from %d pods, want 2: %v", len(pods), pods)
	}
	if len(batches) > 0 {
		first := batches[0]
		for i := 1; i < len(first); i++ {
			if first[i].Time < first[i-1].Time {
				t.Fatalf("the first batch is not in time order at %d", i)
			}
		}
	}
	t.Logf("workload logs: %d lines in %d batches from %v", total, len(batches), pods)

	// ---- The timeline, an impact preview and a report ----
	dep := GVR{Group: "apps", Version: "v1", Resource: "deployments"}
	if err := k.Scale(ctx, id, dep, ns, "web", 3); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the scale in the timeline", 30*time.Second, func() bool {
		list, _ := tl.Query(TimelineQuery{Cluster: id, Namespace: ns, Kind: "Deployment", Name: "web"})
		for _, ch := range list {
			if strings.Contains(strings.Join(ch.Summary, ""), "replicas 2 → 3") && ch.Actor == "k8s-client" {
				return true
			}
		}
		return false
	})
	list, _ := tl.Query(TimelineQuery{Cluster: id, Namespace: ns})
	kinds := map[string]int{}
	for _, ch := range list {
		kinds[string(ch.Type)+" "+ch.Kind]++
	}
	t.Logf("timeline: %d changes %v", len(list), kinds)
	if kinds["event Pod"] == 0 {
		t.Errorf("no warning events of pods in the timeline")
	}
	items, err := k.Impact(ctx, id, ImpactRequest{Action: ImpactDelete, Kind: "Deployment", Namespace: ns, Name: "web"})
	if err != nil || len(items) == 0 || !strings.Contains(items[0].Text, "pods are deleted") {
		t.Fatalf("impact: %v %+v", err, items)
	}
	rep, err := k.Report(ctx, id, "Deployment", ns, "crasher")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# Deployment " + ns + "/crasher", "CrashLoopBackOff", "boom", "before its last restart", "## Recent changes"} {
		if !strings.Contains(rep.Markdown, want) {
			t.Errorf("the report lacks %q:\n%s", want, rep.Markdown)
		}
	}

	// ---- Read-only clusters ----
	if err := c.Update(id, ClusterPrefs{ReadOnly: true}); err != nil {
		t.Fatal(err)
	}
	for name, err := range map[string]error{
		"delete":  k.Delete(ctx, id, dep, ns, "web", false),
		"scale":   k.Scale(ctx, id, dep, ns, "web", 0),
		"restart": k.Restart(ctx, id, dep, ns, "web"),
		"apply":   func() error { _, err := k.Apply(ctx, id, "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\n", ns); return err }(),
		"cordon":  k.Cordon(ctx, id, "any", true),
	} {
		if !errors.Is(err, errReadOnly) {
			t.Errorf("%s on a read-only cluster: %v, want errReadOnly", name, err)
		}
	}
	if d, err := cn.cs.AppsV1().Deployments(ns).Get(ctx, "web", metav1.GetOptions{}); err != nil || *d.Spec.Replicas != 3 {
		t.Fatalf("the deployment changed on a read-only cluster: %v", err)
	}
	if err := c.Update(id, ClusterPrefs{}); err != nil {
		t.Fatal(err)
	}
	if err := k.Scale(ctx, id, dep, ns, "web", 1); err != nil {
		t.Fatalf("scale once writable again: %v", err)
	}

	// Stopping to watch stops the watch.
	if err := a.SetWatched(id, ns, false); err != nil {
		t.Fatal(err)
	}
	if n := len(a.State().Targets); n != 0 {
		t.Fatalf("%d targets after unwatching", n)
	}
}

func waitFor(t *testing.T, what string, limit time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
