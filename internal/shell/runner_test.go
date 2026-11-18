package shell

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPlainRunnerEnvironmentAndOutput(t *testing.T) {
	t.Setenv("NANITE_TEST_PARENT_PRIVATE", "parent-value")
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result, err := (Plain{}).Run(t.Context(), Command{
		Dir: dir, Timeout: time.Second, Env: map[string]string{"NANITE_TEST_DECLARED": "declared-value"},
		Script: "printf '%s\\n' \"$PWD\" \"$NANITE_TEST_DECLARED\" \"${NANITE_TEST_PARENT_PRIVATE-unset}\"; printf '%70000s' x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result.Output, dir+"\ndeclared-value\nunset\n") || !strings.HasSuffix(result.Output, "[output truncated]") {
		t.Fatalf("unexpected output length %d", len(result.Output))
	}
}

func TestPlainRunnerTimeout(t *testing.T) {
	start := time.Now()
	_, err := (Plain{}).Run(t.Context(), Command{Script: "trap '' TERM; sleep 30 & wait", Dir: t.TempDir(), Timeout: 50 * time.Millisecond})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Fatalf("timeout returned %v after %s", err, time.Since(start))
	}
}

func TestPlainRunnerGivesTerminationHandlerTime(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	dir := t.TempDir()
	cleaned := filepath.Join(dir, "cleaned")
	done := make(chan error, 1)
	go func() {
		_, err := (Plain{}).Run(ctx, Command{Dir: dir, Timeout: 5 * time.Second,
			Script: `trap 'printf cleaned > cleaned; kill -KILL "$child"; wait "$child" 2>/dev/null; exit 0' TERM
(trap '' TERM; printf ready > child_ready; exec sleep 30) &
child=$!
printf ready > ready
wait "$child"`,
		})
		done <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, parentReady := os.Stat(filepath.Join(dir, "ready"))
		_, childReady := os.Stat(filepath.Join(dir, "child_ready"))
		if parentReady == nil && childReady == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("shell did not install its handler")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shell stayed active after cancellation")
	}
	if content, err := os.ReadFile(cleaned); err != nil || string(content) != "cleaned" {
		t.Fatalf("cleanup marker = %q, %v", content, err)
	}
}
