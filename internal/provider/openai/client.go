// Package openai adapts the OpenAI Chat Completions and Responses APIs.
package openai

import (
	"context"
	"net/http"
	"os"
	"strings"
	"sync"

	llmcontracts "github.com/hollis-labs/libs/llm/core/llmcontracts"
	llmtypes "github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/provider/station"
	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// Client wraps openai-go and implements llmcontracts.Provider for the
// chat-completion path used by nanite's chat surface.
type Client struct {
	// keyMu guards sdk, apiKey, keyFn and keyLoaded: a lazily resolved key
	// (SetKeyFunc) changes them at first use rather than at construction.
	keyMu     sync.Mutex
	keyFn     KeyFunc
	keyLoaded bool
	sdk       sdk.Client
	apiKey    string
	// gw is set for a Station gateway provider (SetGateway). It is part of
	// every SDK rebuild so a key set never drops the base URL or headers.
	gw *station.Settings
}

// Compile-time interface assertion. The Client also exposes Embed* methods
// and EmbeddingDimensions but those satisfy embedcontracts.Embedder via the
// embedding endpoint.
var _ llmcontracts.Provider = (*Client)(nil)

// New constructs a Client. apiKey defaults to OPENAI_API_KEY when empty.
// httpClient may be nil to use the SDK default; tests can pass a custom
// transport to capture / mock requests. Additional opts are appended after
// the API-key + http-client + retry options so callers (notably tests) can
// override the base URL.
//
// Nanite owns retries, so the SDK uses zero automatic retries.
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
	c.keyMu.Lock()
	defer c.keyMu.Unlock()
	c.keyFn, c.keyLoaded = nil, false
	c.apiKey = key
	c.sdk = c.newSDK(key)
}

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
// message text of the first choice. Tool calls are not surfaced through
// Complete — use StreamChat for tool-using turns. No choices, or a choice
// with no text or only whitespace, returns errEmptyResponse.
func (c *Client) Complete(ctx context.Context, req llmtypes.ChatRequest) (string, error) {
	if useResponses(ctx, req) {
		return c.completeResponse(ctx, req)
	}
	params, err := buildChatParams(req)
	if err != nil {
		return "", err
	}
	api, err := c.client(ctx)
	if err != nil {
		return "", err
	}
	resp, err := api.Chat.Completions.New(ctx, params, c.requestOptions(ctx)...)
	if err != nil {
		return "", translateError(err)
	}
	if len(resp.Choices) == 0 || strings.TrimSpace(resp.Choices[0].Message.Content) == "" {
		return "", errEmptyResponse
	}
	return resp.Choices[0].Message.Content, nil
}
