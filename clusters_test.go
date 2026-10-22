package main

import (
	"os"
	"path/filepath"
	"testing"
)

const testKubeconfig = `apiVersion: v1
kind: Config
clusters:
- name: default
  cluster:
    server: https://10.0.0.1:6443
    insecure-skip-tls-verify: true
contexts:
- name: default
  context: {cluster: default, user: default}
- name: other
  context: {cluster: default, user: default, namespace: apps}
users:
- name: default
  user: {token: abc}
current-context: default
`

func newTestClusters(t *testing.T) *Clusters {
	t.Helper()
	// Keep the machine's kubeconfig out of the test.
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "none"))
	shellEnvOnce.Do(func() {})
	c := newClusters()
	c.dir = t.TempDir()
	c.store.Prefs = map[string]ClusterPrefs{}
	c.store.Namespaces = map[string]string{}
	if err := os.MkdirAll(c.storedDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAddKubeconfigAndRemove(t *testing.T) {
	c := newTestClusters(t)
	added, err := c.AddKubeconfig("prod", testKubeconfig)
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 2 {
		t.Fatalf("added %d clusters, want 2", len(added))
	}
	names := map[string]bool{}
	for _, cl := range added {
		names[cl.Name] = true
	}
	if !names["prod/default"] || !names["prod/other"] {
		t.Fatalf("aliases = %v", names)
	}

	if err := c.Remove(added[0].ID); err != nil {
		t.Fatal(err)
	}
	if got := len(c.List().Clusters); got != 1 {
		t.Fatalf("%d clusters after removing one, want 1", got)
	}
	if err := c.Remove(c.List().Clusters[0].ID); err != nil {
		t.Fatal(err)
	}
	if got := len(c.List().Clusters); got != 0 {
		t.Fatalf("%d clusters after removing both, want 0", got)
	}
	entries, _ := os.ReadDir(c.storedDir())
	if len(entries) != 0 {
		t.Fatalf("the stored kubeconfig was not deleted: %v", entries)
	}
}

func TestAddFileHideAndForget(t *testing.T) {
	c := newTestClusters(t)
	path := filepath.Join(t.TempDir(), "team.yaml")
	if err := os.WriteFile(path, []byte(testKubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	added, err := c.AddFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddFile(path); err == nil {
		t.Fatal("adding the same file twice succeeded")
	}
	if err := c.SetHidden(added[0].ID, true); err != nil {
		t.Fatal(err)
	}
	hidden := 0
	for _, cl := range c.List().Clusters {
		if cl.Hidden {
			hidden++
		}
	}
	if hidden != 1 {
		t.Fatalf("%d hidden, want 1", hidden)
	}
	// Removing the last shown context stops reading the file, which stays.
	if err := c.Remove(added[1].ID); err != nil {
		t.Fatal(err)
	}
	if got := len(c.List().Clusters); got != 0 {
		t.Fatalf("%d clusters after forgetting the file, want 0", got)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the user's kubeconfig was touched: %v", err)
	}
}

func TestAddManual(t *testing.T) {
	c := newTestClusters(t)
	added, err := c.AddManual(ManualCluster{Name: "staging", Server: "10.1.2.3:6443", Token: "t", Insecure: true, Namespace: "web"})
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 || added[0].Name != "staging" || added[0].Server != "https://10.1.2.3:6443" || added[0].Namespace != "web" {
		t.Fatalf("added = %+v", added)
	}
	if _, err := c.AddManual(ManualCluster{Name: "x"}); err == nil {
		t.Fatal("a cluster without a server was added")
	}
}

func TestAddKubeconfigRejectsBrokenFiles(t *testing.T) {
	c := newTestClusters(t)
	for _, bad := range []string{"", "not: [yaml", "apiVersion: v1\nkind: Config\n"} {
		if _, err := c.AddKubeconfig("", bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
