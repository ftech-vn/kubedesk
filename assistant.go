package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/foundry"
	"github.com/anthropics/anthropic-sdk-go/v2/lib/betafallback"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
	"github.com/anthropics/anthropic-sdk-go/v2/vertex"
	"github.com/egoist/mygo"
	"golang.org/x/oauth2/google"
)

// AssistantProvider is the cloud the assistant reaches Claude through.
type AssistantProvider string

const (
	// ProviderFoundry is Microsoft Foundry, with an API key.
	ProviderFoundry AssistantProvider = "foundry"
	// ProviderVertex is Google Cloud Vertex AI, with the Application Default
	// Credentials of gcloud.
	ProviderVertex AssistantProvider = "vertex"
)

// AssistantSettings is how the assistant reaches Claude. The Foundry API
// key is kept in the system's credential store, apart.
type AssistantSettings struct {
	// Provider is foundry (the default) or vertex.
	Provider AssistantProvider `json:"provider"`
	// Endpoint is, for Foundry, the resource name or its full base URL
	// (https://<resource>.services.ai.azure.com/anthropic/); for Vertex, an
	// optional base URL, as of a Private Service Connect endpoint.
	Endpoint string `json:"endpoint"`
	// Deployment is the model: the deployment name in Foundry, which Foundry
	// takes as the model, or the model ID in Vertex.
	Deployment string `json:"deployment"`
	// Fallback is a deployment or model that answers when the main one
	// declines a request; "" for none.
	Fallback string `json:"fallback"`
	// Effort is low, medium, high, xhigh or max.
	Effort string `json:"effort"`
	// Project and Region are where Vertex serves Claude; the project of the
	// credentials, and the global region, by default.
	Project string `json:"project,omitempty"`
	Region  string `json:"region,omitempty"`
}

// AssistantInfo is what the page shows about the assistant's setup.
type AssistantInfo struct {
	Settings   AssistantSettings `json:"settings"`
	HasKey     bool              `json:"hasKey"`
	Configured bool              `json:"configured"`
	// Google says which Google credentials Vertex uses, or why there are
	// none, as GoogleError.
	Google      string `json:"google,omitempty"`
	GoogleError string `json:"googleError,omitempty"`
}

// ChatSummary is a saved conversation, for the list of past chats.
type ChatSummary struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Updated string `json:"updated"`
}

// savedChat is a conversation as kept on disk: the history the model sees,
// and the page's turns, which the page writes.
type savedChat struct {
	ID       string                       `json:"id"`
	Title    string                       `json:"title"`
	Created  string                       `json:"created"`
	Updated  string                       `json:"updated"`
	Model    string                       `json:"model"`
	Messages []anthropic.BetaMessageParam `json:"messages"`
	Turns    json.RawMessage              `json:"turns"`
}

