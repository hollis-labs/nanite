package mcp

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func tempDevTools(t *testing.T) (*DevToolsTransport, string) {
	t.Helper()
	dir := t.TempDir()
	// Resolve symlinks so isAllowed matches on macOS (/var -> /private/var).
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	dt := NewDevToolsTransport([]string{real})
	return dt, real
}

// --- dev_bash ---

func TestDevBash_Execute(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash tests require unix shell")
	}
	dt, _ := tempDevTools(t)
	result, err := dt.CallTool(context.Background(), "dev_bash", map[string]any{
		"command": "echo hello world",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, "hello world") {
		t.Errorf("expected 'hello world' in output, got: %s", result.Content[0].Text)
	}
}

func TestDevBash_CapturesStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash tests require unix shell")
	}
	dt, _ := tempDevTools(t)
	result, err := dt.CallTool(context.Background(), "dev_bash", map[string]any{
		"command": "echo err >&2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Content[0].Text, "err") {
		t.Errorf("expected stderr in output, got: %s", result.Content[0].Text)
	}
}

func TestDevBash_Timeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash tests require unix shell")
	}
	dt, _ := tempDevTools(t)
	result, err := dt.CallTool(context.Background(), "dev_bash", map[string]any{
		"command": "sleep 10",
		"timeout": float64(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error for timeout")
	}
	if !strings.Contains(result.Content[0].Text, "timed out") {
		t.Errorf("expected timeout message, got: %s", result.Content[0].Text)
	}
}

func TestDevBash_RejectsBadWorkingDir(t *testing.T) {
	dt, _ := tempDevTools(t)
	result, err := dt.CallTool(context.Background(), "dev_bash", map[string]any{
		"command":     "ls",
		"working_dir": "/etc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error for disallowed working_dir")
	}
	if !strings.Contains(result.Content[0].Text, "outside allowed") {
		t.Errorf("expected path error, got: %s", result.Content[0].Text)
	}
}

// --- dev_glob ---

func TestDevGlob_FindsFiles(t *testing.T) {
	dt, dir := tempDevTools(t)

	// Create test files.
	os.MkdirAll(filepath.Join(dir, "src"), 0o755)
	os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main"), 0o644)
	os.WriteFile(filepath.Join(dir, "src", "util.go"), []byte("package main"), 0o644)
	os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# hi"), 0o644)

	result, err := dt.CallTool(context.Background(), "dev_glob", map[string]any{
		"pattern":   "**/*.go",
		"directory": dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "main.go") || !strings.Contains(text, "util.go") {
		t.Errorf("expected go files in results, got: %s", text)
	}
	if strings.Contains(text, "readme.md") {
		t.Errorf("should not contain readme.md")
	}
}

func TestDevGlob_SortedByMtime(t *testing.T) {
	dt, dir := tempDevTools(t)

	// Create files with different mtimes.
	old := filepath.Join(dir, "old.txt")
	new := filepath.Join(dir, "new.txt")
	os.WriteFile(old, []byte("old"), 0o644)
	os.Chtimes(old, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour))
	os.WriteFile(new, []byte("new"), 0o644)

	result, err := dt.CallTool(context.Background(), "dev_glob", map[string]any{
		"pattern":   "*.txt",
		"directory": dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].Text
	newIdx := strings.Index(text, "new.txt")
	oldIdx := strings.Index(text, "old.txt")
	if newIdx < 0 || oldIdx < 0 {
		t.Fatalf("expected both files, got: %s", text)
	}
	if newIdx > oldIdx {
		t.Error("expected new.txt before old.txt (newest first)")
	}
}

func TestDevGlob_RespectsAllowedPaths(t *testing.T) {
	dt, _ := tempDevTools(t)
	result, err := dt.CallTool(context.Background(), "dev_glob", map[string]any{
		"pattern":   "*.go",
		"directory": "/etc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error for disallowed directory")
	}
}

// --- dev_edit ---

func TestDevEdit_SingleReplace(t *testing.T) {
	dt, dir := tempDevTools(t)
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("hello world\ngoodbye world\n"), 0o644)

	result, err := dt.CallTool(context.Background(), "dev_edit", map[string]any{
		"path":       path,
		"old_string": "hello world",
		"new_string": "hi world",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "hi world") {
		t.Errorf("expected replacement, got: %s", string(data))
	}
}

func TestDevEdit_ReplaceAll(t *testing.T) {
	dt, dir := tempDevTools(t)
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("foo bar foo baz foo"), 0o644)

	result, err := dt.CallTool(context.Background(), "dev_edit", map[string]any{
		"path":        path,
		"old_string":  "foo",
		"new_string":  "qux",
		"replace_all": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, "3 occurrence") {
		t.Errorf("expected 3 occurrences, got: %s", result.Content[0].Text)
	}

	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "foo") {
		t.Error("expected all foo replaced")
	}
}

func TestDevEdit_AmbiguousMatchError(t *testing.T) {
	dt, dir := tempDevTools(t)
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("foo bar foo baz"), 0o644)

	result, err := dt.CallTool(context.Background(), "dev_edit", map[string]any{
		"path":       path,
		"old_string": "foo",
		"new_string": "qux",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error for ambiguous match")
	}
	if !strings.Contains(result.Content[0].Text, "2 times") {
		t.Errorf("expected count in error, got: %s", result.Content[0].Text)
	}
}

func TestDevEdit_PathScoping(t *testing.T) {
	dt, _ := tempDevTools(t)
	result, err := dt.CallTool(context.Background(), "dev_edit", map[string]any{
		"path":       "/etc/passwd",
		"old_string": "root",
		"new_string": "toor",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error for disallowed path")
	}
}

// --- dev_read (existing tool, basic coverage) ---

func TestDevRead_Basic(t *testing.T) {
	dt, dir := tempDevTools(t)
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0o644)

	result, err := dt.CallTool(context.Background(), "dev_read", map[string]any{
		"path": path,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, "line1") {
		t.Errorf("expected file content, got: %s", result.Content[0].Text)
	}
}

// --- dev_grep (existing tool, basic coverage) ---

func TestDevGrep_Basic(t *testing.T) {
	dt, dir := tempDevTools(t)
	os.WriteFile(filepath.Join(dir, "test.go"), []byte("package main\nfunc Hello() {}\n"), 0o644)

	result, err := dt.CallTool(context.Background(), "dev_grep", map[string]any{
		"pattern":   "Hello",
		"directory": dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, "Hello") {
		t.Errorf("expected match, got: %s", result.Content[0].Text)
	}
}

// --- dev_write (existing tool, basic coverage) ---

func TestDevWrite_Basic(t *testing.T) {
	dt, dir := tempDevTools(t)
	path := filepath.Join(dir, "sub", "test.txt")

	result, err := dt.CallTool(context.Background(), "dev_write", map[string]any{
		"path":    path,
		"content": "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Errorf("expected 'hello', got: %s", string(data))
	}
}

// --- tilde expansion / canonicalization (CW-20260430-0005) ---

// TestExpandHome_Forms verifies the boundary tilde-expansion helper that
// fixes the canonicalization gap reported in c120: Go's filepath package
// treats ~ as a literal character, so a user-supplied "~/Projects" was
// passed through filepath.Abs as "/cwd/~/Projects" and tripped the escape
// check on every root.
func TestExpandHome_Forms(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("cannot determine home dir: %v", err)
	}
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "bare tilde", in: "~", want: home},
		{name: "tilde slash", in: "~/Projects", want: filepath.Join(home, "Projects")},
		{name: "tilde slash deep", in: "~/Projects-apps/nanite/coordination", want: filepath.Join(home, "Projects-apps", "nanite", "coordination")},
		{name: "no tilde absolute", in: "/etc/hosts", want: "/etc/hosts"},
		{name: "no tilde relative", in: "Projects", want: "Projects"},
		{name: "tilde-prefixed name not user", in: "~root/Projects", want: "~root/Projects"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := expandHome(tc.in)
			if got != tc.want {
				t.Fatalf("expandHome(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestResolveAllowed_TildeUserPath simulates the c120 scenario: the LLM
// passes ~/<root>/<sub> as a directory argument. Without tilde expansion,
// filepath.Abs prepends the cwd and the path appears to escape the root.
// After the fix the boundary expands ~ to the home directory before the
// allow-list check runs.
func TestResolveAllowed_TildeUserPath(t *testing.T) {
	dir := t.TempDir()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Fake "home" so we can construct ~-style paths that resolve into the
	// allowed root without polluting the running user's actual home.
	t.Setenv("HOME", real)
	// On macOS UserHomeDir reads from $HOME first, so the override is
	// enough. Re-resolve to be sure nothing cached.
	if h, _ := os.UserHomeDir(); h != real {
		t.Skipf("HOME override not honoured (got %q, want %q)", h, real)
	}

	// Allowed path includes a literal ~/sub entry; NewDevToolsTransport
	// expands it so the configured root canonicalizes to <real>/sub.
	if err := os.MkdirAll(filepath.Join(real, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "sub", "file.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	dt := NewDevToolsTransport([]string{"~/sub"})
	if len(dt.AllowedPaths) != 1 {
		t.Fatalf("expected 1 allowed path, got %d: %v", len(dt.AllowedPaths), dt.AllowedPaths)
	}
	if dt.AllowedPaths[0] != filepath.Join(real, "sub") {
		t.Fatalf("expected allowed path %q, got %q", filepath.Join(real, "sub"), dt.AllowedPaths[0])
	}

	// Now exercise the canonicalization fix: a user path with leading ~/
	// must resolve under the allowed root, not be rejected as an escape.
	resolved, err := dt.resolveAllowed("~/sub/file.txt")
	if err != nil {
		t.Fatalf("expected ~/sub/file.txt to resolve, got error: %v", err)
	}
	want := filepath.Join(real, "sub", "file.txt")
	if resolved != want {
		t.Fatalf("got %q, want %q", resolved, want)
	}

	// Bare ~ — the directory itself — must also resolve when ~ is one of
	// the allow-list roots. This is the "~/Projects escapes ~/Projects"
	// regression call-out from the ticket; before the fix Go's filepath
	// package made the equality check unreachable.
	dtRoot := NewDevToolsTransport([]string{"~/sub"})
	resolvedRoot, err := dtRoot.resolveAllowed("~/sub")
	if err != nil {
		t.Fatalf("expected ~/sub to resolve to its own root, got error: %v", err)
	}
	if resolvedRoot != filepath.Join(real, "sub") {
		t.Fatalf("got %q, want %q", resolvedRoot, filepath.Join(real, "sub"))
	}
}

// TestResolveAllowed_EscapeStillBlocked is the orthogonal regression:
// widening the allow-list and adding tilde expansion must NOT weaken the
// path-safety escape check. A path that legitimately escapes every
// configured root still has to fail with an *pathsafe.EscapeError.
func TestResolveAllowed_EscapeStillBlocked(t *testing.T) {
	dir := t.TempDir()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	dt := NewDevToolsTransport([]string{real})

	if _, err := dt.resolveAllowed("/etc/hosts"); err == nil {
		t.Fatal("expected escape error for /etc/hosts; allow-list widening must not weaken the safety check")
	}
	if _, err := dt.resolveAllowed("~/../../etc/hosts"); err == nil {
		t.Fatal("expected escape error for tilde-prefixed traversal; expansion must run BEFORE the escape check")
	}
}
