//go:build devmode

package muxproxy

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	agentmux "github.com/hollis-labs/go-agentmux-client"
)

// daemonEndpoint is hardcoded for the POC. Any consumer promoting this
// package beyond throwaway must replace with config-schema/env-var.
const daemonEndpoint = "unix:~/.agent-mux/run/muxd.sock"

// launchTimeout bounds individual HTTP calls to muxd. go-agentmux-client
// v0.1.0 defaults to 5s which is too tight for LaunchSession — spinning
// up the claude CLI subprocess routinely exceeds that. Set long enough
// to cover realistic subprocess startup on a cold machine.
const launchTimeout = 2 * time.Minute

// orchestratorProfileSlug is the agent-profile slug that activates
// the mux_* tool allowlist. Kept constant here for revert-friendliness.
const orchestratorProfileSlug = "mux-orchestrator" //nolint:unused // referenced by later task

var (
	clientOnce sync.Once
	clientVal  *agentmux.Client
)

// Client returns the singleton agent-mux daemon client. First call
// constructs the client; subsequent calls return the cached value.
// Uses a custom http.Client with an extended timeout because the
// stock client's 5s default is too short for real-world claudestream
// subprocess startup.
func Client() *agentmux.Client {
	clientOnce.Do(func() {
		socketPath := expandSocketPath(daemonEndpoint)
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		}
		httpClient := &http.Client{Transport: transport, Timeout: launchTimeout}
		clientVal = agentmux.MustNew(daemonEndpoint, agentmux.WithHTTPClient(httpClient))
	})
	return clientVal
}

// expandSocketPath strips the "unix:" prefix and resolves a leading
// "~/" against the current user's home directory.
func expandSocketPath(listenAddr string) string {
	path := strings.TrimPrefix(listenAddr, "unix:")
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}