// ChatView is what the user looks at in the app when they write.
type ChatView struct {
	Cluster   string `json:"cluster,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Name      string `json:"name,omitempty"`
	Section   string `json:"section,omitempty"`
}

// ChatEventType is the kind of a ChatEvent.
type ChatEventType string

const (
	ChatText      ChatEventType = "text"
	ChatThinking  ChatEventType = "thinking"
	ChatToolStart ChatEventType = "tool_start"
	ChatToolEnd   ChatEventType = "tool_end"
	ChatNote      ChatEventType = "note"
	ChatDone      ChatEventType = "done"
)

// ChatEvent is a part of the assistant's answer, as it streams.
type ChatEvent struct {
	Type ChatEventType `json:"type"`
	Text string        `json:"text,omitempty"`
	// Tool, ToolID, Input and IsError describe tool calls.
	Tool    string `json:"tool,omitempty"`
	ToolID  string `json:"toolId,omitempty"`
	Input   string `json:"input,omitempty"`
	IsError bool   `json:"isError,omitempty"`
	// Write marks tools that change the cluster, after approval.
	Write bool `json:"write,omitempty"`
	// Usage, on done: input, cached and output tokens of the turn.
	InputTokens  int64 `json:"inputTokens,omitempty"`
	CachedTokens int64 `json:"cachedTokens,omitempty"`
	OutputTokens int64 `json:"outputTokens,omitempty"`
}

const (
	assistantKeySecret = "assistant-foundry-api-key"
	assistantClient    = "Kube Desk assistant"
	defaultDeployment  = "claude-opus-5-5"
	defaultFallback    = "claude-opus-4-8"
	defaultRegion      = "global"
	// maxSavedChats is how many past conversations are kept.
	maxSavedChats = 100
	// maxAssistantTurns bounds the tool calls of one answer.
	maxAssistantTurns = 40
	// maxToolResult bounds what one tool result puts into the conversation.
	maxToolResult = 60_000
)

const assistantSystem = `You are the assistant inside Kube Desk, a desktop app for Kubernetes. You help the user understand and operate their clusters.

Your tools reach every cluster the user has in Kube Desk. Look things up instead of guessing: cluster_overview gives the whole cluster at a glance (nodes, every namespace with its pods by status, workloads not ready, restarting pods, recent warnings); list_resources, get_resource, get_logs, get_events, get_changes (what changed, and who changed it), incident_report and helm_releases give the details; get_runbooks has what the user's team wrote to do when something fails, so read it before proposing a fix and follow it; search and compare work across clusters. A user message may start with a <view> note saying what the user is looking at in the app: use it to resolve "this pod", "here" or "this cluster".

To change anything, call apply_manifest, delete_resource, scale or restart. Kube Desk shows each change to the user with a diff and what it affects, and makes it only if they approve, so propose the change with a clear reason rather than asking in chat whether you may. If they reject it, ask what they want instead. For risky changes, preview_impact first. You cannot run kubectl or a shell: when something needs one, give the user the command.

Secret values are masked in everything you see. Never ask the user for secrets.

Reply in the user's language. Lead with the answer or the finding, then the evidence; keep it short. Use Markdown, and name objects as kind namespace/name.`

// Assistant chats with Claude in Microsoft Foundry about the clusters, with
// the tools of the MCP server; every change waits for the user's approval.
type Assistant struct {
	agents   *Agents
	clusters *Clusters

	// key reads the API key; tests replace it, to keep the keychain out.
	key func() ([]byte, error)
	// google finds the Google credentials, for Vertex; tests replace it.
	google func(ctx context.Context) (*google.Credentials, error)

	mu    sync.Mutex
	chats map[string]*chat
}

type chat struct {
	mu       sync.Mutex
	busy     bool
	messages []anthropic.BetaMessageParam
	fallback betafallback.BetaFallbackState
	// title, created and model are kept with the conversation; model is the
	// provider and model its history comes from.
	title, created, model string
}

func newAssistant(a *Agents, c *Clusters) *Assistant {
	return &Assistant{
		agents: a, clusters: c, chats: map[string]*chat{},
		key: func() ([]byte, error) { return mygo.Secrets.Get(assistantKeySecret) },
		google: func(ctx context.Context) (*google.Credentials, error) {
			return google.FindDefaultCredentials(ctx, "https://www.googleapis.com/auth/cloud-platform")
		},
	}
}

func (as *Assistant) settings() AssistantSettings {
	s := as.clusters.assistantSettings()
	if s.Provider == "" {
		s.Provider = ProviderFoundry
	}
	if s.Deployment == "" {
		s.Deployment = defaultDeployment
	}
	if s.Effort == "" {
		s.Effort = "high"
	}
	if s.Provider == ProviderVertex && s.Region == "" {
		s.Region = defaultRegion
	}
	return s
}

// Info returns the assistant's setup, without the key.
func (as *Assistant) Info(ctx context.Context) AssistantInfo {
	s := as.settings()
	_, err := as.key()
	info := AssistantInfo{Settings: s, HasKey: err == nil}
	if s.Provider == ProviderVertex {
		creds, err := as.googleCredentials(ctx)
		if err != nil {
			info.GoogleError = err.Error()
		} else {
			info.Google = credentialsLabel(creds)
			if s.Project == "" {
				info.Settings.Project = creds.ProjectID
			}
		}
		info.Configured = err == nil && info.Settings.Project != ""
		return info
	}
	info.Configured = info.HasKey && s.Endpoint != ""
	return info
}

func (as *Assistant) googleCredentials(ctx context.Context) (*google.Credentials, error) {
	loadShellEnv() // GOOGLE_APPLICATION_CREDENTIALS may come from the shell
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	creds, err := as.google(ctx)
	if err != nil {
		return nil, errors.New("no Google credentials: run gcloud auth application-default login in a terminal, then try again")
	}
	return creds, nil
}

// credentialsLabel says what Google credentials are, without their secrets.
func credentialsLabel(c *google.Credentials) string {
	var f struct {
		Type        string `json:"type"`
		ClientEmail string `json:"client_email"`
	}
	_ = json.Unmarshal(c.JSON, &f)
	switch {
	case f.ClientEmail != "":
		return "service account " + f.ClientEmail
	case f.Type == "authorized_user":
		return "your gcloud login (application default credentials)"
	case f.Type != "":
		return strings.ReplaceAll(f.Type, "_", " ") + " credentials"
	}
	return "the credentials of this machine"
}

// Save keeps the settings, and the API key in the credential store; an
// empty key keeps the one stored.
func (as *Assistant) Save(ctx context.Context, s AssistantSettings, apiKey string) (AssistantInfo, error) {
	s.Endpoint = strings.TrimSpace(s.Endpoint)
	s.Deployment = strings.TrimSpace(s.Deployment)
	s.Fallback = strings.TrimSpace(s.Fallback)
	s.Project = strings.TrimSpace(s.Project)
	s.Region = strings.TrimSpace(s.Region)
	switch s.Effort {
	case "", "low", "medium", "high", "xhigh", "max":
	default:
		return AssistantInfo{}, fmt.Errorf("unknown effort %q", s.Effort)
	}
	switch s.Provider {
	case "", ProviderFoundry, ProviderVertex:
	default:
		return AssistantInfo{}, fmt.Errorf("unknown provider %q", s.Provider)
	}
	if key := strings.TrimSpace(apiKey); key != "" {
		if err := mygo.Secrets.Set(assistantKeySecret, []byte(key)); err != nil {
			return AssistantInfo{}, fmt.Errorf("could not keep the key in the keychain: %w", err)
		}
	}
	if err := as.clusters.setAssistantSettings(s); err != nil {
		return AssistantInfo{}, err
	}
	return as.Info(ctx), nil
}

// ForgetKey removes the API key from the credential store.
func (as *Assistant) ForgetKey() error {
	return mygo.Secrets.Delete(assistantKeySecret)
}

// client connects to the model, with the fallback answering requests the
// main one declines.
func (as *Assistant) client(ctx context.Context, s AssistantSettings) (*anthropic.BetaMessageService, error) {
	// The fallback goes first, to see the requests as the Messages API has
	// them, before Vertex rewrites them.
	var opts []option.RequestOption
	if s.Fallback != "" && s.Fallback != s.Deployment {
		opts = append(opts, option.WithMiddleware(betafallback.BetaRefusalFallbackMiddleware(
			[]anthropic.BetaFallbackParam{{Model: anthropic.Model(s.Fallback)}},
		)))
	}
	if s.Provider == ProviderVertex {
		creds, err := as.googleCredentials(ctx)
		if err != nil {
			return nil, err
		}
		project := s.Project
		if project == "" {
			project = creds.ProjectID
		}
		if project == "" {
			return nil, errors.New("set the Google Cloud project in the assistant's settings")
		}
		opts = append(opts, vertex.WithCredentials(context.Background(), s.Region, project, creds))
		if s.Endpoint != "" {
			opts = append(opts, option.WithBaseURL(strings.TrimRight(s.Endpoint, "/")+"/"))
		}
		c := anthropic.NewClient(opts...)
		return &c.Beta.Messages, nil
	}
	key, err := as.key()
	if err != nil {
		return nil, errors.New("set the API key of your Foundry resource in the assistant's settings")
	}
	if s.Endpoint == "" {
		return nil, errors.New("set your Foundry resource in the assistant's settings")
	}
	cfg := foundry.ClientConfig{APIKey: string(key)}
	if strings.HasPrefix(s.Endpoint, "https://") || strings.HasPrefix(s.Endpoint, "http://") {
		base := strings.TrimRight(s.Endpoint, "/")
		// The Target URI of a deployment ends in /v1/messages.
		base = strings.TrimSuffix(strings.TrimSuffix(base, "/v1/messages"), "/v1")
		if !strings.HasSuffix(base, "/anthropic") {
			base += "/anthropic"
		}
		cfg.BaseURL = base + "/"
	} else {
		cfg.Resource = s.Endpoint
	}
	c, err := foundry.NewClient(cfg, opts...)
	if err != nil {
		return nil, err
	}
	return &c.Beta.Messages, nil
}

// Test sends a short request with the settings, and returns the model's
// reply.
func (as *Assistant) Test(ctx context.Context) (string, error) {
	s := as.settings()
	c, err := as.client(ctx, s)
	if err != nil {
		return "", err
	}
	params := anthropic.BetaMessageNewParams{
		Model:        anthropic.Model(s.Deployment),
		MaxTokens:    1024,
		OutputConfig: anthropic.BetaOutputConfigParam{Effort: anthropic.BetaOutputConfigEffortLow},
		Messages:     []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock("Reply with the single word: ready"))},
	}
	msg, err := c.New(ctx, params)
	if err != nil && s.Fallback != "" && isBetaRejection(err) {
		plain := s
		plain.Fallback = ""
		if c, err2 := as.client(ctx, plain); err2 == nil {
			if _, err2 = c.New(ctx, params); err2 == nil {
				return "", errors.New("the model works, but rejects the fallback: clear the Fallback field")
			}
		}
	}
	if err != nil {
		return "", errors.New(apiErrorText(err, s))
	}
	for _, b := range msg.Content {
		if t, ok := b.AsAny().(anthropic.BetaTextBlock); ok {
			return fmt.Sprintf("%s answered: %s", msg.Model, strings.TrimSpace(t.Text)), nil
		}
	}
	return string(msg.Model) + " answered", nil
}

