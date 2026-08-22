package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/skillvendor"
	"github.com/hollis-labs/nanite/internal/store"
)

// fakeSkillStore is a test double satisfying SkillStore (SkillGrantStore +
// SkillCatalogStore) without a real *store.Store.
type fakeSkillStore struct {
	known   map[string][]store.AgentKnownSkill // agentID -> rows
	skills  map[string]*store.Skill            // slug -> skill
	listErr error
	getErr  error
}

func (f *fakeSkillStore) ListAgentKnownSkills(_ context.Context, agentID string) ([]store.AgentKnownSkill, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.known[agentID], nil
}

func (f *fakeSkillStore) GetSkillBySlug(slug string) (*store.Skill, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.skills[slug], nil
}

// fakeSkillVendor is a test double satisfying SkillVendorReader without a
// real *skillvendor.Store.
type fakeSkillVendor struct {
	files map[string]skillvendor.FileMap // address -> files
	err   error
}

func (f *fakeSkillVendor) ReadFiles(address string) (skillvendor.FileMap, error) {
	if f.err != nil {
		return nil, f.err
	}
	fm, ok := f.files[address]
	if !ok {
		return nil, errors.New("fake skill vendor: no files for " + address)
	}
	return fm, nil
}

const (
	skillAddrGood  = "skl-vendor-0000000000000001"
	skillAddrStale = "skl-vendor-0000000000000002"
)

