package anthropic

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	llmtypes "github.com/hollis-labs/go-llm-types"
)

func TestClient_ImplementsContracts(t *testing.T) {
	// Compile-time assertions are in client.go; these explicitly type-check
	// at runtime so failures surface as a test rather than a build error.
	c := New()
	var _ llmcontracts.Provider = c
	var _ llmcontracts.RateLimited = c
	var _ llmcontracts.CacheableProvider = c
	var _ llmcontracts.Cacheable = c
}

func TestClient_RateLimitTPM_PreCalibrationReturnsZero(t *testing.T) {
	c := New()
	if got := c.RateLimitTPM(); got != 0 {
		t.Fatalf("pre-calibration RateLimitTPM = %d, want 0 (unknown)", got)
	}
}

func TestClient_RateLimitTPM_AfterCalibration(t *testing.T) {
	c := New()
	c.calibrated.Store(true)
	c.RateTracker.UpdateLimit(80000)
	if got := c.RateLimitTPM(); got != 80000 {
		t.Fatalf("post-calibration RateLimitTPM = %d, want 80000", got)
	}
}

func TestClient_StreamChat_RequiresAPIKey(t *testing.T) {
	c := New()
	_, err := c.StreamChat(context.Background(), llmtypes.ChatRequest{
		Model: "claude-sonnet-4-5",
		Messages: []llmtypes.ChatMessage{
			{Role: "user", Content: "hi"},
		},
	})
	if err == nil {
		t.Fatal("expected error when API key is empty")
	}
	if !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Fatalf("err mentions wrong thing: %v", err)
	}
}

