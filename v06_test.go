package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// helmSecret stores a release the way Helm 3 does.
func helmSecret(t *testing.T, ns, name string, rev int, status string, rel map[string]any) *corev1.Secret {
	t.Helper()
	b, _ := json.Marshal(rel)
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write(b)
	zw.Close()
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("sh.helm.release.v1.%s.v%d", name, rev), Namespace: ns,
			Labels: map[string]string{"owner": "helm", "name": name, "version": fmt.Sprint(rev), "status": status, "modifiedAt": "1761200000"},
		},
		Type: "helm.sh/release.v1",
		Data: map[string][]byte{"release": []byte(base64.StdEncoding.EncodeToString(gz.Bytes()))},
	}
}

func shopRelease(rev int, chartVersion, image string, extra string) map[string]any {
	manifest := `---
# Source: shop/templates/deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: shop
spec:
  replicas: 2
  template:
    spec:
      containers:
      - name: shop
        image: ` + image + `
---
# Source: shop/templates/secret.yaml
apiVersion: v1
kind: Secret
metadata:
  name: shop-db
stringData:
  password: hunter2
` + extra
	return map[string]any{
		"name": "shop", "namespace": "apps", "version": rev, "manifest": manifest,
		"info":   map[string]any{"status": "deployed", "description": "Upgrade complete", "last_deployed": "2026-10-20T10:00:00Z", "notes": "Visit http://shop"},
		"chart":  map[string]any{"metadata": map[string]any{"name": "shop", "version": chartVersion, "appVersion": "1." + fmt.Sprint(rev)}, "values": map[string]any{"replicas": 1, "db": map[string]any{"host": "db", "port": 5432}, "debug": true}},
		"config": map[string]any{"db": map[string]any{"host": "prod-db", "password": "hunter2"}, "debug": nil},
	}
}

func TestHelmReleases(t *testing.T) {
	cs := fake.NewSimpleClientset(
		helmSecret(t, "apps", "shop", 1, "superseded", shopRelease(1, "0.1.0", "shop:1", "---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: shop-old\n")),
		helmSecret(t, "apps", "shop", 2, "deployed", shopRelease(2, "0.2.0", "shop:2", "")),
		helmSecret(t, "infra", "ingress", 1, "failed", map[string]any{"name": "ingress", "version": 1, "info": map[string]any{"status": "failed"}, "chart": map[string]any{"metadata": map[string]any{"name": "ingress-nginx", "version": "4.0.0"}}}),
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "apps", Labels: map[string]string{"owner": "helm"}}},
	)
	ctx := context.Background()
	list, err := helmReleases(ctx, cs, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "shop" || list[0].Revision != 2 || list[0].ChartVersion != "0.2.0" || list[0].Status != "deployed" || list[1].Status != "failed" {
		t.Fatalf("releases: %+v", list)
	}

	d, err := helmRelease(ctx, cs, "apps", "shop", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.History) != 2 || d.History[0].Revision != 2 || d.History[1].ChartVersion != "0.1.0" {
		t.Errorf("history: %+v", d.History)
	}
	// The user's values over the chart's: maps merge, null removes.
	if !strings.Contains(d.AllValues, "host: prod-db") || !strings.Contains(d.AllValues, "port: 5432") || strings.Contains(d.AllValues, "debug") || !strings.Contains(d.AllValues, "replicas: 1") {
		t.Errorf("all values:\n%s", d.AllValues)
	}
	if strings.Contains(d.Manifest, "hunter2") || !strings.Contains(d.Manifest, "# Source: shop/templates/secret.yaml") || !strings.Contains(d.Manifest, "image: shop:2") {
		t.Errorf("manifest:\n%s", d.Manifest)
	}
	if len(d.Objects) != 2 || d.Objects[0].Kind != "Deployment" || d.Objects[0].Namespace != "apps" {
		t.Errorf("objects: %+v", d.Objects)
	}
	old, err := helmRelease(ctx, cs, "apps", "shop", 1)
	if err != nil || !strings.Contains(old.Manifest, "image: shop:1") || old.Revision != 1 {
		t.Errorf("revision 1: %v %q", err, old.Manifest)
	}
	if _, err := helmRelease(ctx, cs, "apps", "shop", 9); err == nil {
		t.Error("a missing revision was found")
	}

	// What agents see masks the secrets of the values too.
	sum, err := helmSummary(ctx, cs, "apps", "shop")
	if err != nil || strings.Contains(sum, "hunter2") || !strings.Contains(sum, "host: prod-db") || !strings.Contains(sum, "Deployment apps/shop") {
		t.Errorf("summary (%v):\n%s", err, sum)
	}
}

func TestHelmForbidden(t *testing.T) {
	cs := fake.NewSimpleClientset()
	cs.PrependReactor("list", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("secrets is forbidden")
	})
	if _, err := helmReleases(context.Background(), cs, "apps"); err == nil {
		t.Fatal("no error")
	}
}

