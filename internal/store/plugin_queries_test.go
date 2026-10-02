package store

import (
	"context"
	"reflect"
	"testing"
)

func TestPluginQueryStorageBoundsScopeAndExcludesPrivateValues(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	first := &Session{Title: "First", Metadata: `{"private":"session secret"}`}
	second := &Session{Title: "Second"}
	for _, session := range []*Session{first, second} {
		if checkErr := s.CreateSession(ctx, session); checkErr != nil {
			t.Fatal(checkErr)
		}
	}
	before, err := s.GetSession(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.ReadPluginQuerySessions(ctx, []string{first.ID}, false, 2)
	if err != nil || len(rows) != 1 || rows[0].ID != first.ID || rows[0].Metadata != "" {
		t.Fatalf("session projection: %+v %v", rows, err)
	}
	rows, err = s.ReadPluginQuerySessions(ctx, nil, false, 2)
	if err != nil || len(rows) != 0 {
		t.Fatal("empty allowlist expanded")
	}
	rows, err = s.ReadPluginQuerySessions(ctx, []string{`x') OR 1=1 --`}, false, 2)
	if err != nil || len(rows) != 0 {
		t.Fatal("session identifier changed query semantics")
	}
	rows, err = s.ReadPluginQuerySessions(ctx, nil, true, 1)
	if err != nil || len(rows) != 1 {
		t.Fatalf("bounded workspace list: %d %v", len(rows), err)
	}
	if _, checkErr := s.ReadPluginQuerySessions(ctx, []string{first.ID}, true, 2); checkErr == nil {
		t.Fatal("accepted mixed scope")
	}
	if _, checkErr := s.ReadPluginQuerySessions(ctx, nil, true, 102); checkErr == nil {
		t.Fatal("accepted unbounded limit")
	}
	metric := ExecutionMetrics{SessionID: first.ID, InputTokens: 7, OutputTokens: 3, ToolCalls: 1, Error: "private error credential", DebugSnapshots: "private tool arguments", EffectiveLimitsJSON: "private config"}
	for range 3 {
		if checkErr := s.RecordExecutionMetrics(ctx, &metric); checkErr != nil {
			t.Fatal(checkErr)
		}
	}
	metrics, err := s.ReadPluginQueryMetrics(ctx, first.ID, 2)
	if err != nil || len(metrics) != 2 {
		t.Fatalf("bounded metrics: %d %v", len(metrics), err)
	}
	for _, row := range metrics {
		if row.InputTokens != 7 || row.OutputTokens != 3 || row.Error != "present" || row.DebugSnapshots != "" || row.EffectiveLimitsJSON != "" {
			t.Fatalf("metric projection: %+v", row)
		}
	}
	metrics, err = s.ReadPluginQueryMetrics(ctx, second.ID, 2)
	if err != nil || len(metrics) != 0 {
		t.Fatal("cross-session metrics escaped")
	}
	after, err := s.GetSession(ctx, first.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("read queries changed session state")
	}
	full, err := s.GetSessionExecutionMetrics(ctx, first.ID)
	if err != nil || len(full) != 3 || full[0].Error != metric.Error || full[0].DebugSnapshots != metric.DebugSnapshots || full[0].EffectiveLimitsJSON != metric.EffectiveLimitsJSON {
		t.Fatal("query mutated private metric data")
	}
}
