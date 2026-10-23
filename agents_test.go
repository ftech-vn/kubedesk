package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestAgents(t *testing.T) (*Agents, http.Handler, string) {
	t.Helper()
	c := newTestClusters(t)
	path := filepath.Join(t.TempDir(), "team.yaml")
	if err := os.WriteFile(path, []byte(testKubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	added, err := c.AddFile(path)
	if err != nil {
		t.Fatal(err)
	}
	a := newAgents(&Kube{clusters: c, timeline: newTimeline()}, c, func() {})
	a.publish = func() {}
	a.notify = func(ApprovalRequest) {}
	return a, a.handler(47999, "secret-token"), added[0].ID
}

func post(t *testing.T, h http.Handler, host, token, session, body string) (*http.Response, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "http://"+host+"/mcp", strings.NewReader(body))
	req.Host = host
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if session != "" {
		req.Header.Set("Mcp-Session-Id", session)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	res := rec.Result()
	b, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return res, out
}

func TestMCPAuth(t *testing.T) {
	_, h, _ := newTestAgents(t)
	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","clientInfo":{"name":"claude-code"}}}`
	if res, _ := post(t, h, "127.0.0.1:47999", "wrong", "", init); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong token: %d", res.StatusCode)
	}
	if res, _ := post(t, h, "127.0.0.1:47999", "", "", init); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("no token: %d", res.StatusCode)
	}
	if res, _ := post(t, h, "evil.example.com:47999", "secret-token", "", init); res.StatusCode != http.StatusForbidden {
		t.Errorf("rebound host: %d", res.StatusCode)
	}
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:47999/mcp", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", rec.Code)
	}
}

func TestMCPProtocol(t *testing.T) {
	a, h, id := newTestAgents(t)
	const host = "127.0.0.1:47999"
	res, out := post(t, h, host, "secret-token", "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","clientInfo":{"name":"claude-code"}}}`)
	session := res.Header.Get("Mcp-Session-Id")
	result, _ := out["result"].(map[string]any)
	if session == "" || result["protocolVersion"] != "2025-06-18" || result["instructions"] == nil {
		t.Fatalf("initialize: session %q, %v", session, out)
	}
	if res, _ := post(t, h, host, "secret-token", session, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); res.StatusCode != http.StatusAccepted {
		t.Errorf("notification: %d", res.StatusCode)
	}
	_, out = post(t, h, host, "secret-token", session, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	tools, _ := out["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 17 {
		t.Fatalf("%d tools, want 17", len(tools))
	}
	for _, x := range tools {
		tool := x.(map[string]any)
		ann := tool["annotations"].(map[string]any)
		write := strings.Contains("apply_manifest delete_resource scale restart", tool["name"].(string))
		if ann["readOnlyHint"] == write {
			t.Errorf("%s: readOnlyHint %v", tool["name"], ann["readOnlyHint"])
		}
	}

	call := func(name, args string) (string, bool) {
		_, out := post(t, h, host, "secret-token", session, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"`+name+`","arguments":`+args+`}}`)
		r, _ := out["result"].(map[string]any)
		if r == nil {
			t.Fatalf("%s: %v", name, out)
		}
		content := r["content"].([]any)[0].(map[string]any)["text"].(string)
		isErr, _ := r["isError"].(bool)
		return content, isErr
	}

	// Nothing is shared until the user says so.
	if text, _ := call("list_clusters", `{}`); !strings.Contains(text, "No cluster is shared") {
		t.Errorf("list_clusters with nothing shared: %s", text)
	}
	if text, isErr := call("get_resource", `{"cluster":"`+id+`","kind":"pods","name":"x"}`); !isErr || !strings.Contains(text, "not shared") {
		t.Errorf("reading a cluster not shared: %v %s", isErr, text)
	}
	if err := a.SetAccess(id, AgentRead); err != nil {
		t.Fatal(err)
	}
	if text, _ := call("list_clusters", `{}`); !strings.Contains(text, `"access": "read"`) {
		t.Errorf("list_clusters: %s", text)
	}
	if text, isErr := call("delete_resource", `{"cluster":"`+id+`","kind":"pods","name":"x","reason":"test"}`); !isErr || !strings.Contains(text, "may only read") {
		t.Errorf("a change on a read cluster: %v %s", isErr, text)
	}
	// Read-only clusters refuse agents' changes too.
	if err := a.SetAccess(id, AgentWrite); err != nil {
		t.Fatal(err)
	}
	if err := a.clusters.Update(id, ClusterPrefs{ReadOnly: true, Agent: AgentWrite}); err != nil {
		t.Fatal(err)
	}
	if text, isErr := call("scale", `{"cluster":"`+id+`","kind":"deployments","namespace":"a","name":"x","replicas":1,"reason":"test"}`); !isErr || !strings.Contains(text, "read-only") {
		t.Errorf("a change on a read-only cluster: %v %s", isErr, text)
	}

	_, out = post(t, h, host, "secret-token", session, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"rm_rf","arguments":{}}}`)
	if out["error"] == nil {
		t.Errorf("unknown tool: %v", out)
	}
	acts := a.Activity()
	if len(acts) < 4 || acts[0].Client != "claude-code" {
		t.Errorf("activity: %+v", acts)
	}
}

func TestSearchQuery(t *testing.T) {
	q := parseQuery("api image:nginx app=web ns:shop is:failing kind:deploy")
	if len(q.words) != 1 || q.words[0] != "api" || q.images[0] != "nginx" || q.labels["app"] != "web" || q.namespace != "shop" || !q.failing || q.kinds[0] != "deploy" {
		t.Fatalf("parsed %+v", q)
	}
	if !q.wantsKind("Deployment", []string{"deployment", "deploy"}) || q.wantsKind("Pod", []string{"pod"}) {
		t.Error("kind filter")
	}
	if p := parseQuery("status:crashloopbackoff"); !p.wantsKind("Pod", nil) || p.wantsKind("Service", nil) {
		t.Error("status filters only pods")
	}
}

func TestMaskSecret(t *testing.T) {
	o := map[string]any{"kind": "Secret", "data": map[string]any{"password": "aHVudGVyMg=="}, "stringData": map[string]any{"token": "abc123"}}
	y := comparableYAML(o)
	if strings.Contains(y, "aHVudGVyMg") || strings.Contains(y, "abc123") || !strings.Contains(y, "<secret") {
		t.Fatalf("secret leaked:\n%s", y)
	}
}
