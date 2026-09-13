// Package openai is nanite's SDK-backed wrapper around openai-go.
//
// This package replaces the deleted hand-rolled provider/openai.go HTTP client
// from go-providers v0.10. It exposes both the chat-completion path
// (llmcontracts.Provider — StreamChat / Complete / Capabilities) and an
// embedding path (embedcontracts.Embedder).
//
// Per CW-20260508-0009 (rate-tracker spike) the OpenAI wrapper does NOT
// implement llmcontracts.RateLimited and does NOT carry a TokenRateTracker /
// CircuitBreaker. The replaced provider had no rate-budget plumbing and
// release-scope parity with Anthropic was deferred to follow-up
// `followups.nanite.cw_20260508_0012.openai_rate_budget_parity`.
package openai

import (
	"context"
	"net/http"
	"os"

	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	llmtypes "github.com/hollis-labs/go-llm-types"
	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// Client wraps openai-go and implements llmcontracts.Provider for the
// chat-completion path used by nanite's chat surface.
type Client struct {
	sdk    sdk.Client
	apiKey string
}

// Compile-time interface assertion. The Client also exposes Embed* methods
// and EmbeddingDimensions but those satisfy embedcontracts.Embedder via the
// dedicated Embedder type below — keeping the chat client's surface narrow.
var _ llmcontracts.Provider = (*Client)(nil)

// New constructs a Client. apiKey defaults to OPENAI_API_KEY when empty.
// httpClient may be nil to use the SDK default; tests can pass a custom
// transport to capture / mock requests. Additional opts are appended after
// the API-key + http-client + retry options so callers (notably tests) can
// override the base URL.
//
// SDK retry is disabled (option.WithMaxRetries(0)) to match nanite's
// existing OpenAI behavior and avoid double-retry pathology with the
// chat-rate-budget loop in chat_rate_budget_pause.go.
func New(apiKey string, httpClient *http.Client, opts ...option.RequestOption) *Client {
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}
	base := []option.RequestOption{
		option.WithAPIKey(apiKey),
		option.WithMaxRetries(0),
	}
	if httpClient != nil {
		base = append(base, option.WithHTTPClient(httpClient))
	}
	base = append(base, opts...)
	return &Client{sdk: sdk.NewClient(base...), apiKey: apiKey}
}

// SetAPIKey rebuilds the underlying SDK client with the new API key. Kept
// for parity with the registration shape in cmd/nanite/main.go which
// constructs the provider, then sets the key once it has been resolved.
func (c *Client) SetAPIKey(key string) {
	c.apiKey = key
	c.sdk = sdk.NewClient(
		option.WithAPIKey(key),
		option.WithMaxRetries(0),
	)
}

// Capabilities reports OpenAI's supported feature set for nanite's chat
// pipeline. Numbers are conservative defaults; the per-model registry in
// pkg/models/registry.go is authoritative for context-window sizing.
func (c *Client) Capabilities() llmtypes.ProviderCapabilities {
	return llmtypes.ProviderCapabilities{
		SupportsStreamJSON:    true,
		SupportsToolCalling:   true,
		SupportsImageInput:    true,
		SupportsEmbedding:     true,
		DefaultEmbeddingModel: "text-embedding-3-small",
	}
}

// Complete runs a non-streaming chat completion. Returns the assistant
// message text concatenated from the first choice. Tool calls are not
// surfaced through Complete — use StreamChat for tool-using turns.
func (c *Client) Complete(ctx context.Context, req llmtypes.ChatRequest) (string, error) {
	if useResponses(ctx, req) {
		return c.completeResponse(ctx, req)
	}
	params, err := buildChatParams(req)
	if err != nil {
		return "", err
	}
	resp, err := c.sdk.Chat.Completions.New(ctx, params)
	if err != nil {
		return "", translateError(err)
	}
	if len(resp.Choices) == 0 {
		return "", errEmptyResponse
	}
	return resp.Choices[0].Message.Content, nil
}
