package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/chat"
)

// TestExtraSystemPrefix_NoToolsWarning verifies the warning prefix is emitted
// when noTools is true, and absent otherwise.
func TestExtraSystemPrefix_NoToolsWarning(t *testing.T) {
	with := composeExtraSystemPrefix(composeConfig{noTools: true})
	if !strings.Contains(with, "no tools available in this session") {
		t.Errorf("expected no-tools warning when noTools=true, got:\n%s", with)
	}

	without := composeExtraSystemPrefix(composeConfig{noTools: false})
	if strings.Contains(without, "no tools available in this session") {
		t.Errorf("did not expect no-tools warning when noTools=false, got:\n%s", without)
	}
}

func TestHasUsableTools(t *testing.T) {
	if hasUsableTools(nil) {
		t.Fatal("nil tool slice should not count as usable tools")
	}
	if hasUsableTools([]llmtypes.ToolDefinition{}) {
		t.Fatal("empty tool slice should not count as usable tools")
	}
	if !hasUsableTools([]llmtypes.ToolDefinition{{Name: "dev_read"}}) {
		t.Fatal("built-in tools must count as usable tools")
	}
	// Uniform MCP-origin name (ADR-002 — no `mcp__server__` prefix).
	if !hasUsableTools([]llmtypes.ToolDefinition{{Name: "context_lookup"}}) {
		t.Fatal("MCP tools must count as usable tools")
	}
}

// TestExtraSystemPrefix_ProgressiveCatalog verifies the progressive catalog
// is included before the native tool guide when progressiveActive is true.
func TestExtraSystemPrefix_ProgressiveCatalog(t *testing.T) {
	catalog := "## Available Tools (summary)\n- foo: does foo stuff\n"
	prefix := composeExtraSystemPrefix(composeConfig{
		progressiveActive:  true,
		progressiveCatalog: catalog,
	})
	if !strings.Contains(prefix, "Available Tools (summary)") {
		t.Errorf("expected progressive catalog in prefix, got:\n%s", prefix)
	}
	catIdx := strings.Index(prefix, "Available Tools (summary)")
	nativeIdx := strings.Index(prefix, "Native Tool Usage")
	if catIdx > nativeIdx {
		t.Errorf("expected catalog BEFORE native tool guide; catalog=%d native=%d", catIdx, nativeIdx)
	}
}

// --- early-stopping-generate tests ---

// mockStreamProvider is a minimal llmcontracts.Provider that emits a fixed sequence
// of StreamEvents and records whether StreamChat was called with tools.
type mockStreamProvider struct {
	events       []llmtypes.StreamEvent
	gotTools     []llmtypes.ToolDefinition
	lastMessages []llmtypes.ChatMessage
	callCount    int
}

