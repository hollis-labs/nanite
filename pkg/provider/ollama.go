package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/ollama/ollama/api"
	feotel "github.com/hollis-labs/go-otel"
	"github.com/hollis-labs/nanite/internal/safego"
	"github.com/hollis-labs/nanite/pkg/models"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// maxOllamaErrBody caps forwarded API-error response bytes.
// The SDK does not apply an io.LimitReader to response bodies; we cap what we
// forward into APIError.Message ourselves. See ADAPTER_PATTERN.md §8.
const maxOllamaErrBody = 1 << 20 // 1 MiB

// Compile-time check: Ollama implements the Embedder interface.
var _ Embedder = (*Ollama)(nil)

// Ollama implements the Provider and Embedder interfaces for a local (or
// remote) Ollama daemon. Transport is the official `github.com/ollama/ollama/api`
// client; this type adapts its types to nanite's Provider interface.
//
// Unlike cloud adapters, Ollama takes no API key: the base URL alone controls
// which daemon we talk to. OLLAMA_HOST overrides the default of
// http://localhost:11434.
type Ollama struct {
	host       string
	httpClient *http.Client
	client     *api.Client // lazily constructed once host is resolved

	Retry          RetryConfig
	OnStatus       StatusCallback
	CircuitBreaker *CircuitBreaker
	OnCircuitOpen  func()
	RateTracker    *TokenRateTracker
}

// NewOllama creates a new Ollama provider. The host is read from OLLAMA_HOST
// (defaulting to http://localhost:11434); the SDK client is lazily constructed.
func NewOllama() *Ollama {
	host := os.Getenv("OLLAMA_HOST")
	if host == "" {
		host = "http://localhost:11434"
	}
	return &Ollama{
		host:           strings.TrimRight(host, "/"),
		httpClient:     &http.Client{},
		Retry:          DefaultRetryConfig(),
		CircuitBreaker: NewCircuitBreaker(3),
		RateTracker:    NewTokenRateTracker(30000),
	}
}

// ensureClient lazily constructs the SDK client from the resolved host URL.
// The SDK has no configurable retry to disable — its transport is a plain
// http.Client with no built-in retry, so our decorator chain owns retry policy
// unambiguously.
func (o *Ollama) ensureClient() error {
	if o.client != nil {
		return nil
	}
	base, err := parseOllamaHost(o.host)
	if err != nil {
		return fmt.Errorf("parse OLLAMA_HOST %q: %w", o.host, err)
	}
	o.client = api.NewClient(base, o.httpClient)
	return nil
}

// parseOllamaHost normalises a user-provided host into a *url.URL. Accepts
// bare "host:port" or full URLs with scheme.
func parseOllamaHost(h string) (*url.URL, error) {
	if h == "" {
		h = "http://localhost:11434"
	}
	if !strings.Contains(h, "://") {
		h = "http://" + h
	}
	u, err := url.Parse(h)
	if err != nil {
		return nil, err
	}
	if u.Host == "" {
		return nil, fmt.Errorf("missing host in %q", h)
	}
	return u, nil
}

// -----------------------------------------------------------------------------
// SDK request builders — translate nanite's types into ollama/api params.
// -----------------------------------------------------------------------------

// buildSDKMessages prepends the system prompt (if any) and maps nanite
// ChatMessages onto api.Message values.
func buildOllamaMessages(systemPrompt string, messages []ChatMessage) []api.Message {
	out := make([]api.Message, 0, len(messages)+1)
	if systemPrompt != "" {
		out = append(out, api.Message{Role: "system", Content: systemPrompt})
	}
	for _, m := range messages {
		msg := api.Message{Role: m.Role}
		switch {
		case len(m.ContentBlocks) > 0:
			// Flatten blocks: text → content, tool_use → ToolCalls,
			// tool_result → a user "tool" message. Ollama's API is simpler
			// than Anthropic's content-block model; we do a best-effort
			// mapping so nanite's orchestration layer keeps working.
			var text strings.Builder
			for _, b := range m.ContentBlocks {
				switch b.Type {
				case "text":
					if text.Len() > 0 {
						text.WriteString("\n")
					}
					text.WriteString(b.Text)
				case "tool_use":
					var args api.ToolCallFunctionArguments
					if b.Input != nil {
						// Round-trip through JSON into the ordered-map
						// argument type the SDK expects.
						if raw, err := json.Marshal(*b.Input); err == nil {
							_ = json.Unmarshal(raw, &args)
						}
					}
					msg.ToolCalls = append(msg.ToolCalls, api.ToolCall{
						ID: b.ID,
						Function: api.ToolCallFunction{
							Name:      b.Name,
							Arguments: args,
						},
					})
				case "tool_result":
					// Ollama represents tool results as a "tool" role message
					// referencing the originating call. Emit a separate
					// message so the original assistant message remains clean.
					out = append(out, api.Message{
						Role:       "tool",
						Content:    b.Content,
						ToolCallID: b.ToolUseID,
					})
				}
			}
			msg.Content = text.String()
		default:
			msg.Content = m.Content
		}
		// Skip empty assistant/user messages that only contributed tool_result
		// (already emitted above) to avoid sending a bare empty turn.
		if msg.Content == "" && len(msg.ToolCalls) == 0 {
			continue
		}
		out = append(out, msg)
	}
	return out
}

