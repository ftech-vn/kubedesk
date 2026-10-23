package main

import (
	"crypto/sha1"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// HealthLevel is how much a health issue matters.
type HealthLevel string

const (
	HealthBad  HealthLevel = "bad"
	HealthWarn HealthLevel = "warn"
	HealthInfo HealthLevel = "info"
)

// HealthIssue is something wrong, or about to be, with how the kubeconfig
// reaches a cluster.
type HealthIssue struct {
	Level HealthLevel `json:"level"`
	Text  string      `json:"text"`
	// Fix says what to do about it, when there is something to do.
	Fix string `json:"fix,omitempty"`
}

// ClusterHealth is the health of a cluster's kubeconfig entry.
type ClusterHealth struct {
	ID     string        `json:"id"`
	Issues []HealthIssue `json:"issues"`
	// LastSeen is when the app last reached the cluster, if ever.
	LastSeen string `json:"lastSeen,omitempty"`
}

// credentialPlugins says how to install the exec plugins people meet most.
var credentialPlugins = map[string]string{
	"gke-gcloud-auth-plugin": "gcloud components install gke-gcloud-auth-plugin",
	"kubelogin":              "brew install Azure/kubelogin/kubelogin",
	"aws":                    "brew install awscli",
	"aws-iam-authenticator":  "brew install aws-iam-authenticator",
	"doctl":                  "brew install doctl",
	"oci":                    "brew install oci-cli",
	"kubectl-oidc_login":     "kubectl krew install oidc-login",
}

// Health checks the kubeconfig entry of every cluster, without connecting:
// certificates and tokens that expired or soon will, credential plugins
// that are missing, auth providers Kubernetes removed, and clusters that are
// in the app twice.
func (c *Clusters) Health() []ClusterHealth {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.health(time.Now())
}

func (c *Clusters) health(now time.Time) []ClusterHealth {
	list, _ := c.list()
	byID := map[string]*ClusterHealth{}
	var out []*ClusterHealth
	type seen struct{ id, name, source string }
	identities := map[string][]seen{}
	for _, s := range c.sources() {
		cfg, err := s.load()
		if err != nil {
			continue
		}
		for name, kctx := range cfg.Contexts {
			id := clusterID(s.key, name)
			h := &ClusterHealth{ID: id, Issues: []HealthIssue{}, LastSeen: c.store.LastSeen[id]}
			byID[id] = h
			out = append(out, h)
			kc, ok := cfg.Clusters[kctx.Cluster]
			if !ok {
				h.Issues = append(h.Issues, HealthIssue{Level: HealthBad, Text: fmt.Sprintf("The context names the cluster %q, which the kubeconfig does not define", kctx.Cluster)})
				continue
			}
			user, ok := cfg.AuthInfos[kctx.AuthInfo]
			if !ok && kctx.AuthInfo != "" {
				h.Issues = append(h.Issues, HealthIssue{Level: HealthBad, Text: fmt.Sprintf("The context names the user %q, which the kubeconfig does not define", kctx.AuthInfo)})
			}
			h.Issues = append(h.Issues, clusterIssues(kc, now)...)
			if user != nil {
				h.Issues = append(h.Issues, userIssues(user, now)...)
			}
			// Contexts for other namespaces of a cluster are not twins.
			key := kc.Server + "\x00" + identityOf(user) + "\x00" + kctx.Namespace
			identities[key] = append(identities[key], seen{id, name, s.label()})
		}
	}
	names := map[string]string{}
	for _, cl := range list.Clusters {
		names[cl.ID] = cl.Name
	}
	for _, group := range identities {
		if len(group) < 2 {
			continue
		}
		for _, x := range group {
			var others []string
			for _, y := range group {
				if y.id != x.id {
					others = append(others, fmt.Sprintf("%s (%s)", names[y.id], y.source))
				}
			}
			byID[x.id].Issues = append(byID[x.id].Issues, HealthIssue{
				Level: HealthInfo,
				Text:  "The same cluster and user as " + strings.Join(others, ", "),
				Fix:   "Hide or remove one of them, if you do not need both",
			})
		}
	}
	res := make([]ClusterHealth, 0, len(out))
	for _, h := range out {
		sort.SliceStable(h.Issues, func(i, j int) bool { return levelRank(h.Issues[i].Level) < levelRank(h.Issues[j].Level) })
		res = append(res, *h)
	}
	sort.Slice(res, func(i, j int) bool { return res[i].ID < res[j].ID })
	return res
}

func levelRank(l HealthLevel) int {
	switch l {
	case HealthBad:
		return 0
	case HealthWarn:
		return 1
	}
	return 2
}

// identityOf tells users apart without keeping their secrets.
func identityOf(u *clientcmdapi.AuthInfo) string {
	if u == nil {
		return ""
	}
	h := sha1.New()
	add := func(parts ...string) {
		for _, p := range parts {
			h.Write([]byte(p))
			h.Write([]byte{0})
		}
	}
	add(u.Token, u.TokenFile, u.ClientCertificate, string(u.ClientCertificateData), u.Username, u.Impersonate)
	if u.Exec != nil {
		add(u.Exec.Command)
		add(u.Exec.Args...)
	}
	if u.AuthProvider != nil {
		add(u.AuthProvider.Name)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func clusterIssues(kc *clientcmdapi.Cluster, now time.Time) []HealthIssue {
	var out []HealthIssue
	if strings.HasPrefix(kc.Server, "http://") {
		out = append(out, HealthIssue{Level: HealthWarn, Text: "The API server is reached over plain HTTP, unencrypted"})
	}
	if kc.InsecureSkipTLSVerify {
		out = append(out, HealthIssue{Level: HealthWarn, Text: "The server's certificate is not checked (insecure-skip-tls-verify)", Fix: "Set the cluster's CA certificate instead"})
	}
	data, file := kc.CertificateAuthorityData, kc.CertificateAuthority
	if len(data) == 0 && file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return append(out, HealthIssue{Level: HealthBad, Text: "The CA certificate file " + filepath.Base(file) + " cannot be read", Fix: "Check that " + file + " exists"})
		}
		data = b
	}
	if len(data) > 0 {
		if is := certIssue("The cluster's CA certificate", data, now, 30); is != nil {
			out = append(out, *is)
		}
	}
	return out
}

func userIssues(u *clientcmdapi.AuthInfo, now time.Time) []HealthIssue {
	var out []HealthIssue
	data, file := u.ClientCertificateData, u.ClientCertificate
	if len(data) == 0 && file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			out = append(out, HealthIssue{Level: HealthBad, Text: "The client certificate file " + filepath.Base(file) + " cannot be read", Fix: "Check that " + file + " exists"})
		}
		data = b
	}
	if len(data) > 0 {
		if is := certIssue("Your client certificate", data, now, 14); is != nil {
			is.Fix = "Get a new kubeconfig from the cluster's administrator or provider"
			out = append(out, *is)
		}
	}
	token := u.Token
	if token == "" && u.TokenFile != "" {
		b, err := os.ReadFile(u.TokenFile)
		if err != nil {
			out = append(out, HealthIssue{Level: HealthBad, Text: "The token file " + filepath.Base(u.TokenFile) + " cannot be read", Fix: "Check that " + u.TokenFile + " exists"})
		}
		token = strings.TrimSpace(string(b))
	}
	if exp, ok := tokenExpiry(token); ok {
		switch left := exp.Sub(now); {
		case left <= 0:
			out = append(out, HealthIssue{Level: HealthBad, Text: "The token expired " + exp.Local().Format("Jan 2, 2006 15:04"), Fix: "Create a new token, and replace it in the kubeconfig"})
		case left < 7*24*time.Hour:
			out = append(out, HealthIssue{Level: HealthWarn, Text: "The token expires " + inDays(left) + " (" + exp.Local().Format("Jan 2") + ")"})
		}
	}
	if u.Exec != nil && u.Exec.Command != "" {
		cmd := u.Exec.Command
		var err error
		if filepath.IsAbs(cmd) {
			_, err = os.Stat(cmd)
		} else {
			loadShellEnv()
			_, err = exec.LookPath(cmd)
		}
		if err != nil {
			fix := "Install it, or make sure your login shell has it on its PATH"
			if how, ok := credentialPlugins[filepath.Base(cmd)]; ok {
				fix = "Install it: " + how
			}
			out = append(out, HealthIssue{Level: HealthBad, Text: "The credential plugin " + filepath.Base(cmd) + " is not installed", Fix: fix})
		}
	}
	if u.AuthProvider != nil {
		switch u.AuthProvider.Name {
		case "gcp":
			out = append(out, HealthIssue{Level: HealthBad, Text: "The gcp auth provider was removed from Kubernetes clients", Fix: "Install gke-gcloud-auth-plugin, then run gcloud container clusters get-credentials again"})
		case "azure":
			out = append(out, HealthIssue{Level: HealthBad, Text: "The azure auth provider was removed from Kubernetes clients", Fix: "Install kubelogin, then run kubelogin convert-kubeconfig"})
		}
	}
	return out
}

