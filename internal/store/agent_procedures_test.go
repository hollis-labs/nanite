package store

import (
	"context"
	"reflect"
	"testing"
)

func TestImmutableAgentProceduresRefuseMutableSOPsAndPreserveHistory(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	const agentID = "historical-procedure"
	retiredBehaviorHistoricalProfile(t, s, agentID)
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO agent_procedures(agent_id,name,body,scope,created_at,updated_at) VALUES(?,'agent-procedure','Retained agent SOP','agent','created-fixture','updated-fixture'),(?,'shared-procedure','Retained shared SOP','shared','created-fixture','updated-fixture')`, agentID, agentID); err != nil {
		t.Fatal(err)
	}
	const query = `SELECT * FROM agent_procedures ORDER BY agent_id,name`
	before := retiredBehaviorSnapshot(t, s, query)
	if len(before) != 2 {
		t.Fatal("historical agent/shared procedure controls not installed")
	}
	for _, row := range []AgentProcedure{
		{AgentID: agentID, Name: "new", Body: "Must not create"},
		{AgentID: agentID, Name: "agent-procedure", Body: "Must not replace", Scope: "shared"},
		{},
	} {
		requireRetiredBehavior(t, s.InsertAgentProcedure(ctx, row))
		// The internal ingestion writer must refuse as well as the public method.
		requireRetiredBehavior(t, insertAgentProcedure(ctx, s.DB, row))
	}
	rows, err := s.ListAgentProcedures(ctx, agentID)
	requireRetiredBehavior(t, err)
	if len(rows) != 0 {
		t.Fatal("old procedures exposed as current SOP behavior")
	}
	for _, name := range []string{"agent-procedure", "shared-procedure", "missing"} {
		got, getErr := s.GetAgentProcedure(ctx, agentID, name)
		requireRetiredBehavior(t, getErr)
		if got != nil {
			t.Fatal("old procedure exposed as current SOP content")
		}
		requireRetiredBehavior(t, s.DeleteAgentProcedure(ctx, agentID, name))
	}
	if after := retiredBehaviorSnapshot(t, s, query); !reflect.DeepEqual(before, after) {
		t.Fatalf("refused procedure operation changed content/scope/history: before=%v after=%v", before, after)
	}
}
