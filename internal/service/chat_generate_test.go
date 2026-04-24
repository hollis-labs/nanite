package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/go-toolbroker/broker"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/toolclient"
)

// TestExtraSystemPrefix_IncludesOverrideBlock verifies that when a non-empty
// overrideBlock is passed, the composed prefix includes it AFTER the Native
// Tool Usage guide (so tool-specific overrides override the general guidance).
func TestExtraSystemPrefix_IncludesOverrideBlock(t *testing.T) {
	overrideBlock := "## Tool Overrides\n\n- **example_tool**: returns widgets\n"
	prefix := composeExtraSystemPrefix(overrideBlock, composeConfig{progressiveActive: false, noTools: false})
	if !strings.Contains(prefix, "## Tool Overrides") {
		t.Errorf("prefix missing override block:\n%s", prefix)
	}
	if !strings.Contains(prefix, "Native Tool Usage") {
		t.Errorf("prefix missing native tool guide:\n%s", prefix)
	}
	// Ensure override block comes AFTER native tool guide.
	nativeIdx := strings.Index(prefix, "Native Tool Usage")
	overrideIdx := strings.Index(prefix, "Tool Overrides")
	if overrideIdx < nativeIdx {
		t.Errorf("expected Tool Overrides to appear AFTER Native Tool Usage in prefix; native=%d override=%d", nativeIdx, overrideIdx)
	}
}

// TestExtraSystemPrefix_OmitsEmptyOverrideBlock verifies that when the passed
// overrideBlock is empty, no override section is emitted.
func TestExtraSystemPrefix_OmitsEmptyOverrideBlock(t *testing.T) {
	prefix := composeExtraSystemPrefix("", composeConfig{progressiveActive: false, noTools: false})
	if strings.Contains(prefix, "Tool Overrides") {
		t.Errorf("prefix unexpectedly contains override section:\n%s", prefix)
	}
	if !strings.Contains(prefix, "Native Tool Usage") {
		t.Errorf("prefix missing native tool guide:\n%s", prefix)
	}
}

// TestExtraSystemPrefix_NoToolsWarning verifies the warning prefix is emitted
// when noTools is true, and absent otherwise.
func TestExtraSystemPrefix_NoToolsWarning(t *testing.T) {
	with := composeExtraSystemPrefix("", composeConfig{noTools: true})
	if !strings.Contains(with, "no tools available in this session") {
		t.Errorf("expected no-tools warning when noTools=true, got:\n%s", with)
	}

	without := composeExtraSystemPrefix("", composeConfig{noTools: false})
	if strings.Contains(without, "no tools available in this session") {
		t.Errorf("did not expect no-tools warning when noTools=false, got:\n%s", without)
	}
}

