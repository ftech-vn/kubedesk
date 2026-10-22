package main

import (
	"context"
	"os"
	"testing"
)

// TestLiveCluster reads (never changes) the current context of the default
// kubeconfig. Run it with K8S_LIVE=1.
func TestLiveCluster(t *testing.T) {
	if os.Getenv("K8S_LIVE") == "" {
		t.Skip("set K8S_LIVE=1 to read a real cluster")
	}
	c := newClusters()
	c.dir = t.TempDir()
	c.store.Prefs = map[string]ClusterPrefs{}
	c.store.Namespaces = map[string]string{}
	list := c.List()
	if len(list.Clusters) == 0 {
		t.Fatalf("no clusters: %+v", list.Errors)
	}
	var current Cluster
	for _, cl := range list.Clusters {
		t.Logf("cluster %s %q %s current=%v", cl.ID, cl.Name, cl.Server, cl.Current)
		if cl.Current {
			current = cl
		}
	}
	ctx := context.Background()
	st := c.Test(ctx, current.ID)
	t.Logf("status: %+v", st)
	if !st.OK {
		t.Skip("current cluster unreachable")
	}
	k := &Kube{clusters: c}
	ns, err := k.List(ctx, current.ID, GVR{Version: "v1", Resource: "namespaces"}, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d namespaces", len(ns))
	pods, err := k.List(ctx, current.ID, GVR{Version: "v1", Resource: "pods"}, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d pods", len(pods))
	res, err := k.Resources(ctx, current.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d resource kinds", len(res))
}

// TestLiveStatus connects to every cluster of the default kubeconfig.
func TestLiveStatus(t *testing.T) {
	if os.Getenv("K8S_LIVE") == "" {
		t.Skip("set K8S_LIVE=1 to read real clusters")
	}
	c := newClusters()
	c.dir = t.TempDir()
	c.store.Prefs = map[string]ClusterPrefs{}
	c.store.Namespaces = map[string]string{}
	for _, cl := range c.List().Clusters {
		t.Logf("%s: %+v", cl.Name, c.Test(context.Background(), cl.ID))
	}
}
