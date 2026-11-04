package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/egoist/mygo"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/yaml"
)

// The stacks that come with the app; the user's own are YAML files of the
// same form in the stacks folder of the app's data.
//
//go:embed stacks/*.yaml
var builtinStacks embed.FS

// Stack is a set of components that run together in a namespace, such as
// Loki, Alloy and Grafana for logs, installed in one go.
type Stack struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Description string `json:"description"`
	// Namespace is where the stack goes unless the user picks another.
	Namespace string `json:"namespace"`
	// Requires says what the stack needs from the cluster, for people.
	Requires   string           `json:"requires,omitempty"`
	Tags       []string         `json:"tags,omitempty"`
	Fields     []StackField     `json:"fields,omitempty"`
	Components []StackComponent `json:"components"`
	Access     []StackAccess    `json:"access,omitempty"`
	// Notes is Markdown shown once the stack runs, a template of the fields.
	Notes string `json:"notes,omitempty"`

	// Source is "builtin" or "user"; the app sets it.
	Source string `json:"source,omitempty"`
	// NeedsHelm says a component installs a Helm chart; the app sets it.
	NeedsHelm bool `json:"needsHelm,omitempty"`
}

// StackField is a setting asked before running a stack.
type StackField struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	// Type is text, password, number, bool, select, domain or email, or
	// one whose options come from the cluster: ingressClass,
	// clusterIssuer or storageClass.
	Type        string   `json:"type,omitempty"`
	Default     string   `json:"default,omitempty"`
	Hint        string   `json:"hint,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Options     []string `json:"options,omitempty"`
	// Section groups fields under a heading, in the order they come.
	Section string `json:"section,omitempty"`
	// ShowIf is a field that must be on, or not empty, for this one to
	// be asked; a field not asked is not checked either.
	ShowIf string `json:"showIf,omitempty"`
	// Pattern is a regular expression the value must match.
	Pattern string `json:"pattern,omitempty"`
	// Required refuses an empty value.
	Required bool `json:"required,omitempty"`
	// Generate makes a random password when the value is left empty.
	Generate bool `json:"generate,omitempty"`
	// Advanced fields are folded away until asked for.
	Advanced bool `json:"advanced,omitempty"`
}

// fieldTypes are the types of fields, with whether the cluster gives the
// options.
var fieldTypes = map[string]bool{"text": false, "password": false, "number": false, "bool": false, "select": false, "domain": false, "email": false, "ingressClass": true, "clusterIssuer": true, "storageClass": true}

var (
	domainPattern = regexp.MustCompile(`^(\*\.)?([a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?\.)+[a-z0-9][-a-z0-9]{0,61}[a-z0-9]$`)
	emailPattern  = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
)

// StackComponent is a part of a stack: a Helm chart, manifests at URLs or
// manifests written in the stack, all templates of the fields.
type StackComponent struct {
	// Name names the component, and the Helm release of a chart.
	Name  string `json:"name"`
	Title string `json:"title,omitempty"`
	// When is a bool field that turns the component on.
	When string `json:"when,omitempty"`
	// Namespace puts the component elsewhere than the stack, such as an
	// operator others share; uninstalling a stack keeps what another
	// installed stack also has.
	Namespace string     `json:"namespace,omitempty"`
	Helm      *StackHelm `json:"helm,omitempty"`
	URLs      []string   `json:"urls,omitempty"`
	Manifest  string     `json:"manifest,omitempty"`
	// NoWait goes on without waiting for what the component runs to be ready.
	NoWait bool `json:"noWait,omitempty"`
	// NoTemplate takes the manifest as it is, for those with {{ }} of their own.
	NoTemplate bool `json:"noTemplate,omitempty"`
	// Timeout bounds the wait, as a Go duration; 15m by default.
	Timeout string `json:"timeout,omitempty"`
}

// StackHelm is a chart, from a repository or an oci:// reference.
type StackHelm struct {
	Repo    string `json:"repo,omitempty"`
	Chart   string `json:"chart"`
	Version string `json:"version,omitempty"`
	// Values are the chart's values, as a YAML template of the fields.
	Values string `json:"values,omitempty"`
}

// StackAccess is a web page a stack serves, opened through a port forward.
type StackAccess struct {
	Label   string `json:"label"`
	Service string `json:"service"`
	Port    int    `json:"port"`
	Scheme  string `json:"scheme,omitempty"`
	Path    string `json:"path,omitempty"`
	// Domain is the domain field it is published at, when filled in; TLS
	// is the clusterIssuer field whose certificate makes it https.
	Domain string `json:"domain,omitempty"`
	TLS    string `json:"tls,omitempty"`
	User   string `json:"user,omitempty"`
	// Password is the password field that signs in; PasswordSecret is
	// "secret/key", for passwords made by what the stack runs.
	Password       string `json:"password,omitempty"`
	PasswordSecret string `json:"passwordSecret,omitempty"`
}

// StackCatalog is the stacks the app knows of.
type StackCatalog struct {
	Stacks []Stack `json:"stacks"`
	// Errors are the user's stack files that could not be read.
	Errors []string `json:"errors"`
	Folder string   `json:"folder"`
	// HelmInstalled says the helm command was found.
	HelmInstalled bool `json:"helmInstalled"`
}

// StackRequest asks to run a stack.
type StackRequest struct {
	Stack     string            `json:"stack"`
	Namespace string            `json:"namespace"`
	Values    map[string]string `json:"values"`
	// Overrides replace what a component renders, by component name: the
	// values of a chart, or the manifest of the others.
	Overrides map[string]string `json:"overrides,omitempty"`
}

// StackPlan is what running a stack does, without doing it.
type StackPlan struct {
	// Values are the request's values with defaults and generated
	// passwords filled in, or those of the stack already installed.
	Values      map[string]string  `json:"values"`
	FieldErrors map[string]string  `json:"fieldErrors"`
	Components  []PlannedComponent `json:"components"`
	Warnings    []string           `json:"warnings"`
	HelmMissing bool               `json:"helmMissing"`
	// Installed is the stack already in the namespace, which running
	// again updates.
	Installed *InstalledStack `json:"installed,omitempty"`
}

// PlannedComponent is a component as it will run.
type PlannedComponent struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	// Kind is helm, urls or manifest.
	Kind      string `json:"kind"`
	Source    string `json:"source"`
	Namespace string `json:"namespace"`
	// Content is the rendered values of a chart, or the manifest.
	Content    string `json:"content"`
	Overridden bool   `json:"overridden,omitempty"`
	Skipped    bool   `json:"skipped,omitempty"`
	Error      string `json:"error,omitempty"`
}

// InstalledStack is a stack run in a namespace, as the cluster records it.
type InstalledStack struct {
	ID         string               `json:"id"`
	Name       string               `json:"name"`
	Namespace  string               `json:"namespace"`
	Status     string               `json:"status"`
	Error      string               `json:"error,omitempty"`
	Installed  string               `json:"installed"`
	Updated    string               `json:"updated"`
	Components []InstalledComponent `json:"components"`
	// Values are the settings it ran with, passwords left out.
	Values map[string]string `json:"values"`
	// InCatalog is false when the stack's definition is gone.
	InCatalog bool          `json:"inCatalog"`
	Access    []StackAccess `json:"access"`
	// Links are its pages at the domains it was given.
	Links []StackLink `json:"links"`
}

// InstalledComponent is a component of an installed stack.
type InstalledComponent struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Source  string `json:"source"`
	Objects int    `json:"objects"`
}

// StackStep is a step of a stack job.
type StackStep struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	// Status is waiting, running, done, failed or skipped.
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// StackJob is a stack being installed or uninstalled; it goes on when the
// page stops watching it.
type StackJob struct {
	ID        string      `json:"id"`
	Cluster   string      `json:"cluster"`
	Namespace string      `json:"namespace"`
	Stack     string      `json:"stack"`
	Name      string      `json:"name"`
	Action    string      `json:"action"`
	State     string      `json:"state"`
	Error     string      `json:"error,omitempty"`
	Notes     string      `json:"notes,omitempty"`
	Links     []StackLink `json:"links,omitempty"`
	Started   string      `json:"started"`
	Finished  string      `json:"finished,omitempty"`
	Steps     []StackStep `json:"steps"`
	// Output is what the steps printed; only Follow sends it.
	Output []string `json:"output,omitempty"`
}

// StacksChanged is sent when a stack job starts, moves on or ends.
var StacksChanged = mygo.NewEvent[[]StackJob]("stacks:changed")

const (
	stackLabel     = "kubedesk.cloud/stack"
	stackPrefix    = "kubedesk-stack-"
	stackManager   = "kube-desk"
	maxStackOutput = 4000
)

var (
	stackIDPattern  = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,38}[a-z0-9])?$`)
	dnsLabel        = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)
	fieldKeyPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]*$`)
)

// Stacks installs ready-made stacks into clusters: Helm charts and
// manifests that run together, from the app's catalog or the user's.
type Stacks struct {
	kube *Kube

	mu   sync.Mutex
	jobs map[string]*stackJob
	seq  int
}

type stackJob struct {
	StackJob
	cancel  context.CancelFunc
	changed chan struct{}
}

func newStacks(kube *Kube) *Stacks {
	return &Stacks{kube: kube, jobs: map[string]*stackJob{}}
}

func (s *Stacks) dir() string {
	return filepath.Join(s.kube.clusters.dir, "stacks")
}

// ---- The catalog ----

// parseStack reads a stack's YAML and checks it.
func parseStack(data []byte) (Stack, error) {
	var st Stack
	if err := yaml.UnmarshalStrict(data, &st); err != nil {
		return Stack{}, fmt.Errorf("invalid stack: %w", err)
	}
	if err := checkStack(&st); err != nil {
		return Stack{}, err
	}
	return st, nil
}

// checkStack checks a stack and fills in its defaults.
func checkStack(st *Stack) error {
	if !stackIDPattern.MatchString(st.ID) {
		return fmt.Errorf("id %q: use lowercase letters, digits and dashes, up to 40", st.ID)
	}
	if strings.TrimSpace(st.Name) == "" {
		return errors.New("give the stack a name")
	}
	if st.Category == "" {
		st.Category = "Other"
	}
	if st.Namespace == "" {
		st.Namespace = st.ID
	}
	if !dnsLabel.MatchString(st.Namespace) {
		return fmt.Errorf("namespace %q is not a valid name", st.Namespace)
	}
	fields := map[string]StackField{}
	for i := range st.Fields {
		f := &st.Fields[i]
		if !fieldKeyPattern.MatchString(f.Key) {
			return fmt.Errorf("field key %q: use letters, digits and _, starting with a letter", f.Key)
		}
		if f.Key == "Namespace" || f.Key == "Stack" {
			return fmt.Errorf("field key %q is taken by the app", f.Key)
		}
		if _, dup := fields[f.Key]; dup {
			return fmt.Errorf("field %q is there twice", f.Key)
		}
		if f.Type == "" {
			f.Type = "text"
		}
		if _, ok := fieldTypes[f.Type]; !ok {
			return fmt.Errorf("field %q: type %q is not text, password, number, bool, select, domain, email, ingressClass, clusterIssuer or storageClass", f.Key, f.Type)
		}
		if f.Type == "select" && len(f.Options) == 0 {
			return fmt.Errorf("field %q: a select needs options", f.Key)
		}
		if f.Pattern != "" {
			if _, err := regexp.Compile(f.Pattern); err != nil {
				return fmt.Errorf("field %q: pattern: %v", f.Key, err)
			}
		}
		if f.Label == "" {
			f.Label = f.Key
		}
		fields[f.Key] = *f
	}
	for _, f := range st.Fields {
		if _, ok := fields[f.ShowIf]; f.ShowIf != "" && (!ok || f.ShowIf == f.Key) {
			return fmt.Errorf("field %q: showIf must name another field", f.Key)
		}
	}
	if len(st.Components) == 0 {
		return errors.New("a stack needs components")
	}
	names := map[string]bool{}
	for i := range st.Components {
		c := &st.Components[i]
		if !dnsLabel.MatchString(c.Name) || len(c.Name) > 53 {
			return fmt.Errorf("component name %q: use lowercase letters, digits and dashes, up to 53", c.Name)
		}
		if names[c.Name] {
			return fmt.Errorf("component %q is there twice", c.Name)
		}
		names[c.Name] = true
		if c.Title == "" {
			c.Title = c.Name
		}
		kinds := 0
		if c.Helm != nil {
			kinds++
			if c.Helm.Chart == "" {
				return fmt.Errorf("component %q: give the chart", c.Name)
			}
			if c.Helm.Repo == "" && !strings.HasPrefix(c.Helm.Chart, "oci://") {
				return fmt.Errorf("component %q: give the chart's repo, or an oci:// chart", c.Name)
			}
			st.NeedsHelm = true
		}
		if len(c.URLs) > 0 {
			kinds++
		}
		if strings.TrimSpace(c.Manifest) != "" {
			kinds++
		}
		if kinds != 1 {
			return fmt.Errorf("component %q: give one of helm, urls or manifest", c.Name)
		}
		if c.When != "" && fields[c.When].Type != "bool" {
			return fmt.Errorf("component %q: when must name a bool field", c.Name)
		}
		if c.Timeout != "" {
			if _, err := time.ParseDuration(c.Timeout); err != nil {
				return fmt.Errorf("component %q: timeout %q is not a duration like 10m", c.Name, c.Timeout)
			}
		}
		for _, t := range componentTemplates(*c) {
			if _, err := parseStackTemplate(c.Name, t); err != nil {
				return fmt.Errorf("component %q: %w", c.Name, err)
			}
		}
	}
	for _, a := range st.Access {
		if a.Service == "" || a.Port <= 0 {
			return fmt.Errorf("access %q: give the service and its port", a.Label)
		}
		if a.Password != "" && fields[a.Password].Type != "password" {
			return fmt.Errorf("access %q: password must name a password field", a.Label)
		}
		if a.PasswordSecret != "" && !strings.Contains(a.PasswordSecret, "/") {
			return fmt.Errorf("access %q: passwordSecret is secret/key", a.Label)
		}
		if a.Domain != "" && fields[a.Domain].Type != "domain" {
			return fmt.Errorf("access %q: domain must name a domain field", a.Label)
		}
		if a.TLS != "" && fields[a.TLS].Type != "clusterIssuer" {
			return fmt.Errorf("access %q: tls must name a clusterIssuer field", a.Label)
		}
		for _, t := range []string{a.Scheme, a.Path} {
			if _, err := parseStackTemplate("access", t); err != nil {
				return fmt.Errorf("access %q: %w", a.Label, err)
			}
		}
	}
	if _, err := parseStackTemplate("notes", st.Notes); err != nil {
		return fmt.Errorf("notes: %w", err)
	}
	return nil
}

func componentTemplates(c StackComponent) []string {
	if c.NoTemplate {
		return c.URLs
	}
	out := append([]string{c.Manifest, c.Namespace}, c.URLs...)
	if c.Helm != nil {
		out = append(out, c.Helm.Values, c.Helm.Version)
	}
	return out
}

func (c StackComponent) kind() string {
	switch {
	case c.Helm != nil:
		return "helm"
	case len(c.URLs) > 0:
		return "urls"
	}
	return "manifest"
}

func (c StackComponent) source() string {
	switch c.kind() {
	case "helm":
		src := c.Helm.Chart
		if c.Helm.Repo != "" {
			src = c.Helm.Repo + " " + c.Helm.Chart
		}
		if c.Helm.Version != "" {
			src += " " + c.Helm.Version
		}
		return src
	case "urls":
		return strings.Join(c.URLs, "\n")
	}
	return "manifest"
}

// Catalog returns the stacks of the app and the user's; a user's stack
// with the id of a built-in one replaces it.
func (s *Stacks) Catalog() StackCatalog {
	cat := StackCatalog{Stacks: []Stack{}, Errors: []string{}, Folder: s.dir()}
	byID := map[string]int{}
	add := func(st Stack) {
		if i, ok := byID[st.ID]; ok {
			cat.Stacks[i] = st
			return
		}
		byID[st.ID] = len(cat.Stacks)
		cat.Stacks = append(cat.Stacks, st)
	}
	entries, _ := fs.Glob(builtinStacks, "stacks/*.yaml")
	for _, p := range entries {
		data, _ := builtinStacks.ReadFile(p)
		st, err := parseStack(data)
		if err != nil {
			cat.Errors = append(cat.Errors, fmt.Sprintf("built-in %s: %v", filepath.Base(p), err))
			continue
		}
		st.Source = "builtin"
		add(st)
	}
	files, _ := filepath.Glob(filepath.Join(s.dir(), "*.yaml"))
	for _, p := range files {
		data, err := os.ReadFile(p)
		if err == nil {
			var st Stack
			if st, err = parseStack(data); err == nil {
				st.Source = "user"
				add(st)
				continue
			}
		}
		cat.Errors = append(cat.Errors, fmt.Sprintf("%s: %v", filepath.Base(p), err))
	}
	sort.SliceStable(cat.Stacks, func(i, j int) bool {
		if cat.Stacks[i].Category != cat.Stacks[j].Category {
			return cat.Stacks[i].Category < cat.Stacks[j].Category
		}
		return cat.Stacks[i].Name < cat.Stacks[j].Name
	})
	_, err := helmCommand()
	cat.HelmInstalled = err == nil
	return cat
}

func (s *Stacks) find(id string) (Stack, error) {
	for _, st := range s.Catalog().Stacks {
		if st.ID == id {
			return st, nil
		}
	}
	return Stack{}, fmt.Errorf("there is no stack %q", id)
}

// Source returns the YAML of a stack, to edit it or make a copy.
func (s *Stacks) Source(id string) (string, error) {
	if data, err := os.ReadFile(filepath.Join(s.dir(), id+".yaml")); err == nil {
		return string(data), nil
	}
	data, err := builtinStacks.ReadFile("stacks/" + id + ".yaml")
	if err != nil {
		return "", fmt.Errorf("there is no stack %q", id)
	}
	return string(data), nil
}

// Check reads a stack's YAML, as Save would, without saving it.
func (s *Stacks) Check(source string) (Stack, error) {
	return parseStack([]byte(source))
}

// Save writes one of the user's stacks; renaming its id moves the file.
func (s *Stacks) Save(source, previousID string) (Stack, error) {
	st, err := parseStack([]byte(source))
	if err != nil {
		return Stack{}, err
	}
	if err := os.MkdirAll(s.dir(), 0o755); err != nil {
		return Stack{}, err
	}
	if err := os.WriteFile(filepath.Join(s.dir(), st.ID+".yaml"), []byte(source), 0o644); err != nil {
		return Stack{}, err
	}
	if previousID != "" && previousID != st.ID {
		_ = os.Remove(filepath.Join(s.dir(), previousID+".yaml"))
	}
	st.Source = "user"
	return st, nil
}

// Delete deletes one of the user's stacks; a built-in one it replaced
// comes back.
func (s *Stacks) Delete(id string) error {
	if !stackIDPattern.MatchString(id) {
		return fmt.Errorf("there is no stack %q", id)
	}
	err := os.Remove(filepath.Join(s.dir(), id+".yaml"))
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s is not one of your stacks", id)
	}
	return err
}

// Reveal shows the folder of the user's stacks in the file manager.
func (s *Stacks) Reveal() error {
	if err := os.MkdirAll(s.dir(), 0o755); err != nil {
		return err
	}
	return mygo.Shell.OpenPath(s.dir())
}

// ---- Rendering ----

var stackFuncs = template.FuncMap{
	"quote": func(v any) string {
		b, _ := json.Marshal(fmt.Sprint(v))
		return string(b)
	},
	"toJson": func(v any) string {
		b, _ := json.Marshal(v)
		return string(b)
	},
	"b64enc": func(v any) string { return base64.StdEncoding.EncodeToString([]byte(fmt.Sprint(v))) },
	"default": func(d, v any) any {
		if v == nil || v == "" || v == false {
			return d
		}
		return v
	},
	"lower":  func(v any) string { return strings.ToLower(fmt.Sprint(v)) },
	"upper":  func(v any) string { return strings.ToUpper(fmt.Sprint(v)) },
	"trim":   func(v any) string { return strings.TrimSpace(fmt.Sprint(v)) },
	"indent": indentText,
	"nindent": func(n int, v any) string {
		return "\n" + indentText(n, v)
	},
}

func indentText(n int, v any) string {
	pad := strings.Repeat(" ", n)
	return pad + strings.ReplaceAll(fmt.Sprint(v), "\n", "\n"+pad)
}

func parseStackTemplate(name, text string) (*template.Template, error) {
	return template.New(name).Funcs(stackFuncs).Option("missingkey=error").Parse(text)
}

func renderStackTemplate(name, text string, data map[string]any) (string, error) {
	t, err := parseStackTemplate(name, text)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := t.Execute(&b, data); err != nil {
		return "", err
	}
	return b.String(), nil
}

// stackData is what the templates of a stack see: the fields, bools as
// bools, and the namespace and the stack's id.
func stackData(st Stack, namespace string, values map[string]string) map[string]any {
	data := map[string]any{"Namespace": namespace, "Stack": st.ID}
	for _, f := range st.Fields {
		v := values[f.Key]
		if f.Type == "bool" {
			data[f.Key] = v == "true"
		} else {
			data[f.Key] = v
		}
	}
	return data
}

// resolveValues fills in the values of a stack: those given, those it
// ran with before, the defaults, then generated passwords. It reports
// the fields whose values are wrong.
func resolveValues(st Stack, given, previous map[string]string) (map[string]string, map[string]string) {
	values := map[string]string{}
	errs := map[string]string{}
	for _, f := range st.Fields {
		v, ok := given[f.Key]

		if !ok {
			if v, ok = previous[f.Key]; !ok {
				v = f.Default
			}
		}
		if f.Type != "password" {
			v = strings.TrimSpace(v)
		}
		if f.Type == "domain" || f.Type == "email" {
			v = strings.ToLower(v)
		}
		if v == "" && f.Type == "password" && f.Generate {
			v = generatePassword()
		}
		switch {
		case v == "" && f.Type == "bool":
			v = "false"
		case v == "" && f.Required:
			errs[f.Key] = "required"
		case v == "":
		case f.Type == "number":
			if _, err := strconv.ParseFloat(v, 64); err != nil {
				errs[f.Key] = "not a number"
			}
		case f.Type == "bool" && v != "true" && v != "false":
			errs[f.Key] = "true or false"
		case f.Type == "select" && !contains(f.Options, v):
			errs[f.Key] = "not one of the options"
		case f.Type == "domain" && !domainPattern.MatchString(v):
			errs[f.Key] = "not a domain such as grafana.example.com"
		case f.Type == "email" && !emailPattern.MatchString(v):
			errs[f.Key] = "not an email address"
		case f.Pattern != "" && !regexp.MustCompile(f.Pattern).MatchString(v):
			errs[f.Key] = "not in the expected form"
		}
		values[f.Key] = v
	}
	// Fields not asked, as what they depend on is off, are not checked.
	for _, f := range st.Fields {
		if !fieldShown(st, f, values) {
			delete(errs, f.Key)
		}
	}
	return values, errs
}

// fieldShown says whether a field is asked, following its showIf chain.
func fieldShown(st Stack, f StackField, values map[string]string) bool {
	for seen := 0; f.ShowIf != "" && seen < len(st.Fields); seen++ {
		if v := values[f.ShowIf]; v == "" || v == "false" {
			return false
		}
		for _, g := range st.Fields {
			if g.Key == f.ShowIf {
				f = g
				break
			}
		}
	}
	return true
}

// StackLink is a web page of a stack at its domain.
type StackLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// stackLinks are the pages of a stack published at the domains given.
func stackLinks(st Stack, values map[string]string) []StackLink {
	out := []StackLink{}
	data := stackData(st, "", values)
	for _, a := range st.Access {
		domain := values[a.Domain]
		if a.Domain == "" || domain == "" {
			continue
		}
		scheme := "http"
		if a.TLS != "" && values[a.TLS] != "" {
			scheme = "https"
		}
		path, _ := renderStackTemplate("path", a.Path, data)
		out = append(out, StackLink{Label: a.Label, URL: scheme + "://" + domain + path})
	}
	return out
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// generatePassword makes a password that password rules accept: 20
// characters with upper and lower case letters, digits and a dash.
func generatePassword() string {
	const letters = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	pick := func(set string) byte {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(set))))
		return set[n.Int64()]
	}
	for {
		b := make([]byte, 20)
		for i := range b {
			b[i] = pick(letters)
		}
		b[10] = '-'
		p := string(b)
		if strings.ContainsAny(p, "abcdefghijkmnopqrstuvwxyz") && strings.ContainsAny(p, "ABCDEFGHJKLMNPQRSTUVWXYZ") && strings.ContainsAny(p, "23456789") {
			return p
		}
	}
}

// planStack renders the components of a stack with the values.
func planStack(st Stack, namespace string, values map[string]string, overrides map[string]string) []PlannedComponent {
	data := stackData(st, namespace, values)
	out := make([]PlannedComponent, 0, len(st.Components))
	for _, c := range st.Components {
		p := PlannedComponent{Name: c.Name, Title: c.Title, Kind: c.kind(), Source: c.source(), Namespace: namespace}
		if c.When != "" && values[c.When] != "true" {
			p.Skipped = true
			out = append(out, p)
			continue
		}
		var err error
		if c.Namespace != "" {
			if p.Namespace, err = renderStackTemplate(c.Name, c.Namespace, data); err == nil && !dnsLabel.MatchString(p.Namespace) {
				err = fmt.Errorf("namespace %q is not a valid name", p.Namespace)
			}
			if err != nil {
				p.Error = err.Error()
				out = append(out, p)
				continue
			}
		}
		switch c.kind() {
		case "helm":
			p.Content, err = renderStackTemplate(c.Name, c.Helm.Values, data)
			if err == nil && c.Helm.Version != "" {
				var v string
				if v, err = renderStackTemplate(c.Name, c.Helm.Version, data); err == nil {
					p.Source = strings.Replace(p.Source, c.Helm.Version, v, 1)
				}
			}
		case "urls":
			var urls []string
			for _, u := range c.URLs {
				if !c.NoTemplate {
					if u, err = renderStackTemplate(c.Name, u, data); err != nil {
						break
					}
				}
				urls = append(urls, strings.TrimSpace(u))
			}
			p.Source = strings.Join(urls, "\n")
		default:
			p.Content = c.Manifest
			if !c.NoTemplate {
				p.Content, err = renderStackTemplate(c.Name, c.Manifest, data)
			}
		}
		if err != nil {
			p.Error = err.Error()
		}
		if o, ok := overrides[c.Name]; ok && p.Kind != "urls" {
			p.Content, p.Overridden, p.Error = o, true, ""
		}
		if p.Error == "" && p.Kind != "urls" && strings.TrimSpace(p.Content) != "" {
			var check any
			for _, doc := range splitYAMLDocs(p.Content) {
				if err := yaml.Unmarshal([]byte(doc), &check); err != nil {
					p.Error = "invalid YAML: " + err.Error()
					break
				}
			}
		}
		out = append(out, p)
	}
	return out
}

func splitYAMLDocs(text string) []string {
	return regexp.MustCompile(`(?m)^---\s*$`).Split(text, -1)
}

// ---- Records of installed stacks ----

// stackRecord is what the cluster keeps of an installed stack, in a
// ConfigMap of its namespace; its passwords are in a Secret beside it.
type stackRecord struct {
	Stack      string            `json:"stack"`
	Name       string            `json:"name"`
	Status     string            `json:"status"`
	Error      string            `json:"error,omitempty"`
	Installed  string            `json:"installed"`
	Updated    string            `json:"updated"`
	Values     map[string]string `json:"values"`
	Components []recordComponent `json:"components"`
}

type recordComponent struct {
	Name      string      `json:"name"`
	Kind      string      `json:"kind"`
	Source    string      `json:"source"`
	Namespace string      `json:"namespace,omitempty"`
	Objects   []objectRef `json:"objects,omitempty"`
}

type objectRef struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name"`
}

