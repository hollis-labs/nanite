package shell

import (
	"context"
	"errors"
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
	_, err := (Plain{}).Run(t.Context(), Command{Script: "sleep 30 & wait", Dir: t.TempDir(), Timeout: 50 * time.Millisecond})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Fatalf("timeout returned %v after %s", err, time.Since(start))
	}
}
