package sandbox

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/pathsafe"
)

// Phase 4c.6 (CW-20260508-0002): TestPopulate_* tests removed. The
// adapter-claude PopulateSandbox body is now a no-op (sandbox content
// generation moved to internal/runtime/agent/bootdir_claude.Setup at
// agent.Boot time). sandbox.Populate itself has no production callers
// remaining and is queued for follow-up cleanup; the function + its
// remaining tests can drop together once the cleanup ticket lands.

func TestDir_CreatesDirectory(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	dir, err := Dir("test-session-123")
	if err != nil {
		t.Fatalf("Dir() error: %v", err)
	}

	expected := filepath.Join(tmpHome, baseDirName, "test-session-123")
	// pathsafe.ResolveUnder normalizes through filepath.EvalSymlinks, which
	// on macOS resolves /var → /private/var. Accept either form.
	if dir != expected && !strings.HasSuffix(dir, filepath.Join(baseDirName, "test-session-123")) {
		t.Errorf("expected %s (or symlink-resolved equivalent), got %s", expected, dir)
	}

	// Both the session dir and .sandbox/ subdir should exist.
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("session directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected a directory")
	}

	subDir := filepath.Join(dir, sandboxSubDir)
	info, err = os.Stat(subDir)
	if err != nil {
		t.Fatalf(".sandbox/ subdirectory not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected .sandbox/ to be a directory")
	}
}

// TestDir_RejectsTraversal verifies that a session ID containing path
// traversal segments never escapes the sandbox base dir. Before the fix,
// `filepath.Join(home, baseDirName, "../../etc")` silently collapsed to
// `/etc`, and MkdirAll happily tried to create it. The regex guard now
// refuses the value before any path resolution happens, and pathsafe
// provides the second-layer containment check.
func TestDir_RejectsTraversal(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	traversals := []string{
		"../../etc",
		"..",
		"foo/../bar",
		"../escape",
		"/absolute/escape",
		"", // empty — no default allowed
	}
	for _, sid := range traversals {
		t.Run(sid, func(t *testing.T) {
			_, err := Dir(sid)
			if err == nil {
				t.Fatalf("Dir(%q) returned no error; expected rejection", sid)
			}
		})
	}
}

// TestDir_RejectsSeatbeltInjectionChars verifies that bytes with meaning to
// sandbox-exec's TinyScheme parser (quotes, parens, semicolons,
// backslashes, control chars) are refused at the session ID gate — so they
// can never reach the profile literal on darwin.
func TestDir_RejectsSeatbeltInjectionChars(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	hostile := []string{
		`foo") (allow file-write*) ;"`,
		`a"b`,
		`a(b`,
		`a)b`,
		`a;b`,
		"a\x00b",
		"a\x1fb",
		"a b", // whitespace
	}
	for _, sid := range hostile {
		t.Run(sid, func(t *testing.T) {
			_, err := Dir(sid)
			if err == nil {
				t.Fatalf("Dir(%q) accepted hostile session id", sid)
			}
		})
	}
}

// TestDir_AcceptsValidSessionIDs confirms that legitimate slugs, UUIDs,
// and short test names pass the regex gate.
func TestDir_AcceptsValidSessionIDs(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	valid := []string{
		"sess-abc",
		"01234567-89ab-cdef-0123-456789abcdef",
		"CamelCase_123",
		"a",
	}
	for _, sid := range valid {
		if _, err := Dir(sid); err != nil {
			t.Errorf("Dir(%q) rejected valid id: %v", sid, err)
		}
	}
}

// TestDir_EscapeErrorIsTyped exercises the pathsafe containment layer. The
// regex gate blocks most escape vectors; this test injects a crafted value
// that the gate would pass but pathsafe must still catch. The current gate
// is strict enough that no such value exists in practice — the test
// instead asserts the error chain so downstream code (telemetry, logs)
// can rely on the typed error when the gate evolves.
func TestDir_EscapeErrorTypeAvailable(t *testing.T) {
	// Direct pathsafe invocation with a crafted input that escapes root.
	root := t.TempDir()
	_, err := pathsafe.ResolveUnder(root, "../escape")
	if err == nil {
		t.Fatal("pathsafe.ResolveUnder accepted a traversal")
	}
	var esc *pathsafe.EscapeError
	if !errors.As(err, &esc) {
		t.Fatalf("expected *pathsafe.EscapeError, got %T: %v", err, err)
	}
}

func TestDir_Idempotent(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	dir1, err := Dir("sess-abc")
	if err != nil {
		t.Fatalf("first Dir() error: %v", err)
	}
	dir2, err := Dir("sess-abc")
	if err != nil {
		t.Fatalf("second Dir() error: %v", err)
	}
	if dir1 != dir2 {
		t.Errorf("expected same path, got %s and %s", dir1, dir2)
	}
}
