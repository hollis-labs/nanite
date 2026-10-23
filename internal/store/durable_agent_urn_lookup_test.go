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

	// A PRIVATE prior-authorized instance, not the refused production issuer.
	inst := makeTestActorInstance(t, s, agent.ID, "urn-lookup-real")

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

	// Supply an invalid old journal value privately to prove empty input never
	// becomes a wildcard. Production creation cannot mint an actor.
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO actor_instances
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
			`INSERT INTO actor_instances
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

func TestDurableActorURNForSession(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	agent := makeTestAgent(t, s, "actor-urn-session")
	inst := makeTestActorInstance(t, s, agent.ID, "actor-urn-session")
	if _, err := s.SetDurableAgentInstanceLaunchState(ctx, inst.ID, DurableAgentStatusActive, "sess-1", ""); err != nil {
		t.Fatal(err)
	}

	got, err := s.DurableActorURNForSession(ctx, "sess-1", agent.ID)
	if err != nil || got != inst.URN {
		t.Fatalf("urn = %q err=%v, want %q", got, err, inst.URN)
	}
	// The same lookup the bridge's route resolution makes, so both directions
	// use one address.
	if via, err := s.GetDurableAgentInstanceByURN(ctx, got); err != nil || via.CurrentSessionID != "sess-1" {
		t.Fatalf("bridge side disagrees: %+v %v", via, err)
	}
	for _, c := range [][2]string{{"", agent.ID}, {"sess-1", ""}, {"other-session", agent.ID}, {"sess-1", "other-profile"}} {
		if u, err := s.DurableActorURNForSession(ctx, c[0], c[1]); err != nil || u != "" {
			t.Fatalf("lookup %v = %q,%v, want empty", c, u, err)
		}
	}
}

// Two live instances bound to one session and profile make the slot
// ambiguous: no address is guessed, the same refusal the bridge makes.
func TestDurableActorURNForSession_AmbiguousSlotReturnsEmpty(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	agent := makeTestAgent(t, s, "actor-urn-ambiguous")
	first := makeTestActorInstance(t, s, agent.ID, "ambiguous-a")
	if _, err := s.SetDurableAgentInstanceLaunchState(ctx, first.ID, DurableAgentStatusActive, "sess-amb", ""); err != nil {
		t.Fatal(err)
	}
	if got, err := s.DurableActorURNForSession(ctx, "sess-amb", agent.ID); err != nil || got != first.URN {
		t.Fatalf("one instance: %q, %v, want %q", got, err, first.URN)
	}
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO actor_instances(id,name,slug,profile_id,provider,model,urn,status,current_session_id) VALUES(?,?,?,?,?,?,?,?,?)`,
		"inst-amb-b", "B", "ambiguous-b", agent.ID, "fixture-provider", "fixture-model", "msg://agent/nanite/other", DurableAgentStatusActive, "sess-amb"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.DurableActorURNForSession(ctx, "sess-amb", agent.ID); err != nil || got != "" {
		t.Fatalf("ambiguous slot = %q, %v; want empty and no error", got, err)
	}
}
