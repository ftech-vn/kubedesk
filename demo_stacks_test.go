package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
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
