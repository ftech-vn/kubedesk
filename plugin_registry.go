package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"k8s-client/internal/pluginindex"
)

// The plugin registry is Kube Desk's own: static files on R2, published by
// scripts/publish-plugin.sh, whose index is signed with a key of its own.
// The app installs only what the signed index lists, and checks each
// archive against the index's SHA-256.
const (
	registryURL = "https://pub-e3b2030c705649ef87d35925ea3331eb.r2.dev/plugins"
	registryKey = "IgT9MuG4bAcd1k0S7i4xC1DA1r4PmAQd1f9tkM1+Kl8="
	// indexTTL is how long the app keeps the index it read.
	indexTTL = 5 * time.Minute
)

type registry struct {
	url string
	key ed25519.PublicKey

	index *pluginindex.Index
	at    time.Time
}

// defaultRegistry is Kube Desk's registry, or the one of
// KUBEDESK_PLUGIN_REGISTRY and KUBEDESK_PLUGIN_REGISTRY_PUBLIC_KEY, such as
// a company's own, or a local one while testing.
func defaultRegistry() registry {
	u, k := registryURL, registryKey
	if s := os.Getenv("KUBEDESK_PLUGIN_REGISTRY"); s != "" {
		u = strings.TrimRight(s, "/")
	}
	if s := os.Getenv("KUBEDESK_PLUGIN_REGISTRY_PUBLIC_KEY"); s != "" {
		k = s
	}
	key, _ := base64.StdEncoding.DecodeString(k)
	return registry{url: u, key: key}
}

// CatalogPlugin is a plugin of the registry.
type CatalogPlugin struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Author      string   `json:"author,omitempty"`
	Homepage    string   `json:"homepage,omitempty"`
	License     string   `json:"license,omitempty"`
	Keywords    []string `json:"keywords,omitempty"`
	Features    []string `json:"features,omitempty"`
	// Icon is the address of its icon, if it has one.
	Icon string `json:"icon,omitempty"`
	// Version is the newest version that works with this Kube Desk, "" when
	// none does; Latest is the newest of all, and Engine what it needs.
	Version   string `json:"version"`
	Latest    string `json:"latest"`
	Engine    string `json:"engine"`
	Size      int64  `json:"size"`
	Published string `json:"published"`
	HasReadme bool   `json:"hasReadme"`
	// Installed is the version installed, if any.
	Installed string `json:"installed,omitempty"`
}

// readIndex returns the registry's index, checked against its key, from
// the last few minutes or read again.
func (p *Plugins) readIndex(ctx context.Context, fresh bool) (*pluginindex.Index, error) {
	p.mu.Lock()
	r := p.registry
	p.mu.Unlock()
	if !fresh && r.index != nil && time.Since(r.at) < indexTTL {
		return r.index, nil
	}
	if err := checkRegistryURL(r.url); err != nil {
		return nil, err
	}
	doc, err := p.download(ctx, r.url+"/index.json")
	if err != nil {
		return nil, fmt.Errorf("could not reach the plugin registry: %w", err)
	}
	ix, err := pluginindex.Open(doc, r.key)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.registry.index, p.registry.at = ix, time.Now()
	p.mu.Unlock()
	return ix, nil
}

// checkRegistryURL allows HTTPS, and HTTP to this machine, for testing.
func checkRegistryURL(u string) error {
	pu, err := url.Parse(u)
	if err != nil {
		return err
	}
	if pu.Scheme == "https" || pu.Scheme == "http" && (pu.Hostname() == "localhost" || pu.Hostname() == "127.0.0.1") {
		return nil
	}
	return fmt.Errorf("the plugin registry must be served over HTTPS: %s", u)
}

