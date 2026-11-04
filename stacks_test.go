package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Every built-in stack reads and renders with its defaults.
func TestBuiltinStacks(t *testing.T) {
	files, _ := fs.Glob(builtinStacks, "stacks/*.yaml")
	if len(files) == 0 {
		t.Fatal("no built-in stacks")
	}
	for _, p := range files {
		data, _ := builtinStacks.ReadFile(p)
		st, err := parseStack(data)
		if err != nil {
			t.Errorf("%s: %v", p, err)
			continue
		}
		if p != "stacks/"+st.ID+".yaml" {
			t.Errorf("%s: the file of stack %s is named after its id", p, st.ID)
		}
		values, errs := resolveValues(st, nil, nil)
		if len(errs) > 0 {
			t.Errorf("%s: defaults are wrong: %v", st.ID, errs)
		}
		// Every component runs, the optional ones too, with every
		// optional part filled in.
		fillAll(st, values)
		for _, c := range planStack(st, st.Namespace, values, nil) {
			if c.Error != "" {
				t.Errorf("%s/%s: %s", st.ID, c.Name, c.Error)
			}
			if c.Kind == "manifest" {
				if _, err := decodeManifest(c.Content); err != nil {
					t.Errorf("%s/%s: %v", st.ID, c.Name, err)
				}
			}
		}
		if _, err := renderStackTemplate("notes", st.Notes, stackData(st, st.Namespace, values)); err != nil {
			t.Errorf("%s notes: %v", st.ID, err)
		}
	}
}

// With KD_STACK_HELM=1, the charts of the built-in stacks render with
// their values, through helm template, which downloads them.
func TestBuiltinStackCharts(t *testing.T) {
	if os.Getenv("KD_STACK_HELM") == "" {
		t.Skip("set KD_STACK_HELM=1 to render the charts with helm")
	}
	only := os.Getenv("KD_STACK")
	s := &Stacks{kube: &Kube{clusters: &Clusters{}}}
	for _, st := range s.Catalog().Stacks {
		if only != "" && st.ID != only {
			continue
		}
		values, _ := resolveValues(st, nil, nil)
		fillAll(st, values)
		for i, c := range planStack(st, st.Namespace, values, nil) {
			if c.Kind != "helm" {
				continue
			}
			h := st.Components[i].Helm
			f, _ := os.CreateTemp(t.TempDir(), "values-*.yaml")
			f.WriteString(c.Content)
			f.Close()
			args := []string{"template", c.Name, h.Chart, "--namespace", st.Namespace, "--values", f.Name()}
			if h.Repo != "" {
				args = append(args, "--repo", h.Repo)
			}
			if h.Version != "" {
				args = append(args, "--version", h.Version)
			}
			out, err := exec.Command("helm", args...).CombinedOutput()
			if err != nil {
				t.Errorf("%s/%s: %s", st.ID, c.Name, out)
			}
		}
	}
}

func TestResolveValues(t *testing.T) {
	st := Stack{ID: "x", Name: "X", Components: []StackComponent{{Name: "a", Manifest: "kind: X"}}, Fields: []StackField{
		{Key: "size", Type: "number", Default: "3"},
		{Key: "pass", Type: "password", Generate: true},
		{Key: "name", Required: true},
		{Key: "on", Type: "bool"},
		{Key: "mode", Type: "select", Options: []string{"a", "b"}, Default: "a"},
	}}
	if err := checkStack(&st); err != nil {
		t.Fatal(err)
	}
	v, errs := resolveValues(st, map[string]string{"size": "x", "mode": "c"}, map[string]string{"pass": "kept"})
	if errs["size"] == "" || errs["name"] != "required" || errs["mode"] == "" {
		t.Errorf("errors: %v", errs)
	}
	if v["pass"] != "kept" || v["on"] != "false" {
		t.Errorf("values: %v", v)
	}
	v, _ = resolveValues(st, nil, nil)
	if len(v["pass"]) != 20 || v["size"] != "3" {
		t.Errorf("defaults: %v", v)
	}
}

