package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestDemoStacks installs the stacks of KD_STACKS (ids, comma separated)
// into a throwaway cluster, checks them, and with KD_STACK_UNINSTALL=1
// uninstalls them. Run it with K8S_DEMO=1.
func TestDemoStacks(t *testing.T) {
	if os.Getenv("K8S_DEMO") == "" || os.Getenv("KD_STACKS") == "" {
		t.Skip("set K8S_DEMO=1, KD_STACKS=valkey,… and KUBECONFIG to a throwaway cluster")
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
	s := newStacks(&Kube{clusters: c, timeline: newTimeline()})
	ctx := context.Background()
	wait := func(job string) StackJob {
		for {
			for _, j := range s.Jobs() {
				if j.ID == job && j.State != "running" {
					s.mu.Lock()
					j.Output = s.jobs[job].Output
					s.mu.Unlock()
					return j
				}
			}
			time.Sleep(time.Second)
		}
	}
	values := map[string]map[string]string{
		"ingress": {"ingressClass": "traefik-stack", "serviceType": "NodePort"},
	}
	for _, sid := range strings.Split(os.Getenv("KD_STACKS"), ",") {
		start := time.Now()
		job, err := s.Install(ctx, id, StackRequest{Stack: sid, Values: values[sid]})
		if err != nil {
			t.Fatalf("%s: %v", sid, err)
		}
		j := wait(job)
		t.Logf("%s: %s in %s", sid, j.State, time.Since(start).Round(time.Second))
		if j.State != "done" {
			t.Errorf("%s: %s\n%s", sid, j.Error, strings.Join(j.Output, "\n"))
			continue
		}
		list, err := s.Installed(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		var is *InstalledStack
		for i := range list {
			if list[i].ID == sid {
				is = &list[i]
			}
		}
		if is == nil || is.Status != "installed" {
			t.Fatalf("%s: not recorded as installed: %+v", sid, list)
		}
		for i, a := range is.Access {
			if a.Password != "" || a.PasswordSecret != "" {
				if p, err := s.Password(ctx, id, is.Namespace, sid, i); err != nil || p == "" {
					t.Errorf("%s: password of %s: %v", sid, a.Label, err)
				}
			}
			if pod, port, err := servicePod(ctx, mustConn(t, c, id), is.Namespace, a.Service, a.Port); err != nil {
				t.Errorf("%s: %s: %v", sid, a.Label, err)
			} else {
				t.Logf("%s: %s is pod %s port %d", sid, a.Label, pod, port)
			}
		}
		// Running again updates the stack with the values it has.
		plan, err := s.Plan(ctx, id, StackRequest{Stack: sid})
		if err != nil || plan.Installed == nil {
			t.Errorf("%s: plan again: %v %+v", sid, err, plan.Installed)
		}
		if os.Getenv("KD_STACK_UNINSTALL") != "" {
			job, err := s.Uninstall(ctx, id, is.Namespace, sid, true)
			if err != nil {
				t.Fatal(err)
			}
			if j := wait(job); j.State != "done" {
				t.Errorf("%s uninstall: %s\n%s", sid, j.Error, strings.Join(j.Output, "\n"))
			}
		}
	}
}

// TestDemoStackPrune runs a stack of the user's with a domain, then again
// without it, and checks that its Ingress goes. Run it with K8S_DEMO=1.
func TestDemoStackPrune(t *testing.T) {
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
	s := newStacks(&Kube{clusters: c, timeline: newTimeline()})
	if _, err := s.Save(`id: prune-demo
name: Prune demo
fields:
  - {key: domain, type: domain}
  - {key: extra, type: bool, default: "true"}
components:
  - name: web
    manifest: |
      apiVersion: v1
      kind: ConfigMap
      metadata: {name: web}
      data: {domain: {{ quote .domain }}}
      {{- if .domain }}
      ---
      apiVersion: networking.k8s.io/v1
      kind: Ingress
      metadata: {name: web}
      spec:
        rules:
          - host: {{ .domain }}
            http:
              paths:
                - {path: /, pathType: Prefix, backend: {service: {name: web, port: {number: 80}}}}
      {{- end }}
  - name: extra
    when: extra
    manifest: |
      apiVersion: v1
      kind: ConfigMap
      metadata: {name: extra}
access:
  - {label: Web, service: web, port: 80, domain: domain}
`, ""); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cn := mustConn(t, c, id)
	run := func(values map[string]string) {
		job, err := s.Install(ctx, id, StackRequest{Stack: "prune-demo", Namespace: "prune-demo", Values: values})
		if err != nil {
			t.Fatal(err)
		}
		for {
			var j *StackJob
			for _, x := range s.Jobs() {
				if x.ID == job && x.State != "running" {
					j = &x
				}
			}
			if j != nil {
				if j.State != "done" {
					t.Fatalf("%s: %s", j.State, j.Error)
				}
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
	}
	run(map[string]string{"domain": "web.example.com"})
	if _, err := cn.cs.NetworkingV1().Ingresses("prune-demo").Get(ctx, "web", metav1.GetOptions{}); err != nil {
		t.Fatalf("no ingress after the first run: %v", err)
	}
	list, _ := s.Installed(ctx, id)
	if len(list) != 1 || len(list[0].Links) != 1 || list[0].Links[0].URL != "http://web.example.com" {
		t.Errorf("links: %+v", list)
	}
	run(map[string]string{"domain": "", "extra": "false"})
	if _, err := cn.cs.NetworkingV1().Ingresses("prune-demo").Get(ctx, "web", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("the ingress stayed: %v", err)
	}
	if _, err := cn.cs.CoreV1().ConfigMaps("prune-demo").Get(ctx, "extra", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("the component turned off stayed: %v", err)
	}
	if _, err := cn.cs.CoreV1().ConfigMaps("prune-demo").Get(ctx, "web", metav1.GetOptions{}); err != nil {
		t.Errorf("the config map went: %v", err)
	}
	job, err := s.Uninstall(ctx, id, "prune-demo", "prune-demo", true)
	if err != nil {
		t.Fatal(err)
	}
	for done := false; !done; time.Sleep(500 * time.Millisecond) {
		for _, x := range s.Jobs() {
			if x.ID == job && x.State != "running" {
				done = true
			}
		}
	}
}

// TestDemoStackHelm installs a stack with a small chart, whatever the
// user's helm repositories are. Run it with K8S_DEMO=1.
func TestDemoStackHelm(t *testing.T) {
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
	s := newStacks(&Kube{clusters: c, timeline: newTimeline()})
	if _, err := s.Save(`id: helm-demo
name: Helm demo
components:
  - name: podinfo
    helm:
      repo: https://stefanprodan.github.io/podinfo
      chart: podinfo
      version: 6.9.2
      values: |
        replicaCount: 1
`, ""); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	job, err := s.Install(ctx, id, StackRequest{Stack: "helm-demo"})
	if err != nil {
		t.Fatal(err)
	}
	for {
		var j *StackJob
		for _, x := range s.Jobs() {
			if x.ID == job && x.State != "running" {
				j = &x
			}
		}
		if j != nil {
			if j.State != "done" {
				s.mu.Lock()
				out := s.jobs[job].Output
				s.mu.Unlock()
				t.Fatalf("%s: %s\n%s", j.State, j.Error, strings.Join(out, "\n"))
			}
			break
		}
		time.Sleep(time.Second)
	}
	job, _ = s.Uninstall(ctx, id, "helm-demo", "helm-demo", true)
	for done := false; !done; time.Sleep(500 * time.Millisecond) {
		for _, x := range s.Jobs() {
			if x.ID == job && x.State != "running" {
				done = true
			}
		}
	}
}
