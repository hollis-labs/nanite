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
