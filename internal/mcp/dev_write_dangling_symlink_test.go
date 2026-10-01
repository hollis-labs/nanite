package mcp

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestDevWrite_DanglingSymlinkCannotEscapeRoot pins CW-20260930-0023 at a
// real Nanite call site. dev_write resolves the agent's path through
// pathsafe.ResolveUnder and then os.WriteFile's it, which follows symlinks.
// Nanite's old internal/pathsafe returned root/link with no error when link
// was a dangling symlink to a path outside the root (its walk never Lstat'd
// the final component), so the write created the file outside the root. An
// agent that can make a symlink in its allowed root (dev_bash, ln -s) could
// write anywhere the process can. go-safefs's ResolveUnder refuses it.
func TestDevWrite_DanglingSymlinkCannotEscapeRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on windows")
	}
	dt, root := tempDevTools(t)
	outside := filepath.Join(t.TempDir(), "planted.txt") //nolint:forbidigo // test fixture under t.TempDir(); the escape is the point
	link := filepath.Join(root, "link")                  //nolint:forbidigo // test fixture under t.TempDir()
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	result, err := dt.CallTool(context.Background(), "dev_write", map[string]any{
		"path":    link,
		"content": "escaped",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Lstat(outside); !os.IsNotExist(statErr) {
		t.Fatalf("dev_write through a dangling symlink created %s outside the allowed root (stat err = %v)", outside, statErr)
	}
	if !result.IsError {
		t.Fatalf("expected dev_write through a dangling symlink to be refused, got: %s", result.Content[0].Text)
	}
}