// isBetaRejection reports a 400 over a beta header, which a deployment may
// send for the fallback middleware's credit beta.
func isBetaRejection(err error) bool {
	var apiErr *anthropic.Error
	return errors.As(err, &apiErr) && apiErr.StatusCode == 400 && strings.Contains(strings.ToLower(apiErr.Error()), "beta")
}

// apiErrorText explains the errors people meet when setting up Foundry or
// Vertex.
func apiErrorText(err error, s AssistantSettings) string {
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) && s.Provider == ProviderVertex {
		switch apiErr.StatusCode {
		case 401:
			return "Google Cloud refused the credentials (401): run gcloud auth application-default login in a terminal, then try again"
		case 403:
			return fmt.Sprintf("Google Cloud denied access (403): the account needs the Vertex AI User role in project %s, and Claude enabled in Vertex AI Model Garden", s.Project)
		case 404:
			return fmt.Sprintf("Vertex AI has no model %q in region %s (404): enable it in Model Garden, or try another region", s.Deployment, s.Region)
		case 429:
			return "Vertex AI is rate limiting requests (429): wait a moment, or ask for a higher quota"
		}
		return fmt.Sprintf("Vertex AI answered %d: %s", apiErr.StatusCode, truncate(apiErr.Error(), 400))
	}
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case 401:
			return "Foundry refused the API key (401): copy the Key from your deployment's Details tab in the Foundry portal"
		case 403:
			return "Foundry denied access (403): your Azure account needs a role such as Foundry User on the resource"
		case 404:
			return fmt.Sprintf("Foundry has no deployment named %q (404): use the deployment name from the Foundry portal", s.Deployment)
		case 429:
			return "Foundry is rate limiting requests (429): wait a moment and try again"
		}
		return fmt.Sprintf("Foundry answered %d: %s", apiErr.StatusCode, truncate(apiErr.Error(), 400))
	}
	return friendlyError(err)
}

