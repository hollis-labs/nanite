package api

import (
	"reflect"
	"sort"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

var sessionUsageViewKeys = []string{
	"reasoning_tokens", "partial_rows",
	"input_tokens", "output_tokens", "total_tokens", "tool_input_tokens",
	"cache_creation_tokens", "cache_read_tokens", "estimated_cost_usd", "message_count",
}

var usageSummaryViewKeys = []string{
	"partial_rows", "total_input", "total_output", "total_tokens", "total_cost", "by_model"}

// executionMetricsViewKeys is the full key set; debug_snapshots and the
// three harness-profile keys drop out when empty.
var executionMetricsViewKeys = []string{
	"id", "session_id", "message_id", "provider", "adapter", "model", "agent_id",
	"agent_slug", "mode", "duration_ms", "context_messages", "context_tokens",
	"input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens",
	"estimated_cost_usd", "tool_iterations", "tool_calls", "is_utility",
	"stop_reason", "error", "debug_snapshots", "profile_name", "profile_digest",
	"effective_limits_json", "created_at",
}

var utilityCallSummaryViewKeys = []string{
	"provider", "model", "call_type", "call_count", "avg_duration_ms",
	"min_duration_ms", "max_duration_ms", "error_count", "total_cost_usd",
}

func assertKeys(t *testing.T, name string, raw []byte, want []string) {
	t.Helper()
	w := append([]string(nil), want...)
	sort.Strings(w)
	if got := sortedKeys(t, raw); !reflect.DeepEqual(got, w) {
		t.Fatalf("%s JSON keys changed\n got: %v\nwant: %v", name, got, w)
	}
}

func assertSameJSON(t *testing.T, name string, view, row any) {
	t.Helper()
	if got, want := mustJSON(t, view), mustJSON(t, row); string(got) != string(want) {
		t.Errorf("%s: view JSON differs from row JSON\n got: %s\nwant: %s", name, got, want)
	}
}

func TestSessionUsageViewJSON(t *testing.T) {
	var u store.SessionUsageSummary
	populate(t, &u)
	assertKeys(t, "SessionUsageView", mustJSON(t, sessionUsageToView(&u)), sessionUsageViewKeys)
	assertSameJSON(t, "populated", sessionUsageToView(&u), u)
	assertSameJSON(t, "zero", sessionUsageToView(&store.SessionUsageSummary{}), store.SessionUsageSummary{})
	if sessionUsageToViewPtr(nil) != nil {
		t.Fatal("sessionUsageToViewPtr(nil) != nil")
	}
}

func TestUsageSummaryViewJSON(t *testing.T) {
	var m store.ModelUsage
	populate(t, &m)
	full := store.UsageSummary{TotalInput: 1, TotalOutput: 2, TotalTokens: 3, TotalCost: 4.5, ByModel: []store.ModelUsage{m}}
	assertKeys(t, "UsageSummaryView", mustJSON(t, usageSummaryToView(&full)), usageSummaryViewKeys)
	assertSameJSON(t, "populated", usageSummaryToView(&full), full)
	// The store always returns by_model as []; nil would stay null.
	empty := store.UsageSummary{ByModel: []store.ModelUsage{}}
	assertSameJSON(t, "empty by_model", usageSummaryToView(&empty), empty)
	assertSameJSON(t, "nil by_model", usageSummaryToView(&store.UsageSummary{}), store.UsageSummary{})
}

func TestExecutionMetricsViewJSON(t *testing.T) {
	var m store.ExecutionMetrics
	populate(t, &m)
	rows := []store.ExecutionMetrics{m}
	raw := mustJSON(t, executionMetricsToView(rows)[0])
	assertKeys(t, "ExecutionMetricsView", raw, executionMetricsViewKeys)
	assertSameJSON(t, "populated", executionMetricsToView(rows), rows)
	// A zero row drops debug_snapshots and the harness-profile keys, as the
	// row did.
	zero := []store.ExecutionMetrics{{}}
	assertSameJSON(t, "zero", executionMetricsToView(zero), zero)
	assertSameJSON(t, "empty", executionMetricsToView([]store.ExecutionMetrics{}), []store.ExecutionMetrics{})
	if got := string(mustJSON(t, executionMetricsToView(nil))); got != "null" {
		t.Errorf("nil metrics = %s, want null", got)
	}
}

func TestUtilityCallSummaryViewJSON(t *testing.T) {
	var u store.UtilityCallSummary
	populate(t, &u)
	rows := []store.UtilityCallSummary{u}
	assertKeys(t, "UtilityCallSummaryView", mustJSON(t, utilityCallSummaryToView(rows)[0]), utilityCallSummaryViewKeys)
	assertSameJSON(t, "populated", utilityCallSummaryToView(rows), rows)
	assertSameJSON(t, "empty", utilityCallSummaryToView([]store.UtilityCallSummary{}), []store.UtilityCallSummary{})
}