func TestCheckStack(t *testing.T) {
	bad := map[string]string{
		"id":         "id: Bad_ID\nname: x\ncomponents: [{name: a, manifest: 'kind: X'}]",
		"no kind":    "id: a\nname: x\ncomponents: [{name: a}]",
		"two kinds":  "id: a\nname: x\ncomponents: [{name: a, manifest: 'kind: X', urls: [https://x]}]",
		"when":       "id: a\nname: x\ncomponents: [{name: a, manifest: 'kind: X', when: nope}]",
		"template":   "id: a\nname: x\ncomponents: [{name: a, manifest: '{{ .x '}]",
		"typo":       "id: a\nname: x\ncomponent: []",
		"repo":       "id: a\nname: x\ncomponents: [{name: a, helm: {chart: c}}]",
		"access pwd": "id: a\nname: x\nfields: [{key: p}]\ncomponents: [{name: a, manifest: 'kind: X'}]\naccess: [{label: A, service: s, port: 80, password: p}]",
	}
	for name, src := range bad {
		if _, err := parseStack([]byte(src)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	st, err := parseStack([]byte("id: a\nname: x\ncomponents: [{name: a, helm: {chart: 'oci://r/c'}}]"))
	if err != nil || st.Namespace != "a" || !st.NeedsHelm || st.Category != "Other" {
		t.Errorf("%+v %v", st, err)
	}
}

func TestRenderManifest(t *testing.T) {
	st, err := parseStack([]byte(`id: a
name: A
fields:
  - {key: replicas, type: number, default: "2"}
  - {key: debug, type: bool}
  - {key: pass, type: password, default: 'a"b'}
components:
  - name: app
    manifest: |
      apiVersion: v1
      kind: Secret
      metadata: {name: s}
      stringData:
        pass: {{ quote .pass }}
      ---
      apiVersion: apps/v1
      kind: Deployment
      metadata: {name: d, namespace: {{ .Namespace }}}
      spec:
        replicas: {{ .replicas }}
        {{- if .debug }}
        paused: true
        {{- end }}
  - name: raw
    noTemplate: true
    manifest: |
      apiVersion: v1
      kind: ConfigMap
      metadata: {name: c}
      data: {t: "{{ $labels.x }}"}
`))
	if err != nil {
		t.Fatal(err)
	}
	values, _ := resolveValues(st, nil, nil)
	plan := planStack(st, "ns1", values, nil)
	objs, err := decodeManifest(plan[0].Content)
	if err != nil || len(objs) != 2 {
		t.Fatalf("%v %d\n%s", err, len(objs), plan[0].Content)
	}
	if p, _, _ := unstructured.NestedString(objs[0].Object, "stringData", "pass"); p != `a"b` {
		t.Errorf("password %q", p)
	}
	if n, _, _ := unstructured.NestedFieldNoCopy(objs[1].Object, "spec", "replicas"); fmt.Sprint(n) != "2" || objs[1].GetNamespace() != "ns1" {
		t.Errorf("deployment %v", objs[1].Object)
	}
	if !strings.Contains(plan[1].Content, "$labels.x") {
		t.Errorf("noTemplate rendered: %s", plan[1].Content)
	}
	plan = planStack(st, "ns1", values, map[string]string{"app": "kind: [oops"})
	if !plan[0].Overridden || plan[0].Error == "" {
		t.Errorf("override %+v", plan[0])
	}
}

func TestDecodeManifestOrder(t *testing.T) {
	objs, err := decodeManifest(`apiVersion: example.com/v1
kind: Thing
metadata: {name: t}
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata: {name: things.example.com}
---
apiVersion: v1
kind: List
items:
  - apiVersion: v1
    kind: Namespace
    metadata: {name: n}
`)
	if err != nil || len(objs) != 3 {
		t.Fatal(err, len(objs))
	}
	if objs[0].GetKind() != "Namespace" || objs[1].GetKind() != "CustomResourceDefinition" || objs[2].GetKind() != "Thing" {
		t.Errorf("order: %s %s %s", objs[0].GetKind(), objs[1].GetKind(), objs[2].GetKind())
	}
}

func TestReadiness(t *testing.T) {
	obj := func(kind string, status map[string]any, spec map[string]any) *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "x/v1", "kind": kind, "metadata": map[string]any{"name": "a"}, "spec": spec, "status": status}}
	}
	cases := []struct {
		u      *unstructured.Unstructured
		waited time.Duration
		ready  bool
	}{
		{obj("Deployment", map[string]any{"availableReplicas": int64(1), "updatedReplicas": int64(1)}, map[string]any{"replicas": int64(2)}), 0, false},
		{obj("Deployment", map[string]any{"availableReplicas": int64(2), "updatedReplicas": int64(2)}, map[string]any{"replicas": int64(2)}), 0, true},
		{obj("DaemonSet", map[string]any{"desiredNumberScheduled": int64(3), "numberReady": int64(3), "updatedNumberScheduled": int64(3)}, nil), 0, true},
		{obj("Cluster", nil, nil), time.Minute, false},
		{obj("Cluster", nil, nil), 3 * time.Minute, true},
		{obj("Cluster", map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": "False"}}}, nil), 0, false},
		{obj("Elasticsearch", map[string]any{"health": "green"}, nil), 0, true},
		{obj("Elasticsearch", map[string]any{"health": "red"}, nil), 0, false},
		{obj("Kafka", map[string]any{"phase": "Pending"}, nil), 0, false},
	}
	for i, c := range cases {
		if ok, why := readiness(c.u, c.waited); ok != c.ready {
			t.Errorf("case %d (%s): ready=%v (%s)", i, c.u.GetKind(), ok, why)
		}
	}
}

func TestDomainFields(t *testing.T) {
	st, err := parseStack([]byte(`id: a
name: A
fields:
  - {key: domain, type: domain}
  - {key: issuer, type: clusterIssuer, showIf: domain}
  - {key: email, type: email, required: true, showIf: issuer}
  - {key: code, pattern: '^[A-Z]{3}$'}
components: [{name: a, manifest: 'kind: X'}]
access:
  - {label: Web, service: web, port: 80, domain: domain, tls: issuer, path: /ui}
`))
	if err != nil {
		t.Fatal(err)
	}
	// Without a domain, nothing below it is asked or checked.
	if _, errs := resolveValues(st, map[string]string{}, nil); len(errs) != 0 {
		t.Errorf("hidden fields checked: %v", errs)
	}
	_, errs := resolveValues(st, map[string]string{"domain": "not a domain", "code": "abc"}, nil)
	if errs["domain"] == "" || errs["code"] == "" {
		t.Errorf("errors: %v", errs)
	}
	v, errs := resolveValues(st, map[string]string{"domain": "Grafana.Example.com", "issuer": "letsencrypt"}, nil)
	if errs["email"] != "required" || v["domain"] != "grafana.example.com" {
		t.Errorf("%v %v", v, errs)
	}
	if links := stackLinks(st, v); len(links) != 1 || links[0].URL != "https://grafana.example.com/ui" {
		t.Errorf("links %+v", links)
	}
	v["issuer"] = ""
	if links := stackLinks(st, v); links[0].URL != "http://grafana.example.com/ui" {
		t.Errorf("links without tls %+v", links)
	}
	for _, bad := range []string{
		"id: a\nname: x\nfields: [{key: d}]\ncomponents: [{name: a, manifest: 'kind: X'}]\naccess: [{label: A, service: s, port: 80, domain: d}]",
		"id: a\nname: x\nfields: [{key: d, showIf: nope}]\ncomponents: [{name: a, manifest: 'kind: X'}]",
		"id: a\nname: x\nfields: [{key: d, type: url}]\ncomponents: [{name: a, manifest: 'kind: X'}]",
	} {
		if _, err := parseStack([]byte(bad)); err == nil {
			t.Errorf("accepted: %s", bad)
		}
	}
}

// fillAll turns every bool on and fills in the optional fields, so that
// templates render all their parts.
func fillAll(st Stack, values map[string]string) {
	for _, f := range st.Fields {
		switch f.Type {
		case "bool":
			values[f.Key] = "true"
		case "domain":
			values[f.Key] = strings.ToLower(f.Key) + ".example.com"
		case "email":
			values[f.Key] = "ops@example.com"
		case "ingressClass":
			values[f.Key] = "nginx"
		case "clusterIssuer":
			values[f.Key] = "letsencrypt"
		case "storageClass":
			values[f.Key] = "fast"
		}
	}
}

func TestPrivateAddress(t *testing.T) {
	for addr, want := range map[string]bool{
		"10.0.0.4": true, "172.16.3.1": true, "192.168.1.9": true, "100.72.0.1": true, "127.0.0.1": true, "fd00::1": true,
		"172.173.76.191": false, "8.8.8.8": false, "lb.example.com": false, "ip-10-0-0-1.ec2.internal": true,
	} {
		if privateAddress(addr) != want {
			t.Errorf("%s: private=%v", addr, !want)
		}
	}
}
