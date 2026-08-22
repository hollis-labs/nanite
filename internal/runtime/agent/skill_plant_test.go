package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

	got, err := SkillPlantFiles(context.Background(), skills, vendor, func(slug string) ([]string, bool) {
		return []string{"prefix-a/" + slug, "prefix-b/" + slug}, true
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

// TestSkillDestPrefixSafe covers the invariant the path-traversal fix
// (TASKS/skills/10's "Fix required" section / ESCALATIONS.md's 2026-08-22
// HIGH finding) actually needs to hold: "no skill's planted files can
// ever land outside <providerPrefix>/<own-slug>/" — not merely that the
// two literal reproduction slugs from the finding are blocked.
func TestSkillDestPrefixSafe(t *testing.T) {
	cases := []struct {
		name    string
		root    string
		slug    string
		wantOK  bool
		wantDst string
	}{
		{"normal slug", ".claude/skills", "demo-skill", true, ".claude/skills/demo-skill"},
		{"double-dotdot cancels entire root", ".claude/skills", "../..", false, ""},
		{"single dotdot escapes one level", ".claude/skills", "..", false, ""},
		{"single dotdot escapes short root entirely", "skills", "..", false, ""},
		{"triple dotdot escapes past root", ".claude/skills", "../../..", false, ""},
		{"partial internal cancellation still rejected", ".claude/skills", "valid/../valid2", false, ""},
		{"embedded dotdot mid-slug", ".claude/skills", "a/../../b", false, ""},
		{"empty slug", ".claude/skills", "", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dest, ok := skillDestPrefixSafe(tc.root, tc.slug)
			if ok != tc.wantOK {
				t.Fatalf("skillDestPrefixSafe(%q, %q) ok = %v, want %v (dest=%q)", tc.root, tc.slug, ok, tc.wantOK, dest)
			}
			if ok && dest != tc.wantDst {
				t.Fatalf("skillDestPrefixSafe(%q, %q) dest = %q, want %q", tc.root, tc.slug, dest, tc.wantDst)
			}
			// The stronger, general invariant: whenever ok is true, dest
			// must genuinely be nested under root's own subtree — never
			// equal to root itself, and always root+"/" as a real path
			// prefix.
			if ok {
				if dest == tc.root {
					t.Fatalf("skillDestPrefixSafe(%q, %q) returned root itself as dest, not a per-skill subtree", tc.root, tc.slug)
				}
				if !strings.HasPrefix(dest+"/", tc.root+"/") {
					t.Fatalf("skillDestPrefixSafe(%q, %q) dest %q does not stay under root", tc.root, tc.slug, dest)
				}
			}
		})
	}
}

// TestClaudeSkillDestPrefixes_AdversarialSlugBlocked and
// TestOpencodeSkillDestPrefixes_AdversarialSlugBlocked are the exact two
// reproduction cases named in TASKS/skills/10's "Fix required" section:
// claude's longer root needs a full "../.." to cancel out entirely,
// opencode's shorter "skills" root needs only a bare "..".
func TestClaudeSkillDestPrefixes_AdversarialSlugBlocked(t *testing.T) {
	if prefixes, ok := claudeSkillDestPrefixes("../.."); ok || prefixes != nil {
		t.Fatalf("claudeSkillDestPrefixes(\"../..\") = (%v, %v), want (nil, false)", prefixes, ok)
	}
}

func TestOpencodeSkillDestPrefixes_AdversarialSlugBlocked(t *testing.T) {
	if prefixes, ok := opencodeSkillDestPrefixes(".."); ok || prefixes != nil {
		t.Fatalf("opencodeSkillDestPrefixes(\"..\") = (%v, %v), want (nil, false)", prefixes, ok)
	}
}

// TestSkillPlantFiles_AdversarialSlugBlocked is the task's own required
// regression test: an adversarial slug must (a) have its escape blocked,
// (b) be omitted from the resulting Files map entirely (not landed
// elsewhere), and (c) never clobber an already-planted file at the
// collision path a canceled prefix would otherwise land on (e.g. the
// exact "CLAUDE.md" key claudePlantSpec uses for the agent's own real
// system prompt, or opencode's own top-level config stand-in).
func TestSkillPlantFiles_AdversarialSlugBlocked(t *testing.T) {
	cases := []struct {
		name         string
		slug         string
		destPrefixes func(string) ([]string, bool)
		sentinelKey  string // stand-in for an already-planted real boot-dir file
	}{
		{
			name:         "claude: \"../..\" cancels the entire .claude/skills root",
			slug:         "../..",
			destPrefixes: claudeSkillDestPrefixes,
			sentinelKey:  "CLAUDE.md", // claudePlantSpec's real system-prompt key
		},
		{
			name:         "opencode: bare \"..\" escapes the shorter skills/ root",
			slug:         "..",
			destPrefixes: opencodeSkillDestPrefixes,
			sentinelKey:  "agents.json", // stand-in for opencodePlantSpec's own real config key
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vendor := &fakeSkillVendor{files: map[string]skillvendor.FileMap{
				skillAddrGood: {"SKILL.md": []byte("MALICIOUS OVERWRITE")},
			}}
			skills := []PlantableSkill{{Slug: tc.slug, ContentHash: skillAddrGood}}

			got, err := SkillPlantFiles(context.Background(), skills, vendor, tc.destPrefixes)
			if err != nil {
				t.Fatalf("SkillPlantFiles: %v", err)
			}
			// (a) + (b): the escape is blocked and the malicious skill is
			// omitted from the Files map entirely.
			if len(got) != 0 {
				t.Fatalf("expected no files planted for adversarial slug %q, got %v", tc.slug, keysOf(got))
			}

			// (c): merging `got` into an already-"planted" boot dir's file
			// map (matching claudePlantSpec/opencodePlantSpec's own
			// map-merge pattern for combining skill files with every
			// other boot-dir content source) must never clobber the
			// sentinel — since `got` is empty this is definitionally
			// satisfied, but assert it explicitly so a future regression
			// that makes SkillPlantFiles return a non-empty map for an
			// unsafe slug is caught here too.
			files := map[string][]byte{tc.sentinelKey: []byte("REAL BOOT CONTENT")}
			for k, v := range got {
				files[k] = v
			}
			if string(files[tc.sentinelKey]) != "REAL BOOT CONTENT" {
				t.Fatalf("sentinel file %q was clobbered: got %q", tc.sentinelKey, files[tc.sentinelKey])
			}
		})
	}
}

