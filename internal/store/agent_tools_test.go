package store

import (
	"errors"
	"testing"
)

func TestActorGrantReadsAndRevocationRequireCurrentBinding(t *testing.T) {
	s := newTestStore(t)
	a := makeTestAgent(t, s, "grant-reader")
	ctx := t.Context()
	tool := &KnownTool{Name: "private-tool", Source: "builtin", Status: "available"}
	id, catalogErr := s.UpsertKnownTool(ctx, tool.Name, tool.Source, tool.Status, "private test fixture")
	tool.ID = id
	if catalogErr != nil {
		t.Fatal(catalogErr)
	}
	// Private fixture simulates prior issuer decisions. These are never obtained
	// from a declaration, profile, request metadata, or a new application issuer.
	for _, query := range []string{`INSERT INTO actor_granted_tools(agent_id,tool_id,granted_via,created_at) VALUES(?,?,'verified-private-fixture','fixture-time')`, `INSERT INTO actor_dispatch_tool_allowlist(agent_id,tool_id,created_at) VALUES(?,?,'fixture-time')`} {
		if _, err := s.DB.ExecContext(ctx, query, a.ID, tool.ID); err != nil {
			t.Fatal(err)
		}
	}
	for _, read := range []func() ([]string, error){func() ([]string, error) { return s.ListAgentToolNames(ctx, a.ID) }, func() ([]string, error) { return s.ListAgentDispatchToolNames(ctx, a.ID) }} {
		names, err := read()
		if err != nil || len(names) != 1 || names[0] != tool.Name {
			t.Fatal(names, err)
		}
	}
	if err := s.GrantAgentTool(ctx, a.ID, tool.ID, "role_seed"); !errors.Is(err, ErrVerifiedActorRequired) {
		t.Fatal("declaration issued authority", err)
	}
	if err := s.GrantAgentDispatchTool(ctx, a.ID, tool.ID); !errors.Is(err, ErrVerifiedActorRequired) {
		t.Fatal("dispatch authority invented", err)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE agent_actor_bindings SET enabled=0 WHERE actor_uri=?`, a.ID); err != nil {
		t.Fatal(err)
	}
	for _, read := range []func() ([]string, error){func() ([]string, error) { return s.ListAgentToolNames(ctx, a.ID) }, func() ([]string, error) { return s.ListAgentDispatchToolNames(ctx, a.ID) }} {
		names, err := read()
		if err != nil || len(names) != 0 {
			t.Fatal("disabled actor retained usable authority", names, err)
		}
	}
	if err := s.RevokeAgentTool(ctx, a.ID, tool.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeAgentDispatchTool(ctx, a.ID, tool.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE agent_actor_bindings SET enabled=1 WHERE actor_uri=?`, a.ID); err != nil {
		t.Fatal(err)
	}
	names, err := s.ListAgentToolNames(ctx, a.ID)
	if err != nil || len(names) != 0 {
		t.Fatal("revoked grant replayed", names, err)
	}
	known, err := s.ListAgentKnownTools(ctx, a.ID)
	if err != nil || len(known) != 0 {
		t.Fatal("grant mutated familiar-tool state", known, err)
	}
}
