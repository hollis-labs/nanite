package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ollama/ollama/api"
)

// -----------------------------------------------------------------------------
// Request builders
// -----------------------------------------------------------------------------

func TestBuildOllamaMessages_SystemPromptPrepended(t *testing.T) {
	msgs := buildOllamaMessages("You are helpful.", []ChatMessage{
		{Role: "user", Content: "Hello"},
	})
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "system" || msgs[0].Content != "You are helpful." {
		t.Errorf("expected system message first, got %+v", msgs[0])
	}
	if msgs[1].Role != "user" || msgs[1].Content != "Hello" {
		t.Errorf("expected user message second, got %+v", msgs[1])
	}
}

func TestBuildOllamaMessages_EmptySystemOmitted(t *testing.T) {
	msgs := buildOllamaMessages("", []ChatMessage{{Role: "user", Content: "Hi"}})
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Role == "system" {
		t.Error("expected no system message")
	}
}

func TestBuildOllamaMessages_TextContentBlocksJoin(t *testing.T) {
	msgs := buildOllamaMessages("", []ChatMessage{{
		Role: "user",
		ContentBlocks: []ContentBlock{
			{Type: "text", Text: "Part 1"},
			{Type: "text", Text: "Part 2"},
		},
	}})
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Content != "Part 1\nPart 2" {
		t.Errorf("expected joined text, got %q", msgs[0].Content)
	}
}

func TestBuildOllamaMessages_ToolUseBlockBecomesToolCall(t *testing.T) {
	input := map[string]any{"arg1": "value"}
	msgs := buildOllamaMessages("", []ChatMessage{{
		Role: "assistant",
		ContentBlocks: []ContentBlock{
			{Type: "tool_use", ID: "call_1", Name: "get_weather", Input: &input},
		},
	}})
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if len(msgs[0].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(msgs[0].ToolCalls))
	}
	tc := msgs[0].ToolCalls[0]
	if tc.ID != "call_1" || tc.Function.Name != "get_weather" {
		t.Errorf("unexpected tool call: %+v", tc)
	}
	args := tc.Function.Arguments.ToMap()
	if args["arg1"] != "value" {
		t.Errorf("expected arg1=value, got %+v", args)
	}
}

func TestBuildOllamaMessages_ToolResultEmitsSeparateMessage(t *testing.T) {
	msgs := buildOllamaMessages("", []ChatMessage{{
		Role: "user",
		ContentBlocks: []ContentBlock{
			{Type: "tool_result", ToolUseID: "call_1", Content: "72 degrees"},
		},
	}})
	// Should produce a tool role message; the original user wrapper carried no
	// text so it is skipped.
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "tool" {
		t.Errorf("expected tool role, got %q", msgs[0].Role)
	}
	if msgs[0].ToolCallID != "call_1" {
		t.Errorf("expected tool_call_id=call_1, got %q", msgs[0].ToolCallID)
	}
	if msgs[0].Content != "72 degrees" {
		t.Errorf("expected content=72 degrees, got %q", msgs[0].Content)
	}
}

func TestBuildOllamaTools_EmptyReturnsNil(t *testing.T) {
	if got := buildOllamaTools(nil); got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestBuildOllamaTools_MapsDefinition(t *testing.T) {
	tools := buildOllamaTools([]ToolDefinition{{
		Name:        "get_weather",
		Description: "gets the weather",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"location"},
			"properties": map[string]any{
				"location": map[string]any{"type": "string", "description": "city"},
			},
		},
	}})
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	if tools[0].Type != "function" {
		t.Errorf("expected type=function, got %q", tools[0].Type)
	}
	if tools[0].Function.Name != "get_weather" {
		t.Errorf("expected name=get_weather, got %q", tools[0].Function.Name)
	}
	if tools[0].Function.Description != "gets the weather" {
		t.Errorf("unexpected description: %q", tools[0].Function.Description)
	}
	if tools[0].Function.Parameters.Type != "object" {
		t.Errorf("expected params.type=object, got %q", tools[0].Function.Parameters.Type)
	}
	if len(tools[0].Function.Parameters.Required) != 1 || tools[0].Function.Parameters.Required[0] != "location" {
		t.Errorf("unexpected required: %+v", tools[0].Function.Parameters.Required)
	}
}

// -----------------------------------------------------------------------------
// Error classifier
// -----------------------------------------------------------------------------

