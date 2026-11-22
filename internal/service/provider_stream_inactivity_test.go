package service

import (
	"context"
	"testing"
	"time"

	"github.com/hollis-labs/substrate/agent/runloop"

	llmtypes "github.com/hollis-labs/go-llm-types"
	feotel "github.com/hollis-labs/go-otel"
	"github.com/hollis-labs/nanite/internal/chat"
	"go.opentelemetry.io/otel/trace"
)

// The host must terminate a silent provider, close its attempt, and retain
// its original error/outcome. Reader watchdog behavior belongs to the core.

func TestConsumeProviderIteration_InactivityTerminatesAndClosesAttempt(t *testing.T) {
	f := newCharacterizationFixture(t, nil)
	agent := f.svc.agents.(*characterizationAgents).agent
	run := &runState{loop: newLoopState(chat.AgentConstraints{}, nil, false)}
	run.loop.iteration = 1
	run.loop.limits.idleTimeout = 15 * time.Millisecond
	setup := &turnSetup{
		agent: agent, model: "characterization-model", providerName: "characterization",
		slotResult: &SlotAssemblyResult{},
	}
	stalled := make(chan llmtypes.StreamEvent)
	cancelCalls := 0
	_, span := feotel.StartSpan(context.Background(), "test.consume-provider-inactivity")
	attempt := &providerAttempt{events: stalled, cancel: func() { cancelCalls++ }, span: span}
	stream := make(chan chat.StreamEvent, 8)

	started := time.Now()
	result := f.svc.consumeProviderIteration(context.Background(), f.session, "assistant-stalled", setup, run, attempt, stream)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("inactivity termination took %s, want under 1s", elapsed)
	}
	if result.directive != runloop.Terminate {
		t.Fatalf("directive = %v, want runloop.Terminate", result.directive)
	}
	if cancelCalls != 1 {
		t.Fatalf("attempt cancel calls = %d, want exactly 1", cancelCalls)
	}
	var sawError bool
	for len(stream) > 0 {
		if evt := <-stream; evt.Type == "error" {
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("inactivity termination emitted no error event")
	}
}

type countingAttemptSpan struct {
	trace.Span
	endCalls int
}

func (s *countingAttemptSpan) End(...trace.SpanEndOption) { s.endCalls++ }

func TestProviderAttempt_CloseIsIdempotent(t *testing.T) {
	cancelCalls := 0
	_, baseSpan := feotel.StartSpan(context.Background(), "test.provider-attempt-close")
	span := &countingAttemptSpan{Span: baseSpan}
	attempt := &providerAttempt{cancel: func() { cancelCalls++ }, span: span}

	attempt.cancelStream()
	attempt.cancelStream()
	attempt.close()
	attempt.close()

	if cancelCalls != 1 {
		t.Fatalf("cancel calls = %d, want exactly 1", cancelCalls)
	}
	if span.endCalls != 1 {
		t.Fatalf("span end calls = %d, want exactly 1", span.endCalls)
	}
}
