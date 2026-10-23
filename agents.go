package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// AgentAccess is what AI agents may do with a cluster, through Kube Desk.
type AgentAccess string

const (
	// AgentOff hides the cluster from agents.
	AgentOff AgentAccess = ""
	// AgentRead lets agents read: resources, logs, events, changes.
	AgentRead AgentAccess = "read"
	// AgentWrite also lets agents propose changes, which the user approves
	// one by one in the app.
	AgentWrite AgentAccess = "write"
)

// AgentSettings is the MCP server of the app, kept in its store.
type AgentSettings struct {
	Enabled bool   `json:"enabled"`
	Port    int    `json:"port"`
	Token   string `json:"token,omitempty"`
}

// AgentInfo is what the page shows about the MCP server.
type AgentInfo struct {
	Enabled bool   `json:"enabled"`
	Running bool   `json:"running"`
	Error   string `json:"error,omitempty"`
	URL     string `json:"url"`
	Token   string `json:"token"`
	// Command and Args run the stdio bridge, for clients that start their
	// servers as processes.
	Command    string   `json:"command"`
	Args       []string `json:"args"`
	ConfigPath string   `json:"configPath"`
}

// ApprovalRequest is a change an agent proposes, waiting for the user.
type ApprovalRequest struct {
	ID          string `json:"id"`
	Client      string `json:"client"`
	Cluster     string `json:"cluster"`
	ClusterName string `json:"clusterName"`
	Production  bool   `json:"production"`
	// Action is apply, delete, scale or restart.
	Action    string `json:"action"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	// Summary says the change in a line; Reason is the agent's why.
	Summary string         `json:"summary"`
	Reason  string         `json:"reason,omitempty"`
	Diffs   []ObjectDiff   `json:"diffs,omitempty"`
	Impact  []ImpactItem   `json:"impact,omitempty"`
	Created time.Time      `json:"created"`
	Expires time.Time      `json:"expires"`
	done    chan bool
}

// AgentActivity is a call an agent made.
type AgentActivity struct {
	Time    time.Time `json:"time"`
	Client  string    `json:"client"`
	Tool    string    `json:"tool"`
	Cluster string    `json:"cluster,omitempty"`
	Detail  string    `json:"detail,omitempty"`
	// Outcome is ok, error, approved, rejected or expired.
	Outcome string `json:"outcome"`
}

// AgentApprovals is sent when the changes waiting for approval change.
var AgentApprovals = mygo.NewEvent[[]ApprovalRequest]("agents:approvals")

// AgentActivityChanged is sent when an agent made a call.
var AgentActivityChanged = mygo.NewEvent[[]AgentActivity]("agents:activity")

// approvalTimeout is how long a proposed change waits for the user.
const approvalTimeout = 5 * time.Minute

const defaultAgentPort = 47428

// Agents is the MCP server through which AI agents, such as Claude Code,
// read the clusters the user lets them and propose changes, which the user
// approves in the app.
type Agents struct {
	kube     *Kube
	clusters *Clusters
	show     func()
	// notify tells the user about a change waiting, and publish tells the
	// pages; tests replace them.
	notify  func(ApprovalRequest)
	publish func()

	mu       sync.Mutex
	server   *http.Server
	err      string
	pending  map[string]*ApprovalRequest
	activity []AgentActivity
	sessions map[string]string
}

func newAgents(k *Kube, c *Clusters, show func()) *Agents {
	a := &Agents{kube: k, clusters: c, show: show, pending: map[string]*ApprovalRequest{}, sessions: map[string]string{}}
	a.notify = func(r ApprovalRequest) {
		show()
		if r.Client == assistantClient {
			return // asked in the app, which shows the request
		}
		showNotification(mygo.NotificationOptions{
			ID:    "agent|" + r.ID,
			Title: fmt.Sprintf("%s asks to %s", r.Client, r.Action),
			Body:  r.Summary + " · " + r.ClusterName,
		})
	}
	a.publish = func() {
		AgentApprovals.Broadcast(a.Pending())
		AgentActivityChanged.Broadcast(a.Activity())
	}
	return a
}

// start runs the server when the user turned it on.
func (a *Agents) start() {
	if a.clusters.agentSettings().Enabled {
		a.serve()
	}
}

// Info returns the MCP server's state and how to connect to it.
func (a *Agents) Info() AgentInfo {
	s := a.clusters.agentSettings()
	exe, _ := os.Executable()
	cfg := a.configPath()
	a.mu.Lock()
	defer a.mu.Unlock()
	return AgentInfo{
		Enabled: s.Enabled, Running: a.server != nil, Error: a.err,
		URL: fmt.Sprintf("http://127.0.0.1:%d/mcp", s.Port), Token: s.Token,
		Command: exe, Args: []string{"mcp", "--config", cfg}, ConfigPath: cfg,
	}
}

// SetEnabled starts or stops the MCP server.
func (a *Agents) SetEnabled(on bool) (AgentInfo, error) {
	s := a.clusters.agentSettings()
	s.Enabled = on
	if s.Token == "" {
		s.Token = randomHex(24)
	}
	if s.Port == 0 {
		s.Port = defaultAgentPort
	}
	if err := a.clusters.setAgentSettings(s); err != nil {
		return AgentInfo{}, err
	}
	if on {
		a.serve()
	} else {
		a.stop()
		_ = os.Remove(a.configPath())
	}
	return a.Info(), nil
}

// RegenerateToken replaces the token agents connect with; connected agents
// must be set up again.
func (a *Agents) RegenerateToken() (AgentInfo, error) {
	s := a.clusters.agentSettings()
	s.Token = randomHex(24)
	if err := a.clusters.setAgentSettings(s); err != nil {
		return AgentInfo{}, err
	}
	if s.Enabled {
		a.stop()
		a.serve()
	}
	return a.Info(), nil
}

// SetAccess sets what agents may do with a cluster.
func (a *Agents) SetAccess(cluster string, access AgentAccess) error {
	return a.clusters.setAgentAccess(cluster, access)
}

// Pending returns the changes waiting for approval, oldest first.
func (a *Agents) Pending() []ApprovalRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := []ApprovalRequest{}
	for _, r := range a.pending {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}

// Decide approves or rejects a change an agent proposed.
func (a *Agents) Decide(id string, approve bool) error {
	a.mu.Lock()
	r, ok := a.pending[id]
	if ok {
		delete(a.pending, id)
	}
	a.mu.Unlock()
	if !ok {
		return errors.New("this request is no longer waiting: it was answered or expired")
	}
	r.done <- approve
	a.publish()
	return nil
}

// Activity returns the latest calls of agents, newest first.
func (a *Agents) Activity() []AgentActivity {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]AgentActivity, len(a.activity))
	for i := range a.activity {
		out[i] = a.activity[len(a.activity)-1-i]
	}
	return out
}

func (a *Agents) record(act AgentActivity) {
	act.Time = time.Now()
	a.mu.Lock()
	a.activity = append(a.activity, act)
	if len(a.activity) > 300 {
		a.activity = a.activity[len(a.activity)-300:]
	}
	a.mu.Unlock()
	a.publish()
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ---- The HTTP server ----

func (a *Agents) configPath() string {
	return filepath.Join(a.clusters.dir, "mcp.json")
}

// bridgeConfig is what the stdio bridge reads to reach the app.
type bridgeConfig struct {
	URL   string `json:"url"`
	Token string `json:"token"`
	App   string `json:"app,omitempty"`
}

func (a *Agents) serve() {
	a.mu.Lock()
	if a.server != nil {
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()
	s := a.clusters.agentSettings()
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", s.Port))
	if err != nil {
		// Another app has the port: take a free one, and remember it.
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			a.mu.Lock()
			a.err = err.Error()
			a.mu.Unlock()
			return
		}
		s.Port = ln.Addr().(*net.TCPAddr).Port
		_ = a.clusters.setAgentSettings(s)
	}
	srv := &http.Server{Handler: a.handler(s.Port, s.Token), ReadHeaderTimeout: 10 * time.Second}
	a.mu.Lock()
	a.server, a.err = srv, ""
	a.mu.Unlock()
	exe, _ := os.Executable()
	app := ""
	if i := strings.Index(exe, ".app/"); i > 0 {
		app = exe[:i+4]
	}
	b, _ := json.MarshalIndent(bridgeConfig{URL: fmt.Sprintf("http://127.0.0.1:%d/mcp", s.Port), Token: s.Token, App: app}, "", "  ")
	_ = os.WriteFile(a.configPath(), b, 0o600)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("mcp: %v", err)
			a.mu.Lock()
			a.err = err.Error()
			a.server = nil
			a.mu.Unlock()
		}
	}()
}

func (a *Agents) stop() {
	a.mu.Lock()
	srv := a.server
	a.server = nil
	a.mu.Unlock()
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
}

func (a *Agents) handler(port int, token string) http.Handler {
	hosts := map[string]bool{fmt.Sprintf("127.0.0.1:%d", port): true, fmt.Sprintf("localhost:%d", port): true}
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only local clients with the token; a web page cannot set the
		// header, and a rebound DNS name fails the host check.
		if !hosts[r.Host] {
			http.Error(w, "unknown host", http.StatusForbidden)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			http.Error(w, "missing or wrong token", http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/mcp" {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodPost:
		case http.MethodDelete:
			a.mu.Lock()
			delete(a.sessions, r.Header.Get("Mcp-Session-Id"))
			a.mu.Unlock()
			w.WriteHeader(http.StatusOK)
			return
		default:
			w.Header().Set("Allow", "POST, DELETE")
			http.Error(w, "this server sends no event stream", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		session := r.Header.Get("Mcp-Session-Id")
		var replies []json.RawMessage
		var batch []json.RawMessage
		trimmed := strings.TrimSpace(string(body))
		if strings.HasPrefix(trimmed, "[") {
			if err := json.Unmarshal(body, &batch); err != nil {
				writeJSON(w, rpcError(nil, -32700, "parse error"))
				return
			}
		} else {
			batch = []json.RawMessage{body}
		}
		for _, msg := range batch {
			reply, newSession := a.handleMessage(r.Context(), session, msg)
			if newSession != "" {
				w.Header().Set("Mcp-Session-Id", newSession)
			}
			if reply != nil {
				replies = append(replies, reply)
			}
		}
		switch {
		case len(replies) == 0:
			w.WriteHeader(http.StatusAccepted)
		case len(batch) == 1 && !strings.HasPrefix(trimmed, "["):
			w.Header().Set("Content-Type", "application/json")
			w.Write(replies[0])
		default:
			b, _ := json.Marshal(replies)
			w.Header().Set("Content-Type", "application/json")
			w.Write(b)
		}
	})
}

func writeJSON(w http.ResponseWriter, v json.RawMessage) {
	w.Header().Set("Content-Type", "application/json")
	w.Write(v)
}

// ---- JSON-RPC and MCP ----

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func rpcResult(id json.RawMessage, result any) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	return b
}

func rpcError(id json.RawMessage, code int, msg string) json.RawMessage {
	if id == nil {
		id = json.RawMessage("null")
	}
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}})
	return b
}

var mcpVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

const mcpInstructions = `Kube Desk gives access to the user's Kubernetes clusters that they chose to share with agents.
Start with list_clusters. Read before you change: get_resource, get_logs, get_events, get_changes (what changed recently, and who changed it) and incident_report are cheap.
Changes (apply_manifest, delete_resource, scale, restart) wait for the user to approve them in Kube Desk, with a diff and what the change affects: say why in "reason", and expect a rejection. preview_impact shows what a change would affect without asking anything.
Secret values are always masked, and so are tokens and passwords in logs.`

// handleMessage answers one JSON-RPC message; nil for notifications.
func (a *Agents) handleMessage(ctx context.Context, session string, raw json.RawMessage) (json.RawMessage, string) {
	var req rpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return rpcError(nil, -32700, "parse error"), ""
	}
	if req.ID == nil {
		return nil, "" // notifications: initialized, cancelled
	}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
			ClientInfo      struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"clientInfo"`
		}
		_ = json.Unmarshal(req.Params, &p)
		version := mcpVersions[0]
		for _, v := range mcpVersions {
			if v == p.ProtocolVersion {
				version = v
			}
		}
		id := randomHex(16)
		a.mu.Lock()
		a.sessions[id] = orDefault(p.ClientInfo.Name, "agent")
		a.mu.Unlock()
		return rpcResult(req.ID, map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "kube-desk", "title": "Kube Desk", "version": mygo.App.Version()},
			"instructions":    mcpInstructions,
		}), id
	case "ping":
		return rpcResult(req.ID, map[string]any{}), ""
	case "tools/list":
		return rpcResult(req.ID, map[string]any{"tools": toolDefs}), ""
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return rpcError(req.ID, -32602, "invalid params"), ""
		}
		a.mu.Lock()
		client := orDefault(a.sessions[session], "agent")
		a.mu.Unlock()
		text, err := a.callTool(ctx, client, p.Name, p.Arguments)
		if errors.Is(err, errUnknownTool) {
			return rpcError(req.ID, -32602, err.Error()), ""
		}
		if err != nil {
			return rpcResult(req.ID, map[string]any{"content": []map[string]any{{"type": "text", "text": err.Error()}}, "isError": true}), ""
		}
		return rpcResult(req.ID, map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}), ""
	}
	return rpcError(req.ID, -32601, "method not found: "+req.Method), ""
}

