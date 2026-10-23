package main

import (
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// SourceKind tells where the kubeconfig of a cluster comes from.
type SourceKind string

const (
	// SourceDefault is the kubeconfig kubectl uses: $KUBECONFIG, or
	// ~/.kube/config.
	SourceDefault SourceKind = "default"
	// SourceFile is a kubeconfig file the user added, read where it is.
	SourceFile SourceKind = "file"
	// SourceStored is a kubeconfig pasted or created in the app, kept in the
	// app's data directory.
	SourceStored SourceKind = "stored"
)

// defaultSourceKey stands for the merged default kubeconfig in cluster IDs.
const defaultSourceKey = "~default"

// Cluster is a context of a kubeconfig: a cluster the app connects to.
type Cluster struct {
	ID string `json:"id"`
	// Name is the alias the user gave the cluster, or its context's name.
	Name    string `json:"name"`
	Context string `json:"context"`
	Server  string `json:"server"`
	// Namespace is the default namespace of the context.
	Namespace  string     `json:"namespace"`
	Source     string     `json:"source"`
	SourceKind SourceKind `json:"sourceKind"`
	Color      string     `json:"color,omitempty"`
	Hidden     bool       `json:"hidden"`
	// Production asks to type the cluster's name before every change.
	Production bool `json:"production"`
	// ReadOnly refuses every change to the cluster, in the app.
	ReadOnly bool `json:"readOnly"`
	// Agent is what AI agents may do with the cluster, through the app.
	Agent AgentAccess `json:"agent"`
	// Current marks the current context of the default kubeconfig.
	Current bool `json:"current"`
}

// SourceError is a kubeconfig file that could not be read.
type SourceError struct {
	Source string `json:"source"`
	Error  string `json:"error"`
}

// ClusterList is every cluster the app knows of, in the user's order.
type ClusterList struct {
	Clusters []Cluster    `json:"clusters"`
	Errors   []SourceError `json:"errors"`
}

// ClusterPrefs are what the user changes about a cluster in the app.
type ClusterPrefs struct {
	Alias      string `json:"alias,omitempty"`
	Color      string `json:"color,omitempty"`
	Hidden     bool   `json:"hidden,omitempty"`
	Production bool        `json:"production,omitempty"`
	ReadOnly   bool        `json:"readOnly,omitempty"`
	Agent      AgentAccess `json:"agent,omitempty"`
}

// Theme is the appearance the user chose.
type Theme string

const (
	ThemeSystem Theme = "system"
	ThemeLight  Theme = "light"
	ThemeDark   Theme = "dark"
)

// AppState is what the app restores at startup.
type AppState struct {
	// Active is the ID of the cluster shown last.
	Active string `json:"active"`
	// Namespaces is the namespace chosen last in each cluster, by ID; "" is
	// all namespaces.
	Namespaces map[string]string `json:"namespaces"`
	Theme      Theme             `json:"theme"`
}

// ManualCluster describes a cluster to add from its address and a token.
type ManualCluster struct {
	Name   string `json:"name"`
	Server string `json:"server"`
	Token  string `json:"token"`
	// CAData is the PEM certificate of the cluster's CA; empty uses the
	// system's roots, unless Insecure.
	CAData    string `json:"caData,omitempty"`
	Insecure  bool   `json:"insecure,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

// KubeconfigCandidate is a kubeconfig file found on the machine that the app
// does not read yet.
type KubeconfigCandidate struct {
	Path     string   `json:"path"`
	Contexts []string `json:"contexts"`
}

// ClusterStatus is the outcome of connecting to a cluster.
type ClusterStatus struct {
	OK      bool   `json:"ok"`
	Version string `json:"version,omitempty"`
	Error   string `json:"error,omitempty"`
}

type clusterStore struct {
	Files      []string                `json:"files"`
	Prefs      map[string]ClusterPrefs `json:"prefs"`
	Order      []string                `json:"order"`
	Active     string                  `json:"active"`
	Namespaces map[string]string       `json:"namespaces"`
	Theme      Theme                   `json:"theme,omitempty"`
	Watch      []WatchTarget           `json:"watch,omitempty"`
	Agents     AgentSettings           `json:"agents,omitempty"`
	Assistant  AssistantSettings       `json:"assistant,omitempty"`
	// RunbookDir is the folder of the runbooks; "" is the app's own.
	RunbookDir string `json:"runbookDir,omitempty"`
	// LastSeen is when each cluster was last reached, by ID.
	LastSeen map[string]string `json:"lastSeen,omitempty"`
}

// conn is a connection to a cluster, reused until its kubeconfig changes.
type conn struct {
	cluster Cluster
	cfg     *rest.Config
	cs      kubernetes.Interface
	dyn     dynamic.Interface
	disc    discovery.CachedDiscoveryInterface
	mapper  *restmapper.DeferredDiscoveryRESTMapper
	src     source
	stamp   string
	// snapshot is the namespace snapshot the connection serves, if it is one.
	snapshot *snapshotData
}

// Clusters manages the clusters the app knows of: the contexts of the
// default kubeconfig, of kubeconfig files the user added and of kubeconfigs
// pasted into the app.
type Clusters struct {
	mu    sync.Mutex
	dir   string
	store clusterStore
	conns map[string]*conn
	// snapshots are the IDs of the snapshots opened, as list found them.
	snapshots map[string]bool
}

func newClusters() *Clusters {
	return &Clusters{conns: map[string]*conn{}}
}

// init loads the store; it runs once the app is ready, when its paths are.
func (c *Clusters) init() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return err
	}
	c.dir = dir
	if err := os.MkdirAll(c.storedDir(), 0o700); err != nil {
		return err
	}
	data, err := os.ReadFile(c.storePath())
	if err == nil {
		_ = json.Unmarshal(data, &c.store)
	}
	if c.store.Prefs == nil {
		c.store.Prefs = map[string]ClusterPrefs{}
	}
	if c.store.Namespaces == nil {
		c.store.Namespaces = map[string]string{}
	}
	c.applyTheme()
	return nil
}

func (c *Clusters) storePath() string { return filepath.Join(c.dir, "clusters.json") }
func (c *Clusters) storedDir() string { return filepath.Join(c.dir, "kubeconfigs") }

func (c *Clusters) save() error {
	data, err := json.MarshalIndent(c.store, "", "  ")
	if err != nil {
		return err
	}
	tmp := c.storePath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.storePath())
}

func clusterID(source, context string) string {
	sum := sha1.Sum([]byte(source + "\x00" + context))
	return hex.EncodeToString(sum[:8])
}

type source struct {
	key  string // the path, or defaultSourceKey
	kind SourceKind
	// files are the files the source reads, whose changes reset connections.
	files []string
}

func (c *Clusters) sources() []source {
	loadShellEnv()
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	out := []source{{key: defaultSourceKey, kind: SourceDefault, files: rules.GetLoadingPrecedence()}}
	for _, f := range c.store.Files {
		out = append(out, source{key: f, kind: SourceFile, files: []string{f}})
	}
	entries, _ := os.ReadDir(c.storedDir())
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		p := filepath.Join(c.storedDir(), e.Name())
		out = append(out, source{key: p, kind: SourceStored, files: []string{p}})
	}
	if c.dir != "" {
		snaps, _ := os.ReadDir(c.snapshotsDir())
		for _, e := range snaps {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".kubedesk") {
				continue
			}
			p := filepath.Join(c.snapshotsDir(), e.Name())
			out = append(out, source{key: p, kind: SourceSnapshot, files: []string{p}})
		}
	}
	return out
}

func (s source) load() (*clientcmdapi.Config, error) {
	if s.kind == SourceSnapshot {
		return nil, errors.New("a snapshot has no kubeconfig")
	}
	if s.kind == SourceDefault {
		return clientcmd.NewDefaultClientConfigLoadingRules().Load()
	}
	return clientcmd.LoadFromFile(s.key)
}

func (s source) label() string {
	if s.kind == SourceDefault {
		return strings.Join(s.files, string(os.PathListSeparator))
	}
	return s.key
}

func (s source) stamp() string {
	var b strings.Builder
	for _, f := range s.files {
		if st, err := os.Stat(f); err == nil {
			fmt.Fprintf(&b, "%s:%d:%d;", f, st.ModTime().UnixNano(), st.Size())
		}
	}
	return b.String()
}

// list returns the clusters; c.mu must be held.
func (c *Clusters) list() (ClusterList, map[string]source) {
	var res ClusterList
	bySource := map[string]source{}
	c.snapshots = map[string]bool{}
	for _, s := range c.sources() {
		if s.kind == SourceSnapshot {
			d, err := loadSnapshot(s)
			if err != nil {
				res.Errors = append(res.Errors, SourceError{Source: s.label(), Error: err.Error()})
				continue
			}
			cl := snapshotCluster(s, d)
			if p, ok := c.store.Prefs[cl.ID]; ok {
				if p.Alias != "" {
					cl.Name = p.Alias
				}
				cl.Color, cl.Hidden = p.Color, p.Hidden
			}
			res.Clusters = append(res.Clusters, cl)
			bySource[cl.ID] = s
			c.snapshots[cl.ID] = true
			continue
		}
		cfg, err := s.load()
		if err != nil {
			if s.kind == SourceDefault && errors.Is(err, os.ErrNotExist) {
				continue
			}
			res.Errors = append(res.Errors, SourceError{Source: s.label(), Error: err.Error()})
			continue
		}
		names := make([]string, 0, len(cfg.Contexts))
		for name := range cfg.Contexts {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			kctx := cfg.Contexts[name]
			cl := Cluster{
				ID:         clusterID(s.key, name),
				Name:       name,
				Context:    name,
				Namespace:  kctx.Namespace,
				Source:     s.label(),
				SourceKind: s.kind,
				Current:    s.kind == SourceDefault && cfg.CurrentContext == name,
			}
			if kc, ok := cfg.Clusters[kctx.Cluster]; ok {
				cl.Server = kc.Server
			}
			if p, ok := c.store.Prefs[cl.ID]; ok {
				if p.Alias != "" {
					cl.Name = p.Alias
				}
				cl.Color = p.Color
				cl.Hidden = p.Hidden
				cl.Production = p.Production
				cl.ReadOnly = p.ReadOnly
				cl.Agent = p.Agent
			}
			res.Clusters = append(res.Clusters, cl)
			bySource[cl.ID] = s
		}
	}
	order := map[string]int{}
	for i, id := range c.store.Order {
		order[id] = i
	}
	sort.SliceStable(res.Clusters, func(i, j int) bool {
		oi, iok := order[res.Clusters[i].ID]
		oj, jok := order[res.Clusters[j].ID]
		switch {
		case iok && jok:
			return oi < oj
		case iok != jok:
			return iok
		}
		return false
	})
	return res, bySource
}

// List returns every cluster the app knows of, hidden ones included.
func (c *Clusters) List() ClusterList {
	c.mu.Lock()
	defer c.mu.Unlock()
	res, _ := c.list()
	return res
}

// State returns what the app restores at startup.
func (c *Clusters) State() AppState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return AppState{Active: c.store.Active, Namespaces: cloneMap(c.store.Namespaces), Theme: c.theme()}
}

func cloneMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (c *Clusters) theme() Theme {
	switch c.store.Theme {
	case ThemeLight, ThemeDark:
		return c.store.Theme
	}
	return ThemeSystem
}

// applyTheme gives the app the saved appearance; c.mu must be held.
func (c *Clusters) applyTheme() {
	mygo.Theme.SetSource(mygo.ThemeSource(c.theme()))
}

// SetTheme switches the app to light, dark or the system's appearance, and
// remembers it.
func (c *Clusters) SetTheme(theme Theme) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store.Theme = theme
	c.applyTheme()
	return c.save()
}

// SetActive remembers the cluster shown last.
func (c *Clusters) SetActive(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store.Active = id
	return c.save()
}

// SetNamespace remembers the namespace chosen in a cluster; "" is all
// namespaces.
func (c *Clusters) SetNamespace(id, namespace string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store.Namespaces[id] = namespace
	return c.save()
}

// Update changes the alias, color or visibility of a cluster.
func (c *Clusters) Update(id string, prefs ClusterPrefs) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	prefs.Alias = strings.TrimSpace(prefs.Alias)
	if prefs == (ClusterPrefs{}) {
		delete(c.store.Prefs, id)
	} else {
		c.store.Prefs[id] = prefs
	}
	return c.save()
}

// SetHidden hides a cluster from the switcher, or shows it again.
func (c *Clusters) SetHidden(id string, hidden bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.store.Prefs[id]
	p.Hidden = hidden
	if p == (ClusterPrefs{}) {
		delete(c.store.Prefs, id)
	} else {
		c.store.Prefs[id] = p
	}
	return c.save()
}

// Reorder sets the order of the clusters in the switcher.
func (c *Clusters) Reorder(ids []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store.Order = slices.Clone(ids)
	return c.save()
}

// Remove takes a cluster out of the app. A pasted cluster is deleted; a
// cluster of a kubeconfig file is hidden, and the file is no longer read
// when it was the last of its clusters shown. The kubeconfig files of the
// machine are never changed.
func (c *Clusters) Remove(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	list, bySource := c.list()
	s, ok := bySource[id]
	if !ok {
		return errors.New("unknown cluster")
	}
	var cl Cluster
	for _, x := range list.Clusters {
		if x.ID == id {
			cl = x
		}
	}
	delete(c.conns, id)
	switch s.kind {
	case SourceSnapshot:
		if err := os.Remove(s.key); err != nil && !os.IsNotExist(err) {
			return err
		}
		delete(c.store.Prefs, id)
		c.store.Order = slices.DeleteFunc(c.store.Order, func(x string) bool { return x == id })
	case SourceStored:
		cfg, err := clientcmd.LoadFromFile(s.key)
		if err != nil {
			return err
		}
		removeContext(cfg, cl.Context)
		delete(c.store.Prefs, id)
		if len(cfg.Contexts) == 0 {
			if err := os.Remove(s.key); err != nil {
				return err
			}
		} else if err := clientcmd.WriteToFile(*cfg, s.key); err != nil {
			return err
		}
	case SourceFile:
		visible := 0
		for _, x := range list.Clusters {
			if bySource[x.ID].key == s.key && !x.Hidden && x.ID != id {
				visible++
			}
		}
		if visible == 0 {
			c.store.Files = slices.DeleteFunc(c.store.Files, func(f string) bool { return f == s.key })
			for _, x := range list.Clusters {
				if bySource[x.ID].key == s.key {
					delete(c.store.Prefs, x.ID)
				}
			}
		} else {
			p := c.store.Prefs[id]
			p.Hidden = true
			c.store.Prefs[id] = p
		}
	default:
		p := c.store.Prefs[id]
		p.Hidden = true
		c.store.Prefs[id] = p
	}
	if c.store.Active == id {
		c.store.Active = ""
	}
	c.store.Watch = slices.DeleteFunc(c.store.Watch, func(t WatchTarget) bool { return t.Cluster == id })
	return c.save()
}

// removeContext deletes a context and the cluster and user only it uses.
func removeContext(cfg *clientcmdapi.Config, name string) {
	kctx, ok := cfg.Contexts[name]
	if !ok {
		return
	}
	delete(cfg.Contexts, name)
	clusterUsed, userUsed := false, false
	for _, other := range cfg.Contexts {
		clusterUsed = clusterUsed || other.Cluster == kctx.Cluster
		userUsed = userUsed || other.AuthInfo == kctx.AuthInfo
	}
	if !clusterUsed {
		delete(cfg.Clusters, kctx.Cluster)
	}
	if !userUsed {
		delete(cfg.AuthInfos, kctx.AuthInfo)
	}
	if cfg.CurrentContext == name {
		cfg.CurrentContext = ""
	}
}

// PickFiles asks for kubeconfig files and adds them.
func (c *Clusters) PickFiles(ctx context.Context) ([]Cluster, error) {
	home, _ := os.UserHomeDir()
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:          mygo.CallerWindow(ctx),
		Title:           "Add kubeconfig files",
		ButtonLabel:     "Add",
		DefaultPath:     filepath.Join(home, ".kube"),
		Multiple:        true,
		ShowHiddenFiles: true,
	})
	if err != nil {
		return nil, err
	}
	var added []Cluster
	for _, p := range paths {
		cls, err := c.AddFile(p)
		if err != nil {
			return added, fmt.Errorf("%s: %w", filepath.Base(p), err)
		}
		added = append(added, cls...)
	}
	return added, nil
}

// AddFile reads the kubeconfig file at path, where it is, from now on, and
// returns its clusters.
func (c *Clusters) AddFile(path string) ([]Cluster, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	cfg, err := clientcmd.LoadFromFile(path)
	if err != nil {
		return nil, fmt.Errorf("not a kubeconfig: %w", err)
	}
	if len(cfg.Contexts) == 0 {
		return nil, errors.New("the kubeconfig has no contexts")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if slices.Contains(clientcmd.NewDefaultClientConfigLoadingRules().GetLoadingPrecedence(), path) {
		return nil, errors.New("this is the default kubeconfig, which the app reads already")
	}
	if slices.Contains(c.store.Files, path) {
		return nil, errors.New("this kubeconfig was added already")
	}
	c.store.Files = append(c.store.Files, path)
	if err := c.save(); err != nil {
		return nil, err
	}
	return c.clustersOf(path), nil
}

// clustersOf returns the clusters of a source; c.mu must be held.
func (c *Clusters) clustersOf(key string) []Cluster {
	list, bySource := c.list()
	var out []Cluster
	for _, cl := range list.Clusters {
		if bySource[cl.ID].key == key {
			out = append(out, cl)
		}
	}
	return out
}

// AddKubeconfig keeps a pasted kubeconfig in the app and returns its
// clusters. A name, when given, becomes the alias of its clusters.
func (c *Clusters) AddKubeconfig(name, content string) ([]Cluster, error) {
	cfg, err := clientcmd.Load([]byte(content))
	if err != nil {
		return nil, fmt.Errorf("not a kubeconfig: %w", err)
	}
	if len(cfg.Contexts) == 0 {
		return nil, errors.New("the kubeconfig has no contexts")
	}
	for name, kctx := range cfg.Contexts {
		if _, ok := cfg.Clusters[kctx.Cluster]; !ok {
			return nil, fmt.Errorf("context %q refers to cluster %q, which the kubeconfig lacks", name, kctx.Cluster)
		}
		if _, ok := cfg.AuthInfos[kctx.AuthInfo]; !ok && kctx.AuthInfo != "" {
			return nil, fmt.Errorf("context %q refers to user %q, which the kubeconfig lacks", name, kctx.AuthInfo)
		}
	}
	return c.addStored(strings.TrimSpace(name), cfg)
}

// AddManual adds a cluster from its address and a bearer token.
func (c *Clusters) AddManual(m ManualCluster) ([]Cluster, error) {
	m.Name = strings.TrimSpace(m.Name)
	m.Server = strings.TrimSpace(m.Server)
	if m.Server == "" {
		return nil, errors.New("the API server address is required")
	}
	if !strings.HasPrefix(m.Server, "https://") && !strings.HasPrefix(m.Server, "http://") {
		m.Server = "https://" + m.Server
	}
	name := m.Name
	if name == "" {
		name = strings.TrimPrefix(strings.TrimPrefix(m.Server, "https://"), "http://")
	}
	cfg := clientcmdapi.NewConfig()
	kc := clientcmdapi.NewCluster()
	kc.Server = m.Server
	kc.InsecureSkipTLSVerify = m.Insecure
	if ca := strings.TrimSpace(m.CAData); ca != "" && !m.Insecure {
		kc.CertificateAuthorityData = []byte(ca + "\n")
	}
	user := clientcmdapi.NewAuthInfo()
	user.Token = strings.TrimSpace(m.Token)
	kctx := clientcmdapi.NewContext()
	kctx.Cluster, kctx.AuthInfo, kctx.Namespace = name, name, strings.TrimSpace(m.Namespace)
	cfg.Clusters[name], cfg.AuthInfos[name], cfg.Contexts[name] = kc, user, kctx
	cfg.CurrentContext = name
	return c.addStored(m.Name, cfg)
}

func (c *Clusters) addStored(alias string, cfg *clientcmdapi.Config) ([]Cluster, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	path := filepath.Join(c.storedDir(), time.Now().Format("20060102-150405")+"-"+hex.EncodeToString(b)+".yaml")
	if err := clientcmd.WriteToFile(*cfg, path); err != nil {
		return nil, err
	}
	_ = os.Chmod(path, 0o600)
	if alias != "" {
		for ctxName := range cfg.Contexts {
			a := alias
			if len(cfg.Contexts) > 1 {
				a = alias + "/" + ctxName
			}
			c.store.Prefs[clusterID(path, ctxName)] = ClusterPrefs{Alias: a}
		}
		if err := c.save(); err != nil {
			return nil, err
		}
	}
	return c.clustersOf(path), nil
}

// Discover finds kubeconfig files in ~/.kube that the app does not read.
func (c *Clusters) Discover() []KubeconfigCandidate {
	c.mu.Lock()
	known := map[string]bool{}
	for _, f := range c.store.Files {
		known[f] = true
	}
	c.mu.Unlock()
	loadShellEnv()
	for _, f := range clientcmd.NewDefaultClientConfigLoadingRules().GetLoadingPrecedence() {
		known[f] = true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	dir := filepath.Join(home, ".kube")
	entries, _ := os.ReadDir(dir)
	var out []KubeconfigCandidate
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		info, err := e.Info()
		if err != nil || e.IsDir() || known[p] || info.Size() > 1<<20 || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		cfg, err := clientcmd.LoadFromFile(p)
		if err != nil || len(cfg.Contexts) == 0 {
			continue
		}
		cand := KubeconfigCandidate{Path: p}
		for name := range cfg.Contexts {
			cand.Contexts = append(cand.Contexts, name)
		}
		sort.Strings(cand.Contexts)
		out = append(out, cand)
	}
	return out
}

// Test connects to a cluster and returns its version.
func (c *Clusters) Test(ctx context.Context, id string) ClusterStatus {
	cn, err := c.conn(id)
	if err != nil {
		return ClusterStatus{Error: err.Error()}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	v, err := serverVersion(ctx, cn)
	if err != nil {
		return ClusterStatus{Error: friendlyError(err)}
	}
	c.seen(id, time.Now())
	return ClusterStatus{OK: true, Version: v}
}

// seen records that a cluster answered, at most once an hour.
func (c *Clusters) seen(id string, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if last, err := time.Parse(time.RFC3339, c.store.LastSeen[id]); err == nil && now.Sub(last) < time.Hour {
		return
	}
	if c.store.LastSeen == nil {
		c.store.LastSeen = map[string]string{}
	}
	c.store.LastSeen[id] = now.UTC().Format(time.RFC3339)
	if c.dir != "" {
		_ = c.save()
	}
}

func (c *Clusters) agentSettings() AgentSettings {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.store.Agents
	if s.Port == 0 {
		s.Port = defaultAgentPort
	}
	return s
}

func (c *Clusters) setAgentSettings(s AgentSettings) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store.Agents = s
	return c.save()
}

func (c *Clusters) assistantSettings() AssistantSettings {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.store.Assistant
}

func (c *Clusters) setAssistantSettings(s AssistantSettings) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store.Assistant = s
	return c.save()
}

func (c *Clusters) agentAccess(id string) AgentAccess {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.store.Prefs[id].Agent
}

func (c *Clusters) setAgentAccess(id string, access AgentAccess) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.store.Prefs[id]
	p.Agent = access
	if p == (ClusterPrefs{}) {
		delete(c.store.Prefs, id)
	} else {
		c.store.Prefs[id] = p
	}
	return c.save()
}

// watchTargets returns what the watchlist watches.
func (c *Clusters) watchTargets() []WatchTarget {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.store.Watch)
}

// setWatched adds a target to the watchlist, or removes it.
func (c *Clusters) setWatched(cluster, namespace string, on bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := WatchTarget{Cluster: cluster, Namespace: namespace}
	c.store.Watch = slices.DeleteFunc(c.store.Watch, func(x WatchTarget) bool { return x == t })
	if on {
		c.store.Watch = append(c.store.Watch, t)
	}
	return c.save()
}

// errReadOnly is the error of changes to a read-only cluster.
var errReadOnly = errors.New("this cluster is read-only in Kube Desk: turn it off in the cluster's settings to make changes")

// writable reports whether the app may change a cluster.
func (c *Clusters) writable(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.store.Prefs[id].ReadOnly {
		return errReadOnly
	}
	if c.snapshots[id] {
		return errSnapshot
	}
	return nil
}

// name returns the name the app shows for a cluster.
func (c *Clusters) name(id string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cn, ok := c.conns[id]; ok {
		if a := c.store.Prefs[id].Alias; a != "" {
			return a
		}
		return cn.cluster.Context
	}
	list, _ := c.list()
	for _, cl := range list.Clusters {
		if cl.ID == id {
			return cl.Name
		}
	}
	return id
}

// Reconnect drops the connection to a cluster, so that the next request
// reads its kubeconfig and authenticates again.
func (c *Clusters) Reconnect(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.conns, id)
	access.forget(id)
}

func serverVersion(ctx context.Context, cn *conn) (string, error) {
	if cn.snapshot != nil {
		return cn.snapshot.ServerVersion, nil
	}
	var info struct {
		GitVersion string `json:"gitVersion"`
	}
	raw, err := cn.cs.Discovery().RESTClient().Get().AbsPath("/version").Do(ctx).Raw()
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		return "", err
	}
	return info.GitVersion, nil
}

// conn returns the connection to a cluster, making it when its kubeconfig
// changed since the last one.
func (c *Clusters) conn(id string) (*conn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Reuse the connection while its kubeconfig files are unchanged, without
	// reading them again.
	if cn, ok := c.conns[id]; ok && cn.src.stamp() == cn.stamp {
		return cn, nil
	}
	list, bySource := c.list()
	s, ok := bySource[id]
	if !ok {
		return nil, errors.New("this cluster is no longer in its kubeconfig")
	}
	stamp := s.stamp()
	var cl Cluster
	for _, x := range list.Clusters {
		if x.ID == id {
			cl = x
		}
	}
	if s.kind == SourceSnapshot {
		d, err := loadSnapshot(s)
		if err != nil {
			return nil, err
		}
		cn, err := snapshotConn(cl, s, d)
		if err != nil {
			return nil, err
		}
		c.conns[id] = cn
		return cn, nil
	}
	raw, err := s.load()
	if err != nil {
		return nil, err
	}
	cfg, err := clientcmd.NewNonInteractiveClientConfig(*raw, cl.Context, &clientcmd.ConfigOverrides{}, nil).ClientConfig()
	if err != nil {
		return nil, err
	}
	cfg.QPS, cfg.Burst = 50, 100
	cfg.UserAgent = "k8s-client/" + rest.DefaultKubernetesUserAgent()
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	disc := memory.NewMemCacheClient(cs.Discovery())
	cn := &conn{
		cluster: cl,
		cfg:     cfg,
		cs:      cs,
		dyn:     dyn,
		disc:    disc,
		mapper:  restmapper.NewDeferredDiscoveryRESTMapper(disc),
		src:     s,
		stamp:   stamp,
	}
	c.conns[id] = cn
	return cn, nil
}

// friendlyError rewrites the errors people meet most when connecting.
func friendlyError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "executable") && strings.Contains(msg, "not found"):
		return msg + " — install the credential plugin, or make sure it is on the PATH of your login shell"
	case strings.Contains(msg, "x509"):
		return msg + " — the cluster's certificate is not trusted"
	case strings.Contains(msg, "connection refused"), strings.Contains(msg, "no such host"), strings.Contains(msg, "i/o timeout"), strings.Contains(msg, "context deadline exceeded"):
		return msg + " — the API server cannot be reached"
	}
	return msg
}
