package main

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestDemoV06 runs the Helm, access and health checks of 0.6 against a
// throwaway cluster with a Helm release "shop" of two revisions in
// namespace demo, and the kubeconfig of a user who may only read pods
// there, in $K8S_DEMO_VIEWER. Run it with K8S_DEMO=1.
func TestDemoV06(t *testing.T) {
	if os.Getenv("K8S_DEMO") == "" {
		t.Skip("set K8S_DEMO=1, and KUBECONFIG to a throwaway cluster")
	}
	c := newClusters()
	c.dir = t.TempDir()
	c.store.Prefs = map[string]ClusterPrefs{}
	c.store.Namespaces = map[string]string{}
	_ = os.MkdirAll(c.storedDir(), 0o700)
	var id string
	for _, cl := range c.List().Clusters {
		if cl.Current {
			id = cl.ID
		}
	}
	k := &Kube{clusters: c, timeline: newTimeline()}
	h := &Helm{kube: k}
	ctx := context.Background()

	list, err := h.Releases(ctx, id, "")
	if err != nil || len(list) != 1 || list[0].Name != "shop" || list[0].Revision != 2 || list[0].Chart != "shop" {
		t.Fatalf("releases: %v %+v", err, list)
	}
	d, err := h.Release(ctx, id, "demo", "shop", 0)
	if err != nil || len(d.History) != 2 || !strings.Contains(d.Values, "replicaCount: 2") || !strings.Contains(d.AllValues, "repository: nginx") || len(d.Objects) < 3 {
		t.Fatalf("release: %v values %q objects %d history %d", err, d.Values, len(d.Objects), len(d.History))
	}
	t.Logf("objects: %+v", d.Objects)
	plan, err := h.PlanRollback(ctx, id, "demo", "shop", 1)
	if err != nil || plan.HelmMissing {
		t.Fatalf("plan: %v %+v", err, plan)
	}
	changed := 0
	for _, df := range plan.Diffs {
		if df.Action == "update" {
			changed++
			t.Logf("rollback changes %s %s", df.Kind, df.Name)
		}
	}
	// Revision 1 differs in the deployment's replicas and image only.
	if changed != 1 {
		t.Errorf("the rollback plan changes %d objects, want 1: %+v", changed, plan.Diffs)
	}
	out, err := h.Rollback(ctx, id, "demo", "shop", 1)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	t.Logf("helm: %s", out)
	d, err = h.Release(ctx, id, "demo", "shop", 0)
	if err != nil || d.Revision != 3 || d.Status != "deployed" || strings.Contains(d.Values, "replicaCount: 2") {
		t.Fatalf("after rollback: %v rev %d %s values %q", err, d.Revision, d.Status, d.Values)
	}
	sum, err := helmSummary(ctx, mustConn(t, c, id).cs, "demo", "shop")
	if err != nil || !strings.Contains(sum, "Rollback to 1") {
		t.Errorf("summary: %v\n%s", err, sum)
	}

	// A read-only cluster refuses the rollback.
	c.store.Prefs[id] = ClusterPrefs{ReadOnly: true}
	if _, err := h.Rollback(ctx, id, "demo", "shop", 2); err == nil {
		t.Error("a read-only cluster rolled back")
	}
	c.store.Prefs[id] = ClusterPrefs{}

	// The admin may do anything; the viewer only reads pods.
	admin, err := k.Can(ctx, id, []AccessCheck{{Verb: "delete", Resource: "pods", Namespace: "demo"}})
	if err != nil || !admin[0] {
		t.Errorf("admin: %v %v", err, admin)
	}
	viewerFile := os.Getenv("K8S_DEMO_VIEWER")
	added, err := c.AddFile(viewerFile)
	if err != nil {
		t.Fatal(err)
	}
	got, err := k.Can(ctx, added[0].ID, []AccessCheck{
		{Verb: "list", Resource: "pods", Namespace: "demo"},
		{Verb: "delete", Resource: "pods", Namespace: "demo"},
		{Verb: "create", Resource: "pods", Subresource: "exec", Namespace: "demo"},
		{Verb: "list", Resource: "pods", Namespace: "kube-system"},
		{Verb: "patch", Group: "apps", Resource: "deployments", Namespace: "demo"},
	})
	if err != nil || strings.Join(boolWords(got), ",") != "yes,no,no,no,no" {
		t.Errorf("viewer: %v %v", err, got)
	}
	// The viewer cannot read Secrets, where Helm keeps its releases.
	if _, err := h.Releases(ctx, added[0].ID, "demo"); err == nil || !strings.Contains(err.Error(), "needs access to Secrets") {
		t.Errorf("viewer releases: %v", err)
	}

	// The token of the viewer expires in two hours.
	for _, hl := range c.Health() {
		if hl.ID == added[0].ID {
			t.Logf("viewer health: %+v", hl.Issues)
			found := false
			for _, is := range hl.Issues {
				found = found || strings.Contains(is.Text, "token expires today")
			}
			if !found {
				t.Errorf("no warning of the token: %+v", hl.Issues)
			}
		}
	}
}

func mustConn(t *testing.T, c *Clusters, id string) *conn {
	t.Helper()
	cn, err := c.conn(id)
	if err != nil {
		t.Fatal(err)
	}
	return cn
}

func boolWords(b []bool) []string {
	var out []string
	for _, x := range b {
		if x {
			out = append(out, "yes")
		} else {
			out = append(out, "no")
		}
	}
	return out
}
