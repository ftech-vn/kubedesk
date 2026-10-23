package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

var shellEnvOnce sync.Once

// loadShellEnv copies PATH, KUBECONFIG and the Google Cloud credentials
// settings from the user's login shell.
//
// Apps started from the Finder or the Dock get launchd's minimal PATH, so
// exec credential plugins of kubeconfigs (gke-gcloud-auth-plugin, aws,
// kubelogin...) would not be found. Running the login shell once gives the
// app the environment the user has in a terminal.
func loadShellEnv() {
	shellEnvOnce.Do(func() {
		if runtime.GOOS == "windows" {
			return
		}
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/zsh"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		const marker = "__K8S_CLIENT_ENV__"
		vars := []string{"KUBECONFIG", "GOOGLE_APPLICATION_CREDENTIALS", "CLOUDSDK_CONFIG"}
		script := `printf '` + marker + `%s` + marker + `' "$PATH"`
		for _, v := range vars {
			script += `; printf '%s` + marker + `' "$` + v + `"`
		}
		out, err := exec.CommandContext(ctx, shell, "-ilc", script).Output()
		if err == nil {
			parts := strings.Split(string(out), marker)
			if len(parts) >= len(vars)+3 {
				mergePath(parts[1])
				for i, v := range vars {
					if os.Getenv(v) == "" && parts[i+2] != "" {
						os.Setenv(v, parts[i+2])
					}
				}
			}
		}
		// Common install locations, in case the shell could not tell.
		home, _ := os.UserHomeDir()
		mergePath(strings.Join([]string{
			"/opt/homebrew/bin",
			"/usr/local/bin",
			filepath.Join(home, "google-cloud-sdk", "bin"),
			"/opt/homebrew/share/google-cloud-sdk/bin",
			"/usr/local/share/google-cloud-sdk/bin",
			filepath.Join(home, ".krew", "bin"),
			filepath.Join(home, "go", "bin"),
		}, string(os.PathListSeparator)))
	})
}

// mergePath appends the entries of extra that PATH lacks.
func mergePath(extra string) {
	current := filepath.SplitList(os.Getenv("PATH"))
	seen := map[string]bool{}
	for _, p := range current {
		seen[p] = true
	}
	var buf bytes.Buffer
	buf.WriteString(os.Getenv("PATH"))
	for _, p := range filepath.SplitList(extra) {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		if buf.Len() > 0 {
			buf.WriteRune(os.PathListSeparator)
		}
		buf.WriteString(p)
	}
	os.Setenv("PATH", buf.String())
}
