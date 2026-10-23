package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"
)

// HelmRelease is the latest revision of a Helm release.
type HelmRelease struct {
	Name         string `json:"name"`
	Namespace    string `json:"namespace"`
	Revision     int    `json:"revision"`
	Status       string `json:"status"`
	Chart        string `json:"chart"`
	ChartVersion string `json:"chartVersion"`
	AppVersion   string `json:"appVersion"`
	Updated      string `json:"updated"`
	Description  string `json:"description"`
	Icon         string `json:"icon,omitempty"`
}

// HelmRevision is a revision of a release, in its history.
type HelmRevision struct {
	Revision     int    `json:"revision"`
	Status       string `json:"status"`
	ChartVersion string `json:"chartVersion"`
	AppVersion   string `json:"appVersion"`
	Updated      string `json:"updated"`
	Description  string `json:"description"`
}

// HelmReleaseDetail is a revision of a release with what it deployed.
type HelmReleaseDetail struct {
	HelmRelease
	// Values are the values the user gave, as YAML; AllValues adds the
	// chart's defaults.
	Values    string `json:"values"`
	AllValues string `json:"allValues"`
	// Manifest is what the release rendered, Secret values masked.
	Manifest string         `json:"manifest"`
	Notes    string         `json:"notes"`
	Objects  []HelmObject   `json:"objects"`
	History  []HelmRevision `json:"history"`
}

// HelmObject is an object of a release's manifest.
type HelmObject struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace"`
	Name       string `json:"name"`
}

// HelmRollbackPlan is what rolling a release back would change.
type HelmRollbackPlan struct {
	Diffs []ObjectDiff `json:"diffs"`
	// Removed are the objects of the current revision that the target
	// revision does not have, which Helm deletes.
	Removed []string `json:"removed"`
	// HelmMissing says the helm command, which rolls back, is not installed.
	HelmMissing bool `json:"helmMissing"`
}

// helmRecord is a release as Helm stores it, in a Secret or a ConfigMap.
type helmRecord struct {
	Name string `json:"name"`
	Info struct {
		LastDeployed string `json:"last_deployed"`
		Description  string `json:"description"`
		Status       string `json:"status"`
		Notes        string `json:"notes"`
	} `json:"info"`
	Chart struct {
		Metadata struct {
			Name       string `json:"name"`
			Version    string `json:"version"`
			AppVersion string `json:"appVersion"`
			Icon       string `json:"icon"`
		} `json:"metadata"`
		Values map[string]any `json:"values"`
	} `json:"chart"`
	Config    map[string]any `json:"config"`
	Manifest  string         `json:"manifest"`
	Version   int            `json:"version"`
	Namespace string         `json:"namespace"`
}

// helmStored is a stored revision: its labels, and its data until decoded.
type helmStored struct {
	name, namespace, status string
	revision                int
	modified                string
	data                    []byte
}

// Helm reads Helm releases from the cluster, as Helm stores them, without
// the helm command; rolling back uses the command when it is installed.
type Helm struct {
	kube *Kube
}

// decodeHelmRelease reads what Helm stores: base64 of gzipped JSON.
func decodeHelmRelease(data []byte) (*helmRecord, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil {
		return nil, fmt.Errorf("not a Helm release: %w", err)
	}
	if len(raw) > 2 && raw[0] == 0x1f && raw[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		if raw, err = io.ReadAll(io.LimitReader(zr, 64<<20)); err != nil {
			return nil, err
		}
	}
	var r helmRecord
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("not a Helm release: %w", err)
	}
	return &r, nil
}

// helmStore lists the stored revisions of releases in a namespace, or all
// of them with "", from Secrets and, for releases kept there, ConfigMaps.
func helmStore(ctx context.Context, cs kubernetes.Interface, namespace, release string) ([]helmStored, error) {
	sel := "owner=helm"
	if release != "" {
		sel += ",name=" + release
	}
	var out []helmStored
	add := func(m metav1.ObjectMeta, data []byte) {
		rev, err := strconv.Atoi(m.Labels["version"])
		if err != nil || m.Labels["name"] == "" {
			return
		}
		modified := m.Labels["modifiedAt"]
		if modified != "" {
			if sec, err := strconv.ParseInt(modified, 10, 64); err == nil {
				modified = time.Unix(sec, 0).UTC().Format(time.RFC3339)
			}
		} else {
			modified = m.CreationTimestamp.UTC().Format(time.RFC3339)
		}
		out = append(out, helmStored{name: m.Labels["name"], namespace: m.Namespace, status: m.Labels["status"], revision: rev, modified: modified, data: data})
	}
	secrets, err := cs.CoreV1().Secrets(namespace).List(ctx, metav1.ListOptions{LabelSelector: sel})
	if apierrors.IsForbidden(err) {
		return nil, errors.New("reading Helm releases needs access to Secrets, where Helm keeps them: " + err.Error())
	}
	if err != nil {
		return nil, errors.New(friendlyError(err))
	}
	for _, s := range secrets.Items {
		if s.Type == "helm.sh/release.v1" {
			add(s.ObjectMeta, s.Data["release"])
		}
	}
	// Releases kept in ConfigMaps are rare; reading them is best effort.
	if cms, err := cs.CoreV1().ConfigMaps(namespace).List(ctx, metav1.ListOptions{LabelSelector: sel}); err == nil {
		for _, c := range cms.Items {
			add(c.ObjectMeta, []byte(c.Data["release"]))
		}
	}
	return out, nil
}

