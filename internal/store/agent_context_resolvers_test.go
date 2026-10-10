package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// These are private historical rows, not fresh actors or mutable authoring.
func retiredBehaviorHistoricalProfile(t *testing.T, s *Store, id string) {
	t.Helper()
	if _, err := s.DB.ExecContext(context.Background(), `INSERT INTO agent_profiles(id,name,slug,system_prompt,source) VALUES(?,?,?,?,?)`, id, "Historical fixture", id, "Retained historical body", "user"); err != nil {
		t.Fatal(err)
	}
}

// Scan raw SQLite cells so refusals cannot silently change retained data,
// including NULL, timestamps, JSON text and the original historical identity.
func retiredBehaviorSnapshot(t *testing.T, s *Store, query string) [][]any {
	t.Helper()
	rows, err := s.DB.QueryContext(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer closeRows(rows)
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var snapshot [][]any
	for rows.Next() {
		cells := make([]any, len(columns))
		targets := make([]any, len(columns))
		for i := range cells {
			targets[i] = &cells[i]
		}
		if err = rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		for i, cell := range cells {
			if b, ok := cell.([]byte); ok {
				cells[i] = append([]byte(nil), b...)
			}
		}
		snapshot = append(snapshot, cells)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func requireRetiredBehavior(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrImmutableAgentProfile) {
		t.Fatalf("retired mutable behavior returned %v, want ErrImmutableAgentProfile", err)
	}
}

func TestImmutableAgentContextResolversRefuseRuntimeAndMutation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	const agentID = "historical-resolver"
	retiredBehaviorHistoricalProfile(t, s, agentID)
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO agent_context_resolvers(id,agent_id,slot_name,kind,run,url,headers_json,response_format,enabled,created_at,updated_at) VALUES('retained-cmd',?,'command','cmd','echo retained','','{}','text',1,'created-fixture','updated-fixture'),('retained-http',?,'remote','http','','https://example.invalid/retained','{"X-Fixture":"retained"}','json',0,'created-fixture','updated-fixture')`, agentID, agentID); err != nil {
		t.Fatal(err)
	}
	const query = `SELECT * FROM agent_context_resolvers ORDER BY id`
	before := retiredBehaviorSnapshot(t, s, query)
	if len(before) != 2 {
		t.Fatal("historical resolver controls not installed")
	}
	for _, row := range []AgentContextResolver{
		{AgentID: agentID, SlotName: "new", Kind: "cmd", Run: "echo new", Enabled: true},
		{AgentID: agentID, SlotName: "command", Kind: "cmd", Run: "echo replacement"},
		{AgentID: agentID, SlotName: "invalid", Kind: "http", ResponseFormat: "xml"},
		{},
	} {
		id, err := s.InsertAgentContextResolver(ctx, row)
		requireRetiredBehavior(t, err)
		if id != "" {
			t.Fatalf("refusal returned created resolver identity %q", id)
		}
	}
	got, err := s.GetAgentContextResolver(ctx, "retained-cmd")
	requireRetiredBehavior(t, err)
	if got != nil {
		t.Fatal("historical resolver exposed as current behavior")
	}
	listed, err := s.ListAgentContextResolvers(ctx, agentID)
	requireRetiredBehavior(t, err)
	if len(listed) != 0 {
		t.Fatal("historical resolvers exposed to mutable catalog")
	}
	enabled, err := s.ListEnabledAgentContextResolvers(ctx, agentID)
	requireRetiredBehavior(t, err)
	if len(enabled) != 0 {
		t.Fatal("historical command exposed to boot execution")
	}
	requireRetiredBehavior(t, s.UpdateAgentContextResolver(ctx, AgentContextResolver{ID: "retained-cmd", AgentID: agentID, SlotName: "command", Kind: "cmd", Run: "echo changed", Enabled: false}))
	for _, id := range []string{"retained-cmd", "missing"} {
		requireRetiredBehavior(t, s.DeleteAgentContextResolver(ctx, id))
	}
	// Ordinary profile deletion cannot cascade away audited resolver history.
	requireRetiredBehavior(t, s.DeleteAgent(ctx, agentID))
	if after := retiredBehaviorSnapshot(t, s, query); !reflect.DeepEqual(before, after) {
		t.Fatalf("refused resolver operation changed history: before=%v after=%v", before, after)
	}
	retained, err := s.GetHistoricalAgentProfile(ctx, agentID)
	if err != nil || retained.SystemPrompt != "Retained historical body" {
		t.Fatalf("historical parent changed: %+v, %v", retained, err)
	}
}
