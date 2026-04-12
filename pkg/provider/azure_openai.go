package provider

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/azure"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/shared"
	feotel "github.com/hollis-labs/go-otel"
	"github.com/hollis-labs/nanite/internal/safego"
	"github.com/hollis-labs/nanite/pkg/models"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

var _ Embedder = (*AzureOpenAI)(nil)

// AzureOpenAI implements the Provider and Embedder interfaces for Azure-hosted
// OpenAI models. Transport is the official openai-go SDK configured via its
// azure subpackage, which rewrites the request path to
// /openai/deployments/<deployment>/... and sends the Api-Key header for auth.
// The Azure SDK middleware extracts the deployment name from the request's
// `model` field, so we set that field to the deployment name.
type AzureOpenAI struct {
	apiKey         string
	endpoint       string // e.g. https://<resource>.openai.azure.com
	deployment     string // deployment name; used as the "model" field
	apiVersion     string // e.g. 2024-06-01
	httpClient     *http.Client
	client         *openai.Client
	Retry          RetryConfig
	OnStatus       StatusCallback
	CircuitBreaker *CircuitBreaker
	OnCircuitOpen  func()
	RateTracker    *TokenRateTracker
}

// NewAzureOpenAI creates a new Azure OpenAI provider. Configuration from env:
//   - AZURE_OPENAI_API_KEY — API key
//   - AZURE_OPENAI_ENDPOINT — resource endpoint URL
//   - AZURE_OPENAI_DEPLOYMENT — deployment name
//   - AZURE_OPENAI_API_VERSION — API version (default: 2024-06-01)
func NewAzureOpenAI() *AzureOpenAI {
	apiVersion := os.Getenv("AZURE_OPENAI_API_VERSION")
	if apiVersion == "" {
		apiVersion = "2024-06-01"
	}
	return &AzureOpenAI{
		endpoint:       strings.TrimRight(os.Getenv("AZURE_OPENAI_ENDPOINT"), "/"),
		deployment:     os.Getenv("AZURE_OPENAI_DEPLOYMENT"),
		apiVersion:     apiVersion,
		httpClient:     &http.Client{},
		Retry:          DefaultRetryConfig(),
		CircuitBreaker: NewCircuitBreaker(3),
		RateTracker:    NewTokenRateTracker(30000),
	}
}

func (az *AzureOpenAI) ensureClient() {
	if az.client != nil || az.apiKey == "" || az.endpoint == "" || az.apiVersion == "" {
		return
	}
	c := openai.NewClient(
		azure.WithEndpoint(az.endpoint, az.apiVersion),
		azure.WithAPIKey(az.apiKey),
		option.WithHTTPClient(az.httpClient),
		option.WithMaxRetries(0),
	)
	az.client = &c
}

// effectiveDeployment returns the deployment to use as the SDK `model` field.
// For chat completions the caller-supplied `model` is ignored (Azure routes by
// deployment); when it's empty we fall back to the configured deployment.
func (az *AzureOpenAI) effectiveDeployment(model string) string {
	if model != "" {
		return model
	}
	return az.deployment
}

// StreamChat implements Provider.StreamChat using Azure OpenAI's streaming API.
func (az *AzureOpenAI) StreamChat(ctx context.Context, systemPrompt string, messages []ChatMessage, model string) (<-chan StreamEvent, error) {
	return az.streamChatInternal(ctx, systemPrompt, messages, model, nil)
}

// StreamChatWithTools delegates through the same SDK path.
func (az *AzureOpenAI) StreamChatWithTools(ctx context.Context, systemPrompt string, messages []ChatMessage, model string, tools []ToolDefinition) (<-chan StreamEvent, error) {
	return az.streamChatInternal(ctx, systemPrompt, messages, model, tools)
}