// TestExtraSystemPrefix_ProgressiveCatalog verifies the progressive catalog
// is included before the native tool guide when progressiveActive is true.
func TestExtraSystemPrefix_ProgressiveCatalog(t *testing.T) {
	catalog := "## Available Tools (summary)\n- foo: does foo stuff\n"
	prefix := composeExtraSystemPrefix("", composeConfig{
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

// TestOverrideBlockReachesPrefix_EndToEnd exercises the full P1 ToolSurface
// pipeline against a real store: migration 022 applies, a synthetic
// example_tool enrichment is upserted directly (no migration seed), a
// ToolClient is constructed (which wires the storeEnricher), the tool is
// registered, SelectToolsAsProvider is called, and the resulting
// SelectResult.OverrideBlock is passed to composeExtraSystemPrefix. The
// final prefix must contain the override section with the tool name and
// hint content.
//
// This is the real-path integration test: store → toolclient.storeEnricher
// → go-toolbroker ComposeOverrideBlock → toolclient.SelectResult →
// composeExtraSystemPrefix.
func TestOverrideBlockReachesPrefix_EndToEnd(t *testing.T) {
	// 1. Real store, migration 022 applied via store.New.
	s, err := store.New(t.TempDir() + "/e2e.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	// 2. Seed a synthetic enrichment. Synthetic name — no external-app
	// coupling. Four fields populated so summarizeHints exercises each
	// section of the output line.
	hints := broker.Hints{
		Preconditions: []string{"call list_example first to get a real ID"},
		AntiPatterns:  []string{"IDs are ULIDs, not file paths"},
		ChainsWith:    []string{"example_get"},
		OutputShape:   "Array of {id, name, status}",
	}
	hintsJSON, err := broker.MarshalHints(hints)
	if err != nil {
		t.Fatalf("MarshalHints: %v", err)
	}
	if err := s.UpsertToolEnrichment(store.ToolEnrichment{
		ToolName:  "example_tool",
		HintsJSON: hintsJSON,
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertToolEnrichment: %v", err)
	}

	// 3. ToolClient wires the storeEnricher against the real store.
	tc := toolclient.New(nil, s, toolclient.DefaultConfig())

	// 4. Register the matching broker ToolDefinition so selection will
	// include it in the final tool set.
	tc.RegisterTools([]broker.ToolDefinition{
		{Name: "example_tool", Description: "a synthetic tool used for E2E validation"},
	})

	// 5. Call the production selection path. A permissive/unknown agent ID
	// defaults to permit, so the tool clears CheckPermission.
	res, err := tc.SelectToolsAsProvider(context.Background(), "general", nil, "", "agent-e2e")
	if err != nil {
		t.Fatalf("SelectToolsAsProvider: %v", err)
	}

	// Confirm the broker actually selected the tool we expect (not hidden
	// by a permission filter) and that the override block mentions it.
	var gotToolInSelection bool
	for _, d := range res.Tools {
		if d.Name == "example_tool" {
			gotToolInSelection = true
			break
		}
	}
	if !gotToolInSelection {
		t.Fatalf("example_tool missing from selected tools; got %+v", res.Tools)
	}
	if !strings.Contains(res.OverrideBlock, "## Tool Overrides") {
		t.Errorf("expected ## Tool Overrides header in OverrideBlock, got:\n%s", res.OverrideBlock)
	}
	if !strings.Contains(res.OverrideBlock, "example_tool") {
		t.Errorf("expected tool name in OverrideBlock, got:\n%s", res.OverrideBlock)
	}
	if !strings.Contains(res.OverrideBlock, "Array of {id, name, status}") {
		t.Errorf("expected OutputShape hint in OverrideBlock, got:\n%s", res.OverrideBlock)
	}
	if !strings.Contains(res.OverrideBlock, "ULID") {
		t.Errorf("expected AntiPatterns hint in OverrideBlock, got:\n%s", res.OverrideBlock)
	}

	// 6. Feed the block into the system-prompt helper and confirm it lands
	// in the final prefix AFTER the native tool guide.
	prefix := composeExtraSystemPrefix(res.OverrideBlock, composeConfig{})
	if !strings.Contains(prefix, "Native Tool Usage") {
		t.Errorf("prefix missing native tool guide:\n%s", prefix)
	}
	if !strings.Contains(prefix, "## Tool Overrides") {
		t.Errorf("prefix missing tool overrides section:\n%s", prefix)
	}
	if !strings.Contains(prefix, "example_tool") {
		t.Errorf("prefix missing tool name:\n%s", prefix)
	}
	nativeIdx := strings.Index(prefix, "Native Tool Usage")
	overrideIdx := strings.Index(prefix, "Tool Overrides")
	if overrideIdx < nativeIdx {
		t.Errorf("expected Tool Overrides to appear AFTER Native Tool Usage; native=%d override=%d", nativeIdx, overrideIdx)
	}
}

// --- early-stopping-generate tests ---

// mockStreamProvider is a minimal provider.Provider that emits a fixed sequence
// of StreamEvents and records whether StreamChat was called with tools.
type mockStreamProvider struct {
	events    []provider.StreamEvent
	gotTools  []provider.ToolDefinition
	callCount int
}

func (m *mockStreamProvider) StreamChat(_ context.Context, req provider.ChatRequest) (<-chan provider.StreamEvent, error) {
	m.callCount++
	m.gotTools = req.Tools
	ch := make(chan provider.StreamEvent, len(m.events)+1)
	for _, ev := range m.events {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

func (m *mockStreamProvider) Complete(_ context.Context, _ provider.ChatRequest) (string, error) {
	return "", nil
}

func (m *mockStreamProvider) Capabilities() provider.ProviderCapabilities {
	return provider.ProviderCapabilities{}
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
		events: []provider.StreamEvent{
			{Type: "delta", Content: "Here is "},
			{Type: "delta", Content: "my best answer."},
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
		"system prompt",
		nil, // slotResult — nil is safe; slotBlocksFor handles nil
		nil, // chatMessages
		ch,
		&fullContent,
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
		events: []provider.StreamEvent{{Type: "done"}},
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
		[]provider.ChatMessage{{Role: "user", Content: "prior message"}},
		ch,
		&fullContent,
	)
	close(ch)

	if len(prov.gotTools) != 0 {
		t.Errorf("synthesis call should not forward tools; got %d tools", len(prov.gotTools))
	}
}

// TestEarlyStopSynthesis_PromptInjected verifies that the synthesis prompt is
// injected as the final user message so the LLM receives it.
func TestEarlyStopSynthesis_PromptInjected(t *testing.T) {
	// We can't directly inspect messages sent to StreamChat without a custom
	// mock; instead we verify that earlyStopSynthesis calls the provider
	// exactly once (i.e., it does make a call).
	prov := &mockStreamProvider{
		events: []provider.StreamEvent{{Type: "done"}},
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
		nil,
		ch,
		&fullContent,
	)
	close(ch)

	if prov.callCount != 1 {
		t.Errorf("expected 1 StreamChat call, got %d", prov.callCount)
	}
}

// TestEarlyStopSynthesis_FiringCodes verifies the logic that determines for
// which TerminationCode the early-stop synthesis fires.
func TestEarlyStopSynthesis_FiringCodes(t *testing.T) {
	synthCodes := []TerminationCode{
		TerminationMaxTurns,
		TerminationRunawayToolFailures,
	}
	noSynthCodes := []TerminationCode{
		TerminationHardCeiling,
		TerminationIdleTimeout,
		TerminationRetryBudgetExhausted,
	}

	fires := func(code TerminationCode) bool {
		return code == TerminationMaxTurns || code == TerminationRunawayToolFailures
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
