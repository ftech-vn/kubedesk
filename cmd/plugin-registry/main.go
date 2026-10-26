// Command plugin-registry keeps Kube Desk's plugin registry: the signed
// index of the plugins, and their archives, in a folder that
// scripts/publish-plugin.sh uploads to R2.
//
//	plugin-registry keygen [-o dir]
//	plugin-registry publish -out dir plugin-1.0.0.tgz...
//	plugin-registry remove -out dir plugin[@version]
//	plugin-registry list
//
// publish and remove start from the index the registry serves, check its
// signature, and write the new index and the files to upload into -out.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"k8s-client/internal/pluginindex"
)

const defaultRegistry = "https://pub-e3b2030c705649ef87d35925ea3331eb.r2.dev/plugins"

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen(os.Args[2:])
	case "publish":
		err = publish(os.Args[2:])
	case "remove":
		err = remove(os.Args[2:])
	case "list":
		err = list(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "plugin-registry:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  plugin-registry keygen [-o dir]
  plugin-registry publish [-registry url] [-key file] -out dir plugin.tgz...
  plugin-registry remove [-registry url] [-key file] -out dir plugin[@version]
  plugin-registry list [-registry url]`)
	os.Exit(2)
}

func defaultKeyFile() string {
	if k := os.Getenv("KUBEDESK_PLUGIN_REGISTRY_KEY_FILE"); k != "" {
		return k
	}
	dir, _ := os.UserConfigDir()
	return filepath.Join(dir, "mygo", "update-keys", "kubedesk-plugins.key")
}

func registryURL() string {
	if r := os.Getenv("KUBEDESK_PLUGIN_REGISTRY"); r != "" {
		return strings.TrimRight(r, "/")
	}
	return defaultRegistry
}

func keygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	out := fs.String("o", filepath.Dir(defaultKeyFile()), "folder of the keys")
	fs.Parse(args)
	keyFile := filepath.Join(*out, "kubedesk-plugins.key")
	if _, err := os.Stat(keyFile); err == nil {
		return fmt.Errorf("%s exists: a new key would make Kube Desk refuse the registry until it is updated", keyFile)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*out, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(keyFile, []byte(base64.StdEncoding.EncodeToString(priv)+"\n"), 0o600); err != nil {
		return err
	}
	pubText := base64.StdEncoding.EncodeToString(pub)
	if err := os.WriteFile(filepath.Join(*out, "kubedesk-plugins.pub"), []byte(pubText+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Printf("Secret key: %s (keep it out of the repository, and back it up)\nPublic key: %s\n", keyFile, pubText)
	return nil
}

func readKey(file string) (ed25519.PrivateKey, error) {
	if s := os.Getenv("KUBEDESK_PLUGIN_REGISTRY_KEY"); s != "" {
		return decodeKey(s)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("no registry key: %w (make one with keygen, or set KUBEDESK_PLUGIN_REGISTRY_KEY)", err)
	}
	return decodeKey(string(b))
}

func decodeKey(s string) (ed25519.PrivateKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != ed25519.PrivateKeySize {
		return nil, errors.New("the registry key is not a base64 Ed25519 private key")
	}
	return ed25519.PrivateKey(b), nil
}

// current reads the index the registry serves, checked with the key; an
// empty one when there is none yet.
func current(registry string, key ed25519.PrivateKey) (*pluginindex.Index, error) {
	res, err := http.Get(registry + "/index.json")
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		fmt.Println("The registry has no index yet: starting one")
		return &pluginindex.Index{Schema: pluginindex.Schema}, nil
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s/index.json answered %s", registry, res.Status)
	}
	doc, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if key == nil {
		return pluginindex.Unsigned(doc)
	}
	return pluginindex.Open(doc, key.Public().(ed25519.PublicKey))
}

func write(out string, files map[string][]byte) error {
	for name, b := range files {
		p := filepath.Join(out, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func publish(args []string) error {
	fs := flag.NewFlagSet("publish", flag.ExitOnError)
	registry := fs.String("registry", registryURL(), "the registry's address")
	keyFile := fs.String("key", defaultKeyFile(), "the registry's secret key")
	out := fs.String("out", "", "folder to write the index and files to upload into")
	fs.Parse(args)
	if *out == "" || fs.NArg() == 0 {
		usage()
	}
	key, err := readKey(*keyFile)
	if err != nil {
		return err
	}
	ix, err := current(*registry, key)
	if err != nil {
		return err
	}
	files := map[string][]byte{}
	for _, a := range fs.Args() {
		b, err := os.ReadFile(a)
		if err != nil {
			return err
		}
		p, err := ix.Add(b, time.Now())
		if err != nil {
			return fmt.Errorf("%s: %w", a, err)
		}
		for k, v := range p.Files {
			files[k] = v
		}
		fmt.Printf("%s %s (Kube Desk %s)\n", p.Entry.ID, p.Version.Version, p.Version.Engine)
	}
	doc, err := pluginindex.Sign(ix, key)
	if err != nil {
		return err
	}
	files["index.json"] = doc
	return write(*out, files)
}

func remove(args []string) error {
	fs := flag.NewFlagSet("remove", flag.ExitOnError)
	registry := fs.String("registry", registryURL(), "the registry's address")
	keyFile := fs.String("key", defaultKeyFile(), "the registry's secret key")
	out := fs.String("out", "", "folder to write the index into")
	fs.Parse(args)
	if *out == "" || fs.NArg() != 1 {
		usage()
	}
	key, err := readKey(*keyFile)
	if err != nil {
		return err
	}
	ix, err := current(*registry, key)
	if err != nil {
		return err
	}
	id, version := fs.Arg(0), ""
	if i := strings.LastIndex(id, "@"); i > 0 {
		id, version = id[:i], id[i+1:]
	}
	e := ix.Find(id)
	if e == nil {
		return fmt.Errorf("the registry has no %s", id)
	}
	if version == "" {
		e.Versions = nil
	} else {
		kept := e.Versions[:0]
		for _, v := range e.Versions {
			if v.Version != version {
				kept = append(kept, v)
			}
		}
		if len(kept) == len(e.Versions) {
			return fmt.Errorf("the registry has no %s %s", id, version)
		}
		e.Versions = kept
	}
	if len(e.Versions) == 0 {
		plugins := ix.Plugins[:0]
		for _, p := range ix.Plugins {
			if p != e {
				plugins = append(plugins, p)
			}
		}
		ix.Plugins = plugins
	}
	ix.Updated = time.Now().UTC().Format(time.RFC3339)
	doc, err := pluginindex.Sign(ix, key)
	if err != nil {
		return err
	}
	fmt.Printf("Removed %s from the index; its archives stay in the bucket, for installed copies\n", fs.Arg(0))
	return write(*out, map[string][]byte{"index.json": doc})
}

func list(args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	registry := fs.String("registry", registryURL(), "the registry's address")
	fs.Parse(args)
	ix, err := current(*registry, nil)
	if err != nil {
		return err
	}
	fmt.Printf("%d plugins, updated %s\n", len(ix.Plugins), ix.Updated)
	for _, e := range ix.Plugins {
		var vs []string
		for _, v := range e.Versions {
			vs = append(vs, v.Version+" ("+v.Engine+")")
		}
		fmt.Printf("  %-40s %s\n", e.ID, strings.Join(vs, ", "))
	}
	return nil
}
