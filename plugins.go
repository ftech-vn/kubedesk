package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"

	"k8s-client/internal/pluginindex"
)

// Plugin is a plugin the app knows of: a folder with a package.json and the
// JavaScript bundle it names.
type Plugin struct {
	// ID is the plugin's package name, such as "kubedesk-plugin-cert-manager".
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
	Author      string `json:"author,omitempty"`
	Homepage    string `json:"homepage,omitempty"`
	// Source is "installed" for plugins in the app's plugins folder and
	// "dev" for folders the user loads while writing a plugin.
	Source string `json:"source"`
	Path   string `json:"path"`
	// Origin is where an installed plugin came from: "registry:<id>", a URL,
	// or a file.
	Origin      string `json:"origin,omitempty"`
	InstalledAt string `json:"installedAt,omitempty"`
	// Engine is the range of Kube Desk versions the plugin works with
	// (engines.kubedesk in its package.json).
	Engine  string `json:"engine,omitempty"`
	Enabled bool   `json:"enabled"`
	// Error says why the plugin cannot load; the app does not run it.
	Error string `json:"error,omitempty"`
}

// PluginPrefs are what the app keeps about a plugin.
type PluginPrefs struct {
	Enabled  bool           `json:"enabled,omitempty"`
	Settings map[string]any `json:"settings,omitempty"`
}

// PluginUpdate is a newer version of an installed plugin, in the registry.
type PluginUpdate struct {
	ID      string `json:"id"`
	Current string `json:"current"`
	Latest  string `json:"latest"`
}

// PluginsChanged is sent when plugins were added, removed, turned on or off,
// or when the files of a plugin in development changed; the payload is the
// plugin's ID, or "" when the list changed.
var PluginsChanged = mygo.NewEvent[string]("plugins:changed")

// PluginsRequest asks the page to show the plugins, or to install one from
// the registry, from the app's menu or a kubedesk://plugins link.
type PluginsRequest struct {
	Install string `json:"install,omitempty"`
	Version string `json:"version,omitempty"`
}

// OpenPlugins asks the page to show the plugins.
var OpenPlugins = mygo.NewEvent[PluginsRequest]("plugins:open")

// Plugins installs and loads the plugins that extend the app's pages.
type Plugins struct {
	clusters *Clusters
	client   *http.Client
	registry registry

	mu       sync.Mutex
	watchers map[string]context.CancelFunc
	// pending is a request that came before the page could take it.
	pending *PluginsRequest
	// show shows the main window; tests replace it.
	show func()
}

const (
	pluginOriginFile = ".kubedesk-plugin.json"
	maxPluginBundle  = 32 << 20
	maxPluginArchive = 100 << 20
)

func newPlugins(clusters *Clusters) *Plugins {
	return &Plugins{clusters: clusters, registry: defaultRegistry(), client: &http.Client{Timeout: 60 * time.Second}, show: showMainWindow, watchers: map[string]context.CancelFunc{}}
}

// init watches the folders of plugins in development, to reload them as
// they are rebuilt.
func (p *Plugins) init() {
	for _, d := range p.devDirs() {
		p.watch(d)
	}
}

func (p *Plugins) dir() string { return filepath.Join(p.clusters.dir, "plugins") }

func (p *Plugins) devDirs() []string {
	p.clusters.mu.Lock()
	defer p.clusters.mu.Unlock()
	return append([]string(nil), p.clusters.store.PluginDevDirs...)
}

func (p *Plugins) prefs(id string) PluginPrefs {
	p.clusters.mu.Lock()
	defer p.clusters.mu.Unlock()
	return p.clusters.store.Plugins[id]
}

func (p *Plugins) updatePrefs(id string, f func(*PluginPrefs)) error {
	p.clusters.mu.Lock()
	defer p.clusters.mu.Unlock()
	if p.clusters.store.Plugins == nil {
		p.clusters.store.Plugins = map[string]PluginPrefs{}
	}
	pr := p.clusters.store.Plugins[id]
	f(&pr)
	p.clusters.store.Plugins[id] = pr
	return p.clusters.save()
}