func (o objectRef) String() string {
	if o.Namespace != "" {
		return fmt.Sprintf("%s %s/%s", o.Kind, o.Namespace, o.Name)
	}
	return o.Kind + " " + o.Name
}

func readRecord(cm *corev1.ConfigMap) (stackRecord, bool) {
	var r stackRecord
	if cm.Labels[stackLabel] == "" || json.Unmarshal([]byte(cm.Data["record.json"]), &r) != nil {
		return r, false
	}
	return r, true
}

// loadRecord returns the record of a stack in a namespace, and its
// passwords.
func loadRecord(ctx context.Context, cn *conn, namespace, id string) (*stackRecord, map[string]string, error) {
	cm, err := cn.cs.CoreV1().ConfigMaps(namespace).Get(ctx, stackPrefix+id, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, errors.New(friendlyError(err))
	}
	r, ok := readRecord(cm)
	if !ok {
		return nil, nil, nil
	}
	secrets := map[string]string{}
	if sec, err := cn.cs.CoreV1().Secrets(namespace).Get(ctx, stackPrefix+id, metav1.GetOptions{}); err == nil {
		for k, v := range sec.Data {
			secrets[k] = string(v)
		}
	}
	return &r, secrets, nil
}

func saveRecord(ctx context.Context, cn *conn, namespace string, r stackRecord, secrets map[string]string) error {
	data, _ := json.Marshal(r)
	lbl := map[string]string{stackLabel: r.Stack, "app.kubernetes.io/managed-by": stackManager}
	cm := &corev1.ConfigMap{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
		ObjectMeta: metav1.ObjectMeta{Name: stackPrefix + r.Stack, Namespace: namespace, Labels: lbl},
		Data:       map[string]string{"record.json": string(data)},
	}
	if err := applyTyped(ctx, cn, cm); err != nil {
		return err
	}
	if len(secrets) == 0 {
		return nil
	}
	sec := &corev1.Secret{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{Name: stackPrefix + r.Stack, Namespace: namespace, Labels: lbl},
		Type:       corev1.SecretTypeOpaque,
		StringData: secrets,
	}
	return applyTyped(ctx, cn, sec)
}

