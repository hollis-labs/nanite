package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/chat"
	ctxpkg "github.com/hollis-labs/nanite/internal/context"
	"github.com/hollis-labs/nanite/internal/store"
)

// TestAssembleSlots_FillsAllSlotsFromRawSources locks down the T2 contract:
// AssembleSlots must populate the System / Agent / Rules / Tools / Session /
// Conversation slots from the raw inputs (workspace, agent profile, selected
// tools, session metadata, message history) rather than packing everything
// into the System slot the way the legacy AssembleContext path did.
func TestAssembleSlots_FillsAllSlotsFromRawSources(t *testing.T) {
	s, err := store.New(context.Background(), t.TempDir()+"/test.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}

	client := chat.NewContextClient(s)
	svc := NewContextService(ContextServiceConfig{Client: client})

	sess := &store.Session{ID: "slot-fill-sess", Title: "FillTest", WorkspaceID: ""}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := s.CreateMessage(&store.Message{
		ID:        "slot-fill-msg",
		SessionID: sess.ID,
		Role:      "user",
		Content:   "hello world",
	}); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	agent := &store.AgentProfile{
		ID:           "agent-fill",
		Name:         "FillAgent",
		Slug:         "fill",
		SystemPrompt: "You are FillAgent.",
		Status:       "active",
		Tags:         `["code","backend"]`,
		Tools:        `["dev_grep","dev_read"]`,
	}
	tools := []llmtypes.ToolDefinition{
		{Name: "dev_grep", Description: "Search files via ripgrep"},
		{Name: "dev_read", Description: "Read a file"},
	}

	result, err := svc.AssembleSlots(context.Background(), sess, agent, nil, tools, "Native tool guide.", 200000, "")
	if err != nil {
		t.Fatalf("AssembleSlots: %v", err)
	}
	if result == nil || result.Window == nil {
		t.Fatal("expected non-nil result + window")
	}

	required := map[string]bool{
		ctxpkg.SlotSystem:       false,
		ctxpkg.SlotAgent:        false,
		ctxpkg.SlotRules:        false,
		ctxpkg.SlotTools:        false,
		ctxpkg.SlotSession:      false,
		ctxpkg.SlotConversation: false,
	}
	totalFromBlocks := 0
	for _, b := range result.Blocks {
		if _, ok := required[b.SlotName]; ok {
			required[b.SlotName] = true
		}
		totalFromBlocks += len(b.Content)
	}
	for slot, present := range required {
		if !present {
			t.Errorf("slot %q missing from assembled blocks", slot)
		}
	}

	// Each required slot should also have non-zero TokenCount on its window
	// entry — UsedTokens must reflect per-slot accounting, not zero.
	used := result.Window.UsedTokens()
	if used == 0 {
		t.Error("expected window UsedTokens > 0")
	}
	if got := result.Window.Slot(ctxpkg.SlotTools); got == nil || got.TokenCount == 0 {
		t.Error("Tools slot should have non-zero token count when tools are passed")
	}

	// The Rules slot must surface the agent's tags + tool allowlist verbatim
	// (S4a will expand this; for now confirm the raw projection).
	rulesSlot := result.Window.Slot(ctxpkg.SlotRules)
	if rulesSlot == nil || rulesSlot.Content == "" {
		t.Fatal("Rules slot should be populated from agent profile")
	}
	if !strings.Contains(rulesSlot.Content, "code") {
		t.Errorf("Rules slot should mention agent tags; got %q", rulesSlot.Content)
	}
	if !strings.Contains(rulesSlot.Content, "dev_grep") {
		t.Errorf("Rules slot should mention tool allowlist; got %q", rulesSlot.Content)
	}

	// The Tools slot content must be the JSON marshaling of the tool defs so
	// CacheKey changes when tool selection changes.
	toolsSlot := result.Window.Slot(ctxpkg.SlotTools)
	if toolsSlot == nil {
		t.Fatal("Tools slot missing")
	}
	var roundtrip []llmtypes.ToolDefinition
	if err := json.Unmarshal([]byte(toolsSlot.Content), &roundtrip); err != nil {
		t.Errorf("Tools slot content should be JSON-marshaled tool defs: %v (raw=%q)", err, toolsSlot.Content)
	}
	if len(roundtrip) != len(tools) {
		t.Errorf("Tools slot tool count: got %d want %d", len(roundtrip), len(tools))
	}

	// extraSystemPrefix must appear at the head of the legacy SystemPrompt
	// (the back-compat field consumed by EmitContextAssembled / budget
	// telemetry) — confirms callers like the no-tools warning continue to
	// have a place to land.
	if !strings.HasPrefix(result.SystemPrompt, "Native tool guide.") {
		t.Errorf("SystemPrompt should lead with extraSystemPrefix; got prefix %q", result.SystemPrompt[:min(40, len(result.SystemPrompt))])
	}
}

// TestAssembleSlots_NoToolsLeavesToolsSlotEmpty asserts that calling without
// any tools selected results in an unpopulated Tools slot (no JSON noise) so
// the cache-key for Tools stays stable across consecutive no-tool turns.
func TestAssembleSlots_NoToolsLeavesToolsSlotEmpty(t *testing.T) {
	s, err := store.New(context.Background(), t.TempDir()+"/test.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	client := chat.NewContextClient(s)
	svc := NewContextService(ContextServiceConfig{Client: client})

	sess := &store.Session{ID: "no-tools-sess"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	agent := &store.AgentProfile{ID: "no-tools-agent", Slug: "x", Status: "active"}

	result, err := svc.AssembleSlots(context.Background(), sess, agent, nil, nil, "", 200000, "")
	if err != nil {
		t.Fatalf("AssembleSlots: %v", err)
	}
	toolsSlot := result.Window.Slot(ctxpkg.SlotTools)
	if toolsSlot == nil {
		t.Fatal("Tools slot should exist on the window")
	}
	if toolsSlot.Content != "" {
		t.Errorf("Tools slot should be empty when no tools selected; got %q", toolsSlot.Content)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
