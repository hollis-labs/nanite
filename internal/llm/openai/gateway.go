package openai

import (
	"context"

	"github.com/hollis-labs/nanite/internal/llm/station"
	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// SetGateway points the client at a Station gateway: its base URL and static
// headers apply to every request, and each request carries the session id from
// its context as X-Station-Session. The settings survive SetAPIKey and
// SetKeyFunc, which rebuild the SDK client.
func (c *Client) SetGateway(g station.Settings) {
	c.keyMu.Lock()
	defer c.keyMu.Unlock()
	c.gw = &g
	c.sdk = c.newSDK(c.apiKey)
}

// newSDK builds the SDK client for key with the gateway settings, if any. The
// caller holds keyMu (or owns the Client during construction).
func (c *Client) newSDK(key string) sdk.Client {
	opts := []option.RequestOption{option.WithAPIKey(key), option.WithMaxRetries(0)}
	if c.gw != nil {
		opts = append(opts, option.WithBaseURL(c.gw.BaseURL))
		for name, v := range c.gw.Headers {
			opts = append(opts, option.WithHeader(name, v))
		}
	}
	return sdk.NewClient(opts...)
}

// requestOptions are the per-request options: the Station session id. One SDK
// client serves every session, so the header cannot be a client option. The
// same id is sent on every turn and retry of a session because it comes from
// the context, not from a counter.
func (c *Client) requestOptions(ctx context.Context) []option.RequestOption {
	c.keyMu.Lock()
	gw := c.gw
	c.keyMu.Unlock()
	if gw == nil {
		return nil
	}
	if id, ok := station.SessionID(ctx); ok {
		return []option.RequestOption{option.WithHeader(station.SessionHeader, id)}
	}
	return nil
}