// Reset lets go of a conversation in memory; a saved one stays saved.
func (as *Assistant) Reset(chatID string) {
	as.mu.Lock()
	defer as.mu.Unlock()
	if c := as.chats[chatID]; c != nil {
		c.mu.Lock()
		busy := c.busy
		c.mu.Unlock()
		if !busy {
			delete(as.chats, chatID)
		}
	}
}

var chatIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

func (as *Assistant) chatsDir() string { return filepath.Join(as.clusters.dir, "assistant") }

func (as *Assistant) chatPath(id string) (string, error) {
	if !chatIDPattern.MatchString(id) || as.clusters.dir == "" {
		return "", errors.New("invalid chat")
	}
	return filepath.Join(as.chatsDir(), id+".json"), nil
}

// Chats returns the saved conversations, the latest first.
func (as *Assistant) Chats() []ChatSummary {
	entries, _ := os.ReadDir(as.chatsDir())
	var out []ChatSummary
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(as.chatsDir(), e.Name()))
		if err != nil {
			continue
		}
		var c struct {
			ID, Title, Updated string
		}
		if json.Unmarshal(b, &c) == nil && c.ID != "" {
			out = append(out, ChatSummary{ID: c.ID, Title: c.Title, Updated: c.Updated})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated > out[j].Updated })
	return out
}

