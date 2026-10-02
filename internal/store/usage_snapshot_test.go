package store_test

import (
	"encoding/json"
	"math"
	"path/filepath"
	"testing"

	ledger "github.com/hollis-labs/go-usage-ledger"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
	"github.com/hollis-labs/nanite/internal/usagecost"
)

func TestUsageSnapshotPersistenceAndPartialSummaries(t *testing.T) {
	ctx := t.Context()
	st, openErr := storetest.New(t, ctx, filepath.Join(t.TempDir(), "usage.db"))
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer st.Close(ctx)
	session := &store.Session{}
	if err := st.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	row := ledger.Row{Provider: "openai", Model: "test-model", Usage: usagecost.FromRaw("openai", `{"input_tokens":1000,"output_tokens":300,"input_tokens_details":{"cached_tokens":250,"cache_write_tokens":100},"output_tokens_details":{"reasoning_tokens":200}}`).Usage("openai"), Price: &ledger.PriceSnapshot{InputPerMillion: 2, OutputPerMillion: 8, CacheReadPerMillion: .2, CacheWritePerMillion: 2.5, ReasoningPerMillion: 8}}
	if err := st.RecordUsageSnapshot(ctx, session.ID, "measured", row.Model, 1000, 300, 0, 100, 250, []ledger.Row{row}); err != nil {
		t.Fatal(err)
	}
	// Mutating the caller's price after recording cannot alter stored history.
	row.Price.OutputPerMillion = 800
	var raw, status string
	var cost float64
	var total, reasoning int64
	if err := st.DB.QueryRowContext(ctx, `SELECT cost_snapshot,cost_status,estimated_cost_usd,total_tokens,reasoning_tokens FROM token_usage WHERE message_id=?`, "measured").Scan(&raw, &status, &cost, &total, &reasoning); err != nil {
		t.Fatal(err)
	}
	var snapshot usagecost.Snapshot
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		t.Fatal(err)
	}
	if status != "COMPLETE" || total != 1300 || reasoning != 200 || math.Abs(cost-.004) > 1e-12 || snapshot.Calls[0].Price.OutputPerMillion != 8 || snapshot.Calls[0].SessionID != session.ID {
		t.Fatalf("stored snapshot=%+v cost=%v status=%s total=%d reasoning=%d", snapshot, cost, status, total, reasoning)
	}
	missing := usagecost.NewRow(nil, "openai", "unknown")
	if err := st.RecordUsageSnapshot(ctx, session.ID, "unreported", "unknown", 0, 0, 0, 0, 0, []ledger.Row{missing}); err != nil {
		t.Fatal(err)
	}
	summary, err := st.GetSessionUsage(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	global, err := st.GetUsageSummary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if summary.PartialRows != 1 || summary.ReasoningTokens != 200 || global.PartialRows != 1 || math.Abs(summary.EstimatedCostUSD-.004) > 1e-12 || global.TotalCost != summary.EstimatedCostUSD {
		t.Fatalf("summaries: %+v %+v", summary, global)
	}
	metrics := &store.ExecutionMetrics{SessionID: session.ID, MessageID: "measured", Model: "test-model", InputTokens: 1000, OutputTokens: 300}
	if err := st.RecordExecutionMetrics(ctx, metrics); err != nil {
		t.Fatal(err)
	}
	if metrics.EstimatedCostUSD != cost {
		t.Fatal("chat execution cost disagrees with frozen ledger")
	}
}

func TestLegacyUsageSnapshotIsPartial(t *testing.T) {
	ctx := t.Context()
	st, openErr := storetest.New(t, ctx, filepath.Join(t.TempDir(), "legacy.db"))
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer st.Close(ctx)
	session := &store.Session{}
	if err := st.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordUsage(ctx, session.ID, "archived", "claude-sonnet-4-20250514", 1000, 500, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	var cost float64
	var status, raw string
	if err := st.DB.QueryRowContext(ctx, `SELECT estimated_cost_usd,cost_status,cost_snapshot FROM token_usage`).Scan(&cost, &status, &raw); err != nil {
		t.Fatal(err)
	}
	if math.Abs(cost-.0105) > 1e-12 || status != "PARTIAL" || raw == "" {
		t.Fatalf("archived usage cost=%v status=%s snapshot=%s", cost, status, raw)
	}
}
