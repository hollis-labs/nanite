package service

import (
	"context"
	"strings"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	feotel "github.com/hollis-labs/go-otel"
	"github.com/hollis-labs/nanite/internal/chat"
)

func TestConsumeProviderIteration_PromissoryPreambleSelfHealingRecovery(t *testing.T) {
	f := newCharacterizationFixture(t, nil)
	agent := f.svc.agents.(*characterizationAgents).agent
	run := &runState{
		loop:  newLoopState(chat.AgentConstraints{}, []string{"dev_grep"}, false),
		tools: []llmtypes.ToolDefinition{{Name: "dev_grep"}},
	}
	run.loop.iteration = 0
	setup := &turnSetup{
		agent: agent, model: "gpt-5.6-luna", providerName: "openai",
		slotResult: &SlotAssemblyResult{},
	}

	preambleText := "I’ll treat this as an exploratory follow-up: verify Nanite, Torque, and Tether against current source, " +
		"inspect Tesseract for related evidence, then capture the alignment question without deciding the migration. " +
		"I’ll first recall the existing Atlas/library context, then inspect the relevant repository guidance and sources."

	eventsCh := make(chan llmtypes.StreamEvent, 4)
	eventsCh <- llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: preambleText}
	eventsCh <- llmtypes.StreamEvent{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{StopReason: "end_turn"}}
	eventsCh <- llmtypes.StreamEvent{Type: llmtypes.EventDone}
	close(eventsCh)

	_, span := feotel.StartSpan(context.Background(), "test.consume-provider-preamble-recovery")
	attempt := &providerAttempt{events: eventsCh, cancel: func() {}, span: span}
	stream := make(chan chat.StreamEvent, 16)

	result := f.svc.consumeProviderIteration(context.Background(), f.session, "assistant-msg-1", setup, run, attempt, stream)

	if result.directive != generationContinueIteration {
		t.Fatalf("expected directive generationContinueIteration, got %v", result.directive)
	}
	if run.loop.preambleNudgeCount != 1 {
		t.Fatalf("expected preambleNudgeCount 1, got %d", run.loop.preambleNudgeCount)
	}
	if len(run.chatMessages) != 2 {
		t.Fatalf("expected 2 chatMessages appended (assistant + user nudge), got %d", len(run.chatMessages))
	}
	if run.chatMessages[0].Role != "assistant" || run.chatMessages[0].ContentBlocks[0].Text != preambleText {
		t.Fatalf("assistant message mismatch: %+v", run.chatMessages[0])
	}
	if run.chatMessages[1].Role != "user" || run.chatMessages[1].ContentBlocks[0].Text != chat.PromissoryPreambleRecoveryNudge {
		t.Fatalf("user nudge message mismatch: %+v", run.chatMessages[1])
	}
	if !strings.Contains(run.narrationContent.String(), preambleText) {
		t.Fatalf("narrationContent does not contain preamble: %q", run.narrationContent.String())
	}
	if run.finalContent.Len() != 0 {
		t.Fatalf("finalContent was not reset: %q", run.finalContent.String())
	}

	// Verify stream events emitted to client
	var sawReplace bool
	var sawNarrationDelta bool
	for len(stream) > 0 {
		evt := <-stream
		if evt.Type == "replace_content" && evt.Content == "" {
			sawReplace = true
		}
		if evt.Type == "delta" && evt.Phase == "narration" && strings.Contains(evt.Content, preambleText) {
			sawNarrationDelta = true
		}
	}
	if !sawReplace {
		t.Error("stream did not receive replace_content to clear final content")
	}
	if !sawNarrationDelta {
		t.Error("stream did not receive delta with phase=narration for the preamble")
	}

	// Test second iteration with same stall doesn't loop infinitely
	run.loop.iteration = 1
	eventsCh2 := make(chan llmtypes.StreamEvent, 4)
	eventsCh2 <- llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: preambleText}
	eventsCh2 <- llmtypes.StreamEvent{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{StopReason: "end_turn"}}
	eventsCh2 <- llmtypes.StreamEvent{Type: llmtypes.EventDone}
	close(eventsCh2)

	_, span2 := feotel.StartSpan(context.Background(), "test.consume-provider-preamble-no-loop")
	attempt2 := &providerAttempt{events: eventsCh2, cancel: func() {}, span: span2}
	result2 := f.svc.consumeProviderIteration(context.Background(), f.session, "assistant-msg-1", setup, run, attempt2, stream)
	if result2.directive != generationFinishRun {
		t.Fatalf("second stall expected generationFinishRun, got %v", result2.directive)
	}
}

func TestConsumeProviderIteration_NormalAnswerDoesNotTriggerRecovery(t *testing.T) {
	f := newCharacterizationFixture(t, nil)
	agent := f.svc.agents.(*characterizationAgents).agent
	run := &runState{
		loop:  newLoopState(chat.AgentConstraints{}, []string{"dev_grep"}, false),
		tools: []llmtypes.ToolDefinition{{Name: "dev_grep"}},
	}
	run.loop.iteration = 0
	setup := &turnSetup{
		agent: agent, model: "gpt-5.6-luna", providerName: "openai",
		slotResult: &SlotAssemblyResult{},
	}

	answerText := "The current directory contains 12 files."
	eventsCh := make(chan llmtypes.StreamEvent, 4)
	eventsCh <- llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: answerText}
	eventsCh <- llmtypes.StreamEvent{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{StopReason: "end_turn"}}
	eventsCh <- llmtypes.StreamEvent{Type: llmtypes.EventDone}
	close(eventsCh)

	_, span := feotel.StartSpan(context.Background(), "test.consume-provider-normal-answer")
	attempt := &providerAttempt{events: eventsCh, cancel: func() {}, span: span}
	stream := make(chan chat.StreamEvent, 16)

	result := f.svc.consumeProviderIteration(context.Background(), f.session, "assistant-msg-1", setup, run, attempt, stream)

	if result.directive != generationFinishRun {
		t.Fatalf("normal answer expected directive generationFinishRun, got %v", result.directive)
	}
	if run.loop.preambleNudgeCount != 0 {
		t.Fatalf("expected preambleNudgeCount 0, got %d", run.loop.preambleNudgeCount)
	}
	if run.finalContent.String() != answerText {
		t.Fatalf("expected finalContent %q, got %q", answerText, run.finalContent.String())
	}
}