// buildSDKTools maps nanite ToolDefinitions onto api.Tools. The SDK uses a
// typed ToolFunctionParameters with an ordered properties map; we round-trip
// nanite's free-form InputSchema through JSON to avoid reimplementing the
// type conversion.
func buildOllamaTools(tools []ToolDefinition) api.Tools {
	if len(tools) == 0 {
		return nil
	}
	out := make(api.Tools, 0, len(tools))
	for _, t := range tools {
		tool := api.Tool{
			Type: "function",
			Function: api.ToolFunction{
				Name:        t.Name,
				Description: t.Description,
			},
		}
		if len(t.InputSchema) > 0 {
			if raw, err := json.Marshal(t.InputSchema); err == nil {
				_ = json.Unmarshal(raw, &tool.Function.Parameters)
			}
		}
		out = append(out, tool)
	}
	return out
}

// -----------------------------------------------------------------------------
// Provider interface
// -----------------------------------------------------------------------------

// StreamChat implements Provider.StreamChat.
func (o *Ollama) StreamChat(ctx context.Context, systemPrompt string, messages []ChatMessage, model string) (<-chan StreamEvent, error) {
	return o.streamChatInternal(ctx, systemPrompt, messages, model, nil)
}

// StreamChatWithTools implements Provider.StreamChatWithTools. Tool-use is
// dispatched to the model; whether the model honours it depends on the model
// (Ollama surfaces tool_calls in ChatResponse.Message.ToolCalls when the
// model supports it).
func (o *Ollama) StreamChatWithTools(ctx context.Context, systemPrompt string, messages []ChatMessage, model string, tools []ToolDefinition) (<-chan StreamEvent, error) {
	return o.streamChatInternal(ctx, systemPrompt, messages, model, tools)
}

func (o *Ollama) streamChatInternal(ctx context.Context, systemPrompt string, messages []ChatMessage, model string, tools []ToolDefinition) (<-chan StreamEvent, error) {
	ctx, span := feotel.StartSpan(ctx, "nanite.provider.ollama.stream")
	span.SetAttributes(
		attribute.String("nanite.provider", "ollama"),
		attribute.String("nanite.model", model),
		attribute.Int("nanite.messages.count", len(messages)),
		attribute.Int("nanite.tools.count", len(tools)),
	)

	if err := o.ensureClient(); err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.End()
		return nil, err
	}

	if model == "" {
		model = "llama3.1"
	}

	if o.CircuitBreaker != nil && o.CircuitBreaker.IsOpen() {
		span.SetStatus(codes.Error, "circuit breaker open")
		span.End()
		return nil, fmt.Errorf("circuit breaker open: provider rate limited after multiple retries")
	}

	streamTrue := true
	req := &api.ChatRequest{
		Model:    model,
		Messages: buildOllamaMessages(systemPrompt, messages),
		Stream:   &streamTrue,
		Tools:    buildOllamaTools(tools),
	}

	ch := make(chan StreamEvent, 64)
	safego.Go(ctx, "provider.ollama.bridgeStream", func() {
		o.bridgeStream(ctx, req, ch, span)
	})
	return ch, nil
}

