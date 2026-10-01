package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/go-agent-wrapper/plant"
	"github.com/hollis-labs/go-materialize/artifact"
	"github.com/hollis-labs/nanite/internal/store"
)

// TestPlantSpec_FilesMCPAndProviderSettings verifies the shared plantSpec
// write routine writes Files entries, the MCPConfig ".mcp.json" shortcut,
// and the configured ProviderSettings destination.
func TestPlantSpec_FilesMCPAndProviderSettings(t *testing.T) {
	bootDir := t.TempDir()

	spec := plant.Spec{
		Files: map[string][]byte{
			"top.md":             []byte("top-content"),
			"nested/dir/file.md": []byte("nested-content"),
		},
		MCPConfig: []byte(`{"mcp":true}`),
		ProviderSettings: map[string][]byte{
			"claude": []byte(`{"permissions":{}}`),
		},
	}
	result, err := plantSpec(context.Background(), bootDir, spec, plantConfig{
		provider:             "claude",
		providerSettingsPath: ".claude/settings.json",
	})
	if err != nil {
		t.Fatalf("plantSpec: %v", err)
	}

	cases := []struct {
		path string
		want string
	}{
		{"top.md", "top-content"},
		{"nested/dir/file.md", "nested-content"},
		{".mcp.json", `{"mcp":true}`},
		{".claude/settings.json", `{"permissions":{}}`},
	}
	for _, c := range cases {
		body, err := os.ReadFile(filepath.Join(bootDir, filepath.FromSlash(c.path)))
		if err != nil {
			t.Errorf("read %s: %v", c.path, err)
			continue
		}
		if string(body) != c.want {
			t.Errorf("%s = %q, want %q", c.path, string(body), c.want)
		}
	}
	if len(result.PlantedFiles) != len(cases) {
		t.Errorf("PlantedFiles = %v, want %d entries", result.PlantedFiles, len(cases))
	}
}

// TestPlantSpec_ProviderSettingsAndFileModeOverrides verifies a
// configured providerSettingsMode / fileModeOverrides entry is honored —
// codex's config.toml/auth.json need 0o600, not the 0o644 Files default.
func TestPlantSpec_ProviderSettingsAndFileModeOverrides(t *testing.T) {
	bootDir := t.TempDir()
	spec := plant.Spec{
		Files:            map[string][]byte{"auth.json": []byte("secret")},
		ProviderSettings: map[string][]byte{"codex": []byte("policy")},
	}
	_, err := plantSpec(context.Background(), bootDir, spec, plantConfig{
		provider:             "codex",
		providerSettingsPath: "config.toml",
		providerSettingsMode: 0o600,
		fileModeOverrides:    map[string]os.FileMode{"auth.json": 0o600},
	})
	if err != nil {
		t.Fatalf("plantSpec: %v", err)
	}
	for _, p := range []string{"config.toml", "auth.json"} {
		info, err := os.Stat(filepath.Join(bootDir, p))
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s mode = %o, want 0600", p, perm)
		}
	}
}

// TestPlantSpec_RejectsUnsafePath verifies the shared bootdir path-safety
// gate (agentlaunch.ValidateBootDirRelPath) rejects a traversal path
// before any write happens.
func TestPlantSpec_RejectsUnsafePath(t *testing.T) {
	bootDir := t.TempDir()
	_, err := plantSpec(context.Background(), bootDir, plant.Spec{
		Files: map[string][]byte{"../escape.txt": []byte("nope")},
	}, plantConfig{provider: "claude"})
	if err == nil {
		t.Fatal("expected path-safety rejection, got nil")
	}
	if !strings.Contains(err.Error(), "escape.txt") {
		t.Errorf("error should name the offending path, got %v", err)
	}
}

