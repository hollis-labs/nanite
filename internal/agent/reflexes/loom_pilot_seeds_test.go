package reflexes

import (
	"context"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

// seedLoomPilotAgents creates loom-weaver (advisor) and loom-curator
// (process) agent_profiles rows so GetAgentBySlug resolves the way it
// would once the profiles have been provisioned in the database.
func seedLoomPilotAgents(t *testing.T, st *store.Store) (weaverID, curatorID string) {
	t.Helper()
	if err := st.CreateAgent(context.Background(), &store.AgentProfile{
		ID:           "profile-loom-weaver",
		Name:         "Loom Weaver",
		Slug:         "loom-weaver",
		Class:        "advisor",
		SystemPrompt: "test",
		Source:       "project",
	}); err != nil {
		t.Fatalf("CreateAgent(loom-weaver): %v", err)
	}
	if err := st.CreateAgent(context.Background(), &store.AgentProfile{
		ID:           "profile-loom-curator",
		Name:         "Loom Curator",
		Slug:         "loom-curator",
		Class:        "process",
		SystemPrompt: "test",
		Source:       "project",
	}); err != nil {
		t.Fatalf("CreateAgent(loom-curator): %v", err)
	}
	return "profile-loom-weaver", "profile-loom-curator"
}

// TestSeedAgentReflexesBySlug_SeedsLoomPilotPairIdempotently proves
// CW-20260816-0023's AgentID-scoped seeding mechanism end to end: once
// loom-weaver's and loom-curator's real agent_profiles rows exist,
// SeedAgentReflexesBySlug(LoomPilotReflexSeeds()) resolves each seed's
// slug to its agent_id, inserts exactly the expected rows scoped to
// AgentID (not ClassTag), and a second call is a true no-op — no
// duplicate rows, and an operator/engine mutation to fired_count /
// last_fired_at on an existing row survives the re-seed untouched
// (same discipline SeedBaseReflexes already established for class-base
// rows).
func TestSeedAgentReflexesBySlug_SeedsLoomPilotPairIdempotently(t *testing.T) {
	ctx := context.Background()
	st := newReflexTestStore(t)
	weaverID, curatorID := seedLoomPilotAgents(t, st)

	inserted, err := SeedAgentReflexesBySlug(ctx, st, LoomPilotReflexSeeds(), nil)
	if err != nil {
		t.Fatalf("SeedAgentReflexesBySlug (first call): %v", err)
	}
	if inserted != 3 {
		t.Fatalf("first seed pass inserted = %d, want 3 (weaver x2 + curator x1)", inserted)
	}

	weaverRows, err := st.ListAllAgentReflexes(ctx, weaverID)
	if err != nil {
		t.Fatalf("ListAllAgentReflexes(weaver): %v", err)
	}
	if len(weaverRows) != 2 {
		t.Fatalf("weaver reflex rows = %d, want 2 (check_before_answer + capture_on_discovery); got %+v", len(weaverRows), weaverRows)
	}
	gotNames := map[string]bool{}
	for _, r := range weaverRows {
		gotNames[r.Name] = true
		if r.AgentID != weaverID {
			t.Fatalf("weaver row %q has AgentID=%q, want %q", r.Name, r.AgentID, weaverID)
		}
		if r.ClassTag != "" {
			t.Fatalf("weaver row %q has ClassTag=%q, want empty (AgentID-scoped, not class-scoped)", r.Name, r.ClassTag)
		}
	}
	if !gotNames["check_before_answer"] || !gotNames["capture_on_discovery"] {
		t.Fatalf("weaver rows missing expected names: %+v", gotNames)
	}

	curatorRows, err := st.ListAllAgentReflexes(ctx, curatorID)
	if err != nil {
		t.Fatalf("ListAllAgentReflexes(curator): %v", err)
	}
	if len(curatorRows) != 1 {
		t.Fatalf("curator reflex rows = %d, want 1 (capture_on_discovery only)", len(curatorRows))
	}
	if curatorRows[0].Name != "capture_on_discovery" {
		t.Fatalf("curator row name = %q, want capture_on_discovery", curatorRows[0].Name)
	}
	if curatorRows[0].AgentID != curatorID {
		t.Fatalf("curator row AgentID = %q, want %q", curatorRows[0].AgentID, curatorID)
	}

	// Simulate the engine having fired one of the rows, so a naive
	// re-seed that upserts (rather than skips) would be caught here.
	fired := weaverRows[0]
	now := time.Now().UTC()
	if err := st.BumpAgentReflexFired(ctx, fired.ID, now); err != nil {
		t.Fatalf("BumpAgentReflexFired: %v", err)
	}

	inserted, err = SeedAgentReflexesBySlug(ctx, st, LoomPilotReflexSeeds(), nil)
	if err != nil {
		t.Fatalf("SeedAgentReflexesBySlug (second call): %v", err)
	}
	if inserted != 0 {
		t.Fatalf("second seed pass inserted = %d, want 0 (idempotent — rows already exist)", inserted)
	}

	weaverRowsAfter, err := st.ListAllAgentReflexes(ctx, weaverID)
	if err != nil {
		t.Fatalf("ListAllAgentReflexes(weaver) after re-seed: %v", err)
	}
	if len(weaverRowsAfter) != 2 {
		t.Fatalf("weaver reflex rows after re-seed = %d, want 2 (no duplicates)", len(weaverRowsAfter))
	}
	var refetched *store.AgentReflex
	for i := range weaverRowsAfter {
		if weaverRowsAfter[i].ID == fired.ID {
			refetched = &weaverRowsAfter[i]
		}
	}
	if refetched == nil {
		t.Fatalf("previously-fired row %q not found after re-seed", fired.ID)
	}
	if refetched.FiredCount != 1 {
		t.Fatalf("re-seed must not reset fired_count: got %d, want 1", refetched.FiredCount)
	}
	if refetched.LastFiredAt == "" {
		t.Fatalf("re-seed must not clear last_fired_at")
	}
}

// TestSeedAgentReflexesBySlug_SkipsUningestedAgentGracefully proves the
// "profile not provisioned yet" case is a graceful skip, not an error.
// This matters because SeedAgentReflexesBySlug runs on every container boot,
// including clean databases where these optional profiles do not exist.
func TestSeedAgentReflexesBySlug_SkipsUningestedAgentGracefully(t *testing.T) {
	ctx := context.Background()
	st := newReflexTestStore(t) // no agent_profiles rows created at all

	inserted, err := SeedAgentReflexesBySlug(ctx, st, LoomPilotReflexSeeds(), nil)
	if err != nil {
		t.Fatalf("SeedAgentReflexesBySlug must not error when target agents aren't ingested yet: %v", err)
	}
	if inserted != 0 {
		t.Fatalf("inserted = %d, want 0 when no target agent profiles exist", inserted)
	}
}

// TestSeedAgentReflexesBySlug_SeedsPartialSetWhenOnlyOneAgentIngested
// proves a mixed boot state — e.g. loom-weaver.md has been ingested but
// loom-curator.md hasn't yet — seeds exactly the subset that resolves,
// skipping the rest without failing the whole pass.
func TestSeedAgentReflexesBySlug_SeedsPartialSetWhenOnlyOneAgentIngested(t *testing.T) {
	ctx := context.Background()
	st := newReflexTestStore(t)
	if err := st.CreateAgent(context.Background(), &store.AgentProfile{
		ID:           "profile-loom-weaver",
		Name:         "Loom Weaver",
		Slug:         "loom-weaver",
		Class:        "advisor",
		SystemPrompt: "test",
		Source:       "project",
	}); err != nil {
		t.Fatalf("CreateAgent(loom-weaver): %v", err)
	}
	// loom-curator intentionally not created.

	inserted, err := SeedAgentReflexesBySlug(ctx, st, LoomPilotReflexSeeds(), nil)
	if err != nil {
		t.Fatalf("SeedAgentReflexesBySlug: %v", err)
	}
	if inserted != 2 {
		t.Fatalf("inserted = %d, want 2 (weaver's two seeds only; curator's skipped)", inserted)
	}
	weaverRows, err := st.ListAllAgentReflexes(ctx, "profile-loom-weaver")
	if err != nil {
		t.Fatalf("ListAllAgentReflexes(weaver): %v", err)
	}
	if len(weaverRows) != 2 {
		t.Fatalf("weaver reflex rows = %d, want 2", len(weaverRows))
	}
}
