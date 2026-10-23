package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
)

// TestDemoV07 takes a snapshot of namespace demo of a throwaway cluster,
// opens it, and promotes deployment demo/web, with its config, to namespace
// promoted through a second context of the same cluster. Run it with
// K8S_DEMO=1.
func TestDemoV07(t *testing.T) {
	if os.Getenv("K8S_DEMO") == "" {
		t.Skip("set K8S_DEMO=1, and KUBECONFIG to a throwaway cluster")
	}
	raw, err := clientcmd.NewDefaultClientConfigLoadingRules().Load()
	if err != nil {
		t.Fatal(err)
	}
	twin := *raw.Contexts[raw.CurrentContext]
	raw.Contexts["twin"] = &twin
	merged := filepath.Join(t.TempDir(), "kubeconfig")
	_ = clientcmd.WriteToFile(*raw, merged)
	t.Setenv("KUBECONFIG", merged)
	c := newClusters()
	c.dir = t.TempDir()
	c.store.Prefs = map[string]ClusterPrefs{}
	c.store.Namespaces = map[string]string{}
	_ = os.MkdirAll(c.storedDir(), 0o700)
	var id, twinID string
	for _, cl := range c.List().Clusters {
		if cl.Context == "twin" {
			twinID = cl.ID
		} else if cl.Current {
			id = cl.ID
		}
	}
	k := &Kube{clusters: c, timeline: newTimeline()}
	k.timeline.external = c.snapshotChanges
	ctx := context.Background()

	// Snapshot.
	cn, _ := c.conn(id)
	d, err := buildSnapshot(ctx, cn, "k3s", "demo", SnapshotOptions{Logs: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	_ = d.write(&buf)
	if strings.Contains(buf.String(), "hunter2") || strings.Contains(buf.String(), "abc123secret") {
		t.Fatal("a secret is in the snapshot")
	}
	t.Logf("snapshot: %d objects, %d logs, %d bytes, skipped %v", d.count(), len(d.Logs), buf.Len(), d.Skipped)
	file := filepath.Join(t.TempDir(), "demo.kubedesk")
	_ = os.WriteFile(file, buf.Bytes(), 0o600)
	snap, err := c.addSnapshot(file)
	if err != nil {
		t.Fatal(err)
	}
	pods, err := k.List(ctx, snap.ID, GVR{Version: "v1", Resource: "pods"}, "demo")
	if err != nil || len(pods) != 1 {
		t.Fatalf("snapshot pods: %v %d", err, len(pods))
	}
	sc, _ := c.conn(snap.ID)
	name := pods[0]["metadata"].(map[string]any)["name"].(string)
	logs, err := openLogs(ctx, sc.cs, "demo", name, &corev1.PodLogOptions{Container: "web"})
	if err != nil {
		t.Fatal(err)
	}
	var lb bytes.Buffer
	_, _ = lb.ReadFrom(logs)
	if !strings.Contains(lb.String(), "started with live") || strings.Contains(lb.String(), "abc123secret") {
		t.Errorf("snapshot logs: %q", lb.String())
	}
	if rep, err := k.Report(ctx, snap.ID, "Deployment", "demo", "web"); err != nil || !strings.Contains(rep.Markdown, "started with live") {
		t.Errorf("report from the snapshot: %v", err)
	}
	if ov, err := clusterOverview(ctx, sc.cs); err != nil || len(ov.Nodes) != 1 {
		t.Errorf("overview of the snapshot: %v %+v", err, ov.Nodes)
	}

	// Promote, twice: create, then update.
	req := PromoteRequest{From: id, To: twinID, GVR: GVR{Group: "apps", Version: "v1", Resource: "deployments"}, Kind: "Deployment", Namespace: "demo", Name: "web", TargetNamespace: "promoted", Images: map[string]string{"web": "busybox:1.37"}, WithConfig: true, WithSecrets: true}
	plan, err := k.PlanPromote(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("plan: %d diffs, notes %v", len(plan.Diffs), plan.Notes)
	out, err := k.Promote(ctx, req)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	t.Logf("promoted: %v", out)
	svcReq := PromoteRequest{From: id, To: twinID, GVR: GVR{Version: "v1", Resource: "services"}, Kind: "Service", Namespace: "demo", Name: "web", TargetNamespace: "promoted"}
	for i := 0; i < 2; i++ {
		if out, err := k.Promote(ctx, svcReq); err != nil {
			t.Fatalf("service, round %d: %v", i, err)
		} else {
			t.Logf("service: %v", out)
		}
	}
	tc, _ := c.conn(twinID)
	dep, err := tc.cs.AppsV1().Deployments("promoted").Get(ctx, "web", metav1.GetOptions{})
	if err != nil || dep.Spec.Template.Spec.Containers[0].Image != "busybox:1.37" {
		t.Fatalf("promoted deployment: %v", err)
	}
	byPromote := false
	for _, f := range dep.ManagedFields {
		byPromote = byPromote || f.Manager == promoteManager
	}
	if !byPromote {
		t.Errorf("no field manager %s: %+v", promoteManager, dep.ManagedFields)
	}
	sec, err := tc.cs.CoreV1().Secrets("promoted").Get(ctx, "web-db", metav1.GetOptions{})
	if err != nil || string(sec.Data["url"]) != "postgres://u:hunter2@db" {
		t.Errorf("secret: %v", err)
	}
	// Promoted again, the deployment is unchanged.
	plan, _ = k.PlanPromote(ctx, req)
	for _, df := range plan.Diffs {
		t.Logf("again: %s %s %s", df.Kind, df.Name, df.Action)
	}
}
