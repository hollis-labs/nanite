package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	feotel "github.com/hollis-labs/go-otel"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/packages/ssestream"
	"github.com/openai/openai-go/shared"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// maxCompatErrBody caps forwarded API-error response bytes for all four
// OpenAI-compatible adapters (Mistral, Azure OpenAI, OpenRouter, OpenZen).
// See ADAPTER_PATTERN.md §8.
const maxCompatErrBody = 1 << 20 // 1 MiB

const (
	mistralBaseURL          = "https://api.mistral.ai/v1/"
	mistralDefaultChatModel = "mistral-large-latest"
	mistralDefaultEmbedding = "mistral-embed"
)

var _ Embedder = (*Mistral)(nil)

// Mistral implements the Provider and Embedder interfaces for the Mistral API,
// which is wire-compatible with OpenAI's chat-completions + embeddings APIs.
// Transport is the official openai-go SDK configured with Mistral's base URL.
type Mistral struct {
	apiKey         string
	httpClient     *http.Client
	client         *openai.Client // lazily constructed once apiKey is set
	Retry          RetryConfig
	OnStatus       StatusCallback
	CircuitBreaker *CircuitBreaker
	OnCircuitOpen  func()
	RateTracker    *TokenRateTracker
}

// NewMistral creates a new Mistral provider. The API key is injected later via
// SetAPIKey (see pkg/provider/api_key.go).
func NewMistral() *Mistral {
	return &Mistral{
		httpClient:     &http.Client{},
		Retry:          DefaultRetryConfig(),
		CircuitBreaker: NewCircuitBreaker(3),
		RateTracker:    NewTokenRateTracker(30000),
	}
}

func (m *Mistral) ensureClient() {
	if m.client != nil || m.apiKey == "" {
		return
	}
	c := openai.NewClient(
		option.WithAPIKey(m.apiKey),
		option.WithBaseURL(mistralBaseURL),
		option.WithHTTPClient(m.httpClient),
		option.WithMaxRetries(0),
	)
	m.client = &c
}

// StreamChat implements Provider.StreamChat.
func (m *Mistral) StreamChat(ctx context.Context, systemPrompt string, messages []ChatMessage, model string) (<-chan StreamEvent, error) {
	return m.streamChatInternal(ctx, systemPrompt, messages, model, nil)
}

// StreamChatWithTools delegates to StreamChat for Mistral (tool calling path
// identical to the stock SDK, but the capability claim stays off until a
// follow-up task wires tools end-to-end).
func (m *Mistral) StreamChatWithTools(ctx context.Context, systemPrompt string, messages []ChatMessage, model string, tools []ToolDefinition) (<-chan StreamEvent, error) {
	return m.streamChatInternal(ctx, systemPrompt, messages, model, tools)
}

func (m *Mistral) streamChatInternal(ctx context.Context, systemPrompt string, messages []ChatMessage, model string, tools []ToolDefinition) (<-chan StreamEvent, error) {
	ctx, span := feotel.StartSpan(ctx, "nanite.provider.mistral.stream")
	span.SetAttributes(
		attribute.String("nanite.provider", "mistral"),
		attribute.String("nanite.model", model),
		attribute.Int("nanite.messages.count", len(messages)),
		attribute.Int("nanite.tools.count", len(tools)),
	)

	if m.apiKey == "" {
		span.SetStatus(codes.Error, "MISTRAL_API_KEY not set")
		span.End()
		return nil, fmt.Errorf("MISTRAL_API_KEY not set")
	}
	m.ensureClient()

	if model == "" {
		model = mistralDefaultChatModel
	}

	if m.CircuitBreaker != nil && m.CircuitBreaker.IsOpen() {
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

	if m.RateTracker != nil {
		waitForRateBudget(ctx, m.RateTracker, m.OnStatus, systemPrompt, messages)
	}

	stream, err := runCompatStreamRetry(ctx, m.client, params, m.Retry, m.CircuitBreaker, m.OnStatus, m.OnCircuitOpen, span)
	if err != nil {
		return nil, err
	}

	ch := make(chan StreamEvent, 64)
	go bridgeCompatStream(ctx, stream, ch, m.RateTracker, span)
	return ch, nil
}

// Complete makes a non-streaming completion call.
func (m *Mistral) Complete(ctx context.Context, systemPrompt string, messages []ChatMessage, model string) (string, error) {
	ctx, span := feotel.StartSpan(ctx, "nanite.provider.mistral.complete")
	defer span.End()
	span.SetAttributes(
		attribute.String("nanite.provider", "mistral"),
		attribute.String("nanite.model", model),
	)

	if m.apiKey == "" {
		return "", fmt.Errorf("MISTRAL_API_KEY not set")
	}
	m.ensureClient()

	if model == "" {
		model = mistralDefaultChatModel
	}

	params := openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(model),
		Messages: buildCompatMessages(systemPrompt, messages),
	}

	return runCompatComplete(ctx, m.client, params, m.Retry)
}

