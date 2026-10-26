package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"k8s-client/internal/pluginindex"
)

func testPlugins(t *testing.T) *Plugins {
	t.Helper()
	c := newClusters()
	c.dir = t.TempDir()
	p := newPlugins(c)
	p.show = func() {}
	return p
}

const testManifest = `{"name": "kubedesk-plugin-demo", "version": "1.2.0", "description": "A demo", "author": {"name": "Ann"},
  "engines": {"kubedesk": ">=0.8.0"}, "kubedesk": {"displayName": "Demo"}}`

// tgz makes a .tgz of files, by their names in the archive.
func tgz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestPluginInstallFromNpmArchive(t *testing.T) {
	p := testPlugins(t)
	// npm pack puts the files in package/.
	pl, err := p.install(bytes.NewReader(tgz(t, map[string]string{
		"package/package.json": testManifest,
		"package/dist/main.js": "exports.activate = () => {}",
		"package/README.md":    "# Demo",
	})), "demo-1.2.0.tgz", "file:demo-1.2.0.tgz", "")
	if err != nil {
		t.Fatal(err)
	}
	if pl.ID != "kubedesk-plugin-demo" || pl.Name != "Demo" || pl.Version != "1.2.0" || pl.Author != "Ann" || pl.Source != "installed" || pl.Error != "" {
		t.Fatalf("plugin = %+v", pl)
	}
	if pl.Enabled {
		t.Fatal("a plugin just installed runs only once the user turns it on")
	}
	if _, err := p.Source(pl.ID); err == nil || !strings.Contains(err.Error(), "turned off") {
		t.Fatalf("source of a plugin that is off: %v", err)
	}
	if err := p.SetEnabled(pl.ID, true); err != nil {
		t.Fatal(err)
	}
	src, err := p.Source(pl.ID)
	if err != nil || src != "exports.activate = () => {}" {
		t.Fatalf("source = %q, %v", src, err)
	}

	// A new version replaces it, and keeps it on, with its settings.
	if err := p.SaveSettings(pl.ID, map[string]any{"days": 3.0}); err != nil {
		t.Fatal(err)
	}
	pl, err = p.install(bytes.NewReader(tgz(t, map[string]string{
		"package/package.json": strings.Replace(testManifest, "1.2.0", "1.3.0", 1),
		"package/dist/main.js": "exports.activate = () => 2",
	})), "demo-1.3.0.tgz", "file:demo-1.3.0.tgz", "")
	if err != nil {
		t.Fatal(err)
	}
	if pl.Version != "1.3.0" || !pl.Enabled || p.Settings(pl.ID)["days"] != 3.0 {
		t.Fatalf("after the update: %+v, settings %v", pl, p.Settings(pl.ID))
	}
	if list := p.List(); len(list) != 1 || list[0].Origin != "file:demo-1.3.0.tgz" {
		t.Fatalf("list = %+v", list)
	}
	if _, err := os.Stat(filepath.Join(p.dir(), "kubedesk-plugin-demo", "README.md")); !os.IsNotExist(err) {
		t.Fatal("the files of the old version stay after the update")
	}

	if err := p.Uninstall(pl.ID); err != nil {
		t.Fatal(err)
	}
	if len(p.List()) != 0 || len(p.Settings(pl.ID)) != 0 {
		t.Fatal("uninstalling leaves the plugin or its settings")
	}
}

func TestPluginInstallRefusesBadArchives(t *testing.T) {
	p := testPlugins(t)
	cases := map[string]map[string]string{
		"outside its folder": {"package.json": testManifest, "dist/main.js": "", "../evil.js": "x"},
		"no package.json":    {"dist/main.js": ""},
		"its build":          {"package/package.json": testManifest, "package/src/index.tsx": ""},
		"not a Kube Desk":    {"package.json": `{"name": "left-pad", "version": "1.0.0"}`, "dist/main.js": ""},
		"not a package name": {"package.json": `{"name": "../x", "version": "1.0.0", "kubedesk": {}}`, "dist/main.js": ""},
		"is outside":         {"package.json": `{"name": "x", "version": "1.0.0", "kubedesk": {"main": "../../etc/passwd"}}`},
	}
	for want, files := range cases {
		_, err := p.install(bytes.NewReader(tgz(t, files)), "x.tgz", "file:x.tgz", "")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v", want, err)
		}
	}
	if _, err := os.Stat(filepath.Join(p.dir(), "..", "evil.js")); !os.IsNotExist(err) {
		t.Fatal("a file was written outside the plugins folder")
	}
	if entries, _ := os.ReadDir(p.dir()); len(entries) != 0 {
		t.Fatalf("failed installs left %d entries", len(entries))
	}
}

