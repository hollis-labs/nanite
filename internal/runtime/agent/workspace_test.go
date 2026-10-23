package agent

import (
	"os"
	"path/filepath"
	"slices"
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

func TestSessionToolRoots(t *testing.T) {
	root := t.TempDir()
	ws, err := EnsureSessionWorkspace(filepath.Join(root, "workspaces"), "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	host1, host2 := t.TempDir(), t.TempDir()

	got := SessionToolRoots(nil, ws, []string{host1, "relative", host2})
	want := []string{host1, host2, ws.Root}
	if !slices.Equal(got, want) {
		t.Fatalf("roots = %v, want host directories in order, then the workspace: %v", got, want)
	}
	if only := SessionToolRoots(nil, ws, nil); !slices.Equal(only, []string{ws.Root}) {
		t.Fatalf("no host directories: %v, want the workspace as the work root", only)
	}
	if none := SessionToolRoots(nil, nil, nil); none != nil {
		t.Fatalf("nothing to offer: %v", none)
	}

	// A host directory inside the control plane is dropped; the workspace,
	// which Nanite owns, is kept even though it lives under the same tree.
	plane := t.TempDir()
	inside := filepath.Join(plane, "state")
	if err = os.MkdirAll(inside, 0o750); err != nil {
		t.Fatal(err)
	}
	wsInPlane, err := EnsureSessionWorkspace(filepath.Join(plane, "workspaces"), "sess-2")
	if err != nil {
		t.Fatal(err)
	}
	deps := &Dependencies{ControlPlane: ControlPlane{Dirs: []string{plane}}}
	got = SessionToolRoots(deps, wsInPlane, []string{inside, host1})
	if !slices.Equal(got, []string{host1, wsInPlane.Root}) {
		t.Fatalf("control plane filter: %v, want [%s %s]", got, host1, wsInPlane.Root)
	}
}

func TestSessionToolProtection(t *testing.T) {
	ws, err := EnsureSessionWorkspace(t.TempDir(), "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	protected := filepath.Join(parent, "control")
	outside := t.TempDir()
	if err = os.MkdirAll(protected, 0o750); err != nil {
		t.Fatal(err)
	}
	deps := &Dependencies{ControlPlane: ControlPlane{Dirs: []string{protected}}}

	if dirs, refusal := SessionToolProtection(nil, ws, []string{parent}); dirs != nil || refusal != "" {
		t.Fatalf("no dependencies: %v %q", dirs, refusal)
	}
	// A host directory wholly outside the control plane leaves the shell on.
	dirs, refusal := SessionToolProtection(deps, ws, []string{outside})
	if len(dirs) == 0 || refusal != "" {
		t.Fatalf("outside root: dirs=%v refusal=%q, want protected dirs and a shell", dirs, refusal)
	}
	// An ancestor of a protected directory is kept as a root, so the shell is
	// withheld and the refusal names the protected directory.
	_, refusal = SessionToolProtection(deps, ws, []string{outside, parent})
	if !strings.Contains(refusal, protected) {
		t.Fatalf("ancestor root: refusal = %q, want it to name %s", refusal, protected)
	}
	if roots := SessionToolRoots(deps, ws, []string{parent}); !slices.Contains(roots, parent) {
		t.Fatalf("roots = %v, want the ancestor kept whole for the sink to police", roots)
	}
}