// Capabilities returns the capabilities supported by the Mistral provider.
func (m *Mistral) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{
		SupportsStreamJSON:    true,
		SupportsToolCalling:   false, // Not yet wired end-to-end
		SupportsImageInput:    true,  // Pixtral models support vision
		SupportsEmbedding:     true,
		DefaultEmbeddingModel: mistralDefaultEmbedding,
		MaxTokens:             16384,
		ContextWindowSize:     131072,
	}
}

// Embed generates an embedding vector for a single text input.
func (m *Mistral) Embed(ctx context.Context, text string, model string) (*EmbeddingResult, error) {
	results, err := m.EmbedBatch(ctx, []string{text}, model)
	if err != nil {
		return nil, err
	}
	return &results[0], nil
}

// EmbedBatch generates embedding vectors for multiple texts in a single API call.
func (m *Mistral) EmbedBatch(ctx context.Context, texts []string, model string) ([]EmbeddingResult, error) {
	if m.apiKey == "" {
		return nil, fmt.Errorf("MISTRAL_API_KEY not set")
	}
	m.ensureClient()

	if model == "" {
		model = mistralDefaultEmbedding
	}

	resp, err := m.client.Embeddings.New(ctx, openai.EmbeddingNewParams{
		Input: openai.EmbeddingNewParamsInputUnion{OfArrayOfStrings: texts},
		Model: openai.EmbeddingModel(model),
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
func (m *Mistral) EmbeddingDimensions(model string) int {
	switch model {
	case "mistral-embed":
		return 1024
	default:
		return 0
	}
}

// -----------------------------------------------------------------------------
// Shared helpers for OpenAI-protocol-compatible adapters
//
// These helpers are used by mistral.go, azure_openai.go, openrouter.go, and
// openzen.go. They live in this file (rather than a new shared file) because
// the task scope restricts changes to the four adapter files. The stock OpenAI
// adapter in openai.go does not use them.
// -----------------------------------------------------------------------------

// buildCompatMessages translates nanite ChatMessages into the openai-go
// chat-completions param shape. It handles the plain text message case only —
// tool_use / tool_result content blocks for these wire-compatible providers
// go through the same path but fall back to plain text per role.
func buildCompatMessages(systemPrompt string, messages []ChatMessage) []openai.ChatCompletionMessageParamUnion {
	out := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages)+1)
	if systemPrompt != "" {
		out = append(out, openai.SystemMessage(systemPrompt))
	}
	for _, m := range messages {
		if len(m.ContentBlocks) > 0 {
			out = append(out, chatMessageBlocksToSDK(m)...)
			continue
		}
		switch m.Role {
		case "user":
			out = append(out, openai.UserMessage(m.Content))
		case "assistant":
			out = append(out, openai.AssistantMessage(m.Content))
		case "system":
			out = append(out, openai.SystemMessage(m.Content))
		default:
			out = append(out, openai.UserMessage(m.Content))
		}
	}
	return out
}

func buildCompatTools(tools []ToolDefinition) []openai.ChatCompletionToolParam {
	if len(tools) == 0 {
		return nil
	}
	out := make([]openai.ChatCompletionToolParam, len(tools))
	for i, t := range tools {
		fn := shared.FunctionDefinitionParam{
			Name:       t.Name,
			Parameters: shared.FunctionParameters(t.InputSchema),
		}
		if t.Description != "" {
			fn.Description = param.NewOpt(t.Description)
		}
		out[i] = openai.ChatCompletionToolParam{Function: fn}
	}
	return out
}

// waitForRateBudget honours the RateTracker before a request fires.
func waitForRateBudget(ctx context.Context, rt *TokenRateTracker, onStatus StatusCallback, systemPrompt string, messages []ChatMessage) {
	if rt == nil {
		return
	}
	estimatedTokens := estimatePromptTokens(systemPrompt, messages)
	wait := rt.WaitTime(estimatedTokens)
	if wait <= 0 {
		return
	}
	avail, limit := rt.Remaining()
	if estimatedTokens > limit {
		log.Printf("provider: request ~%d tokens exceeds per-minute rate limit %d, proceeding anyway", estimatedTokens, limit)
	}
	if onStatus != nil {
		onStatus(fmt.Sprintf("Waiting %ds for rate limit budget...", int(wait.Seconds()+0.5)))
	}
	log.Printf("provider: pacing — waiting %s for rate limit budget (est. %d tokens, available %d/%d)",
		wait.Round(time.Millisecond), estimatedTokens, avail, limit)
	select {
	case <-ctx.Done():
		return
	case <-time.After(wait):
	}
}

// compatStreamHandle bundles the SDK stream adapter with the peeked first
// event so bridgeCompatStream can replay it.
type compatStreamHandle struct {
	stream   compatSDKStream
	primed   bool
	primedOK bool
}

type compatSDKStream interface {
	Next() bool
	Current() openai.ChatCompletionChunk
	Err() error
	Close() error
}

type compatStreamAdapter struct {
	s *ssestream.Stream[openai.ChatCompletionChunk]
}

func (a *compatStreamAdapter) Next() bool                          { return a.s.Next() }
func (a *compatStreamAdapter) Current() openai.ChatCompletionChunk { return a.s.Current() }
func (a *compatStreamAdapter) Err() error                          { return a.s.Err() }
func (a *compatStreamAdapter) Close() error                        { return a.s.Close() }

// peekCompatStream calls Next() once so start-time API errors surface through
// the retry loop rather than leaking into the consumer.
func peekCompatStream(s *ssestream.Stream[openai.ChatCompletionChunk]) (*compatStreamHandle, *APIError, time.Duration) {
	adapter := &compatStreamAdapter{s: s}
	primedOK := adapter.Next()
	if !primedOK {
		if err := adapter.Err(); err != nil {
			return nil, classifyCompatError(err), parseCompatRetryAfter(err)
		}
	}
	return &compatStreamHandle{stream: adapter, primed: true, primedOK: primedOK}, nil, 0
}

// runCompatStreamRetry wraps the SDK streaming call in our retry/circuit
// loop. The SDK's built-in retry is disabled in ensureClient.
func runCompatStreamRetry(
	ctx context.Context,
	client *openai.Client,
	params openai.ChatCompletionNewParams,
	retry RetryConfig,
	cb *CircuitBreaker,
	onStatus StatusCallback,
	onCircuitOpen func(),
	span trace.Span,
) (*compatStreamHandle, error) {
	requestStart := time.Now()

	var handle *compatStreamHandle
	var lastErr error
	for attempt := 0; attempt <= retry.MaxRetries; attempt++ {
		s := client.Chat.Completions.NewStreaming(ctx, params)
		h, apiErr, retryAfter := peekCompatStream(s)
		if apiErr == nil {
			handle = h
			if cb != nil {
				cb.RecordSuccess()
			}
			break
		}

		if !RetryableStatusCode(apiErr.StatusCode) || attempt == retry.MaxRetries {
			if cb != nil && attempt == retry.MaxRetries {
				if tripped := cb.RecordFailure(); tripped {
					log.Printf("provider: circuit breaker tripped after consecutive failures")
					if onCircuitOpen != nil {
						onCircuitOpen()
					}
				}
			}
			if span != nil {
				span.RecordError(apiErr)
				span.SetStatus(codes.Error, apiErr.Error())
				span.SetAttributes(attribute.Int("nanite.http.status", apiErr.StatusCode))
				span.End()
			}
			return nil, apiErr
		}

		delay := retry.BackoffDelay(attempt, retryAfter)
		log.Printf("provider: retryable error %d (attempt %d/%d), retrying in %s",
			apiErr.StatusCode, attempt+1, retry.MaxRetries, delay)
		if onStatus != nil {
			onStatus(fmt.Sprintf("Rate limited, retrying in %s... (attempt %d/%d)",
				delay.Round(time.Millisecond), attempt+1, retry.MaxRetries))
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context cancelled during retry: %w", ctx.Err())
		case <-time.After(delay):
		}
		lastErr = apiErr
	}
	_ = lastErr

	if span != nil {
		span.SetAttributes(
			attribute.Int64("nanite.provider.latency_ms", time.Since(requestStart).Milliseconds()),
		)
	}
	return handle, nil
}

// runCompatComplete wraps the non-streaming SDK call in the retry loop.
func runCompatComplete(ctx context.Context, client *openai.Client, params openai.ChatCompletionNewParams, retry RetryConfig) (string, error) {
	var resp *openai.ChatCompletion
	for attempt := 0; attempt <= retry.MaxRetries; attempt++ {
		var err error
		resp, err = client.Chat.Completions.New(ctx, params)
		if err == nil {
			break
		}
		apiErr := classifyCompatError(err)
		if !RetryableStatusCode(apiErr.StatusCode) || attempt == retry.MaxRetries {
			return "", apiErr
		}
		delay := retry.BackoffDelay(attempt, parseCompatRetryAfter(err))
		log.Printf("provider: retryable error %d (attempt %d/%d), retrying in %s",
			apiErr.StatusCode, attempt+1, retry.MaxRetries, delay)
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("context cancelled during retry: %w", ctx.Err())
		case <-time.After(delay):
		}
	}
	if resp != nil && len(resp.Choices) > 0 {
		return strings.TrimSpace(resp.Choices[0].Message.Content), nil
	}
	return "", nil
}