// Open loads a saved conversation, to go on with it, and returns the
// page's turns.
func (as *Assistant) Open(id string) (string, error) {
	p, err := as.chatPath(id)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", errors.New("this chat is no longer saved")
	}
	var sc savedChat
	if err := json.Unmarshal(b, &sc); err != nil {
		return "", fmt.Errorf("this chat cannot be read: %w", err)
	}
	c := as.chat(id)
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.busy {
		c.messages, c.title, c.created, c.model = sc.Messages, sc.Title, sc.Created, sc.Model
	}
	return string(sc.Turns), nil
}

// SaveTurns saves a conversation: its history, and the page's turns.
func (as *Assistant) SaveTurns(id, turns string) error {
	p, err := as.chatPath(id)
	if err != nil {
		return err
	}
	if !json.Valid([]byte(turns)) {
		return errors.New("the turns are not JSON")
	}
	c := as.chat(id)
	c.mu.Lock()
	sc := savedChat{ID: id, Title: c.title, Created: c.created, Model: c.model, Messages: c.messages, Turns: json.RawMessage(turns), Updated: time.Now().UTC().Format(time.RFC3339)}
	c.mu.Unlock()
	if len(sc.Messages) == 0 {
		return nil
	}
	if sc.Created == "" {
		sc.Created = sc.Updated
	}
	b, err := json.Marshal(sc)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(as.chatsDir(), 0o700); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		return err
	}
	// Keep the latest conversations only.
	if list := as.Chats(); len(list) > maxSavedChats {
		for _, old := range list[maxSavedChats:] {
			_ = as.Delete(old.ID)
		}
	}
	return nil
}

