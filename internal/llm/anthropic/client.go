// Package anthropic is nanite's wrapper around the official anthropic-sdk-go
// for chat completions. It satisfies llmcontracts.Provider (StreamChat,
// Complete, Capabilities), llmcontracts.RateLimited (RateLimitTPM),
// llmcontracts.CacheableProvider (SetCacheHints, deprecated), and
// llmcontracts.Cacheable (EstimateCacheablePrefix), preserving the public
// surface the deleted go-providers/provider/anthropic.go exposed to nanite
// call-sites.
//
// The wrapper relocates header parsing from a hand-rolled HTTP middleware
// into option.WithMiddleware (SDK-blessed), drives the existing
// llmcontracts.TokenRateTracker + CircuitBreaker (the "three load-bearing
// seams" identified in CW-20260508-0009), and translates 429 →
// ErrRequestExceedsRateBudget for the existing pre-flight gate.
//
// Cache-hint sourcing (FU-13 / CW-20260520-0054): hints are read per call
// from llmtypes.ChatRequest.CacheHints. The deprecated SetCacheHints setter
// stays as a fallback for unmigrated callers; effectiveCacheHints prefers
// req.CacheHints when populated, so concurrent sessions on the same Client
// no longer race on the shared c.cacheHints field.
package anthropic

import (
	"errors"
	"net/http"
	"strings"
	"sync/atomic"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	llmtypes "github.com/hollis-labs/go-llm-types"
)

// DefaultMaxTokens is used when ChatRequest.MaxTokens is unset. Mirrors the
// previous hand-rolled adapter so the streaming and non-streaming paths agree
// on what "no cap" means.
const DefaultMaxTokens = 16384

// DefaultRateLimitTPM seeds the TokenRateTracker before the first calibrated
// response. Picked to match the deleted hand-rolled adapter so pre-launch
// behavior under no-key/no-response conditions is unchanged.
const DefaultRateLimitTPM = 50000

// DefaultBreakerThreshold trips the circuit after this many consecutive
// failures. Mirrors the value used by the deleted adapter.
const DefaultBreakerThreshold = 3

// ErrModelRequired is returned by StreamChat / Complete when the caller
// supplies an empty ChatRequest.Model. CW-20260526-0003 removed the
// in-package default fallback — every caller must resolve a model via
// store.ResolveProviderAndModel before invoking the SDK. A silent
// default here was load-bearing in the original bug (a stale bare alias
// hit Anthropic and returned 404).
var ErrModelRequired = errors.New("anthropic: ChatRequest.Model is required (resolve via store.ResolveProviderAndModel)")

// InterleavedThinkingBetaHeader is the beta header value that enables
// interleaved thinking (thinking_delta blocks). Supported on Claude
// Opus/Sonnet/Haiku 4.x models with a release-date suffix on or after
// 20250514. Mirrors the constant the deleted adapter exported.
const InterleavedThinkingBetaHeader = "interleaved-thinking-2025-05-14"

// Client wraps anthropic-sdk-go and exposes the rate-budget primitives nanite
// chat-service expects (RateTracker + CircuitBreaker fields, RateLimitTPM
// method, ErrRequestExceedsRateBudget pre-flight). Concrete-type field
// access matches the shape of the deleted *provider.Anthropic so consumers
// like chat_generate.go and chat_rate_budget_pause.go can migrate via type
// rename only.
type Client struct {
	apiKey         string
	sdk            sdk.Client
	httpClient     *http.Client
	RateTracker    *llmcontracts.TokenRateTracker
	CircuitBreaker *llmcontracts.CircuitBreaker
	OnStatus       StatusCallback // optional; called during pacing waits
	OnCircuitOpen  func()         // called when the circuit breaker trips
	// cacheHints holds hints set via SetCacheHints. Deprecated: prefer
	// llmtypes.ChatRequest.CacheHints on each call. Retained as a fallback
	// for callers that have not migrated; effectiveCacheHints reads
	// req.CacheHints first and only falls back to this shared field when
	// the per-call slot is empty. See FU-13 / CW-20260520-0054.
	cacheHints     []llmcontracts.CacheHint
	// calibrated reports whether at least one provider response has supplied
	// an x-ratelimit-limit-input-tokens header. RateLimitTPM consults this
	// to honor the RateLimited contract: return 0 when the limit is unknown
	// (i.e. before the first calibrated response), not the seeded default.
	calibrated atomic.Bool
}