// TestPlantSpec_RejectsHooks verifies a non-empty Spec.Hooks is rejected
// loudly — Nanite has no hook-planting destination yet, and nothing in
// this package populates Hooks today, so a non-empty value can only mean
// a caller expected behavior that isn't implemented.
func TestPlantSpec_RejectsHooks(t *testing.T) {
	bootDir := t.TempDir()
	_, err := plantSpec(context.Background(), bootDir, plant.Spec{
		Hooks: []plant.Hook{{Provider: "claude", Name: "pre-tool-use"}},
	}, plantConfig{provider: "claude"})
	if err == nil || !strings.Contains(err.Error(), "hooks") {
		t.Fatalf("expected hooks-unsupported error, got %v", err)
	}
}

// TestPlantSpec_RejectsRecoveryPrompt mirrors TestPlantSpec_RejectsHooks
// for Spec.RecoveryPrompt.
func TestPlantSpec_RejectsRecoveryPrompt(t *testing.T) {
	bootDir := t.TempDir()
	_, err := plantSpec(context.Background(), bootDir, plant.Spec{
		RecoveryPrompt: "resume here",
	}, plantConfig{provider: "claude"})
	if err == nil || !strings.Contains(err.Error(), "RecoveryPrompt") {
		t.Fatalf("expected RecoveryPrompt-unsupported error, got %v", err)
	}
}

// TestClaudePlantSpec_Shape verifies the claude layout's app-extra files
// (.sandbox/* docs) and provider settings ride the plant.Spec vocabulary
// correctly — i.e. the Nanite app extras land in Spec.Files and the MCP
// descriptor lands in Spec.MCPConfig.
func TestClaudePlantSpec_Shape(t *testing.T) {
	params := SetupParams{
		SessionID:    "sess-x",
		AgentProfile: &store.AgentProfile{Name: "Specced", Slug: "specced"},
		SystemPrompt: "be helpful",
		BootContent:  "# kickoff\n",
		MCPConfig: MCPConfig{
			BinaryPath: "/bin/nanite",
			DBPath:     "/tmp/x.db",
		},
	}
	spec, err := claudePlantSpec(t.TempDir(), params)
	if err != nil {
		t.Fatalf("claudePlantSpec: %v", err)
	}
	for _, want := range []string{".sandbox/agent-context.md", ".sandbox/envelope-schema.md", "CLAUDE.md", "boot.md"} {
		if _, ok := spec.Files[want]; !ok {
			t.Errorf("claudePlantSpec: missing Files[%q]", want)
		}
	}
	if len(spec.MCPConfig) == 0 {
		t.Errorf("claudePlantSpec: expected non-empty MCPConfig")
	}
	if _, ok := spec.ProviderSettings["claude"]; !ok {
		t.Errorf("claudePlantSpec: missing ProviderSettings[claude]")
	}
}

// TestMCPConfigBytes_DisabledWhenNoDBPath verifies MCP planting is
// skipped (nil MCPConfig bytes) when MCPConfig carries no DBPath —
// matching mcpOverlay's existing DBPath-gating behavior.
func TestMCPConfigBytes_DisabledWhenNoDBPath(t *testing.T) {
	body, err := mcpConfigBytes(SetupParams{SessionID: "s1"})
	if err != nil {
		t.Fatalf("mcpConfigBytes: %v", err)
	}
	if len(body) != 0 {
		t.Errorf("expected no MCP config when DBPath empty, got %q", body)
	}
}

// TestPlanters_ImplementPlanterInterface is a compile-time-adjacent
// smoke test pinning that all three provider Planters satisfy
// plant.Planter (also enforced by the `var _ plant.Planter = ...`
// assertions alongside each type, but kept here too so `go test` output
// names the invariant explicitly).
func TestPlanters_ImplementPlanterInterface(t *testing.T) {
	var planters = []plant.Planter{claudePlanter{}, codexPlanter{}, opencodePlanter{}}
	if len(planters) != 3 {
		t.Fatalf("expected 3 planters, got %d", len(planters))
	}
}