// Delete removes a saved conversation.
func (as *Assistant) Delete(id string) error {
	p, err := as.chatPath(id)
	if err != nil {
		return err
	}
	as.Reset(id)
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// withoutThinking drops the thinking blocks of a history, which only the
// model that wrote them can read, when a conversation goes on with
// another model.
func withoutThinking(msgs []anthropic.BetaMessageParam) []anthropic.BetaMessageParam {
	out := make([]anthropic.BetaMessageParam, 0, len(msgs))
	for _, m := range msgs {
		if m.OfAssistant != nil {
			a := *m.OfAssistant
			var blocks []anthropic.BetaContentBlockParamUnion
			for _, b := range a.Content {
				if b.OfThinking == nil && b.OfRedactedThinking == nil {
					blocks = append(blocks, b)
				}
			}
			if len(blocks) == 0 {
				blocks = append(blocks, anthropic.NewBetaTextBlock("(no answer)"))
			}
			a.Content = blocks
			m = anthropic.BetaMessageParam{OfAssistant: &a}
		}
		out = append(out, m)
	}
	return out
}

func (as *Assistant) chat(id string) *chat {
	as.mu.Lock()
	defer as.mu.Unlock()
	c := as.chats[id]
	if c == nil {
		c = &chat{}
		as.chats[id] = c
	}
	return c
}

// assistantTools are the tools of the MCP server, for the assistant.
func assistantTools() []anthropic.BetaToolUnionParam {
	tools := make([]anthropic.BetaToolUnionParam, 0, len(toolDefs))
	for _, d := range toolDefs {
		req, _ := d.InputSchema["required"].([]string)
		t := anthropic.BetaToolParam{
			Name:        d.Name,
			Description: anthropic.String(d.Description),
			InputSchema: anthropic.BetaToolInputSchemaParam{Properties: d.InputSchema["properties"], Required: req},
			// Arguments stream as they are written; inputs are checked before
			// they run.
			EagerInputStreaming: anthropic.Bool(true),
		}
		tools = append(tools, anthropic.BetaToolUnionParam{OfTool: &t})
	}
	return tools
}

func isWriteTool(name string) bool {
	return name == "apply_manifest" || name == "delete_resource" || name == "scale" || name == "restart"
}

// viewNote tells the model what the user looks at.
func (as *Assistant) viewNote(v ChatView) string {
	if v.Cluster == "" {
		return "<view>The user is on the list of clusters.</view>\n\n"
	}
	parts := []string{fmt.Sprintf("cluster %q (id %s)", as.clusters.name(v.Cluster), v.Cluster)}
	if v.Namespace != "" {
		parts = append(parts, "namespace "+v.Namespace)
	} else {
		parts = append(parts, "all namespaces")
	}
	if v.Section != "" {
		parts = append(parts, "page "+v.Section)
	}
	if v.Kind != "" && v.Name != "" {
		parts = append(parts, fmt.Sprintf("open object: %s %s", v.Kind, v.Name))
	}
	return "<view>The user is looking at " + strings.Join(parts, ", ") + ".</view>\n\n"
}

// Send asks the assistant something and streams its answer, tool calls
// included, until it is done or the page stops listening.
func (as *Assistant) Send(ctx context.Context, chatID, text string, view ChatView, events *mygo.Channel[ChatEvent]) error {
	return as.send(ctx, chatID, text, view, events.Send)
}

func (as *Assistant) send(ctx context.Context, chatID, text string, view ChatView, emit func(ChatEvent) error) error {
	s := as.settings()
	client, err := as.client(ctx, s)
	if err != nil {
		return err
	}
	model := string(s.Provider) + ":" + s.Deployment
	c := as.chat(chatID)
	c.mu.Lock()
	if c.busy {
		c.mu.Unlock()
		return errors.New("the assistant is still answering: wait, or stop it first")
	}
	c.busy = true
	if c.model != "" && c.model != model {
		c.messages = withoutThinking(c.messages)
	}
	c.model = model
	if c.title == "" {
		c.title = truncate(strings.Join(strings.Fields(text), " "), 80)
		c.created = time.Now().UTC().Format(time.RFC3339)
	}
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.busy = false
		c.mu.Unlock()
	}()

	// The history only grows: what is sent once is sent again unchanged, as
	// the thinking blocks of the model ask.
	messages := append([]anthropic.BetaMessageParam(nil), c.messages...)
	messages = append(messages, anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(as.viewNote(view)+text)))
	commit := func() {
		c.mu.Lock()
		c.messages = messages
		c.mu.Unlock()
	}
	tools := assistantTools()
	var usage ChatEvent
	usage.Type = ChatDone
	for turn := 0; turn < maxAssistantTurns; turn++ {
		params := anthropic.BetaMessageNewParams{
			Model:        anthropic.Model(s.Deployment),
			MaxTokens:    64000,
			System:       []anthropic.BetaTextBlockParam{{Text: assistantSystem}},
			Messages:     messages,
			Tools:        tools,
			Thinking:     anthropic.BetaThinkingConfigParamUnion{OfAdaptive: &anthropic.BetaThinkingConfigAdaptiveParam{Display: anthropic.BetaThinkingConfigAdaptiveDisplaySummarized}},
			OutputConfig: anthropic.BetaOutputConfigParam{Effort: anthropic.BetaOutputConfigEffort(s.Effort)},
			// Tools, the system prompt and the history before this turn are
			// the same every request: cache them.
			CacheControl: anthropic.NewBetaCacheControlEphemeralParam(),
		}
		stream := client.NewStreaming(ctx, params, betafallback.WithBetaFallbackState(&c.fallback))
		msg := anthropic.BetaMessage{}
		// The raw arguments of each tool call, as streamed: the SDK empties a
		// cut-off input to {}, which must not run as if it were complete.
		inputs := map[int64]*strings.Builder{}
		for stream.Next() {
			ev := stream.Current()
			if err := msg.Accumulate(ev); err != nil {
				return err
			}
			var out *ChatEvent
			switch e := ev.AsAny().(type) {
			case anthropic.BetaRawContentBlockDeltaEvent:
				switch d := e.Delta.AsAny().(type) {
				case anthropic.BetaTextDelta:
					out = &ChatEvent{Type: ChatText, Text: d.Text}
				case anthropic.BetaThinkingDelta:
					if d.Thinking != "" {
						out = &ChatEvent{Type: ChatThinking, Text: d.Thinking}
					}
				case anthropic.BetaInputJSONDelta:
					b := inputs[e.Index]
					if b == nil {
						b = &strings.Builder{}
						inputs[e.Index] = b
					}
					b.WriteString(d.PartialJSON)
				}
			case anthropic.BetaRawContentBlockStartEvent:
				switch b := e.ContentBlock.AsAny().(type) {
				case anthropic.BetaToolUseBlock:
					out = &ChatEvent{Type: ChatToolStart, Tool: b.Name, ToolID: b.ID, Write: isWriteTool(b.Name)}
				case anthropic.BetaFallbackBlock:
					out = &ChatEvent{Type: ChatNote, Text: fmt.Sprintf("%s declined; %s continues", b.From.Model, b.To.Model)}
				}
			}
			if out != nil && emit(*out) != nil {
				return nil // the page stopped listening
			}
		}
		if err := stream.Err(); err != nil {
			if ctx.Err() != nil {
				commit() // the question stays, the unfinished answer goes
				return nil
			}
			if s.Fallback != "" && len(msg.Content) == 0 && isBetaRejection(err) {
				// The deployment takes no fallback: go on without it.
				s.Fallback = ""
				if client, err = as.client(ctx, s); err != nil {
					return err
				}
				_ = emit(ChatEvent{Type: ChatNote, Text: "This model rejects the fallback, so this chat goes on without it. Clear the Fallback field in the settings."})
				turn--
				continue
			}
			return errors.New(apiErrorText(err, s))
		}
		usage.InputTokens += msg.Usage.InputTokens + msg.Usage.CacheReadInputTokens + msg.Usage.CacheCreationInputTokens
		usage.CachedTokens += msg.Usage.CacheReadInputTokens
		usage.OutputTokens += msg.Usage.OutputTokens

		switch msg.StopReason {
		case anthropic.BetaStopReasonRefusal:
			commit()
			why := "The model declined this request."
			if msg.StopDetails.Explanation != "" {
				why += " " + msg.StopDetails.Explanation
			}
			if s.Fallback == "" {
				why += " A fallback deployment, in the assistant's settings, can answer such requests."
			}
			_ = emit(ChatEvent{Type: ChatNote, Text: why, IsError: true})
			return emit(usage)
		case anthropic.BetaStopReasonMaxTokens:
			// A cut-off answer may hold a partial tool call: run nothing.
			messages = append(messages, msg.ToParam())
			commit()
			_ = emit(ChatEvent{Type: ChatNote, Text: "The answer reached its length limit.", IsError: true})
			return emit(usage)
		}
		messages = append(messages, msg.ToParam())
		if msg.StopReason != anthropic.BetaStopReasonToolUse {
			commit()
			return emit(usage)
		}

		// Run the tools the model called, and answer every one of them in a
		// single message.
		var results []anthropic.BetaContentBlockParamUnion
		for i, block := range msg.Content {
			use, ok := block.AsAny().(anthropic.BetaToolUseBlock)
			if !ok {
				continue
			}
			if ctx.Err() != nil {
				results = append(results, anthropic.NewBetaToolResultBlock(use.ID, "The user stopped the assistant before this ran.", true))
				continue
			}
			raw := "{}"
			if b := inputs[int64(i)]; b != nil && b.Len() > 0 {
				raw = b.String()
			}
			out, isErr := as.runTool(ctx, use.Name, raw)
			if emit(ChatEvent{Type: ChatToolEnd, Tool: use.Name, ToolID: use.ID, Input: inputSummary(raw), IsError: isErr, Text: truncate(out, 600), Write: isWriteTool(use.Name)}) != nil {
				results = append(results, anthropic.NewBetaToolResultBlock(use.ID, out, isErr))
				ctx = context.Background() // keep the history whole, then stop
				continue
			}
			results = append(results, anthropic.NewBetaToolResultBlock(use.ID, out, isErr))
		}
		messages = append(messages, anthropic.NewBetaUserMessage(results...))
		commit()
		if ctx.Err() != nil {
			return nil
		}
	}
	_ = emit(ChatEvent{Type: ChatNote, Text: "The assistant stopped after many tool calls: ask it to continue if needed.", IsError: true})
	return emit(usage)
}

