package anthropic

import (
	"context"
	"errors"
	"strings"
	"testing"

	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	llmtypes "github.com/hollis-labs/go-llm-types"
)

func TestClient_ImplementsContracts(t *testing.T) {
	// Compile-time assertions are in client.go; these explicitly type-check
	// at runtime so failures surface as a test rather than a build error.
	var c *Client = New()
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
		{"claude-sonnet-4-20250101", false},  // pre-min date
		{"claude-opus-4-0", false},           // no date
		{"claude-opus-3-5-20250514", false},  // wrong major
		{"claude-opus-40-20250514", false},   // false-prefix
		{"claude-sonnet-4-5", false},         // no date suffix
		{"gpt-4-turbo-20240414", false},      // wrong family
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
