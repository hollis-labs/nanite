package service

// J7 (CW-20260421-0011): tests for the skills/agents DB ingestion pipeline.

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	agentpkg "github.com/hollis-labs/nanite/internal/agent"
	skillpkg "github.com/hollis-labs/nanite/internal/skill"
	"github.com/hollis-labs/nanite/internal/store"
)

func newIngestTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.New(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestAutoIngestSkills_InsertNewSkill verifies that a previously unknown skill
// is inserted into the DB with the correct metadata columns.
func TestAutoIngestSkills_InsertNewSkill(t *testing.T) {
	st := newIngestTestStore(t)

	defs := []*skillpkg.Definition{
		{
			Name:        "My Skill",
			Slug:        "my-skill",
			Description: "Does something useful",
			Source:      "user",
			SourceRef:   "/home/user/.nanite/skills/my-skill.md",
			Prompt:      "## My Skill\nDo the thing.",
		},
	}

	n := AutoIngestSkills(st, defs)
	if n != 1 {
		t.Fatalf("expected 1 ingested skill, got %d", n)
	}

	sk, err := st.GetSkillBySlug("my-skill")
	if err != nil {
		t.Fatalf("GetSkillBySlug: %v", err)
	}
	if sk == nil {
		t.Fatal("expected skill in DB, got nil")
	}
	if sk.Source != "user" {
		t.Errorf("Source: got %q, want %q", sk.Source, "user")
	}
	if sk.ImportedAt == "" {
		t.Error("ImportedAt should be set after ingest")
	}
	if sk.OriginSystem != "nanite" {
		t.Errorf("OriginSystem: got %q, want %q", sk.OriginSystem, "nanite")
	}
	if sk.Format != "markdown" {
		t.Errorf("Format: got %q, want %q", sk.Format, "markdown")
	}
	if sk.Version != 1 {
		t.Errorf("Version: got %d, want 1", sk.Version)
	}
	if sk.IsBuiltin {
		t.Error("file-dropped skill should not be marked is_builtin")
	}
}

// TestAutoIngestSkills_IdempotentReingest verifies that re-ingesting the same
// skill without content changes is a no-op (version stays at 1).
func TestAutoIngestSkills_IdempotentReingest(t *testing.T) {
	st := newIngestTestStore(t)

	def := &skillpkg.Definition{
		Name:   "Stable Skill",
		Slug:   "stable-skill",
		Source: "user",
		Prompt: "same content",
	}

	AutoIngestSkills(st, []*skillpkg.Definition{def})
	AutoIngestSkills(st, []*skillpkg.Definition{def}) // re-ingest same content

	sk, err := st.GetSkillBySlug("stable-skill")
	if err != nil || sk == nil {
		t.Fatalf("GetSkillBySlug: %v, %v", sk, err)
	}
	if sk.Version != 1 {
		t.Errorf("Version should stay 1 on no-op reingest, got %d", sk.Version)
	}
}

// TestAutoIngestSkills_ContentChangeDoesNotOverwriteExistingRow is
// TASKS/phase-1/08's core regression for skills: once a row has been
// ingested via AutoIngestSkills' boot-time pass, a subsequent boot's
// re-parse of the same (now-changed) file must NOT overwrite the DB row's
// content or bump its version -- the file is the first-ingest path, not a
// standing sync. (Before this fix, re-ingesting with a changed prompt under
// the same source bumped the version and overwrote the content on every
// boot -- see this test's prior name/assertions,
// TestAutoIngestSkills_VersionBumpsOnContentChange.)
func TestAutoIngestSkills_ContentChangeDoesNotOverwriteExistingRow(t *testing.T) {
	st := newIngestTestStore(t)

	def := &skillpkg.Definition{
		Name:   "Evolving Skill",
		Slug:   "evolving-skill",
		Source: "user",
		Prompt: "v1 content",
	}
	AutoIngestSkills(st, []*skillpkg.Definition{def})

	def.Prompt = "v2 content — changed"
	AutoIngestSkills(st, []*skillpkg.Definition{def})

	sk, err := st.GetSkillBySlug("evolving-skill")
	if err != nil || sk == nil {
		t.Fatalf("GetSkillBySlug: %v, %v", sk, err)
	}
	if sk.Version != 1 {
		t.Errorf("Version should stay frozen at 1 on boot-time reingest, got %d", sk.Version)
	}
	if sk.Prompt != "v1 content" {
		t.Errorf("Prompt: got %q, want frozen v1 content (boot-time reingest must not overwrite an existing row)", sk.Prompt)
	}
}

// TestAutoIngestSkills_SourceChangeStillSyncsOnce is the provenance-
// transition exception to the freeze above: if a skill's source genuinely
// changes between boots (e.g. the file relocated from one discovery tier to
// another), that's a deliberate one-time reclassification, not an ordinary
// repeated boot -- the content sync (and version bump, if content also
// changed) still happens once.
func TestAutoIngestSkills_SourceChangeStillSyncsOnce(t *testing.T) {
	st := newIngestTestStore(t)

	def := &skillpkg.Definition{
		Name:   "Relocating Skill",
		Slug:   "relocating-skill",
		Source: "user",
		Prompt: "v1 content",
	}
	AutoIngestSkills(st, []*skillpkg.Definition{def})

	def.Source = "project"
	def.Prompt = "v2 content — relocated"
	AutoIngestSkills(st, []*skillpkg.Definition{def})

	sk, err := st.GetSkillBySlug("relocating-skill")
	if err != nil || sk == nil {
		t.Fatalf("GetSkillBySlug: %v, %v", sk, err)
	}
	if sk.Source != "project" {
		t.Errorf("Source: got %q, want %q after the provenance transition", sk.Source, "project")
	}
	if sk.Prompt != "v2 content — relocated" {
		t.Errorf("Prompt: got %q, want the synced v2 content", sk.Prompt)
	}
	if sk.Version != 2 {
		t.Errorf("Version should bump once on the provenance-transition sync, got %d", sk.Version)
	}

	// A further boot pass under the new (now-stable) source freezes again.
	def.Prompt = "v3 content — should be ignored"
	AutoIngestSkills(st, []*skillpkg.Definition{def})
	sk2, err := st.GetSkillBySlug("relocating-skill")
	if err != nil || sk2 == nil {
		t.Fatalf("GetSkillBySlug (2nd check): %v, %v", sk2, err)
	}
	if sk2.Prompt != "v2 content — relocated" {
		t.Errorf("Prompt after re-freeze: got %q, want frozen v2 content", sk2.Prompt)
	}
}

// TestAutoIngestAgents_InsertNewAgent verifies that a previously unknown agent
// is inserted into the DB with the correct metadata and H1 trust tier.
func TestAutoIngestAgents_InsertNewAgent(t *testing.T) {
	st := newIngestTestStore(t)

	defs := []*agentpkg.Definition{
		{
			Name:         "My Agent",
			Slug:         "my-agent",
			SystemPrompt: "You are a helpful assistant.",
			Source:       "user",
			SourceRef:    "/home/user/.nanite/agents/my-agent.md",
		},
	}

	n := AutoIngestAgents(st, defs, nil)
	if n != 1 {
		t.Fatalf("expected 1 ingested agent, got %d", n)
	}

	a, err := st.GetAgentBySlug("my-agent")
	if err != nil {
		t.Fatalf("GetAgentBySlug: %v", err)
	}
	if a.Source != "user" {
		t.Errorf("Source: got %q, want %q", a.Source, "user")
	}
	if a.ImportedAt == "" {
		t.Error("ImportedAt should be set after ingest")
	}
	if a.OriginSystem != "nanite" {
		t.Errorf("OriginSystem: got %q, want %q", a.OriginSystem, "nanite")
	}
	if a.Format != "markdown" {
		t.Errorf("Format: got %q, want %q", a.Format, "markdown")
	}
}

// TestAutoIngestAgents_ClassHarness is the CW-20260815-0009 regression test:
// a profile with class: harness (a real, used LifecycleClass value for
// durable-agent instances — see store.DurableAgentClassHarness) must ingest
// into agent_profiles like any other class value, not be silently dropped
// by the store-layer enum validator.
func TestAutoIngestAgents_ClassHarness(t *testing.T) {
	st := newIngestTestStore(t)

	defs := []*agentpkg.Definition{
		{
			Slug:         "orchestrator",
			Name:         "Orchestrator",
			SystemPrompt: "You dispatch.",
			Source:       "project",
			Class:        "harness",
		},
	}

	n := AutoIngestAgents(st, defs, nil)
	if n != 1 {
		t.Fatalf("expected 1 ingested agent, got %d", n)
	}

	a, err := st.GetAgentBySlug("orchestrator")
	if err != nil {
		t.Fatalf("GetAgentBySlug: %v", err)
	}
	if a == nil {
		t.Fatal("expected an orchestrator row in agent_profiles, got none")
	}
	if a.Class != "harness" {
		t.Errorf("Class: got %q, want %q", a.Class, "harness")
	}
}

// TestAutoIngestAgents_InvalidClassNotSilent is the visibility half of
// CW-20260815-0009: a deliberately unsupported class value must (a) fail to
// ingest — no phantom agent_profiles row — and (b) be loud about it: an
// aggregate startup-time ERROR log naming the failed slug, not just a
// per-item Warn easy to miss in startup noise.
func TestAutoIngestAgents_InvalidClassNotSilent(t *testing.T) {
	st := newIngestTestStore(t)

	var logBuf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	defs := []*agentpkg.Definition{
		{
			Slug:         "bogus-agent",
			Name:         "Bogus Agent",
			SystemPrompt: "x",
			Source:       "project",
			Class:        "not-a-real-class",
		},
	}

	n := AutoIngestAgents(st, defs, nil)
	if n != 0 {
		t.Fatalf("expected 0 ingested agents for a rejected class value, got %d", n)
	}
	if _, err := st.GetAgentBySlug("bogus-agent"); err == nil {
		t.Fatal("expected no agent_profiles row for a rejected class value")
	}

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "level=ERROR") {
		t.Errorf("expected an ERROR-level aggregate ingestion-failure log, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "bogus-agent") {
		t.Errorf("expected the failure log to name the failed slug, got: %s", logOutput)
	}
}

// TestAutoIngestAgents_FailureLogCountsExcludeSkippedDefs is a
// CW-20260815-0009 follow-up (Copilot review on PR #241): defs is filtered
// (nil entries, empty slugs) before ingestion is even attempted, so the
// aggregate failure log's counts must reflect only defs actually attempted
// — otherwise failed+succeeded silently doesn't add up to the logged total.
func TestAutoIngestAgents_FailureLogCountsExcludeSkippedDefs(t *testing.T) {
	st := newIngestTestStore(t)

	var logBuf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	defs := []*agentpkg.Definition{
		nil,        // skipped, not attempted
		{Slug: ""}, // skipped, not attempted
		{Slug: "ok-agent", Name: "OK", SystemPrompt: "x", Source: "project"},
		{Slug: "bad-agent", Name: "Bad", SystemPrompt: "x", Source: "project", Class: "not-a-real-class"},
	}

	n := AutoIngestAgents(st, defs, nil)
	if n != 1 {
		t.Fatalf("expected 1 ingested agent, got %d", n)
	}

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "considered=2") {
		t.Errorf("expected considered=2 (the 2 skipped entries excluded from the 4 defs), got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "failed=1") || !strings.Contains(logOutput, "succeeded=1") {
		t.Errorf("expected failed=1 succeeded=1, got: %s", logOutput)
	}
}

// TestAutoIngestAgents_UnknownToolNameIsLoud is CW-20260815-0013's
// visibility half: a profile declaring a tool name that isn't in the
// registered catalog (a typo'd or renamed tool, e.g. bash_run instead of
// dev_bash) must still ingest successfully (the profile row itself is
// valid) but log loudly — an aggregate ERROR naming the slug and the
// specific unknown name(s) — rather than silently no-op at selection time.
func TestAutoIngestAgents_UnknownToolNameIsLoud(t *testing.T) {
	st := newIngestTestStore(t)

	var logBuf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	defs := []*agentpkg.Definition{
		{
			Slug:         "typo-agent",
			Name:         "Typo Agent",
			SystemPrompt: "x",
			Source:       "project",
			RoleTools:    []string{"dev_read", "bash_run"}, // dev_read real, bash_run not
			Tools:        []string{"skill_get"},            // not real either
		},
	}

	knownTools := map[string]bool{"dev_read": true, "dev_bash": true}
	n := AutoIngestAgents(st, defs, knownTools)
	if n != 1 {
		t.Fatalf("expected the profile to still ingest despite the bad tool names, got n=%d", n)
	}
	if _, err := st.GetAgentBySlug("typo-agent"); err != nil {
		t.Fatalf("expected agent_profiles row despite unknown tool names: %v", err)
	}

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "level=ERROR") {
		t.Errorf("expected an ERROR-level aggregate log for unknown tool references, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "typo-agent") {
		t.Errorf("expected the log to name the affected slug, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "bash_run") || !strings.Contains(logOutput, "skill_get") {
		t.Errorf("expected the log to name both unknown tools (bash_run, skill_get), got: %s", logOutput)
	}
	if strings.Contains(logOutput, `"dev_read"`) {
		t.Errorf("dev_read is a real known tool and must not be flagged, got: %s", logOutput)
	}
}

// TestAutoIngestAgents_HardcodedModelIsLoud is the regression pin for
// CW-20260815-0021: a profile that hardcodes `model:` instead of leaving it
// blank (to inherit the system default via ResolveProviderAndModel) must
// still ingest normally, but produce a loud, discoverable warning naming the
// offending slug and model — the guard against the bug recurring silently.
func TestAutoIngestAgents_HardcodedModelIsLoud(t *testing.T) {
	st := newIngestTestStore(t)

	var logBuf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	defs := []*agentpkg.Definition{
		{
			Slug:         "pinned-agent",
			Name:         "Pinned Agent",
			SystemPrompt: "x",
			Source:       "project",
			Model:        "claude-sonnet-4-20250514",
		},
		{
			Slug:         "default-agent",
			Name:         "Default Agent",
			SystemPrompt: "x",
			Source:       "project",
		},
	}

	n := AutoIngestAgents(st, defs, nil)
	if n != 2 {
		t.Fatalf("expected both profiles to ingest despite the hardcoded model, got n=%d", n)
	}

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "level=WARN") {
		t.Errorf("expected a WARN-level log for the hardcoded model, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "pinned-agent") {
		t.Errorf("expected the log to name the affected slug, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "claude-sonnet-4-20250514") {
		t.Errorf("expected the log to name the hardcoded model, got: %s", logOutput)
	}
	if strings.Contains(logOutput, "default-agent") {
		t.Errorf("default-agent leaves model blank and must not be flagged, got: %s", logOutput)
	}
}

// TestAutoIngestAgents_NilKnownToolsSkipsValidation proves nil means "skip
// the check" — not "nothing is known" (which would flag every declared
// tool as unknown). Existing callers/tests that don't wire a tool catalog
// must see unchanged behavior.
func TestAutoIngestAgents_NilKnownToolsSkipsValidation(t *testing.T) {
	st := newIngestTestStore(t)

	var logBuf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	defs := []*agentpkg.Definition{
		{
			Slug:         "no-catalog-agent",
			Name:         "No Catalog Agent",
			SystemPrompt: "x",
			Source:       "project",
			RoleTools:    []string{"anything_at_all"},
		},
	}

	if n := AutoIngestAgents(st, defs, nil); n != 1 {
		t.Fatalf("expected 1 ingested agent, got %d", n)
	}
	if strings.Contains(logBuf.String(), "unregistered tool name") {
		t.Errorf("expected no unknown-tool warning when knownTools is nil, got: %s", logBuf.String())
	}
}

// TestAutoIngestAgents_H1TrustTierUserDropped verifies that Source="user" agents
// get default_trust_tier="untrusted" (H1 rule, CW-20260421-0014).
func TestAutoIngestAgents_H1TrustTierUserDropped(t *testing.T) {
	st := newIngestTestStore(t)

	defs := []*agentpkg.Definition{
		{
			Slug:         "untrusted-agent",
			Name:         "Untrusted Agent",
			SystemPrompt: "Do stuff",
			Source:       "user", // dropped into ~/.nanite/agents/
		},
	}
	AutoIngestAgents(st, defs, nil)

	var tier string
	err := st.DB.QueryRow(
		`SELECT default_trust_tier FROM agent_profiles WHERE slug = ?`,
		"untrusted-agent",
	).Scan(&tier)
	if err != nil {
		t.Fatalf("query trust tier: %v", err)
	}
	if tier != "untrusted" {
		t.Errorf("H1 trust: want %q, got %q", "untrusted", tier)
	}
}

// TestAutoIngestAgents_H1TrustTierBuiltin verifies that Source="builtin" agents
// keep the default_trust_tier="normal" (built-in agents are trusted at normal level).
func TestAutoIngestAgents_H1TrustTierBuiltin(t *testing.T) {
	st := newIngestTestStore(t)

	defs := []*agentpkg.Definition{
		{
			Slug:         "builtin-agent",
			Name:         "Builtin Agent",
			SystemPrompt: "System role",
			Source:       "builtin",
		},
	}
	AutoIngestAgents(st, defs, nil)

	var tier string
	err := st.DB.QueryRow(
		`SELECT default_trust_tier FROM agent_profiles WHERE slug = ?`,
		"builtin-agent",
	).Scan(&tier)
	if err != nil {
		t.Fatalf("query trust tier: %v", err)
	}
	if tier != "normal" {
		t.Errorf("H1 trust: want %q, got %q", "normal", tier)
	}
}

// TestAutoIngestAgents_H1TrustTierPlugin verifies that Source="plugin" agents
// get default_trust_tier="untrusted" (same rule as user-dropped).
func TestAutoIngestAgents_H1TrustTierPlugin(t *testing.T) {
	st := newIngestTestStore(t)

	defs := []*agentpkg.Definition{
		{
			Slug:         "plugin-agent",
			Name:         "Plugin Agent",
			SystemPrompt: "Plugin system role",
			Source:       "plugin",
		},
	}
	AutoIngestAgents(st, defs, nil)

	var tier string
	err := st.DB.QueryRow(
		`SELECT default_trust_tier FROM agent_profiles WHERE slug = ?`,
		"plugin-agent",
	).Scan(&tier)
	if err != nil {
		t.Fatalf("query trust tier: %v", err)
	}
	if tier != "untrusted" {
		t.Errorf("H1 trust: want %q, got %q", "untrusted", tier)
	}
}

// TestAutoIngestAgents_ReingestDoesNotOverwriteExistingRow is
// TASKS/phase-1/08's core regression ("kill the file-reingest-on-boot
// pattern, in full"): once a row has been ingested via AutoIngestAgents'
// boot-time pass, a subsequent boot's re-parse of the same (now-changed)
// file must NOT overwrite the DB row's content, for any source (project,
// user, plugin, internal alike) -- the file stays the first-ingest path,
// not a standing sync that runs unconditionally on every process start.
// (This test previously asserted the opposite -- the old always-overwrite
// behavior this task kills -- under the name
// TestAutoIngestAgents_UpdateOnReingest.)
func TestAutoIngestAgents_ReingestDoesNotOverwriteExistingRow(t *testing.T) {
	st := newIngestTestStore(t)

	def := &agentpkg.Definition{
		Slug:         "update-agent",
		Name:         "Update Agent",
		SystemPrompt: "v1 prompt",
		Source:       "user",
	}
	AutoIngestAgents(st, []*agentpkg.Definition{def}, nil)

	// Change the system prompt and re-ingest via the same boot-time path
	// (simulates the file on disk changing, or simply being re-parsed
	// verbatim, on a subsequent Nanite restart).
	def.SystemPrompt = "v2 prompt — should be ignored"
	AutoIngestAgents(st, []*agentpkg.Definition{def}, nil)

	a, err := st.GetAgentBySlug("update-agent")
	if err != nil {
		t.Fatalf("GetAgentBySlug: %v", err)
	}
	if a.SystemPrompt != "v1 prompt" {
		t.Errorf("SystemPrompt: got %q, want frozen v1 value (boot-time reingest must not overwrite an existing row)", a.SystemPrompt)
	}
}

// TestIngestAgentDefinition_ExplicitReimportStillSyncs proves the other half
// of TASKS/phase-1/08's contract: IngestAgentDefinition -- the explicit,
// deliberate reimport path used by AgentConfigService.writeManaged /
// SaveManagedAgentProfile immediately after a managed agent's file is
// written -- is NOT subject to the boot-time freeze. It always
// content-syncs, even when a row already exists under the same source; this
// is the one legitimate "pull this file's content into the DB" action the
// task explicitly preserves.
func TestIngestAgentDefinition_ExplicitReimportStillSyncs(t *testing.T) {
	st := newIngestTestStore(t)

	def := &agentpkg.Definition{
		Slug:         "managed-agent",
		Name:         "Managed Agent",
		SystemPrompt: "v1 prompt",
		Source:       "user",
	}
	if err := IngestAgentDefinition(st, def); err != nil {
		t.Fatalf("IngestAgentDefinition (create): %v", err)
	}

	def.SystemPrompt = "v2 prompt — deliberate edit"
	if err := IngestAgentDefinition(st, def); err != nil {
		t.Fatalf("IngestAgentDefinition (reimport): %v", err)
	}

	a, err := st.GetAgentBySlug("managed-agent")
	if err != nil {
		t.Fatalf("GetAgentBySlug: %v", err)
	}
	if a.SystemPrompt != "v2 prompt — deliberate edit" {
		t.Errorf("SystemPrompt: got %q, want the deliberate reimport's updated value (explicit reimport must not be frozen)", a.SystemPrompt)
	}
}

// TestAutoIngestAgents_DBEditSurvivesBootReingest is TASKS/phase-1/08's
// literal Done-means scenario, simulated at the ingest layer: a DB-side
// edit to an agent's content -- however it landed (REST API PATCH, an
// agent_update self-tool call, etc.) -- must survive a subsequent boot's
// AutoIngestAgents pass, even though the backing definition (standing in
// for the file discovery would re-parse) still carries its original
// content.
func TestAutoIngestAgents_DBEditSurvivesBootReingest(t *testing.T) {
	st := newIngestTestStore(t)

	def := &agentpkg.Definition{
		Slug:         "project-agent",
		Name:         "Project Agent",
		SystemPrompt: "file-authored prompt",
		Source:       "project",
	}
	if n := AutoIngestAgents(st, []*agentpkg.Definition{def}, nil); n != 1 {
		t.Fatalf("expected 1 ingested agent, got %d", n)
	}

	// Simulate a DB-side edit landing outside the file-parse path.
	if _, err := st.DB.Exec(
		`UPDATE agent_profiles SET system_prompt = ? WHERE slug = ?`,
		"DB-edited prompt", "project-agent",
	); err != nil {
		t.Fatalf("simulate DB edit: %v", err)
	}

	// Re-run the boot-time pass with the unchanged file-derived def.
	AutoIngestAgents(st, []*agentpkg.Definition{def}, nil)

	a, err := st.GetAgentBySlug("project-agent")
	if err != nil {
		t.Fatalf("GetAgentBySlug: %v", err)
	}
	if a.SystemPrompt != "DB-edited prompt" {
		t.Errorf("SystemPrompt: got %q, want the DB edit to survive the boot-time reingest", a.SystemPrompt)
	}
}

// TestAutoIngestAgents_NewFileStillIngestedAlongsideFrozenRow proves
// TASKS/phase-1/08's explicit non-regression requirement: freezing an
// already-ingested row must not block first-ingest of a genuinely new file
// discovered in the same (or a later) boot-time pass.
func TestAutoIngestAgents_NewFileStillIngestedAlongsideFrozenRow(t *testing.T) {
	st := newIngestTestStore(t)

	existingDef := &agentpkg.Definition{
		Slug:         "already-there",
		Name:         "Already There",
		SystemPrompt: "v1",
		Source:       "project",
	}
	AutoIngestAgents(st, []*agentpkg.Definition{existingDef}, nil)

	// Second boot: the existing def's file content "changed" (must freeze)
	// and a brand new file appeared (must still be created).
	existingDef.SystemPrompt = "v2 -- should be ignored"
	newDef := &agentpkg.Definition{
		Slug:         "brand-new",
		Name:         "Brand New",
		SystemPrompt: "hello",
		Source:       "project",
	}
	n := AutoIngestAgents(st, []*agentpkg.Definition{existingDef, newDef}, nil)
	if n != 2 {
		t.Fatalf("expected both defs to report as ingested (frozen no-op still counts as success), got %d", n)
	}

	frozen, err := st.GetAgentBySlug("already-there")
	if err != nil {
		t.Fatalf("GetAgentBySlug already-there: %v", err)
	}
	if frozen.SystemPrompt != "v1" {
		t.Errorf("already-there SystemPrompt: got %q, want frozen v1", frozen.SystemPrompt)
	}

	created, err := st.GetAgentBySlug("brand-new")
	if err != nil {
		t.Fatalf("GetAgentBySlug brand-new: %v", err)
	}
	if created.SystemPrompt != "hello" {
		t.Errorf("brand-new SystemPrompt: got %q, want %q (first-ingest of a new file must still work)", created.SystemPrompt, "hello")
	}
}

// TestAutoIngestAgents_RolesTableUntouched is TASKS/phase-1/08's negative
// verification for its "role reingest" landmine item: the roles table
// (TASKS/phase-1/01-add-roles-table-and-cascade-resolution.md) has zero
// file-reingest path of its own -- confirmed by code review (grep across
// internal/ found store.CreateRole/UpdateRole called only from
// internal/api/roles.go's REST handlers; no boot-time or file-parse call
// site references either function) and reinforced here at the ingest-pass
// level: running AutoIngestAgents/AutoIngestSkills must never create,
// alter, or seed a roles row.
func TestAutoIngestAgents_RolesTableUntouched(t *testing.T) {
	st := newIngestTestStore(t)

	// The only supported write path for roles is store.CreateRole (the REST
	// API) -- seed one directly so the boot passes below can be proven not
	// to touch it, in either direction (no accidental creation of a second
	// row, no accidental mutation of this one).
	role := &store.Role{Slug: "sme", Name: "Subject Matter Expert", SystemPrompt: "v1"}
	if err := st.CreateRole(role); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	agentDefs := []*agentpkg.Definition{
		{Slug: "some-agent", Name: "Some Agent", SystemPrompt: "x", Source: "project"},
	}
	skillDefs := []*skillpkg.Definition{
		{Slug: "some-skill", Name: "Some Skill", Source: "user", Prompt: "x"},
	}
	AutoIngestAgents(st, agentDefs, nil)
	AutoIngestSkills(st, skillDefs)

	roles, err := st.ListRoles()
	if err != nil {
		t.Fatalf("ListRoles: %v", err)
	}
	if len(roles) != 1 {
		t.Fatalf("expected exactly the 1 directly-seeded role to remain, got %d -- AutoIngestAgents/AutoIngestSkills must never write to roles", len(roles))
	}
	got, err := st.GetRoleBySlug("sme")
	if err != nil || got == nil {
		t.Fatalf("GetRoleBySlug: %v, %v", got, err)
	}
	if got.SystemPrompt != "v1" {
		t.Errorf("SystemPrompt: got %q, want unchanged %q (AutoIngestAgents/AutoIngestSkills must never write to roles)", got.SystemPrompt, "v1")
	}
}

// TestAutoIngestAgents_SourceFlipFromBuiltinToInternal verifies that the
// boot-time sync (which carries def.Source = "internal" for embedded
// internal profiles) flips the `source` column on an already-deployed row
// that previously carried `source='builtin'`. Without this guarantee the
// Wave 2 cleanup migration (CW-20260512-0112, DELETE WHERE source !=
// 'internal') would wipe these rows on the next operator boot.
//
// CW-20260512-0111 W1 regression guard.
func TestAutoIngestAgents_SourceFlipFromBuiltinToInternal(t *testing.T) {
	st := newIngestTestStore(t)

	// Use a synthetic slug so we don't collide with migration 060's
	// INSERT OR IGNORE seed of the four canonical internal profile slugs
	// (default / worker / planner / hint-selector). The behaviour under
	// test is the source-column flip itself, not its application to a
	// specific slug.
	const slug = "test-internal-profile-flip"

	// Seed an already-deployed-shaped row with source='builtin' (the value
	// in user DBs before W1 lands).
	existing := &store.AgentProfile{
		ID:           "blt-test-flip-001",
		Name:         "Flip Target",
		Slug:         slug,
		SystemPrompt: "legacy body",
		Source:       "builtin",
		SourceRef:    "embedded:legacy-path",
	}
	if err := st.CreateAgent(existing); err != nil {
		t.Fatalf("seed CreateAgent: %v", err)
	}

	// Boot-time sync simulates the InternalProfiles() output: source flips
	// to 'internal', source_ref points to the new embedded path, body
	// updates to the file-SOT content.
	def := &agentpkg.Definition{
		Slug:         slug,
		Name:         "Flip Target",
		SystemPrompt: "new body from internal/agent/builtin/profiles/<slug>.md",
		Source:       "internal",
		SourceRef:    "embedded:profiles/" + slug + ".md",
	}
	if n := AutoIngestAgents(st, []*agentpkg.Definition{def}, nil); n != 1 {
		t.Fatalf("AutoIngestAgents count: got %d, want 1", n)
	}

	got, err := st.GetAgentBySlug(slug)
	if err != nil {
		t.Fatalf("GetAgentBySlug: %v", err)
	}
	if got.ID != "blt-test-flip-001" {
		t.Errorf("ID: got %q, want preserved blt-test-flip-001", got.ID)
	}
	if got.Source != "internal" {
		t.Errorf("Source: got %q, want 'internal' (Wave 2 cleanup keeps off this value)", got.Source)
	}
	if got.SourceRef != "embedded:profiles/"+slug+".md" {
		t.Errorf("SourceRef: got %q, want embedded:profiles/%s.md", got.SourceRef, slug)
	}
	if got.SystemPrompt != "new body from internal/agent/builtin/profiles/<slug>.md" {
		t.Errorf("SystemPrompt: got %q, want updated file-SOT body", got.SystemPrompt)
	}

	// TASKS/phase-1/08 extension: once the row has flipped to source=
	// 'internal', a further boot-time pass must freeze it -- the flip is a
	// one-time provenance transition, not a standing sync. Without this
	// third pass, this test would only prove the flip works, not that
	// upsertAgentDef's freeze actually engages afterward.
	def.SystemPrompt = "third body -- should be ignored, row is now frozen"
	if n := AutoIngestAgents(st, []*agentpkg.Definition{def}, nil); n != 1 {
		t.Fatalf("AutoIngestAgents count (3rd pass): got %d, want 1", n)
	}
	gotAfterFreeze, err := st.GetAgentBySlug(slug)
	if err != nil {
		t.Fatalf("GetAgentBySlug (3rd pass): %v", err)
	}
	if gotAfterFreeze.SystemPrompt != "new body from internal/agent/builtin/profiles/<slug>.md" {
		t.Errorf("SystemPrompt after freeze: got %q, want the 2nd-pass body to stay frozen", gotAfterFreeze.SystemPrompt)
	}
}

// TestAutoIngestSkills_EmptyDefsIsNoOp verifies that passing an empty def slice
// returns 0 and doesn't error.
func TestAutoIngestSkills_EmptyDefsIsNoOp(t *testing.T) {
	st := newIngestTestStore(t)
	n := AutoIngestSkills(st, nil)
	if n != 0 {
		t.Errorf("expected 0 for nil input, got %d", n)
	}
}

// TestAutoIngestAgents_EmptyDefsIsNoOp verifies that passing an empty def slice
// returns 0 and doesn't error.
func TestAutoIngestAgents_EmptyDefsIsNoOp(t *testing.T) {
	st := newIngestTestStore(t)
	n := AutoIngestAgents(st, nil, nil)
	if n != 0 {
		t.Errorf("expected 0 for nil input, got %d", n)
	}
}

// TestAutoIngestSkills_StoresModeSlugsUnresolved is the E2 (CW-20260428-0017)
// happy-path, updated for Phase 0 item 21 ("Cut Modes, in full"): a skill
// frontmatter with `modes: [plan, work]` is ingested with those slugs
// serialized directly into mode_ids. Before the cut, resolveSkillModeIDs
// resolved each slug against the now-deleted `modes` catalog table (dropping
// unresolved slugs); there is no more catalog to resolve against, so the
// slugs are now stored as their own identity, unresolved — see
// resolveSkillModeIDs's doc comment in ingest.go.
func TestAutoIngestSkills_StoresModeSlugsUnresolved(t *testing.T) {
	st := newIngestTestStore(t)

	defs := []*skillpkg.Definition{
		{
			Name:        "Plan-Bound Skill",
			Slug:        "plan-bound",
			Description: "only in plan",
			Source:      "user",
			Modes:       []string{"plan", "nonexistent"},
		},
		{
			Name:   "Universal Skill",
			Slug:   "universal-skill",
			Source: "user",
		},
	}
	if n := AutoIngestSkills(st, defs); n != 2 {
		t.Fatalf("expected 2 ingested, got %d", n)
	}

	planSkill, err := st.GetSkillBySlug("plan-bound")
	if err != nil {
		t.Fatalf("GetSkillBySlug plan-bound: %v", err)
	}
	if planSkill == nil {
		t.Fatal("plan-bound not in DB")
	}
	gotIDs := store.ParseSkillModeIDs(planSkill.ModeIDs)
	wantIDs := []string{"plan", "nonexistent"}
	if len(gotIDs) != len(wantIDs) {
		t.Fatalf("plan-bound mode_ids: got %v, want %v", gotIDs, wantIDs)
	}
	for i, want := range wantIDs {
		if gotIDs[i] != want {
			t.Errorf("plan-bound mode_ids[%d] = %q, want %q", i, gotIDs[i], want)
		}
	}

	universal, err := st.GetSkillBySlug("universal-skill")
	if err != nil {
		t.Fatalf("GetSkillBySlug universal-skill: %v", err)
	}
	if universal == nil {
		t.Fatal("universal-skill not in DB")
	}
	if universal.ModeIDs != "[]" {
		t.Errorf("universal mode_ids should be empty, got %q", universal.ModeIDs)
	}
}
