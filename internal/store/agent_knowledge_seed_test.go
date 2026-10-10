package store

import (
	"context"
	"reflect"
	"testing"
)

func TestImmutableAgentKnowledgeSeedsRefuseSeedingAndPreserveHistory(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	const agentID = "historical-knowledge"
	retiredBehaviorHistoricalProfile(t, s, agentID)
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO agent_knowledge_seed(agent_id,seed_key,namespace,body,tags_json,applied_at,created_at) VALUES(?,'unapplied','project/private-fixture/knowledge','Retained unapplied body','["history"]',NULL,'created-fixture'),(?,'applied','project/private-fixture/knowledge','Retained applied body','[]','applied-fixture','created-fixture')`, agentID, agentID); err != nil {
		t.Fatal(err)
	}
	const query = `SELECT * FROM agent_knowledge_seed ORDER BY agent_id,seed_key`
	before := retiredBehaviorSnapshot(t, s, query)
	if len(before) != 2 {
		t.Fatal("historical applied/unapplied controls not installed")
	}
	for _, row := range []AgentKnowledgeSeed{
		{AgentID: agentID, SeedKey: "new", Namespace: "project/private-fixture/knowledge", Body: "Must not seed", TagsJSON: "[]"},
		{AgentID: agentID, SeedKey: "unapplied", Namespace: "changed", Body: "Must not replace", TagsJSON: "[\"changed\"]"},
		{},
	} {
		requireRetiredBehavior(t, s.InsertAgentKnowledgeSeed(ctx, row))
	}
	rows, err := s.ListAgentKnowledgeSeeds(ctx, agentID)
	requireRetiredBehavior(t, err)
	if len(rows) != 0 {
		t.Fatal("old manifests exposed to automatic knowledge seeding")
	}
	for _, key := range []string{"unapplied", "applied", "missing"} {
		got, getErr := s.GetAgentKnowledgeSeed(ctx, agentID, key)
		requireRetiredBehavior(t, getErr)
		if got != nil {
			t.Fatal("old knowledge manifest exposed as current content")
		}
		requireRetiredBehavior(t, s.MarkAgentKnowledgeSeedApplied(ctx, agentID, key))
		requireRetiredBehavior(t, s.DeleteAgentKnowledgeSeed(ctx, agentID, key))
	}
	if after := retiredBehaviorSnapshot(t, s, query); !reflect.DeepEqual(before, after) {
		t.Fatalf("refused knowledge operation changed body/tags/application history: before=%v after=%v", before, after)
	}
}
