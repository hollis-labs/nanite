package store_test

import (
	"encoding/json"
	"math"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
	"github.com/hollis-labs/nanite/internal/usagecost"
	"github.com/hollis-labs/substrate/llm-core/modelsdev"
	ledger "github.com/hollis-labs/substrate/llm-core/usageledger"
)

type mutableUsageCatalog struct{ rates modelsdev.Pricing }

func (c *mutableUsageCatalog) Get(_, _ string) (modelsdev.Model, bool) {
	return modelsdev.Model{Cost: c.rates}, true
}

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
	catalog := &mutableUsageCatalog{rates: modelsdev.Pricing{Input: 2, Output: 8, CacheRead: .2, CacheWrite: 2.5}}
	row := usagecost.NewRow(catalog, "openai", "test-model")
	row.Usage = usagecost.FromRaw("openai", `{"input_tokens":1000,"output_tokens":300,"input_tokens_details":{"cached_tokens":250,"cache_write_tokens":100},"output_tokens_details":{"reasoning_tokens":200}}`).Usage("openai")
	if err := st.RecordUsageSnapshot(ctx, session.ID, "measured", row.Model, 1000, 300, 0, 100, 250, []ledger.Row{row}); err != nil {
		t.Fatal(err)
	}
	// Mutating the caller's price after recording cannot alter stored history.
	row.Price.OutputPerMillion = 800
	catalog.rates = modelsdev.Pricing{Input: 200, Output: 800, CacheRead: 20, CacheWrite: 250}
	if newRow := usagecost.NewRow(catalog, "openai", "test-model"); newRow.Price.OutputPerMillion != 800 {
		t.Fatal("catalog refresh fixture did not change prices")
	}
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
	var metricsCost float64
	if err := st.DB.QueryRowContext(ctx, `SELECT estimated_cost_usd FROM execution_metrics WHERE message_id=?`, "measured").Scan(&metricsCost); err != nil {
		t.Fatal(err)
	}
	if metricsCost != cost {
		t.Fatal("persisted execution cost disagrees with frozen ledger")
	}
}

func TestImpossibleUsageOverlapPersistsPartialRow(t *testing.T) {
	ctx := t.Context()
	st, openErr := storetest.New(t, ctx, filepath.Join(t.TempDir(), "invalid-overlap.db"))
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer st.Close(ctx)
	session := &store.Session{}
	if err := st.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	row := usagecost.NewRow(&mutableUsageCatalog{rates: modelsdev.Pricing{Input: 2, Output: 8}}, "openai", "test-model")
	row.Usage = usagecost.FromRaw("openai", `{"prompt_tokens":10,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":20,"cache_write_tokens":0},"completion_tokens_details":{"reasoning_tokens":10}}`).Usage("openai")
	if err := st.RecordUsageSnapshot(ctx, session.ID, "overlap", row.Model, 10, 5, 0, 0, 20, []ledger.Row{row}); err != nil {
		t.Fatal(err)
	}
	var total int
	var raw, status string
	if err := st.DB.QueryRowContext(ctx, `SELECT total_tokens,cost_status,cost_snapshot FROM token_usage WHERE message_id=?`, "overlap").Scan(&total, &status, &raw); err != nil {
		t.Fatal(err)
	}
	var snapshot usagecost.Snapshot
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		t.Fatal(err)
	}
	if total != 15 || status != "PARTIAL" || snapshot.Calls[0].Usage.CacheReadTokens.Provenance != ledger.ProvenanceUnknown || snapshot.Calls[0].Usage.ReasoningTokens.Provenance != ledger.ProvenanceUnknown {
		t.Fatalf("invalid overlap lost row or evidence: %s %d %+v", status, total, snapshot)
	}
	summary, err := st.GetSessionUsage(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if summary.PartialRows != 1 || summary.MessageCount != 1 {
		t.Fatalf("invalid overlap disappeared: %+v", summary)
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
