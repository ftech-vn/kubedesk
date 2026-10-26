package pluginindex

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"
)

// The registry is a folder of static files, such as a bucket of R2:
//
//	index.json                       the signed index, the only file read first
//	<dir>/<dir>-<version>.tgz        each version, as npm pack makes it
//	<dir>/README.md, <dir>/icon.png  of the latest version, for the marketplace
//
// where <dir> is DirName of the plugin's ID. Paths in the index are
// relative to the registry, so it can move.

// Schema is the version of the index's format.
const Schema = 1

// signingContext keeps the registry's signatures from being taken for
// signatures of anything else made with the same key.
const signingContext = "kubedesk plugin index v1\n"

// Index lists the plugins of a registry.
type Index struct {
	Schema  int      `json:"schema"`
	Updated string   `json:"updated"`
	Plugins []*Entry `json:"plugins"`
}

// Entry is a plugin and its versions, newest first.
type Entry struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Author      string    `json:"author,omitempty"`
	Homepage    string    `json:"homepage,omitempty"`
	License     string    `json:"license,omitempty"`
	Keywords    []string  `json:"keywords,omitempty"`
	Features    []string  `json:"features,omitempty"`
	Icon        string    `json:"icon,omitempty"`
	Readme      string    `json:"readme,omitempty"`
	Versions    []Version `json:"versions"`
}

