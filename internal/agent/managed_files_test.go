package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// TestManagedAgentPath_AcceptsLegitimateSlug pins the happy path: a
// well-formed slug resolves to <configRoot>/agents/<slug>.md.
func TestManagedAgentPath_AcceptsLegitimateSlug(t *testing.T) {
	root := t.TempDir()
	if err := EnsureManagedConfigDirs(root); err != nil {
		t.Fatalf("EnsureManagedConfigDirs: %v", err)
	}
	path, err := ManagedAgentPath(root, "atlas-curator")
	if err != nil {
		t.Fatalf("ManagedAgentPath: %v", err)
	}
	wantDir := filepath.Join(root, "agents")
	if filepath.Dir(path) != wantDir && !strings.HasSuffix(filepath.Dir(path), filepath.Join(filepath.Base(root), "agents")) {
		// On macOS t.TempDir() lives under a symlinked prefix (/var ->
		// /private/var); pathsafe.ResolveUnder resolves that symlink, so
		// compare basenames/suffix rather than requiring byte-identical
		// absolute paths.
		t.Fatalf("path dir = %q, want to resolve under %q", filepath.Dir(path), wantDir)
	}
	if filepath.Base(path) != "atlas-curator.md" {
		t.Fatalf("path base = %q, want atlas-curator.md", filepath.Base(path))
	}
}

// TestManagedAgentPath_RejectsTraversalSlug is GO-AGENT-001's direct
// regression: a crafted slug must never resolve outside configRoot/agents,
// and must be rejected before any filesystem call (the function returns an
// error rather than a path a caller could still write through).
func TestManagedAgentPath_RejectsTraversalSlug(t *testing.T) {
	root := t.TempDir()
	if err := EnsureManagedConfigDirs(root); err != nil {
		t.Fatalf("EnsureManagedConfigDirs: %v", err)
	}
	for _, malicious := range []string{
		"../evil",
		"../../etc/passwd",
		"..",
		"a/b",
		"/etc/passwd",
		"..%2f..%2fevil",
		"UPPER",
		"with space",
	} {
		path, err := ManagedAgentPath(root, malicious)
		if err == nil {
			t.Errorf("ManagedAgentPath(%q) = %q, nil, want a rejection error", malicious, path)
		}
	}
}

// TestManagedAgentPath_EmptyInputsRejected pins the pre-existing (unchanged)
// empty-configRoot/empty-slug guards still fire ahead of the new format
// check.
func TestManagedAgentPath_EmptyInputsRejected(t *testing.T) {
	if _, err := ManagedAgentPath("", "atlas"); err == nil {
		t.Error("empty configRoot: expected error, got nil")
	}
	if _, err := ManagedAgentPath(t.TempDir(), ""); err == nil {
		t.Error("empty slug: expected error, got nil")
	}
}

// TestWriteManagedAgentProfile_RoundTrips verifies a profile can be written
// to a ManagedAgentPath-resolved path and parsed back with its fields
// intact — the write half of GO-AGENT-002's zero-coverage gap.
func TestWriteManagedAgentProfile_RoundTrips(t *testing.T) {
	root := t.TempDir()
	if err := EnsureManagedConfigDirs(root); err != nil {
		t.Fatalf("EnsureManagedConfigDirs: %v", err)
	}
	path, err := ManagedAgentPath(root, "atlas-curator")
	if err != nil {
		t.Fatalf("ManagedAgentPath: %v", err)
	}
	profile := &store.AgentProfile{
		ID:           "11111111-1111-1111-1111-111111111111",
		Name:         "Atlas Curator",
		Slug:         "atlas-curator",
		SystemPrompt: "You curate the atlas knowledge base.",
		Description:  "Curator agent",
	}
	if err := WriteManagedAgentProfile(path, profile, nil); err != nil {
		t.Fatalf("WriteManagedAgentProfile: %v", err)
	}

	def, err := ParseMDFile(path)
	if err != nil {
		t.Fatalf("ParseMDFile: %v", err)
	}
	if def.Slug != "atlas-curator" || def.Name != "Atlas Curator" {
		t.Fatalf("round-trip mismatch: %+v", def)
	}
	if !strings.Contains(strings.TrimSpace(def.SystemPrompt), "curate") {
		t.Fatalf("system prompt not round-tripped: %q", def.SystemPrompt)
	}
}

// TestWriteManagedAgentProfile_RejectsNilProfile is a cheap guard-clause
// regression (previously untested at 0.0% coverage).
func TestWriteManagedAgentProfile_RejectsNilProfile(t *testing.T) {
	if err := WriteManagedAgentProfile(filepath.Join(t.TempDir(), "x.md"), nil, nil); err == nil {
		t.Fatal("expected an error for a nil profile")
	}
}

// TestFileRevision_MissingFileYieldsEmptyToken and
// TestFileRevision_ChangesWithContent close out managed_files.go's other
// previously-0.0%-covered exported functions.
func TestFileRevision_MissingFileYieldsEmptyToken(t *testing.T) {
	rev, err := FileRevision(filepath.Join(t.TempDir(), "does-not-exist.md"))
	if err != nil {
		t.Fatalf("FileRevision: %v", err)
	}
	if rev != "" {
		t.Fatalf("rev = %q, want empty for a missing file", rev)
	}
}

func TestFileRevision_ChangesWithContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.md")
	if err := os.WriteFile(path, []byte("one"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	revOne, err := FileRevision(path)
	if err != nil || revOne == "" {
		t.Fatalf("FileRevision(one) = %q, %v", revOne, err)
	}
	if err := os.WriteFile(path, []byte("two"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	revTwo, err := FileRevision(path)
	if err != nil || revTwo == "" {
		t.Fatalf("FileRevision(two) = %q, %v", revTwo, err)
	}
	if revOne == revTwo {
		t.Fatal("revision token did not change with content")
	}
}
