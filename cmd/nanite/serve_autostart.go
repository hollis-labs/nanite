package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/config"
)

// Bounds for the auto-start health check / poll loop. CW-20260813-0007.
const (
	autostartHealthCheckTimeout = 2 * time.Second
	autostartPollTimeout        = 20 * time.Second
	autostartPollInitialDelay   = 100 * time.Millisecond
	autostartPollMaxDelay       = 1 * time.Second
)

var autostartHTTPClient = &http.Client{}

// errAutoStartUnsupported is returned by lockExclusiveNonBlocking on
// platforms where nanite chat's auto-start mechanics (flock + Setsid
// detachment) have no equivalent. CW-20260813-0007 item 7 scopes this
// feature to Unix/macOS/Linux; Windows support is a separate ticket.
var errAutoStartUnsupported = errors.New("auto-starting `nanite serve` is not supported on this platform")

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

	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("auto-start: open lock file %s: %w", lockPath, err)
	}
	defer lockFile.Close()

	fd := int(lockFile.Fd())
	var exitCh chan error
	if lockErr := lockExclusiveNonBlocking(fd); lockErr != nil {
		if errors.Is(lockErr, errAutoStartUnsupported) {
			return fmt.Errorf("nanite serve is not reachable at %s: %w", baseURL, lockErr)
		}
		// Another `nanite chat` invocation holds the lock and is already
		// spawning (or just finished spawning) the server. Don't race it —
		// wait and re-check health below instead of spawning our own.
	} else {
		defer unlockFile(fd)

		// Re-check health now that we hold the lock: the previous holder
		// may have already finished starting the server while we waited.
		if healthy, _ := checkHealth(ctx, baseURL); healthy {
			return nil
		}
		exitCh, err = spawnServe(port, logPath)
		if err != nil {
			return fmt.Errorf("auto-start: %w", err)
		}
	}

	if err := pollHealthUntilReady(ctx, baseURL, autostartPollTimeout, exitCh); err != nil {
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
	// harnessClient.newRequest — a no-op when unset, since basicAuthMiddleware
	// is also a no-op then.
	if user := os.Getenv(brand.Env("AUTH_USER")); user != "" {
		req.SetBasicAuth(user, os.Getenv(brand.Env("AUTH_PASSWORD")))
	}
	resp, err := autostartHTTPClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK, nil
}

// pollHealthUntilReady polls baseURL's health endpoint with bounded
// exponential backoff until it reports healthy, exitCh fires (a spawned
// child died before becoming healthy — nil exitCh simply never fires), or
// timeout elapses.
//
// This is the same poll-until-ready mechanism CW-20260813-0008 (retry/
// backoff for harness-v1 connection establishment) needs for its own
// connection-retry loop. Share this rather than writing a second one if
// that ticket lands close to this one.
func pollHealthUntilReady(ctx context.Context, baseURL string, timeout time.Duration, exitCh <-chan error) error {
	deadline := time.Now().Add(timeout)
	delay := autostartPollInitialDelay
	for {
		if healthy, _ := checkHealth(ctx, baseURL); healthy {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("timed out after %s waiting for nanite serve to become healthy at %s", timeout, baseURL)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-exitCh:
			// exitCh is nil when there is no spawned child to watch (the
			// "loser" of the spawn race) — a nil-channel receive never
			// fires, so this case is simply inert in that path.
			if err != nil {
				return fmt.Errorf("nanite serve exited before becoming healthy: %w", err)
			}
			return errors.New("nanite serve exited before becoming healthy")
		case <-time.After(delay):
		}
		delay *= 2
		if delay > autostartPollMaxDelay {
			delay = autostartPollMaxDelay
		}
	}
}

// spawnServe re-execs the running nanite binary as `nanite serve --port
// <port>`, detached from this process's controlling terminal (item 2/7):
// stdout/stderr redirect to logPath instead of being inherited, and the
// platform-specific detachedProcAttr (Setsid on unix) puts it in its own
// session so it survives this process exiting or its parent terminal
// closing. The child inherits the environment, so NANITE_DB_PATH /
// NANITE_WORKSPACE overrides carry through unchanged.
//
// Returns a channel that receives the child's exit result exactly once,
// used by pollHealthUntilReady to fail fast (item 6) instead of waiting out
// the full timeout when the spawn failed outright (e.g. the port is held by
// something unhealthy).
func spawnServe(port int, logPath string) (chan error, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve nanite binary path: %w", err)
	}

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open serve log %s: %w", logPath, err)
	}

	cmd := exec.Command(exe, "serve", "--port", strconv.Itoa(port))
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = detachedProcAttr()

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return nil, fmt.Errorf("start nanite serve (%s): %w", exe, err)
	}

	exitCh := make(chan error, 1)
	go func() {
		defer logFile.Close()
		exitCh <- cmd.Wait()
	}()
	return exitCh, nil
}

// autostartStateDir resolves (and creates) the directory the auto-start
// lock file and server log live in, anchored on the go-apppaths StateDir —
// runtime state, matching internal/coordination and internal/worktree's
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
