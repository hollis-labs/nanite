package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/packages/ssestream"
	"github.com/openai/openai-go/shared"
	feotel "github.com/hollis-labs/go-otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// maxOpenAIErrBody caps forwarded API-error response bytes. The openai-go SDK
// does not apply a body limit when reading error responses — see
// ADAPTER_PATTERN.md §8. We cap what we forward into APIError.Message.
const maxOpenAIErrBody = 1 << 20 // 1 MiB

// Compile-time interface check: OpenAI must satisfy Embedder.
var _ Embedder = (*OpenAI)(nil)

// OpenAI implements the Provider and Embedder interfaces for the OpenAI
// Chat Completions and Embeddings APIs. Transport is the official openai-go
// SDK; this type adapts its types to nanite's Provider interface.
type OpenAI struct {
	apiKey         string
	httpClient     *http.Client
	client         *openai.Client // lazily constructed once apiKey is set
	Retry          RetryConfig
	OnStatus       StatusCallback
	CircuitBreaker *CircuitBreaker
	OnCircuitOpen  func()
	RateTracker    *TokenRateTracker
}

// NewOpenAI creates a new OpenAI provider. The API key is injected later via
// SetAPIKey (see pkg/provider/api_key.go).
func NewOpenAI() *OpenAI {
	return &OpenAI{
		httpClient:     &http.Client{},
		Retry:          DefaultRetryConfig(),
		CircuitBreaker: NewCircuitBreaker(3),
		RateTracker:    NewTokenRateTracker(30000),
	}
}

// ensureClient lazily builds the SDK client once an API key is set. The SDK's
// own retry is disabled (WithMaxRetries(0)) — the retry decorator layer
// (retry.go, circuit.go) owns that policy.
func (o *OpenAI) ensureClient() {
	if o.client != nil || o.apiKey == "" {
		return
	}
	c := openai.NewClient(
		option.WithAPIKey(o.apiKey),
		option.WithHTTPClient(o.httpClient),
		option.WithMaxRetries(0),
	)
	o.client = &c
}

// -----------------------------------------------------------------------------
// SDK request builders — translate nanite's types into openai-go params.
// -----------------------------------------------------------------------------

