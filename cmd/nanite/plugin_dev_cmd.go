package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/hollis-labs/nanite/internal/brand"
	naniteplugin "github.com/hollis-labs/nanite/internal/plugin"
)

// apiBaseURL resolves the local nanite API base URL for dev-mode CLI
// commands. NANITE_API_URL overrides everything, else NANITE_PORT picks the
// port, else the main.go default of 8090.
func apiBaseURL() string {
	if u := os.Getenv(brand.Env("API_URL")); u != "" {
		return u
	}
	port := 8090
	if p := os.Getenv(brand.Env("PORT")); p != "" {
		if parsed, err := strconv.Atoi(p); err == nil && parsed > 0 {
			port = parsed
		}
	}
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

// apiPost sends a JSON POST to the local nanite API with optional basic-auth
// pulled from NANITE_AUTH_USER and NANITE_AUTH_PASSWORD. When unset, the server's
// basicAuthMiddleware is a no-op (dev default), so this still works.
func apiPost(path string, payload any) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, apiBaseURL()+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if user := os.Getenv(brand.Env("AUTH_USER")); user != "" {
		req.SetBasicAuth(user, os.Getenv(brand.Env("AUTH_PASSWORD")))
	}
	client := &http.Client{Timeout: 30 * time.Second}
	return client.Do(req)
}

// pluginReload hot-reloads a plugin in the running service.
func pluginReload(name string) {
	resp, err := apiPost("/api/plugins/reload", map[string]string{"name": name})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s plugin reload: could not reach %s — is the service running?\n  %v\n",
			brand.BinaryName, apiBaseURL(), err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		fmt.Fprintf(os.Stderr, "%s plugin reload: server returned %d: %s\n",
			brand.BinaryName, resp.StatusCode, string(body))
		os.Exit(1)
	}
	fmt.Printf("Plugin %q reloaded.\n", name)
}

// pluginWatch polls a plugin source directory and calls pluginReload on
// debounced change bursts. Polling over fsnotify: no extra dep, portable,
// and the watched tree is always a single plugin dir.
func pluginWatch(path string) {
	abs, err := filepath.Abs(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s plugin watch: %v\n", brand.BinaryName, err)
		os.Exit(1)
	}
	manifestPath := filepath.Join(abs, "plugin.yaml")
	manifest, err := naniteplugin.ParseManifest(manifestPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s plugin watch: %v\n", brand.BinaryName, err)
		os.Exit(1)
	}
	name := manifest.Identifier()

	fmt.Printf("Watching %s for changes; reloading %q on change. Ctrl-C to stop.\n", abs, name)

	const poll = 500 * time.Millisecond
	const debounce = 300 * time.Millisecond
	prev := snapshotTree(abs)
	var pendingAt time.Time

	for {
		time.Sleep(poll)
		cur := snapshotTree(abs)
		if !snapshotEqual(prev, cur) {
			prev = cur
			pendingAt = time.Now().Add(debounce)
			continue
		}
		if !pendingAt.IsZero() && time.Now().After(pendingAt) {
			pendingAt = time.Time{}
			fmt.Printf("[watch] change detected → reloading %q…\n", name)
			pluginReload(name)
		}
	}
}

func snapshotTree(root string) map[string]time.Time {
	out := map[string]time.Time{}
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "dist", "build", ".cache":
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		out[p] = info.ModTime()
		return nil
	})
	return out
}

func snapshotEqual(a, b map[string]time.Time) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok || !av.Equal(bv) {
			return false
		}
	}
	return true
}

// pluginRelease shells out to `make release` in the plugin directory. The
// scaffold (J.1) ships a release target; builtin/core plugins don't need
// this command.
func pluginRelease(path string) {
	abs, err := filepath.Abs(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s plugin release: %v\n", brand.BinaryName, err)
		os.Exit(1)
	}
	if _, err := os.Stat(filepath.Join(abs, "Makefile")); err != nil {
		fmt.Fprintf(os.Stderr, "%s plugin release: no Makefile in %s — scaffolded plugins include `make release`.\n",
			brand.BinaryName, abs)
		os.Exit(1)
	}
	cmd := exec.Command("make", "release")
	cmd.Dir = abs
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s plugin release: make release failed: %v\n", brand.BinaryName, err)
		os.Exit(1)
	}
}