// certIssue reports a certificate that expired, or expires within warnDays.
func certIssue(what string, data []byte, now time.Time, warnDays int) *HealthIssue {
	block, _ := pem.Decode(data)
	if block == nil {
		return &HealthIssue{Level: HealthBad, Text: what + " is not a PEM certificate"}
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return &HealthIssue{Level: HealthBad, Text: what + " cannot be read: " + err.Error()}
	}
	left := cert.NotAfter.Sub(now)
	switch {
	case left <= 0:
		return &HealthIssue{Level: HealthBad, Text: what + " expired " + cert.NotAfter.Local().Format("Jan 2, 2006")}
	case left < time.Duration(warnDays)*24*time.Hour:
		return &HealthIssue{Level: HealthWarn, Text: what + " expires " + inDays(left) + " (" + cert.NotAfter.Local().Format("Jan 2, 2006") + ")"}
	}
	return nil
}

func inDays(d time.Duration) string {
	days := int(d.Hours() / 24)
	switch {
	case days == 0:
		return "today"
	case days == 1:
		return "tomorrow"
	}
	return fmt.Sprintf("in %d days", days)
}

// tokenExpiry reads the expiry of a JWT, such as a service account token.
func tokenExpiry(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp float64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Exp == 0 {
		return time.Time{}, false
	}
	return time.Unix(int64(claims.Exp), 0), true
}