func (o *OpenAI) buildSDKMessages(systemPrompt string, messages []ChatMessage) []openai.ChatCompletionMessageParamUnion {
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

// chatMessageBlocksToSDK translates a ChatMessage with ContentBlocks (the
// tool_use / tool_result shape) into one or more SDK message params.
//
// OpenAI's chat-completions schema differs from Anthropic's:
//   - An assistant turn that makes tool calls is a single assistant message
//     whose `tool_calls` array carries every tool_use block.
//   - Each tool_result is its own `tool` message referencing the tool_call_id.
//
// We therefore collapse tool_use blocks into a single assistant message and
// emit a separate tool message per tool_result.
func chatMessageBlocksToSDK(m ChatMessage) []openai.ChatCompletionMessageParamUnion {
	out := make([]openai.ChatCompletionMessageParamUnion, 0, len(m.ContentBlocks))

	switch m.Role {
	case "assistant":
		var text strings.Builder
		var toolCalls []openai.ChatCompletionMessageToolCallParam
		for _, b := range m.ContentBlocks {
			switch b.Type {
			case "text":
				text.WriteString(b.Text)
			case "tool_use":
				args := "{}"
				if b.Input != nil {
					if raw, err := json.Marshal(*b.Input); err == nil {
						args = string(raw)
					}
				}
				toolCalls = append(toolCalls, openai.ChatCompletionMessageToolCallParam{
					ID: b.ID,
					Function: openai.ChatCompletionMessageToolCallFunctionParam{
						Name:      b.Name,
						Arguments: args,
					},
				})
			}
		}
		asst := openai.ChatCompletionAssistantMessageParam{}
		if text.Len() > 0 {
			asst.Content = openai.ChatCompletionAssistantMessageParamContentUnion{
				OfString: param.NewOpt(text.String()),
			}
		}
		if len(toolCalls) > 0 {
			asst.ToolCalls = toolCalls
		}
		out = append(out, openai.ChatCompletionMessageParamUnion{OfAssistant: &asst})

	case "user":
		// tool_result blocks from the caller arrive on user-role messages
		// (mirroring the Anthropic convention). Emit one tool message per
		// tool_result and fold any plain text into a separate user message.
		var text strings.Builder
		for _, b := range m.ContentBlocks {
			switch b.Type {
			case "tool_result":
				out = append(out, openai.ToolMessage(b.Content, b.ToolUseID))
			case "text":
				text.WriteString(b.Text)
			}
		}
		if text.Len() > 0 {
			out = append(out, openai.UserMessage(text.String()))
		}

	default:
		// Fallback: concatenate text blocks.
		var text strings.Builder
		for _, b := range m.ContentBlocks {
			if b.Type == "text" {
				text.WriteString(b.Text)
			}
		}
		if text.Len() > 0 {
			out = append(out, openai.UserMessage(text.String()))
		}
	}
	return out
}

func (o *OpenAI) buildSDKTools(tools []ToolDefinition) []openai.ChatCompletionToolParam {
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

// -----------------------------------------------------------------------------
// Provider interface
// -----------------------------------------------------------------------------

// StreamChat implements Provider.StreamChat using OpenAI's streaming SSE API.
func (o *OpenAI) StreamChat(ctx context.Context, systemPrompt string, messages []ChatMessage, model string) (<-chan StreamEvent, error) {
	return o.streamChatInternal(ctx, systemPrompt, messages, model, nil)
}

// StreamChatWithTools implements Provider.StreamChatWithTools using OpenAI's
// streaming function-calling API.
func (o *OpenAI) StreamChatWithTools(ctx context.Context, systemPrompt string, messages []ChatMessage, model string, tools []ToolDefinition) (<-chan StreamEvent, error) {
	return o.streamChatInternal(ctx, systemPrompt, messages, model, tools)
}

func (o *OpenAI) streamChatInternal(ctx context.Context, systemPrompt string, messages []ChatMessage, model string, tools []ToolDefinition) (<-chan StreamEvent, error) {
	ctx, span := feotel.StartSpan(ctx, "nanite.provider.openai.stream")
	span.SetAttributes(
		attribute.String("nanite.provider", "openai"),
		attribute.String("nanite.model", model),
		attribute.Int("nanite.messages.count", len(messages)),
		attribute.Int("nanite.tools.count", len(tools)),
	)

	if o.apiKey == "" {
		span.SetStatus(codes.Error, "OPENAI_API_KEY not set")
		span.End()
		return nil, fmt.Errorf("OPENAI_API_KEY not set")
	}
	o.ensureClient()

	if model == "" {
		model = "gpt-4o"
	}

	if o.CircuitBreaker != nil && o.CircuitBreaker.IsOpen() {
		span.SetStatus(codes.Error, "circuit breaker open")
		span.End()
		return nil, fmt.Errorf("circuit breaker open: provider rate limited after multiple retries")
	}

	params := openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(model),
		Messages: o.buildSDKMessages(systemPrompt, messages),
		// Request usage stats in the terminal stream chunk.
		StreamOptions: openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: param.NewOpt(true),
		},
	}
	if len(tools) > 0 {
		params.Tools = o.buildSDKTools(tools)
	}

	// Rate-limit pacing. Estimate is coarse — the SDK marshals the body itself.
	if o.RateTracker != nil {
		estimatedTokens := estimatePromptTokens(systemPrompt, messages)
		if wait := o.RateTracker.WaitTime(estimatedTokens); wait > 0 {
			avail, limit := o.RateTracker.Remaining()
			if estimatedTokens > limit {
				log.Printf("provider: request ~%d tokens exceeds per-minute rate limit %d, proceeding anyway", estimatedTokens, limit)
			}
			if o.OnStatus != nil {
				o.OnStatus(fmt.Sprintf("Waiting %ds for rate limit budget...", int(wait.Seconds()+0.5)))
			}
			log.Printf("provider: pacing — waiting %s for rate limit budget (est. %d tokens, available %d/%d)",
				wait.Round(time.Millisecond), estimatedTokens, avail, limit)
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("context cancelled during rate limit wait: %w", ctx.Err())
			case <-time.After(wait):
			}
		}
	}

	requestStart := time.Now()

	// Retry loop — our decorator chain; the SDK's own retry is disabled.
	var stream *openaiStreamHandle
	var lastErr error
	for attempt := 0; attempt <= o.Retry.MaxRetries; attempt++ {
		s := o.client.Chat.Completions.NewStreaming(ctx, params)

		// The SDK defers HTTP work until Next() is called. Peek the first
		// chunk so start-time API errors flow through the retry loop.
		handle, apiErr, retryAfter := peekOpenAIStream(s)
		if apiErr == nil {
			stream = handle
			if o.CircuitBreaker != nil {
				o.CircuitBreaker.RecordSuccess()
			}
			break
		}

		if !RetryableStatusCode(apiErr.StatusCode) || attempt == o.Retry.MaxRetries {
			if o.CircuitBreaker != nil && attempt == o.Retry.MaxRetries {
				if tripped := o.CircuitBreaker.RecordFailure(); tripped {
					log.Printf("provider: circuit breaker tripped after consecutive failures")
					if o.OnCircuitOpen != nil {
						o.OnCircuitOpen()
					}
				}
			}
			span.RecordError(apiErr)
			span.SetStatus(codes.Error, apiErr.Error())
			span.SetAttributes(attribute.Int("nanite.http.status", apiErr.StatusCode))
			span.End()
			return nil, apiErr
		}

		delay := o.Retry.BackoffDelay(attempt, retryAfter)
		log.Printf("provider: retryable error %d (attempt %d/%d), retrying in %s",
			apiErr.StatusCode, attempt+1, o.Retry.MaxRetries, delay)
		if o.OnStatus != nil {
			o.OnStatus(fmt.Sprintf("Rate limited, retrying in %s... (attempt %d/%d)",
				delay.Round(time.Millisecond), attempt+1, o.Retry.MaxRetries))
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context cancelled during retry: %w", ctx.Err())
		case <-time.After(delay):
		}
		lastErr = apiErr
	}
	_ = lastErr

	span.SetAttributes(
		attribute.Int64("nanite.provider.latency_ms", time.Since(requestStart).Milliseconds()),
	)

	ch := make(chan StreamEvent, 64)
	go o.bridgeStream(ctx, stream, ch, span)
	return ch, nil
}