func applyTyped(ctx context.Context, cn *conn, obj any) error {
	b, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	u := &unstructured.Unstructured{Object: m}
	_, err = applyObject(ctx, cn, u, u.GetNamespace())
	return err
}

func installedOf(r stackRecord, namespace string, cat map[string]Stack) InstalledStack {
	is := InstalledStack{ID: r.Stack, Name: r.Name, Namespace: namespace, Status: r.Status, Error: r.Error, Installed: r.Installed, Updated: r.Updated, Values: r.Values, Components: []InstalledComponent{}, Access: []StackAccess{}, Links: []StackLink{}}
	for _, c := range r.Components {
		is.Components = append(is.Components, InstalledComponent{Name: c.Name, Kind: c.Kind, Source: c.Source, Objects: len(c.Objects)})
	}
	if st, ok := cat[r.Stack]; ok {
		is.InCatalog = true
		is.Access = st.Access
		is.Links = stackLinks(st, r.Values)
	}
	if is.Values == nil {
		is.Values = map[string]string{}
	}
	return is
}

// Installed returns the stacks installed in a cluster.
func (s *Stacks) Installed(ctx context.Context, cluster string) ([]InstalledStack, error) {
	cn, err := s.kube.clusters.conn(cluster)
	if err != nil {
		return nil, err
	}
	list, err := cn.cs.CoreV1().ConfigMaps("").List(ctx, metav1.ListOptions{LabelSelector: stackLabel})
	if err != nil {
		return nil, errors.New(friendlyError(err))
	}
	cat := map[string]Stack{}
	for _, st := range s.Catalog().Stacks {
		cat[st.ID] = st
	}
	out := []InstalledStack{}
	for i := range list.Items {
		cm := &list.Items[i]
		if cm.Name != stackPrefix+cm.Labels[stackLabel] {
			continue
		}
		if r, ok := readRecord(cm); ok {
			out = append(out, installedOf(r, cm.Namespace, cat))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Namespace+"/"+out[i].ID < out[j].Namespace+"/"+out[j].ID })
	return out, nil
}

