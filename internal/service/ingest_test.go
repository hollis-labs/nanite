package service

// J7 (CW-20260421-0011): tests for the agent DB ingestion pipeline.
//
// TASKS/skills/01: this file used to also cover AutoIngestSkills/
// upsertSkillDef — deleted along with those functions (see
// docs/engineering/architecture/20-skills.md's "Migration" section), so the
// internal/skill import is gone too.

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	agentpkg "github.com/hollis-labs/nanite/internal/agent"
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

// TASKS/skills/01: every AutoIngestSkills/upsertSkillDef test formerly here
// (insert, idempotent reingest, content-freeze, provenance-transition sync)
// is deleted along with the functions themselves — see
// docs/engineering/architecture/20-skills.md's "Migration: clean slate, no
// carried-forward content" section. There is no more file-based skill
// ingestion pass to regression-test.

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

// TestAutoIngestAgents_DirectDBMutationToInternalProfileSurvivesBootReingest
// is TASKS/phase-2/05-freeze-internal-agent-profiles-on-reingest.md's own
// literal Done-means scenario, targeted specifically at source="internal"
// (the profile class that task exists to protect) rather than the generic
// source used by TestAutoIngestAgents_DBEditSurvivesBootReingest above.
// upsertAgentDef's freeze (TASKS/phase-1/08, `bootPass && existing.Source ==
// profile.Source`) is fully source-agnostic and already covers this case as
// a special case of the general rule -- proven indirectly by
// TestAutoIngestAgents_SourceFlipFromBuiltinToInternal's third pass (a
// changed in-memory def is ignored once frozen at source="internal"). This
// test closes the remaining literal gap: a mutation applied directly to the
// DB row (standing in for however the edit landed -- REST PATCH, the
// agent_update self-tool, manual SQL) rather than via a second
// AutoIngestAgents call with a mutated def.
func TestAutoIngestAgents_DirectDBMutationToInternalProfileSurvivesBootReingest(t *testing.T) {
	st := newIngestTestStore(t)

	def := &agentpkg.Definition{
		Slug:         "internal-profile-under-test",
		Name:         "Internal Profile Under Test",
		SystemPrompt: "compiled-in prompt v1",
		Source:       "internal",
	}
	if n := AutoIngestAgents(st, []*agentpkg.Definition{def}, nil); n != 1 {
		t.Fatalf("expected 1 ingested agent, got %d", n)
	}

	// Simulate a DB-side edit landing outside the file-parse path (e.g. a
	// PATCH through the REST API, or -- pre-task-34 -- the agent_update
	// self-tool).
	if _, err := st.DB.Exec(
		`UPDATE agent_profiles SET system_prompt = ? WHERE slug = ?`,
		"DB-edited prompt", "internal-profile-under-test",
	); err != nil {
		t.Fatalf("simulate DB edit: %v", err)
	}

	// Re-run the boot-time pass with the unchanged file-derived def (the
	// compiled-in builtin/internal profile content never changed).
	if n := AutoIngestAgents(st, []*agentpkg.Definition{def}, nil); n != 1 {
		t.Fatalf("expected 1 ingested agent on re-run, got %d", n)
	}

	a, err := st.GetAgentBySlug("internal-profile-under-test")
	if err != nil {
		t.Fatalf("GetAgentBySlug: %v", err)
	}
	if a.SystemPrompt != "DB-edited prompt" {
		t.Errorf("SystemPrompt: got %q, want the DB edit to survive the boot-time reingest of a source=internal profile", a.SystemPrompt)
	}
}