func TestPluginInstallZip(t *testing.T) {
	p := testPlugins(t)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{"demo/package.json": testManifest, "demo/dist/main.js": "x"} {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	zw.Close()
	pl, err := p.install(&buf, "demo.zip", "file:demo.zip", "")
	if err != nil || pl.ID != "kubedesk-plugin-demo" {
		t.Fatalf("%+v, %v", pl, err)
	}
}

func TestPluginInstallURLChecksSHA256(t *testing.T) {
	archive := tgz(t, map[string]string{"package.json": testManifest, "dist/main.js": "x"})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(archive) }))
	defer srv.Close()
	p := testPlugins(t)
	p.client = srv.Client()
	ctx := context.Background()
	if _, err := p.InstallURL(ctx, "http://example.com/x.tgz", ""); err == nil {
		t.Fatal("plain HTTP is refused")
	}
	if _, err := p.InstallURL(ctx, srv.URL+"/x.tgz", strings.Repeat("0", 64)); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("wrong checksum: %v", err)
	}
	sum := sha256.Sum256(archive)
	if _, err := p.InstallURL(ctx, srv.URL+"/x.tgz", hex.EncodeToString(sum[:])); err != nil {
		t.Fatal(err)
	}
}

func TestPluginDevFolderWins(t *testing.T) {
	p := testPlugins(t)
	if _, err := p.install(bytes.NewReader(tgz(t, map[string]string{"package.json": testManifest, "dist/main.js": "installed"})), "x.tgz", "file:x.tgz", ""); err != nil {
		t.Fatal(err)
	}
	dev := t.TempDir()
	os.WriteFile(filepath.Join(dev, "package.json"), []byte(testManifest), 0o644)
	// Without its build, it loads, with an error saying so.
	if _, err := p.LoadDevFolder(dev); err != nil {
		t.Fatal(err)
	}
	list := p.List()
	if len(list) != 2 || list[0].Source != "dev" || !strings.Contains(list[0].Error, "build the plugin") || !strings.Contains(list[1].Error, "in development") {
		t.Fatalf("list = %+v", list)
	}
	os.MkdirAll(filepath.Join(dev, "dist"), 0o755)
	os.WriteFile(filepath.Join(dev, "dist", "main.js"), []byte("dev"), 0o644)
	src, err := p.Source("kubedesk-plugin-demo")
	if err != nil || src != "dev" {
		t.Fatalf("source = %q, %v; the folder in development wins", src, err)
	}
	if err := p.Uninstall("kubedesk-plugin-demo"); err != nil {
		t.Fatal(err)
	}
	if list := p.List(); len(list) != 1 || list[0].Source != "installed" || list[0].Error != "" {
		t.Fatalf("after removing the folder: %+v", list)
	}
	if _, err := os.Stat(dev); err != nil {
		t.Fatal("removing a folder in development deletes it")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.watchers) != 0 {
		t.Fatal("the folder is still watched")
	}
}