// ---- Planning ----

// Plan works out what running a stack does: its values with defaults and
// generated passwords, and what each component renders.
func (s *Stacks) Plan(ctx context.Context, cluster string, req StackRequest) (StackPlan, error) {
	st, err := s.find(req.Stack)
	if err != nil {
		return StackPlan{}, err
	}
	cn, err := s.kube.clusters.conn(cluster)
	if err != nil {
		return StackPlan{}, err
	}
	ns := strings.TrimSpace(req.Namespace)
	if ns == "" {
		ns = st.Namespace
	}
	plan := StackPlan{FieldErrors: map[string]string{}, Warnings: []string{}}
	if !dnsLabel.MatchString(ns) {
		plan.FieldErrors["namespace"] = "lowercase letters, digits and dashes"
	}
	var previous map[string]string
	if plan.FieldErrors["namespace"] == "" {
		rec, secrets, err := loadRecord(ctx, cn, ns, st.ID)
		if err != nil {
			plan.Warnings = append(plan.Warnings, "Could not check what the namespace has: "+err.Error())
		}
		if rec != nil {
			is := installedOf(*rec, ns, map[string]Stack{st.ID: st})
			plan.Installed = &is
			previous = map[string]string{}
			for k, v := range rec.Values {
				previous[k] = v
			}
			for k, v := range secrets {
				previous[k] = v
			}
		} else {
			plan.Warnings = append(plan.Warnings, s.conflicts(ctx, cn, st, ns)...)
		}
	}
	values, errs := resolveValues(st, req.Values, previous)
	plan.Values = values
	for k, v := range errs {
		plan.FieldErrors[k] = v
	}
	plan.Components = planStack(st, ns, values, req.Overrides)
	if st.NeedsHelm {
		if _, err := helmCommand(); err != nil {
			plan.HelmMissing = true
		}
	}
	return plan, nil
}

// conflicts warns of Helm releases a new stack would take over.
func (s *Stacks) conflicts(ctx context.Context, cn *conn, st Stack, namespace string) []string {
	var out []string
	releases, err := helmReleases(ctx, cn.cs, namespace)
	if err != nil {
		return nil
	}
	have := map[string]HelmRelease{}
	for _, r := range releases {
		have[r.Name] = r
	}
	for _, c := range st.Components {
		if r, ok := have[c.Name]; ok && c.Helm != nil {
			out = append(out, fmt.Sprintf("Namespace %s already has the Helm release %s (%s %s): it will be upgraded with this stack's chart and values.", namespace, r.Name, r.Chart, r.ChartVersion))
		}
	}
	return out
}

// ---- Jobs ----

func (s *Stacks) newJob(cluster, namespace string, st Stack, action string, steps []StackStep) (*stackJob, context.Context, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.State == "running" && j.Cluster == cluster && j.Namespace == namespace && j.Stack == st.ID {
			return nil, nil, fmt.Errorf("%s is already being changed in %s", st.Name, namespace)
		}
	}
	s.seq++
	ctx, cancel := context.WithCancel(context.Background())
	j := &stackJob{StackJob: StackJob{
		ID: strconv.Itoa(s.seq), Cluster: cluster, Namespace: namespace, Stack: st.ID, Name: st.Name,
		Action: action, State: "running", Started: time.Now().UTC().Format(time.RFC3339), Steps: steps,
	}, cancel: cancel, changed: make(chan struct{})}
	s.jobs[j.ID] = j
	return j, ctx, nil
}

// update changes a job under the lock and tells who follows it; with
// broadcast, the pages hear of it too.
func (s *Stacks) update(j *stackJob, broadcast bool, f func(*StackJob)) {
	s.mu.Lock()
	f(&j.StackJob)
	close(j.changed)
	j.changed = make(chan struct{})
	var list []StackJob
	if broadcast {
		list = s.listLocked()
	}
	s.mu.Unlock()
	if broadcast {
		_ = StacksChanged.Broadcast(list)
	}
}

func (s *Stacks) listLocked() []StackJob {
	out := make([]StackJob, 0, len(s.jobs))
	for _, j := range s.jobs {
		c := j.StackJob
		c.Output = nil
		c.Steps = append([]StackStep(nil), j.Steps...)
		out = append(out, c)
	}
	sort.Slice(out, func(i, k int) bool {
		a, _ := strconv.Atoi(out[i].ID)
		b, _ := strconv.Atoi(out[k].ID)
		return a > b
	})
	return out
}

// Jobs returns the stack jobs of this session, the latest first.
func (s *Stacks) Jobs() []StackJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listLocked()
}

// Follow streams a job, output included, as it changes, until it ends or
// the page stops listening.
func (s *Stacks) Follow(ctx context.Context, id string, job *mygo.Channel[StackJob]) error {
	for {
		s.mu.Lock()
		j, ok := s.jobs[id]
		if !ok {
			s.mu.Unlock()
			return fmt.Errorf("there is no job %s", id)
		}
		snap := j.StackJob
		snap.Steps = append([]StackStep(nil), j.Steps...)
		snap.Output = append([]string(nil), j.Output...)
		changed := j.changed
		s.mu.Unlock()
		if err := job.Send(snap); err != nil {
			return nil
		}
		if snap.State != "running" {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-changed:
		}
		// Output comes line by line; batching keeps the page calm.
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(150 * time.Millisecond):
		}
	}
}