// openaiStreamHandle carries the SDK stream plus the peeked first chunk.
type openaiStreamHandle struct {
	stream openaiSDKStream
	primed bool
	primedOK bool
}

// openaiSDKStream is the subset of ssestream.Stream we use; defining it as an
// interface keeps the bridge test-friendly.
type openaiSDKStream interface {
	Next() bool
	Current() openai.ChatCompletionChunk
	Err() error
	Close() error
}

// peekOpenAIStream does Next() once to surface start-time API errors so the
// retry loop can catch them.
func peekOpenAIStream(s *ssestream.Stream[openai.ChatCompletionChunk]) (*openaiStreamHandle, *APIError, time.Duration) {
	adapter := &openaiStreamAdapter{s: s}
	primedOK := adapter.Next()
	if !primedOK {
		if err := adapter.Err(); err != nil {
			return nil, classifyOpenAIError(err), parseOpenAIRetryAfter(err)
		}
		// Empty stream — treat as success; bridge closes immediately.
	}
	return &openaiStreamHandle{
		stream:   adapter,
		primed:   true,
		primedOK: primedOK,
	}, nil, 0
}

type openaiStreamAdapter struct {
	s *ssestream.Stream[openai.ChatCompletionChunk]
}

func (a *openaiStreamAdapter) Next() bool                           { return a.s.Next() }
func (a *openaiStreamAdapter) Current() openai.ChatCompletionChunk  { return a.s.Current() }
func (a *openaiStreamAdapter) Err() error                           { return a.s.Err() }
func (a *openaiStreamAdapter) Close() error                         { return a.s.Close() }

