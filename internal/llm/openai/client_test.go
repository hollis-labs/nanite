package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/openai/openai-go/option"
)

// newTestClient builds a Client wired to an httptest.Server. The handler
// receives the raw request and writes a canned response.
func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return New("test-key", srv.Client(), option.WithBaseURL(srv.URL+"/"))
}

// readJSON drains and decodes a request body for assertion.
func readJSON(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal body: %v\nraw: %s", err, body)
	}
	return out
}

func TestComplete_BasicRoundTrip(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body := readJSON(t, r)
		if body["model"] != "gpt-test" {
			t.Errorf("model = %v", body["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"x","object":"chat.completion","created":1,"model":"gpt-test",
			"choices":[{"index":0,"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`))
	})
	got, err := c.Complete(context.Background(), llmtypes.ChatRequest{
		Model:        "gpt-test",
		SystemPrompt: "be brief",
		Messages:     []llmtypes.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got != "answer" {
		t.Errorf("Complete = %q, want answer", got)
	}
}

func TestComplete_UnsupportedRoleErrors(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("HTTP must not be called for unsupported role")
	})
	_, err := c.Complete(context.Background(), llmtypes.ChatRequest{
		Model:    "gpt",
		Messages: []llmtypes.ChatMessage{{Role: "function", Content: "x"}},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported message role") {
		t.Errorf("err = %v, want unsupported-role error", err)
	}
}

// TestStreamChat_DeltasToolCallUsage exercises the full streaming surface:
// text deltas → tool_call (split across two chunks) → final usage chunk →
// done event. Asserts ordering and shape of internal StreamEvent flow.
func TestStreamChat_DeltasToolCallUsage(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		write := func(s string) {
			_, _ = io.WriteString(w, "data: "+s+"\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		}
		// Two text deltas
		write(`{"id":"x","object":"chat.completion.chunk","created":1,"model":"gpt-test","choices":[{"index":0,"delta":{"role":"assistant","content":"hello"},"finish_reason":""}]}`)
		write(`{"id":"x","object":"chat.completion.chunk","created":1,"model":"gpt-test","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":""}]}`)
		// Tool call delivered in two pieces (id+name first, args streamed)
		write(`{"id":"x","object":"chat.completion.chunk","created":1,"model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"dev_glob","arguments":"{\"pat"}}]},"finish_reason":""}]}`)
		write(`{"id":"x","object":"chat.completion.chunk","created":1,"model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"tern\":\"*.go\"}"}}]},"finish_reason":"tool_calls"}]}`)
		// Final usage chunk
		write(`{"id":"x","object":"chat.completion.chunk","created":1,"model":"gpt-test","choices":[],"usage":{"prompt_tokens":12,"completion_tokens":34,"total_tokens":46}}`)
		write(`[DONE]`)
	})

	ch, err := c.StreamChat(context.Background(), llmtypes.ChatRequest{
		Model:    "gpt-test",
		Messages: []llmtypes.ChatMessage{{Role: "user", Content: "ls"}},
		Tools: []llmtypes.ToolDefinition{{
			Name:        "dev_glob",
			Description: "glob a path",
			InputSchema: map[string]any{"type": "object"},
		}},
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}

	var (
		text     strings.Builder
		toolUse  *llmtypes.ToolUseBlock
		usage    *llmtypes.Usage
		gotDone  bool
		gotError string
	)
	for ev := range ch {
		switch ev.Type {
		case llmtypes.EventDelta:
			text.WriteString(ev.Content)
		case llmtypes.EventToolUse:
			tu := *ev.ToolUse
			toolUse = &tu
		case llmtypes.EventUsage:
			usage = ev.Usage
		case llmtypes.EventDone:
			gotDone = true
		case llmtypes.EventError:
			gotError = ev.Error
		}
	}
	if gotError != "" {
		t.Fatalf("error event: %s", gotError)
	}
	if got := text.String(); got != "hello world" {
		t.Errorf("text = %q, want %q", got, "hello world")
	}
	if toolUse == nil || toolUse.Name != "dev_glob" || toolUse.ID != "call_1" {
		t.Fatalf("toolUse = %+v", toolUse)
	}
	if v, ok := toolUse.Input["pattern"]; !ok || v != "*.go" {
		t.Errorf("toolUse.Input = %v, want pattern=*.go", toolUse.Input)
	}
	if usage == nil || usage.InputTokens != 12 || usage.OutputTokens != 34 {
		t.Errorf("usage = %+v", usage)
	}
	if usage != nil && usage.StopReason != "tool_use" {
		t.Errorf("usage.StopReason = %q, want tool_use", usage.StopReason)
	}
	if !gotDone {
		t.Errorf("missing done event")
	}
}

func TestStreamChat_ErrorTranslated(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":"rate_limit","message":"slow down","type":"rate_limit_error","param":""}}`))
	})
	ch, err := c.StreamChat(context.Background(), llmtypes.ChatRequest{
		Model:    "gpt-test",
		Messages: []llmtypes.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	var (
		gotError string
		gotDone  bool
	)
	for ev := range ch {
		switch ev.Type {
		case llmtypes.EventError:
			gotError = ev.Error
		case llmtypes.EventDone:
			gotDone = true
		}
	}
	if gotError == "" {
		t.Errorf("expected error event")
	}
	if gotDone {
		t.Errorf("done event must not follow an error")
	}
	if !strings.Contains(gotError, "openai") {
		t.Errorf("error not translated: %q", gotError)
	}
}

func TestCapabilities_HasReasonableDefaults(t *testing.T) {
	c := New("test-key", nil)
	caps := c.Capabilities()
	if !caps.SupportsToolCalling {
		t.Error("SupportsToolCalling should be true")
	}
	if !caps.SupportsEmbedding {
		t.Error("SupportsEmbedding should be true")
	}
	if caps.DefaultEmbeddingModel == "" {
		t.Error("DefaultEmbeddingModel should be set")
	}
}