// StatusCallback is the optional pacing-wait progress reporter shape.
// Mirrors the deleted adapter's contract so consumers wiring an OnStatus
// hook can migrate without behavior change.
type StatusCallback = func(string)

// Compile-time assertions that the wrapper implements the contracts the
// chat-service depends on.
var (
	_ llmcontracts.Provider          = (*Client)(nil)
	_ llmcontracts.RateLimited       = (*Client)(nil)
	_ llmcontracts.CacheableProvider = (*Client)(nil)
	_ llmcontracts.Cacheable         = (*Client)(nil)
)

// New constructs a wrapper Client. apiKey may be empty here; SetAPIKey is
// called separately during composition (mirrors the deleted adapter's
// keychain-deferred wiring in cmd/nanite/main.go).
//
// The SDK client is built with option.WithMaxRetries(0) — nanite owns its
// own compaction-tied retry loop in chat_rate_budget_pause.go, and SDK-side
// retry would double-fire. The rate-aware middleware feeds the
// TokenRateTracker / CircuitBreaker as responses flow through.
func New() *Client {
	httpClient := &http.Client{}
	rt := llmcontracts.NewTokenRateTracker(DefaultRateLimitTPM)
	cb := llmcontracts.NewCircuitBreaker(DefaultBreakerThreshold)

	c := &Client{
		httpClient:     httpClient,
		RateTracker:    rt,
		CircuitBreaker: cb,
	}
	c.rebuildSDK()
	return c
}

// SetAPIKey updates the API key and rebuilds the underlying SDK client so
// the new key is in effect for subsequent requests. Composition root calls
// this after pulling the key from the OS keychain.
func (c *Client) SetAPIKey(key string) {
	c.apiKey = key
	c.rebuildSDK()
}

// rebuildSDK constructs (or re-constructs) the SDK client with the current
// apiKey + middleware. Called from New and SetAPIKey.
func (c *Client) rebuildSDK() {
	opts := []option.RequestOption{
		option.WithHTTPClient(c.httpClient),
		option.WithMaxRetries(0),
		option.WithMiddleware(rateAwareMiddleware(c.RateTracker, c.CircuitBreaker, &c.calibrated)),
	}
	if c.apiKey != "" {
		opts = append(opts, option.WithAPIKey(c.apiKey))
	}
	c.sdk = sdk.NewClient(opts...)
}

// SetCacheHints implements llmcontracts.CacheableProvider. Stores hints on
// the shared singleton.
//
// Deprecated: this is the legacy path that races under concurrent callers
// — session A's hints can be overwritten by session B between the
// SetCacheHints call and the request build, producing requests without
// cache_control markers and the cache-miss-echo signature
// (cache_read=0 with input_tokens<10) tracked under FU-13. New code MUST
// populate llmtypes.ChatRequest.CacheHints on each ChatRequest instead;
// effectiveCacheHints prefers req.CacheHints over this shared field.
// Retained on the Client to keep the llmcontracts.CacheableProvider
// interface implementable and to leave legacy call-sites compiling until
// they migrate.
func (c *Client) SetCacheHints(hints []llmcontracts.CacheHint) {
	c.cacheHints = hints
}

// effectiveCacheHints returns the cache hints that govern a single call.
// Prefers req.CacheHints (per-call, race-free) over c.cacheHints (shared
// singleton populated by the deprecated SetCacheHints). When a caller
// populates the per-call slot, the shared field is ignored entirely so
// concurrent sessions on the same Client cannot leak hints across calls.
// When both are empty the result is nil and the cache pipeline is no-op.
func (c *Client) effectiveCacheHints(req llmtypes.ChatRequest) []llmcontracts.CacheHint {
	if len(req.CacheHints) > 0 {
		return req.CacheHints
	}
	return c.cacheHints
}

// hasCacheHintIn reports whether hints include one matching the given
// position. Pure helper so both the per-call (effectiveCacheHints) and
// legacy-fallback (c.cacheHints via hasCacheHint) paths share one
// implementation.
func hasCacheHintIn(hints []llmcontracts.CacheHint, position string) bool {
	for _, h := range hints {
		if h.Position == position {
			return true
		}
	}
	return false
}

// recentMessageCacheCountIn returns the number of "recent_message" hints
// in hints, which controls how many trailing user messages get
// cache_control markers.
func recentMessageCacheCountIn(hints []llmcontracts.CacheHint) int {
	count := 0
	for _, h := range hints {
		if h.Position == "recent_message" {
			count++
		}
	}
	return count
}