// bridgeStream runs the SDK's streaming Chat call and fans each ChatResponse
// callback into nanite StreamEvents. Ollama's SDK uses a callback-per-chunk
// model (blocking Chat call) rather than a pull iterator.
func (o *Ollama) bridgeStream(ctx context.Context, req *api.ChatRequest, ch chan<- StreamEvent, span trace.Span) {
	var totalInput, totalOutput int
	toolAccumulator := make(map[string]*ollamaToolAccumulator)

	defer func() {
		close(ch)
		if span != nil {
			span.SetAttributes(
				attribute.Int("nanite.provider.input_tokens", totalInput),
				attribute.Int("nanite.provider.output_tokens", totalOutput),
			)
			span.End()
		}
	}()

	sendEvent := func(ev StreamEvent) bool {
		select {
		case <-ctx.Done():
			return false
		case ch <- ev:
			return true
		}
	}

	// Callback invoked by the SDK for each streamed chunk.
	cb := func(resp api.ChatResponse) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Text delta.
		if resp.Message.Content != "" {
			if !sendEvent(StreamEvent{Type: "delta", Content: resp.Message.Content}) {
				return context.Canceled
			}
		}

		// Tool calls. Ollama can emit multiple tool_calls in one chunk or
		// split across chunks; identify by ID (or synthesised key) to
		// dedupe across chunks.
		for i, tc := range resp.Message.ToolCalls {
			key := tc.ID
			if key == "" {
				key = fmt.Sprintf("%s#%d", tc.Function.Name, i)
			}
			if _, seen := toolAccumulator[key]; seen {
				// Already emitted this call in a prior chunk.
				continue
			}
			toolAccumulator[key] = &ollamaToolAccumulator{}
			input := tc.Function.Arguments.ToMap()
			if input == nil {
				input = map[string]any{}
			}
			if !sendEvent(StreamEvent{Type: "tool_use", ToolUse: &ToolUseBlock{
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: input,
			}}) {
				return context.Canceled
			}
		}

		if resp.Done {
			in := resp.PromptEvalCount
			outTok := resp.EvalCount
			totalInput += in
			totalOutput += outTok
			if o.RateTracker != nil && in > 0 {
				o.RateTracker.Record(in)
			}
			stopReason := resp.DoneReason
			if stopReason == "" {
				stopReason = "end_turn"
			}
			if !sendEvent(StreamEvent{Type: "usage", Usage: &Usage{
				InputTokens:  in,
				OutputTokens: outTok,
				StopReason:   stopReason,
			}}) {
				return context.Canceled
			}
			if !sendEvent(StreamEvent{Type: "done"}) {
				return context.Canceled
			}
		}
		return nil
	}

	// Retry loop at the request level. For Ollama-local this typically only
	// covers transient 5xx from a reloading daemon; the Chat call blocks for
	// the entire stream, so any mid-stream error aborts retries.
	var lastErr error
	for attempt := 0; attempt <= o.Retry.MaxRetries; attempt++ {
		err := o.client.Chat(ctx, req, cb)
		if err == nil {
			if o.CircuitBreaker != nil {
				o.CircuitBreaker.RecordSuccess()
			}
			return
		}
		if isStreamClosedErr(err) {
			// Benign termination — don't emit an error event.
			return
		}
		apiErr, retryAfter := classifyOllamaError(err)
		if !RetryableStatusCode(apiErr.StatusCode) || attempt == o.Retry.MaxRetries {
			if o.CircuitBreaker != nil && attempt == o.Retry.MaxRetries {
				if tripped := o.CircuitBreaker.RecordFailure(); tripped {
					slog.Warn("provider: circuit breaker tripped after consecutive failures", "provider", "ollama")
					if o.OnCircuitOpen != nil {
						o.OnCircuitOpen()
					}
				}
			}
			span.RecordError(apiErr)
			span.SetAttributes(attribute.Int("nanite.http.status", apiErr.StatusCode))
			sendEvent(StreamEvent{Type: "error", Error: apiErr.Error()})
			return
		}
		delay := o.Retry.BackoffDelay(attempt, retryAfter)
		slog.Info("provider: retryable error, retrying",
			"provider", "ollama",
			"status", apiErr.StatusCode,
			"attempt", attempt+1,
			"max_attempts", o.Retry.MaxRetries,
			"delay", delay.String(),
		)
		if o.OnStatus != nil {
			o.OnStatus(fmt.Sprintf("Ollama transient error, retrying in %s... (attempt %d/%d)",
				delay.Round(time.Millisecond), attempt+1, o.Retry.MaxRetries))
		}
		select {
		case <-ctx.Done():
			sendEvent(StreamEvent{Type: "error", Error: ctx.Err().Error()})
			return
		case <-time.After(delay):
		}
		lastErr = apiErr
	}
	_ = lastErr
}

// Complete makes a non-streaming completion call.
func (o *Ollama) Complete(ctx context.Context, systemPrompt string, messages []ChatMessage, model string) (string, error) {
	ctx, span := feotel.StartSpan(ctx, "nanite.provider.ollama.complete")
	defer span.End()
	span.SetAttributes(
		attribute.String("nanite.provider", "ollama"),
		attribute.String("nanite.model", model),
		attribute.Int("nanite.messages.count", len(messages)),
	)

	if err := o.ensureClient(); err != nil {
		span.SetStatus(codes.Error, err.Error())
		return "", err
	}

	if model == "" {
		model = "llama3.1"
	}

	streamFalse := false
	req := &api.ChatRequest{
		Model:    model,
		Messages: buildOllamaMessages(systemPrompt, messages),
		Stream:   &streamFalse,
	}

	var final api.ChatResponse
	cb := func(resp api.ChatResponse) error {
		// With Stream=false the SDK still streams one payload through the
		// callback; capture the final one.
		final = resp
		return nil
	}

	for attempt := 0; attempt <= o.Retry.MaxRetries; attempt++ {
		err := o.client.Chat(ctx, req, cb)
		if err == nil {
			return strings.TrimSpace(final.Message.Content), nil
		}
		apiErr, retryAfter := classifyOllamaError(err)
		if !RetryableStatusCode(apiErr.StatusCode) || attempt == o.Retry.MaxRetries {
			return "", apiErr
		}
		delay := o.Retry.BackoffDelay(attempt, retryAfter)
		slog.Info("provider: retryable error, retrying",
			"provider", "ollama",
			"status", apiErr.StatusCode,
			"attempt", attempt+1,
			"max_attempts", o.Retry.MaxRetries,
			"delay", delay.String(),
		)
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("context cancelled during retry: %w", ctx.Err())
		case <-time.After(delay):
		}
	}
	return "", fmt.Errorf("ollama: retries exhausted")
}