func releaseOf(s helmStored, r *helmRecord) HelmRelease {
	h := HelmRelease{Name: s.name, Namespace: s.namespace, Revision: s.revision, Status: s.status, Updated: s.modified}
	if r != nil {
		h.Chart = r.Chart.Metadata.Name
		h.ChartVersion = r.Chart.Metadata.Version
		h.AppVersion = r.Chart.Metadata.AppVersion
		h.Icon = r.Chart.Metadata.Icon
		h.Description = r.Info.Description
		if r.Info.Status != "" {
			h.Status = r.Info.Status
		}
		if r.Info.LastDeployed != "" {
			h.Updated = r.Info.LastDeployed
		}
	}
	return h
}

// helmReleases returns the latest revision of every release.
func helmReleases(ctx context.Context, cs kubernetes.Interface, namespace string) ([]HelmRelease, error) {
	stored, err := helmStore(ctx, cs, namespace, "")
	if err != nil {
		return nil, err
	}
	latest := map[string]helmStored{}
	for _, s := range stored {
		key := s.namespace + "/" + s.name
		if cur, ok := latest[key]; !ok || s.revision > cur.revision {
			latest[key] = s
		}
	}
	out := make([]HelmRelease, 0, len(latest))
	for _, s := range latest {
		r, _ := decodeHelmRelease(s.data)
		out = append(out, releaseOf(s, r))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// helmRelease returns a revision of a release, the latest with 0, and the
// release's history.
func helmRelease(ctx context.Context, cs kubernetes.Interface, namespace, name string, revision int) (HelmReleaseDetail, error) {
	stored, err := helmStore(ctx, cs, namespace, name)
	if err != nil {
		return HelmReleaseDetail{}, err
	}
	stored = filterNamespace(stored, namespace)
	if len(stored) == 0 {
		return HelmReleaseDetail{}, fmt.Errorf("no Helm release %s in namespace %s", name, namespace)
	}
	sort.Slice(stored, func(i, j int) bool { return stored[i].revision > stored[j].revision })
	pick := stored[0]
	if revision > 0 {
		found := false
		for _, s := range stored {
			if s.revision == revision {
				pick, found = s, true
			}
		}
		if !found {
			return HelmReleaseDetail{}, fmt.Errorf("release %s has no revision %d", name, revision)
		}
	}
	var d HelmReleaseDetail
	for _, s := range stored {
		r, _ := decodeHelmRelease(s.data)
		h := releaseOf(s, r)
		d.History = append(d.History, HelmRevision{Revision: h.Revision, Status: h.Status, ChartVersion: h.ChartVersion, AppVersion: h.AppVersion, Updated: h.Updated, Description: h.Description})
		if s.revision != pick.revision {
			continue
		}
		if r == nil {
			return HelmReleaseDetail{}, fmt.Errorf("revision %d of %s cannot be read", s.revision, name)
		}
		d.HelmRelease = h
		d.Values = yamlOf(r.Config)
		d.AllValues = yamlOf(coalesceValues(r.Chart.Values, r.Config))
		d.Manifest = maskManifest(r.Manifest)
		d.Notes = r.Info.Notes
		d.Objects = manifestObjects(r.Manifest, namespace)
	}
	return d, nil
}

func filterNamespace(list []helmStored, namespace string) []helmStored {
	out := list[:0]
	for _, s := range list {
		if namespace == "" || s.namespace == namespace {
			out = append(out, s)
		}
	}
	return out
}

func yamlOf(v map[string]any) string {
	if len(v) == 0 {
		return ""
	}
	b, err := yaml.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// coalesceValues lays the user's values over the chart's defaults, as Helm
// does: maps merge, other values replace, and null removes a default.
func coalesceValues(defaults, user map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range defaults {
		out[k] = v
	}
	for k, v := range user {
		if v == nil {
			delete(out, k)
			continue
		}
		um, uok := v.(map[string]any)
		dm, dok := out[k].(map[string]any)
		if uok && dok {
			out[k] = coalesceValues(dm, um)
		} else {
			out[k] = v
		}
	}
	return out
}

// eachManifestObject calls f with each object of a multi-document YAML.
func eachManifestObject(manifest string, f func(map[string]any)) {
	dec := utilyaml.NewYAMLOrJSONDecoder(strings.NewReader(manifest), 4096)
	for {
		var o map[string]any
		if err := dec.Decode(&o); err != nil {
			return
		}
		if len(o) > 0 {
			f(o)
		}
	}
}

// manifestObjects lists the objects of a manifest; those without a
// namespace are in the release's.
func manifestObjects(manifest, namespace string) []HelmObject {
	var out []HelmObject
	eachManifestObject(manifest, func(o map[string]any) {
		u := unstructured.Unstructured{Object: o}
		ns := u.GetNamespace()
		if ns == "" {
			ns = namespace
		}
		out = append(out, HelmObject{APIVersion: u.GetAPIVersion(), Kind: u.GetKind(), Namespace: ns, Name: u.GetName()})
	})
	return out
}

// maskManifest masks the values of the Secrets of a manifest.
func maskManifest(manifest string) string {
	if !strings.Contains(manifest, "kind: Secret") {
		return manifest
	}
	var docs []string
	for _, doc := range strings.Split(manifest, "\n---") {
		if !strings.Contains(doc, "kind: Secret") {
			docs = append(docs, doc)
			continue
		}
		var o map[string]any
		if err := yaml.Unmarshal([]byte(doc), &o); err != nil || o["kind"] != "Secret" {
			docs = append(docs, doc)
			continue
		}
		maskSecret(o)
		b, _ := yaml.Marshal(o)
		// Keep the "# Source:" comment that names the template.
		head := ""
		for _, l := range strings.Split(doc, "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "# Source:") {
				head = l + "\n"
				break
			}
		}
		docs = append(docs, "\n"+head+strings.TrimRight(string(b), "\n"))
	}
	return strings.Join(docs, "\n---")
}

func (h *Helm) clientset(cluster string) (kubernetes.Interface, error) {
	cn, err := h.kube.clusters.conn(cluster)
	if err != nil {
		return nil, err
	}
	return cn.cs, nil
}

// Releases returns the Helm releases of a namespace, or of all with "".
func (h *Helm) Releases(ctx context.Context, cluster, namespace string) ([]HelmRelease, error) {
	cs, err := h.clientset(cluster)
	if err != nil {
		return nil, err
	}
	return helmReleases(ctx, cs, namespace)
}

// Release returns a revision of a release, the latest with 0, with its
// history.
func (h *Helm) Release(ctx context.Context, cluster, namespace, name string, revision int) (HelmReleaseDetail, error) {
	cs, err := h.clientset(cluster)
	if err != nil {
		return HelmReleaseDetail{}, err
	}
	return helmRelease(ctx, cs, namespace, name, revision)
}

// PlanRollback works out what rolling a release back to a revision would
// change, without changing anything.
func (h *Helm) PlanRollback(ctx context.Context, cluster, namespace, name string, revision int) (HelmRollbackPlan, error) {
	cs, err := h.clientset(cluster)
	if err != nil {
		return HelmRollbackPlan{}, err
	}
	stored, err := helmStore(ctx, cs, namespace, name)
	if err != nil {
		return HelmRollbackPlan{}, err
	}
	stored = filterNamespace(stored, namespace)
	var current, target *helmRecord
	top := 0
	for _, s := range stored {
		if s.revision == revision {
			target, _ = decodeHelmRelease(s.data)
		}
		if s.revision > top {
			top = s.revision
			current, _ = decodeHelmRelease(s.data)
		}
	}
	if target == nil {
		return HelmRollbackPlan{}, fmt.Errorf("release %s has no revision %d", name, revision)
	}
	plan := HelmRollbackPlan{Diffs: []ObjectDiff{}, Removed: []string{}}
	if _, err := helmCommand(); err != nil {
		plan.HelmMissing = true
	}
	// Helm applies what changed between the revisions' manifests, so the
	// plan compares them, without the defaults the cluster fills in.
	now := map[string]string{}
	if current != nil {
		eachRendered(current.Manifest, namespace, func(key string, _ HelmObject, y string) { now[key] = y })
	}
	keep := map[string]bool{}
	eachRendered(target.Manifest, namespace, func(key string, o HelmObject, y string) {
		keep[key] = true
		d := ObjectDiff{Kind: o.Kind, Namespace: o.Namespace, Name: o.Name, Before: now[key], After: y, Action: "update"}
		switch before, ok := now[key]; {
		case !ok:
			d.Action = "create"
		case before == y:
			d.Action = "unchanged"
		}
		plan.Diffs = append(plan.Diffs, d)
	})
	if current != nil {
		eachRendered(current.Manifest, namespace, func(key string, o HelmObject, _ string) {
			if !keep[key] {
				plan.Removed = append(plan.Removed, fmt.Sprintf("%s %s/%s", o.Kind, o.Namespace, o.Name))
			}
		})
	}
	return plan, nil
}

// eachRendered calls f with each object of a manifest, its key and its
// YAML as the plans compare it.
func eachRendered(manifest, namespace string, f func(key string, o HelmObject, yaml string)) {
	eachManifestObject(manifest, func(m map[string]any) {
		u := unstructured.Unstructured{Object: m}
		if u.GetNamespace() == "" {
			u.SetNamespace(namespace)
		}
		o := HelmObject{APIVersion: u.GetAPIVersion(), Kind: u.GetKind(), Namespace: u.GetNamespace(), Name: u.GetName()}
		f(o.Kind+"/"+o.Namespace+"/"+o.Name, o, comparableYAML(u.Object))
	})
}

// helmCommand finds the helm command, on the PATH of the login shell too.
func helmCommand() (string, error) {
	loadShellEnv()
	p, err := exec.LookPath("helm")
	if err != nil {
		return "", errors.New("rolling back needs the helm command: install it (brew install helm), then try again")
	}
	return p, nil
}

// Rollback rolls a release back to a revision with the helm command, which
// records the rollback in the release's history as Helm does.
func (h *Helm) Rollback(ctx context.Context, cluster, namespace, name string, revision int) (string, error) {
	if err := h.kube.clusters.writable(cluster); err != nil {
		return "", err
	}
	if revision <= 0 {
		return "", errors.New("choose a revision to roll back to")
	}
	helm, err := helmCommand()
	if err != nil {
		return "", err
	}
	cn, err := h.kube.clusters.conn(cluster)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, helm, "rollback", name, strconv.Itoa(revision), "--namespace", namespace, "--kube-context", cn.cluster.Context)
	// helm reads the same kubeconfig files as the cluster's connection.
	cmd.Env = append(os.Environ(), "KUBECONFIG="+strings.Join(cn.src.files, string(os.PathListSeparator)))
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return "", errors.New(text)
	}
	return text, nil
}