// ---- Tools ----

type toolDef struct {
	Name        string         `json:"name"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations"`
}

func toolSchema(required []string, props map[string]any) map[string]any {
	return map[string]any{"type": "object", "properties": props, "required": required}
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func num(desc string) map[string]any { return map[string]any{"type": "integer", "description": desc} }

var (
	pCluster   = str("Cluster ID, from list_clusters")
	pKind      = str("Kind, plural or short name: Pod, deployments, svc, a custom resource…")
	pNamespace = str("Namespace; empty for cluster-scoped objects, or all namespaces when listing")
	pName      = str("Object name")
	pReason    = str("Why you make this change, shown to the user who approves it")
	readOnly   = map[string]any{"readOnlyHint": true, "openWorldHint": false}
	changes    = map[string]any{"readOnlyHint": false, "destructiveHint": true, "openWorldHint": false}
)

var toolDefs = []toolDef{
	{"list_clusters", "List clusters", "The clusters the user shares with agents, with what each allows: read, or read and propose changes.", toolSchema(nil, map[string]any{}), readOnly},
	{"cluster_overview", "Cluster overview", "The whole cluster at a glance: nodes and their state, every namespace with its pods by status, workloads that are not fully ready, pods restarting, and warning events of the last hour. Start here.", toolSchema([]string{"cluster"}, map[string]any{
		"cluster": pCluster,
	}), readOnly},
	{"list_resources", "List resources", "Lists objects of a kind with their state: pods with status, readiness, restarts and node; workloads with ready replicas and images.", toolSchema([]string{"cluster", "kind"}, map[string]any{
		"cluster": pCluster, "kind": pKind, "namespace": pNamespace, "label_selector": str("Label selector, such as app=web"), "limit": num("At most this many, 200 by default"),
	}), readOnly},
	{"get_resource", "Get a resource", "The YAML of an object, status included, managed fields left out. Secret values are masked.", toolSchema([]string{"cluster", "kind", "name"}, map[string]any{
		"cluster": pCluster, "kind": pKind, "namespace": pNamespace, "name": pName,
	}), readOnly},
	{"get_logs", "Get logs", "The last lines of a container's logs, or of its previous run. Tokens and passwords are masked.", toolSchema([]string{"cluster", "namespace", "pod"}, map[string]any{
		"cluster": pCluster, "namespace": pNamespace, "pod": str("Pod name"), "container": str("Container; the first one by default"),
		"tail_lines": num("Lines from the end, 200 by default, at most 2000"), "previous": map[string]any{"type": "boolean", "description": "The logs of the run before the last restart"},
	}), readOnly},
	{"get_events", "Get events", "Recent events of a namespace, or of one object, newest first.", toolSchema([]string{"cluster"}, map[string]any{
		"cluster": pCluster, "namespace": pNamespace, "name": str("Only events of objects with this name, or whose names start with it"),
	}), readOnly},
	{"get_changes", "Get recent changes", "What changed in watched namespaces, recorded by Kube Desk: spec changes with who made them, restarts, readiness, warnings. Empty when the namespace is not watched.", toolSchema([]string{"cluster"}, map[string]any{
		"cluster": pCluster, "namespace": pNamespace, "kind": str("Only this kind, such as Deployment"), "name": pName, "since_minutes": num("How far back, 360 by default"),
	}), readOnly},
	{"incident_report", "Incident report", "A Markdown report of a pod or workload: state, events, logs of each container and of their previous runs, node, recent changes and what depends on it.", toolSchema([]string{"cluster", "kind", "namespace", "name"}, map[string]any{
		"cluster": pCluster, "kind": str("Pod, Deployment, StatefulSet, DaemonSet, ReplicaSet or Job"), "namespace": pNamespace, "name": pName,
	}), readOnly},
	{"helm_releases", "Helm releases", "Helm releases, read from the cluster: without a name, every release with its chart, revision and status; with a name and namespace, its history, the values the user gave (secrets masked) and the objects it deployed.", toolSchema([]string{"cluster"}, map[string]any{
		"cluster": pCluster, "namespace": pNamespace, "name": str("Release name, for one release"),
	}), readOnly},
	{"search", "Search clusters", "Finds objects in every shared cluster at once. Words match names; filters: image:nginx, app=web, kind:pod, ns:shop, node:n1, is:failing, status:CrashLoopBackOff.", toolSchema([]string{"query"}, map[string]any{
		"query": str("Search words and filters"),
	}), readOnly},
	{"compare", "Compare across clusters", "Lines up an object's fields (images, replicas, resources, environment, keys) across clusters, and marks what differs.", toolSchema([]string{"kind", "name"}, map[string]any{
		"kind": pKind, "namespace": pNamespace, "name": pName, "clusters": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Cluster IDs; all shared clusters by default"},
	}), readOnly},
	{"preview_impact", "Preview the impact of a change", "What a delete, scale, restart or drain would affect, without changing anything: services left without endpoints, ingresses, disruption budgets, data, capacity.", toolSchema([]string{"cluster", "action", "kind", "name"}, map[string]any{
		"cluster": pCluster, "action": map[string]any{"type": "string", "enum": []string{"delete", "scale", "restart", "drain"}}, "kind": pKind, "namespace": pNamespace, "name": pName, "replicas": num("For scale"),
	}), readOnly},
	{"apply_manifest", "Apply a manifest", "Creates or replaces objects from YAML, after the user approves it in Kube Desk with a diff of each object.", toolSchema([]string{"cluster", "manifest", "reason"}, map[string]any{
		"cluster": pCluster, "manifest": str("YAML of one or more objects, separated by ---"), "namespace": str("For objects that name none"), "reason": pReason,
	}), changes},
	{"delete_resource", "Delete a resource", "Deletes an object, after the user approves it in Kube Desk with what the deletion affects.", toolSchema([]string{"cluster", "kind", "name", "reason"}, map[string]any{
		"cluster": pCluster, "kind": pKind, "namespace": pNamespace, "name": pName, "reason": pReason,
	}), changes},
	{"scale", "Scale a workload", "Sets the replicas of a deployment, stateful set or replica set, after the user approves it.", toolSchema([]string{"cluster", "kind", "namespace", "name", "replicas", "reason"}, map[string]any{
		"cluster": pCluster, "kind": str("Deployment, StatefulSet or ReplicaSet"), "namespace": pNamespace, "name": pName, "replicas": num("Replicas wanted"), "reason": pReason,
	}), changes},
	{"restart", "Restart a workload", "Restarts the pods of a deployment, stateful set or daemon set, like kubectl rollout restart, after the user approves it.", toolSchema([]string{"cluster", "kind", "namespace", "name", "reason"}, map[string]any{
		"cluster": pCluster, "kind": str("Deployment, StatefulSet or DaemonSet"), "namespace": pNamespace, "name": pName, "reason": pReason,
	}), changes},
}

var errUnknownTool = errors.New("unknown tool")

// accessPolicy says which clusters a caller of the tools may read, and on
// which it may propose changes. Changes still wait for the user's approval.
type accessPolicy struct {
	name     string
	readable func(cluster string) bool
	writable func(cluster string) bool
	// notShared and readOnlyAccess explain the refusals, in the caller's terms.
	notShared, readOnlyAccess string
}

// mcpPolicy follows what the user allowed agents, cluster by cluster.
func (a *Agents) mcpPolicy() accessPolicy {
	return accessPolicy{
		name:           "mcp",
		readable:       func(id string) bool { return a.clusters.agentAccess(id) != AgentOff },
		writable:       func(id string) bool { return a.clusters.agentAccess(id) == AgentWrite },
		notShared:      "this cluster is not shared with agents: the user can share it in Kube Desk, under AI agents",
		readOnlyAccess: "agents may only read this cluster: the user can allow proposing changes in Kube Desk, under AI agents",
	}
}

// assistantPolicy lets the app's own assistant read every cluster shown,
// and propose changes to any; the user approves each.
func (a *Agents) assistantPolicy() accessPolicy {
	shown := func(id string) bool {
		for _, c := range a.clusters.List().Clusters {
			if c.ID == id {
				return !c.Hidden
			}
		}
		return false
	}
	return accessPolicy{
		name:      "assistant",
		readable:  shown,
		writable:  shown,
		notShared: "there is no such cluster in Kube Desk: call list_clusters for their IDs",
	}
}

// sharedClusters lists the clusters a policy may read.
func (a *Agents) sharedClusters(pol accessPolicy) []Cluster {
	var out []Cluster
	for _, c := range a.clusters.List().Clusters {
		if !c.Hidden && pol.readable(c.ID) {
			out = append(out, c)
		}
	}
	return out
}

type toolArgs struct {
	Cluster       string   `json:"cluster"`
	Kind          string   `json:"kind"`
	Namespace     string   `json:"namespace"`
	Name          string   `json:"name"`
	Pod           string   `json:"pod"`
	Container     string   `json:"container"`
	LabelSelector string   `json:"label_selector"`
	Limit         int      `json:"limit"`
	TailLines     int      `json:"tail_lines"`
	Previous      bool     `json:"previous"`
	SinceMinutes  int      `json:"since_minutes"`
	Query         string   `json:"query"`
	Clusters      []string `json:"clusters"`
	Action        string   `json:"action"`
	Replicas      *int     `json:"replicas"`
	Manifest      string   `json:"manifest"`
	Reason        string   `json:"reason"`
}

func (a *Agents) callTool(ctx context.Context, client, name string, raw json.RawMessage) (string, error) {
	var args toolArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
	}
	act := AgentActivity{Client: client, Tool: name, Cluster: a.clusters.name(args.Cluster)}
	if args.Cluster == "" {
		act.Cluster = ""
	}
	act.Detail = strings.Trim(strings.Join([]string{args.Kind, args.Namespace, orDefault(args.Name, args.Pod), args.Query}, " "), " ")
	text, outcome, err := a.dispatch(ctx, client, name, args, a.mcpPolicy())
	act.Outcome = outcome
	if err != nil && outcome == "" {
		act.Outcome = "error"
		act.Detail = strings.TrimSpace(act.Detail + " · " + truncate(err.Error(), 160))
	} else if outcome == "" {
		act.Outcome = "ok"
	}
	if !errors.Is(err, errUnknownTool) {
		a.record(act)
	}
	return text, err
}

