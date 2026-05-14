package bootprofile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCatalog_EmptyRootIsInert(t *testing.T) {
	cat, err := LoadCatalog("")
	if err != nil {
		t.Fatalf("LoadCatalog(\"\") error: %v", err)
	}
	if !cat.IsEmpty() {
		t.Fatalf("expected empty catalog for empty root, got %d profiles / %d launches", len(cat.Profiles), len(cat.Launches))
	}
}

func TestLoadCatalog_MissingRootIsInert(t *testing.T) {
	cat, err := LoadCatalog(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("LoadCatalog(missing) error: %v", err)
	}
	if !cat.IsEmpty() {
		t.Fatalf("expected empty catalog for missing root, got %v", cat)
	}
}

func TestLoadCatalog_HappyPath(t *testing.T) {
	root := t.TempDir()
	writeYAML(t, root, "boot-profiles/nanite.backend.main.yaml", happyProfileYAML)
	writeYAML(t, root, "launches/nanite-claude.yaml", happyLaunchYAML)

	cat, err := LoadCatalog(root)
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	if len(cat.Profiles) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(cat.Profiles))
	}
	prof, ok := cat.Profiles["nanite.backend.main"]
	if !ok {
		t.Fatal("profile id not indexed")
	}
	if prof.Identity.Role != "backend" {
		t.Fatalf("identity.role = %q, want %q", prof.Identity.Role, "backend")
	}
	if got, want := len(cat.Launches), 1; got != want {
		t.Fatalf("expected %d launches, got %d", want, got)
	}
	launch, ok := cat.Launches["nanite-claude"]
	if !ok {
		t.Fatal("launch id not indexed")
	}
	if launch.Provider != "pty-claude" {
		t.Fatalf("launch provider = %q", launch.Provider)
	}
}

func TestLoadProfile_MissingFile(t *testing.T) {
	_, err := LoadProfile(filepath.Join(t.TempDir(), "nope.yaml"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "nope.yaml") {
		t.Fatalf("error %q should name the missing path", err)
	}
}

func TestLoadProfile_MalformedYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.yaml")
	if err := os.WriteFile(path, []byte("id: ok\nidentity: [this is not a map"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadProfile(path)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "broken.yaml") {
		t.Fatalf("error %q should name the broken path", err)
	}
}

func TestLoadProfile_MissingRequiredFields(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "missing id",
			body: "identity:\n  lineage_alias: x.y.z\n",
			want: "missing required field 'id'",
		},
		{
			name: "missing lineage_alias",
			body: "id: x.y.z\nidentity: {}\n",
			want: "missing required field 'identity.lineage_alias'",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "p.yaml")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadProfile(path)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q should mention %q", err, tc.want)
			}
		})
	}
}

func TestLoadLaunch_MissingProvider(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "l.yaml")
	if err := os.WriteFile(path, []byte("id: foo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadLaunch(path)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "provider") {
		t.Fatalf("error %q should mention provider", err)
	}
}

func TestLoadCatalog_DuplicateProfileID(t *testing.T) {
	root := t.TempDir()
	writeYAML(t, root, "boot-profiles/a.yaml", happyProfileYAML)
	writeYAML(t, root, "boot-profiles/b.yaml", happyProfileYAML)
	_, err := LoadCatalog(root)
	if err == nil {
		t.Fatal("expected duplicate-id error")
	}
	if !strings.Contains(err.Error(), "duplicate profile id") {
		t.Fatalf("error %q should call out duplicate", err)
	}
}

func TestCompileFromCatalog_ProfileNotFound(t *testing.T) {
	cat, err := LoadCatalog("")
	if err != nil {
		t.Fatal(err)
	}
	_, err = CompileFromCatalog(cat, "missing.profile", nil)
	if !errors.Is(err, ErrProfileNotFound) {
		t.Fatalf("expected ErrProfileNotFound, got %v", err)
	}
}

func TestCompileFromCatalog_LaunchNotFound(t *testing.T) {
	prof := Profile{
		ID:       "x.y.z",
		Identity: Identity{LineageAlias: "x.y.z"},
		Launch:   "missing-launch",
	}
	cat := &Catalog{
		Root:     "",
		Profiles: map[string]Profile{prof.ID: prof},
		Launches: map[string]Launch{},
	}
	_, err := CompileFromCatalog(cat, prof.ID, nil)
	if !errors.Is(err, ErrLaunchNotFound) {
		t.Fatalf("expected ErrLaunchNotFound, got %v", err)
	}
}

// ── shared fixtures ─────────────────────────────────────────────────

const happyProfileYAML = `id: nanite.backend.main
display_name: "Nanite — Backend"
launch: nanite-claude
identity:
  lineage_alias:   nanite.backend.main
  profile_id:      nanite-backend
  profile_version: 1
  role:            backend
  project:         nanite
  work_root:       ~/Projects-apps/nanite
  tracking_root:   ~/Projects-apps/agent-workspaces/execution/nanite/
  vanta_primary:   "2026-04-19"
slots:
  agent:
    type: text
    content: |
      You are a {{role}} agent on {{project}}.
  recap:
    type: cmd
    run: "echo recap"
    timeout: 5s
mcp_servers:
  - vanta
  - clockwork
`

const happyLaunchYAML = `id: nanite-claude
provider: pty-claude
workdir: ~/Projects-apps/nanite
ui_label: "Nanite (Claude PTY)"
env:
  NANITE_FOO: bar
boot_mode: file
`

func writeYAML(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