// Cancel stops a running job; what its steps already did stays.
func (s *Stacks) Cancel(id string) {
	s.mu.Lock()
	j, ok := s.jobs[id]
	s.mu.Unlock()
	if ok {
		j.cancel()
	}
}

func (s *Stacks) logf(j *stackJob, format string, args ...any) {
	s.log(j, fmt.Sprintf(format, args...))
}

func (s *Stacks) log(j *stackJob, line string) {
	if len(line) > 2000 {
		line = line[:2000] + "…"
	}
	s.update(j, false, func(sj *StackJob) {
		sj.Output = append(sj.Output, line)
		if len(sj.Output) > maxStackOutput {
			sj.Output = sj.Output[len(sj.Output)-maxStackOutput:]
		}
	})
}

func (s *Stacks) step(j *stackJob, i int, status, detail string) {
	s.update(j, true, func(sj *StackJob) {
		sj.Steps[i].Status = status
		sj.Steps[i].Detail = detail
	})
}

func (s *Stacks) finish(j *stackJob, err error, notes string, links []StackLink) {
	s.update(j, true, func(sj *StackJob) {
		sj.Finished = time.Now().UTC().Format(time.RFC3339)
		switch {
		case err == nil:
			sj.State = "done"
			sj.Notes = notes
			sj.Links = links
		case errors.Is(err, context.Canceled):
			sj.State = "cancelled"
			sj.Error = "Cancelled"
		default:
			sj.State = "failed"
			sj.Error = err.Error()
		}
		for i := range sj.Steps {
			if sj.Steps[i].Status == "running" {
				sj.Steps[i].Status = "failed"
			}
		}
	})
	j.cancel()
}

// ---- Installing ----

// Install runs a stack in a namespace of a cluster, making the namespace
// when it is missing, or updates the stack already there. It returns the
// job, which goes on in the background.
func (s *Stacks) Install(ctx context.Context, cluster string, req StackRequest) (string, error) {
	if err := s.kube.clusters.writable(cluster); err != nil {
		return "", err
	}
	plan, err := s.Plan(ctx, cluster, req)
	if err != nil {
		return "", err
	}
	if len(plan.FieldErrors) > 0 {
		var list []string
		for k, v := range plan.FieldErrors {
			list = append(list, k+": "+v)
		}
		sort.Strings(list)
		return "", errors.New("check the settings: " + strings.Join(list, ", "))
	}
	for _, c := range plan.Components {
		if c.Error != "" {
			return "", fmt.Errorf("%s: %s", c.Title, c.Error)
		}
	}
	if plan.HelmMissing {
		_, err := helmCommand()
		return "", err
	}
	st, _ := s.find(req.Stack)
	ns := strings.TrimSpace(req.Namespace)
	if ns == "" {
		ns = st.Namespace
	}
	cn, err := s.kube.clusters.conn(cluster)
	if err != nil {
		return "", err
	}
	steps := []StackStep{{Name: "namespace", Title: "Namespace " + ns, Status: "waiting"}}
	for _, c := range plan.Components {
		status := "waiting"
		if c.Skipped {
			status = "skipped"
		}
		steps = append(steps, StackStep{Name: c.Name, Title: c.Title, Status: status})
	}
	j, jctx, err := s.newJob(cluster, ns, st, "install", steps)
	if err != nil {
		return "", err
	}
	go func() {
		err := s.install(jctx, j, cn, st, ns, plan)
		notes := ""
		if err == nil {
			notes, _ = renderStackTemplate("notes", st.Notes, stackData(st, ns, plan.Values))
		}
		s.finish(j, err, notes, stackLinks(st, plan.Values))
	}()
	return j.ID, nil
}

func (s *Stacks) install(ctx context.Context, j *stackJob, cn *conn, st Stack, ns string, plan StackPlan) error {
	now := time.Now().UTC().Format(time.RFC3339)
	rec := stackRecord{Stack: st.ID, Name: st.Name, Status: "installing", Installed: now, Updated: now, Values: map[string]string{}}
	secrets := map[string]string{}
	for _, f := range st.Fields {
		if f.Type == "password" {
			secrets[f.Key] = plan.Values[f.Key]
		} else {
			rec.Values[f.Key] = plan.Values[f.Key]
		}
	}
	var previous []recordComponent
	if plan.Installed != nil {
		rec.Installed = plan.Installed.Installed
		if old, _, err := loadRecord(ctx, cn, ns, st.ID); err == nil && old != nil {
			previous = old.Components
		}
	}

	s.step(j, 0, "running", "")
	if err := ensureNamespace(ctx, cn, ns, st.ID); err != nil {
		s.step(j, 0, "failed", err.Error())
		return err
	}
	rec.Components = previous
	if err := saveRecord(ctx, cn, ns, rec, secrets); err != nil {
		s.step(j, 0, "failed", err.Error())
		return err
	}
	s.step(j, 0, "done", "")

	fail := func(i int, err error) error {
		rec.Status, rec.Error = "failed", err.Error()
		rec.Updated = time.Now().UTC().Format(time.RFC3339)
		_ = saveRecord(context.Background(), cn, ns, rec, secrets)
		s.step(j, i, "failed", err.Error())
		return err
	}
	comps := map[string]recordComponent{}
	for _, c := range previous {
		comps[c.Name] = c
	}
	for i, p := range plan.Components {
		step := i + 1
		if p.Skipped {
			continue
		}
		c := st.Components[i]
		s.step(j, step, "running", "")
		timeout := 15 * time.Minute
		if c.Timeout != "" {
			timeout, _ = time.ParseDuration(c.Timeout)
		}
		rc := recordComponent{Name: c.Name, Kind: p.Kind, Source: p.Source}
		cns := p.Namespace
		if cns != ns {
			rc.Namespace = cns
		}
		var err error
		switch p.Kind {
		case "helm":
			err = s.helmInstall(ctx, j, cn, c, p, cns, timeout)
		case "urls":
			var text string
			if err = ensureNamespace(ctx, cn, cns, st.ID); err == nil {
				text, err = s.fetchManifests(ctx, j, strings.Split(p.Source, "\n"))
			}
			if err == nil {
				rc.Objects, err = s.applyManifest(ctx, j, cn, text, cns, st.ID, !c.NoWait, timeout)
			}
		default:
			if err = ensureNamespace(ctx, cn, cns, st.ID); err == nil {
				rc.Objects, err = s.applyManifest(ctx, j, cn, p.Content, cns, st.ID, !c.NoWait, timeout)
			}
		}
		// What the component made before and no longer has goes, as
		// Helm does; until then, both stay on record for uninstalling.
		prev := comps[c.Name].Objects
		if err == nil && p.Kind != "helm" {
			err = s.prune(ctx, j, cn, ns, st.ID, prev, rc.Objects)
		}
		if err != nil {
			rc.Objects = mergeRefs(prev, rc.Objects)
		}
		comps[c.Name] = rc
		rec.Components = orderedComponents(st, comps)
		if err != nil {
			return fail(step, err)
		}
		_ = saveRecord(ctx, cn, ns, rec, secrets)
		s.step(j, step, "done", "")
	}
	// Components turned off, or gone from the stack, are removed.
	running := map[string]bool{}
	for _, p := range plan.Components {
		running[p.Name] = !p.Skipped
	}
	for name, rc := range comps {
		if running[name] {
			continue
		}
		s.logf(j, "Removing %s, which this run leaves out", name)
		if err := s.removeComponent(ctx, j, cn, ns, st.ID, rc); err != nil {
			return fail(len(plan.Components), err)
		}
		delete(comps, name)
	}
	rec.Components = orderedComponents(st, comps)
	rec.Status, rec.Error = "installed", ""
	rec.Updated = time.Now().UTC().Format(time.RFC3339)
	if err := saveRecord(ctx, cn, ns, rec, secrets); err != nil {
		return err
	}
	s.logf(j, "✓ %s is running in %s", st.Name, ns)
	return nil
}

// prune deletes the objects a component applied before and no longer has,
// keeping those another installed stack has.
func (s *Stacks) prune(ctx context.Context, j *stackJob, cn *conn, ns, id string, before, now []objectRef) error {
	keep := map[objectRef]bool{}
	for _, o := range now {
		keep[o] = true
	}
	var stale []objectRef
	for _, o := range before {
		if !keep[o] {
			stale = append(stale, o)
		}
	}
	if len(stale) == 0 {
		return nil
	}
	shared, err := sharedParts(ctx, cn, ns, id)
	if err != nil {
		return err
	}
	var mine []objectRef
	for _, o := range stale {
		if shared[o.String()] == "" {
			mine = append(mine, o)
		}
	}
	return s.deleteObjects(ctx, j, cn, mine)
}

// removeComponent removes a component of a stack: its Helm release or
// the objects it applied, keeping what another installed stack has.
func (s *Stacks) removeComponent(ctx context.Context, j *stackJob, cn *conn, ns, id string, c recordComponent) error {
	cns := c.Namespace
	if cns == "" {
		cns = ns
	}
	shared, err := sharedParts(ctx, cn, ns, id)
	if err != nil {
		return err
	}
	if c.Kind == "helm" {
		if by := shared["helm/"+cns+"/"+c.Name]; by != "" {
			s.logf(j, "Keeping the Helm release %s/%s: %s has it too", cns, c.Name, by)
			return nil
		}
		return s.helmRun(ctx, j, cn, "uninstall", c.Name, "--namespace", cns, "--ignore-not-found")
	}
	var mine []objectRef
	for _, o := range c.Objects {
		if by := shared[o.String()]; by != "" {
			s.logf(j, "Keeping %s: %s has it too", o, by)
		} else {
			mine = append(mine, o)
		}
	}
	return s.deleteObjects(ctx, j, cn, mine)
}