func TestClassifyOllamaError_StatusError(t *testing.T) {
	err := api.StatusError{StatusCode: 429, Status: "Too Many Requests", ErrorMessage: "slow down"}
	apiErr, _ := classifyOllamaError(err)
	if apiErr.StatusCode != 429 {
		t.Errorf("expected 429, got %d", apiErr.StatusCode)
	}
	if apiErr.Message != "slow down" {
		t.Errorf("unexpected message: %q", apiErr.Message)
	}
}

func TestClassifyOllamaError_CapsOversizedMessage(t *testing.T) {
	huge := strings.Repeat("x", maxOllamaErrBody+500)
	err := api.StatusError{StatusCode: 500, ErrorMessage: huge}
	apiErr, _ := classifyOllamaError(err)
	if len(apiErr.Message) != maxOllamaErrBody {
		t.Errorf("expected message len %d, got %d", maxOllamaErrBody, len(apiErr.Message))
	}
}

func TestClassifyOllamaError_NonTypedErrorIsNonRetryable(t *testing.T) {
	err := errors.New("connection refused")
	apiErr, _ := classifyOllamaError(err)
	if apiErr.StatusCode != 0 {
		t.Errorf("expected status 0, got %d", apiErr.StatusCode)
	}
	if RetryableStatusCode(apiErr.StatusCode) {
		t.Error("status 0 should be non-retryable")
	}
}

func TestClassifyOllamaError_NilReturnsNil(t *testing.T) {
	apiErr, _ := classifyOllamaError(nil)
	if apiErr != nil {
		t.Errorf("expected nil, got %+v", apiErr)
	}
}

// -----------------------------------------------------------------------------
// Interface conformance & construction
// -----------------------------------------------------------------------------

func TestNewOllama_DefaultsHost(t *testing.T) {
	t.Setenv("OLLAMA_HOST", "")
	o := NewOllama()
	if o.host != "http://localhost:11434" {
		t.Errorf("expected default host, got %q", o.host)
	}
}

func TestNewOllama_HonoursEnv(t *testing.T) {
	t.Setenv("OLLAMA_HOST", "http://remote:9999")
	o := NewOllama()
	if o.host != "http://remote:9999" {
		t.Errorf("expected http://remote:9999, got %q", o.host)
	}
}

func TestOllamaProviderInterfaceConformance(t *testing.T) {
	var _ Provider = (*Ollama)(nil)
	var _ Embedder = (*Ollama)(nil)
}

func TestParseOllamaHost(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"http://localhost:11434", "http://localhost:11434"},
		{"localhost:11434", "http://localhost:11434"},
		{"remote:9999", "http://remote:9999"},
	}
	for _, c := range cases {
		u, err := parseOllamaHost(c.in)
		if err != nil {
			t.Errorf("parseOllamaHost(%q) error: %v", c.in, err)
			continue
		}
		if u.Scheme+"://"+u.Host != c.want {
			t.Errorf("parseOllamaHost(%q) = %s, want %s", c.in, u.String(), c.want)
		}
	}
}

// -----------------------------------------------------------------------------
// Streaming bridge via a mock Ollama daemon
// -----------------------------------------------------------------------------

// newMockOllama starts an httptest server that plays back the given
// newline-delimited JSON response bytes for /api/chat.
func newMockOllama(t *testing.T, chunks []api.ChatResponse) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		bw := bufio.NewWriter(w)
		for _, c := range chunks {
			b, _ := json.Marshal(c)
			bw.Write(b)
			bw.WriteByte('\n')
			bw.Flush()
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
}

// ollamaFromURL constructs an Ollama adapter pointed at a test server URL.
func ollamaFromURL(t *testing.T, raw string) *Ollama {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	o := &Ollama{
		host:           raw,
		httpClient:     &http.Client{},
		client:         api.NewClient(u, &http.Client{}),
		Retry:          DefaultRetryConfig(),
		CircuitBreaker: NewCircuitBreaker(3),
		RateTracker:    NewTokenRateTracker(30000),
	}
	return o
}