// TestPlantSpec_SurfacesMaterializationHandle pins CW-20260910-0020's
// actual deliverable: the shared engine's Handle (manifest + per-entry
// change data) reaches the caller instead of being discarded at the
// planting boundary, which is what CW-20260910-0006 originally wanted.
func TestPlantSpec_SurfacesMaterializationHandle(t *testing.T) {
	bootDir := t.TempDir()
	result, err := plantSpec(context.Background(), bootDir, plant.Spec{
		Files: map[string][]byte{"CLAUDE.md": []byte("prompt")},
	}, plantConfig{provider: "claude"})
	if err != nil {
		t.Fatalf("plantSpec: %v", err)
	}
	if result.Handle == nil {
		t.Fatal("Result.Handle is nil — the materialization handle was discarded")
	}
	if len(result.Handle.Manifest.Entries) == 0 {
		t.Error("Handle.Manifest carries no entries")
	}
	if !result.Complete {
		t.Error("Result.Complete = false, want true for a clean plant")
	}
}

// TestPlantSpec_PreservesReservedPrefixGate pins the path-safety gate the
// migration had to keep EXPLICITLY. agentlaunch.ValidateBootDirRelPath
// denies a reserved-prefix destination (".ssh/", ".aws/", ...);
// artifact.ValidateRelPath — which the shared engine applies on its own —
// does NOT carry that denylist and would happily plant these. If this
// test ever starts failing, the gate was dropped in favor of the
// engine's weaker validation.
func TestPlantSpec_PreservesReservedPrefixGate(t *testing.T) {
	for _, relPath := range []string{".ssh/authorized_keys", ".aws/credentials", ".git/config", ".gnupg/secring.gpg"} {
		t.Run(relPath, func(t *testing.T) {
			bootDir := t.TempDir()
			_, err := plantSpec(context.Background(), bootDir, plant.Spec{
				Files: map[string][]byte{relPath: []byte("nope")},
			}, plantConfig{provider: "claude"})
			if err == nil {
				t.Fatalf("expected %q to be rejected by the reserved-prefix gate", relPath)
			}
			if _, statErr := os.Stat(filepath.Join(bootDir, filepath.FromSlash(relPath))); statErr == nil {
				t.Errorf("%q was planted despite the gate", relPath)
			}
		})
	}
}

