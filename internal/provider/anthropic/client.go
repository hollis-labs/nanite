// Package anthropic adapts the Anthropic Messages API to the native provider
// contract, including streaming, prompt caching and rate-budget accounting.
package anthropic

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/hollis-labs/libs/core/panics"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	llmcontracts "github.com/hollis-labs/libs/llm/core/llmcontracts"
	llmtypes "github.com/hollis-labs/libs/llm/core/llmtypes"

	"github.com/hollis-labs/nanite/internal/provider/station"
)

// DefaultMaxTokens is the max_tokens used when ChatRequest.MaxTokens is unset
// and the model's real output limit is unknown, and the ceiling for an unset
// value on the non-streaming Complete path.
const DefaultMaxTokens = 16384 // fallback when the model's output limit is unknown

// DefaultRateLimitTPM seeds the TokenRateTracker before the first calibrated
// response. Picked to match the deleted hand-rolled adapter so pre-launch
// behavior under no-key/no-response conditions is unchanged.
const DefaultRateLimitTPM = 50000

// DefaultBreakerThreshold trips the circuit after this many consecutive
// failures. Mirrors the value used by the deleted adapter.
const DefaultBreakerThreshold = 3

// ErrModelRequired indicates that a request omitted its selected model.
var ErrModelRequired = errors.New("anthropic: ChatRequest.Model is required (set runtime.model)")

// InterleavedThinkingBetaHeader is the beta header value that enables
// interleaved thinking (thinking_delta blocks). Supported on Claude 4.x
// models released on or after 20250514 and on every later major version;
// see modelSupportsInterleavedThinking.
const InterleavedThinkingBetaHeader = "interleaved-thinking-2025-05-14"

// Client wraps the Messages API with rate tracking and a circuit breaker.
type Client struct {
	// panics contains a panic in the stream goroutine (SetPanicGuard).
	panics *panics.Guard
	// keyMu guards apiKey, sdk, keyFn and keyLoaded, which a lazily resolved
	// key (SetKeyFunc) changes at first use rather than at construction.
	keyMu          sync.Mutex
	keyFn          KeyFunc
	keyLoaded      bool
	apiKey         string
	sdk            sdk.Client
	httpClient     *http.Client
	RateTracker    *llmcontracts.TokenRateTracker
	CircuitBreaker *llmcontracts.CircuitBreaker //nolint:staticcheck // Retain the adapter’s existing streaming breaker semantics.
	OnStatus       StatusCallback               // optional; called during pacing waits
	OnCircuitOpen  func()                       // called when the circuit breaker trips
	// cacheHints holds hints set via SetCacheHints. Deprecated: prefer
	// llmtypes.ChatRequest.CacheHints on each call. Retained as a fallback
	// for callers that have not migrated; effectiveCacheHints reads
	// req.CacheHints first and only falls back to this shared field when
	// the per-call slot is empty. See FU-13 / CW-20260520-0054.
	cacheHints []llmcontracts.CacheHint
	// calibrated reports whether at least one provider response has supplied
	// an x-ratelimit-limit-input-tokens header. RateLimitTPM consults this
	// to honor the RateLimited contract: return 0 when the limit is unknown
	// (i.e. before the first calibrated response), not the seeded default.
	calibrated atomic.Bool
	// gw is set for a Station gateway provider (SetGateway); rebuildSDK
	// applies it so a key set never drops the base URL or headers.
	gw *station.Settings
}

// StatusCallback is the optional pacing-wait progress reporter shape.
// Mirrors the deleted adapter's contract so consumers wiring an OnStatus
// hook can migrate without behavior change.
type StatusCallback = func(string)

var (
	_ llmcontracts.Provider          = (*Client)(nil)
	_ llmcontracts.RateLimited       = (*Client)(nil)
	_ llmcontracts.CacheableProvider = (*Client)(nil) //nolint:staticcheck // Compatibility fallback is covered by the adapter cache tests.
	_ llmcontracts.Cacheable         = (*Client)(nil)
)

// New constructs an adapter. API keys may be configured eagerly or lazily.
// Nanite owns the retry policy for streamed turns.
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
	c.keyMu.Lock()
	defer c.keyMu.Unlock()
	c.keyFn, c.keyLoaded = nil, false
	c.apiKey = key
	c.rebuildSDK()
}