// compatToolCallAccumulator buffers partial tool_call deltas keyed by
// per-choice tool-call index.
type compatToolCallAccumulator struct {
	id        string
	name      string
	arguments strings.Builder
}

// bridgeCompatStream pumps ChatCompletionChunk values into nanite StreamEvents.
func bridgeCompatStream(ctx context.Context, handle *compatStreamHandle, ch chan<- StreamEvent, rateTracker *TokenRateTracker, span trace.Span) {
	var totalInput, totalOutput int
	defer func() {
		if handle != nil && handle.stream != nil {
			_ = handle.stream.Close()
		}
		close(ch)
		if span != nil {
			span.SetAttributes(
				attribute.Int("nanite.provider.input_tokens", totalInput),
				attribute.Int("nanite.provider.output_tokens", totalOutput),
			)
			span.End()
		}
	}()

	if handle == nil || handle.stream == nil {
		return
	}
	s := handle.stream

	toolAcc := map[int64]*compatToolCallAccumulator{}
	toolOrder := []int64{}
	inputRecorded := false
	var lastFinishReason string

	emitToolCalls := func() {
		for _, idx := range toolOrder {
			acc := toolAcc[idx]
			if acc == nil {
				continue
			}
			raw := acc.arguments.String()
			var input map[string]any
			if raw != "" {
				if err := json.Unmarshal([]byte(raw), &input); err != nil {
					input = map[string]any{"_raw": raw}
				}
			} else {
				input = map[string]any{}
			}
			ch <- StreamEvent{Type: "tool_use", ToolUse: &ToolUseBlock{
				ID:    acc.id,
				Name:  acc.name,
				Input: input,
			}}
		}
		toolAcc = map[int64]*compatToolCallAccumulator{}
		toolOrder = toolOrder[:0]
	}

	step := func(chunk openai.ChatCompletionChunk) bool {
		select {
		case <-ctx.Done():
			ch <- StreamEvent{Type: "error", Error: "context cancelled"}
			return false
		default:
		}

		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				ch <- StreamEvent{Type: "delta", Content: choice.Delta.Content}
			}
			for _, tc := range choice.Delta.ToolCalls {
				acc, ok := toolAcc[tc.Index]
				if !ok {
					acc = &compatToolCallAccumulator{}
					toolAcc[tc.Index] = acc
					toolOrder = append(toolOrder, tc.Index)
				}
				if tc.ID != "" {
					acc.id = tc.ID
				}
				if tc.Function.Name != "" {
					acc.name = tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					acc.arguments.WriteString(tc.Function.Arguments)
				}
			}
			if choice.FinishReason != "" {
				lastFinishReason = choice.FinishReason
			}
		}

		if chunk.Usage.PromptTokens != 0 || chunk.Usage.CompletionTokens != 0 {
			in := int(chunk.Usage.PromptTokens)
			out := int(chunk.Usage.CompletionTokens)
			if !inputRecorded && in > 0 {
				totalInput += in
				if rateTracker != nil {
					rateTracker.Record(in)
					avail, limit := rateTracker.Remaining()
					log.Printf("provider: recorded %d input tokens (rate budget: %d/%d)", in, avail, limit)
				}
				ch <- StreamEvent{Type: "usage", Usage: &Usage{InputTokens: in}}
				inputRecorded = true
			}
			if out > 0 {
				totalOutput += out
				ch <- StreamEvent{Type: "usage", Usage: &Usage{
					OutputTokens: out,
					StopReason:   lastFinishReason,
				}}
			}
		}
		return true
	}

	if handle.primed && handle.primedOK {
		if !step(s.Current()) {
			return
		}
	}
	for s.Next() {
		if !step(s.Current()) {
			return
		}
	}
	if err := s.Err(); err != nil && !isStreamClosedErr(err) {
		ch <- StreamEvent{Type: "error", Error: err.Error()}
		return
	}

	emitToolCalls()
	ch <- StreamEvent{Type: "done"}
}

// classifyCompatError maps an openai-go SDK error to an *APIError with a
// response body cap. Non-SDK errors return StatusCode 0 (non-retryable).
func classifyCompatError(err error) *APIError {
	if err == nil {
		return nil
	}
	var sdkErr *openai.Error
	if errors.As(err, &sdkErr) {
		raw := sdkErr.RawJSON()
		if len(raw) > maxCompatErrBody {
			raw = raw[:maxCompatErrBody]
		}
		return &APIError{
			StatusCode: sdkErr.StatusCode,
			Message:    raw,
			RetryAfter: parseCompatRetryAfter(err),
		}
	}
	return &APIError{StatusCode: 0, Message: err.Error()}
}

func parseCompatRetryAfter(err error) time.Duration {
	var sdkErr *openai.Error
	if errors.As(err, &sdkErr) && sdkErr.Response != nil {
		return ParseRetryAfter(sdkErr.Response.Header.Get("Retry-After"))
	}
	return 0
}