func (az *AzureOpenAI) streamChatInternal(ctx context.Context, systemPrompt string, messages []ChatMessage, model string, tools []ToolDefinition) (<-chan StreamEvent, error) {
	ctx, span := feotel.StartSpan(ctx, "nanite.provider.azure_openai.stream")
	span.SetAttributes(
		attribute.String("nanite.provider", "azure_openai"),
		attribute.String("nanite.model", model),
		attribute.Int("nanite.messages.count", len(messages)),
		attribute.Int("nanite.tools.count", len(tools)),
	)

	if az.apiKey == "" {
		span.SetStatus(codes.Error, "AZURE_OPENAI_API_KEY not set")
		span.End()
		return nil, fmt.Errorf("AZURE_OPENAI_API_KEY not set")
	}
	if az.endpoint == "" {
		span.SetStatus(codes.Error, "AZURE_OPENAI_ENDPOINT not set")
		span.End()
		return nil, fmt.Errorf("AZURE_OPENAI_ENDPOINT not set")
	}
	az.ensureClient()

	deployment := az.effectiveDeployment(model)
	if deployment == "" {
		span.SetStatus(codes.Error, "AZURE_OPENAI_DEPLOYMENT not set")
		span.End()
		return nil, fmt.Errorf("AZURE_OPENAI_DEPLOYMENT not set")
	}

	if az.CircuitBreaker != nil && az.CircuitBreaker.IsOpen() {
		span.SetStatus(codes.Error, "circuit breaker open")
		span.End()
		return nil, fmt.Errorf("circuit breaker open: provider rate limited after multiple retries")
	}

	params := openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(deployment),
		Messages: buildCompatMessages(systemPrompt, messages),
		StreamOptions: openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: param.NewOpt(true),
		},
	}
	if len(tools) > 0 {
		params.Tools = buildCompatTools(tools)
	}

	if az.RateTracker != nil {
		waitForRateBudget(ctx, az.RateTracker, az.OnStatus, systemPrompt, messages)
	}

	stream, err := runCompatStreamRetry(ctx, az.client, params, az.Retry, az.CircuitBreaker, az.OnStatus, az.OnCircuitOpen, span)
	if err != nil {
		return nil, err
	}

	ch := make(chan StreamEvent, 64)
	safego.Go(ctx, "provider.azure_openai.bridgeCompatStream", func() {
		bridgeCompatStream(ctx, stream, ch, az.RateTracker, span)
	})
	return ch, nil
}

// Complete makes a non-streaming completion call to Azure OpenAI.
func (az *AzureOpenAI) Complete(ctx context.Context, systemPrompt string, messages []ChatMessage, model string) (string, error) {
	ctx, span := feotel.StartSpan(ctx, "nanite.provider.azure_openai.complete")
	defer span.End()
	span.SetAttributes(
		attribute.String("nanite.provider", "azure_openai"),
		attribute.String("nanite.model", model),
	)

	if az.apiKey == "" {
		return "", fmt.Errorf("AZURE_OPENAI_API_KEY not set")
	}
	if az.endpoint == "" {
		return "", fmt.Errorf("AZURE_OPENAI_ENDPOINT not set")
	}
	az.ensureClient()

	deployment := az.effectiveDeployment(model)
	if deployment == "" {
		return "", fmt.Errorf("AZURE_OPENAI_DEPLOYMENT not set")
	}

	params := openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(deployment),
		Messages: buildCompatMessages(systemPrompt, messages),
	}

	return runCompatComplete(ctx, az.client, params, az.Retry)
}

// Capabilities returns the capabilities supported by the Azure OpenAI provider.
func (az *AzureOpenAI) Capabilities() ProviderCapabilities {
	return capabilitiesFromRegistry("azure-openai")
}

// Embed generates an embedding vector for a single text input.
func (az *AzureOpenAI) Embed(ctx context.Context, text string, model string) (*EmbeddingResult, error) {
	results, err := az.EmbedBatch(ctx, []string{text}, model)
	if err != nil {
		return nil, err
	}
	return &results[0], nil
}

// EmbedBatch generates embedding vectors for multiple texts in a single API call.
// On Azure, `model` is the deployment name for the embedding model.
func (az *AzureOpenAI) EmbedBatch(ctx context.Context, texts []string, model string) ([]EmbeddingResult, error) {
	if az.apiKey == "" {
		return nil, fmt.Errorf("AZURE_OPENAI_API_KEY not set")
	}
	if az.endpoint == "" {
		return nil, fmt.Errorf("AZURE_OPENAI_ENDPOINT not set")
	}
	az.ensureClient()

	deployment := model
	if deployment == "" {
		deployment = az.deployment
	}
	if deployment == "" {
		return nil, fmt.Errorf("AZURE_OPENAI_DEPLOYMENT not set")
	}

	resp, err := az.client.Embeddings.New(ctx, openai.EmbeddingNewParams{
		Input: openai.EmbeddingNewParamsInputUnion{OfArrayOfStrings: texts},
		Model: openai.EmbeddingModel(deployment),
	})
	if err != nil {
		return nil, classifyCompatError(err)
	}

	results := make([]EmbeddingResult, len(resp.Data))
	tokensPerText := 0
	if len(texts) > 0 {
		tokensPerText = int(resp.Usage.PromptTokens) / len(texts)
	}
	for _, d := range resp.Data {
		vec := make([]float32, len(d.Embedding))
		for i, v := range d.Embedding {
			vec[i] = float32(v)
		}
		results[d.Index] = EmbeddingResult{
			Embedding:  vec,
			TokenCount: tokensPerText,
		}
	}
	return results, nil
}

// EmbeddingDimensions returns the output dimensions for the given model.
func (az *AzureOpenAI) EmbeddingDimensions(model string) int {
	return models.EmbeddingDimensionsFor(model)
}
