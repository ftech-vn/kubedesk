package pluginindex

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"strings"
	"testing"
	"time"
)

func tgz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

const png = "\x89PNG\r\n\x1a\nrest"

func manifest(version, extra string) string {
	return `{"name": "kubedesk-plugin-demo", "version": "` + version + `", "description": "Demo ` + version + `", "author": "Ann <ann@example.com>",
  "keywords": ["kubedesk-plugin", "certs"], "engines": {"kubedesk": ">=0.8.0"}, "kubedesk": {"displayName": "Demo"` + extra + `}}`
}

func TestAddSignOpen(t *testing.T) {
	ix := &Index{}
	now := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	p, err := ix.Add(tgz(t, map[string]string{
		"package/package.json": manifest("1.0.0", `, "icon": "icon.png", "features": ["A page"]`),
		"package/dist/main.js": "x", "package/README.md": "# Demo", "package/icon.png": png,
	}), now)
	if err != nil {
		t.Fatal(err)
	}
	e := p.Entry
	if e.Name != "Demo" || e.Author != "Ann" || len(e.Keywords) != 1 || e.Keywords[0] != "certs" || e.Features[0] != "A page" {
		t.Fatalf("entry = %+v", e)
	}
	want := []string{"kubedesk-plugin-demo/kubedesk-plugin-demo-1.0.0.tgz", "kubedesk-plugin-demo/README.md", "kubedesk-plugin-demo/icon.png"}
	for _, f := range want {
		if _, ok := p.Files[f]; !ok {
			t.Errorf("no %s among the files to upload", f)
		}
	}

	// A newer version comes first; an older one published later does not
	// change what the marketplace shows.
	if _, err := ix.Add(tgz(t, map[string]string{"package.json": manifest("1.1.0", ""), "dist/main.js": "y"}), now); err != nil {
		t.Fatal(err)
	}
	if _, err := ix.Add(tgz(t, map[string]string{"package.json": manifest("0.9.0", ""), "dist/main.js": "z"}), now); err != nil {
		t.Fatal(err)
	}
	if e.Versions[0].Version != "1.1.0" || e.Versions[2].Version != "0.9.0" || e.Description != "Demo 1.1.0" || e.Icon != "" {
		t.Fatalf("versions = %+v, entry = %+v", e.Versions, e)
	}
	if _, err := ix.Add(tgz(t, map[string]string{"package.json": manifest("1.1.0", ""), "dist/main.js": "changed"}), now); err == nil {
		t.Fatal("publishing a version again with another archive")
	}

	pub, priv, _ := ed25519.GenerateKey(nil)
	doc, err := Sign(ix, priv)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Open(doc, pub)
	if err != nil || len(got.Plugins) != 1 || len(got.Plugins[0].Versions) != 3 {
		t.Fatalf("opened %+v, %v", got, err)
	}
	if _, err := Open(bytes.Replace(doc, []byte("Demo 1.1.0"), []byte("Demo 6.6.6"), 1), pub); err == nil {
		t.Fatal("a changed index opens")
	}
	other, _, _ := ed25519.GenerateKey(nil)
	if _, err := Open(doc, other); err == nil {
		t.Fatal("an index opens with another key")
	}
	// A signature of the index is not one of anything else made with the key.
	if ed25519.Verify(pub, []byte(got.Updated), []byte(doc)) {
		t.Fatal("unexpected")
	}

	if v, ok := got.Plugins[0].Latest("0.8.2"); !ok || v.Version != "1.1.0" {
		t.Fatalf("latest = %+v", v)
	}
	if _, ok := got.Plugins[0].Latest("0.7.1"); ok {
		t.Fatal("a version for Kube Desk 0.7.1")
	}
}

func TestAddRefuses(t *testing.T) {
	cases := map[string]map[string]string{
		"engines.kubedesk":   {"package.json": `{"name": "x", "version": "1.0.0", "kubedesk": {}}`, "dist/main.js": ""},
		"build the plugin":   {"package.json": manifest("1.0.0", "")},
		"not a version":      {"package.json": manifest("one", ""), "dist/main.js": ""},
		"the icon":           {"package.json": manifest("1.0.0", `, "icon": "icon.png"`), "dist/main.js": ""},
		"must be a PNG":      {"package.json": manifest("1.0.0", `, "icon": "icon.svg"`), "dist/main.js": "", "icon.svg": "<svg/>"},
		"outside the plugin": {"package.json": manifest("1.0.0", `, "main": "../x.js"`)},
		"no package.json":    {"dist/main.js": ""},
	}
	for want, files := range cases {
		_, err := (&Index{}).Add(tgz(t, files), time.Now())
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v", want, err)
		}
	}
}

func TestSatisfies(t *testing.T) {
	cases := []struct {
		v, r string
		ok   bool
	}{
		{"0.8.0", ">=0.8.0", true},
		{"0.7.1", ">=0.8.0", false},
		{"0.8.3", "^0.8.0", true},
		{"0.9.0", "^0.8.0", false},
		{"1.4.0", "^1.2.0", true},
		{"2.0.0", "^1.2.0", false},
		{"0.8.5", "~0.8.1", true},
		{"0.9.0", "~0.8.1", false},
		{"0.8.0", ">=0.8.0 <1.0.0", true},
		{"1.0.0", ">=0.8.0 <1.0.0", false},
		{"0.7.0", "0.7.x || >=0.9", true},
		{"0.8.0", "0.7.x || >=0.9", false},
		{"0.8.0", "0.8.0 - 0.9.0", true},
		{"0.8.0", "*", true},
		{"0.8.0", "", true},
		{"0.8.0-beta.1", ">=0.8.0", true},
		{"", ">=9.0.0", true},
	}
	for _, c := range cases {
		if got := Satisfies(c.v, c.r); got != c.ok {
			t.Errorf("Satisfies(%q, %q) = %v", c.v, c.r, got)
		}
	}
	if Compare("0.10.0", "0.9.9") <= 0 || Compare("1.0.0", "1.0") != 0 || !Valid("1.2.3") || Valid("1.2") || Valid("latest") {
		t.Error("Compare or Valid")
	}
}
