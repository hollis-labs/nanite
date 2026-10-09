package store

import (
	"context"
	"sync"
	"testing"
)

func TestInitializeAgentToolGrants_CompetingSnapshotsAndRevocation(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	row := makeTestAgent(t, st, "competing-install")
	first, err := st.UpsertKnownTool(ctx, "first_tool", "mcp", "available", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.UpsertKnownTool(ctx, "second_tool", "mcp", "available", "")
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errors := make(chan error, 2)
	var wg sync.WaitGroup
	for _, toolID := range []string{first, second} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errors <- st.InitializeAgentToolGrants(ctx, row.ID, []InitialAgentToolGrant{{ToolID: toolID, GrantedVia: "explicit"}})
		}()
	}
	close(start)
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	names, err := st.ListAgentToolNames(ctx, row.ID)
	if err != nil || len(names) != 1 {
		t.Fatalf("competing snapshots combined grants: %v %v", names, err)
	}
	winner, err := st.GetKnownToolByName(ctx, names[0])
	if err != nil {
		t.Fatal(err)
	}
	if operationErr := st.RevokeAgentTool(ctx, row.ID, winner.ID); operationErr != nil {
		t.Fatal(operationErr)
	}
	if operationErr := st.InitializeAgentToolGrants(ctx, row.ID, []InitialAgentToolGrant{{ToolID: winner.ID, GrantedVia: "explicit"}}); operationErr != nil {
		t.Fatal(operationErr)
	}
	names, err = st.ListAgentToolNames(ctx, row.ID)
	if err != nil || len(names) != 0 {
		t.Fatalf("repeated init restored revoked grant: %v %v", names, err)
	}
}
