package envelope_render

import (
	"context"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/dispatch"
)

// Benchmark prompt corpus — 8 representative dispatch payloads spanning real-data
// and demo-data intents across the v1 passive-renderable allow-list. Mirrors the
// prompt enumeration in docs/measurements/executor-handoff-pilot.md so the bench
// and the report stay in lockstep — when one changes, the other should follow.
//
// Notes on selection (matched 1:1 with the report):
//   - P1, P3, P5: c117-shaped demo prompts (synthesis allowed, grounded types).
//   - P2, P4, P7: real-data renders (sources required when grounded).
//   - P6, P8: non-grounded passive types (no Sources requirement).
//
// All payloads pass the per-type schema with no repair pass, so the bench
// reflects steady-state dispatch overhead rather than the recovery branch.
//
// The fixed Clock makes every Execute call byte-identical run-to-run — useful
// for replay/diff and for ruling out time.Now() jitter as a noise source.
type benchPrompt struct {
	name string
	req  dispatch.ExecutorRequest
}

func benchCorpus() []benchPrompt {
	return []benchPrompt{
		{
			name: "P1_demo_report_card",
			req: dispatch.ExecutorRequest{
				Intent:             IntentRenderEnvelope,
				TargetEnvelopeType: "report-card",
				UserRequest:        "Let's do some testing — show me a demo report card with sprint metrics.",
				Data: map[string]any{
					"title": "Q1 Demo Sprint Metrics",
					"metrics": []any{
						map[string]any{"label": "Active sprints", "value": "3"},
						map[string]any{"label": "Tickets closed", "value": "47"},
						map[string]any{"label": "Open blockers", "value": "2"},
					},
					"summary": "Demo data — synthesized for illustration.",
				},
				Sources: []dispatch.ExecutorSource{
					{ToolUseID: "toolu_synth_p1", ToolName: "synthesized", Note: "demo data"},
				},
				SyntheticAllowed: true,
			},
		},
		{
			name: "P2_real_report_card_grounded",
			req: dispatch.ExecutorRequest{
				Intent:             IntentRenderEnvelope,
				TargetEnvelopeType: "report-card",
				UserRequest:        "Render a report card from the latest sprint summary I pulled.",
				Data: map[string]any{
					"title": "SP-20260429-0001 — Phase B status",
					"metrics": []any{
						map[string]any{"label": "Tickets done", "value": "5"},
						map[string]any{"label": "Tickets in flight", "value": "4"},
					},
					"summary": "Phase B is two-thirds complete; B6 in progress.",
				},
				Sources: []dispatch.ExecutorSource{
					{ToolUseID: "toolu_clockwork_p2", ToolName: "clockwork_sprint_get"},
				},
			},
		},
		{
			name: "P3_demo_document_viewer",
			req: dispatch.ExecutorRequest{
				Intent:             IntentRenderEnvelope,
				TargetEnvelopeType: "document-viewer",
				UserRequest:        "Sketch a demo design doc as a document viewer.",
				Data: map[string]any{
					"title":   "Demo design — Envelope handoff",
					"content": "# Demo\n\nThis is synthesized demo content for illustration purposes.",
				},
				Sources: []dispatch.ExecutorSource{
					{ToolUseID: "toolu_synth_p3", ToolName: "synthesized", Note: "demo data"},
				},
				SyntheticAllowed: true,
			},
		},
		{
			name: "P4_real_table_card",
			req: dispatch.ExecutorRequest{
				Intent:             IntentRenderEnvelope,
				TargetEnvelopeType: "table-card",
				UserRequest:        "Show me the open tickets in a table.",
				Data: map[string]any{
					"title": "Open tickets",
					"columns": []any{
						map[string]any{"key": "id", "label": "ID"},
						map[string]any{"key": "title", "label": "Title"},
					},
					"rows": []any{
						map[string]any{"id": "CW-20260429-0035", "title": "B6 measurement"},
						map[string]any{"id": "CW-20260429-0033", "title": "B4 lens placement"},
					},
				},
			},
		},
		{
			name: "P5_demo_metric_card",
			req: dispatch.ExecutorRequest{
				Intent:             IntentRenderEnvelope,
				TargetEnvelopeType: "metric-card",
				UserRequest:        "Demo: show a single metric card for active users.",
				Data: map[string]any{
					"label":    "Active users",
					"value":    "1,247",
					"unit":     "users",
					"trend":    "up",
					"previous": "1,180",
				},
				SyntheticAllowed: true,
			},
		},
		{
			name: "P6_real_list_card",
			req: dispatch.ExecutorRequest{
				Intent:             IntentRenderEnvelope,
				TargetEnvelopeType: "list-card",
				UserRequest:        "List the upcoming agenda items.",
				Data: map[string]any{
					"title": "Agenda",
					"items": []any{
						map[string]any{"label": "Sync on Phase B"},
						map[string]any{"label": "Triage backlog"},
						map[string]any{"label": "Review PR #112"},
					},
				},
			},
		},
		{
			name: "P7_real_progress_card",
			req: dispatch.ExecutorRequest{
				Intent:             IntentRenderEnvelope,
				TargetEnvelopeType: "progress-card",
				UserRequest:        "Show how far along Phase B is.",
				Data: map[string]any{
					"title":    "Phase B",
					"progress": 67,
					"steps": []any{
						map[string]any{"label": "B1 design", "done": true},
						map[string]any{"label": "B2 classifier", "done": true},
						map[string]any{"label": "B3 pilot", "done": true},
						map[string]any{"label": "B4 lens placement", "done": false},
					},
				},
			},
		},
		{
			name: "P8_real_timeline_card",
			req: dispatch.ExecutorRequest{
				Intent:             IntentRenderEnvelope,
				TargetEnvelopeType: "timeline-card",
				UserRequest:        "Show the merge timeline for SP-20260429-0001.",
				Data: map[string]any{
					"title": "Merge timeline",
					"events": []any{
						map[string]any{"timestamp": "2026-05-07T10:00:00Z", "label": "B1 merged", "status": "completed"},
						map[string]any{"timestamp": "2026-05-08T11:00:00Z", "label": "B3 merged", "status": "completed"},
						map[string]any{"timestamp": "2026-05-08T16:00:00Z", "label": "B4/B6 in flight", "status": "active"},
					},
				},
			},
		},
	}
}

