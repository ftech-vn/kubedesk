package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"sigs.k8s.io/yaml"
)

// RunbookTarget is what a runbook is for. Each field matches with
// wildcards (*); an empty one matches anything. Cluster matches the
// cluster's name or its context, which other machines share.
type RunbookTarget struct {
	Cluster   string `json:"cluster,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Name      string `json:"name,omitempty"`
}

// Runbook is a Markdown note of what to do, for some objects.
type Runbook struct {
	// ID is the file's path in the runbooks folder.
	ID      string          `json:"id"`
	Title   string          `json:"title"`
	Targets []RunbookTarget `json:"targets"`
	Updated string          `json:"updated"`
	// Body is the Markdown, without the front matter.
	Body string `json:"body"`
}

// RunbookFolder is where the runbooks are.
type RunbookFolder struct {
	Path string `json:"path"`
	// Default says the folder is the app's own, not one the user chose.
	Default bool `json:"default"`
}

// Runbooks keeps runbooks as Markdown files in a folder, such as one in a
// git repository the team shares.
type Runbooks struct {
	clusters *Clusters
}

func (r *Runbooks) dir() string {
	r.clusters.mu.Lock()
	defer r.clusters.mu.Unlock()
	if r.clusters.store.RunbookDir != "" {
		return r.clusters.store.RunbookDir
	}
	return filepath.Join(r.clusters.dir, "runbooks")
}

// Folder returns where the runbooks are.
func (r *Runbooks) Folder() RunbookFolder {
	d := r.dir()
	return RunbookFolder{Path: d, Default: d == filepath.Join(r.clusters.dir, "runbooks")}
}

// ChooseFolder asks for the folder of the runbooks; "" when the user
// canceled.
func (r *Runbooks) ChooseFolder(ctx context.Context) (RunbookFolder, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent: mygo.CallerWindow(ctx), Title: "Choose the folder of your runbooks", ButtonLabel: "Use this folder",
		Message:   "Kube Desk reads and writes runbooks there as Markdown files. A folder in a git repository lets your team share them.",
		Directory: true, CreateDirectories: true, DefaultPath: r.dir(),
	})
	if err != nil || len(paths) == 0 {
		return r.Folder(), err
	}
	return r.SetFolder(paths[0])
}

// SetFolder sets the folder of the runbooks; "" goes back to the app's own.
func (r *Runbooks) SetFolder(dir string) (RunbookFolder, error) {
	r.clusters.mu.Lock()
	r.clusters.store.RunbookDir = dir
	err := r.clusters.save()
	r.clusters.mu.Unlock()
	return r.Folder(), err
}

// Reveal shows the folder of the runbooks in the file manager.
func (r *Runbooks) Reveal() error {
	d := r.dir()
	if err := os.MkdirAll(d, 0o755); err != nil {
		return err
	}
	return mygo.Shell.OpenPath(d)
}

var frontMatter = regexp.MustCompile(`(?s)\A---\r?\n(.*?)\r?\n---\r?\n?`)

type runbookMeta struct {
	Title   string          `json:"title"`
	Targets []RunbookTarget `json:"targets,omitempty"`
}

// parseRunbook reads a runbook file: YAML front matter, then Markdown.
func parseRunbook(id string, data []byte, mod time.Time) Runbook {
	rb := Runbook{ID: id, Updated: mod.UTC().Format(time.RFC3339), Targets: []RunbookTarget{}}
	body := string(data)
	if m := frontMatter.FindStringSubmatch(body); m != nil {
		var meta runbookMeta
		if yaml.Unmarshal([]byte(m[1]), &meta) == nil {
			rb.Title = meta.Title
			if meta.Targets != nil {
				rb.Targets = meta.Targets
			}
		}
		body = body[len(m[0]):]
	}
	rb.Body = strings.TrimLeft(body, "\n")
	if rb.Title == "" {
		// The first heading, or the file's name.
		for _, l := range strings.Split(rb.Body, "\n") {
			if strings.HasPrefix(l, "# ") {
				rb.Title = strings.TrimSpace(l[2:])
				break
			}
		}
		if rb.Title == "" {
			rb.Title = strings.TrimSuffix(filepath.Base(id), filepath.Ext(id))
		}
	}
	return rb
}

func formatRunbook(rb Runbook) ([]byte, error) {
	meta, err := yaml.Marshal(runbookMeta{Title: rb.Title, Targets: rb.Targets})
	if err != nil {
		return nil, err
	}
	return []byte("---\n" + string(meta) + "---\n\n" + strings.TrimLeft(rb.Body, "\n")), nil
}

// List returns every runbook in the folder, its subfolders included.
func (r *Runbooks) List() ([]Runbook, error) {
	root := r.dir()
	var out []Runbook
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if p == root && errors.Is(err, os.ErrNotExist) {
				return filepath.SkipAll
			}
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && p != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > 1<<20 {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		out = append(out, parseRunbook(filepath.ToSlash(rel), b, info.ModTime()))
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title) })
	if out == nil {
		out = []Runbook{}
	}
	return out, err
}

// pathOf is the file of a runbook ID, which must stay in the folder.
func (r *Runbooks) pathOf(id string) (string, error) {
	clean := path.Clean("/" + filepath.ToSlash(id))[1:]
	if clean == "" || clean != filepath.ToSlash(id) || !strings.HasSuffix(strings.ToLower(clean), ".md") {
		return "", errors.New("invalid runbook")
	}
	return filepath.Join(r.dir(), filepath.FromSlash(clean)), nil
}

// Get returns a runbook.
func (r *Runbooks) Get(id string) (Runbook, error) {
	p, err := r.pathOf(id)
	if err != nil {
		return Runbook{}, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return Runbook{}, errors.New("this runbook is no longer in the folder")
	}
	info, _ := os.Stat(p)
	return parseRunbook(id, b, info.ModTime()), nil
}

// Save writes a runbook, a new one when its ID is empty, and returns it.
func (r *Runbooks) Save(rb Runbook) (Runbook, error) {
	rb.Title = strings.TrimSpace(rb.Title)
	if rb.Title == "" {
		return Runbook{}, errors.New("give the runbook a title")
	}
	var clean []RunbookTarget
	for _, t := range rb.Targets {
		t = RunbookTarget{Cluster: strings.TrimSpace(t.Cluster), Namespace: strings.TrimSpace(t.Namespace), Kind: strings.TrimSpace(t.Kind), Name: strings.TrimSpace(t.Name)}
		if t != (RunbookTarget{}) {
			clean = append(clean, t)
		}
	}
	rb.Targets = clean
	if rb.ID == "" {
		base := safeName(strings.ToLower(strings.ReplaceAll(rb.Title, " ", "-")))
		if base == "" {
			base = "runbook"
		}
		rb.ID = base + ".md"
		for i := 2; ; i++ {
			p, _ := r.pathOf(rb.ID)
			if _, err := os.Stat(p); os.IsNotExist(err) {
				break
			}
			rb.ID = fmt.Sprintf("%s-%d.md", base, i)
		}
	}
	p, err := r.pathOf(rb.ID)
	if err != nil {
		return Runbook{}, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return Runbook{}, err
	}
	b, err := formatRunbook(rb)
	if err != nil {
		return Runbook{}, err
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		return Runbook{}, err
	}
	return r.Get(rb.ID)
}

// Delete removes a runbook's file.
func (r *Runbooks) Delete(id string) error {
	p, err := r.pathOf(id)
	if err != nil {
		return err
	}
	return os.Remove(p)
}

// matchGlob matches a wildcard pattern, without case; an empty pattern
// matches anything.
func matchGlob(pattern, s string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	ok, _ := path.Match(strings.ToLower(pattern), strings.ToLower(s))
	return ok
}

// score says how well a target matches an object: -1 when it does not,
// more for a more specific target.
func (t RunbookTarget) score(clusterNames []string, namespace, kind, name string) int {
	clusterOK := t.Cluster == "" || t.Cluster == "*"
	for _, n := range clusterNames {
		clusterOK = clusterOK || matchGlob(t.Cluster, n)
	}
	if !clusterOK || !matchGlob(t.Namespace, namespace) || !matchGlob(t.Kind, kind) || !matchGlob(t.Name, name) {
		return -1
	}
	// A target for one object needs that object; one for a kind or a
	// namespace needs an object of it, or the namespace itself.
	if (t.Name != "" && t.Name != "*" && name == "") || (t.Kind != "" && t.Kind != "*" && kind == "") {
		return -1
	}
	s := 0
	for _, f := range []string{t.Cluster, t.Namespace, t.Kind, t.Name} {
		switch {
		case f == "" || f == "*":
		case strings.Contains(f, "*"):
			s++
		default:
			s += 2
		}
	}
	return s
}

// For returns the runbooks for an object, or for a namespace or a cluster
// with empty fields, the most specific first.
func (r *Runbooks) For(cluster, namespace, kind, name string) ([]Runbook, error) {
	all, err := r.List()
	if err != nil {
		return nil, err
	}
	names := []string{r.clusters.name(cluster)}
	for _, cl := range r.clusters.List().Clusters {
		if cl.ID == cluster {
			names = append(names, cl.Context)
		}
	}
	type hit struct {
		rb    Runbook
		score int
	}
	var hits []hit
	for _, rb := range all {
		best := -1
		for _, t := range rb.Targets {
			best = max(best, t.score(names, namespace, kind, name))
		}
		if best >= 0 {
			hits = append(hits, hit{rb, best})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	out := make([]Runbook, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.rb)
	}
	return out, nil
}
