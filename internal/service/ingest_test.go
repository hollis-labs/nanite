package service

// J7 (CW-20260421-0011): tests for the skills/agents DB ingestion pipeline.

import (
	"path/filepath"
	"testing"

	agentpkg "github.com/hollis-labs/nanite/internal/agent"
	skillpkg "github.com/hollis-labs/nanite/internal/skill"
	"github.com/hollis-labs/nanite/internal/store"
)

func newIngestTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.New(filepath.Join(t.TempDir(), "test.db"))
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

// TestAutoIngestSkills_VersionBumpsOnContentChange verifies that re-ingesting
// with a changed prompt bumps the version counter.
func TestAutoIngestSkills_VersionBumpsOnContentChange(t *testing.T) {
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
	if sk.Version != 2 {
		t.Errorf("Version should be 2 after content change, got %d", sk.Version)
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

	n := AutoIngestAgents(st, defs)
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
	AutoIngestAgents(st, defs)

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
	AutoIngestAgents(st, defs)

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
	AutoIngestAgents(st, defs)

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

// TestAutoIngestAgents_UpdateOnReingest verifies that re-ingesting an agent
// with changed system prompt updates the DB row.
func TestAutoIngestAgents_UpdateOnReingest(t *testing.T) {
	st := newIngestTestStore(t)

	def := &agentpkg.Definition{
		Slug:         "update-agent",
		Name:         "Update Agent",
		SystemPrompt: "v1 prompt",
		Source:       "user",
	}
	AutoIngestAgents(st, []*agentpkg.Definition{def})

	// Change the system prompt and re-ingest.
	def.SystemPrompt = "v2 prompt — updated"
	AutoIngestAgents(st, []*agentpkg.Definition{def})

	a, err := st.GetAgentBySlug("update-agent")
	if err != nil {
		t.Fatalf("GetAgentBySlug: %v", err)
	}
	if a.SystemPrompt != "v2 prompt — updated" {
		t.Errorf("SystemPrompt: got %q, want updated value", a.SystemPrompt)
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
	n := AutoIngestAgents(st, nil)
	if n != 0 {
		t.Errorf("expected 0 for nil input, got %d", n)
	}
}

// TestAutoIngestSkills_ResolvesModeSlugsToIDs is the E2 (CW-20260428-0017)
// happy-path: a skill frontmatter with `modes: [plan, work]` is ingested with
// the corresponding mode IDs serialized into mode_ids. Unknown slugs are
// dropped silently (warnings logged) so a typo doesn't crash boot.
func TestAutoIngestSkills_ResolvesModeSlugsToIDs(t *testing.T) {
	st := newIngestTestStore(t)
	plan := &store.Mode{Slug: "plan", Name: "Plan"}
	work := &store.Mode{Slug: "work", Name: "Work"}
	if err := st.CreateMode(plan); err != nil {
		t.Fatalf("CreateMode plan: %v", err)
	}
	if err := st.CreateMode(work); err != nil {
		t.Fatalf("CreateMode work: %v", err)
	}

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
	if len(gotIDs) != 1 || gotIDs[0] != plan.ID {
		t.Fatalf("plan-bound mode_ids: got %v, want [%q]", gotIDs, plan.ID)
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