// List returns the plugins: those in development first, which win over an
// installed plugin with the same ID.
func (p *Plugins) List() []Plugin {
	var out []Plugin
	seen := map[string]bool{}
	for _, d := range p.devDirs() {
		pl := p.read(d, "dev")
		if seen[pl.ID] {
			pl.Error = "Another folder has a plugin with this name"
		}
		seen[pl.ID] = true
		out = append(out, pl)
	}
	entries, _ := os.ReadDir(p.dir())
	var installed []Plugin
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		pl := p.read(filepath.Join(p.dir(), e.Name()), "installed")
		if seen[pl.ID] {
			pl.Error = "A plugin in development with this name replaces it"
		}
		installed = append(installed, pl)
	}
	sort.Slice(installed, func(i, j int) bool { return strings.ToLower(installed[i].Name) < strings.ToLower(installed[j].Name) })
	return append(out, installed...)
}

// read reads the plugin of a folder.
func (p *Plugins) read(dir, source string) Plugin {
	pl := Plugin{ID: filepath.Base(dir), Name: filepath.Base(dir), Source: source, Path: dir}
	m, err := readPluginManifest(dir)
	if err != nil {
		pl.Error = err.Error()
	} else {
		pl.ID, pl.Name, pl.Version, pl.Description, pl.Author, pl.Homepage, pl.Engine = m.Name, m.DisplayName(), m.Version, m.Description, m.AuthorName(), m.Homepage, m.Engines["kubedesk"]
		if _, err := os.Stat(filepath.Join(dir, m.Main())); err != nil {
			pl.Error = fmt.Sprintf("%s is missing: build the plugin first", m.Main())
		} else if pl.Engine != "" && !pluginindex.Satisfies(mygo.App.Version(), pl.Engine) {
			pl.Error = fmt.Sprintf("Made for Kube Desk %s; this is %s", pl.Engine, mygo.App.Version())
		}
	}
	if b, err := os.ReadFile(filepath.Join(dir, pluginOriginFile)); err == nil {
		var o struct {
			Origin      string `json:"origin"`
			InstalledAt string `json:"installedAt"`
		}
		if json.Unmarshal(b, &o) == nil {
			pl.Origin, pl.InstalledAt = o.Origin, o.InstalledAt
		}
	}
	pl.Enabled = p.prefs(pl.ID).Enabled
	return pl
}

func readPluginManifest(dir string) (pluginindex.Manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return pluginindex.Manifest{}, errors.New("no package.json in the folder")
	}
	return pluginindex.ParseManifest(b)
}

func (p *Plugins) find(id string) (Plugin, error) {
	// Plugins in development come first, and win.
	for _, pl := range p.List() {
		if pl.ID == id {
			return pl, nil
		}
	}
	return Plugin{}, fmt.Errorf("no plugin %s", id)
}

// Source returns the JavaScript of an enabled plugin, for the page to run.
func (p *Plugins) Source(id string) (string, error) {
	pl, err := p.find(id)
	if err != nil {
		return "", err
	}
	if !pl.Enabled {
		return "", fmt.Errorf("%s is turned off", pl.Name)
	}
	if pl.Error != "" {
		return "", errors.New(pl.Error)
	}
	m, err := readPluginManifest(pl.Path)
	if err != nil {
		return "", err
	}
	f, err := os.Open(filepath.Join(pl.Path, m.Main()))
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxPluginBundle+1))
	if err != nil {
		return "", err
	}
	if len(b) > maxPluginBundle {
		return "", fmt.Errorf("%s is larger than %d MB", m.Main(), maxPluginBundle>>20)
	}
	return string(b), nil
}