func TestClient_StreamChat_CircuitOpenReturnsError(t *testing.T) {
	c := New()
	c.SetAPIKey("test-key")
	// Trip the breaker.
	for i := 0; i < DefaultBreakerThreshold; i++ {
		c.CircuitBreaker.RecordFailure()
	}
	if !c.CircuitBreaker.IsOpen() {
		t.Fatal("setup: breaker should be open")
	}

	_, err := c.StreamChat(context.Background(), llmtypes.ChatRequest{
		Model: "claude-sonnet-4-5",
		Messages: []llmtypes.ChatMessage{
			{Role: "user", Content: "hi"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "circuit breaker open") {
		t.Fatalf("expected circuit-breaker-open error, got %v", err)
	}
}

func TestClient_SetCacheHints(t *testing.T) {
	c := New()
	hints := []llmcontracts.CacheHint{
		{Position: "system", Index: 0},
		{Position: "tools", Index: 0},
		{Position: "recent_message", Index: 0},
		{Position: "recent_message", Index: 1},
	}
	c.SetCacheHints(hints)

	if !c.hasCacheHint("system") {
		t.Error("hasCacheHint('system') = false")
	}
	if !c.hasCacheHint("tools") {
		t.Error("hasCacheHint('tools') = false")
	}
	if c.hasCacheHint("nonexistent") {
		t.Error("hasCacheHint('nonexistent') = true")
	}
	if got := c.recentMessageCacheCount(); got != 2 {
		t.Errorf("recentMessageCacheCount = %d, want 2", got)
	}
}

func TestClient_Capabilities(t *testing.T) {
	c := New()
	caps := c.Capabilities()
	if !caps.SupportsStreamJSON {
		t.Error("expected SupportsStreamJSON")
	}
	if !caps.SupportsSystemPromptCaching {
		t.Error("expected SupportsSystemPromptCaching")
	}
	if !caps.SupportsToolCalling {
		t.Error("expected SupportsToolCalling")
	}
	if !caps.SupportsImageInput {
		t.Error("expected SupportsImageInput")
	}
	if caps.MaxTokens <= 0 {
		t.Error("expected MaxTokens > 0")
	}
	if caps.ContextWindowSize <= 0 {
		t.Error("expected ContextWindowSize > 0")
	}
}

func TestModelSupportsInterleavedThinking(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"claude-opus-4-20250514", true},
		{"claude-sonnet-4-5-20250930", true},
		{"claude-haiku-4-5-20251001", true},
		{"claude-sonnet-4-20250101", false}, // pre-min date
		{"claude-opus-4-0", false},          // no date
		{"claude-opus-3-5-20250514", false}, // wrong major
		{"claude-opus-40-20250514", false},  // false-prefix
		{"claude-sonnet-4-5", false},        // no date suffix
		{"gpt-4-turbo-20240414", false},     // wrong family
		{"", false},
	}
	for _, tc := range cases {
		if got := modelSupportsInterleavedThinking(tc.model); got != tc.want {
			t.Errorf("modelSupportsInterleavedThinking(%q) = %v, want %v", tc.model, got, tc.want)
		}
	}
}

func TestShouldEnableInterleavedThinking(t *testing.T) {
	supportedModel := "claude-sonnet-4-5-20250930"
	cases := []struct {
		name string
		cfg  llmcontracts.ReasoningConfig
		want bool
	}{
		{
			name: "all conditions met",
			cfg: llmcontracts.ReasoningConfig{
				Enabled: true, BudgetTokens: 4096, BetasHeader: InterleavedThinkingBetaHeader,
			},
			want: true,
		},
		{
			name: "disabled",
			cfg:  llmcontracts.ReasoningConfig{Enabled: false, BudgetTokens: 4096, BetasHeader: InterleavedThinkingBetaHeader},
			want: false,
		},
		{
			name: "zero budget",
			cfg:  llmcontracts.ReasoningConfig{Enabled: true, BudgetTokens: 0, BetasHeader: InterleavedThinkingBetaHeader},
			want: false,
		},
		{
			name: "wrong beta header",
			cfg:  llmcontracts.ReasoningConfig{Enabled: true, BudgetTokens: 4096, BetasHeader: "other-beta"},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldEnableInterleavedThinking(tc.cfg, supportedModel); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEstimateCacheablePrefix_NoHintsReturnsZero(t *testing.T) {
	c := New()
	c.SetAPIKey("test-key")
	got := c.EstimateCacheablePrefix(context.Background(), llmtypes.ChatRequest{
		Model:        "claude-sonnet-4-5",
		SystemPrompt: "system",
		Messages:     []llmtypes.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if got != 0 {
		t.Errorf("expected 0 with no cache hints, got %d", got)
	}
}

func TestEstimateCacheablePrefix_WithHintsNonZero(t *testing.T) {
	c := New()
	c.SetAPIKey("test-key")
	c.SetCacheHints([]llmcontracts.CacheHint{{Position: "system"}})
	got := c.EstimateCacheablePrefix(context.Background(), llmtypes.ChatRequest{
		Model:        "claude-sonnet-4-5",
		SystemPrompt: "you are a helpful assistant — long enough to leave a marker",
		Messages:     []llmtypes.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if got <= 0 {
		t.Errorf("expected non-zero estimate when system hint produces marker, got %d", got)
	}
}

func TestStreamChat_PreFlightExceedsBudget(t *testing.T) {
	c := New()
	c.SetAPIKey("test-key")
	// Calibrate to a tiny limit so any non-trivial request exceeds it.
	c.RateTracker.UpdateLimit(10) // 10 tokens/min
	c.calibrated.Store(true)

	// Build a request large enough that estimatedTokens > 10.
	largeContent := strings.Repeat("hello world ", 200) // ~2400 chars / 4 = ~600 tokens
	_, err := c.StreamChat(context.Background(), llmtypes.ChatRequest{
		Model: "claude-sonnet-4-5",
		Messages: []llmtypes.ChatMessage{
			{Role: "user", Content: largeContent},
		},
	})
	if err == nil {
		t.Fatal("expected ErrRequestExceedsRateBudget")
	}
	if !errors.Is(err, llmcontracts.ErrRequestExceedsRateBudget) {
		t.Fatalf("expected ErrRequestExceedsRateBudget, got %v", err)
	}
	// Verify the wrap format the chat-service regex parser expects.
	if !strings.Contains(err.Error(), "estimated") || !strings.Contains(err.Error(), "tokens vs") || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("error format does not match parser: %v", err)
	}
}

func TestStreamChat_MalformedToolArgumentsGracefulFallback(t *testing.T) {
	c := newStreamingTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		flusher, _ := w.(http.Flusher)
		lines := []string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"id":"msg_bad","type":"message","role":"assistant","model":"claude-sonnet-4-5-20250514","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":4,"output_tokens":0}}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_bad","name":"dev_glob","input":{}}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"pattern\":"}}`,
			``,
			`event: content_block_stop`,
			`data: {"type":"content_block_stop","index":0}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":5}}`,
			``,
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			``,
		}
		for _, line := range lines {
			_, _ = io.WriteString(w, line+"\n")
		}
		if flusher != nil {
			flusher.Flush()
		}
	})

	ch, err := c.StreamChat(context.Background(), llmtypes.ChatRequest{
		Model:    "claude-sonnet-4-5-20250514",
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
		toolUse      *llmtypes.ToolUseBlock
		inputTokens  int
		outputTokens int
		stopReason   string
		gotDone      bool
		gotError     string
	)
	for ev := range ch {
		switch ev.Type {
		case llmtypes.EventToolUse:
			tu := *ev.ToolUse
			toolUse = &tu
		case llmtypes.EventUsage:
			if ev.Usage != nil {
				inputTokens += ev.Usage.InputTokens
				outputTokens += ev.Usage.OutputTokens
				if ev.Usage.StopReason != "" {
					stopReason = ev.Usage.StopReason
				}
			}
		case llmtypes.EventDone:
			gotDone = true
		case llmtypes.EventError:
			gotError = ev.Error
		}
	}
	if gotError != "" {
		t.Fatalf("unexpected error event: %s", gotError)
	}
	if toolUse == nil || toolUse.Name != "dev_glob" || toolUse.ID != "toolu_bad" {
		t.Fatalf("toolUse = %+v", toolUse)
	}
	if got := toolUse.Input["_raw"]; got != `{"pattern":` {
		t.Fatalf("toolUse.Input = %#v, want _raw malformed JSON", toolUse.Input)
	}
	if inputTokens != 4 || outputTokens != 5 {
		t.Fatalf("usage input/output = %d/%d, want 4/5", inputTokens, outputTokens)
	}
	if stopReason != "tool_use" {
		t.Fatalf("usage stopReason = %q, want tool_use", stopReason)
	}
	if !gotDone {
		t.Fatal("missing done event")
	}
}

func newStreamingTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	c := New()
	c.SetAPIKey("test-key")
	c.httpClient = srv.Client()
	c.sdk = sdk.NewClient(
		option.WithHTTPClient(c.httpClient),
		option.WithMaxRetries(0),
		option.WithAPIKey(c.apiKey),
		option.WithBaseURL(srv.URL),
		option.WithMiddleware(rateAwareMiddleware(c.RateTracker, c.CircuitBreaker, &c.calibrated)),
	)
	return c
}