// access checks what a caller may do with a cluster.
func (a *Agents) access(cluster string, write bool, pol accessPolicy) error {
	switch {
	case cluster == "":
		return errors.New("cluster is required: call list_clusters first")
	case !pol.readable(cluster):
		return errors.New(pol.notShared)
	case write && !pol.writable(cluster):
		return errors.New(pol.readOnlyAccess)
	case write && a.clusters.writable(cluster) != nil:
		return errReadOnly
	}
	return nil
}

func (a *Agents) dispatch(ctx context.Context, client, name string, args toolArgs, pol accessPolicy) (string, string, error) {
	k := a.kube
	switch name {
	case "list_clusters":
		type row struct {
			ID         string      `json:"id"`
			Name       string      `json:"name"`
			Server     string      `json:"server"`
			Access     AgentAccess `json:"access"`
			Production bool        `json:"production"`
			ReadOnly   bool        `json:"readOnly"`
		}
		var rows []row
		for _, c := range a.sharedClusters(pol) {
			acc := AgentWrite
			if pol.name == "mcp" {
				acc = a.clusters.agentAccess(c.ID)
			}
			if c.ReadOnly {
				acc = AgentRead
			}
			rows = append(rows, row{c.ID, c.Name, c.Server, acc, c.Production, c.ReadOnly})
		}
		if len(rows) == 0 {
			return "No cluster is shared with agents. The user can share clusters in Kube Desk, under AI agents.", "", nil
		}
		return jsonText(rows), "", nil
	case "search":
		var ids []string
		for _, c := range a.sharedClusters(pol) {
			ids = append(ids, c.ID)
		}
		if len(ids) == 0 {
			return "", "", errors.New("no cluster is shared with agents")
		}
		var hits []SearchHit
		err := k.search(ctx, SearchQuery{Text: args.Query, Clusters: ids}, func(b []SearchHit) error {
			hits = append(hits, b...)
			return nil
		})
		if len(hits) > 300 {
			hits = hits[:300]
		}
		return jsonText(hits), "", err
	case "compare":
		ids := args.Clusters
		if len(ids) == 0 {
			for _, c := range a.sharedClusters(pol) {
				ids = append(ids, c.ID)
			}
		}
		for _, id := range ids {
			if err := a.access(id, false, pol); err != nil {
				return "", "", fmt.Errorf("%s: %w", id, err)
			}
		}
		if len(ids) == 0 {
			return "", "", errors.New("no cluster is shared with agents")
		}
		gvr, _, err := a.resolve(ids[0], args.Kind)
		if err != nil {
			return "", "", err
		}
		res, err := k.Compare(ctx, CompareRequest{GVR: gvr, Kind: args.Kind, Namespace: args.Namespace, Name: args.Name, Clusters: ids})
		if err != nil {
			return "", "", err
		}
		for i := range res.Columns {
			res.Columns[i].YAML = ""
		}
		return jsonText(res), "", nil
	}

	write := name == "apply_manifest" || name == "delete_resource" || name == "scale" || name == "restart"
	if !write {
		if _, ok := map[string]bool{"cluster_overview": true, "list_resources": true, "get_resource": true, "get_logs": true, "get_events": true, "get_changes": true, "incident_report": true, "preview_impact": true, "helm_releases": true}[name]; !ok {
			return "", "", fmt.Errorf("%w: %s", errUnknownTool, name)
		}
	}
	if err := a.access(args.Cluster, write, pol); err != nil {
		return "", "", err
	}
	switch name {
	case "cluster_overview":
		cn, err := a.clusters.conn(args.Cluster)
		if err != nil {
			return "", "", err
		}
		ov, err := clusterOverview(ctx, cn.cs)
		if err != nil {
			return "", "", errors.New(friendlyError(err))
		}
		return jsonText(ov), "", nil
	case "list_resources":
		return a.listResources(ctx, args)
	case "helm_releases":
		cn, err := a.clusters.conn(args.Cluster)
		if err != nil {
			return "", "", err
		}
		text, err := helmSummary(ctx, cn.cs, args.Namespace, args.Name)
		return text, "", err
	case "get_resource":
		gvr, namespaced, err := a.resolve(args.Cluster, args.Kind)
		if err != nil {
			return "", "", err
		}
		ns := args.Namespace
		if !namespaced {
			ns = ""
		}
		r, err := k.resource(args.Cluster, gvr, ns)
		if err != nil {
			return "", "", err
		}
		o, err := r.Get(ctx, args.Name, metav1.GetOptions{})
		if err != nil {
			return "", "", err
		}
		c := o.DeepCopy().Object
		unstructured.RemoveNestedField(c, "metadata", "managedFields")
		maskSecret(c)
		b, _ := yaml.Marshal(c)
		return string(b), "", nil
	case "get_logs":
		tail := args.TailLines
		if tail <= 0 {
			tail = 200
		}
		tail = min(tail, 2000)
		cn, err := a.clusters.conn(args.Cluster)
		if err != nil {
			return "", "", err
		}
		t := int64(tail)
		stream, err := cn.cs.CoreV1().Pods(args.Namespace).GetLogs(args.Pod, &corev1.PodLogOptions{Container: args.Container, TailLines: &t, Previous: args.Previous, Timestamps: true}).Stream(ctx)
		if err != nil {
			return "", "", errors.New(friendlyError(err))
		}
		defer stream.Close()
		b, _ := io.ReadAll(io.LimitReader(stream, 1<<20))
		out := newRedactor().apply(string(b))
		if strings.TrimSpace(out) == "" {
			out = "(no lines)"
		}
		return out, "", nil
	case "get_events":
		cn, err := a.clusters.conn(args.Cluster)
		if err != nil {
			return "", "", err
		}
		r := &reporter{ctx: ctx, cs: cn.cs, redact: newRedactor()}
		r.events(args.Namespace, "", args.Name)
		if r.b.Len() == 0 {
			return "No events.", "", nil
		}
		return r.b.String(), "", nil
	case "get_changes":
		since := args.SinceMinutes
		if since <= 0 {
			since = 360
		}
		list, err := k.timeline.Query(TimelineQuery{Cluster: args.Cluster, Namespace: args.Namespace, Kind: args.Kind, Name: args.Name, Since: time.Now().Add(-time.Duration(since) * time.Minute).Format(time.RFC3339), Limit: 200})
		if err != nil {
			return "", "", err
		}
		if len(list) == 0 {
			return "No changes recorded. Kube Desk records changes in the namespaces the user watches, while it runs.", "", nil
		}
		var b strings.Builder
		for _, c := range list {
			who := ""
			if c.Actor != "" {
				who = " by " + c.Actor
			}
			fmt.Fprintf(&b, "%s %s %s/%s %s%s: %s\n", c.Time.Format(time.RFC3339), c.Kind, c.Namespace, c.Name, c.Type, who, strings.Join(c.Summary, "; "))
		}
		return b.String(), "", nil
	case "incident_report":
		rep, err := k.Report(ctx, args.Cluster, canonicalKind(args.Kind), args.Namespace, args.Name)
		if err != nil {
			return "", "", err
		}
		return rep.Markdown, "", nil
	case "preview_impact":
		req := ImpactRequest{Action: ImpactAction(args.Action), Kind: canonicalKind(args.Kind), Namespace: args.Namespace, Name: args.Name}
		if args.Replicas != nil {
			req.Replicas = *args.Replicas
		}
		items, err := k.Impact(ctx, args.Cluster, req)
		if err != nil {
			return "", "", err
		}
		if len(items) == 0 {
			return "Nothing else depends on it.", "", nil
		}
		return jsonText(items), "", nil
	}
	return a.propose(ctx, client, name, args)
}