// helmSummary is a release for agents: its history, values with secrets
// masked, and the objects it deployed.
func helmSummary(ctx context.Context, cs kubernetes.Interface, namespace, name string) (string, error) {
	if name == "" {
		list, err := helmReleases(ctx, cs, namespace)
		if err != nil {
			return "", err
		}
		if len(list) == 0 {
			return "No Helm releases.", nil
		}
		return jsonText(list), nil
	}
	if namespace == "" {
		return "", errors.New("give the namespace of the release")
	}
	d, err := helmRelease(ctx, cs, namespace, name, 0)
	if err != nil {
		return "", err
	}
	red := newRedactor()
	var b strings.Builder
	fmt.Fprintf(&b, "Release %s/%s, revision %d, %s; chart %s %s, app %s; updated %s\n", d.Namespace, d.Name, d.Revision, d.Status, d.Chart, d.ChartVersion, d.AppVersion, d.Updated)
	b.WriteString("\nHistory:\n")
	for _, r := range d.History {
		fmt.Fprintf(&b, "- %d %s %s chart %s: %s\n", r.Revision, r.Updated, r.Status, r.ChartVersion, r.Description)
	}
	b.WriteString("\nValues given by the user:\n")
	if d.Values == "" {
		b.WriteString("(none)\n")
	} else {
		b.WriteString(red.apply(maskValues(d.Values)))
	}
	b.WriteString("\nObjects:\n")
	for _, o := range d.Objects {
		fmt.Fprintf(&b, "- %s %s/%s\n", o.Kind, o.Namespace, o.Name)
	}
	return b.String(), nil
}

// maskValues masks values whose keys look like they hold secrets.
func maskValues(values string) string {
	lines := strings.Split(values, "\n")
	for i, l := range lines {
		key, _, ok := strings.Cut(strings.TrimSpace(l), ":")
		if !ok {
			continue
		}
		k := strings.ToLower(key)
		for _, w := range []string{"password", "passwd", "secret", "token", "apikey", "api_key", "privatekey", "private_key", "credential"} {
			if strings.Contains(k, w) {
				indent := l[:len(l)-len(strings.TrimLeft(l, " "))]
				if rest := strings.TrimSpace(l[len(indent)+len(key)+1:]); rest != "" && rest != "|" && rest != ">" {
					lines[i] = indent + key + ": '••••'"
				}
				break
			}
		}
	}
	return strings.Join(lines, "\n")
}
