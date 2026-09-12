package store

import (
	"context"
	"errors"
	"testing"
)

// The tetherbridge resolver is tested against a fake store, because the
// ambiguous-slot state it guards cannot be created in a real database today.
// That leaves the two SQL lookups it depends on unproven, which is the half
// a fake cannot cover. These tests are that half.

func TestGetDurableAgentInstanceByURN(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	agent := makeTestAgent(t, s, "urn-lookup-real")

	inst := &DurableAgentInstance{Name: "Lookup", Slug: "urn-lookup-real", ProfileID: agent.ID}
	if err := s.CreateDurableAgentInstance(ctx, inst); err != nil {
		t.Fatalf("CreateDurableAgentInstance: %v", err)
	}
	if inst.URN == "" {
		t.Fatal("no URN minted; every assertion below would be vacuous")
	}

	got, err := s.GetDurableAgentInstanceByURN(ctx, inst.URN)
	if err != nil {
		t.Fatalf("GetDurableAgentInstanceByURN: %v", err)
	}
	if got.ID != inst.ID {
		t.Errorf("resolved instance = %s, want %s", got.ID, inst.ID)
	}

	if _, err := s.GetDurableAgentInstanceByURN(ctx, "msg://agent/nanite/agt_nosuchactor"); !errors.Is(err, ErrDurableAgentInstanceNotFound) {
		t.Errorf("unknown URN error = %v, want ErrDurableAgentInstanceNotFound", err)
	}
}

// TestGetDurableAgentInstanceByURN_EmptyNeverMatches is the one that matters.
// Every column defaults to the empty string and the guard is a WHERE clause, so
// without the early return an empty argument would match any row that has an
// empty URN and silently mis-deliver. Proven by planting exactly that row.
func TestGetDurableAgentInstanceByURN_EmptyNeverMatches(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	agent := makeTestAgent(t, s, "urn-empty-guard")

	// Write an empty-URN row directly; CreateDurableAgentInstance mints one,
	// so the only way to produce this state is to bypass it.
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO durable_agent_instances
		     (id, name, slug, profile_id, lifecycle_class, provider, model,
		      runtime_kind, launch_source_type, launch_source_id, work_root,
		      status, current_session_id, failure_reason, metadata_json, urn,
		      created_at, updated_at)
		 VALUES ('empty-urn-inst', 'Empty', 'empty-urn-inst', ?, ?, '', '', 'api', ?, '', '',
		         ?, '', '', '{}', '', '2026-09-12T00:00:00Z', '2026-09-12T00:00:00Z')`,
		agent.ID, DurableAgentClassAdvisor, DurableAgentLaunchDurableAdvisor,
		DurableAgentStatusSleeping); err != nil {
		t.Fatalf("plant empty-URN instance: %v", err)
	}

	if _, err := s.GetDurableAgentInstanceByURN(ctx, ""); !errors.Is(err, ErrDurableAgentInstanceNotFound) {
		t.Errorf("empty URN matched a row: err = %v. An empty actor address must never "+
			"resolve to a recipient.", err)
	}
}

func TestCountInstancesSharingMailboxSlot(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	agent := makeTestAgent(t, s, "slot-count")

	const sess = "sess-slot-count"
	mk := func(slug, status, session string) {
		t.Helper()
		if _, err := s.DB.ExecContext(ctx,
			`INSERT INTO durable_agent_instances
			     (id, name, slug, profile_id, lifecycle_class, provider, model,
			      runtime_kind, launch_source_type, launch_source_id, work_root,
			      status, current_session_id, failure_reason, metadata_json, urn,
			      created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, '', '', 'api', ?, '', '', ?, ?, '', '{}', ?,
			         '2026-09-12T00:00:00Z', '2026-09-12T00:00:00Z')`,
			slug, slug, slug, agent.ID, DurableAgentClassAdvisor,
			DurableAgentLaunchDurableAdvisor, status, session,
			"msg://agent/nanite/agt_"+slug); err != nil {
			t.Fatalf("insert %s: %v", slug, err)
		}
	}

	mk("slot-a", DurableAgentStatusSleeping, sess)
	if n, err := s.CountInstancesSharingMailboxSlot(ctx, agent.ID, sess); err != nil || n != 1 {
		t.Fatalf("one live instance: n=%d err=%v, want 1", n, err)
	}

	// A second live instance in the same session is the collision.
	mk("slot-b", DurableAgentStatusSleeping, sess)
	if n, err := s.CountInstancesSharingMailboxSlot(ctx, agent.ID, sess); err != nil || n != 2 {
		t.Fatalf("two live instances: n=%d err=%v, want 2", n, err)
	}

	// An ARCHIVED instance must not count. Archived rows are the common case —
	// 16 of 18 live instances are archived or stopped — so counting them would
	// make the guard fire constantly and get it removed.
	mk("slot-archived", DurableAgentStatusArchived, sess)
	if n, err := s.CountInstancesSharingMailboxSlot(ctx, agent.ID, sess); err != nil || n != 2 {
		t.Errorf("archived instance counted: n=%d err=%v, want 2", n, err)
	}

	// A different session is a different slot.
	if n, err := s.CountInstancesSharingMailboxSlot(ctx, agent.ID, "sess-other"); err != nil || n != 0 {
		t.Errorf("other session: n=%d err=%v, want 0", n, err)
	}

	// Empty arguments are not a wildcard.
	if n, err := s.CountInstancesSharingMailboxSlot(ctx, "", sess); err != nil || n != 0 {
		t.Errorf("empty profile: n=%d err=%v, want 0", n, err)
	}
	if n, err := s.CountInstancesSharingMailboxSlot(ctx, agent.ID, ""); err != nil || n != 0 {
		t.Errorf("empty session: n=%d err=%v, want 0", n, err)
	}
}