// runTool runs a tool the model called, with the assistant's access: every
// cluster shown, changes after the user approves.
func (as *Assistant) runTool(ctx context.Context, name, raw string) (string, bool) {
	var args toolArgs
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		// A streamed input can arrive cut short: say so rather than guess.
		return "INVALID_JSON: the arguments of this call were not valid JSON; call the tool again with complete arguments", true
	}
	text, outcome, err := as.agents.dispatch(ctx, assistantClient, name, args, as.agents.assistantPolicy())
	act := AgentActivity{Client: assistantClient, Tool: name, Cluster: as.clusters.name(args.Cluster), Detail: inputSummary(raw), Outcome: outcome}
	if args.Cluster == "" {
		act.Cluster = ""
	}
	if err != nil && act.Outcome == "" {
		act.Outcome = "error"
	} else if act.Outcome == "" {
		act.Outcome = "ok"
	}
	as.agents.record(act)
	if err != nil {
		return err.Error(), true
	}
	if len(text) > maxToolResult {
		text = text[:maxToolResult] + "\n… (cut: ask for less, with a namespace, a label selector or fewer lines)"
	}
	return text, false
}

// inputSummary shows a tool call's arguments in a line, without manifests.
func inputSummary(raw string) string {
	var m map[string]any
	if json.Unmarshal([]byte(raw), &m) != nil {
		return ""
	}
	var parts []string
	for _, k := range []string{"cluster", "kind", "namespace", "name", "pod", "query", "replicas", "action"} {
		if v, ok := m[k]; ok && fmt.Sprint(v) != "" {
			if k == "cluster" {
				parts = append(parts, fmt.Sprint(v))
				continue
			}
			parts = append(parts, fmt.Sprintf("%s=%v", k, v))
		}
	}
	return strings.Join(parts, " ")
}