// TestAutoIngestAgents_NewFileStillIngestedAlongsideFrozenRow proves
// TASKS/phase-1/08's explicit non-regression requirement: freezing an
// already-ingested row must not block first-ingest of a genuinely new file
// discovered in the same (or a later) boot-time pass.
// TestAutoIngestAgents_NewInternalDefStillIngestedAlongsideFrozenRow is the
// Round 2 rewrite of the original
// TestAutoIngestAgents_NewFileStillIngestedAlongsideFrozenRow (TASKS/phase-1/08
// Round 1). That test proved a brand-new *project*-sourced file, discovered
// alongside an already-frozen row in the same boot batch, still got
// first-ingested — exactly the standing "keep discovering and ingesting new
// project/user/plugin files forever" behavior Round 2 removes at the
// agent.Discover layer (a file dropped into .nanite/agents/ is no longer
// discovered at all, so AutoIngestAgents never sees such a def in practice).
//
// AutoIngestAgents itself is still source-agnostic, though — it ingests
// whatever []*Definition it's handed, regardless of where the caller got the
// list from. That generic behavior is still real and still load-bearing:
// when a new release ships an additional compiled-in builtin/internal
// profile, AutoIngestAgents' next boot-time pass must still create that new
// row even though every other already-ingested internal profile in the same
// batch stays frozen. This rewrite proves exactly that scenario, using
// Source: "internal" (a source that can still legitimately produce a new def
// post-cut) instead of "project" (which no longer reaches AutoIngestAgents
// at all).
func TestAutoIngestAgents_NewInternalDefStillIngestedAlongsideFrozenRow(t *testing.T) {
	st := newIngestTestStore(t)

	existingDef := &agentpkg.Definition{
		Slug:         "already-there",
		Name:         "Already There",
		SystemPrompt: "v1",
		Source:       "internal",
	}
	AutoIngestAgents(st, []*agentpkg.Definition{existingDef}, nil)

	// Second boot: the existing def's compiled content "changed" (must
	// freeze) and a new internal profile shipped in this release (must
	// still be created).
	existingDef.SystemPrompt = "v2 -- should be ignored"
	newDef := &agentpkg.Definition{
		Slug:         "brand-new",
		Name:         "Brand New",
		SystemPrompt: "hello",
		Source:       "internal",
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
		t.Errorf("brand-new SystemPrompt: got %q, want %q (a new internal/builtin def in the same batch must still ingest)", created.SystemPrompt, "hello")
	}
}

// TestAutoIngestAgents_RolesTableUntouched is TASKS/phase-1/08's negative
// verification for its "role reingest" landmine item: the roles table
// (TASKS/phase-1/01-add-roles-table-and-cascade-resolution.md) has zero
// file-reingest path of its own -- confirmed by code review (grep across
// internal/ found store.CreateRole/UpdateRole called only from
// internal/api/roles.go's REST handlers; no boot-time or file-parse call
// site references either function) and reinforced here at the ingest-pass
// level: running AutoIngestAgents must never create, alter, or seed a roles
// row.
//
// TASKS/skills/01: this test used to also run AutoIngestSkills over a
// skillDefs slice as a second negative check -- that function no longer
// exists (skill file-reingest is cut in full), so the roles-table assertion
// now covers AutoIngestAgents alone.
func TestAutoIngestAgents_RolesTableUntouched(t *testing.T) {
	st := newIngestTestStore(t)

	// The only supported write path for roles is store.CreateRole (the REST
	// API) -- seed one directly so the boot pass below can be proven not to
	// touch it, in either direction (no accidental creation of a second row,
	// no accidental mutation of this one).
	role := &store.Role{Slug: "sme", Name: "Subject Matter Expert", SystemPrompt: "v1"}
	if err := st.CreateRole(role); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	agentDefs := []*agentpkg.Definition{
		{Slug: "some-agent", Name: "Some Agent", SystemPrompt: "x", Source: "project"},
	}
	AutoIngestAgents(st, agentDefs, nil)

	roles, err := st.ListRoles()
	if err != nil {
		t.Fatalf("ListRoles: %v", err)
	}
	if len(roles) != 1 {
		t.Fatalf("expected exactly the 1 directly-seeded role to remain, got %d -- AutoIngestAgents must never write to roles", len(roles))
	}
	got, err := st.GetRoleBySlug("sme")
	if err != nil || got == nil {
		t.Fatalf("GetRoleBySlug: %v, %v", got, err)
	}
	if got.SystemPrompt != "v1" {
		t.Errorf("SystemPrompt: got %q, want unchanged %q (AutoIngestAgents must never write to roles)", got.SystemPrompt, "v1")
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

// TestAutoIngestAgents_EmptyDefsIsNoOp verifies that passing an empty def slice
// returns 0 and doesn't error.
func TestAutoIngestAgents_EmptyDefsIsNoOp(t *testing.T) {
	st := newIngestTestStore(t)
	n := AutoIngestAgents(st, nil, nil)
	if n != 0 {
		t.Errorf("expected 0 for nil input, got %d", n)
	}
}

// TASKS/skills/01: TestAutoIngestSkills_EmptyDefsIsNoOp and
// TestAutoIngestSkills_StoresModeSlugsUnresolved (the E2/CW-20260428-0017
// mode-slug-storage regression) are deleted along with AutoIngestSkills
// itself — see docs/engineering/architecture/20-skills.md's "Migration"
// section. store.ParseSkillModeIDs/MarshalSkillModeIDs/SkillMatchesMode are
// untouched (out of this task's scope — skill_mode_filter.go's own doc
// comment already flags them as surviving, independently-useful primitives
// with no other production caller today); only the AutoIngestSkills-side
// writer of skills.mode_ids is gone.
