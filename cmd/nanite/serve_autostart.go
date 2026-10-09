package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	localdaemon "github.com/hollis-labs/libs/util/localdaemon"
	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/config"
)

// Bounds for the auto-start health check / poll loop. CW-20260813-0007.
const (
	autostartHealthCheckTimeout = 2 * time.Second
	autostartPollTimeout        = 20 * time.Second
	autostartPollInitialDelay   = 100 * time.Millisecond
)

var autostartHTTPClient = &http.Client{}

// ensureServeRunning implements CW-20260813-0007's self-bootstrapping
// client/server: `nanite chat` checks whether a `nanite serve` instance is
// already healthy at baseURL and, if not, spawns one as a detached
// background process rather than embedding its own independent runtime.
//
// This preserves the single-resident-runtime invariant session_takeover and
// GUI/CLI cross-surface coordination depend on — exactly one process owns
// the in-memory session state. A fully embedded `nanite chat` would run its
// own runtime with zero visibility into a concurrently-running `nanite
// serve` (e.g. the GUI open at the same time); auto-starting the same
// resident server instead removes only the "remember to start it" step,
// which was the actual complaint, without duplicating the runtime.
//
// Lifetime policy (item 5): the auto-started server persists after `nanite
// chat` exits, same as a manually-started `nanite serve` left running. This
// matters for durable agents, which need a live process to wake/resume in
// the background. noAutostart (--no-autostart) is the escape hatch for
// scripted/CI callers that want fail-fast behavior instead.
func ensureServeRunning(ctx context.Context, baseURL string, noAutostart bool) error {
	if healthy, _ := checkHealth(ctx, baseURL); healthy {
		return nil
	}

	if noAutostart {
		return fmt.Errorf("nanite serve is not reachable at %s and --no-autostart was given; start it manually with `nanite serve`", baseURL)
	}

	if !isLoopbackURL(baseURL) {
		// Nothing we can spawn for a remote target. Leave it to the first
		// real request to surface the usual "could not reach" error.
		return nil
	}

	port, err := portFromURL(baseURL)
	if err != nil {
		return fmt.Errorf("auto-start: %w", err)
	}

	stateDir, err := autostartStateDir()
	if err != nil {
		return fmt.Errorf("auto-start: %w", err)
	}
	lockPath := filepath.Join(stateDir, "serve.lock")
	logPath := filepath.Join(stateDir, "serve.log")

	lock, lockErr := localdaemon.TryAcquire(lockPath)
	var pid int
	switch {
	case lockErr == nil:
		defer func() {
			if releaseErr := lock.Release(); releaseErr != nil {
				fmt.Fprintf(os.Stderr, "auto-start: release lock %s: %v\n", lockPath, releaseErr)
			}
		}()
		// A previous launcher may have completed startup before we acquired the lock.
		if healthy, _ := checkHealth(ctx, baseURL); healthy {
			return nil
		}
		pid, err = spawnServe(ctx, port, logPath)
		if err != nil {
			return fmt.Errorf("auto-start: %w", err)
		}
	case errors.Is(lockErr, localdaemon.ErrAlreadyRunning):
		// Another launcher owns startup; wait for its server without spawning.
	default:
		return fmt.Errorf("auto-start: acquire lock %s: %w", lockPath, lockErr)
	}

	if err := pollHealthUntilReady(ctx, baseURL, autostartPollTimeout, pid); err != nil {
		return fmt.Errorf("auto-start: %w (log: %s)", err, logPath)
	}
	return nil
}

// checkHealth reports whether baseURL's /api/health endpoint responds 200
// within autostartHealthCheckTimeout. A non-nil error is diagnostic only —
// callers treat any failure the same as "not healthy yet".
func checkHealth(ctx context.Context, baseURL string) (bool, error) {
	reqCtx, cancel := context.WithTimeout(ctx, autostartHealthCheckTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/api/health", nil)
	if err != nil {
		return false, err
	}
	// Same NANITE_AUTH_USER/NANITE_AUTH_PASSWORD convention as
	// agentClient.newRequest — a no-op when unset, since basicAuthMiddleware
	// is also a no-op then.
	if user := os.Getenv(brand.Env("AUTH_USER")); user != "" {
		req.SetBasicAuth(user, os.Getenv(brand.Env("AUTH_PASSWORD")))
	}
	resp, err := autostartHTTPClient.Do(req)
	if err != nil {
		return false, err
	}
	defer func() {
		_ = resp.Body.Close() // Response-body close is best-effort cleanup after the request result is read.
	}()
	return resp.StatusCode == http.StatusOK, nil
}

// pollHealthUntilReady waits for the authenticated Nanite health endpoint.
// A positive pid identifies our spawned child; zero means another launcher owns it.
func pollHealthUntilReady(ctx context.Context, baseURL string, timeout time.Duration, pid int) error {
	return localdaemon.WaitReady(ctx, timeout, autostartPollInitialDelay, func(checkCtx context.Context) (bool, error) {
		if healthy, _ := checkHealth(checkCtx, baseURL); healthy {
			return true, nil
		}
		if pid > 0 && !localdaemon.IsAlive(pid) {
			return false, errors.New("nanite serve exited before becoming healthy")
		}
		return false, nil
	})
}

// spawnServe starts the resident server in its own session. It inherits Nanite's
// environment and outlives its launcher; the library reaps it in the background.
func spawnServe(ctx context.Context, port int, logPath string) (int, error) {
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, fmt.Errorf("open serve log %s: %w", logPath, err)
	}
	defer func() {
		if err := logFile.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "auto-start: close serve log %s: %v\n", logPath, err)
		}
	}()
	return localdaemon.Spawn(ctx, localdaemon.SpawnOptions{
		Args:   []string{"serve", "--port", strconv.Itoa(port)},
		Stdout: logFile, Stderr: logFile,
	})
}

// autostartStateDir resolves (and creates) the directory the auto-start
// lock file and server log live in, anchored on the go-apppaths StateDir —
// runtime state, matching coordination and worker worktree
// directories in cmdServe, not derived from the DB path.
func autostartStateDir() (string, error) {
	layout, err := config.ResolveLayout()
	if err != nil {
		return "", fmt.Errorf("resolve app layout: %w", err)
	}
	dir := filepath.Join(layout.StateDir(), "serve")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create state dir %s: %w", dir, err)
	}
	return dir, nil
}

// portFromURL extracts the TCP port nanite serve should bind, so a spawned
// server matches whatever NANITE_PORT/NANITE_API_URL/--url resolved
// baseURL to instead of always falling back to cmdServe's own 8090 default.
func portFromURL(rawURL string) (int, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return 0, fmt.Errorf("parse url %q: %w", rawURL, err)
	}
	portStr := u.Port()
	if portStr == "" {
		if u.Scheme == "https" {
			return 443, nil
		}
		return 80, nil
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return 0, fmt.Errorf("parse port in url %q: %w", rawURL, err)
	}
	return port, nil
}

// isLoopbackURL reports whether rawURL's host is loopback (127.0.0.1,
// ::1, or literal "localhost"). Auto-start only makes sense against a
// loopback target — nanite chat cannot spawn a process on a remote host.
func isLoopbackURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