// testRegistry serves a registry signed with a key of its own, with the
// archives given, and points p at it.
func testRegistry(t *testing.T, p *Plugins, archives ...[]byte) (*pluginindex.Index, map[string][]byte, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(nil)
	ix := &pluginindex.Index{}
	files := map[string][]byte{}
	for _, a := range archives {
		pb, err := ix.Add(a, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range pb.Files {
			files[k] = v
		}
	}
	doc, err := pluginindex.Sign(ix, priv)
	if err != nil {
		t.Fatal(err)
	}
	files["index.json"] = doc
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := files[strings.TrimPrefix(r.URL.Path, "/plugins/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	}))
	t.Cleanup(srv.Close)
	p.registry = registry{url: strings.Replace(srv.URL, "127.0.0.1", "localhost", 1) + "/plugins", key: pub}
	return ix, files, priv
}

func TestPluginRegistryInstall(t *testing.T) {
	p := testPlugins(t)
	v1 := tgz(t, map[string]string{"package/package.json": testManifest, "package/dist/main.js": "v1", "package/README.md": "# Demo\nIt shows things."})
	v2 := tgz(t, map[string]string{"package/package.json": strings.NewReplacer("1.2.0", "2.0.0", ">=0.8.0", ">=99.0.0").Replace(testManifest), "package/dist/main.js": "v2", "package/README.md": "# Demo 2\nIt shows things."})
	_, files, priv := testRegistry(t, p, v1, v2)
	ctx := context.Background()

	cat, err := p.Catalog(ctx, "demo", false)
	if err != nil {
		t.Fatal(err)
	}
	// 2.0.0 needs a Kube Desk that does not exist: 1.2.0 is the one to install.
	if len(cat) != 1 || cat[0].Latest != "2.0.0" || cat[0].Version != "1.2.0" && cat[0].Version != "2.0.0" || cat[0].Name != "Demo" {
		t.Fatalf("catalog = %+v", cat)
	}
	if cat, _ := p.Catalog(ctx, "nothing like it", false); len(cat) != 0 {
		t.Fatalf("a search that matches nothing found %+v", cat)
	}
	if md, err := p.Readme(ctx, "kubedesk-plugin-demo"); err != nil || !strings.Contains(md, "It shows things") {
		t.Fatalf("readme = %q, %v", md, err)
	}

	pl, err := p.InstallFromRegistry(ctx, "kubedesk-plugin-demo", "1.2.0")
	if err != nil {
		t.Fatal(err)
	}
	if pl.Origin != "registry:kubedesk-plugin-demo" || pl.Enabled {
		t.Fatalf("installed = %+v", pl)
	}
	if _, err := p.InstallFromRegistry(ctx, "kubedesk-plugin-other", ""); err == nil {
		t.Fatal("a plugin the registry does not list")
	}

	// An archive changed after it was signed is refused.
	ix, _ := pluginindex.Unsigned(files["index.json"])
	files[ix.Plugins[0].Versions[1].Archive] = tgz(t, map[string]string{"package/package.json": testManifest, "package/dist/main.js": "evil"})
	if _, err := p.InstallFromRegistry(ctx, "kubedesk-plugin-demo", "1.2.0"); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("a changed archive: %v", err)
	}

	// So is an index changed after it was signed, or signed by another key.
	ix.Plugins[0].Versions[1].SHA256 = strings.Repeat("0", 64)
	body, _ := json.Marshal(ix)
	var d pluginindex.Document
	json.Unmarshal(files["index.json"], &d)
	d.Signed = string(body)
	files["index.json"], _ = json.Marshal(d)
	if _, err := p.Catalog(ctx, "", true); err == nil || !strings.Contains(err.Error(), "not signed") {
		t.Fatalf("a changed index: %v", err)
	}
	_, other, _ := ed25519.GenerateKey(nil)
	files["index.json"], _ = pluginindex.Sign(ix, other)
	if _, err := p.Catalog(ctx, "", true); err == nil {
		t.Fatal("an index signed by another key")
	}
	_ = priv
}

func TestPluginRegistryRefusesWrongPackage(t *testing.T) {
	p := testPlugins(t)
	_, files, priv := testRegistry(t, p, tgz(t, map[string]string{"package.json": testManifest, "dist/main.js": "x"}))
	// The index says kubedesk-plugin-demo, the archive holds another package.
	ix, _ := pluginindex.Unsigned(files["index.json"])
	other := tgz(t, map[string]string{"package.json": strings.Replace(testManifest, "kubedesk-plugin-demo", "kubedesk-plugin-other", 1), "dist/main.js": "x"})
	sum := sha256.Sum256(other)
	v := &ix.Plugins[0].Versions[0]
	v.SHA256, v.Size = hex.EncodeToString(sum[:]), int64(len(other))
	files[v.Archive] = other
	files["index.json"], _ = pluginindex.Sign(ix, priv)
	if _, err := p.InstallFromRegistry(context.Background(), "kubedesk-plugin-demo", ""); err == nil || !strings.Contains(err.Error(), "not kubedesk-plugin-demo") {
		t.Fatalf("err = %v", err)
	}
}

func TestPluginUpdatesFromRegistry(t *testing.T) {
	p := testPlugins(t)
	v120 := tgz(t, map[string]string{"package.json": testManifest, "dist/main.js": "1"})
	v130 := tgz(t, map[string]string{"package.json": strings.Replace(testManifest, "1.2.0", "1.3.0", 1), "dist/main.js": "2"})
	testRegistry(t, p, v120, v130)
	ctx := context.Background()
	if _, err := p.InstallFromRegistry(ctx, "kubedesk-plugin-demo", "1.2.0"); err != nil {
		t.Fatal(err)
	}
	u, err := p.CheckUpdates(ctx)
	if err != nil || len(u) != 1 || u[0].Latest != "1.3.0" || u[0].Current != "1.2.0" {
		t.Fatalf("updates = %+v, %v", u, err)
	}
	if _, err := p.InstallFromRegistry(ctx, "kubedesk-plugin-demo", ""); err != nil {
		t.Fatal(err)
	}
	if u, _ := p.CheckUpdates(ctx); len(u) != 0 {
		t.Fatalf("after updating: %+v", u)
	}
}

func TestPluginLinks(t *testing.T) {
	p := testPlugins(t)
	if p.openPluginsURL("https://kubedesk.cloud/plugins") || p.openPluginsURL("kubedesk:restart") {
		t.Fatal("not a plugins link")
	}
	cases := map[string]*PluginsRequest{
		"kubedesk://plugins": {},
		"kubedesk://plugins/install?id=kubedesk-plugin-demo":             {Install: "kubedesk-plugin-demo"},
		"kubedesk://plugins/install?id=@acme/ops&version=1.0.0":          {Install: "@acme/ops", Version: "1.0.0"},
		"kubedesk://plugins/install?id=../../etc&version=1":              nil,
		"kubedesk://plugins/install?id=kubedesk-plugin-x&version=latest": nil,
	}
	for u, want := range cases {
		p.pending = nil
		if !p.openPluginsURL(u) {
			t.Errorf("%s: not taken for a plugins link", u)
		}
		got := p.TakePendingOpen()
		if (got == nil) != (want == nil) || got != nil && *got != *want {
			t.Errorf("%s: request = %+v, want %+v", u, got, want)
		}
		if p.TakePendingOpen() != nil {
			t.Errorf("%s: the request is taken twice", u)
		}
	}
}
