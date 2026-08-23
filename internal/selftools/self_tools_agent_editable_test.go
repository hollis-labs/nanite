package selftools

import (
	"context"
	"strings"
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

func TestAgentProfileTools_UsesInjectedClassifier(t *testing.T) {
	st := newSelfTools(t)
	seeded := seedAgent(t, st.Store, "Managed by fallback", "injected-classifier", "", "")
	classifier := &recordingAgentClassifier{class: agent.ManageClassPlugin}
	st.AgentProfileTools.Classifier = classifier

	result, err := st.CallTool(t.Context(), "agent_update", map[string]any{
		"id": seeded.ID, "name": "must not update",
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !result.IsError || !strings.Contains(result.Content[0].Text, "plugin/vendor-provided") {
		t.Fatalf("result = %#v, want injected plugin classification rejection", result)
	}
	if classifier.called != 1 {
		t.Fatalf("classifier calls = %d, want 1", classifier.called)
	}
}

// TestSelfToolsTransport_UpdateAgent_RejectsNonEditableClasses verifies
// agent_update rejects a write against each non-editable ManageClass
// (internal/plugin/external) with a clear error, and leaves the profile
// untouched.
func TestSelfToolsTransport_UpdateAgent_RejectsNonEditableClasses(t *testing.T) {
	cases := []struct {
		name      string
		source    string
		sourceRef string
		wantSnip  string
	}{
		{
			name:     "internal",
			source:   "internal",
			wantSnip: "embedded internal harness profile",
		},
		{
			name:     "plugin",
			source:   "plugin",
			wantSnip: "plugin/vendor-provided",
		},
		{
			// No configured managed roots (AgentClassifier unwired in this
			// test), and a source string outside the recognized
			// user/project/managed_file/cli set — classifies External.
			name:      "external",
			source:    "adapter",
			sourceRef: "/opt/external/agents/foo.md",
			wantSnip:  "not in a writable managed location",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newSelfTools(t)
			ctx := context.Background()

			seeded := seedAgent(t, st.Store, "Seed Agent", "seed-"+tc.name, tc.source, tc.sourceRef)

			result, err := st.CallTool(ctx, "agent_update", map[string]any{
				"id":            seeded.ID,
				"name":          "Overwritten Name",
				"system_prompt": "overwritten prompt",
			})
			if err != nil {
				t.Fatalf("unexpected transport error: %v", err)
			}
			if !result.IsError {
				t.Fatalf("expected rejection for class %s, got success: %s", tc.name, result.Content[0].Text)
			}
			if !strings.Contains(result.Content[0].Text, tc.wantSnip) {
				t.Errorf("expected error to mention %q, got: %s", tc.wantSnip, result.Content[0].Text)
			}
			if !strings.Contains(result.Content[0].Text, "manage_class") {
				t.Errorf("expected error to surface manage_class, got: %s", result.Content[0].Text)
			}

			// Verify the write never happened.
			after, err := st.Store.GetAgent(context.Background(), seeded.ID)
			if err != nil {
				t.Fatalf("get agent after rejected update: %v", err)
			}
			if after.Name != "Seed Agent" || after.SystemPrompt != "original prompt" {
				t.Errorf("expected profile to be unchanged after rejected update, got name=%q prompt=%q", after.Name, after.SystemPrompt)
			}
		})
	}
}

// TestSelfToolsTransport_UpdateAgent_AllowsManagedClass verifies a real
// editable (managed-class) agent profile still updates successfully — the
// gate must not become a false-positive block on the common case.
func TestSelfToolsTransport_UpdateAgent_AllowsManagedClass(t *testing.T) {
	st := newSelfTools(t)
	ctx := context.Background()

	// Empty source/source_ref classifies ManageClassManaged (DB-only
	// operator agent) per internal/agent.Classification.Classify.
	seeded := seedAgent(t, st.Store, "Managed Agent", "managed-agent", "", "")

	result, err := st.CallTool(ctx, "agent_update", map[string]any{
		"id":            seeded.ID,
		"name":          "Updated Managed Agent",
		"system_prompt": "updated prompt",
	})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected managed-class update to succeed, got error: %s", result.Content[0].Text)
	}

	after, err := st.Store.GetAgent(context.Background(), seeded.ID)
	if err != nil {
		t.Fatalf("get agent after update: %v", err)
	}
	if after.Name != "Updated Managed Agent" || after.SystemPrompt != "updated prompt" {
		t.Errorf("expected profile to be updated, got name=%q prompt=%q", after.Name, after.SystemPrompt)
	}
}

// TestSelfToolsTransport_CreateAgent_RejectsSlugCollisionWithNonEditableClasses
// verifies agent_create rejects creating/overwriting an agent whose slug
// collides with an existing non-editable (internal/plugin/external)
// profile, with a clear error, and that the original profile is untouched.
func TestSelfToolsTransport_CreateAgent_RejectsSlugCollisionWithNonEditableClasses(t *testing.T) {
	cases := []struct {
		name      string
		source    string
		sourceRef string
		wantSnip  string
	}{
		{
			name:     "internal",
			source:   "internal",
			wantSnip: "embedded internal harness profile",
		},
		{
			name:     "plugin",
			source:   "plugin",
			wantSnip: "plugin/vendor-provided",
		},
		{
			name:      "external",
			source:    "adapter",
			sourceRef: "/opt/external/agents/bar.md",
			wantSnip:  "not in a writable managed location",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newSelfTools(t)
			ctx := context.Background()

			slug := "collide-" + tc.name
			seeded := seedAgent(t, st.Store, "Seed Agent", slug, tc.source, tc.sourceRef)

			result, err := st.CallTool(ctx, "agent_create", map[string]any{
				"name":          "Fabricated Agent",
				"slug":          slug,
				"system_prompt": "fabricated prompt",
			})
			if err != nil {
				t.Fatalf("unexpected transport error: %v", err)
			}
			if !result.IsError {
				t.Fatalf("expected rejection for class %s, got success: %s", tc.name, result.Content[0].Text)
			}
			if !strings.Contains(result.Content[0].Text, tc.wantSnip) {
				t.Errorf("expected error to mention %q, got: %s", tc.wantSnip, result.Content[0].Text)
			}

			// Verify no fabricated profile was written and the original is
			// untouched.
			after, err := st.Store.GetAgent(context.Background(), seeded.ID)
			if err != nil {
				t.Fatalf("get agent after rejected create: %v", err)
			}
			if after.Name != "Seed Agent" || after.SystemPrompt != "original prompt" {
				t.Errorf("expected original profile to be unchanged, got name=%q prompt=%q", after.Name, after.SystemPrompt)
			}
		})
	}
}

