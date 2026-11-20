package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/authored"
	"github.com/hollis-labs/nanite/internal/provider/fake"
)

func TestToolApprovalsIgnoreAgentLocalState(t *testing.T) {
	e, db, _ := setupEngine(t, fake.New(), "")
	writeToolPackage(t, e.cfg.Dir, "fixture", "description: d\nkind: mcp\nentry: run.sh\n", "#!/bin/sh\nexit 0\n")
	pkg, err := authored.LoadTool(e.cfg.Dir, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetSetting(t.Context(), "", "toolpkg.approved:fixture", pkg.Digest); err != nil {
		t.Fatal(err)
	}
	if e.toolApproved(t.Context(), pkg) {
		t.Fatal("agent-local setting seeded approval")
	}
	if err := e.approveTool(t.Context(), pkg); err != nil {
		t.Fatal(err)
	}
	restored := &Engine{cfg: Config{Dir: e.cfg.Dir, ToolApprovalDir: e.cfg.ToolApprovalDir}}
	if !restored.toolApproved(t.Context(), pkg) {
		t.Fatal("external approval did not survive a new engine")
	}
	dir, agentDir, err := e.toolApprovalLocation()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("approval directory mode: %v %v", info, err)
	}
	path := filepath.Join(dir, approvalFile(agentDir, pkg))
	info, err = os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("approval file mode: %v %v", info, err)
	}
	changed := pkg
	changed.Digest = "different"
	if restored.toolApproved(t.Context(), changed) {
		t.Fatal("approval carried to changed digest")
	}
	other := &Engine{cfg: Config{Dir: t.TempDir(), ToolApprovalDir: e.cfg.ToolApprovalDir}}
	if other.toolApproved(t.Context(), pkg) {
		t.Fatal("approval carried to another agent")
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(e.cfg.Dir, alias); err != nil {
		t.Fatal(err)
	}
	aliased := &Engine{cfg: Config{Dir: alias, ToolApprovalDir: e.cfg.ToolApprovalDir}}
	if !aliased.toolApproved(t.Context(), pkg) {
		t.Fatal("canonical agent alias lost approval")
	}
	// Even an approval filename in the host directory cannot point to a
	// value supplied by the agent directory.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	seeded := filepath.Join(e.cfg.Dir, "seeded")
	if err := os.WriteFile(seeded, []byte(pkg.Digest+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(seeded, path); err != nil {
		t.Fatal(err)
	}
	if restored.toolApproved(t.Context(), pkg) {
		t.Fatal("approval followed an agent-supplied symlink")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(seeded, path); err != nil {
		t.Fatal(err)
	}
	if restored.toolApproved(t.Context(), pkg) {
		t.Fatal("approval accepted an agent-supplied hard link")
	}
}

func TestToolApprovalsRefuseLocationsInsideAgent(t *testing.T) {
	dir := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(dir, alias); err != nil {
		t.Fatal(err)
	}
	for _, location := range []string{dir, filepath.Join(dir, ".nanite", "tool-approvals"), filepath.Join(alias, "new", "approvals")} {
		e := &Engine{cfg: Config{Dir: dir, ToolApprovalDir: location}}
		if err := e.approveTool(t.Context(), authored.ToolPackage{Name: "fixture", Digest: "digest"}); err == nil {
			t.Fatalf("accepted %s", location)
		}
	}
}

func TestToolApprovalsDefaultToUserConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	config, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{cfg: Config{Dir: t.TempDir()}}
	dir, _, err := e.toolApprovalLocation()
	if err != nil {
		t.Fatal(err)
	}
	expected, err := canonicalLocation(filepath.Join(config, "nanite", "tool-approvals"))
	if err != nil {
		t.Fatal(err)
	}
	if dir != expected {
		t.Fatalf("approval location %s, want %s", dir, expected)
	}
}
