package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
)

// TestDemoAgents runs the MCP server against a throwaway cluster, the
// current context of $KUBECONFIG, over a real socket: reads, a search, a
// comparison, a change the user approves and one they reject, and the
// stdio bridge. Run it with K8S_DEMO=1.
func TestDemoAgents(t *testing.T) {
	if os.Getenv("K8S_DEMO") == "" {
		t.Skip("set K8S_DEMO=1, and KUBECONFIG to a throwaway cluster")
	}
	// A second context of the same cluster, to compare two "clusters".
	raw, err := clientcmd.NewDefaultClientConfigLoadingRules().Load()
	if err != nil {
		t.Fatal(err)
	}
	cur := raw.Contexts[raw.CurrentContext]
	twin := *cur
	raw.Contexts["twin"] = &twin
	merged := filepath.Join(t.TempDir(), "kubeconfig")
	if err := clientcmd.WriteToFile(*raw, merged); err != nil {
		t.Fatal(err)
	}
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
	cn, err := c.conn(id)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ns := fmt.Sprintf("kd-agents-%d", time.Now().Unix()%100000)
	if _, err := cn.cs.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cn.cs.CoreV1().Namespaces().Delete(context.Background(), ns, metav1.DeleteOptions{}) })
	labels := map[string]string{"app": "echo"}
	one := int32(1)
	if _, err := cn.cs.AppsV1().Deployments(ns).Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "echo"},
		Spec: appsv1.DeploymentSpec{Replicas: &one, Selector: &metav1.LabelSelector{MatchLabels: labels}, Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: labels},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "echo", Image: "busybox:1.36", Command: []string{"sh", "-c", "while true; do echo token=hunter2secret ok; sleep 1; done"}}}},
		}},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	k := &Kube{clusters: c, timeline: newTimeline()}
	a := newAgents(k, c, func() {})
	a.publish = func() {}
	requests := make(chan ApprovalRequest, 4)
	a.notify = func(r ApprovalRequest) { requests <- r }
	_ = a.SetAccess(id, AgentWrite)
	_ = a.SetAccess(twinID, AgentRead)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	srv := &http.Server{Handler: a.handler(port, "tok")}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	cfgPath := filepath.Join(t.TempDir(), "mcp.json")
	b, _ := json.Marshal(bridgeConfig{URL: fmt.Sprintf("http://127.0.0.1:%d/mcp", port), Token: "tok"})
	_ = os.WriteFile(cfgPath, b, 0o600)

	// Everything goes through the stdio bridge's forwarding, as a client
	// would use it.
	client := &http.Client{Timeout: time.Minute}
	session := ""
	launched := true
	seq := 0
	rpc := func(method string, params any) map[string]any {
		seq++
		msg, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": seq, "method": method, "params": params})
		var out map[string]any
		if err := json.Unmarshal(forward(client, cfgPath, msg, &session, &launched), &out); err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		return out
	}
	call := func(name string, args map[string]any) (string, bool) {
		out := rpc("tools/call", map[string]any{"name": name, "arguments": args})
		r, ok := out["result"].(map[string]any)
		if !ok {
			t.Fatalf("%s: %v", name, out)
		}
		isErr, _ := r["isError"].(bool)
		return r["content"].([]any)[0].(map[string]any)["text"].(string), isErr
	}
	rpc("initialize", map[string]any{"protocolVersion": "2025-06-18", "clientInfo": map[string]any{"name": "test-agent"}})
	if session == "" {
		t.Fatal("no session")
	}

	waitFor(t, "the echo pod to run", 120*time.Second, func() bool {
		text, _ := call("list_resources", map[string]any{"cluster": id, "kind": "po", "namespace": ns})
		return strings.Contains(text, "Running, 1/1 ready")
	})
	text, isErr := call("get_resource", map[string]any{"cluster": id, "kind": "deployments", "namespace": ns, "name": "echo"})
	if isErr || !strings.Contains(text, "busybox:1.36") || strings.Contains(text, "managedFields") {
		t.Fatalf("get_resource: %s", text)
	}
	text, _ = call("list_resources", map[string]any{"cluster": id, "kind": "pods", "namespace": ns})
	var rows []struct{ Name string }
	_ = json.Unmarshal([]byte(text), &rows)
	if len(rows) == 0 {
		t.Fatalf("list_resources: %s", text)
	}
	time.Sleep(2 * time.Second)
	text, _ = call("get_logs", map[string]any{"cluster": id, "namespace": ns, "pod": rows[0].Name, "tail_lines": 5})
	if !strings.Contains(text, "token=[redacted]") || strings.Contains(text, "hunter2secret") {
		t.Fatalf("get_logs did not mask the token:\n%s", text)
	}
	text, _ = call("search", map[string]any{"query": "image:busybox ns:" + ns})
	if !strings.Contains(text, `"name": "echo"`) {
		t.Fatalf("search: %s", text)
	}
	text, _ = call("compare", map[string]any{"kind": "deployment", "namespace": ns, "name": "echo", "clusters": []string{id, twinID}})
	if !strings.Contains(text, "echo: image") || strings.Contains(text, `"differs": true`) {
		t.Fatalf("compare: %s", text)
	}

	// A change the user approves.
	answered := make(chan string, 1)
	go func() {
		text, isErr := call("scale", map[string]any{"cluster": id, "kind": "deployment", "namespace": ns, "name": "echo", "replicas": 2, "reason": "more traffic"})
		answered <- fmt.Sprint(isErr, " ", text)
	}()
	req := <-requests
	if req.Action != "scale" || req.Reason != "more traffic" || req.Client != "test-agent" || len(a.Pending()) != 1 {
		t.Fatalf("approval request: %+v", req)
	}
	if err := a.Decide(req.ID, true); err != nil {
		t.Fatal(err)
	}
	if got := <-answered; !strings.HasPrefix(got, "false Approved") {
		t.Fatalf("approved scale: %s", got)
	}
	d, _ := cn.cs.AppsV1().Deployments(ns).Get(ctx, "echo", metav1.GetOptions{})
	if *d.Spec.Replicas != 2 {
		t.Fatalf("replicas %d after the approved scale", *d.Spec.Replicas)
	}
	agentManager := false
	for _, f := range d.ManagedFields {
		agentManager = agentManager || f.Manager == "kubedesk-agent (test-agent)"
	}
	if !agentManager {
		t.Errorf("the change does not carry the agent's name: %+v", d.ManagedFields)
	}

	// A change the user rejects.
	go func() {
		text, isErr := call("delete_resource", map[string]any{"cluster": id, "kind": "deploy", "namespace": ns, "name": "echo", "reason": "cleanup"})
		answered <- fmt.Sprint(isErr, " ", text)
	}()
	req = <-requests
	if len(req.Impact) == 0 {
		t.Errorf("the delete request shows no impact")
	}
	_ = a.Decide(req.ID, false)
	if got := <-answered; !strings.Contains(got, "rejected") {
		t.Fatalf("rejected delete: %s", got)
	}
	if _, err := cn.cs.AppsV1().Deployments(ns).Get(ctx, "echo", metav1.GetOptions{}); err != nil {
		t.Fatalf("the rejected delete deleted: %v", err)
	}

	// The twin may only be read.
	if text, isErr := call("restart", map[string]any{"cluster": twinID, "kind": "deployment", "namespace": ns, "name": "echo", "reason": "x"}); !isErr || !strings.Contains(text, "may only read") {
		t.Fatalf("restart on a read cluster: %s", text)
	}

	// The app's own assistant, through a stand-in for Foundry: it looks at the
	// cluster, then proposes a scale that the user approves.
	step := 0
	var overviewSeen string
	foundrySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		step++
		switch step {
		case 1:
			sse(w, "tool_use", [2]string{`{"type":"tool_use","id":"t1","name":"cluster_overview","input":{}}`, fmt.Sprintf(`{"type":"input_json_delta","partial_json":%q}`, fmt.Sprintf(`{"cluster":%q}`, id))})
		case 2:
			overviewSeen = string(b)
			sse(w, "tool_use", [2]string{`{"type":"tool_use","id":"t2","name":"scale","input":{}}`, fmt.Sprintf(`{"type":"input_json_delta","partial_json":%q}`, fmt.Sprintf(`{"cluster":%q,"kind":"deployment","namespace":%q,"name":"echo","replicas":3,"reason":"load"}`, id, ns))})
		default:
			sse(w, "end_turn", [2]string{`{"type":"text","text":""}`, `{"type":"text_delta","text":"Scaled."}`})
		}
	}))
	defer foundrySrv.Close()
	as := newAssistant(a, c)
	as.key = func() ([]byte, error) { return []byte("k"), nil }
	_ = c.setAssistantSettings(AssistantSettings{Endpoint: foundrySrv.URL + "/anthropic", Deployment: "claude-opus-5-5"})
	_ = a.SetAccess(id, AgentOff) // the assistant does not need agents' access
	done := make(chan error, 1)
	go func() { done <- as.send(ctx, "demo", "scale echo to 3", ChatView{Cluster: id, Namespace: ns}, func(ChatEvent) error { return nil }) }()
	req = <-requests
	if req.Client != assistantClient || req.Action != "scale" {
		t.Fatalf("assistant approval: %+v", req)
	}
	_ = a.Decide(req.ID, true)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(overviewSeen, ns) || !strings.Contains(overviewSeen, "byStatus") {
		t.Errorf("the overview did not reach the model: %.300s", overviewSeen)
	}
	d, _ = cn.cs.AppsV1().Deployments(ns).Get(ctx, "echo", metav1.GetOptions{})
	if *d.Spec.Replicas != 3 {
		t.Fatalf("replicas %d after the assistant's approved scale", *d.Spec.Replicas)
	}
	assistantManager := false
	for _, f := range d.ManagedFields {
		assistantManager = assistantManager || f.Manager == "kubedesk-agent (Kube Desk assistant)"
	}
	if !assistantManager {
		t.Errorf("the change does not carry the assistant's name")
	}

	// Saving YAML shows its diff first.
	diffs, err := k.PlanApply(ctx, id, fmt.Sprintf("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: flags\n  namespace: %s\ndata:\n  beta: \"on\"\n", ns), ns)
	if err != nil || len(diffs) != 1 || diffs[0].Action != "create" {
		t.Fatalf("plan: %v %+v", err, diffs)
	}
	t.Logf("activity: %d calls", len(a.Activity()))
}