// mergeRefs keeps the objects a component made before as well as now, so
// that uninstalling removes both.
func mergeRefs(before, now []objectRef) []objectRef {
	seen := map[objectRef]bool{}
	var out []objectRef
	for _, list := range [][]objectRef{now, before} {
		for _, o := range list {
			if !seen[o] {
				seen[o] = true
				out = append(out, o)
			}
		}
	}
	return out
}

func orderedComponents(st Stack, comps map[string]recordComponent) []recordComponent {
	var out []recordComponent
	done := map[string]bool{}
	for _, c := range st.Components {
		if rc, ok := comps[c.Name]; ok {
			out = append(out, rc)
			done[c.Name] = true
		}
	}
	for name, rc := range comps {
		if !done[name] {
			out = append(out, rc)
		}
	}
	return out
}

func ensureNamespace(ctx context.Context, cn *conn, ns, stack string) error {
	_, err := cn.cs.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return errors.New(friendlyError(err))
	}
	_, err = cn.cs.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name: ns, Labels: map[string]string{stackLabel: stack, "app.kubernetes.io/managed-by": stackManager},
	}}, metav1.CreateOptions{FieldManager: stackManager})
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

// helmRun runs the helm command against the cluster, printing its output
// into the job.
func (s *Stacks) helmRun(ctx context.Context, j *stackJob, cn *conn, args ...string) error {
	helm, err := helmCommand()
	if err != nil {
		return err
	}
	args = append(args, "--kube-context", cn.cluster.Context)
	s.log(j, "$ helm "+strings.Join(args, " "))
	cmd := exec.CommandContext(ctx, helm, args...)
	// Stacks name their charts' repositories, so helm gets repositories of
	// its own: with the user's, a repository whose index is not cached
	// fails every install ("no cached repo found"). Registry logins, for
	// oci:// charts, stay the user's.
	dir := filepath.Join(s.kube.clusters.dir, "helm")
	cmd.Env = append(os.Environ(),
		"KUBECONFIG="+strings.Join(cn.src.files, string(os.PathListSeparator)),
		"HELM_REPOSITORY_CONFIG="+filepath.Join(dir, "repositories.yaml"),
		"HELM_REPOSITORY_CACHE="+filepath.Join(dir, "cache"),
	)
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		return err
	}
	// The error helm reports is its line starting with "Error:", which
	// lines of detail may follow.
	var last, reported string
	done := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			last = sc.Text()
			if strings.HasPrefix(last, "Error: ") {
				reported = strings.TrimPrefix(last, "Error: ")
			}
			s.log(j, last)
		}
		close(done)
	}()
	err = cmd.Wait()
	pw.Close()
	<-done
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		switch {
		case reported != "":
			return errors.New(reported)
		case last != "":
			return errors.New(last)
		}
		return err
	}
	return nil
}

func (s *Stacks) helmInstall(ctx context.Context, j *stackJob, cn *conn, c StackComponent, p PlannedComponent, ns string, timeout time.Duration) error {
	f, err := os.CreateTemp("", "kubedesk-values-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.WriteString(p.Content)
	f.Close()
	if err != nil {
		return err
	}
	args := []string{"upgrade", "--install", c.Name, c.Helm.Chart, "--namespace", ns, "--create-namespace", "--values", f.Name()}
	if c.Helm.Repo != "" {
		args = append(args, "--repo", c.Helm.Repo)
	}
	if fields := strings.Fields(p.Source); c.Helm.Version != "" && len(fields) > 0 {
		args = append(args, "--version", fields[len(fields)-1])
	}
	if !c.NoWait {
		args = append(args, "--wait", "--timeout", timeout.String())
	}
	return s.helmRun(ctx, j, cn, args...)
}

// fetchManifests downloads the manifests of a component.
func (s *Stacks) fetchManifests(ctx context.Context, j *stackJob, urls []string) (string, error) {
	var docs []string
	for _, u := range urls {
		if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
			return "", fmt.Errorf("%s is not an http(s) URL", u)
		}
		s.log(j, "Downloading "+u)
		rctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		req, _ := http.NewRequestWithContext(rctx, http.MethodGet, u, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			cancel()
			return "", err
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		resp.Body.Close()
		cancel()
		if err != nil {
			return "", err
		}
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("%s: %s", u, resp.Status)
		}
		docs = append(docs, string(body))
	}
	return strings.Join(docs, "\n---\n"), nil
}

// ---- Applying manifests ----

// decodeManifest reads the objects of a manifest, Lists unpacked,
// namespaces and custom resource definitions first.
func decodeManifest(text string) ([]*unstructured.Unstructured, error) {
	var objs []*unstructured.Unstructured
	var bad error
	eachManifestObject(text, func(m map[string]any) {
		u := &unstructured.Unstructured{Object: m}
		if u.IsList() {
			_ = u.EachListItem(func(o runtime.Object) error {
				if item, ok := o.(*unstructured.Unstructured); ok {
					objs = append(objs, item)
				}
				return nil
			})
			return
		}
		if u.GetKind() == "" || u.GetAPIVersion() == "" {
			bad = errors.New("every object needs apiVersion and kind")
			return
		}
		objs = append(objs, u)
	})
	if bad != nil {
		return nil, bad
	}
	rank := func(u *unstructured.Unstructured) int {
		switch u.GetKind() {
		case "Namespace":
			return 0
		case "CustomResourceDefinition":
			return 1
		}
		return 2
	}
	sort.SliceStable(objs, func(i, k int) bool { return rank(objs[i]) < rank(objs[k]) })
	return objs, nil
}

// applyObject applies an object server side, as the app's field manager,
// taking over fields others set; objects without a namespace go into ns
// when their kind has namespaces.
func applyObject(ctx context.Context, cn *conn, u *unstructured.Unstructured, ns string) (objectRef, error) {
	gvk := u.GroupVersionKind()
	mapping, err := cn.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		cn.mapper.Reset()
		if mapping, err = cn.mapper.RESTMapping(gvk.GroupKind(), gvk.Version); err != nil {
			return objectRef{}, err
		}
	}
	var r dynamic.ResourceInterface = cn.dyn.Resource(mapping.Resource)
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		if u.GetNamespace() == "" {
			u.SetNamespace(ns)
		}
		r = cn.dyn.Resource(mapping.Resource).Namespace(u.GetNamespace())
	} else {
		u.SetNamespace("")
	}
	u.SetResourceVersion("")
	u.SetManagedFields(nil)
	body, err := json.Marshal(u.Object)
	if err != nil {
		return objectRef{}, err
	}
	force := true
	_, err = r.Patch(ctx, u.GetName(), types.ApplyPatchType, body, metav1.PatchOptions{FieldManager: stackManager, Force: &force})
	return objectRef{APIVersion: u.GetAPIVersion(), Kind: u.GetKind(), Namespace: u.GetNamespace(), Name: u.GetName()}, err
}

// applyManifest applies the objects of a manifest, labelled as the
// stack's, then waits for them to be ready.
func (s *Stacks) applyManifest(ctx context.Context, j *stackJob, cn *conn, text, ns, stack string, wait bool, timeout time.Duration) ([]objectRef, error) {
	objs, err := decodeManifest(text)
	if err != nil {
		return nil, err
	}
	if len(objs) == 0 {
		return nil, errors.New("the manifest has no objects")
	}
	var refs []objectRef
	for _, u := range objs {
		lbl := u.GetLabels()
		if lbl == nil {
			lbl = map[string]string{}
		}
		lbl[stackLabel] = stack
		u.SetLabels(lbl)
		var ref objectRef
		// The kinds of a definition just made take a moment to be served.
		deadline := time.Now().Add(90 * time.Second)
		for {
			ref, err = applyObject(ctx, cn, u, ns)
			if err == nil || !meta.IsNoMatchError(err) || time.Now().After(deadline) {
				break
			}
			select {
			case <-ctx.Done():
				return refs, ctx.Err()
			case <-time.After(2 * time.Second):
			}
		}
		if err != nil {
			return refs, fmt.Errorf("%s %s: %s", u.GetKind(), u.GetName(), friendlyError(err))
		}
		refs = append(refs, ref)
		s.logf(j, "%s applied", strings.ToLower(ref.Kind)+"/"+ref.Name)
	}
	if !wait {
		return refs, nil
	}
	return refs, s.waitReady(ctx, j, cn, refs, timeout)
}

