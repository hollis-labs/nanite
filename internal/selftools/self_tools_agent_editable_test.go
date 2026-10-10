package selftools

import (
	"context"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/store"
)

type recordingAgentClassifier struct {
	class  agent.ManageClass
	called int
}

func (c *recordingAgentClassifier) Classify(*store.AgentProfile) agent.ManageClass {
	c.called++
	return c.class
}

// Task 34: agent_update/agent_create must reject writes against non-editable
// (internal/plugin/external) agent profiles, matching the gate
// internal/api/agent_capabilities.go's requireMutableAgent already enforces
// at the REST layer. These tests exercise all four internal/agent.ManageClass
// values against both self-tools with no AgentClassifier wired, proving the
// nil-safe fallback in classifyAgent (a zero-value agent.Classification)
// still enforces the gate correctly.

// seedAgent inserts an agent profile directly via the store (bypassing the
// agent_create self-tool, which never lets a caller set source/source_ref)
// so tests can set up profiles of any ManageClass.
func seedAgent(t *testing.T, s *store.Store, name, slug, source, sourceRef string) *store.AgentProfile {
	t.Helper()
	a := &store.AgentProfile{
		Name:         name,
		Slug:         slug,
		SystemPrompt: "original prompt",
		Description:  "original description",
		Source:       source,
		SourceRef:    sourceRef,
	}
	if err := s.CreateAgent(context.Background(), a); err != nil {
		t.Fatalf("seed agent %s: %v", slug, err)
	}
	return a
}

func TestAgentProfileTools_RetirementPrecedesInjectedClassifier(t *testing.T) {
	st := newSelfTools(t)
	db := fixtureStore(st)
	profile := retainedSelfToolProfile(t, db, "injected-classifier", "user")
	classifier := &recordingAgentClassifier{class: agent.ManageClassPlugin}
	st.AgentProfileTools.Classifier = classifier
	before := retainedSelfToolState(t, db)
	assertRetiredSelfTool(t, st, "agent_update", map[string]any{"id": profile.ID, "name": "must not update"})
	assertRetiredSelfTool(t, st, "agent_create", map[string]any{"name": "must not create", "slug": profile.Slug})
	if classifier.called != 0 {
		t.Fatalf("retirement consulted classifier %d times", classifier.called)
	}
	assertRetainedSelfToolState(t, db, before)
}

// Retired mutable-profile tools refuse every provenance class before writes.
func TestSelfToolsTransport_UpdateAgent_RejectsNonEditableClasses(t *testing.T) {
	for _, source := range []string{"internal", "plugin", "adapter"} {
		t.Run(source, func(t *testing.T) {
			st := newSelfTools(t)
			db := fixtureStore(st)
			profile := retainedSelfToolProfile(t, db, "seed-"+source, source)
			before := retainedSelfToolState(t, db)
			assertRetiredSelfTool(t, st, "agent_update", map[string]any{"id": profile.ID, "name": "Overwritten Name", "system_prompt": "overwritten prompt"})
			assertRetainedSelfToolState(t, db, before)
		})
	}
}

// Retired mutable-profile tools refuse every provenance class before writes.
func TestSelfToolsTransport_UpdateAgent_ManagedHistoryRefused(t *testing.T) {
	st := newSelfTools(t)
	db := fixtureStore(st)
	profile := retainedSelfToolProfile(t, db, "managed-agent", "user")
	before := retainedSelfToolState(t, db)
	assertRetiredSelfTool(t, st, "agent_update", map[string]any{"id": profile.ID, "name": "Updated Managed Agent", "system_prompt": "updated prompt"})
	assertRetainedSelfToolState(t, db, before)
}

// Retired mutable-profile tools refuse every provenance class before writes.
func TestSelfToolsTransport_CreateAgent_RejectsSlugCollisionWithNonEditableClasses(t *testing.T) {
	for _, source := range []string{"internal", "plugin", "adapter"} {
		t.Run(source, func(t *testing.T) {
			st := newSelfTools(t)
			db := fixtureStore(st)
			profile := retainedSelfToolProfile(t, db, "collide-"+source, source)
			before := retainedSelfToolState(t, db)
			assertRetiredSelfTool(t, st, "agent_create", map[string]any{"name": "Fabricated Agent", "slug": profile.Slug, "system_prompt": "fabricated prompt"})
			assertRetainedSelfToolState(t, db, before)
		})
	}
}

// Retired mutable-profile tools refuse every provenance class before writes.
func TestSelfToolsTransport_CreateAgent_ManagedCollisionRefused(t *testing.T) {
	st := newSelfTools(t)
	db := fixtureStore(st)
	profile := retainedSelfToolProfile(t, db, "collide-managed", "user")
	before := retainedSelfToolState(t, db)
	assertRetiredSelfTool(t, st, "agent_create", map[string]any{"name": "Duplicate Slug Agent", "slug": profile.Slug, "system_prompt": "duplicate prompt"})
	assertRetainedSelfToolState(t, db, before)
}

// Retired mutable-profile tools refuse every provenance class before writes.
func TestSelfToolsTransport_CreateAgent_NoCollisionStillRefused(t *testing.T) {
	st := newSelfTools(t)
	db := fixtureStore(st)
	before := retainedSelfToolState(t, db)
	assertRetiredSelfTool(t, st, "agent_create", map[string]any{"name": "Fresh Agent", "slug": "fresh-agent-no-collision", "system_prompt": "fresh prompt"})
	assertRetainedSelfToolState(t, db, before)
}