// SetEnabled turns a plugin on or off; the page loads or unloads it.
func (p *Plugins) SetEnabled(id string, on bool) error {
	if _, err := p.find(id); err != nil {
		return err
	}
	if err := p.updatePrefs(id, func(pr *PluginPrefs) { pr.Enabled = on }); err != nil {
		return err
	}
	_ = PluginsChanged.Broadcast("")
	return nil
}

// Settings returns what a plugin saved with KubeDesk.settings.
func (p *Plugins) Settings(id string) map[string]any {
	s := p.prefs(id).Settings
	if s == nil {
		return map[string]any{}
	}
	return s
}

// SaveSettings keeps a plugin's settings, which must be JSON of at most
// 256 KB.
func (p *Plugins) SaveSettings(id string, settings map[string]any) error {
	b, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	if len(b) > 256<<10 {
		return errors.New("the settings are larger than 256 KB")
	}
	return p.updatePrefs(id, func(pr *PluginPrefs) { pr.Settings = settings })
}

// ---- Installing ----

// InstallFile asks for a plugin archive (.tgz, .tar.gz or .zip, as
// npm pack makes) and installs it. It returns the plugin, turned off: the
// page asks the user to trust it before turning it on. Nil when the user
// canceled.
func (p *Plugins) InstallFile(ctx context.Context) (*Plugin, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent: mygo.CallerWindow(ctx), Title: "Install a plugin", ButtonLabel: "Install",
		Message: "Choose a plugin's archive, as npm pack makes it.",
		Filters: []mygo.FileFilter{{Name: "Plugin archives", Extensions: []string{"tgz", "gz", "zip"}}},
	})
	if err != nil || len(paths) == 0 {
		return nil, err
	}
	f, err := os.Open(paths[0])
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return p.install(f, paths[0], "file:"+paths[0], "")
}

// InstallURL downloads a plugin archive over HTTPS and installs it. With a
// sha256 checksum, the archive must match it.
func (p *Plugins) InstallURL(ctx context.Context, u, sha string) (*Plugin, error) {
	pu, err := url.Parse(u)
	if err != nil || pu.Scheme != "https" || pu.Host == "" {
		return nil, errors.New("the address must start with https://")
	}
	body, err := p.download(ctx, u)
	if err != nil {
		return nil, err
	}
	if sha != "" {
		sum := sha256.Sum256(body)
		if !strings.EqualFold(hex.EncodeToString(sum[:]), strings.TrimPrefix(strings.TrimSpace(sha), "sha256:")) {
			return nil, errors.New("the archive does not match the checksum")
		}
	}
	return p.install(bytes.NewReader(body), pu.Path, u, "")
}

func (p *Plugins) download(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "KubeDesk/"+mygo.App.Version())
	res, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("not found: %s", u)
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %s", res.Request.URL.Host, res.Status)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, maxPluginArchive+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxPluginArchive {
		return nil, fmt.Errorf("larger than %d MB", maxPluginArchive>>20)
	}
	return b, nil
}

