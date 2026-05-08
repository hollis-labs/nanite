package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Test_workspaceCreate_materializes_layout verifies the prompts/state/logs
// triplet is created beneath WorkspacesRoot/<sessID>.
func Test_workspaceCreate_materializes_layout(t *testing.T) {
	tmp := t.TempDir()
	ws, err := workspaceCreate(tmp, "sess-abc", Options{})
	if err != nil {
		t.Fatalf("workspaceCreate: %v", err)
	}

	if want := filepath.Join(tmp, "sess-abc"); ws.Root != want {
		t.Errorf("Root = %q, want %q", ws.Root, want)
	}
	if !strings.HasSuffix(ws.PromptsDir, "/sess-abc/prompts") {
		t.Errorf("PromptsDir = %q, want suffix /sess-abc/prompts", ws.PromptsDir)
	}
	if !strings.HasSuffix(ws.StateDir, "/sess-abc/state") {
		t.Errorf("StateDir = %q, want suffix /sess-abc/state", ws.StateDir)
	}
	if !strings.HasSuffix(ws.LogDir, "/sess-abc/logs") {
		t.Errorf("LogDir = %q, want suffix /sess-abc/logs", ws.LogDir)
	}
	if !strings.HasSuffix(ws.LogPath, "/sess-abc/logs/session.log") {
		t.Errorf("LogPath = %q, want suffix /sess-abc/logs/session.log", ws.LogPath)
	}

	for _, dir := range []string{ws.Root, ws.PromptsDir, ws.StateDir, ws.LogDir} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Errorf("stat %s: %v", dir, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", dir)
		}
	}
}

// Test_workspaceCreate_idempotent verifies re-creating an existing workspace
// is a no-op (so resume flows can be reentrant).
func Test_workspaceCreate_idempotent(t *testing.T) {
	tmp := t.TempDir()
	if _, err := workspaceCreate(tmp, "sess-abc", Options{}); err != nil {
		t.Fatalf("first call: %v", err)
	}
	// Plant a file so we can verify it survives.
	canary := filepath.Join(tmp, "sess-abc", "logs", "canary.txt")
	if err := os.WriteFile(canary, []byte("present"), 0o644); err != nil {
		t.Fatalf("write canary: %v", err)
	}

	if _, err := workspaceCreate(tmp, "sess-abc", Options{}); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if data, err := os.ReadFile(canary); err != nil || string(data) != "present" {
		t.Errorf("canary lost on second create: data=%q err=%v", string(data), err)
	}
}

// Test_workspaceCreate_validates_inputs rejects empty root or sessionID.
func Test_workspaceCreate_validates_inputs(t *testing.T) {
	if _, err := workspaceCreate("", "sess-abc", Options{}); err == nil {
		t.Error("expected error on empty root, got nil")
	}
	if _, err := workspaceCreate(t.TempDir(), "", Options{}); err == nil {
		t.Error("expected error on empty sessionID, got nil")
	}
}