func TestAccessCache(t *testing.T) {
	cs := fake.NewSimpleClientset()
	calls := 0
	cs.PrependReactor("create", "selfsubjectaccessreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
		calls++
		r := a.(k8stesting.CreateAction).GetObject().(*authorizationv1.SelfSubjectAccessReview)
		attr := r.Spec.ResourceAttributes
		r.Status.Allowed = attr.Verb == "get" || (attr.Verb == "delete" && attr.Namespace == "dev")
		if attr.Resource == "broken" {
			return true, nil, fmt.Errorf("the server could not answer")
		}
		return true, r, nil
	})
	a := &accessCache{answers: map[string]map[AccessCheck]accessAnswer{}}
	checks := []AccessCheck{
		{Verb: "get", Resource: "pods", Namespace: "dev"},
		{Verb: "delete", Resource: "pods", Namespace: "dev"},
		{Verb: "delete", Resource: "pods", Namespace: "prod"},
		{Verb: "create", Resource: "pods", Subresource: "exec", Namespace: "prod"},
		{Verb: "delete", Resource: "broken"},
	}
	now := time.Now()
	got := a.can(context.Background(), "c", cs, checks, now)
	want := []bool{true, true, false, false, true}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	// Answers are kept, but the one the cluster could not give.
	a.can(context.Background(), "c", cs, checks, now.Add(time.Minute))
	if calls != 6 {
		t.Errorf("%d reviews, want 6", calls)
	}
	a.can(context.Background(), "c", cs, checks, now.Add(3*time.Minute))
	if calls != 11 {
		t.Errorf("%d reviews after they aged, want 11", calls)
	}
}

func testCert(t *testing.T, notAfter time.Time) string {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "me"}, NotBefore: notAfter.Add(-365 * 24 * time.Hour), NotAfter: notAfter}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func testJWT(exp time.Time) string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"none"}`)) + "." + enc([]byte(fmt.Sprintf(`{"sub":"me","exp":%d}`, exp.Unix()))) + ".sig"
}

func TestKubeconfigHealth(t *testing.T) {
	c := newTestClusters(t)
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	kubeconfig := `apiVersion: v1
kind: Config
clusters:
- name: a
  cluster: {server: "https://a:6443", certificate-authority-data: ` + testCert(t, now.Add(10*24*time.Hour)) + `}
- name: b
  cluster: {server: "http://b:8080"}
contexts:
- {name: expired-cert, context: {cluster: a, user: cert}}
- {name: token-soon, context: {cluster: a, user: token}}
- {name: plugin, context: {cluster: b, user: plugin}}
- {name: gcp, context: {cluster: b, user: gcp}}
- {name: ok, context: {cluster: a, user: token, namespace: apps}}
users:
- name: cert
  user: {client-certificate-data: ` + testCert(t, now.Add(-time.Hour)) + `, client-key-data: eA==}
- name: token
  user: {token: ` + testJWT(now.Add(3*24*time.Hour)) + `}
- name: plugin
  user:
    exec: {apiVersion: client.authentication.k8s.io/v1beta1, command: gke-gcloud-auth-plugin-missing-xyz}
- name: gcp
  user:
    auth-provider: {name: gcp}
`
	path := filepath.Join(t.TempDir(), "team.yaml")
	if err := os.WriteFile(path, []byte(kubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddFile(path); err != nil {
		t.Fatal(err)
	}
	// The same file pasted into the app makes twins.
	if _, err := c.AddKubeconfig("copy", kubeconfig); err != nil {
		t.Fatal(err)
	}
	// A context edited, later, to name a cluster that is not there.
	broken := strings.Replace(kubeconfig, "contexts:\n", "contexts:\n- {name: ghost, context: {cluster: nowhere, user: cert}}\n", 1)
	if err := os.WriteFile(path, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	byName := map[string][]HealthIssue{}
	for _, h := range c.health(now) {
		for _, cl := range c.List().Clusters {
			if cl.ID == h.ID && cl.Source == path {
				byName[cl.Context] = h.Issues
			}
		}
	}
	has := func(ctx string, level HealthLevel, text string) {
		t.Helper()
		for _, is := range byName[ctx] {
			if is.Level == level && strings.Contains(is.Text, text) {
				return
			}
		}
		t.Errorf("%s: no %s issue with %q in %+v", ctx, level, text, byName[ctx])
	}
	has("expired-cert", HealthBad, "client certificate expired")
	has("expired-cert", HealthWarn, "CA certificate expires in 10 days")
	has("token-soon", HealthWarn, "token expires in 3 days")
	has("plugin", HealthBad, "gke-gcloud-auth-plugin-missing-xyz is not installed")
	has("plugin", HealthWarn, "plain HTTP")
	has("gcp", HealthBad, "gcp auth provider was removed")
	has("ghost", HealthBad, `cluster "nowhere"`)
	has("ok", HealthInfo, "same cluster and user as")
	for _, is := range byName["token-soon"] {
		if is.Level == HealthInfo && strings.Contains(is.Text, "ok (") {
			t.Errorf("contexts for other namespaces are taken as twins: %+v", is)
		}
	}
}