// install unpacks an archive into the plugins folder, replacing an earlier
// version of the plugin, which keeps its settings and whether it is on.
func (p *Plugins) install(r io.Reader, name, origin, expectID string) (*Plugin, error) {
	if err := os.MkdirAll(p.dir(), 0o755); err != nil {
		return nil, err
	}
	staging, err := os.MkdirTemp(p.dir(), ".staging-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)
	if strings.HasSuffix(strings.ToLower(name), ".zip") {
		err = unzipPlugin(r, staging)
	} else {
		err = untarPlugin(r, staging)
	}
	if err != nil {
		return nil, err
	}
	root, err := pluginRoot(staging)
	if err != nil {
		return nil, err
	}
	m, err := readPluginManifest(root)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(root, m.Main())); err != nil {
		return nil, fmt.Errorf("the archive has no %s: it holds the plugin's source, not its build", m.Main())
	}
	if !m.IsPlugin() {
		return nil, errors.New("this package is not a Kube Desk plugin: its package.json has neither engines.kubedesk nor kubedesk")
	}
	if expectID != "" && m.Name != expectID {
		return nil, fmt.Errorf("the archive holds %s, not %s", m.Name, expectID)
	}
	if e := m.Engines["kubedesk"]; e != "" && !pluginindex.Satisfies(mygo.App.Version(), e) {
		return nil, fmt.Errorf("%s is made for Kube Desk %s; this is %s", m.DisplayName(), e, mygo.App.Version())
	}
	meta, _ := json.Marshal(map[string]string{"origin": origin, "installedAt": time.Now().UTC().Format(time.RFC3339)})
	if err := os.WriteFile(filepath.Join(root, pluginOriginFile), meta, 0o644); err != nil {
		return nil, err
	}
	dest := filepath.Join(p.dir(), pluginindex.DirName(m.Name))
	old := dest + ".old"
	_ = os.RemoveAll(old)
	if _, err := os.Stat(dest); err == nil {
		if err := os.Rename(dest, old); err != nil {
			return nil, err
		}
	}
	if err := os.Rename(root, dest); err != nil {
		_ = os.Rename(old, dest)
		return nil, err
	}
	_ = os.RemoveAll(old)
	pl := p.read(dest, "installed")
	_ = PluginsChanged.Broadcast(pl.ID)
	return &pl, nil
}

// pluginRoot is the folder of an unpacked archive that holds package.json:
// the archive's top, or the one folder in it, such as npm's package/.
func pluginRoot(dir string) (string, error) {
	if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
		return dir, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	if len(entries) == 1 && entries[0].IsDir() {
		sub := filepath.Join(dir, entries[0].Name())
		if _, err := os.Stat(filepath.Join(sub, "package.json")); err == nil {
			return sub, nil
		}
	}
	return "", errors.New("the archive has no package.json")
}

// safeJoin joins an archive's entry name to dir, refusing names that leave
// it.
func safeJoin(dir, name string) (string, error) {
	slashed := strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(slashed, "/") || slices.Contains(strings.Split(slashed, "/"), "..") {
		return "", fmt.Errorf("the archive has a file outside its folder: %s", name)
	}
	clean := path.Clean("/" + slashed)
	if clean == "/" {
		return "", nil
	}
	p := filepath.Join(dir, filepath.FromSlash(clean[1:]))
	if !strings.HasPrefix(p, dir+string(filepath.Separator)) {
		return "", fmt.Errorf("the archive has a file outside its folder: %s", name)
	}
	return p, nil
}

func untarPlugin(r io.Reader, dir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return errors.New("not a .tgz archive")
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var total int64
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		dest, err := safeJoin(dir, h.Name)
		if err != nil {
			return err
		}
		if dest == "" {
			continue
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			total += h.Size
			if total > maxPluginArchive*3 {
				return errors.New("the archive unpacks to too much")
			}
			if err := writeFile(dest, tr, h.Size); err != nil {
				return err
			}
		}
		// Links and devices are left out.
	}
}

func unzipPlugin(r io.Reader, dir string) error {
	b, err := io.ReadAll(io.LimitReader(r, maxPluginArchive+1))
	if err != nil {
		return err
	}
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return errors.New("not a .zip archive")
	}
	var total uint64
	for _, f := range zr.File {
		dest, err := safeJoin(dir, f.Name)
		if err != nil {
			return err
		}
		if dest == "" {
			continue
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return err
			}
			continue
		}
		if !f.Mode().IsRegular() {
			continue
		}
		total += f.UncompressedSize64
		if total > maxPluginArchive*3 {
			return errors.New("the archive unpacks to too much")
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		err = writeFile(dest, rc, int64(f.UncompressedSize64))
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func writeFile(dest string, r io.Reader, size int64) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, io.LimitReader(r, size))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// Uninstall removes an installed plugin and what the app kept for it, or
// stops loading a folder in development.
func (p *Plugins) Uninstall(id string) error {
	pl, err := p.find(id)
	if err != nil {
		return err
	}
	if pl.Source == "dev" {
		return p.RemoveDevFolder(pl.Path)
	}
	if filepath.Dir(pl.Path) != p.dir() {
		return errors.New("not in the plugins folder")
	}
	if err := os.RemoveAll(pl.Path); err != nil {
		return err
	}
	p.clusters.mu.Lock()
	delete(p.clusters.store.Plugins, id)
	err = p.clusters.save()
	p.clusters.mu.Unlock()
	_ = PluginsChanged.Broadcast("")
	return err
}

