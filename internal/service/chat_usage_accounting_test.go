package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	feotel "github.com/hollis-labs/libs/util/otel"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/usagecost"
	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
	"github.com/hollis-labs/substrate/llm-core/modelsdev"
	ledger "github.com/hollis-labs/substrate/llm-core/usageledger"
)

func TestConsumeProviderUsageKeepsUnknownAcrossCalls(t *testing.T) {
	f := newCharacterizationFixture(t, nil)
	agent := f.svc.agents.(*characterizationAgents).agent
	run := &runState{loop: newLoopState(chat.AgentConstraints{}, nil, false)}
	setup := &turnSetup{agent: agent, model: "claude-sonnet-4-20250514", providerName: "anthropic", slotResult: &SlotAssemblyResult{}}
	for _, raw := range []string{
		`{"input_tokens":10,"output_tokens":20,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"output_tokens_details":{"thinking_tokens":0}}`,
		`{"input_tokens":10,"output_tokens":20}`,
	} {
		events := make(chan llmtypes.StreamEvent, 3)
		events <- llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: "A concrete answer."}
		events <- llmtypes.StreamEvent{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{InputTokens: 10, OutputTokens: 20, StopReason: "end_turn"}, Content: usagecost.Content(usagecost.FromRaw("anthropic", raw))}
		close(events)
		_, span := feotel.StartSpan(t.Context(), "test.usage-accounting")
		attempt := &providerAttempt{events: events, cancel: func() {}, span: span}
		stream := make(chan chat.StreamEvent, 32)
		f.svc.consumeProviderIteration(t.Context(), f.session, "assistant-usage", setup, run, attempt, stream)
	}
	if len(run.usageCalls) != 2 || run.finalUsage.InputTokens != 20 || run.finalUsage.OutputTokens != 40 {
		t.Fatalf("run accounting: %+v usage=%+v", run.usageCalls, run.finalUsage)
	}
	if run.usageCalls[0].Usage.ReasoningTokens.Provenance != ledger.ProvenanceMeasured || run.usageCalls[1].Usage.ReasoningTokens.Provenance != ledger.ProvenanceUnknown {
		t.Fatal("one call's measured zero overwrote the other's omission")
	}
	snapshot, _, err := usagecost.Freeze(run.usageCalls)
	if err != nil || snapshot.Status != "PARTIAL" {
		t.Fatalf("aggregated unknown call: %+v err=%v", snapshot, err)
	}
}