// TestSkillFilesForProvider_AdversarialSlugSkippedButOthersPlanted proves
// the fix at the same layer the reviewer's finding traced the bug to:
// store.Skill.Slug is REST-settable with no format validation, so a
// catalog row's Slug field itself (not just the agent_known_skills grant
// name) can carry the adversarial value. Confirms the malicious skill is
// omitted while a co-granted, legitimate skill still plants normally
// (proving the fix rejects only the offending skill, not the whole
// batch).
func TestSkillFilesForProvider_AdversarialSlugSkippedButOthersPlanted(t *testing.T) {
	vendor := &fakeSkillVendor{files: map[string]skillvendor.FileMap{
		skillAddrGood: {"SKILL.md": []byte("good")},
	}}
	sstore := &fakeSkillStore{
		skills: map[string]*store.Skill{
			"good": {Slug: "good", ContentHash: skillAddrGood},
			"evil": {Slug: "../..", ContentHash: skillAddrGood}, // adversarial catalog Slug
		},
		known: map[string][]store.AgentKnownSkill{
			"agent-1": {
				{AgentID: "agent-1", SkillName: "good", ApprovedContentHash: skillAddrGood},
				{AgentID: "agent-1", SkillName: "evil", ApprovedContentHash: skillAddrGood},
			},
		},
	}
	params := SetupParams{
		AgentProfile: &store.AgentProfile{ID: "agent-1"},
		Skills:       sstore,
		SkillVendor:  vendor,
	}

	got, err := skillFilesForProvider(context.Background(), "claude", params)
	if err != nil {
		t.Fatalf("skillFilesForProvider: %v", err)
	}
	if _, ok := got[".claude/skills/good/SKILL.md"]; !ok {
		t.Errorf("expected legitimate co-granted skill still planted, got %v", keysOf(got))
	}
	for k := range got {
		if !strings.HasPrefix(k, ".claude/skills/") {
			t.Errorf("adversarial skill leaked a file outside its own subtree: %q", k)
		}
	}
	if len(got) != 1 {
		t.Errorf("expected exactly 1 planted file (only the legitimate skill), got %v", keysOf(got))
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