// fixedClock returns a deterministic clock so report-card.generated_at is stable
// across runs. Bench output is comparable run-to-run only when this is in place.
func fixedClock() func() time.Time {
	t := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return t }
}

// TestBenchCorpus_AllPass is the correctness gate for the bench harness:
// every prompt must dispatch to a non-failure ExecutorResponse with a valid
// envelope. If a future schema change rejects one of these payloads the
// benchmark numbers are no longer comparable, so we trip the test loud.
//
// Run with: go test ./internal/executor/envelope_render -run TestBenchCorpus_AllPass
func TestBenchCorpus_AllPass(t *testing.T) {
	exec := &Executor{Clock: fixedClock()}
	for _, p := range benchCorpus() {
		p := p
		t.Run(p.name, func(t *testing.T) {
			resp, err := exec.Execute(context.Background(), p.req)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if resp == nil {
				t.Fatal("nil response")
			}
			if resp.Failure != nil {
				t.Fatalf("unexpected failure %s: %s", resp.Failure.Code, resp.Failure.Message)
			}
			if resp.Envelope == nil {
				t.Fatal("expected non-nil envelope")
			}
			if resp.Envelope.Type != p.req.TargetEnvelopeType {
				t.Fatalf("envelope type %q != requested %q", resp.Envelope.Type, p.req.TargetEnvelopeType)
			}
		})
	}
}

// BenchmarkDispatchExecutor_AllPrompts measures per-prompt dispatch overhead
// at the in-process executor seam (no LLM call, no network). Captures:
//   - allocs/op + bytes/op for each prompt shape (validation cost dominates).
//   - ns/op for the round-trip from DispatchExecutor → Executor.Execute → response.
//
// The numbers are dispatch-overhead only; live LLM benchmarks for the full
// chat-direct vs executor-routed comparison are documented in
// docs/measurements/executor-handoff-pilot.md and require operator-issued
// API keys (out of scope for autonomous CI).
//
// Run with:
//
//	go test ./internal/executor/envelope_render -bench BenchmarkDispatchExecutor -benchmem
func BenchmarkDispatchExecutor_AllPrompts(b *testing.B) {
	exec := &Executor{Clock: fixedClock()}
	corpus := benchCorpus()
	for _, p := range corpus {
		p := p
		b.Run(p.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				resp, err := dispatch.DispatchExecutor(context.Background(), exec, p.req)
				if err != nil {
					b.Fatalf("DispatchExecutor: %v", err)
				}
				if resp.Failure != nil {
					b.Fatalf("unexpected failure: %s", resp.Failure.Message)
				}
			}
		})
	}
}