// TestSelfToolsTransport_CreateAgent_ManagedCollisionNotBlockedByGate proves
// the new editability pre-check specifically distinguishes editable from
// non-editable — colliding with an existing Managed-class profile is NOT
// rejected by the gate (it still fails, but at the store's pre-existing slug
// UNIQUE constraint, a distinct and unrelated error).
func TestSelfToolsTransport_CreateAgent_ManagedCollisionNotBlockedByGate(t *testing.T) {
	st := newSelfTools(t)
	ctx := context.Background()

	slug := "collide-managed"
	seedAgent(t, st.Store, "Seed Agent", slug, "", "")

	result, err := st.CallTool(ctx, "agent_create", map[string]any{
		"name":          "Duplicate Slug Agent",
		"slug":          slug,
		"system_prompt": "duplicate prompt",
	})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected slug collision to still fail (pre-existing UNIQUE constraint), got success: %s", result.Content[0].Text)
	}
	if strings.Contains(result.Content[0].Text, "manage_class") {
		t.Errorf("managed-class collision should not be rejected by the editability gate, got: %s", result.Content[0].Text)
	}
}

// TestSelfToolsTransport_CreateAgent_NoCollisionStillSucceeds is a smoke
// check that the new pre-check doesn't affect the ordinary no-collision
// create path (the gate only fires when GetAgentBySlug finds an existing
// row). Complements TestSelfToolsTransport_CreateAgent in self_tools_test.go.
func TestSelfToolsTransport_CreateAgent_NoCollisionStillSucceeds(t *testing.T) {
	st := newSelfTools(t)
	ctx := context.Background()

	result, err := st.CallTool(ctx, "agent_create", map[string]any{
		"name":          "Fresh Agent",
		"slug":          "fresh-agent-no-collision",
		"system_prompt": "fresh prompt",
	})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected create with no slug collision to succeed, got error: %s", result.Content[0].Text)
	}
}