// A real catalog client with a local endpoint; prices can refresh while calls
// are in flight without modifying the process-global model registry.
func usageCatalogFixture(t *testing.T, provider, model string) (*modelsdev.Client, func(int64)) {
	t.Helper()
	var multiplier atomic.Int64
	multiplier.Store(1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		m := multiplier.Load()
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{%q:{"id":%q,"models":{%q:{"id":%q,"cost":{"input":%d,"output":%d}}}}}`, provider, provider, model, model, 2*m, 8*m)
	}))
	t.Cleanup(srv.Close)
	client := modelsdev.New(modelsdev.WithURL(srv.URL), modelsdev.WithHTTPClient(srv.Client()), modelsdev.WithCacheDir(t.TempDir()))
	refresh := func(m int64) {
		multiplier.Store(m)
		if err := client.Refresh(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	refresh(1)
	if _, ok := client.Get(provider, model); !ok {
		t.Fatal("fixture model missing")
	}
	return client, refresh
}

func TestSupplementalCallsRetainUsageAndOmissions(t *testing.T) {
	for _, path := range []string{"synthesis", "correction"} {
		for _, mode := range []string{"reported", "omitted", "error after usage"} {
			t.Run(path+"/"+mode, func(t *testing.T) {
				f := newCharacterizationFixture(t, nil)
				f.svc.modelCatalog, _ = usageCatalogFixture(t, "characterization", "fixture")
				run := &runState{}
				events := []llmtypes.StreamEvent{{Type: llmtypes.EventDelta, Content: "answer"}}
				if mode != "omitted" {
					r := usagecost.FromRaw("openai", `{"input_tokens":1000,"output_tokens":300,"input_tokens_details":{"cached_tokens":0,"cache_write_tokens":0},"output_tokens_details":{"reasoning_tokens":0}}`)
					events = append(events, llmtypes.StreamEvent{Type: llmtypes.EventUsage, Content: usagecost.Content(r), Usage: &llmtypes.Usage{InputTokens: 1000, OutputTokens: 300}})
				}
				if mode == "error after usage" {
					events = append(events, llmtypes.StreamEvent{Type: llmtypes.EventError, Error: "cut off"})
				}
				prov := &mockStreamProvider{events: events}
				ch := make(chan chat.StreamEvent, 32)
				if path == "synthesis" {
					var full, final strings.Builder
					f.svc.earlyStopSynthesis(t.Context(), run, "characterization", prov, "fixture", "", nil, nil, ch, &full, &final)
				} else {
					session, err := f.st.GetSession(t.Context(), f.session)
					if err != nil {
						t.Fatal(err)
					}
					f.svc.retryEnvelopeCorrection(t.Context(), run, "characterization", f.session, session, prov, "fixture", []chat.EnvelopeError{{Reason: "invalid_json"}}, ch)
				}
				if len(run.usageCalls) != 1 {
					t.Fatalf("billed call disappeared: %+v", run.usageCalls)
				}
				snap, cost, err := usagecost.Freeze(run.usageCalls)
				want := "COMPLETE"
				if mode == "omitted" {
					want = "PARTIAL"
				}
				if err != nil || snap.Status != want {
					t.Fatalf("call evidence: %+v %v", snap, err)
				}
				if mode != "omitted" && (run.finalUsage.InputTokens != 1000 || run.finalUsage.OutputTokens != 300 || math.Abs(cost-.0044) > 1e-12) {
					t.Fatalf("supplemental totals/cost: %+v %v", run.finalUsage, cost)
				}
			})
		}
	}
}

func TestUsageCatalogFreezesBeforeProviderDispatchAndSurvivesRefresh(t *testing.T) {
	f := newCharacterizationFixture(t, nil)
	catalog, refresh := usageCatalogFixture(t, "characterization", "characterization-model")
	f.svc.modelCatalog = catalog
	report := usagecost.FromRaw("openai", `{"input_tokens":1000,"output_tokens":300,"input_tokens_details":{"cached_tokens":0,"cache_write_tokens":0},"output_tokens_details":{"reasoning_tokens":0}}`)
	f.provider.steps = []characterizationProviderStep{{
		beforeReturn: func() { refresh(100) },
		events:       []llmtypes.StreamEvent{{Type: llmtypes.EventDelta, Content: "answer"}, {Type: llmtypes.EventUsage, Content: usagecost.Content(report), Usage: &llmtypes.Usage{InputTokens: 1000, OutputTokens: 300, StopReason: "end_turn"}}},
	}}
	f.run(t, "assistant-frozen-price")
	// Change the actual catalog AGAIN after INSERT, then read history and metrics.
	refresh(1000)
	var raw string
	var cost float64
	if err := f.st.DB.QueryRowContext(t.Context(), `SELECT estimated_cost_usd,cost_snapshot FROM token_usage WHERE message_id=?`, "assistant-frozen-price").Scan(&cost, &raw); err != nil {
		t.Fatal(err)
	}
	var snap usagecost.Snapshot
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		t.Fatal(err)
	}
	if math.Abs(cost-.0044) > 1e-12 || snap.Calls[0].Price.InputPerMillion != 2 || snap.Calls[0].Price.OutputPerMillion != 8 {
		t.Fatalf("dispatch/refresh repriced row: %+v %v", snap, cost)
	}
	summary, err := f.st.GetSessionUsage(t.Context(), f.session)
	if err != nil {
		t.Fatal(err)
	}
	global, err := f.st.GetUsageSummary(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	metrics := &store.ExecutionMetrics{SessionID: f.session, MessageID: "assistant-frozen-price", Model: "characterization-model", InputTokens: 1000, OutputTokens: 300}
	if err := f.st.RecordExecutionMetrics(t.Context(), metrics); err != nil {
		t.Fatal(err)
	}
	if summary.EstimatedCostUSD != cost || global.TotalCost != cost || metrics.EstimatedCostUSD != cost {
		t.Fatalf("history/metrics consulted live prices: %+v %+v %+v", summary, global, metrics)
	}
	var persisted float64
	if err := f.st.DB.QueryRowContext(t.Context(), `SELECT estimated_cost_usd FROM execution_metrics WHERE message_id=? ORDER BY id DESC LIMIT 1`, metrics.MessageID).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted != cost {
		t.Fatalf("persisted metrics repriced: %v", persisted)
	}
}

type usageResponseEmitter struct {
	fakeEventEmitter
	calls int
}

func (e *usageResponseEmitter) EmitResponseComplete(context.Context, string, string, string, int, int) {
	e.calls++
}

// Before the snapshot adoption, finalizeRun passed finalUsage directly into
// stream_end (8df76cf9). Preserve stop reasons and cache-only counts while
// suppressing a truly empty object and a 0/0 response_complete notification.
func TestTerminalUsagePreservesStopReasonAndCacheOnlyCounts(t *testing.T) {
	for _, tc := range []struct {
		name          string
		usage         *llmtypes.Usage
		want          *chat.Usage
		tokens        int
		notifications int
	}{
		{name: "no usage event"},
		{name: "truly empty usage", usage: &llmtypes.Usage{}},
		{name: "positive input/output control", usage: &llmtypes.Usage{InputTokens: 3, OutputTokens: 4}, want: &chat.Usage{InputTokens: 3, OutputTokens: 4}, tokens: 7, notifications: 1},
		{name: "stop reason only", usage: &llmtypes.Usage{StopReason: "end_turn"}, want: &chat.Usage{StopReason: "end_turn"}},
		{name: "cache only", usage: &llmtypes.Usage{CacheReadTokens: 11, CacheCreationTokens: 7}, want: &chat.Usage{CacheReadTokens: 11, CacheCreationTokens: 7}, tokens: 18},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := []llmtypes.StreamEvent{{Type: llmtypes.EventDelta, Content: "answer"}}
			if tc.usage != nil {
				events = append(events, llmtypes.StreamEvent{Type: llmtypes.EventUsage, Usage: tc.usage})
			}
			f := newCharacterizationFixture(t, []characterizationProviderStep{{events: events}})
			emitter := &usageResponseEmitter{}
			f.svc.events = emitter
			stream := f.run(t, "assistant-unknown")
			found := false
			for _, event := range stream {
				if event.Type == "stream_end" {
					found = true
					if (event.Usage == nil) != (tc.want == nil) || (tc.want != nil && *event.Usage != *tc.want) {
						t.Fatalf("terminal usage=%+v want=%+v", event.Usage, tc.want)
					}
				}
			}
			if !found || emitter.calls != tc.notifications {
				t.Fatalf("missing end or invented response_complete: %v %d", found, emitter.calls)
			}
			summary, err := f.st.GetSessionUsage(t.Context(), f.session)
			if err != nil {
				t.Fatal(err)
			}
			if summary.MessageCount != 1 || summary.PartialRows != 1 || summary.TotalTokens != tc.tokens {
				t.Fatalf("missing partial evidence: %+v", summary)
			}
		})
	}
}

func TestSupplementalDispatchFailureDoesNotAddUnknownCall(t *testing.T) {
	for _, path := range []string{"synthesis", "correction"} {
		t.Run(path, func(t *testing.T) {
			f := newCharacterizationFixture(t, nil)
			f.svc.modelCatalog, _ = usageCatalogFixture(t, "characterization", "fixture")
			row := usagecost.NewRow(f.svc.modelCatalog, "characterization", "fixture")
			usagecost.FromRaw("openai", `{"input_tokens":1000,"output_tokens":300,"input_tokens_details":{"cached_tokens":0,"cache_write_tokens":0},"output_tokens_details":{"reasoning_tokens":0}}`).Apply(&row)
			run := &runState{usageCalls: []ledger.Row{row}}
			prov := &characterizationProvider{steps: []characterizationProviderStep{{err: errors.New("dispatch failed")}}}
			ch := make(chan chat.StreamEvent, 32)
			if path == "synthesis" {
				var full, final strings.Builder
				f.svc.earlyStopSynthesis(t.Context(), run, "characterization", prov, "fixture", "", nil, nil, ch, &full, &final)
			} else {
				session, err := f.st.GetSession(t.Context(), f.session)
				if err != nil {
					t.Fatal(err)
				}
				f.svc.retryEnvelopeCorrection(t.Context(), run, "characterization", f.session, session, prov, "fixture", []chat.EnvelopeError{{Reason: "invalid_json"}}, ch)
			}
			if prov.callCount() != 1 || len(run.usageCalls) != 1 || run.finalUsage != nil {
				t.Fatalf("failed dispatch changed accounting: calls=%d ledger=%+v usage=%+v", prov.callCount(), run.usageCalls, run.finalUsage)
			}
			snap, cost, err := usagecost.Freeze(run.usageCalls)
			if err != nil || snap.Status != "COMPLETE" || math.Abs(cost-.0044) > 1e-12 {
				t.Fatalf("failed dispatch degraded previous evidence: %+v cost=%v err=%v", snap, cost, err)
			}
		})
	}
}

func TestMainDispatchFailureDoesNotRecordUsage(t *testing.T) {
	f := newCharacterizationFixture(t, []characterizationProviderStep{{err: errors.New("dispatch failed")}})
	f.run(t, "assistant-dispatch-failed")
	var count int
	if err := f.st.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM token_usage WHERE message_id=?`, "assistant-dispatch-failed").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if f.provider.callCount() != 1 || count != 0 {
		t.Fatalf("failed main dispatch calls=%d ledger rows=%d", f.provider.callCount(), count)
	}
}

func TestSupplementalUsageIgnoresNonpositiveDeltas(t *testing.T) {
	run := &runState{finalUsage: &chat.Usage{InputTokens: 10, OutputTokens: 20, CacheReadTokens: 30, CacheCreationTokens: 40}}
	a := &providerCallAccounting{run: run}
	a.consumeSupplemental(llmtypes.StreamEvent{Usage: &llmtypes.Usage{InputTokens: -5, OutputTokens: -6, CacheReadTokens: -7, CacheCreationTokens: -8}})
	a.consumeSupplemental(llmtypes.StreamEvent{Usage: &llmtypes.Usage{}})
	a.consumeSupplemental(llmtypes.StreamEvent{Usage: &llmtypes.Usage{InputTokens: 1, OutputTokens: 2, CacheReadTokens: 3, CacheCreationTokens: 4}})
	want := chat.Usage{InputTokens: 11, OutputTokens: 22, CacheReadTokens: 33, CacheCreationTokens: 44}
	if *run.finalUsage != want {
		t.Fatalf("nonpositive deltas changed totals: %+v want=%+v", run.finalUsage, want)
	}
}
