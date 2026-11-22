package store

import (
	"errors"
	"sync"
	"testing"
)

func TestInitialGrantDeclarationsNeverCreateActorAuthority(t *testing.T) {
	s := newTestStore(t)
	a := makeTestAgent(t, s, "initial-grants")
	ctx := t.Context()
	tool := &KnownTool{Name: "grant-attempt", Source: "builtin", Status: "available"}
	id, err := s.UpsertKnownTool(ctx, tool.Name, tool.Source, tool.Status, "private test fixture")
	tool.ID = id
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, grants := range [][]InitialAgentToolGrant{nil, {{ToolID: tool.ID, GrantedVia: "explicit"}}} {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; results <- s.InitializeAgentToolGrants(ctx, a.ID, grants) }()
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		if !errors.Is(err, ErrVerifiedActorRequired) {
			t.Fatal(err)
		}
	}
	names, err := s.ListAgentToolNames(ctx, a.ID)
	if err != nil || len(names) != 0 {
		t.Fatal("declaration created authority", names, err)
	}
	if err := s.MarkLegacyToolsBackfillRun(ctx, a.ID); !errors.Is(err, ErrVerifiedActorRequired) {
		t.Fatal("backfill revived", err)
	}
}
