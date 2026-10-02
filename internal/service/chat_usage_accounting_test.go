package service

import (
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	feotel "github.com/hollis-labs/go-otel"
	ledger "github.com/hollis-labs/go-usage-ledger"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/usagecost"
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
