package service

import (
	"context"
	"path/filepath"
	"testing"

	agentpkg "github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/agentimport"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

func newGuardTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := storetest.New(t, context.Background(), filepath.Join(t.TempDir(), "guard.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return s
}

// TestAutoIngestAgents_BootPassNeverStompsImportedRow is the regression for
// the collision probed on 2026-09-10 while landing CW-20260910-0009.
//
// upsertAgentDef freezes an already-ingested row, but carves out a genuine
// provenance transition so the historical builtin -> internal
// reclassification could sync once. An imported row's provenance never
// matches a compiled-in seed's, so before sourceTransitionSyncs existed that
// carve-out fired on EVERY boot: the imported content was replaced and its
// source flipped back to "internal", silently. The seed slugs this could hit
// are ordinary words -- `reviewer`, `planner`, `worker`, `researcher`,
// `backend` are all real internal profiles.
//
// agentimport refuses the collision on the way in, which closes today's case;
// this guard closes the one it cannot see -- a FUTURE Nanite release adding a
// seed whose slug an operator already imported.
func TestAutoIngestAgents_BootPassNeverStompsImportedRow(t *testing.T) {
	st := newGuardTestStore(t)
	ctx := context.Background()

	// An operator import lands first, under a slug no seed uses yet.
	imported := &store.AgentProfile{
		Name:         "Imported Reviewer",
		Slug:         "reviewer",
		SystemPrompt: "imported prompt",
		Source:       agentimport.SourceProvenance,
		SourceRef:    "/somewhere/reviewer.md",
	}
	if err := st.CreateAgent(ctx, imported); err != nil {
		t.Fatalf("seed imported row: %v", err)
	}

	// A later release ships a compiled-in seed using that same slug. Boot.
	seed := &agentpkg.Definition{
		Slug:         "reviewer",
		Name:         "Reviewer",
		SystemPrompt: "compiled-in seed prompt",
		Source:       "internal",
	}
	AutoIngestAgents(st, []*agentpkg.Definition{seed}, nil)

	after, err := st.GetAgentBySlug(ctx, "reviewer")
	if err != nil {
		t.Fatalf("GetAgentBySlug: %v", err)
	}
	if after.Source != agentimport.SourceProvenance {
		t.Errorf("source = %q, want the imported provenance %q preserved — a boot pass must never claim ownership of an imported row",
			after.Source, agentimport.SourceProvenance)
	}
	if after.SystemPrompt != "imported prompt" {
		t.Errorf("SystemPrompt = %q, want the imported content preserved", after.SystemPrompt)
	}
	if got := agentpkg.NewClassification().Classify(after.Source); got != agentpkg.ManageClassExternal {
		t.Errorf("row classifies as %q after the boot pass, want external", got)
	}
}

// TestSourceTransitionSyncs pins the guard's decision table directly, so the
// legitimate historical transition the carve-out exists for stays allowed.
func TestSourceTransitionSyncs(t *testing.T) {
	for _, tc := range []struct {
		existing, incoming string
		want               bool
		why                string
	}{
		{"builtin", "internal", true, "the historical reclassification the carve-out exists for"},
		{"internal", "internal", false, "an already-seeded row is frozen"},
		{"user", "internal", true, "a managed row is not what this guard protects"},
		{agentimport.SourceProvenance, "internal", false, "a boot pass must never claim an imported row"},
		{"claude", "internal", false, "any external provenance, not just the literal import marker"},
		{agentimport.SourceProvenance, agentimport.SourceProvenance, false, "identical provenance is frozen either way"},
	} {
		if got := sourceTransitionSyncs(tc.existing, tc.incoming); got != tc.want {
			t.Errorf("sourceTransitionSyncs(%q, %q) = %v, want %v — %s",
				tc.existing, tc.incoming, got, tc.want, tc.why)
		}
	}
}
