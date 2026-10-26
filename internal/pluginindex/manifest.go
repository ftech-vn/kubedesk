// Package pluginindex is what Kube Desk and the tool that publishes plugins
// share: plugins' package.json, the signed index of the plugin registry,
// and versions.
package pluginindex

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// Manifest is the package.json of a plugin.
type Manifest struct {
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Description string            `json:"description"`
	Author      json.RawMessage   `json:"author"`
	Homepage    string            `json:"homepage"`
	Keywords    []string          `json:"keywords"`
	License     string            `json:"license"`
	Engines     map[string]string `json:"engines"`
	KubeDesk    struct {
		DisplayName string `json:"displayName"`
		Main        string `json:"main"`
		// Icon is a square PNG in the package, shown in the marketplace.
		Icon string `json:"icon"`
		// Features say what the plugin adds, one line each, for the
		// marketplace.
		Features []string `json:"features"`
	} `json:"kubedesk"`
}

var npmName = regexp.MustCompile(`^(@[a-z0-9][a-z0-9._~-]*/)?[a-z0-9][a-z0-9._~-]*$`)

// ValidName reports whether name is a package name.
func ValidName(name string) bool { return len(name) <= 214 && npmName.MatchString(name) }

// ParseManifest reads and checks a plugin's package.json.
func ParseManifest(b []byte) (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("package.json: %w", err)
	}
	if !ValidName(m.Name) {
		return m, fmt.Errorf("package.json: %q is not a package name", m.Name)
	}
	if m.Version == "" {
		return m, errors.New("package.json has no version")
	}
	for _, p := range []string{m.Main(), m.KubeDesk.Icon} {
		if p != "" && !InPackage(p) {
			return m, fmt.Errorf("package.json: %q is outside the plugin", p)
		}
	}
	return m, nil
}

// InPackage reports whether a relative path stays in the package.
func InPackage(p string) bool {
	return !filepath.IsAbs(p) && !strings.HasPrefix(p, "/") && !strings.Contains(p, "\\") && !strings.HasPrefix(path.Clean(p), "..") && !strings.Contains(p, "/../")
}

// IsPlugin reports whether the package says it is a Kube Desk plugin.
func (m Manifest) IsPlugin() bool { return m.Engines["kubedesk"] != "" || m.KubeDesk.Main != "" }

// Main is the bundle Kube Desk runs: kubedesk.main, else dist/main.js, as
// Headlamp's plugins have it. The package's own main is for npm, and is
// often its source.
func (m Manifest) Main() string {
	if m.KubeDesk.Main != "" {
		return m.KubeDesk.Main
	}
	return "dist/main.js"
}

// DisplayName is the plugin's name for people.
func (m Manifest) DisplayName() string {
	if m.KubeDesk.DisplayName != "" {
		return m.KubeDesk.DisplayName
	}
	return m.Name
}

// AuthorName is the author's name, from a string or an object.
func (m Manifest) AuthorName() string {
	var s string
	if json.Unmarshal(m.Author, &s) == nil {
		// "Ann <ann@example.com> (https://ann.dev)" is Ann.
		if i := strings.IndexAny(s, "<("); i > 0 {
			s = s[:i]
		}
		return strings.TrimSpace(s)
	}
	var o struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(m.Author, &o)
	return o.Name
}

// DirName is the folder of a plugin, in a plugins folder or the registry:
// its name, with the scope's slash replaced.
func DirName(id string) string { return strings.ReplaceAll(id, "/", "+") }
