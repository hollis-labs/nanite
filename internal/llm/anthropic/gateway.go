package anthropic

import (
	"context"

	"github.com/anthropics/anthropic-sdk-go/option"
	llmcontracts "github.com/hollis-labs/libs/llm/core/llmcontracts"
	"github.com/hollis-labs/nanite/internal/llm/station"
)

// SetGateway points the client at a Station gateway: its base URL (the gateway
// root, without /v1) and static headers apply to every request, each request
// carries the session id from its context as X-Station-Session, and the
// settings' flags turn off the anthropic-beta header and cache_control markers.
// The settings survive SetAPIKey and SetKeyFunc, which rebuild the SDK client.
//
// The gateway owns budget and routing, so the client's own token-rate tracker
// is dropped: its seeded 50000 TPM default would otherwise refuse or pace
// requests against a limit nothing calibrates (a gateway sends no
// anthropic-ratelimit-* or x-ratelimit-* headers). RateLimitTPM reports 0,
// unknown. The circuit breaker stays.
func (c *Client) SetGateway(g station.Settings) {
	c.keyMu.Lock()
	defer c.keyMu.Unlock()
	c.gw = &g
	c.RateTracker = nil
	c.rebuildSDK()
}

// interleavedThinking gates the interleaved-thinking beta and its request
// parameter as a pair; both are off when the gateway takes no beta header.
func (c *Client) interleavedThinking(cfg llmcontracts.ReasoningConfig, model string) bool {
	if c.gw != nil && c.gw.DisableAnthropicBeta {
		return false
	}
	return shouldEnableInterleavedThinking(cfg, model)
}

// requestOptions are the per-call options: the anthropic-beta header (unless
// the gateway provider disables it) and the Station session id.
func (c *Client) requestOptions(ctx context.Context, interleavedThinking bool) []option.RequestOption {
	c.keyMu.Lock()
	gw := c.gw
	c.keyMu.Unlock()
	if gw == nil {
		return []option.RequestOption{betaHeaderRequestOption(interleavedThinking)}
	}
	var opts []option.RequestOption
	if !gw.DisableAnthropicBeta {
		opts = append(opts, betaHeaderRequestOption(interleavedThinking))
	}
	if id, ok := station.SessionID(ctx); ok {
		opts = append(opts, option.WithHeader(station.SessionHeader, id))
	}
	return opts
}
