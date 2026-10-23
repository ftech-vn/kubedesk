package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// sse writes a Messages stream: a message whose blocks are given as
// (start block JSON, delta JSON) pairs, ending with stopReason.
func sse(w http.ResponseWriter, stopReason string, blocks ...[2]string) {
	w.Header().Set("Content-Type", "text/event-stream")
	write := func(event string, data any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
	}
	write("message_start", map[string]any{"type": "message_start", "message": map[string]any{
		"id": "msg_1", "type": "message", "role": "assistant", "model": "claude-opus-5-5", "content": []any{},
		"stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 100, "output_tokens": 1, "cache_read_input_tokens": 40},
	}})
	for i, b := range blocks {
		fmt.Fprintf(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":%d,\"content_block\":%s}\n\n", i, b[0])
		if b[1] != "" {
			fmt.Fprintf(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":%d,\"delta\":%s}\n\n", i, b[1])
		}
		fmt.Fprintf(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":%d}\n\n", i)
	}
	write("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stopReason, "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 20}})
	write("message_stop", map[string]any{"type": "message_stop"})
}

func newTestAssistant(t *testing.T, handler http.HandlerFunc) (*Assistant, string) {
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
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	ag := newAgents(&Kube{clusters: c, timeline: newTimeline()}, c, func() {})
	ag.publish = func() {}
	ag.notify = func(ApprovalRequest) {}
	as := newAssistant(ag, c)
	as.key = func() ([]byte, error) { return []byte("test-key"), nil }
	if err := c.setAssistantSettings(AssistantSettings{Endpoint: srv.URL + "/anthropic/v1/messages", Deployment: "my-claude", Effort: "high"}); err != nil {
		t.Fatal(err)
	}
	return as, added[0].ID
}