// TestPlantSpec_PlantsEmptyFile guards the nil-vs-empty []byte trap:
// artifact.Entry.Validate rejects a file entry whose Bytes is nil, and an
// empty planted file is legitimate (boot.md with no kickoff content).
func TestPlantSpec_PlantsEmptyFile(t *testing.T) {
	bootDir := t.TempDir()
	if _, err := plantSpec(context.Background(), bootDir, plant.Spec{
		Files: map[string][]byte{"boot.md": []byte("")},
	}, plantConfig{provider: "claude"}); err != nil {
		t.Fatalf("plantSpec with empty content: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(bootDir, "boot.md")) //nolint:gosec // reads a file this test just planted into t.TempDir()
	if err != nil {
		t.Fatalf("read boot.md: %v", err)
	}
	if len(body) != 0 {
		t.Errorf("boot.md = %q, want empty", body)
	}
}

// TestPlantSpec_MCPConfigModeUnchanged pins .mcp.json at 0644 — the mode
// Nanite has always written it at. go-agent-wrapper's LEGACY MCPConfig
// conversion uses 0600, so this is the assertion that catches a
// regression back onto the legacy field path.
func TestPlantSpec_MCPConfigModeUnchanged(t *testing.T) {
	bootDir := t.TempDir()
	if _, err := plantSpec(context.Background(), bootDir, plant.Spec{
		MCPConfig: []byte(`{"mcpServers":{}}`),
	}, plantConfig{provider: "claude"}); err != nil {
		t.Fatalf("plantSpec: %v", err)
	}
	info, err := os.Stat(filepath.Join(bootDir, ".mcp.json"))
	if err != nil {
		t.Fatalf("stat .mcp.json: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Errorf(".mcp.json mode = %o, want 0644", perm)
	}
}

// TestPlantSpec_SeparatePlantsCompose is the regression test for the
// ownership trap CW-20260910-0020 found. The shared engine refuses a
// desired path that exists on disk but is absent from its manifest, and
// refuses the WHOLE plant rather than that one entry.
//
// The real sequence this models: a boot-time Populate plants the bootdir
// file-set; PlantAgentSkillFiles then plants a mid-session skill grant
// into the SAME live boot dir; crash recovery then calls Populate again,
// whose Spec.Files legitimately contains both. That third step is the one
// that used to be at risk — it only stays green because the mid-session
// plant also goes through plantSpec and lands in the manifest.
func TestPlantSpec_SeparatePlantsCompose(t *testing.T) {
	ctx := context.Background()
	bootDir := t.TempDir()
	cfg := plantConfig{provider: "claude"}

	bootFiles := map[string][]byte{"CLAUDE.md": []byte("v1"), "boot.md": []byte("kickoff")}
	if _, err := plantSpec(ctx, bootDir, plant.Spec{Files: bootFiles}, cfg); err != nil {
		t.Fatalf("initial Populate: %v", err)
	}

	// Mid-session skill grant, planted on its own (PlantAgentSkillFiles).
	skillFiles := map[string][]byte{".claude/skills/demo/SKILL.md": []byte("skill body")}
	if _, err := plantSpec(ctx, bootDir, plant.Spec{Files: skillFiles}, cfg); err != nil {
		t.Fatalf("mid-session skill plant: %v", err)
	}

	// Crash-recovery Repopulate: the full set, including the skill.
	full := map[string][]byte{}
	for k, v := range bootFiles {
		full[k] = v
	}
	for k, v := range skillFiles {
		full[k] = v
	}
	full["CLAUDE.md"] = []byte("v2-regenerated")
	result, err := plantSpec(ctx, bootDir, plant.Spec{Files: full}, cfg)
	if err != nil {
		t.Fatalf("Repopulate after a separate mid-session plant: %v", err)
	}
	if len(result.ConflictFiles) != 0 {
		t.Errorf("ConflictFiles = %v, want none", result.ConflictFiles)
	}
	body, err := os.ReadFile(filepath.Join(bootDir, ".claude/skills/demo/SKILL.md")) //nolint:gosec // reads a file this test just planted into t.TempDir()
	if err != nil || string(body) != "skill body" {
		t.Errorf("skill file = %q (err %v), want %q", body, err, "skill body")
	}
	if c, _ := os.ReadFile(filepath.Join(bootDir, "CLAUDE.md")); string(c) != "v2-regenerated" { //nolint:gosec // reads a file this test just planted into t.TempDir()
		t.Errorf("CLAUDE.md = %q, want the regenerated body", c)
	}
}

// TestPlantSpec_RejectsUnownedDestination documents the constraint the
// test above depends on: a file written into the boot dir OUTSIDE the
// engine makes the next plant that wants to own its path fail. This is
// why PlantAgentSkillFiles was migrated onto plantSpec rather than left
// on a direct write. Asserting it keeps the constraint visible if a
// future caller is tempted to os.WriteFile into a planted boot dir.
func TestPlantSpec_RejectsUnownedDestination(t *testing.T) {
	ctx := context.Background()
	bootDir := t.TempDir()
	cfg := plantConfig{provider: "claude"}

	if _, err := plantSpec(ctx, bootDir, plant.Spec{
		Files: map[string][]byte{"CLAUDE.md": []byte("v1")},
	}, cfg); err != nil {
		t.Fatalf("initial plant: %v", err)
	}
	// A writer that bypassed the engine.
	if err := os.WriteFile(filepath.Join(bootDir, "rogue.md"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := plantSpec(ctx, bootDir, plant.Spec{
		Files: map[string][]byte{"CLAUDE.md": []byte("v2"), "rogue.md": []byte("owned now")},
	}, cfg)
	if err == nil {
		t.Fatal("expected an ownership conflict for the unowned destination")
	}
}

// TestPlantSpec_ArtifactsPlantDirectoriesAndModes covers what the flat
// Files map structurally cannot express: an explicit directory, and a
// per-entry mode. This is CW-20260910-0010's primitive, and the shape
// CW-20260910-0015 needs for hook scripts (0700, under hooks/<provider>/).
func TestPlantSpec_ArtifactsPlantDirectoriesAndModes(t *testing.T) {
	bootDir := t.TempDir()
	tree := artifact.Tree{Entries: []artifact.Entry{
		{Path: "hooks/claude", Kind: artifact.EntryDirectory, Mode: 0o700},
		{Path: "hooks/claude/pre-tool-use.sh", Kind: artifact.EntryFile, Mode: 0o700, Bytes: []byte("#!/usr/bin/env bash\nexit 0\n")},
	}}
	result, err := plantSpec(context.Background(), bootDir, plant.Spec{Artifacts: tree}, plantConfig{provider: "claude"})
	if err != nil {
		t.Fatalf("plantSpec with Artifacts: %v", err)
	}
	if result.Handle == nil {
		t.Fatal("expected a materialization handle")
	}

	dir, err := os.Stat(filepath.Join(bootDir, "hooks/claude"))
	if err != nil {
		t.Fatalf("stat hooks/claude: %v", err)
	}
	if !dir.IsDir() {
		t.Error("hooks/claude is not a directory")
	}
	if perm := dir.Mode().Perm(); perm != 0o700 {
		t.Errorf("hooks/claude mode = %o, want 0700", perm)
	}

	script, err := os.Stat(filepath.Join(bootDir, "hooks/claude/pre-tool-use.sh"))
	if err != nil {
		t.Fatalf("stat hook script: %v", err)
	}
	if perm := script.Mode().Perm(); perm != 0o700 {
		t.Errorf("hook script mode = %o, want 0700 (must be executable)", perm)
	}
}

// TestPlantSpec_ArtifactsAndFilesCoexist verifies the tree path is
// additive: a caller can supply Artifacts entries alongside the legacy
// Files/MCPConfig/ProviderSettings fields in one plant.
func TestPlantSpec_ArtifactsAndFilesCoexist(t *testing.T) {
	bootDir := t.TempDir()
	_, err := plantSpec(context.Background(), bootDir, plant.Spec{
		Files:     map[string][]byte{"CLAUDE.md": []byte("prompt")},
		MCPConfig: []byte(`{"mcpServers":{}}`),
		Artifacts: artifact.Tree{Entries: []artifact.Entry{
			{Path: "hooks/claude/stop.sh", Kind: artifact.EntryFile, Mode: 0o700, Bytes: []byte("exit 0\n")},
		}},
	}, plantConfig{provider: "claude"})
	if err != nil {
		t.Fatalf("plantSpec: %v", err)
	}
	for _, p := range []string{"CLAUDE.md", ".mcp.json", "hooks/claude/stop.sh"} {
		if _, err := os.Stat(filepath.Join(bootDir, filepath.FromSlash(p))); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
}

// TestPlantSpec_ArtifactsRespectPathGate pins that a caller-built tree is
// not a way around agentlaunch.ValidateBootDirRelPath. artifact's own
// validation would accept ".ssh/authorized_keys" — it carries no
// reserved-prefix denylist — so this has to be checked on the Artifacts
// path explicitly, not just on Files.
func TestPlantSpec_ArtifactsRespectPathGate(t *testing.T) {
	bootDir := t.TempDir()
	_, err := plantSpec(context.Background(), bootDir, plant.Spec{
		Artifacts: artifact.Tree{Entries: []artifact.Entry{
			{Path: ".ssh/authorized_keys", Kind: artifact.EntryFile, Mode: 0o600, Bytes: []byte("ssh-rsa AAAA")},
		}},
	}, plantConfig{provider: "claude"})
	if err == nil {
		t.Fatal("expected the reserved-prefix gate to reject a caller-built tree entry")
	}
	if _, statErr := os.Stat(filepath.Join(bootDir, ".ssh/authorized_keys")); statErr == nil {
		t.Error("the entry was planted despite the gate")
	}
}

// TestBootDirTreeFromDir_PlantsDirectoryTree exercises the on-disk source
// adapter end to end: walk a real directory, prefix it, plant it.
func TestBootDirTreeFromDir_PlantsDirectoryTree(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "nested"), 0o750); err != nil {
		t.Fatal(err)
	}
	// 0o700 deliberately: this test exists to prove an EXECUTABLE source
	// mode survives the walk and the plant, which is what CW-20260910-0015's
	// hook scripts depend on. A 0o600 fixture could not show that.
	if err := os.WriteFile(filepath.Join(src, "top.sh"), []byte("top\n"), 0o700); err != nil { //nolint:gosec // see above — an executable fixture is the point
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "nested", "inner.md"), []byte("inner\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	tree, err := bootDirTreeFromDir(context.Background(), src, "hooks/claude", "nanite:hooks")
	if err != nil {
		t.Fatalf("bootDirTreeFromDir: %v", err)
	}
	var sawDir bool
	for _, e := range tree.Entries {
		if e.Kind == artifact.EntryDirectory && e.Path == "hooks/claude/nested" {
			sawDir = true
		}
		if e.Ownership.GroupID != "nanite:hooks" {
			t.Errorf("entry %s has group %q, want nanite:hooks", e.Path, e.Ownership.GroupID)
		}
	}
	if !sawDir {
		t.Error("expected an explicit directory entry for the nested dir")
	}

	bootDir := t.TempDir()
	if _, plantErr := plantSpec(context.Background(), bootDir, plant.Spec{Artifacts: tree}, plantConfig{provider: "claude"}); plantErr != nil {
		t.Fatalf("plant the resolved tree: %v", plantErr)
	}
	top, err := os.Stat(filepath.Join(bootDir, "hooks/claude/top.sh"))
	if err != nil {
		t.Fatalf("stat planted top.sh: %v", err)
	}
	if perm := top.Mode().Perm(); perm != 0o700 {
		t.Errorf("top.sh mode = %o, want 0700 — source modes must survive the walk", perm)
	}
	body, err := os.ReadFile(filepath.Join(bootDir, "hooks/claude/nested/inner.md")) //nolint:gosec // reads a file this test just planted into t.TempDir()
	if err != nil || string(body) != "inner\n" {
		t.Errorf("nested/inner.md = %q (err %v), want %q", body, err, "inner\n")
	}
}

// TestBootDirTreeFromDir_RejectsSymlink pins the resolver's default
// symlink policy. A boot dir is planted for a child agent to read;
// importing a symlink by value would copy content from outside the source
// root into it. Reject is the default and this asserts Nanite keeps it.
func TestBootDirTreeFromDir_RejectsSymlink(t *testing.T) {
	src := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(src, "link.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := bootDirTreeFromDir(context.Background(), src, "hooks/claude", ""); err == nil {
		t.Fatal("expected the symlink to be rejected")
	}
}

// TestBootDirTreeFromDir_EnforcesLimits verifies the entry bound is live,
// so a mistaken source path cannot fill the boot dir.
func TestBootDirTreeFromDir_EnforcesLimits(t *testing.T) {
	src := t.TempDir()
	deep := src
	for i := 0; i < bootDirSourceLimits.MaxDepth+2; i++ {
		deep = filepath.Join(deep, "d")
	}
	if err := os.MkdirAll(deep, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := bootDirTreeFromDir(context.Background(), src, "hooks/claude", ""); err == nil {
		t.Fatal("expected the depth limit to reject an over-deep source tree")
	}
}