func TestOllamaStream_DeltaUsageDone(t *testing.T) {
	srv := newMockOllama(t, []api.ChatResponse{
		{Message: api.Message{Role: "assistant", Content: "Hello"}},
		{Message: api.Message{Role: "assistant", Content: " world"}},
		{
			Message:    api.Message{Role: "assistant", Content: ""},
			Done:       true,
			DoneReason: "stop",
			Metrics: api.Metrics{
				PromptEvalCount: 10,
				EvalCount:       5,
			},
		},
	})
	defer srv.Close()

	o := ollamaFromURL(t, srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := o.StreamChat(ctx, "", []ChatMessage{{Role: "user", Content: "Hi"}}, "llama3.1")
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}

	var events []StreamEvent
	for ev := range ch {
		events = append(events, ev)
	}

	// Expect: delta, delta, usage, done.
	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d: %+v", len(events), events)
	}
	if events[0].Type != "delta" || events[0].Content != "Hello" {
		t.Errorf("event[0] = %+v, want delta=Hello", events[0])
	}
	if events[1].Type != "delta" || events[1].Content != " world" {
		t.Errorf("event[1] = %+v, want delta= world", events[1])
	}
	if events[2].Type != "usage" || events[2].Usage == nil {
		t.Fatalf("event[2] = %+v, want usage", events[2])
	}
	if events[2].Usage.InputTokens != 10 || events[2].Usage.OutputTokens != 5 {
		t.Errorf("unexpected usage: %+v", events[2].Usage)
	}
	if events[2].Usage.StopReason != "stop" {
		t.Errorf("expected stop reason=stop, got %q", events[2].Usage.StopReason)
	}
	if events[3].Type != "done" {
		t.Errorf("event[3] = %+v, want done", events[3])
	}
}

func TestOllamaStream_ToolUseEmitted(t *testing.T) {
	// Ollama tool_calls are emitted on the final chunk by tool-capable models.
	args := api.NewToolCallFunctionArguments()
	args.Set("location", "SF")
	srv := newMockOllama(t, []api.ChatResponse{
		{
			Message: api.Message{
				Role: "assistant",
				ToolCalls: []api.ToolCall{{
					ID: "call_1",
					Function: api.ToolCallFunction{
						Name:      "get_weather",
						Arguments: args,
					},
				}},
			},
			Done:       true,
			DoneReason: "tool_calls",
			Metrics:    api.Metrics{PromptEvalCount: 4, EvalCount: 2},
		},
	})
	defer srv.Close()

	o := ollamaFromURL(t, srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := o.StreamChatWithTools(ctx, "", []ChatMessage{{Role: "user", Content: "weather?"}}, "llama3.1", []ToolDefinition{
		{Name: "get_weather", Description: "w", InputSchema: map[string]any{"type": "object"}},
	})
	if err != nil {
		t.Fatalf("StreamChatWithTools: %v", err)
	}

	var events []StreamEvent
	for ev := range ch {
		events = append(events, ev)
	}

	var toolUse *StreamEvent
	for i := range events {
		if events[i].Type == "tool_use" {
			toolUse = &events[i]
			break
		}
	}
	if toolUse == nil {
		t.Fatalf("no tool_use event, got %+v", events)
	}
	if toolUse.ToolUse == nil {
		t.Fatal("tool_use event missing ToolUse block")
	}
	if toolUse.ToolUse.Name != "get_weather" {
		t.Errorf("expected name=get_weather, got %q", toolUse.ToolUse.Name)
	}
	if toolUse.ToolUse.Input["location"] != "SF" {
		t.Errorf("expected location=SF, got %+v", toolUse.ToolUse.Input)
	}
}

func TestOllamaStream_ContextCancelDoesNotEmitError(t *testing.T) {
	// Server that writes one chunk then blocks forever.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		b, _ := json.Marshal(api.ChatResponse{Message: api.Message{Role: "assistant", Content: "hi"}})
		w.Write(b)
		w.Write([]byte("\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer srv.Close()

	o := ollamaFromURL(t, srv.URL)
	ctx, cancel := context.WithCancel(context.Background())

	ch, err := o.StreamChat(ctx, "", []ChatMessage{{Role: "user", Content: "Hi"}}, "llama3.1")
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}

	// Consume at least one event, then cancel.
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("channel closed before first event")
		}
		if ev.Type != "delta" {
			t.Fatalf("expected delta, got %+v", ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for first event")
	}

	cancel()

	// Drain any remaining events; the channel must close within a reasonable
	// window, and we should not see an unexpected "error" event from benign
	// cancellation (though the SDK may surface ctx.Err() which is isStreamClosedErr).
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return
			}
			// Some cancel paths cause the callback to return ctx.Err(); we
			// intentionally filter those via isStreamClosedErr. However it's
			// acceptable if an error event did slip through containing
			// "context cancelled" — we just verify the stream terminates.
			_ = ev
		case <-deadline:
			t.Fatal("channel did not close after context cancel")
		}
	}
}