// Version is one published version of a plugin.
type Version struct {
	Version string `json:"version"`
	// Engine is the range of Kube Desk versions it works with.
	Engine    string `json:"engine"`
	Archive   string `json:"archive"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	Published string `json:"published"`
}

// Document is index.json: the index as it was signed, and the signature,
// in one file, so that an index and its signature are never from two
// uploads.
type Document struct {
	Schema    int    `json:"schema"`
	Signed    string `json:"signed"`
	Signature string `json:"signature"`
}

// Sign makes the document of an index with the registry's private key.
func Sign(ix *Index, key ed25519.PrivateKey) ([]byte, error) {
	if len(key) != ed25519.PrivateKeySize {
		return nil, errors.New("not an Ed25519 private key")
	}
	body, err := json.MarshalIndent(ix, "", "  ")
	if err != nil {
		return nil, err
	}
	sig := ed25519.Sign(key, append([]byte(signingContext), body...))
	return json.MarshalIndent(Document{Schema: Schema, Signed: string(body), Signature: base64.StdEncoding.EncodeToString(sig)}, "", "  ")
}

// Open checks the signature of an index document with the registry's
// public key and returns the index.
func Open(doc []byte, pub ed25519.PublicKey) (*Index, error) {
	var d Document
	if err := json.Unmarshal(doc, &d); err != nil {
		return nil, fmt.Errorf("the plugin index is not JSON: %w", err)
	}
	if d.Schema != Schema {
		return nil, fmt.Errorf("the plugin index has format %d; this version of Kube Desk reads %d", d.Schema, Schema)
	}
	sig, err := base64.StdEncoding.DecodeString(d.Signature)
	if err != nil || len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, append([]byte(signingContext), d.Signed...), sig) {
		return nil, errors.New("the plugin index is not signed by the registry's key")
	}
	var ix Index
	if err := json.Unmarshal([]byte(d.Signed), &ix); err != nil {
		return nil, err
	}
	return &ix, nil
}

// Unsigned reads the index of a document without checking it, for tools
// that sign it again.
func Unsigned(doc []byte) (*Index, error) {
	var d Document
	if err := json.Unmarshal(doc, &d); err != nil {
		return nil, err
	}
	var ix Index
	if err := json.Unmarshal([]byte(d.Signed), &ix); err != nil {
		return nil, err
	}
	return &ix, nil
}

// Find returns the entry of a plugin.
func (ix *Index) Find(id string) *Entry {
	for _, e := range ix.Plugins {
		if e.ID == id {
			return e
		}
	}
	return nil
}

// Latest is the newest version that works with Kube Desk app, if any.
func (e *Entry) Latest(app string) (Version, bool) {
	for _, v := range e.Versions {
		if Satisfies(app, v.Engine) {
			return v, true
		}
	}
	return Version{}, false
}

// Get returns a version.
func (e *Entry) Get(version string) (Version, bool) {
	for _, v := range e.Versions {
		if v.Version == version {
			return v, true
		}
	}
	return Version{}, false
}

// Published is what Add made: the index's entry and the files to upload
// next to it, by their paths in the registry.
type Published struct {
	Entry   *Entry
	Version Version
	Files   map[string][]byte
}

// Add adds a plugin's archive, as npm pack makes it, to the index. It
// refuses a version already published, unless it is the same archive.
func (ix *Index) Add(archive []byte, now time.Time) (*Published, error) {
	files, err := ReadArchive(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	pj, ok := files["package.json"]
	if !ok {
		return nil, errors.New("the archive has no package.json")
	}
	m, err := ParseManifest(pj)
	if err != nil {
		return nil, err
	}
	if !Valid(m.Version) {
		return nil, fmt.Errorf("%q is not a version such as 1.2.3", m.Version)
	}
	if m.Engines["kubedesk"] == "" {
		return nil, errors.New("package.json needs engines.kubedesk, the Kube Desk versions the plugin works with, such as \">=0.8.0\"")
	}
	if _, ok := files[m.Main()]; !ok {
		return nil, fmt.Errorf("the archive has no %s: build the plugin before packing it", m.Main())
	}
	sum := sha256.Sum256(archive)
	dir := DirName(m.Name)
	v := Version{
		Version:   m.Version,
		Engine:    m.Engines["kubedesk"],
		Archive:   fmt.Sprintf("%s/%s-%s.tgz", dir, dir, m.Version),
		SHA256:    hex.EncodeToString(sum[:]),
		Size:      int64(len(archive)),
		Published: now.UTC().Format(time.RFC3339),
	}
	e := ix.Find(m.Name)
	if e == nil {
		e = &Entry{ID: m.Name}
		ix.Plugins = append(ix.Plugins, e)
	}
	if old, ok := e.Get(m.Version); ok {
		if old.SHA256 != v.SHA256 {
			return nil, fmt.Errorf("%s %s is already published, with another archive: publish a new version", m.Name, m.Version)
		}
		v = old
	} else {
		e.Versions = append(e.Versions, v)
	}
	sort.Slice(e.Versions, func(i, j int) bool { return Compare(e.Versions[i].Version, e.Versions[j].Version) > 0 })
	sort.Slice(ix.Plugins, func(i, j int) bool { return ix.Plugins[i].ID < ix.Plugins[j].ID })

	out := map[string][]byte{v.Archive: archive}
	// The marketplace shows the newest version's details.
	if e.Versions[0].Version == m.Version {
		e.Name, e.Description, e.Author, e.Homepage, e.License = m.DisplayName(), m.Description, m.AuthorName(), m.Homepage, m.License
		e.Keywords = without(m.Keywords, "kubedesk-plugin")
		e.Features = m.KubeDesk.Features
		e.Readme, e.Icon = "", ""
		for _, name := range []string{"README.md", "readme.md", "Readme.md"} {
			if b, ok := files[name]; ok {
				e.Readme = dir + "/README.md"
				out[e.Readme] = b
				break
			}
		}
		if m.KubeDesk.Icon != "" {
			b, ok := files[path.Clean(m.KubeDesk.Icon)]
			if !ok {
				return nil, fmt.Errorf("the archive has no %s, the icon", m.KubeDesk.Icon)
			}
			if !bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")) {
				return nil, errors.New("the icon must be a PNG")
			}
			e.Icon = dir + "/icon.png"
			out[e.Icon] = b
		}
	}
	ix.Schema = Schema
	ix.Updated = now.UTC().Format(time.RFC3339)
	return &Published{Entry: e, Version: v, Files: out}, nil
}

func without(list []string, drop string) []string {
	var out []string
	for _, s := range list {
		if s != drop {
			out = append(out, s)
		}
	}
	return out
}

// ReadArchive reads the files of a .tgz, by their paths in the package:
// npm's package/ folder, or another single top folder, is left out.
func ReadArchive(r io.Reader) (map[string][]byte, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, errors.New("not a .tgz archive")
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	files := map[string][]byte{}
	var total int64
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		total += h.Size
		if total > 300<<20 {
			return nil, errors.New("the archive unpacks to too much")
		}
		b, err := io.ReadAll(io.LimitReader(tr, h.Size))
		if err != nil {
			return nil, err
		}
		files[strings.TrimPrefix(path.Clean("/"+h.Name), "/")] = b
	}
	if _, ok := files["package.json"]; ok {
		return files, nil
	}
	// One top folder: strip it.
	top := ""
	for name := range files {
		first, _, _ := strings.Cut(name, "/")
		if top != "" && first != top {
			return files, nil
		}
		top = first
	}
	out := map[string][]byte{}
	for name, b := range files {
		out[strings.TrimPrefix(name, top+"/")] = b
	}
	return out, nil
}
