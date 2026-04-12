package provider

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	feotel "github.com/hollis-labs/go-otel"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/shared"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

const (
	openzenDefaultBaseURL   = "https://api.open-zen.com/v1/"
	openzenDefaultChatModel = "claude-sonnet-4-20250514"
)

// openzenAPI is retained for test compatibility. It is the chat-completions
// URL under the default base; tests compare against this literal.
const openzenAPI = openzenDefaultBaseURL + "chat/completions"

// OpenZen implements the Provider interface for the OpenZen API, an
// OpenAI-compatible inference gateway. Transport is the official openai-go
// SDK configured with OpenZen's base URL (or an env override).
type OpenZen struct {
	apiKey         string
	baseURL        string // the chat-completions URL (for test parity) or a base URL
	sdkBaseURL     string // base URL handed to option.WithBaseURL
	httpClient     *http.Client
	client         *openai.Client
	Retry          RetryConfig
	OnStatus       StatusCallback
	CircuitBreaker *CircuitBreaker
	OnCircuitOpen  func()
	RateTracker    *TokenRateTracker
}

// NewOpenZen creates a new OpenZen provider. Optionally reads OPENZEN_BASE_URL
// to override the default endpoint. The override may be either the full
// chat-completions URL (legacy shape, e.g. ".../v1/chat/completions") or a
// base URL ending with /v1/ — both are normalised.
func NewOpenZen() *OpenZen {
	base := os.Getenv("OPENZEN_BASE_URL")
	if base == "" {
		base = openzenAPI
	}
	return &OpenZen{
		baseURL:        strings.TrimRight(base, "/"),
		sdkBaseURL:     deriveOpenZenBaseURL(base),
		httpClient:     &http.Client{},
		Retry:          DefaultRetryConfig(),
		CircuitBreaker: NewCircuitBreaker(3),
		RateTracker:    NewTokenRateTracker(30000),
	}
}

// deriveOpenZenBaseURL normalises the configured URL into a form suitable for
// option.WithBaseURL. If the caller supplied a full chat-completions URL we
// strip that suffix; otherwise we ensure the URL ends with a trailing slash.
func deriveOpenZenBaseURL(raw string) string {
	trimmed := strings.TrimRight(raw, "/")
	if strings.HasSuffix(trimmed, "/chat/completions") {
		trimmed = strings.TrimSuffix(trimmed, "/chat/completions")
	}
	return trimmed + "/"
}

func (oz *OpenZen) ensureClient() {
	if oz.client != nil || oz.apiKey == "" {
		return
	}
	c := openai.NewClient(
		option.WithAPIKey(oz.apiKey),
		option.WithBaseURL(oz.sdkBaseURL),
		option.WithHTTPClient(oz.httpClient),
		option.WithMaxRetries(0),
	)
	oz.client = &c
}

// StreamChat implements Provider.StreamChat.
func (oz *OpenZen) StreamChat(ctx context.Context, systemPrompt string, messages []ChatMessage, model string) (<-chan StreamEvent, error) {
	return oz.streamChatInternal(ctx, systemPrompt, messages, model, nil)
}

// StreamChatWithTools delegates through the same SDK path.
func (oz *OpenZen) StreamChatWithTools(ctx context.Context, systemPrompt string, messages []ChatMessage, model string, tools []ToolDefinition) (<-chan StreamEvent, error) {
	return oz.streamChatInternal(ctx, systemPrompt, messages, model, tools)
}

func (oz *OpenZen) streamChatInternal(ctx context.Context, systemPrompt string, messages []ChatMessage, model string, tools []ToolDefinition) (<-chan StreamEvent, error) {
	ctx, span := feotel.StartSpan(ctx, "nanite.provider.openzen.stream")
	span.SetAttributes(
		attribute.String("nanite.provider", "openzen"),
		attribute.String("nanite.model", model),
		attribute.Int("nanite.messages.count", len(messages)),
		attribute.Int("nanite.tools.count", len(tools)),
	)

	if oz.apiKey == "" {
		span.SetStatus(codes.Error, "OPENZEN_API_KEY not set")
		span.End()
		return nil, fmt.Errorf("OPENZEN_API_KEY not set")
	}
	oz.ensureClient()

	if model == "" {
		model = openzenDefaultChatModel
	}

	if oz.CircuitBreaker != nil && oz.CircuitBreaker.IsOpen() {
		span.SetStatus(codes.Error, "circuit breaker open")
		span.End()
		return nil, fmt.Errorf("circuit breaker open: provider rate limited after multiple retries")
	}

	params := openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(model),
		Messages: buildCompatMessages(systemPrompt, messages),
		StreamOptions: openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: param.NewOpt(true),
		},
	}
	if len(tools) > 0 {
		params.Tools = buildCompatTools(tools)
	}

	if oz.RateTracker != nil {
		waitForRateBudget(ctx, oz.RateTracker, oz.OnStatus, systemPrompt, messages)
	}

	stream, err := runCompatStreamRetry(ctx, oz.client, params, oz.Retry, oz.CircuitBreaker, oz.OnStatus, oz.OnCircuitOpen, span)
	if err != nil {
		return nil, err
	}

	ch := make(chan StreamEvent, 64)
	go bridgeCompatStream(ctx, stream, ch, oz.RateTracker, span)
	return ch, nil
}

// Complete makes a non-streaming completion call.
func (oz *OpenZen) Complete(ctx context.Context, systemPrompt string, messages []ChatMessage, model string) (string, error) {
	ctx, span := feotel.StartSpan(ctx, "nanite.provider.openzen.complete")
	defer span.End()
	span.SetAttributes(
		attribute.String("nanite.provider", "openzen"),
		attribute.String("nanite.model", model),
	)

	if oz.apiKey == "" {
		return "", fmt.Errorf("OPENZEN_API_KEY not set")
	}
	oz.ensureClient()

	if model == "" {
		model = openzenDefaultChatModel
	}

	params := openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(model),
		Messages: buildCompatMessages(systemPrompt, messages),
	}

	return runCompatComplete(ctx, oz.client, params, oz.Retry)
}

// Capabilities returns the capabilities supported by the OpenZen provider.
func (oz *OpenZen) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{
		SupportsStreamJSON:  true,
		SupportsToolCalling: false,
		SupportsImageInput:  true,
		MaxTokens:           0,      // Variable
		ContextWindowSize:   200000, // Upper bound for supported models
	}
}