func (c *Client) rebuildSDK() {
	opts := []option.RequestOption{
		option.WithHTTPClient(c.httpClient),
		option.WithMaxRetries(0),
		option.WithMiddleware(rateAwareMiddleware(c.RateTracker, c.CircuitBreaker, &c.calibrated)),
	}
	if c.apiKey != "" {
		if c.gw != nil && c.gw.Auth == station.AuthBearer {
			opts = append(opts, option.WithAuthToken(c.apiKey))
		} else {
			opts = append(opts, option.WithAPIKey(c.apiKey))
		}
	}
	if c.gw != nil {
		opts = append(opts, option.WithBaseURL(c.gw.BaseURL))
		for name, v := range c.gw.Headers {
			opts = append(opts, option.WithHeader(name, v))
		}
	}
	c.sdk = sdk.NewClient(opts...)
}

// SetCacheHints implements llmcontracts.CacheableProvider. Stores hints on
// the shared singleton.
//
// Deprecated: this is the legacy path that races under concurrent callers
// — one caller's hints can be overwritten by another caller between the
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
// concurrent calls on the same Client cannot leak hints across calls.
// When both are empty the result is nil and the cache pipeline is no-op.
func (c *Client) effectiveCacheHints(req llmtypes.ChatRequest) []llmcontracts.CacheHint {
	if c.gw != nil && c.gw.DisableCacheControl {
		return nil // a gateway that does not take cache_control gets no markers
	}
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

// SetPanicGuard sets the guard that contains a panic in a stream goroutine.
// Nil withholds panic values and reports to standard error.
func (c *Client) SetPanicGuard(g *panics.Guard) { c.panics = g }

// Capabilities reports supported message and tool features. MaxTokens and
// ContextWindowSize are a conservative floor shared by every Claude model;
// the engine sizes requests from the agent's runtime.max_output_tokens and
// runtime.context_window, not from these values.
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
// supports the interleaved-thinking-2025-05-14 beta. Claude model IDs have
// the shape claude-<family>-<major>[-<minor>][-<YYYYMMDD>]:
//
//   - major 5 and later: supported, dated or not (claude-sonnet-5-5,
//     claude-opus-5-20261001).
//   - major 4: supported. A dated ID must be on or after 20250514, the
//     beta's release; an undated alias (claude-sonnet-4-5) names a model
//     from that release or later.
//   - anything else, including Claude 3.x and non-Claude IDs: not supported.
func modelSupportsInterleavedThinking(model string) bool {
	parts := strings.Split(strings.ToLower(model), "-")
	if len(parts) < 3 || len(parts) > 5 || parts[0] != "claude" || !allLetters(parts[1]) {
		return false
	}
	date := ""
	if last := parts[len(parts)-1]; len(last) == 8 && allDigits(last) {
		date, parts = last, parts[:len(parts)-1]
	}
	if len(parts) < 3 || len(parts) > 4 {
		return false
	}
	for _, version := range parts[2:] {
		if len(version) > 2 || !allDigits(version) {
			return false
		}
	}
	major, err := strconv.Atoi(parts[2])
	if err != nil {
		return false
	}
	switch {
	case major >= 5:
		return true
	case major == 4:
		// Fixed 8-digit width makes the string compare a date compare.
		return date == "" || date >= "20250514"
	}
	return false
}

// allLetters reports whether s is a non-empty run of ASCII letters.
func allLetters(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
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
// removed the in-package default fallback — callers must name a model
// before invoking StreamChat / Complete.
// An empty Model now produces ErrModelRequired at the SDK boundary.
func resolveModel(req llmtypes.ChatRequest) (string, error) {
	if req.Model == "" {
		return "", ErrModelRequired
	}
	return req.Model, nil
}

// resolveMaxTokens uses the request limit when set and the adapter default otherwise.
func resolveMaxTokens(req llmtypes.ChatRequest) int64 {
	if req.MaxTokens > 0 {
		return int64(req.MaxTokens)
	}
	return DefaultMaxTokens
}

// nonStreamingRequest bounds an unset MaxTokens for the non-streaming path.
func nonStreamingRequest(req llmtypes.ChatRequest) llmtypes.ChatRequest {
	if req.MaxTokens <= 0 {
		req.MaxTokens = int(min(resolveMaxTokens(req), DefaultMaxTokens))
	}
	return req
}
