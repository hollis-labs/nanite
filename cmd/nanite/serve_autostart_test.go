package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestPortFromURL(t *testing.T) {
	cases := []struct {
		url  string
		want int
	}{
		{"http://127.0.0.1:8090", 8090},
		{"http://127.0.0.1:8090/", 8090},
		{"http://localhost:9000", 9000},
		{"http://127.0.0.1", 80},
		{"https://127.0.0.1", 443},
	}
	for _, tc := range cases {
		got, err := portFromURL(tc.url)
		if err != nil {
			t.Fatalf("portFromURL(%q): %v", tc.url, err)
		}
		if got != tc.want {
			t.Errorf("portFromURL(%q) = %d, want %d", tc.url, got, tc.want)
		}
	}
}

func TestIsLoopbackURL(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"http://127.0.0.1:8090", true},
		{"http://localhost:8090", true},
		{"http://[::1]:8090", true},
		{"http://example.com:8090", false},
		{"http://10.0.0.5:8090", false},
	}
	for _, tc := range cases {
		if got := isLoopbackURL(tc.url); got != tc.want {
			t.Errorf("isLoopbackURL(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}

func TestCheckHealth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/health" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	healthy, err := checkHealth(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("checkHealth: %v", err)
	}
	if !healthy {
		t.Fatal("checkHealth = false, want true")
	}
}

func TestCheckHealth_Unreachable(t *testing.T) {
	healthy, err := checkHealth(context.Background(), "http://127.0.0.1:1")
	if healthy {
		t.Fatal("checkHealth = true for an unreachable port, want false")
	}
	if err == nil {
		t.Fatal("checkHealth: want a non-nil diagnostic error for an unreachable port")
	}
}

// TestPollHealthUntilReady_BecomesHealthy pins the common case: the server
// isn't healthy yet on the first check but becomes healthy shortly after —
// the poll loop must not give up early or wait the full timeout.
func TestPollHealthUntilReady_BecomesHealthy(t *testing.T) {
	var healthyAfter int32 = 2
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < healthyAfter {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	start := time.Now()
	err := pollHealthUntilReady(context.Background(), srv.URL, 5*time.Second, nil)
	if err != nil {
		t.Fatalf("pollHealthUntilReady: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("pollHealthUntilReady took %s, want well under the 5s timeout", elapsed)
	}
}

// TestPollHealthUntilReady_Timeout pins the bounded-timeout requirement
// (item 4): a server that never becomes healthy must produce a clear error,
// not hang indefinitely.
func TestPollHealthUntilReady_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	err := pollHealthUntilReady(context.Background(), srv.URL, 300*time.Millisecond, nil)
	if err == nil {
		t.Fatal("pollHealthUntilReady: want a timeout error, got nil")
	}
}

// TestPollHealthUntilReady_ExitChFailsFast pins item 6: if the spawned
// child dies before becoming healthy, the poll loop must surface that
// immediately instead of waiting out the full timeout.
func TestPollHealthUntilReady_ExitChFailsFast(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	exitCh := make(chan error, 1)
	exitCh <- errors.New("exit status 1")

	start := time.Now()
	err := pollHealthUntilReady(context.Background(), srv.URL, 30*time.Second, exitCh)
	if err == nil {
		t.Fatal("pollHealthUntilReady: want an error when the child exited, got nil")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("pollHealthUntilReady took %s to notice the exited child, want well under the 30s timeout", elapsed)
	}
}

// TestEnsureServeRunning_AlreadyHealthy pins the fast path: a healthy
// server means ensureServeRunning does nothing (no spawn attempt).
func TestEnsureServeRunning_AlreadyHealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := ensureServeRunning(context.Background(), srv.URL, false); err != nil {
		t.Fatalf("ensureServeRunning: %v", err)
	}
}

// TestEnsureServeRunning_NoAutostart pins the --no-autostart escape hatch:
// an unhealthy/unreachable target must fail fast with a clear error instead
// of attempting to spawn anything.
func TestEnsureServeRunning_NoAutostart(t *testing.T) {
	err := ensureServeRunning(context.Background(), "http://127.0.0.1:1", true)
	if err == nil {
		t.Fatal("ensureServeRunning: want an error with --no-autostart against an unreachable server")
	}
}

// TestEnsureServeRunning_RemoteTargetSkipsAutostart pins the loopback-only
// scope: a non-loopback baseURL must not attempt to spawn a local process.
func TestEnsureServeRunning_RemoteTargetSkipsAutostart(t *testing.T) {
	if err := ensureServeRunning(context.Background(), "http://example.invalid:8090", false); err != nil {
		t.Fatalf("ensureServeRunning: want nil (defers to the normal connection-error path) for a remote target, got %v", err)
	}
}
