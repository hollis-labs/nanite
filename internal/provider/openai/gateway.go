package openai

import (
	"context"
	"os"
	"strings"

	"github.com/hollis-labs/nanite/internal/provider/station"
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
	if c.httpClient != nil {
		opts = append(opts, option.WithHTTPClient(c.httpClient))
	}
	if c.gw != nil {
		opts = append(opts, option.WithBaseURL(c.gw.BaseURL))
		opts = append(opts, withoutAmbientIdentity()...)
		for name, v := range c.gw.Headers {
			opts = append(opts, option.WithHeader(name, v))
		}
		if c.gw.Bearer != nil {
			opts = append(opts, option.WithMiddleware(station.BearerMiddleware(c.gw.Bearer)))
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
	var opts []option.RequestOption
	for name, value := range station.ConfiguredHeaders(ctx, gw.Headers, nil) {
		opts = append(opts, option.WithHeader(name, value))
	}
	if id, ok := station.SessionID(ctx); ok {
		opts = append(opts, option.WithHeader(station.SessionHeader, id))
	}
	return opts
}

// withoutAmbientIdentity drops what the SDK's environment defaults add
// besides the key, which Nanite sets itself: the organization and project
// headers, the admin key and OPENAI_CUSTOM_HEADERS. A gateway or other
// endpoint must not receive an account's identity from the environment.
func withoutAmbientIdentity() []option.RequestOption {
	opts := []option.RequestOption{
		option.WithAdminAPIKey(""),
		option.WithHeaderDel("OpenAI-Organization"),
		option.WithHeaderDel("OpenAI-Project"),
	}
	for _, line := range strings.Split(os.Getenv("OPENAI_CUSTOM_HEADERS"), "\n") {
		name, _, ok := strings.Cut(line, ":")
		name = strings.TrimSpace(name)
		// Authorization carries the key Nanite set; deleting it would strip
		// the real credential.
		if ok && name != "" && !strings.EqualFold(name, "Authorization") {
			opts = append(opts, option.WithHeaderDel(name))
		}
	}
	return opts
}