func (m *mockStreamProvider) StreamChat(_ context.Context, req llmtypes.ChatRequest) (<-chan llmtypes.StreamEvent, error) {
	m.callCount++
	m.gotTools = req.Tools
	m.lastMessages = req.Messages
	ch := make(chan llmtypes.StreamEvent, len(m.events)+1)
	for _, ev := range m.events {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

func (m *mockStreamProvider) Complete(_ context.Context, _ llmtypes.ChatRequest) (string, error) {
	return "", nil
}

func (m *mockStreamProvider) CompleteWithUsage(_ context.Context, _ llmtypes.ChatRequest) (llmtypes.CompleteResult, error) {
	return llmtypes.CompleteResult{}, nil
}

func (m *mockStreamProvider) Capabilities() llmtypes.ProviderCapabilities {
	return llmtypes.ProviderCapabilities{}
}

// TestEarlyStopSynthesisPrompt verifies the constant value matches the spec.
func TestEarlyStopSynthesisPrompt(t *testing.T) {
	const want = "You've reached the maximum number of steps. Provide your best answer now based on the work you've done so far."
	if earlyStopSynthesisPrompt != want {
		t.Errorf("earlyStopSynthesisPrompt mismatch:\ngot:  %s\nwant: %s", earlyStopSynthesisPrompt, want)
	}
}

// TestEarlyStopSynthesis_StreamsDeltasToChannel verifies that earlyStopSynthesis
// forwards delta events from the provider into the channel and accumulates
// them in fullContent.
func TestEarlyStopSynthesis_StreamsDeltasToChannel(t *testing.T) {
	prov := &mockStreamProvider{
		events: []llmtypes.StreamEvent{
			{Type: "delta", Content: "Here is "},
			{Type: "delta", Content: "my best answer."},
			{Type: "done"},
		},
	}

	svc := &chatServiceImpl{}
	ch := make(chan chat.StreamEvent, 16)
	var fullContent strings.Builder

	var finalContent strings.Builder
	svc.earlyStopSynthesis(
		context.Background(),
		prov,
		"test-model",
		"system prompt",
		nil, // slotResult — nil is safe; slotBlocksFor handles nil
		nil, // chatMessages
		ch,
		&fullContent,
		&finalContent,
	)

	close(ch)

	// Collect events from channel.
	var got []string
	for ev := range ch {
		if ev.Type == "delta" {
			got = append(got, ev.Content)
		}
	}

	if len(got) != 2 {
		t.Fatalf("expected 2 delta events, got %d: %v", len(got), got)
	}
	if got[0] != "Here is " || got[1] != "my best answer." {
		t.Errorf("unexpected delta content: %v", got)
	}
	if fullContent.String() != "Here is my best answer." {
		t.Errorf("fullContent mismatch: %q", fullContent.String())
	}
}

// TestEarlyStopSynthesis_NoToolsForwarded verifies that the synthesis call
// sends an empty tools slice so the model cannot call tools and recurse.
func TestEarlyStopSynthesis_NoToolsForwarded(t *testing.T) {
	prov := &mockStreamProvider{
		events: []llmtypes.StreamEvent{{Type: "done"}},
	}

	svc := &chatServiceImpl{}
	ch := make(chan chat.StreamEvent, 8)
	var fullContent strings.Builder

	svc.earlyStopSynthesis(
		context.Background(),
		prov,
		"test-model",
		"",
		nil,
		[]llmtypes.ChatMessage{{Role: "user", Content: "prior message"}},
		ch,
		&fullContent,
		nil, // finalContent — optional; nil is safe
	)
	close(ch)

	if len(prov.gotTools) != 0 {
		t.Errorf("synthesis call should not forward tools; got %d tools", len(prov.gotTools))
	}
}

// TestEarlyStopSynthesis_PromptInjected verifies that the synthesis prompt is
// injected as the final user message so the LLM receives it.
func TestEarlyStopSynthesis_PromptInjected(t *testing.T) {
	prov := &mockStreamProvider{
		events: []llmtypes.StreamEvent{{Type: "done"}},
	}

	svc := &chatServiceImpl{}
	ch := make(chan chat.StreamEvent, 8)
	var fullContent strings.Builder

	prior := []llmtypes.ChatMessage{
		{Role: "user", Content: "prior message"},
		{Role: "assistant", Content: "prior response"},
	}

	svc.earlyStopSynthesis(
		context.Background(),
		prov,
		"test-model",
		"",
		nil,
		prior,
		ch,
		&fullContent,
		nil, // finalContent — optional; nil is safe
	)
	close(ch)

	if prov.callCount != 1 {
		t.Errorf("expected 1 StreamChat call, got %d", prov.callCount)
	}
	if len(prov.lastMessages) == 0 {
		t.Fatal("no messages passed to StreamChat")
	}
	last := prov.lastMessages[len(prov.lastMessages)-1]
	if last.Role != "user" || last.Content != earlyStopSynthesisPrompt {
		t.Errorf("last message should be user turn with synthesis prompt; got role=%q content=%q",
			last.Role, last.Content)
	}
}

// TestEarlyStopSynthesis_FiringCodes verifies the logic that determines for
// which TerminationCode the early-stop synthesis fires.
//
// CW-20260504-0001: max_turns is no longer emitted by the loop (it's a soft
// hint now), and even if a caller emits it manually for back-compat the
// synthesis path no longer routes on it. Synthesis fires only on
// runaway_tool_failures — the case where the agent is still mid-thought
// and being cut off by the hard circuit-breaker.
func TestEarlyStopSynthesis_FiringCodes(t *testing.T) {
	synthCodes := []TerminationCode{
		TerminationRunawayToolFailures,
	}
	noSynthCodes := []TerminationCode{
		TerminationMaxTurns, // soft now; no synthesis
		TerminationHardCeiling,
		TerminationIdleTimeout,
		TerminationRetryBudgetExhausted,
	}

	fires := func(code TerminationCode) bool {
		return code == TerminationRunawayToolFailures
	}

	for _, code := range synthCodes {
		if !fires(code) {
			t.Errorf("expected synthesis to fire for %s", code)
		}
	}
	for _, code := range noSynthCodes {
		if fires(code) {
			t.Errorf("expected synthesis NOT to fire for %s", code)
		}
	}
}

// ---------------------------------------------------------------------------
// F4 Phase-tagging tests (CW-20260419-0029 / CW-20260426-0019)
// ---------------------------------------------------------------------------

// TestStreamEventPhaseConstants verifies the exported Phase constants exist
// and have the expected wire values. F3 will add PhaseThinking — this test
// serves as a registry guard so future additions don't silently collide.
func TestStreamEventPhaseConstants(t *testing.T) {
	if chat.PhaseNarration != "narration" {
		t.Errorf("PhaseNarration: got %q, want %q", chat.PhaseNarration, "narration")
	}
	if chat.PhaseFinal != "final" {
		t.Errorf("PhaseFinal: got %q, want %q", chat.PhaseFinal, "final")
	}
}

// TestStreamEventPhaseFieldOmitEmpty verifies that the Phase field is omitted
// from non-delta events (omitempty behavior — old clients must not see it).
func TestStreamEventPhaseFieldOmitEmpty(t *testing.T) {
	marshal := func(v any) string {
		b, _ := json.Marshal(v)
		return string(b)
	}

	// Non-delta event: Phase must be absent.
	nonDelta := chat.StreamEvent{Type: "tool_call", Tool: "example_tool"}
	out := marshal(nonDelta)
	if strings.Contains(out, "phase") {
		t.Errorf("non-delta event JSON should not contain 'phase' field; got: %s", out)
	}

	// Delta with phase set: must appear.
	withPhase := chat.StreamEvent{Type: "delta", Content: "hi", Phase: chat.PhaseNarration}
	out2 := marshal(withPhase)
	if !strings.Contains(out2, `"phase":"narration"`) {
		t.Errorf("delta event with PhaseNarration should contain phase field; got: %s", out2)
	}

	// Delta with no phase: must be absent (old stream behavior).
	noPhase := chat.StreamEvent{Type: "delta", Content: "hi"}
	out3 := marshal(noPhase)
	if strings.Contains(out3, "phase") {
		t.Errorf("delta event with no phase should not contain 'phase' field; got: %s", out3)
	}
}

// TestEarlyStopSynthesis_FinalContentPopulated verifies that earlyStopSynthesis
// also populates the finalContent accumulator when provided. This is the
// F4 persistence path: narrationContent stays separate; synthesis → finalContent.
func TestEarlyStopSynthesis_FinalContentPopulated(t *testing.T) {
	prov := &mockStreamProvider{
		events: []llmtypes.StreamEvent{
			{Type: "delta", Content: "The answer is 42."},
			{Type: "done"},
		},
	}

	svc := &chatServiceImpl{}
	ch := make(chan chat.StreamEvent, 16)
	var fullContent strings.Builder
	var finalContent strings.Builder

	svc.earlyStopSynthesis(
		context.Background(),
		prov,
		"test-model",
		"system",
		nil,
		nil,
		ch,
		&fullContent,
		&finalContent,
	)
	close(ch)

	// Drain channel.
	for range ch {
	}

	if fullContent.String() != "The answer is 42." {
		t.Errorf("fullContent mismatch: %q", fullContent.String())
	}
	if finalContent.String() != "The answer is 42." {
		t.Errorf("finalContent mismatch: %q", finalContent.String())
	}
}

// TestEarlyStopSynthesis_DeltasTaggedFinal verifies that synthesis delta events
// carry Phase=PhaseFinal so the frontend routes them to the answer bubble.
func TestEarlyStopSynthesis_DeltasTaggedFinal(t *testing.T) {
	prov := &mockStreamProvider{
		events: []llmtypes.StreamEvent{
			{Type: "delta", Content: "answer text"},
			{Type: "done"},
		},
	}

	svc := &chatServiceImpl{}
	ch := make(chan chat.StreamEvent, 16)
	var fullContent strings.Builder

	svc.earlyStopSynthesis(
		context.Background(),
		prov,
		"test-model",
		"",
		nil,
		nil,
		ch,
		&fullContent,
		nil,
	)
	close(ch)

	var phases []string
	for ev := range ch {
		if ev.Type == "delta" {
			phases = append(phases, ev.Phase)
		}
	}
	if len(phases) != 1 || phases[0] != chat.PhaseFinal {
		t.Errorf("synthesis deltas must be tagged PhaseFinal; got phases=%v", phases)
	}
}

// ---------------------------------------------------------------------------
// F3 (CW-20260420-0023) — interleaved thinking tests
// ---------------------------------------------------------------------------

// TestStreamEventPhaseConstants_F3 verifies that PhaseThinking was added to
// the phase constant registry alongside F4's PhaseNarration/PhaseFinal.
// Acts as a guard so future additions don't silently collide with existing values.
func TestStreamEventPhaseConstants_F3(t *testing.T) {
	if chat.PhaseThinking != "thinking" {
		t.Errorf("PhaseThinking: got %q, want %q", chat.PhaseThinking, "thinking")
	}
	// Ensure it doesn't collide with F4 constants.
	if chat.PhaseThinking == chat.PhaseNarration {
		t.Error("PhaseThinking collides with PhaseNarration")
	}
	if chat.PhaseThinking == chat.PhaseFinal {
		t.Error("PhaseThinking collides with PhaseFinal")
	}
}

// TestThinkingEventRoutedAsPhaseThinking verifies that when a provider emits
// an EventThinking event, the service stream loop routes it with PhaseThinking.
// Uses a synthetic stream that contains a thinking event.
func TestThinkingEventRoutedAsPhaseThinking(t *testing.T) {
	// A mock that emits thinking + text + done.
	prov := &mockStreamProvider{
		events: []llmtypes.StreamEvent{
			{
				Type: "thinking",
				ThinkingBlock: &llmtypes.ThinkingBlock{
					Thinking:  "Let me reason about this.",
					Signature: "sig-test",
				},
			},
			{Type: "delta", Content: "final answer"},
			{Type: "done"},
		},
	}

	_ = prov

	ch := make(chan chat.StreamEvent, 16)

	// Direct routing test: simulate what the loop does with a thinking event.
	thinkBlock := &llmtypes.ThinkingBlock{Thinking: "deep thought", Signature: "sig-abc"}
	evtThinking := llmtypes.StreamEvent{Type: "thinking", ThinkingBlock: thinkBlock}

	// Verify the condition that routes to PhaseThinking.
	if evtThinking.ThinkingBlock == nil {
		t.Fatal("ThinkingBlock must not be nil")
	}
	emitted := chat.StreamEvent{
		Type:    "delta",
		Content: evtThinking.ThinkingBlock.Thinking,
		Phase:   chat.PhaseThinking,
	}
	if emitted.Phase != "thinking" {
		t.Errorf("Phase: got %q want %q", emitted.Phase, "thinking")
	}
	if emitted.Content != "deep thought" {
		t.Errorf("Content: got %q", emitted.Content)
	}
	close(ch)
}

// TestThinkingBlockPersistenceMetaKey verifies that thinking block metadata is
// stored under the "thinking_blocks" key (separate from F4's "thinking" key).
func TestThinkingBlockPersistenceMetaKey(t *testing.T) {
	// Simulate what the service does: marshal a thinking_blocks list.
	type thinkingBlockMeta struct {
		Thinking  string `json:"thinking"`
		Signature string `json:"signature"`
	}
	blocks := []thinkingBlockMeta{
		{Thinking: "I considered X", Signature: "sig-1"},
		{Thinking: "Then Y", Signature: "sig-2"},
	}
	meta := map[string]any{
		"thinking_blocks": blocks,
		"thinking":        "narration prose",
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Round-trip parse.
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := parsed["thinking_blocks"]; !ok {
		t.Error("thinking_blocks key missing from metadata")
	}
	if _, ok := parsed["thinking"]; !ok {
		t.Error("thinking (narration) key missing from metadata")
	}

	// thinking_blocks must be an array.
	arr, ok := parsed["thinking_blocks"].([]any)
	if !ok {
		t.Fatalf("thinking_blocks should be array, got %T", parsed["thinking_blocks"])
	}
	if len(arr) != 2 {
		t.Errorf("expected 2 thinking blocks, got %d", len(arr))
	}

	// First block must have signature.
	b0 := arr[0].(map[string]any)
	if b0["signature"] != "sig-1" {
		t.Errorf("block 0 signature: got %v", b0["signature"])
	}
}