// TestResolvePlantableSkills covers every branch of the grant-validity
// check, mirroring internal/skill/gate.go's Gate.authorize logic
// (TASKS/skills/09) applied to planting instead of execution.
func TestResolvePlantableSkills(t *testing.T) {
	catalog := map[string]*store.Skill{
		"good":    {Slug: "good", ContentHash: skillAddrGood},
		"revoked": {Slug: "revoked", ContentHash: skillAddrGood},
		"stale":   {Slug: "stale", ContentHash: skillAddrGood}, // re-installed since approval
		// "missing" intentionally absent from the catalog.
	}

	fakeStore := &fakeSkillStore{
		skills: catalog,
		known: map[string][]store.AgentKnownSkill{
			"agent-1": {
				{AgentID: "agent-1", SkillName: "good", ApprovedContentHash: skillAddrGood},
				{AgentID: "agent-1", SkillName: "revoked", ApprovedContentHash: ""},           // bare assignment, never approved
				{AgentID: "agent-1", SkillName: "stale", ApprovedContentHash: skillAddrStale}, // stale vs. catalog's current hash
				{AgentID: "agent-1", SkillName: "missing", ApprovedContentHash: skillAddrGood}, // dangling — no catalog row
			},
		},
	}

	got, err := ResolvePlantableSkills(context.Background(), fakeStore, fakeStore, "agent-1")
	if err != nil {
		t.Fatalf("ResolvePlantableSkills: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("plantable skills = %+v, want exactly 1 (only \"good\")", got)
	}
	if got[0].Slug != "good" || got[0].ContentHash != skillAddrGood {
		t.Errorf("plantable skill = %+v, want {good %s}", got[0], skillAddrGood)
	}
}

// TestResolvePlantableSkills_NilInputs confirms the "nothing to plant" nil
// contract rather than an error, matching skillFilesForProvider's own
// "no skill wiring configured" tolerance.
func TestResolvePlantableSkills_NilInputs(t *testing.T) {
	fakeStore := &fakeSkillStore{}
	if got, err := ResolvePlantableSkills(context.Background(), nil, fakeStore, "agent-1"); err != nil || got != nil {
		t.Errorf("nil grants: got (%v, %v), want (nil, nil)", got, err)
	}
	if got, err := ResolvePlantableSkills(context.Background(), fakeStore, nil, "agent-1"); err != nil || got != nil {
		t.Errorf("nil catalog: got (%v, %v), want (nil, nil)", got, err)
	}
	if got, err := ResolvePlantableSkills(context.Background(), fakeStore, fakeStore, ""); err != nil || got != nil {
		t.Errorf("empty agentID: got (%v, %v), want (nil, nil)", got, err)
	}
}

// TestSkillPlantFiles_MultiplePrefixes verifies a skill is planted under
// every prefix destPrefixes returns, preserving relative paths within the
// vendored tree (mirrors opencode's two-candidate-destination design).
func TestSkillPlantFiles_MultiplePrefixes(t *testing.T) {
	vendor := &fakeSkillVendor{files: map[string]skillvendor.FileMap{
		skillAddrGood: {
			"SKILL.md":        []byte("# hello"),
			"scripts/run.sh":  []byte("#!/bin/sh\necho hi\n"),
			"references/a.md": []byte("ref"),
		},
	}}
	skills := []PlantableSkill{{Slug: "hello-skill", ContentHash: skillAddrGood}}

	got, err := SkillPlantFiles(context.Background(), skills, vendor, func(slug string) []string {
		return []string{"prefix-a/" + slug, "prefix-b/" + slug}
	})
	if err != nil {
		t.Fatalf("SkillPlantFiles: %v", err)
	}

	want := map[string]string{
		"prefix-a/hello-skill/SKILL.md":        "# hello",
		"prefix-a/hello-skill/scripts/run.sh":  "#!/bin/sh\necho hi\n",
		"prefix-a/hello-skill/references/a.md": "ref",
		"prefix-b/hello-skill/SKILL.md":        "# hello",
		"prefix-b/hello-skill/scripts/run.sh":  "#!/bin/sh\necho hi\n",
		"prefix-b/hello-skill/references/a.md": "ref",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d files, want %d\ngot: %v", len(got), len(want), keysOf(got))
	}
	for relPath, wantContent := range want {
		gotContent, ok := got[relPath]
		if !ok {
			t.Errorf("missing planted file %q", relPath)
			continue
		}
		if string(gotContent) != wantContent {
			t.Errorf("%s = %q, want %q", relPath, gotContent, wantContent)
		}
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestSkillPlantFiles_EmptyInputs confirms the nil-not-error contract.
func TestSkillPlantFiles_EmptyInputs(t *testing.T) {
	if got, err := SkillPlantFiles(context.Background(), nil, &fakeSkillVendor{}, claudeSkillDestPrefixes); err != nil || got != nil {
		t.Errorf("empty skills: got (%v, %v), want (nil, nil)", got, err)
	}
	if got, err := SkillPlantFiles(context.Background(), []PlantableSkill{{Slug: "x", ContentHash: skillAddrGood}}, nil, claudeSkillDestPrefixes); err != nil || got != nil {
		t.Errorf("nil vendor: got (%v, %v), want (nil, nil)", got, err)
	}
}

// TestSkillFilesForProvider_Dispatch covers the per-provider destination
// dispatch: claude gets .claude/skills/, opencode gets both candidate
// destinations, codex (no native skill mechanism) gets nothing.
func TestSkillFilesForProvider_Dispatch(t *testing.T) {
	vendor := &fakeSkillVendor{files: map[string]skillvendor.FileMap{
		skillAddrGood: {"SKILL.md": []byte("body")},
	}}
	sstore := &fakeSkillStore{
		skills: map[string]*store.Skill{"demo": {Slug: "demo", ContentHash: skillAddrGood}},
		known: map[string][]store.AgentKnownSkill{
			"agent-1": {{AgentID: "agent-1", SkillName: "demo", ApprovedContentHash: skillAddrGood}},
		},
	}
	params := SetupParams{
		AgentProfile: &store.AgentProfile{ID: "agent-1"},
		Skills:       sstore,
		SkillVendor:  vendor,
	}

	claudeFiles, err := skillFilesForProvider(context.Background(), "claude", params)
	if err != nil {
		t.Fatalf("claude: %v", err)
	}
	if _, ok := claudeFiles[".claude/skills/demo/SKILL.md"]; !ok {
		t.Errorf("claude: missing .claude/skills/demo/SKILL.md, got %v", keysOf(claudeFiles))
	}

	opencodeFiles, err := skillFilesForProvider(context.Background(), "opencode", params)
	if err != nil {
		t.Fatalf("opencode: %v", err)
	}
	for _, want := range []string{"skills/demo/SKILL.md", ".opencode/skills/demo/SKILL.md"} {
		if _, ok := opencodeFiles[want]; !ok {
			t.Errorf("opencode: missing %s, got %v", want, keysOf(opencodeFiles))
		}
	}

	codexFiles, err := skillFilesForProvider(context.Background(), "codex", params)
	if err != nil {
		t.Fatalf("codex: %v", err)
	}
	if len(codexFiles) != 0 {
		t.Errorf("codex: expected no skill files planted (no native skill mechanism), got %v", keysOf(codexFiles))
	}
}

// TestSkillFilesForProvider_NoWiring confirms a SetupParams with no skill
// wiring (Skills/SkillVendor unset — e.g. every pre-existing bootdir test
// in this package) is a silent no-op, not an error, so this task's change
// can't regress any existing boot-dir test that doesn't know about skills.
func TestSkillFilesForProvider_NoWiring(t *testing.T) {
	params := SetupParams{AgentProfile: &store.AgentProfile{ID: "agent-1"}}
	got, err := skillFilesForProvider(context.Background(), "claude", params)
	if err != nil || got != nil {
		t.Errorf("got (%v, %v), want (nil, nil)", got, err)
	}
}

// TestClaudeLayout_Setup_PlantsGrantedSkill is the closest unit-test
// equivalent of this task's own Done-means dogfeed requirement: a real
// claudeLayout.Setup call, with skill wiring configured, actually writes
// the granted skill's vendored files to disk at .claude/skills/<slug>/ —
// and a stale-approval skill is NOT planted.
func TestClaudeLayout_Setup_PlantsGrantedSkill(t *testing.T) {
	vendor := &fakeSkillVendor{files: map[string]skillvendor.FileMap{
		skillAddrGood: {
			"SKILL.md":       []byte("# Demo Skill\n"),
			"scripts/run.sh": []byte("#!/bin/sh\n"),
		},
	}}
	sstore := &fakeSkillStore{
		skills: map[string]*store.Skill{
			"demo":  {Slug: "demo", ContentHash: skillAddrGood},
			"stale": {Slug: "stale", ContentHash: skillAddrGood},
		},
		known: map[string][]store.AgentKnownSkill{
			"agent-plant-1": {
				{AgentID: "agent-plant-1", SkillName: "demo", ApprovedContentHash: skillAddrGood},
				{AgentID: "agent-plant-1", SkillName: "stale", ApprovedContentHash: skillAddrStale},
			},
		},
	}

	profile := &store.AgentProfile{ID: "agent-plant-1", Name: "Plant Test", Slug: "plant-test"}
	bootDir, err := claudeLayout{}.Setup(SetupParams{
		SessionID:    "sess-plant-1",
		AgentProfile: profile,
		SystemPrompt: "test",
		Skills:       sstore,
		SkillVendor:  vendor,
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(bootDir) })

	skillMD, err := os.ReadFile(filepath.Join(bootDir, ".claude/skills/demo/SKILL.md"))
	if err != nil {
		t.Fatalf("read planted SKILL.md: %v", err)
	}
	if string(skillMD) != "# Demo Skill\n" {
		t.Errorf("SKILL.md content = %q", skillMD)
	}
	if _, err := os.ReadFile(filepath.Join(bootDir, ".claude/skills/demo/scripts/run.sh")); err != nil {
		t.Errorf("read planted scripts/run.sh: %v", err)
	}

	if _, err := os.Stat(filepath.Join(bootDir, ".claude/skills/stale")); !os.IsNotExist(err) {
		t.Errorf("stale-approval skill must not be planted, got stat err=%v", err)
	}
}

// TestPlantAgentSkillFiles_MidSession is the mid-session-regen-path
// equivalent (internal/service/chat_boot_drive.go's replant call site) —
// granting a skill and calling PlantAgentSkillFiles against an
// already-existing boot dir (no fresh Setup) makes the skill's files
// appear without a full session restart.
func TestPlantAgentSkillFiles_MidSession(t *testing.T) {
	vendor := &fakeSkillVendor{files: map[string]skillvendor.FileMap{
		skillAddrGood: {"SKILL.md": []byte("# Late Grant\n")},
	}}
	sstore := &fakeSkillStore{
		skills: map[string]*store.Skill{"late": {Slug: "late", ContentHash: skillAddrGood}},
		known:  map[string][]store.AgentKnownSkill{}, // nothing granted yet
	}
	deps := &Dependencies{Skills: sstore, SkillVendor: vendor}

	bootDir := t.TempDir()

	// Before the grant: no-op, nothing planted.
	if err := PlantAgentSkillFiles(context.Background(), deps, bootDir, "claude", "agent-late-1"); err != nil {
		t.Fatalf("PlantAgentSkillFiles (pre-grant): %v", err)
	}
	if _, err := os.Stat(filepath.Join(bootDir, ".claude/skills/late")); !os.IsNotExist(err) {
		t.Fatalf("expected no skill planted before grant, stat err=%v", err)
	}

	// Grant the skill (simulating an admin action mid-session), then
	// replant — this is the exact operation chat_boot_drive.go's
	// driveBootSession performs on the next turn of an active session.
	sstore.known["agent-late-1"] = []store.AgentKnownSkill{
		{AgentID: "agent-late-1", SkillName: "late", ApprovedContentHash: skillAddrGood},
	}
	if err := PlantAgentSkillFiles(context.Background(), deps, bootDir, "claude", "agent-late-1"); err != nil {
		t.Fatalf("PlantAgentSkillFiles (post-grant): %v", err)
	}
	body, err := os.ReadFile(filepath.Join(bootDir, ".claude/skills/late/SKILL.md"))
	if err != nil {
		t.Fatalf("read newly planted SKILL.md: %v", err)
	}
	if string(body) != "# Late Grant\n" {
		t.Errorf("SKILL.md content = %q", body)
	}
}

// TestPlantAgentSkillFiles_NoWiring confirms a Dependencies with no skill
// wiring (production default until the composition root's skill vendor
// store construction succeeds) is a clean no-op.
func TestPlantAgentSkillFiles_NoWiring(t *testing.T) {
	deps := &Dependencies{}
	bootDir := t.TempDir()
	if err := PlantAgentSkillFiles(context.Background(), deps, bootDir, "claude", "agent-1"); err != nil {
		t.Fatalf("PlantAgentSkillFiles: %v", err)
	}
}

// TestPlantAgentSkillFiles_NilDeps guards the exported entry point against
// a nil Dependencies rather than panicking.
func TestPlantAgentSkillFiles_NilDeps(t *testing.T) {
	if err := PlantAgentSkillFiles(context.Background(), nil, "/tmp/x", "claude", "agent-1"); err == nil {
		t.Fatal("expected error for nil Dependencies")
	}
}