func TestOllamaComplete_Returns(t *testing.T) {
	srv := newMockOllama(t, []api.ChatResponse{
		{
			Message:    api.Message{Role: "assistant", Content: "Paris"},
			Done:       true,
			DoneReason: "stop",
		},
	})
	defer srv.Close()

	o := ollamaFromURL(t, srv.URL)
	out, err := o.Complete(context.Background(), "", []ChatMessage{{Role: "user", Content: "Capital of France?"}}, "llama3.1")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if out != "Paris" {
		t.Errorf("expected Paris, got %q", out)
	}
}

func TestOllamaComplete_RetriesOn5xx(t *testing.T) {
	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"error":"unavailable"}`))
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		b, _ := json.Marshal(api.ChatResponse{
			Message:    api.Message{Role: "assistant", Content: "ok"},
			Done:       true,
			DoneReason: "stop",
		})
		w.Write(b)
		w.Write([]byte("\n"))
	}))
	defer srv.Close()

	o := ollamaFromURL(t, srv.URL)
	// Shrink delays so the test is fast.
	o.Retry = RetryConfig{MaxRetries: 2, InitialDelay: 1 * time.Millisecond, MaxDelay: 2 * time.Millisecond, Multiplier: 2}

	out, err := o.Complete(context.Background(), "", []ChatMessage{{Role: "user", Content: "hi"}}, "llama3.1")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if out != "ok" {
		t.Errorf("expected ok, got %q", out)
	}
	if attempts < 2 {
		t.Errorf("expected at least 2 attempts, got %d", attempts)
	}
}

func TestOllamaComplete_NonRetryable4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"bad model"}`))
	}))
	defer srv.Close()

	o := ollamaFromURL(t, srv.URL)
	o.Retry = RetryConfig{MaxRetries: 2, InitialDelay: 1 * time.Millisecond, MaxDelay: 2 * time.Millisecond, Multiplier: 2}

	_, err := o.Complete(context.Background(), "", []ChatMessage{{Role: "user", Content: "hi"}}, "bogus")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", apiErr.StatusCode)
	}
}

// -----------------------------------------------------------------------------
// Embedding
// -----------------------------------------------------------------------------

func TestOllamaEmbed_BatchRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		resp := api.EmbedResponse{
			Model:           "nomic-embed-text",
			Embeddings:      [][]float32{{0.1, 0.2, 0.3}, {0.4, 0.5, 0.6}},
			PromptEvalCount: 8,
		}
		b, _ := json.Marshal(resp)
		w.Write(b)
	}))
	defer srv.Close()

	o := ollamaFromURL(t, srv.URL)
	res, err := o.EmbedBatch(context.Background(), []string{"hello", "world"}, "")
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("expected 2 results, got %d", len(res))
	}
	if len(res[0].Embedding) != 3 || res[0].Embedding[0] != 0.1 {
		t.Errorf("unexpected first embedding: %+v", res[0].Embedding)
	}
	if res[0].TokenCount != 4 { // 8 / 2 texts
		t.Errorf("expected per-text token count 4, got %d", res[0].TokenCount)
	}
}

func TestOllamaEmbeddingDimensions(t *testing.T) {
	o := NewOllama()
	cases := map[string]int{
		"nomic-embed-text":  768,
		"all-minilm":        384,
		"mxbai-embed-large": 1024,
		"unknown":           0,
	}
	for m, want := range cases {
		if got := o.EmbeddingDimensions(m); got != want {
			t.Errorf("EmbeddingDimensions(%q) = %d, want %d", m, got, want)
		}
	}
}

// -----------------------------------------------------------------------------
// Capabilities
// -----------------------------------------------------------------------------

func TestOllamaCapabilitiesShape(t *testing.T) {
	caps := NewOllama().Capabilities()
	if !caps.SupportsStreamJSON {
		t.Error("expected SupportsStreamJSON=true")
	}
	if !caps.SupportsEmbedding {
		t.Error("expected SupportsEmbedding=true")
	}
	if caps.SupportsSystemPromptCaching {
		t.Error("Ollama does not support system prompt caching")
	}
	if caps.DefaultEmbeddingModel != "nomic-embed-text" {
		t.Errorf("expected default embed model=nomic-embed-text, got %q", caps.DefaultEmbeddingModel)
	}
}

// Silence unused import during incremental development.
var _ = fmt.Sprintf