// hasCacheHint is the backward-compat wrapper used by the legacy
// SetCacheHints path. Reads c.cacheHints directly; new code reads hints
// per call via hasCacheHintIn(effectiveCacheHints(req), position).
func (c *Client) hasCacheHint(position string) bool {
	return hasCacheHintIn(c.cacheHints, position)
}

// recentMessageCacheCount is the backward-compat wrapper used by the
// legacy SetCacheHints path. Reads c.cacheHints directly; new code reads
// hints per call via recentMessageCacheCountIn(effectiveCacheHints(req)).
func (c *Client) recentMessageCacheCount() int {
	return recentMessageCacheCountIn(c.cacheHints)
}

// RateLimitTPM implements llmcontracts.RateLimited. Returns 0 before the
// first calibrated response so callers can treat it as "unknown" rather
// than mislabel the seeded default as observed truth.
func (c *Client) RateLimitTPM() int {
	if c.RateTracker == nil {
		return 0
	}
	if !c.calibrated.Load() {
		return 0
	}
	_, limit := c.RateTracker.Remaining()
	return limit
}

// Capabilities implements llmcontracts.Provider. Mirrors the capability set
// the deleted hand-rolled adapter advertised so chat-service feature gates
// (system-prompt caching, tool use, image input) light up as before.
func (c *Client) Capabilities() llmtypes.ProviderCapabilities {
	return llmtypes.ProviderCapabilities{
		SupportsStreamJSON:          true,   // streaming with tool use
		SupportsPreToolHooks:        false,  // no direct pre-tool hook support
		SupportsPostToolHooks:       false,  // no direct post-tool hook support
		SupportsSystemPromptCaching: true,   // prompt caching via cache_control
		SupportsToolCalling:         true,   // function calling
		SupportsBatch:               false,  // no batch API support
		SupportsImageInput:          true,   // image inputs supported
		MaxTokens:                   16384,  // default output cap
		ContextWindowSize:           200000, // 200k context window
	}
}

// modelSupportsInterleavedThinking reports whether the given model ID
// supports the interleaved-thinking-2025-05-14 beta. Accepts the canonical
// Anthropic naming pattern claude-{opus|sonnet|haiku}-4[-<minor>]-<YYYYMMDD>
// with the trailing date on or after minInterleavedThinkingModelDate.
//
// Mirrors the helper from the deleted adapter; release-date-bare model
// aliases (e.g. claude-sonnet-4-5) are not gated here — callers using a
// dated model string get the precise check.
func modelSupportsInterleavedThinking(model string) bool {
	parts := strings.Split(strings.ToLower(model), "-")
	if len(parts) != 4 && len(parts) != 5 {
		return false
	}
	if parts[0] != "claude" {
		return false
	}
	switch parts[1] {
	case "opus", "sonnet", "haiku":
	default:
		return false
	}
	if parts[2] != "4" {
		return false
	}
	dateStr := parts[len(parts)-1]
	if !allDigits(dateStr) || len(dateStr) != 8 {
		return false
	}
	// Compare lexicographically — fixed 8-digit width makes string compare
	// semantically equivalent to numeric compare for valid YYYYMMDD. Min
	// release date is 2025-05-14 (interleaved-thinking-2025-05-14 GA).
	return dateStr >= "20250514"
}

// allDigits reports whether s consists entirely of decimal digits.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// shouldEnableInterleavedThinking gates the interleaved-thinking beta
// header + thinking_config request param as a pair (the SDK ignores the
// header without the config and vice versa).
func shouldEnableInterleavedThinking(cfg llmcontracts.ReasoningConfig, model string) bool {
	return cfg.Enabled &&
		cfg.BudgetTokens > 0 &&
		cfg.BetasHeader == InterleavedThinkingBetaHeader &&
		modelSupportsInterleavedThinking(model)
}

// resolveModel returns the model to use for a request. CW-20260526-0003
// removed the in-package default fallback — callers must resolve a model
// via store.ResolveProviderAndModel before invoking StreamChat / Complete.
// An empty Model now produces ErrModelRequired at the SDK boundary.
func resolveModel(req llmtypes.ChatRequest) (string, error) {
	if req.Model == "" {
		return "", ErrModelRequired
	}
	return req.Model, nil
}

// resolveMaxTokens returns the max_tokens to use for a request, falling
// back to DefaultMaxTokens when the request omits one.
func resolveMaxTokens(req llmtypes.ChatRequest) int64 {
	if req.MaxTokens > 0 {
		return int64(req.MaxTokens)
	}
	return DefaultMaxTokens
}