// waitReady waits for the workloads, definitions and custom resources of
// a component to be ready.
func (s *Stacks) waitReady(ctx context.Context, j *stackJob, cn *conn, refs []objectRef, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	said := map[objectRef]string{}
	for {
		var pending []string
		for _, ref := range refs {
			ready, why := s.objectReady(ctx, cn, ref, time.Since(start))
			if ready {
				continue
			}
			pending = append(pending, ref.String()+": "+why)
			if said[ref] != why {
				said[ref] = why
				s.logf(j, "Waiting for %s: %s", strings.ToLower(ref.Kind)+"/"+ref.Name, why)
			}
		}
		if len(pending) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("not ready after %s: %s", timeout, strings.Join(pending, "; "))
			}
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

var builtinGroups = map[string]bool{"": true, "apps": true, "batch": true, "rbac.authorization.k8s.io": true, "networking.k8s.io": true, "policy": true, "storage.k8s.io": true, "admissionregistration.k8s.io": true, "apiextensions.k8s.io": true, "autoscaling": true, "scheduling.k8s.io": true, "coordination.k8s.io": true, "discovery.k8s.io": true, "apiregistration.k8s.io": true, "certificates.k8s.io": true}

// objectReady says whether an object is ready, and why not.
func (s *Stacks) objectReady(ctx context.Context, cn *conn, ref objectRef, waited time.Duration) (bool, string) {
	gv, _ := schema.ParseGroupVersion(ref.APIVersion)
	watched := map[string]bool{"Deployment": true, "StatefulSet": true, "DaemonSet": true, "CustomResourceDefinition": true, "Job": true}
	if builtinGroups[gv.Group] && !watched[ref.Kind] {
		return true, ""
	}
	mapping, err := cn.mapper.RESTMapping(schema.GroupKind{Group: gv.Group, Kind: ref.Kind}, gv.Version)
	if err != nil {
		return false, "its kind is not served yet"
	}
	var r dynamic.ResourceInterface = cn.dyn.Resource(mapping.Resource)
	if ref.Namespace != "" {
		r = cn.dyn.Resource(mapping.Resource).Namespace(ref.Namespace)
	}
	u, err := r.Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return false, friendlyError(err)
	}
	return readiness(u, waited)
}

// readiness tells whether an object is ready from its status.
func readiness(u *unstructured.Unstructured, waited time.Duration) (bool, string) {
	num := func(path ...string) int64 {
		n, _, _ := unstructured.NestedInt64(u.Object, path...)
		return n
	}
	str := func(path ...string) string {
		v, _, _ := unstructured.NestedString(u.Object, path...)
		return v
	}
	if g := u.GetGeneration(); g > 0 && num("status", "observedGeneration") > 0 && num("status", "observedGeneration") < g {
		return false, "being updated"
	}
	switch u.GetKind() {
	case "Deployment", "StatefulSet":
		want, ok, _ := unstructured.NestedInt64(u.Object, "spec", "replicas")
		if !ok {
			want = 1
		}
		ready := num("status", "readyReplicas")
		if u.GetKind() == "Deployment" {
			ready = num("status", "availableReplicas")
		}
		if ready < want || num("status", "updatedReplicas") < want {
			return false, fmt.Sprintf("%d/%d ready", ready, want)
		}
		return true, ""
	case "DaemonSet":
		want := num("status", "desiredNumberScheduled")
		ready := num("status", "numberReady")
		if ready < want || num("status", "updatedNumberScheduled") < want {
			return false, fmt.Sprintf("%d/%d ready", ready, want)
		}
		return true, ""
	case "Job":
		if num("status", "succeeded") > 0 {
			return true, ""
		}
		if cond(u, "Failed") == "True" {
			return true, ""
		}
		return false, "running"
	case "CustomResourceDefinition":
		if cond(u, "Established") == "True" {
			return true, ""
		}
		return false, "not established yet"
	}
	// Custom resources: their operator reports how they are.
	status, ok := u.Object["status"].(map[string]any)
	if !ok || len(status) == 0 {
		if waited > 2*time.Minute {
			return true, ""
		}
		return false, "waiting for its operator"
	}
	if c := cond(u, "Ready"); c != "" {
		if c == "True" {
			return true, ""
		}
		return false, "not ready"
	}
	if h := strings.ToLower(str("status", "health")); h != "" {
		if h == "green" || h == "yellow" {
			return true, ""
		}
		return false, "health " + h
	}
	if p := str("status", "phase"); p != "" {
		switch strings.ToLower(p) {
		case "ready", "running", "active", "succeeded", "bound", "available", "healthy":
			return true, ""
		}
		return false, p
	}
	return true, ""
}

func cond(u *unstructured.Unstructured, typ string) string {
	list, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	for _, c := range list {
		m, _ := c.(map[string]any)
		if m["type"] == typ {
			s, _ := m["status"].(string)
			return s
		}
	}
	return ""
}

// ---- Uninstalling ----

// Uninstall removes a stack from a namespace: its Helm releases and the
// objects it applied, last first. Custom resource definitions stay, as
// deleting them would delete every object of their kinds. With
// deleteNamespace, the namespace goes too, with its volumes.
func (s *Stacks) Uninstall(ctx context.Context, cluster, namespace, id string, deleteNamespace bool) (string, error) {
	if err := s.kube.clusters.writable(cluster); err != nil {
		return "", err
	}
	cn, err := s.kube.clusters.conn(cluster)
	if err != nil {
		return "", err
	}
	rec, _, err := loadRecord(ctx, cn, namespace, id)
	if err != nil {
		return "", err
	}
	if rec == nil {
		return "", fmt.Errorf("namespace %s has no stack %s", namespace, id)
	}
	for _, c := range rec.Components {
		if c.Kind == "helm" {
			if _, err := helmCommand(); err != nil {
				return "", err
			}
		}
	}
	st := Stack{ID: id, Name: rec.Name}
	var steps []StackStep
	for i := len(rec.Components) - 1; i >= 0; i-- {
		c := rec.Components[i]
		steps = append(steps, StackStep{Name: c.Name, Title: c.Name, Status: "waiting"})
	}
	last := "Record of the stack"
	if deleteNamespace {
		last = "Namespace " + namespace
	}
	steps = append(steps, StackStep{Name: "record", Title: last, Status: "waiting"})
	j, jctx, err := s.newJob(cluster, namespace, st, "uninstall", steps)
	if err != nil {
		return "", err
	}
	go func() {
		s.finish(j, s.uninstall(jctx, j, cn, namespace, *rec, deleteNamespace), "", nil)
	}()
	return j.ID, nil
}

func (s *Stacks) uninstall(ctx context.Context, j *stackJob, cn *conn, ns string, rec stackRecord, deleteNamespace bool) error {
	step := 0
	for i := len(rec.Components) - 1; i >= 0; i-- {
		c := rec.Components[i]
		s.step(j, step, "running", "")
		if err := s.removeComponent(ctx, j, cn, ns, rec.Stack, c); err != nil {
			s.step(j, step, "failed", err.Error())
			return err
		}
		s.step(j, step, "done", "")
		step++
	}
	s.step(j, step, "running", "")
	var err error
	if deleteNamespace {
		s.logf(j, "Deleting namespace %s", ns)
		err = cn.cs.CoreV1().Namespaces().Delete(ctx, ns, metav1.DeleteOptions{})
	} else {
		err = cn.cs.CoreV1().ConfigMaps(ns).Delete(ctx, stackPrefix+rec.Stack, metav1.DeleteOptions{})
		if err == nil || apierrors.IsNotFound(err) {
			err = cn.cs.CoreV1().Secrets(ns).Delete(ctx, stackPrefix+rec.Stack, metav1.DeleteOptions{})
		}
	}
	if err != nil && !apierrors.IsNotFound(err) {
		s.step(j, step, "failed", err.Error())
		return err
	}
	s.step(j, step, "done", "")
	s.logf(j, "✓ %s was removed from %s", rec.Name, ns)
	return nil
}

// sharedParts are the Helm releases and objects that the other stacks
// installed in the cluster have, by the stack that has them.
func sharedParts(ctx context.Context, cn *conn, ns, id string) (map[string]string, error) {
	list, err := cn.cs.CoreV1().ConfigMaps("").List(ctx, metav1.ListOptions{LabelSelector: stackLabel})
	if err != nil {
		return nil, errors.New(friendlyError(err))
	}
	out := map[string]string{}
	for i := range list.Items {
		cm := &list.Items[i]
		r, ok := readRecord(cm)
		if !ok || (cm.Namespace == ns && r.Stack == id) {
			continue
		}
		by := fmt.Sprintf("%s in %s", r.Name, cm.Namespace)
		for _, c := range r.Components {
			cns := c.Namespace
			if cns == "" {
				cns = cm.Namespace
			}
			if c.Kind == "helm" {
				out["helm/"+cns+"/"+c.Name] = by
			}
			for _, o := range c.Objects {
				out[o.String()] = by
			}
		}
	}
	return out, nil
}

func (s *Stacks) deleteObjects(ctx context.Context, j *stackJob, cn *conn, refs []objectRef) error {
	policy := metav1.DeletePropagationBackground
	for i := len(refs) - 1; i >= 0; i-- {
		ref := refs[i]
		if ref.Kind == "CustomResourceDefinition" {
			s.logf(j, "Keeping %s: deleting it would delete every object of its kind", ref)
			continue
		}
		if ref.Kind == "Namespace" {
			s.logf(j, "Keeping %s", ref)
			continue
		}
		gv, _ := schema.ParseGroupVersion(ref.APIVersion)
		mapping, err := cn.mapper.RESTMapping(schema.GroupKind{Group: gv.Group, Kind: ref.Kind}, gv.Version)
		if err != nil {
			s.logf(j, "Skipping %s: %v", ref, err)
			continue
		}
		var r dynamic.ResourceInterface = cn.dyn.Resource(mapping.Resource)
		if ref.Namespace != "" {
			r = cn.dyn.Resource(mapping.Resource).Namespace(ref.Namespace)
		}
		err = r.Delete(ctx, ref.Name, metav1.DeleteOptions{PropagationPolicy: &policy})
		switch {
		case apierrors.IsNotFound(err):
		case err != nil:
			return fmt.Errorf("%s: %w", ref, err)
		default:
			s.logf(j, "%s deleted", strings.ToLower(ref.Kind)+"/"+ref.Name)
		}
	}
	return nil
}