func TestAssistantToolLoop(t *testing.T) {
	var mu sync.Mutex
	var bodies []map[string]any
	var paths []string
	as, cluster := newTestAssistant(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		mu.Lock()
		bodies = append(bodies, body)
		paths = append(paths, r.URL.Path+"?"+r.URL.RawQuery+" key="+r.Header.Get("x-api-key"))
		n := len(bodies)
		mu.Unlock()
		if n == 1 {
			sse(w, "tool_use",
				[2]string{`{"type":"thinking","thinking":"","signature":""}`, `{"type":"thinking_delta","thinking":"Look at the clusters first."}`},
				[2]string{`{"type":"tool_use","id":"toolu_1","name":"list_clusters","input":{}}`, `{"type":"input_json_delta","partial_json":"{}"}`},
			)
			return
		}
		sse(w, "end_turn", [2]string{`{"type":"text","text":""}`, `{"type":"text_delta","text":"You have **one** cluster."}`})
	})

	var events []ChatEvent
	err := as.send(context.Background(), "c1", "Bạn thấy những cluster nào?", ChatView{Cluster: cluster, Namespace: "shop"}, func(e ChatEvent) error {
		events = append(events, e)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 {
		t.Fatalf("%d requests, want 2", len(bodies))
	}
	// The Foundry endpoint, the key and the beta surface (for fallbacks).
	if !strings.HasPrefix(paths[0], "/anthropic/v1/messages?beta=true") || !strings.HasSuffix(paths[0], "key=test-key") {
		t.Errorf("request: %s", paths[0])
	}
	first := bodies[0]
	if first["model"] != "my-claude" || first["thinking"].(map[string]any)["type"] != "adaptive" || first["output_config"].(map[string]any)["effort"] != "high" {
		t.Errorf("request params: model %v thinking %v output %v", first["model"], first["thinking"], first["output_config"])
	}
	tools := first["tools"].([]any)
	if len(tools) != len(toolDefs) || tools[0].(map[string]any)["eager_input_streaming"] != true {
		t.Errorf("tools: %d, first %v", len(tools), tools[0])
	}
	msg0 := first["messages"].([]any)[0].(map[string]any)
	text0 := msg0["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.HasPrefix(text0, "<view>") || !strings.Contains(text0, "namespace shop") || !strings.HasSuffix(text0, "Bạn thấy những cluster nào?") {
		t.Errorf("first message: %q", text0)
	}

	// The second request carries the whole first answer and the tool result.
	msgs := bodies[1]["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("second request: %d messages, want 3", len(msgs))
	}
	result := msgs[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	if result["type"] != "tool_result" || result["tool_use_id"] != "toolu_1" || !strings.Contains(fmt.Sprint(result["content"]), cluster) {
		t.Errorf("tool result: %v", result)
	}
	assistantTurn := msgs[1].(map[string]any)["content"].([]any)
	if assistantTurn[0].(map[string]any)["type"] != "thinking" {
		t.Errorf("the thinking block was not sent back: %v", assistantTurn[0])
	}

	var kinds []string
	var text string
	for _, e := range events {
		kinds = append(kinds, string(e.Type))
		if e.Type == ChatText {
			text += e.Text
		}
	}
	got := strings.Join(kinds, ",")
	if got != "thinking,tool_start,tool_end,text,done" || text != "You have **one** cluster." {
		t.Errorf("events %s, text %q", got, text)
	}
	if done := events[len(events)-1]; done.InputTokens != 280 || done.CachedTokens != 80 || done.OutputTokens != 40 {
		t.Errorf("usage %+v", done)
	}

	// The conversation goes on from there.
	if err := as.send(context.Background(), "c1", "thanks", ChatView{}, func(ChatEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if n := len(bodies[2]["messages"].([]any)); n != 5 {
		t.Errorf("third request: %d messages, want 5", n)
	}
}

func TestAssistantChecksToolInputs(t *testing.T) {
	calls := 0
	var second map[string]any
	as, _ := newTestAssistant(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			sse(w, "tool_use", [2]string{`{"type":"tool_use","id":"toolu_1","name":"get_logs","input":{}}`, `{"type":"input_json_delta","partial_json":"{\"cluster\": \"abc"}`})
			return
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &second)
		sse(w, "end_turn", [2]string{`{"type":"text","text":""}`, `{"type":"text_delta","text":"ok"}`})
	})
	if err := as.send(context.Background(), "c", "logs?", ChatView{}, func(ChatEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	msgs := second["messages"].([]any)
	result := msgs[len(msgs)-1].(map[string]any)["content"].([]any)[0].(map[string]any)
	if result["is_error"] != true || !strings.Contains(fmt.Sprint(result["content"]), "INVALID_JSON") {
		t.Errorf("a cut-off input ran, or was not reported: %v", result)
	}
}

func TestAssistantErrors(t *testing.T) {
	as, _ := newTestAssistant(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"type":"error","error":{"type":"not_found_error","message":"Deployment not found"}}`)
	})
	err := as.send(context.Background(), "c", "hi", ChatView{}, func(ChatEvent) error { return nil })
	if err == nil || !strings.Contains(err.Error(), `no deployment named "my-claude"`) {
		t.Fatalf("404: %v", err)
	}
	as.key = func() ([]byte, error) { return nil, os.ErrNotExist }
	if err := as.send(context.Background(), "c", "hi", ChatView{}, func(ChatEvent) error { return nil }); err == nil || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("no key: %v", err)
	}
}

func TestAssistantStops(t *testing.T) {
	as, _ := newTestAssistant(t, func(w http.ResponseWriter, r *http.Request) {
		sse(w, "end_turn", [2]string{`{"type":"text","text":""}`, `{"type":"text_delta","text":"a long answer"}`})
	})
	// The page stops listening at the first event.
	err := as.send(context.Background(), "c", "hi", ChatView{}, func(ChatEvent) error { return fmt.Errorf("closed") })
	if err != nil {
		t.Fatal(err)
	}
	if c := as.chat("c"); c.busy {
		t.Error("the chat stays busy")
	}
}

func TestAssistantDropsARejectedFallback(t *testing.T) {
	var betas []string
	as, _ := newTestAssistant(t, func(w http.ResponseWriter, r *http.Request) {
		betas = append(betas, r.Header.Get("anthropic-beta"))
		if strings.Contains(r.Header.Get("anthropic-beta"), "fallback") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"type":"error","error":{"type":"invalid_request_error","message":"Unsupported beta header"}}`)
			return
		}
		sse(w, "end_turn", [2]string{`{"type":"text","text":""}`, `{"type":"text_delta","text":"hello"}`})
	})
	_ = as.clusters.setAssistantSettings(AssistantSettings{Endpoint: as.settings().Endpoint, Deployment: "my-claude", Fallback: "claude-opus-4-8"})
	var notes, text string
	err := as.send(context.Background(), "c", "hi", ChatView{}, func(e ChatEvent) error {
		if e.Type == ChatNote {
			notes += e.Text
		}
		if e.Type == ChatText {
			text += e.Text
		}
		return nil
	})
	if err != nil || text != "hello" || !strings.Contains(notes, "rejects the fallback") {
		t.Fatalf("err %v, text %q, notes %q, betas %q", err, text, notes, betas)
	}
	if len(betas) != 2 || !strings.Contains(betas[0], "fallback") || strings.Contains(betas[1], "fallback") {
		t.Errorf("beta headers: %q", betas)
	}
}
