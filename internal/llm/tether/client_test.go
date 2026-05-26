package tether

import (
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	tetherclient "github.com/hollis-labs/go-tether-client"
)

func TestBuildRequest(t *testing.T) {
	got := buildRequest(llmtypes.ChatRequest{
		Model:        "claude-sonnet",
		SystemPrompt: "you are helpful",
		MaxTokens:    512,
		Messages: []llmtypes.ChatMessage{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "hello"},
		},
	}, true)

	r := got.Request
	if r.Operation != "chat" || r.ModelHint != "claude-sonnet" || !r.Streaming || r.MaxOutputTokens != 512 {
		t.Fatalf("request scalars wrong: %+v", r)
	}
	if r.CallerID != callerID {
		t.Errorf("caller id = %q, want %q", r.CallerID, callerID)
	}
	// system prompt becomes a leading system message, then the two turns.
	if len(r.Input) != 3 {
		t.Fatalf("input len = %d, want 3 (system + 2 turns)", len(r.Input))
	}
	if r.Input[0].Role != "system" || r.Input[0].Parts[0].Text != "you are helpful" {
		t.Errorf("leading system message wrong: %+v", r.Input[0])
	}
	if r.Input[1].Role != "user" || r.Input[1].Parts[0].Type != "text" || r.Input[1].Parts[0].Text != "hi" {
		t.Errorf("user message wrong: %+v", r.Input[1])
	}

	// No system prompt → no leading system message.
	none := buildRequest(llmtypes.ChatRequest{Messages: []llmtypes.ChatMessage{{Role: "user", Content: "q"}}}, false)
	if len(none.Request.Input) != 1 || none.Request.Input[0].Role != "user" {
		t.Errorf("expected only the user message, got %+v", none.Request.Input)
	}
	if none.Request.Streaming {
		t.Errorf("streaming should be false for Complete")
	}
}

func TestExtractText(t *testing.T) {
	out := []tetherclient.AIMessage{
		{Role: "assistant", Parts: []tetherclient.AIContentPart{
			{Type: "text", Text: "Hello "},
			{Type: "image", Text: "ignored"},
			{Type: "text", Text: "world"},
		}},
	}
	if got := extractText(out); got != "Hello world" {
		t.Errorf("extractText = %q, want %q", got, "Hello world")
	}
	if got := extractText(nil); got != "" {
		t.Errorf("extractText(nil) = %q, want empty", got)
	}
}

func TestMapUsage(t *testing.T) {
	if mapUsage(tetherclient.AIUsage{}) != nil {
		t.Errorf("empty usage should map to nil")
	}
	u := mapUsage(tetherclient.AIUsage{InputTokens: 10, OutputTokens: 20, CacheReadTokens: 5})
	if u == nil || u.InputTokens != 10 || u.OutputTokens != 20 || u.CacheReadTokens != 5 {
		t.Errorf("mapUsage = %+v, want 10/20/5", u)
	}
}