// Capabilities returns the capabilities supported by the Ollama provider.
// Tool-use depends on the selected model; we still report false because many
// popular Ollama models (llama3.1 base, mistral, etc.) do not honour tool
// calls and the orchestrator should treat Ollama as non-tool-capable by
// default. The SDK *does* forward tool_calls when a tool-capable model
// returns them — see streamChatInternal.
func (o *Ollama) Capabilities() ProviderCapabilities {
	return capabilitiesFromRegistry("ollama")
}

// -----------------------------------------------------------------------------
// Embedder
// -----------------------------------------------------------------------------

// Embed generates an embedding vector for a single text input.
func (o *Ollama) Embed(ctx context.Context, text string, model string) (*EmbeddingResult, error) {
	results, err := o.EmbedBatch(ctx, []string{text}, model)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("ollama: empty embedding response")
	}
	return &results[0], nil
}

// EmbedBatch generates embedding vectors for multiple texts in a single call.
func (o *Ollama) EmbedBatch(ctx context.Context, texts []string, model string) ([]EmbeddingResult, error) {
	if err := o.ensureClient(); err != nil {
		return nil, err
	}
	if model == "" {
		model = "nomic-embed-text"
	}

	req := &api.EmbedRequest{
		Model: model,
		Input: texts,
	}

	resp, err := o.client.Embed(ctx, req)
	if err != nil {
		apiErr, _ := classifyOllamaError(err)
		return nil, apiErr
	}

	if len(resp.Embeddings) != len(texts) {
		return nil, fmt.Errorf("expected %d embeddings, got %d", len(texts), len(resp.Embeddings))
	}

	// Ollama returns only the aggregate prompt_eval_count; split evenly.
	perText := 0
	if len(texts) > 0 {
		perText = resp.PromptEvalCount / len(texts)
	}

	results := make([]EmbeddingResult, len(resp.Embeddings))
	for i, emb := range resp.Embeddings {
		// SDK already returns []float32 — copy to detach from SDK-owned slice.
		cpy := make([]float32, len(emb))
		copy(cpy, emb)
		results[i] = EmbeddingResult{
			Embedding:  cpy,
			TokenCount: perText,
		}
	}
	return results, nil
}

// EmbeddingDimensions returns the output dimensions for the given embedding
// model. Returns 0 if the model is unknown.
func (o *Ollama) EmbeddingDimensions(model string) int {
	return models.EmbeddingDimensionsFor(model)
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

// ollamaToolAccumulator tracks whether we've already emitted a given tool_use
// block (keyed by ID) to avoid duplicate events across streaming chunks.
type ollamaToolAccumulator struct{}

// classifyOllamaError maps an SDK error to an APIError. The SDK exposes
// api.StatusError with a StatusCode; anything else is wrapped as status 0
// (unknown), which RetryableStatusCode treats as non-retryable.
//
// The error message is capped at maxOllamaErrBody to guard against a
// hostile/oversized error body; the SDK buffers error bodies via io.ReadAll
// without a cap. See ADAPTER_PATTERN.md §8.
func classifyOllamaError(err error) (*APIError, time.Duration) {
	if err == nil {
		return nil, 0
	}
	var statusErr api.StatusError
	if errors.As(err, &statusErr) {
		msg := statusErr.ErrorMessage
		if msg == "" {
			msg = statusErr.Status
		}
		if len(msg) > maxOllamaErrBody {
			msg = msg[:maxOllamaErrBody]
		}
		return &APIError{
			StatusCode: statusErr.StatusCode,
			Message:    msg,
		}, 0
	}
	var authErr api.AuthorizationError
	if errors.As(err, &authErr) {
		return &APIError{
			StatusCode: authErr.StatusCode,
			Message:    authErr.Status,
		}, 0
	}
	return &APIError{StatusCode: 0, Message: err.Error()}, 0
}