func (p *Plugins) registryFile(rel string) (string, error) {
	if rel == "" || !pluginindex.InPackage(rel) || strings.Contains(rel, ":") {
		return "", fmt.Errorf("the index names a file outside the registry: %q", rel)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.registry.url + "/" + rel, nil
}

// Catalog lists the registry's plugins, matching the words of query in
// their names, descriptions, keywords and authors.
func (p *Plugins) Catalog(ctx context.Context, query string, refresh bool) ([]CatalogPlugin, error) {
	ix, err := p.readIndex(ctx, refresh)
	if err != nil {
		return nil, err
	}
	installed := map[string]string{}
	for _, pl := range p.List() {
		if pl.Source == "installed" {
			installed[pl.ID] = pl.Version
		}
	}
	app := mygo.App.Version()
	words := strings.Fields(strings.ToLower(query))
	out := []CatalogPlugin{}
	for _, e := range ix.Plugins {
		if len(e.Versions) == 0 {
			continue
		}
		hay := strings.ToLower(strings.Join(append([]string{e.ID, e.Name, e.Description, e.Author}, append(e.Keywords, e.Features...)...), " "))
		match := true
		for _, w := range words {
			match = match && strings.Contains(hay, w)
		}
		if !match {
			continue
		}
		c := CatalogPlugin{
			ID: e.ID, Name: e.Name, Description: e.Description, Author: e.Author, Homepage: e.Homepage, License: e.License,
			Keywords: e.Keywords, Features: e.Features, Latest: e.Versions[0].Version, Engine: e.Versions[0].Engine,
			HasReadme: e.Readme != "", Installed: installed[e.ID],
		}
		if e.Icon != "" {
			c.Icon, _ = p.registryFile(e.Icon)
		}
		shown := e.Versions[0]
		if v, ok := e.Latest(app); ok {
			c.Version, shown = v.Version, v
		}
		c.Size, c.Published = shown.Size, shown.Published
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

// Readme returns the README of a plugin in the registry, as Markdown.
func (p *Plugins) Readme(ctx context.Context, id string) (string, error) {
	ix, err := p.readIndex(ctx, false)
	if err != nil {
		return "", err
	}
	e := ix.Find(id)
	if e == nil || e.Readme == "" {
		return "", nil
	}
	u, err := p.registryFile(e.Readme)
	if err != nil {
		return "", err
	}
	b, err := p.download(ctx, u)
	if err != nil {
		return "", err
	}
	if len(b) > 512<<10 {
		b = b[:512<<10]
	}
	return string(b), nil
}

// InstallFromRegistry installs a plugin of the registry: version, or the
// newest that works with this Kube Desk when "". The archive must match the
// signed index. Like other installs, the plugin comes turned off.
func (p *Plugins) InstallFromRegistry(ctx context.Context, id, version string) (*Plugin, error) {
	ix, err := p.readIndex(ctx, true)
	if err != nil {
		return nil, err
	}
	e := ix.Find(id)
	if e == nil {
		return nil, fmt.Errorf("the plugin registry has no %s", id)
	}
	var v pluginindex.Version
	var ok bool
	if version == "" {
		if v, ok = e.Latest(mygo.App.Version()); !ok {
			return nil, fmt.Errorf("no version of %s works with Kube Desk %s: it needs %s", e.Name, mygo.App.Version(), e.Versions[0].Engine)
		}
	} else if v, ok = e.Get(version); !ok {
		return nil, fmt.Errorf("the plugin registry has no %s %s", e.Name, version)
	}
	u, err := p.registryFile(v.Archive)
	if err != nil {
		return nil, err
	}
	body, err := p.download(ctx, u)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	if int64(len(body)) != v.Size || hex.EncodeToString(sum[:]) != v.SHA256 {
		return nil, errors.New("the archive does not match the plugin registry's index")
	}
	return p.install(bytes.NewReader(body), v.Archive, "registry:"+id, id)
}

// CheckUpdates lists the plugins installed from the registry that have a
// newer version working with this Kube Desk.
func (p *Plugins) CheckUpdates(ctx context.Context) ([]PluginUpdate, error) {
	ix, err := p.readIndex(ctx, true)
	if err != nil {
		return nil, err
	}
	out := []PluginUpdate{}
	for _, pl := range p.List() {
		id, ok := strings.CutPrefix(pl.Origin, "registry:")
		if !ok || pl.Source != "installed" {
			continue
		}
		e := ix.Find(id)
		if e == nil {
			continue
		}
		if v, ok := e.Latest(mygo.App.Version()); ok && pluginindex.Compare(v.Version, pl.Version) > 0 {
			out = append(out, PluginUpdate{ID: pl.ID, Current: pl.Version, Latest: v.Version})
		}
	}
	return out, nil
}

// ---- Links ----

// openPluginsURL handles kubedesk://plugins and
// kubedesk://plugins/install?id=<id>[&version=<v>], from the marketplace
// on the website. It reports whether the URL was one.
func (p *Plugins) openPluginsURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, "kubedesk") || u.Host != "plugins" {
		return false
	}
	req := PluginsRequest{}
	if strings.Trim(u.Path, "/") == "install" {
		req.Install, req.Version = u.Query().Get("id"), u.Query().Get("version")
		if !pluginindex.ValidName(req.Install) || req.Version != "" && !pluginindex.Valid(req.Version) {
			return true
		}
	}
	p.mu.Lock()
	p.pending = &req
	p.mu.Unlock()
	p.show()
	_ = OpenPlugins.Broadcast(req)
	return true
}

// TakePendingOpen returns the request of a link that opened the app, once.
func (p *Plugins) TakePendingOpen() *PluginsRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.pending
	p.pending = nil
	return r
}
