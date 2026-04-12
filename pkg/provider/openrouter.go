package provider

import (
	"context"
	"fmt"
	"net/http"
	"os"

	feotel "github.com/hollis-labs/go-otel"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/shared"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

const (
	openrouterBaseURL          = "https://openrouter.ai/api/v1/"
	openrouterDefaultChatModel = "anthropic/claude-sonnet-4"
)

// OpenRouter implements the Provider interface for the OpenRouter API.
// OpenRouter is an OpenAI-compatible gateway that routes to 200+ models.
// Transport is the official openai-go SDK configured with OpenRouter's base
// URL; the HTTP-Referer and X-Title headers are forwarded so OpenRouter's
// dashboard attribution works correctly.
type OpenRouter struct {
	apiKey         string
	httpReferer    string
	xTitle         string
	httpClient     *http.Client
	client         *openai.Client
	Retry          RetryConfig
	OnStatus       StatusCallback
	CircuitBreaker *CircuitBreaker
	OnCircuitOpen  func()
	RateTracker    *TokenRateTracker
}

// NewOpenRouter creates a new OpenRouter provider.
// Optional env vars OPENROUTER_HTTP_REFERER and OPENROUTER_X_TITLE are used
// to populate the attribution headers OpenRouter documents.
func NewOpenRouter() *OpenRouter {
	return &OpenRouter{
		httpReferer:    os.Getenv("OPENROUTER_HTTP_REFERER"),
		xTitle:         os.Getenv("OPENROUTER_X_TITLE"),
		httpClient:     &http.Client{},
		Retry:          DefaultRetryConfig(),
		CircuitBreaker: NewCircuitBreaker(3),
		RateTracker:    NewTokenRateTracker(30000),
	}
}

func (o *OpenRouter) ensureClient() {
	if o.client != nil || o.apiKey == "" {
		return
	}
	opts := []option.RequestOption{
		option.WithAPIKey(o.apiKey),
		option.WithBaseURL(openrouterBaseURL),
		option.WithHTTPClient(o.httpClient),
		option.WithMaxRetries(0),
	}
	if o.httpReferer != "" {
		opts = append(opts, option.WithHeader("HTTP-Referer", o.httpReferer))
	}
	if o.xTitle != "" {
		opts = append(opts, option.WithHeader("X-Title", o.xTitle))
	}
	c := openai.NewClient(opts...)
	o.client = &c
}

// StreamChat implements Provider.StreamChat.
func (o *OpenRouter) StreamChat(ctx context.Context, systemPrompt string, messages []ChatMessage, model string) (<-chan StreamEvent, error) {
	return o.streamChatInternal(ctx, systemPrompt, messages, model, nil)
}

// StreamChatWithTools delegates through the same SDK path.
func (o *OpenRouter) StreamChatWithTools(ctx context.Context, systemPrompt string, messages []ChatMessage, model string, tools []ToolDefinition) (<-chan StreamEvent, error) {
	return o.streamChatInternal(ctx, systemPrompt, messages, model, tools)
}

func (o *OpenRouter) streamChatInternal(ctx context.Context, systemPrompt string, messages []ChatMessage, model string, tools []ToolDefinition) (<-chan StreamEvent, error) {
	ctx, span := feotel.StartSpan(ctx, "nanite.provider.openrouter.stream")
	span.SetAttributes(
		attribute.String("nanite.provider", "openrouter"),
		attribute.String("nanite.model", model),
		attribute.Int("nanite.messages.count", len(messages)),
		attribute.Int("nanite.tools.count", len(tools)),
	)

	if o.apiKey == "" {
		span.SetStatus(codes.Error, "OPENROUTER_API_KEY not set")
		span.End()
		return nil, fmt.Errorf("OPENROUTER_API_KEY not set")
	}
	o.ensureClient()

	if model == "" {
		model = openrouterDefaultChatModel
	}

	if o.CircuitBreaker != nil && o.CircuitBreaker.IsOpen() {
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

	if o.RateTracker != nil {
		waitForRateBudget(ctx, o.RateTracker, o.OnStatus, systemPrompt, messages)
	}

	stream, err := runCompatStreamRetry(ctx, o.client, params, o.Retry, o.CircuitBreaker, o.OnStatus, o.OnCircuitOpen, span)
	if err != nil {
		return nil, err
	}

	ch := make(chan StreamEvent, 64)
	go bridgeCompatStream(ctx, stream, ch, o.RateTracker, span)
	return ch, nil
}

// Complete makes a non-streaming completion call.
func (o *OpenRouter) Complete(ctx context.Context, systemPrompt string, messages []ChatMessage, model string) (string, error) {
	ctx, span := feotel.StartSpan(ctx, "nanite.provider.openrouter.complete")
	defer span.End()
	span.SetAttributes(
		attribute.String("nanite.provider", "openrouter"),
		attribute.String("nanite.model", model),
	)

	if o.apiKey == "" {
		return "", fmt.Errorf("OPENROUTER_API_KEY not set")
	}
	o.ensureClient()

	if model == "" {
		model = openrouterDefaultChatModel
	}

	params := openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(model),
		Messages: buildCompatMessages(systemPrompt, messages),
	}

	return runCompatComplete(ctx, o.client, params, o.Retry)
}

// Capabilities returns the capabilities supported by the OpenRouter provider.
func (o *OpenRouter) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{
		SupportsStreamJSON:  true,
		SupportsToolCalling: false,
		SupportsImageInput:  true,   // Depends on routed model; upper-bound claim
		MaxTokens:           0,      // Variable
		ContextWindowSize:   200000, // Upper bound for supported models
	}
}