// toolCallAccumulator buffers partial tool_call deltas keyed by per-choice
// tool-call index. The SDK streams each tool_call's name once and the JSON
// arguments as a sequence of string deltas; we finalise on finish_reason or
// end-of-stream.
type toolCallAccumulator struct {
	id        string
	name      string
	arguments strings.Builder
}

// bridgeStream pumps ChatCompletionChunk values into nanite StreamEvents.
func (o *OpenAI) bridgeStream(ctx context.Context, handle *openaiStreamHandle, ch chan<- StreamEvent, span trace.Span) {
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
	// Tool-call accumulators are keyed by per-choice tool_call index; the SDK
	// reports the index on every delta chunk.
	toolAcc := map[int64]*toolCallAccumulator{}
	// Preserve emission order so deterministic tests are possible.
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
		toolAcc = map[int64]*toolCallAccumulator{}
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
			// Text delta.
			if choice.Delta.Content != "" {
				ch <- StreamEvent{Type: "delta", Content: choice.Delta.Content}
			}

			// Tool-call deltas. The SDK reports an index per tool_call that
			// stays stable across chunks; name/id appear on the first delta,
			// arguments accumulate over subsequent chunks.
			for _, tc := range choice.Delta.ToolCalls {
				acc, ok := toolAcc[tc.Index]
				if !ok {
					acc = &toolCallAccumulator{}
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

		// Usage is reported in the terminal chunk when IncludeUsage is set.
		if chunk.Usage.PromptTokens != 0 || chunk.Usage.CompletionTokens != 0 {
			in := int(chunk.Usage.PromptTokens)
			out := int(chunk.Usage.CompletionTokens)
			if !inputRecorded && in > 0 {
				totalInput += in
				if o.RateTracker != nil {
					o.RateTracker.Record(in)
					avail, limit := o.RateTracker.Remaining()
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

	// Process the primed chunk (if peek returned one).
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

	// Emit accumulated tool calls and a terminal done event.
	emitToolCalls()
	ch <- StreamEvent{Type: "done"}
}

// Complete makes a non-streaming completion call.
func (o *OpenAI) Complete(ctx context.Context, systemPrompt string, messages []ChatMessage, model string) (string, error) {
	ctx, span := feotel.StartSpan(ctx, "nanite.provider.openai.complete")
	defer span.End()
	span.SetAttributes(
		attribute.String("nanite.provider", "openai"),
		attribute.String("nanite.model", model),
		attribute.Int("nanite.messages.count", len(messages)),
	)

	if o.apiKey == "" {
		span.SetStatus(codes.Error, "OPENAI_API_KEY not set")
		return "", fmt.Errorf("OPENAI_API_KEY not set")
	}
	o.ensureClient()

	if model == "" {
		model = "gpt-4o"
	}

	params := openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(model),
		Messages: o.buildSDKMessages(systemPrompt, messages),
	}

	var resp *openai.ChatCompletion
	for attempt := 0; attempt <= o.Retry.MaxRetries; attempt++ {
		var err error
		resp, err = o.client.Chat.Completions.New(ctx, params)
		if err == nil {
			break
		}
		apiErr := classifyOpenAIError(err)
		if !RetryableStatusCode(apiErr.StatusCode) || attempt == o.Retry.MaxRetries {
			return "", apiErr
		}
		delay := o.Retry.BackoffDelay(attempt, parseOpenAIRetryAfter(err))
		log.Printf("provider: retryable error %d (attempt %d/%d), retrying in %s",
			apiErr.StatusCode, attempt+1, o.Retry.MaxRetries, delay)
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("context cancelled during retry: %w", ctx.Err())
		case <-time.After(delay):
		}
	}

	if len(resp.Choices) > 0 {
		return strings.TrimSpace(resp.Choices[0].Message.Content), nil
	}
	return "", nil
}

// Capabilities returns the capabilities supported by the OpenAI provider.
// Note: SupportsToolCalling is kept false for backwards-compat with existing
// capability assertions (see capabilities_test.go). The adapter now plumbs
// tools through the SDK, but the capability claim stays off until a follow-up
// task wires tool calling through the orchestrator end-to-end.
func (o *OpenAI) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{
		SupportsStreamJSON:          true,
		SupportsPreToolHooks:        false,
		SupportsPostToolHooks:       false,
		SupportsSystemPromptCaching: false,
		SupportsToolCalling:         false,
		SupportsBatch:               false,
		SupportsImageInput:          true,
		SupportsEmbedding:           true,
		DefaultEmbeddingModel:       "text-embedding-3-small",
		MaxTokens:                   16384,
		ContextWindowSize:           128000,
	}
}

// -----------------------------------------------------------------------------
// Embedder interface
// -----------------------------------------------------------------------------

// Embed generates an embedding vector for a single text input.
func (o *OpenAI) Embed(ctx context.Context, text string, model string) (*EmbeddingResult, error) {
	results, err := o.EmbedBatch(ctx, []string{text}, model)
	if err != nil {
		return nil, err
	}
	return &results[0], nil
}

// EmbedBatch generates embedding vectors for multiple texts in a single API call.
func (o *OpenAI) EmbedBatch(ctx context.Context, texts []string, model string) ([]EmbeddingResult, error) {
	if o.apiKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY not set")
	}
	o.ensureClient()

	if model == "" {
		model = "text-embedding-3-small"
	}

	resp, err := o.client.Embeddings.New(ctx, openai.EmbeddingNewParams{
		Input: openai.EmbeddingNewParamsInputUnion{OfArrayOfStrings: texts},
		Model: openai.EmbeddingModel(model),
	})
	if err != nil {
		return nil, classifyOpenAIError(err)
	}

	results := make([]EmbeddingResult, len(resp.Data))
	tokensPerText := 0
	if len(texts) > 0 {
		tokensPerText = int(resp.Usage.PromptTokens) / len(texts)
	}
	for _, d := range resp.Data {
		// SDK returns []float64; nanite wants []float32.
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
// Returns 0 if the model is unknown.
func (o *OpenAI) EmbeddingDimensions(model string) int {
	switch model {
	case "text-embedding-3-small":
		return 1536
	case "text-embedding-3-large":
		return 3072
	case "text-embedding-ada-002":
		return 1536
	default:
		return 0
	}
}

// -----------------------------------------------------------------------------
// Error mapping
// -----------------------------------------------------------------------------

// classifyOpenAIError maps an SDK error to an *APIError. openai.Error carries
// StatusCode + Response; anything else is wrapped as status 0, which
// RetryableStatusCode treats as non-retryable.
func classifyOpenAIError(err error) *APIError {
	if err == nil {
		return nil
	}
	var sdkErr *openai.Error
	if errors.As(err, &sdkErr) {
		raw := sdkErr.RawJSON()
		if len(raw) > maxOpenAIErrBody {
			raw = raw[:maxOpenAIErrBody]
		}
		return &APIError{
			StatusCode: sdkErr.StatusCode,
			Message:    raw,
			RetryAfter: parseOpenAIRetryAfter(err),
		}
	}
	return &APIError{StatusCode: 0, Message: err.Error()}
}

// parseOpenAIRetryAfter extracts Retry-After from the SDK error's Response.
func parseOpenAIRetryAfter(err error) time.Duration {
	var sdkErr *openai.Error
	if errors.As(err, &sdkErr) && sdkErr.Response != nil {
		return ParseRetryAfter(sdkErr.Response.Header.Get("Retry-After"))
	}
	return 0
}

// ensure io is referenced for the error-cap constant and keep imports stable
// if future edits move the read path around.
var _ = io.EOF