// ---- Access ----

func (s *Stacks) access(cluster, namespace, id string, index int) (*conn, StackAccess, error) {
	st, err := s.find(id)
	if err != nil {
		return nil, StackAccess{}, err
	}
	if index < 0 || index >= len(st.Access) {
		return nil, StackAccess{}, errors.New("no such page")
	}
	cn, err := s.kube.clusters.conn(cluster)
	return cn, st.Access[index], err
}

// Password returns the password that signs in to a page of a stack.
func (s *Stacks) Password(ctx context.Context, cluster, namespace, id string, index int) (string, error) {
	cn, a, err := s.access(cluster, namespace, id, index)
	if err != nil {
		return "", err
	}
	name, key := stackPrefix+id, a.Password
	if a.PasswordSecret != "" {
		name, key, _ = strings.Cut(a.PasswordSecret, "/")
	}
	if key == "" {
		return "", errors.New("this page has no password")
	}
	sec, err := cn.cs.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", errors.New(friendlyError(err))
	}
	v, ok := sec.Data[key]
	if !ok {
		return "", fmt.Errorf("secret %s has no %s", name, key)
	}
	return string(v), nil
}

// Open opens a page of a stack in the browser, forwarding a local port to
// a ready pod of its service, and returns its address.
func (s *Stacks) Open(ctx context.Context, cluster, namespace, id string, index int) (string, error) {
	cn, a, err := s.access(cluster, namespace, id, index)
	if err != nil {
		return "", err
	}
	pod, port, err := servicePod(ctx, cn, namespace, a.Service, a.Port)
	if err != nil {
		return "", err
	}
	local := 0
	pfMu.Lock()
	for _, f := range forwards {
		if f.info.Cluster == cluster && f.info.Namespace == namespace && f.info.Pod == pod && f.info.RemotePort == port && f.info.Error == "" {
			local = f.info.LocalPort
		}
	}
	pfMu.Unlock()
	if local == 0 {
		pf, err := s.kube.StartPortForward(cluster, namespace, pod, port, 0)
		if err != nil {
			return "", err
		}
		local = pf.LocalPort
	}
	// Scheme and path may depend on the settings the stack has.
	st, _ := s.find(id)
	var values map[string]string
	if rec, _, err := loadRecord(ctx, cn, namespace, id); err == nil && rec != nil {
		values = rec.Values
	}
	resolved, _ := resolveValues(st, values, nil)
	data := stackData(st, namespace, resolved)
	scheme, _ := renderStackTemplate("scheme", a.Scheme, data)
	path, _ := renderStackTemplate("path", a.Path, data)
	if scheme = strings.TrimSpace(scheme); scheme == "" {
		scheme = "http"
	}
	u := fmt.Sprintf("%s://localhost:%d%s", scheme, local, strings.TrimSpace(path))
	return u, s.kube.OpenURL(u)
}

// servicePod finds a ready pod behind a service's port, and the port of
// the pod it reaches.
func servicePod(ctx context.Context, cn *conn, namespace, service string, port int) (string, int, error) {
	svc, err := cn.cs.CoreV1().Services(namespace).Get(ctx, service, metav1.GetOptions{})
	if err != nil {
		return "", 0, errors.New(friendlyError(err))
	}
	var target intstr.IntOrString
	found := false
	for _, p := range svc.Spec.Ports {
		if int(p.Port) == port {
			target, found = p.TargetPort, true
		}
	}
	if !found {
		return "", 0, fmt.Errorf("service %s has no port %d", service, port)
	}
	if len(svc.Spec.Selector) == 0 {
		return "", 0, fmt.Errorf("service %s selects no pods", service)
	}
	pods, err := cn.cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: labels.SelectorFromSet(svc.Spec.Selector).String()})
	if err != nil {
		return "", 0, errors.New(friendlyError(err))
	}
	for _, p := range pods.Items {
		if p.Status.Phase != corev1.PodRunning || p.DeletionTimestamp != nil {
			continue
		}
		ready := false
		for _, c := range p.Status.Conditions {
			if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
				ready = true
			}
		}
		if !ready {
			continue
		}
		switch {
		case target.Type == intstr.String:
			for _, c := range p.Spec.Containers {
				for _, cp := range c.Ports {
					if cp.Name == target.StrVal {
						return p.Name, int(cp.ContainerPort), nil
					}
				}
			}
		case target.IntVal != 0:
			return p.Name, int(target.IntVal), nil
		default:
			return p.Name, port, nil
		}
	}
	return "", 0, fmt.Errorf("no pod of %s is ready yet", service)
}

// ---- What the cluster offers ----

// StackClusterOptions are the choices a cluster gives the fields whose
// options come from it, and where its ingress controllers are reached.
type StackClusterOptions struct {
	IngressClasses      []string `json:"ingressClasses"`
	DefaultIngressClass string   `json:"defaultIngressClass"`
	ClusterIssuers      []string `json:"clusterIssuers"`
	StorageClasses      []string `json:"storageClasses"`
	DefaultStorageClass string   `json:"defaultStorageClass"`
	// IngressAddresses are the external addresses of the load balancers
	// of ingress controllers, where domains should point.
	IngressAddresses []IngressAddress `json:"ingressAddresses"`
	// NodeAddresses are the external addresses the nodes report, if any.
	NodeAddresses []string `json:"nodeAddresses"`
	// APIServerHost is the public address Kube Desk reaches the cluster
	// at, often that of a node when the load balancer's are private.
	APIServerHost string `json:"apiServerHost,omitempty"`
}

// IngressAddress is where an ingress controller is reached.
type IngressAddress struct {
	Service string `json:"service"`
	Address string `json:"address"`
	// Private says the address cannot be reached from the internet: one
	// of a private network, as k3s's ServiceLB gives, or an internal load
	// balancer.
	Private bool `json:"private,omitempty"`
}

// privateAddress says whether an address cannot be reached from the
// internet: private, shared (100.64.0.0/10), loopback or link-local.
func privateAddress(addr string) bool {
	ip := net.ParseIP(addr)
	if ip == nil {
		return strings.HasSuffix(addr, ".internal") || strings.HasSuffix(addr, ".local")
	}
	_, shared, _ := net.ParseCIDR("100.64.0.0/10")
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || shared.Contains(ip)
}

var ingressControllerName = regexp.MustCompile(`ingress|traefik|nginx|gateway|envoy|haproxy|contour|kong|istio`)

// ClusterOptions returns the ingress classes, certificate issuers and
// storage classes of a cluster, and the addresses of its ingress
// controllers. What cannot be read is left empty.
func (s *Stacks) ClusterOptions(ctx context.Context, cluster string) (StackClusterOptions, error) {
	out := StackClusterOptions{IngressClasses: []string{}, ClusterIssuers: []string{}, StorageClasses: []string{}, IngressAddresses: []IngressAddress{}, NodeAddresses: []string{}}
	cn, err := s.kube.clusters.conn(cluster)
	if err != nil {
		return out, err
	}
	if list, err := cn.cs.NetworkingV1().IngressClasses().List(ctx, metav1.ListOptions{}); err == nil {
		for _, c := range list.Items {
			out.IngressClasses = append(out.IngressClasses, c.Name)
			if c.Annotations["ingressclass.kubernetes.io/is-default-class"] == "true" {
				out.DefaultIngressClass = c.Name
			}
		}
	}
	if list, err := cn.cs.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{}); err == nil {
		for _, c := range list.Items {
			out.StorageClasses = append(out.StorageClasses, c.Name)
			if c.Annotations["storageclass.kubernetes.io/is-default-class"] == "true" {
				out.DefaultStorageClass = c.Name
			}
		}
	}
	issuers := schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "clusterissuers"}
	if list, err := cn.dyn.Resource(issuers).List(ctx, metav1.ListOptions{}); err == nil {
		for _, c := range list.Items {
			out.ClusterIssuers = append(out.ClusterIssuers, c.GetName())
		}
	}
	if list, err := cn.cs.CoreV1().Services("").List(ctx, metav1.ListOptions{}); err == nil {
		for _, svc := range list.Items {
			if svc.Spec.Type != corev1.ServiceTypeLoadBalancer || !ingressControllerName.MatchString(svc.Name+" "+svc.Namespace) {
				continue
			}
			for _, in := range svc.Status.LoadBalancer.Ingress {
				addr := in.IP
				if in.Hostname != "" {
					addr = in.Hostname
				}
				if addr != "" {
					out.IngressAddresses = append(out.IngressAddresses, IngressAddress{Service: svc.Namespace + "/" + svc.Name, Address: addr, Private: privateAddress(addr)})
				}
			}
		}
	}
	if nodes, err := cn.cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{}); err == nil {
		for _, n := range nodes.Items {
			for _, a := range n.Status.Addresses {
				if a.Type == corev1.NodeExternalIP && !privateAddress(a.Address) {
					out.NodeAddresses = append(out.NodeAddresses, a.Address)
				}
			}
		}
	}
	if u, err := url.Parse(cn.cfg.Host); err == nil && u.Hostname() != "" && !privateAddress(u.Hostname()) {
		out.APIServerHost = u.Hostname()
	}
	sort.Strings(out.IngressClasses)
	sort.Strings(out.ClusterIssuers)
	sort.Strings(out.StorageClasses)
	return out, nil
}