// RevealFolder shows the plugins folder in the file manager.
func (p *Plugins) RevealFolder() error {
	if err := os.MkdirAll(p.dir(), 0o755); err != nil {
		return err
	}
	return mygo.Shell.OpenPath(p.dir())
}

// ---- Development ----

// AddDevFolder asks for the folder of a plugin being written and loads it,
// turned on, reloading it whenever its build changes. Nil when the user
// canceled.
func (p *Plugins) AddDevFolder(ctx context.Context) (*Plugin, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent: mygo.CallerWindow(ctx), Title: "Load a plugin in development", ButtonLabel: "Load",
		Message:   "Choose the folder of the plugin's package.json. Kube Desk reloads the plugin each time its build changes.",
		Directory: true,
	})
	if err != nil || len(paths) == 0 {
		return nil, err
	}
	return p.LoadDevFolder(paths[0])
}

// LoadDevFolder loads the plugin in a folder, as AddDevFolder does.
func (p *Plugins) LoadDevFolder(dir string) (*Plugin, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	m, err := readPluginManifest(dir)
	if err != nil {
		return nil, err
	}
	p.clusters.mu.Lock()
	found := false
	for _, d := range p.clusters.store.PluginDevDirs {
		found = found || d == dir
	}
	if !found {
		p.clusters.store.PluginDevDirs = append(p.clusters.store.PluginDevDirs, dir)
	}
	err = p.clusters.save()
	p.clusters.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if err := p.updatePrefs(m.Name, func(pr *PluginPrefs) { pr.Enabled = true }); err != nil {
		return nil, err
	}
	p.watch(dir)
	pl := p.read(dir, "dev")
	_ = PluginsChanged.Broadcast("")
	return &pl, nil
}

// RemoveDevFolder stops loading a plugin in development.
func (p *Plugins) RemoveDevFolder(dir string) error {
	p.clusters.mu.Lock()
	dirs := p.clusters.store.PluginDevDirs[:0]
	for _, d := range p.clusters.store.PluginDevDirs {
		if d != dir {
			dirs = append(dirs, d)
		}
	}
	p.clusters.store.PluginDevDirs = dirs
	err := p.clusters.save()
	p.clusters.mu.Unlock()
	p.mu.Lock()
	if stop := p.watchers[dir]; stop != nil {
		stop()
		delete(p.watchers, dir)
	}
	p.mu.Unlock()
	_ = PluginsChanged.Broadcast("")
	return err
}

// watch reloads a plugin in development when its package.json or the
// files of its build change.
func (p *Plugins) watch(dir string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.watchers[dir] != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	w, err := mygo.WatchFiles(ctx, dir, mygo.FileWatchOptions{
		Recursive: true,
		Debounce:  150 * time.Millisecond,
		Ignore: func(rel string, isDir bool) bool {
			base := path.Base(rel)
			return isDir && (base == "node_modules" || base == ".git" || base == "src")
		},
	})
	if err != nil {
		cancel()
		return
	}
	p.watchers[dir] = cancel
	go func() {
		defer w.Close()
		for {
			if _, err := w.Next(ctx); err != nil {
				return
			}
			m, err := readPluginManifest(dir)
			if err != nil {
				continue
			}
			_ = PluginsChanged.Broadcast(m.Name)
		}
	}()
}
