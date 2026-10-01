//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	localdaemon "github.com/hollis-labs/go-localdaemon"
)

func TestPollHealthUntilReady_DeadChildFailsFast(t *testing.T) {
	// Observe only a child this test started and reaped, never a system PID.
	child := exec.Command("sh", "-c", "exit 3")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	pid := child.Process.Pid
	if err := child.Wait(); err == nil {
		t.Fatal("expected the helper's failing exit")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	started := time.Now()
	err := pollHealthUntilReady(context.Background(), srv.URL, 30*time.Second, pid)
	if err == nil || !strings.Contains(err.Error(), "exited before becoming healthy") || time.Since(started) > 2*time.Second {
		t.Fatalf("child exit was not reported promptly: %v", err)
	}
}

func TestEnsureServeRunning_HeldLauncherWaitsWithoutSpawning(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir, err := autostartStateDir()
	if err != nil {
		t.Fatal(err)
	}
	lock, err := localdaemon.TryAcquire(filepath.Join(dir, "serve.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Release() }()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()
	if err := ensureServeRunning(context.Background(), srv.URL, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "serve.log")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the competing launcher attempted to spawn: %v", err)
	}
	if _, err := localdaemon.TryAcquire(lock.Path()); !errors.Is(err, localdaemon.ErrAlreadyRunning) {
		t.Fatalf("competing launcher released the owner's lock: %v", err)
	}
}

func TestEnsureServeRunning_LockIOErrorFailsBeforeSpawn(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir, err := autostartStateDir()
	if err != nil {
		t.Fatal(err)
	}
	// A directory at the lock path causes an I/O error, not lock contention.
	if err := os.Mkdir(filepath.Join(dir, "serve.lock"), 0o700); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	if err := ensureServeRunning(context.Background(), srv.URL, false); err == nil || !strings.Contains(err.Error(), "acquire lock") {
		t.Fatalf("lock I/O error was mistaken for another launcher: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "serve.log")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("spawn followed a failed lock acquisition: %v", err)
	}
}

func TestSpawnServe_CanceledContextDoesNotStartChild(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if pid, err := spawnServe(ctx, 8090, filepath.Join(t.TempDir(), "serve.log")); !errors.Is(err, context.Canceled) || pid != 0 {
		t.Fatalf("canceled spawn: pid=%d, err=%v", pid, err)
	}
}

func TestPollHealthUntilReady_BoundsSlowHealthCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	started := time.Now()
	err := pollHealthUntilReady(context.Background(), srv.URL, 100*time.Millisecond, 0)
	if !errors.Is(err, localdaemon.ErrNotReady) || time.Since(started) > time.Second {
		t.Fatalf("readiness deadline did not bound health check: %v", err)
	}
}