func jsonText(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

// canonicalKind turns plural and short names of the built-in workloads into
// their kinds.
func canonicalKind(k string) string {
	for _, sk := range searchKinds {
		if strings.EqualFold(k, sk.kind) {
			return sk.kind
		}
		for _, al := range sk.aliases {
			if strings.EqualFold(k, al) {
				return sk.kind
			}
		}
	}
	switch strings.ToLower(k) {
	case "replicaset", "replicasets", "rs":
		return "ReplicaSet"
	case "job", "jobs":
		return "Job"
	case "secret", "secrets":
		return "Secret"
	case "persistentvolumeclaim", "persistentvolumeclaims", "pvc":
		return "PersistentVolumeClaim"
	}
	return k
}

// resolve finds the resource of a kind, by kind, plural or short name.
func (a *Agents) resolve(cluster, kind string) (GVR, bool, error) {
	k := strings.ToLower(kind)
	for _, fresh := range []bool{false, true} {
		list, err := a.kube.resources(cluster, fresh)
		if err != nil {
			return GVR{}, false, err
		}
		for _, r := range list {
			if strings.ToLower(r.Kind) == k || r.Resource == k || strings.TrimSuffix(r.Resource, "s") == k || containsFold(r.ShortNames, k) ||
				r.Resource+"."+r.Group == k || strings.ToLower(r.Kind)+"."+r.Group == k {
				return GVR{Group: r.Group, Version: r.Version, Resource: r.Resource}, r.Namespaced, nil
			}
		}
	}
	return GVR{}, false, fmt.Errorf("the cluster serves no kind %q", kind)
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

func (a *Agents) listResources(ctx context.Context, args toolArgs) (string, string, error) {
	gvr, namespaced, err := a.resolve(args.Cluster, args.Kind)
	if err != nil {
		return "", "", err
	}
	ns := args.Namespace
	if !namespaced {
		ns = ""
	}
	r, err := a.kube.resource(args.Cluster, gvr, ns)
	if err != nil {
		return "", "", err
	}
	limit := args.Limit
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	l, err := r.List(ctx, metav1.ListOptions{LabelSelector: args.LabelSelector})
	if err != nil {
		return "", "", errors.New(friendlyError(err))
	}
	type row struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace,omitempty"`
		State     string `json:"state,omitempty"`
		Age       string `json:"age"`
	}
	rows := []row{}
	for i := range l.Items {
		if len(rows) == limit {
			break
		}
		o := &l.Items[i]
		state := ""
		kind := o.GetKind()
		if kind == "" {
			kind = canonicalKind(args.Kind)
		}
		switch kind {
		case "Pod":
			var p corev1.Pod
			if fromUnstructured(o.Object, &p) == nil {
				reason, _ := podReason(&p)
				restarts := int32(0)
				readyN := 0
				for _, cs := range p.Status.ContainerStatuses {
					restarts += cs.RestartCount
					if cs.Ready {
						readyN++
					}
				}
				state = fmt.Sprintf("%s, %d/%d ready, %d restarts, node %s", reason, readyN, len(p.Spec.Containers), restarts, orDefault(p.Spec.NodeName, "none"))
			}
		case "Deployment", "StatefulSet", "ReplicaSet":
			want, _, _ := unstructured.NestedInt64(o.Object, "spec", "replicas")
			got, _, _ := unstructured.NestedInt64(o.Object, "status", "readyReplicas")
			state = fmt.Sprintf("%d/%d ready, %s", got, want, strings.Join(imagesOf(kind, o.Object), ", "))
		case "DaemonSet":
			want, _, _ := unstructured.NestedInt64(o.Object, "status", "desiredNumberScheduled")
			got, _, _ := unstructured.NestedInt64(o.Object, "status", "numberReady")
			state = fmt.Sprintf("%d/%d ready", got, want)
		case "Node":
			var n corev1.Node
			if fromUnstructured(o.Object, &n) == nil {
				state = map[bool]string{true: "Ready", false: "NotReady"}[nodeIsReady(&n)]
				if n.Spec.Unschedulable {
					state += ", cordoned"
				}
			}
		}
		rows = append(rows, row{Name: o.GetName(), Namespace: o.GetNamespace(), State: state, Age: time.Since(o.GetCreationTimestamp().Time).Round(time.Minute).String()})
	}
	out := jsonText(rows)
	if len(l.Items) > limit {
		out += fmt.Sprintf("\n(%d of %d shown)", limit, len(l.Items))
	}
	return out, "", nil
}

// propose asks the user to approve a change, and makes it when they do.
func (a *Agents) propose(ctx context.Context, client, tool string, args toolArgs) (string, string, error) {
	k := a.kube
	cl := a.clusterOf(args.Cluster)
	req := &ApprovalRequest{
		ID: randomHex(8), Client: client, Cluster: args.Cluster, ClusterName: cl.Name, Production: cl.Production,
		Kind: canonicalKind(args.Kind), Namespace: args.Namespace, Name: args.Name, Reason: truncate(args.Reason, 600),
		Created: time.Now(), Expires: time.Now().Add(approvalTimeout), done: make(chan bool, 1),
	}
	var gvr GVR
	if tool != "apply_manifest" {
		var namespaced bool
		var err error
		gvr, namespaced, err = a.resolve(args.Cluster, args.Kind)
		if err != nil {
			return "", "", err
		}
		if !namespaced {
			req.Namespace = ""
		}
	}
	where := req.Name
	if req.Namespace != "" {
		where = req.Namespace + "/" + req.Name
	}
	switch tool {
	case "apply_manifest":
		req.Action = "apply"
		diffs, err := k.PlanApply(ctx, args.Cluster, args.Manifest, args.Namespace)
		if err != nil {
			return "", "", err
		}
		req.Diffs = diffs
		var parts []string
		for _, d := range diffs {
			parts = append(parts, fmt.Sprintf("%s %s %s", d.Action, strings.ToLower(d.Kind), d.Name))
		}
		req.Summary = "Apply: " + strings.Join(limitList(parts, 4), ", ")
		if len(diffs) == 1 {
			req.Kind, req.Namespace, req.Name = diffs[0].Kind, diffs[0].Namespace, diffs[0].Name
		}
		allSame := true
		for _, d := range diffs {
			allSame = allSame && d.Action == "unchanged"
		}
		if allSame {
			return "Nothing to apply: the objects already match the manifest.", "ok", nil
		}
	case "delete_resource":
		req.Action = "delete"
		req.Summary = fmt.Sprintf("Delete %s %s", req.Kind, where)
		req.Impact, _ = k.Impact(ctx, args.Cluster, ImpactRequest{Action: ImpactDelete, Kind: req.Kind, Namespace: req.Namespace, Name: req.Name})
	case "scale":
		if args.Replicas == nil || *args.Replicas < 0 {
			return "", "", errors.New("replicas must be 0 or more")
		}
		req.Action = "scale"
		req.Summary = fmt.Sprintf("Scale %s %s to %d replicas", req.Kind, where, *args.Replicas)
		req.Impact, _ = k.Impact(ctx, args.Cluster, ImpactRequest{Action: ImpactScale, Kind: req.Kind, Namespace: req.Namespace, Name: req.Name, Replicas: *args.Replicas})
	case "restart":
		req.Action = "restart"
		req.Summary = fmt.Sprintf("Restart %s %s", req.Kind, where)
		req.Impact, _ = k.Impact(ctx, args.Cluster, ImpactRequest{Action: ImpactRestart, Kind: req.Kind, Namespace: req.Namespace, Name: req.Name})
	}

	a.mu.Lock()
	a.pending[req.ID] = req
	a.mu.Unlock()
	a.publish()
	a.notify(*req)

	timer := time.NewTimer(approvalTimeout)
	defer timer.Stop()
	var approved bool
	select {
	case approved = <-req.done:
	case <-timer.C:
		a.drop(req.ID)
		return "", "expired", errors.New("the user did not answer within 5 minutes; ask them, or try again later")
	case <-ctx.Done():
		a.drop(req.ID)
		return "", "expired", errors.New("the request was cancelled")
	}
	if !approved {
		return "", "rejected", errors.New("the user rejected this change. Ask them what they want instead")
	}

	// Changes carry the agent's name, so that the timeline shows who made them.
	actx := withManager(ctx, "kubedesk-agent ("+truncate(client, 40)+")")
	switch tool {
	case "apply_manifest":
		res, err := k.Apply(actx, args.Cluster, args.Manifest, args.Namespace)
		if err != nil {
			return "", "approved", err
		}
		return "Approved and applied:\n" + strings.Join(res, "\n"), "approved", nil
	case "delete_resource":
		if err := k.Delete(actx, args.Cluster, gvr, req.Namespace, req.Name, false); err != nil {
			return "", "approved", err
		}
		return fmt.Sprintf("Approved: %s %s deleted.", req.Kind, where), "approved", nil
	case "scale":
		if err := k.Scale(actx, args.Cluster, gvr, req.Namespace, req.Name, *args.Replicas); err != nil {
			return "", "approved", err
		}
		return fmt.Sprintf("Approved: %s %s scaled to %d.", req.Kind, where, *args.Replicas), "approved", nil
	case "restart":
		if err := k.Restart(actx, args.Cluster, gvr, req.Namespace, req.Name); err != nil {
			return "", "approved", err
		}
		return fmt.Sprintf("Approved: %s %s is restarting.", req.Kind, where), "approved", nil
	}
	return "", "", errUnknownTool
}

func (a *Agents) drop(id string) {
	a.mu.Lock()
	delete(a.pending, id)
	a.mu.Unlock()
	a.publish()
}

func (a *Agents) clusterOf(id string) Cluster {
	for _, c := range a.clusters.List().Clusters {
		if c.ID == id {
			return c
		}
	}
	return Cluster{ID: id, Name: id}
}
