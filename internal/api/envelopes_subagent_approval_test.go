package api

import (
	"context"
	"errors"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
	"github.com/hollis-labs/substrate/agent/subagent"
)

func TestSubagentApprovalCannotEnrollFromHostRoleOrRequestClaims(t *testing.T) {
	a, _ := newTestAPI(t)
	ctx := context.Background()
	if _, err := a.store.DB.ExecContext(ctx, `INSERT OR IGNORE INTO user_settings(id) VALUES(1)`); err != nil {
		t.Fatal(err)
	}
	session := &store.Session{Title: "Parent"}
	if err := a.store.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	p := &store.AgentProfile{Name: "Private prior actor", Slug: "hint-selector"}
	if err := storetest.PriorAuthorizedActor(ctx, a.store, p); err != nil {
		t.Fatal(err)
	}
	queries := []string{`SELECT * FROM subagent_runs ORDER BY id`, `SELECT * FROM sessions ORDER BY id`, `SELECT * FROM agent_actor_bindings ORDER BY actor_uri`, `SELECT * FROM messages ORDER BY id`}
	before := make([][][]any, len(queries))
	for i, q := range queries {
		before[i] = retiredAPISnapshot(t, a, q)
	}
	for _, mode := range []string{subagent.ModeInteractive, subagent.ModeAsync} {
		id, err := a.Services.Subagent.Spawn(ctx, subagent.SpawnRequest{ParentSessionID: session.ID, ParentAgentID: p.ID, Role: p.Slug, Prompt: "claimed child authority", Mode: mode})
		if id != "" || !errors.Is(err, store.ErrVerifiedActorRequired) {
			t.Fatalf("spawn mode=%s id=%s err=%v", mode, id, err)
		}
		for i, q := range queries {
			retiredAPIHistoryUnchanged(t, a, q, before[i])
		}
	}
}
