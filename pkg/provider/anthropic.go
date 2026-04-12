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

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	feotel "github.com/hollis-labs/go-otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/hollis-labs/nanite/internal/safego"
	"github.com/hollis-labs/nanite/pkg/models"
)

// maxAnthropicErrBody caps forwarded API-error response bytes.
// The official SDK does NOT apply a body limit — see ADAPTER_PATTERN.md.
// We cap what we forward into APIError.Message ourselves.
const maxAnthropicErrBody = 1 << 20 // 1 MiB

// Anthropic implements the Provider and CacheableProvider interfaces for the
// Anthropic Messages API. The underlying transport is the official
// anthropic-sdk-go client; this type adapts its types to nanite's Provider
// interface (StreamEvent, ToolDefinition, ChatMessage, Usage).
type Anthropic struct {
	apiKey         string
	httpClient     *http.Client
	client         *anthropic.Client // lazily constructed when apiKey is set
	Retry          RetryConfig
	OnStatus       StatusCallback
	CircuitBreaker *CircuitBreaker
	OnCircuitOpen  func()
	RateTracker    *TokenRateTracker
	cacheHints     []CacheHint
}

// NewAnthropic creates a new Anthropic provider. API key is injected later via
// SetAPIKey (see pkg/provider/api_key.go). SDK client construction is deferred
// until the key is present.
func NewAnthropic() *Anthropic {
	return &Anthropic{
		httpClient:     &http.Client{},
		Retry:          DefaultRetryConfig(),
		CircuitBreaker: NewCircuitBreaker(3),
		RateTracker:    NewTokenRateTracker(30000),
	}
}

// ensureClient lazily builds the SDK client once an API key is set.
// The SDK's own retry is disabled (WithMaxRetries(0)) because the retry
// decorator layer (retry.go, circuit.go) owns that policy.
func (a *Anthropic) ensureClient() {
	if a.client != nil || a.apiKey == "" {
		return
	}
	c := anthropic.NewClient(
		option.WithAPIKey(a.apiKey),
		option.WithHTTPClient(a.httpClient),
		option.WithMaxRetries(0),
	)
	a.client = &c
}

// SetCacheHints implements CacheableProvider.
func (a *Anthropic) SetCacheHints(hints []CacheHint) { a.cacheHints = hints }

func (a *Anthropic) hasCacheHint(position string) bool {
	for _, h := range a.cacheHints {
		if h.Position == position {
			return true
		}
	}
	return false
}

func (a *Anthropic) recentMessageCacheCount() int {
	n := 0
	for _, h := range a.cacheHints {
		if h.Position == "recent_message" {
			n++
		}
	}
	return n
}

// -----------------------------------------------------------------------------
// Cache-hint shape helpers
//
// These produce map[string]any / []any shapes that the existing unit tests in
// anthropic_test.go and cache_test.go assert against. They are pure mappers —
// the live request path translates cache hints directly onto SDK param types
// (see buildSDKSystem / buildSDKTools / buildSDKMessages below).
// -----------------------------------------------------------------------------

func (a *Anthropic) buildSystemBlocks(systemPrompt string) []map[string]any {
	if systemPrompt == "" {
		return nil
	}
	block := map[string]any{"type": "text", "text": systemPrompt}
	if a.hasCacheHint("system") {
		block["cache_control"] = map[string]string{"type": "ephemeral"}
	}
	return []map[string]any{block}
}

// buildSystemBlocks (package-level) is the legacy static form used by some tests.
// It always applies cache_control for backwards compatibility.
func buildSystemBlocks(systemPrompt string) []map[string]any {
	if systemPrompt == "" {
		return nil
	}
	return []map[string]any{{
		"type":          "text",
		"text":          systemPrompt,
		"cache_control": map[string]string{"type": "ephemeral"},
	}}
}

func (a *Anthropic) buildToolsWithCacheControl(tools []ToolDefinition) []any {
	if len(tools) == 0 {
		return nil
	}
	shouldCache := a.hasCacheHint("tools")
	out := make([]any, len(tools))
	for i, t := range tools {
		entry := map[string]any{
			"name":         t.Name,
			"description":  t.Description,
			"input_schema": t.InputSchema,
		}
		if shouldCache && i == len(tools)-1 {
			entry["cache_control"] = map[string]string{"type": "ephemeral"}
		}
		out[i] = entry
	}
	return out
}

func buildToolsWithCacheControl(tools []ToolDefinition) []any {
	if len(tools) == 0 {
		return nil
	}
	out := make([]any, len(tools))
	for i, t := range tools {
		entry := map[string]any{
			"name":         t.Name,
			"description":  t.Description,
			"input_schema": t.InputSchema,
		}
		if i == len(tools)-1 {
			entry["cache_control"] = map[string]string{"type": "ephemeral"}
		}
		out[i] = entry
	}
	return out
}

func (a *Anthropic) marshalMessages(messages []ChatMessage) []any {
	return marshalMessagesWithCacheCount(messages, a.recentMessageCacheCount())
}

func marshalMessages(messages []ChatMessage) []any {
	return marshalMessagesWithCacheCount(messages, 2)
}

func marshalMessagesWithCacheCount(messages []ChatMessage, cacheCount int) []any {
	userIndices := make([]int, 0, cacheCount)
	for i := len(messages) - 1; i >= 0 && len(userIndices) < cacheCount; i-- {
		if messages[i].Role == "user" {
			userIndices = append(userIndices, i)
		}
	}
	cacheSet := make(map[int]bool, len(userIndices))
	for _, idx := range userIndices {
		cacheSet[idx] = true
	}

	out := make([]any, len(messages))
	for i, m := range messages {
		shouldCache := cacheSet[i]

		if len(m.ContentBlocks) > 0 {
			if shouldCache {
				blocks := make([]map[string]any, len(m.ContentBlocks))
				for j, b := range m.ContentBlocks {
					block := map[string]any{"type": b.Type}
					if b.Text != "" {
						block["text"] = b.Text
					}
					if b.ID != "" {
						block["id"] = b.ID
					}
					if b.Name != "" {
						block["name"] = b.Name
					}
					if b.Input != nil {
						block["input"] = b.Input
					}
					if b.ToolUseID != "" {
						block["tool_use_id"] = b.ToolUseID
					}
					if b.Content != "" {
						block["content"] = b.Content
					}
					if b.IsError {
						block["is_error"] = true
					}
					if j == len(m.ContentBlocks)-1 {
						block["cache_control"] = map[string]string{"type": "ephemeral"}
					}
					blocks[j] = block
				}
				out[i] = map[string]any{"role": m.Role, "content": blocks}
			} else {
				out[i] = map[string]any{"role": m.Role, "content": m.ContentBlocks}
			}
			continue
		}

		if shouldCache {
			out[i] = map[string]any{
				"role": m.Role,
				"content": []map[string]any{{
					"type":          "text",
					"text":          m.Content,
					"cache_control": map[string]string{"type": "ephemeral"},
				}},
			}
		} else {
			out[i] = map[string]any{"role": m.Role, "content": m.Content}
		}
	}
	return out
}

// -----------------------------------------------------------------------------
// SDK request builders — translate nanite's types into anthropic-sdk-go params.
// -----------------------------------------------------------------------------

func (a *Anthropic) buildSDKSystem(systemPrompt string) []anthropic.TextBlockParam {
	if systemPrompt == "" {
		return nil
	}
	b := anthropic.TextBlockParam{Text: systemPrompt}
	if a.hasCacheHint("system") {
		b.CacheControl = ephemeralCache()
	}
	return []anthropic.TextBlockParam{b}
}

func (a *Anthropic) buildSDKTools(tools []ToolDefinition) []anthropic.ToolUnionParam {
	if len(tools) == 0 {
		return nil
	}
	shouldCache := a.hasCacheHint("tools")
	out := make([]anthropic.ToolUnionParam, len(tools))
	for i, t := range tools {
		tp := anthropic.ToolParam{
			Name:        t.Name,
			InputSchema: anthropic.ToolInputSchemaParam{Properties: t.InputSchema},
		}
		if t.Description != "" {
			tp.Description = param.NewOpt(t.Description)
		}
		if shouldCache && i == len(tools)-1 {
			tp.CacheControl = ephemeralCache()
		}
		out[i] = anthropic.ToolUnionParam{OfTool: &tp}
	}
	return out
}

func (a *Anthropic) buildSDKMessages(messages []ChatMessage) []anthropic.MessageParam {
	cacheCount := a.recentMessageCacheCount()
	userIndices := make(map[int]bool, cacheCount)
	found := 0
	for i := len(messages) - 1; i >= 0 && found < cacheCount; i-- {
		if messages[i].Role == "user" {
			userIndices[i] = true
			found++
		}
	}

	out := make([]anthropic.MessageParam, 0, len(messages))
	for i, m := range messages {
		role := anthropic.MessageParamRoleUser
		if m.Role == "assistant" {
			role = anthropic.MessageParamRoleAssistant
		}

		var blocks []anthropic.ContentBlockParamUnion
		if len(m.ContentBlocks) > 0 {
			blocks = make([]anthropic.ContentBlockParamUnion, 0, len(m.ContentBlocks))
			for j, b := range m.ContentBlocks {
				cb := chatBlockToSDK(b)
				// cache_control on the last block of a cacheable message.
				if userIndices[i] && j == len(m.ContentBlocks)-1 {
					applyCacheControl(&cb)
				}
				blocks = append(blocks, cb)
			}
		} else {
			tb := anthropic.TextBlockParam{Text: m.Content}
			if userIndices[i] {
				tb.CacheControl = ephemeralCache()
			}
			blocks = []anthropic.ContentBlockParamUnion{{OfText: &tb}}
		}

		out = append(out, anthropic.MessageParam{Role: role, Content: blocks})
	}
	return out
}

// chatBlockToSDK maps a nanite ContentBlock to an SDK ContentBlockParamUnion.
func chatBlockToSDK(b ContentBlock) anthropic.ContentBlockParamUnion {
	switch b.Type {
	case "tool_use":
		var input any = map[string]any{}
		if b.Input != nil {
			input = *b.Input
		}
		tu := anthropic.ToolUseBlockParam{
			ID:    b.ID,
			Name:  b.Name,
			Input: input,
		}
		return anthropic.ContentBlockParamUnion{OfToolUse: &tu}
	case "tool_result":
		tr := anthropic.ToolResultBlockParam{ToolUseID: b.ToolUseID}
		if b.IsError {
			tr.IsError = param.NewOpt(true)
		}
		if b.Content != "" {
			tr.Content = []anthropic.ToolResultBlockParamContentUnion{{
				OfText: &anthropic.TextBlockParam{Text: b.Content},
			}}
		}
		return anthropic.ContentBlockParamUnion{OfToolResult: &tr}
	default: // "text" and fallback
		tb := anthropic.TextBlockParam{Text: b.Text}
		return anthropic.ContentBlockParamUnion{OfText: &tb}
	}
}

// applyCacheControl sets cache_control on whichever variant of the union is set.
func applyCacheControl(u *anthropic.ContentBlockParamUnion) {
	cc := ephemeralCache()
	switch {
	case u.OfText != nil:
		u.OfText.CacheControl = cc
	case u.OfToolUse != nil:
		u.OfToolUse.CacheControl = cc
	case u.OfToolResult != nil:
		u.OfToolResult.CacheControl = cc
	}
}

// -----------------------------------------------------------------------------
// Provider interface
// -----------------------------------------------------------------------------

func (a *Anthropic) StreamChat(ctx context.Context, systemPrompt string, messages []ChatMessage, model string) (<-chan StreamEvent, error) {
	return a.streamChatInternal(ctx, systemPrompt, messages, model, nil)
}

func (a *Anthropic) StreamChatWithTools(ctx context.Context, systemPrompt string, messages []ChatMessage, model string, tools []ToolDefinition) (<-chan StreamEvent, error) {
	return a.streamChatInternal(ctx, systemPrompt, messages, model, tools)
}

func (a *Anthropic) streamChatInternal(ctx context.Context, systemPrompt string, messages []ChatMessage, model string, tools []ToolDefinition) (<-chan StreamEvent, error) {
	ctx, span := feotel.StartSpan(ctx, "nanite.provider.anthropic.stream")
	span.SetAttributes(
		attribute.String("nanite.provider", "anthropic"),
		attribute.String("nanite.model", model),
		attribute.Int("nanite.messages.count", len(messages)),
		attribute.Int("nanite.tools.count", len(tools)),
	)

	if a.apiKey == "" {
		span.SetStatus(codes.Error, "ANTHROPIC_API_KEY not set")
		span.End()
		return nil, fmt.Errorf("ANTHROPIC_API_KEY not set")
	}
	a.ensureClient()

	if model == "" {
		model = models.DefaultChatModel()
	}

	if a.CircuitBreaker != nil && a.CircuitBreaker.IsOpen() {
		span.SetStatus(codes.Error, "circuit breaker open")
		span.End()
		return nil, fmt.Errorf("circuit breaker open: provider rate limited after multiple retries")
	}

	// Per-model max-output cap sourced from the registry. Prior versions
	// hardcoded 16384 here which silently truncated Opus (32000) and other
	// longer-output models — see audit 2026-04-11 finding 04.
	maxOut := models.MaxOutputFor(model)
	if maxOut <= 0 {
		maxOut = 16384 // provider-level historical default
	}
	params := anthropic.MessageNewParams{
		Model:     model,
		MaxTokens: int64(maxOut),
		System:    a.buildSDKSystem(systemPrompt),
		Messages:  a.buildSDKMessages(messages),
	}
	if len(tools) > 0 {
		params.Tools = a.buildSDKTools(tools)
	}

	// Rate-limit pacing. Estimate is coarse; SDK marshals the body itself so
	// we don't have the exact byte count, but this preserves previous behavior.
	if a.RateTracker != nil {
		estimatedTokens := estimatePromptTokens(systemPrompt, messages)
		if wait := a.RateTracker.WaitTime(estimatedTokens); wait > 0 {
			avail, limit := a.RateTracker.Remaining()
			if estimatedTokens > limit {
				log.Printf("provider: request ~%d tokens exceeds per-minute rate limit %d, proceeding anyway", estimatedTokens, limit)
			}
			if a.OnStatus != nil {
				a.OnStatus(fmt.Sprintf("Waiting %ds for rate limit budget...", int(wait.Seconds()+0.5)))
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
	var stream *anthropicStreamHandle
	var lastErr error
	for attempt := 0; attempt <= a.Retry.MaxRetries; attempt++ {
		// Use option.WithHeader for cache-control header if needed (prompt caching).
		// The beta header is no longer required for the stable prompt-caching feature,
		// but we send it for compatibility with older model versions.
		s := a.client.Messages.NewStreaming(ctx, params,
			option.WithHeader("anthropic-beta", "prompt-caching-2024-07-31"),
		)

		// The SDK defers HTTP work until Next() is called. Peek the first event
		// so retry-on-start-error still works.
		handle, apiErr, retryAfter := peekStream(s)
		if apiErr == nil {
			stream = handle
			if a.CircuitBreaker != nil {
				a.CircuitBreaker.RecordSuccess()
			}
			break
		}

		if !RetryableStatusCode(apiErr.StatusCode) || attempt == a.Retry.MaxRetries {
			if a.CircuitBreaker != nil && attempt == a.Retry.MaxRetries {
				if tripped := a.CircuitBreaker.RecordFailure(); tripped {
					log.Printf("provider: circuit breaker tripped after consecutive failures")
					if a.OnCircuitOpen != nil {
						a.OnCircuitOpen()
					}
				}
			}
			span.RecordError(apiErr)
			span.SetStatus(codes.Error, apiErr.Error())
			span.SetAttributes(attribute.Int("nanite.http.status", apiErr.StatusCode))
			span.End()
			return nil, apiErr
		}

		delay := a.Retry.BackoffDelay(attempt, retryAfter)
		log.Printf("provider: retryable error %d (attempt %d/%d), retrying in %s",
			apiErr.StatusCode, attempt+1, a.Retry.MaxRetries, delay)
		if a.OnStatus != nil {
			a.OnStatus(fmt.Sprintf("Rate limited, retrying in %s... (attempt %d/%d)",
				delay.Round(time.Millisecond), attempt+1, a.Retry.MaxRetries))
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
	safego.Go(ctx, "provider.anthropic.bridgeStream", func() {
		a.bridgeStream(ctx, stream, ch, span)
	})
	return ch, nil
}

// anthropicStreamHandle carries the SDK stream plus the peeked first event.
type anthropicStreamHandle struct {
	stream *ssestreamHandle
}

// ssestreamHandle is a local alias to avoid generic mess in callers.
type ssestreamHandle = struct {
	underlying anthropicStreamIface
	primed     bool
	primedOK   bool
}

// anthropicStreamIface is the subset of ssestream.Stream we use; defining it as
// an interface keeps the bridge test-friendly.
type anthropicStreamIface interface {
	Next() bool
	Current() anthropic.MessageStreamEventUnion
	Err() error
	Close() error
}

// peekStream does Next() once to surface start-time API errors so the retry
// loop can catch them. On success it returns a handle carrying the primed state.
func peekStream(s anthropicSDKStream) (*anthropicStreamHandle, *APIError, time.Duration) {
	adapter := &sdkStreamAdapter{s: s}
	primedOK := adapter.Next()
	if !primedOK {
		if err := adapter.Err(); err != nil {
			return nil, classifyAnthropicError(err), parseRetryAfterFromErr(err)
		}
		// Empty stream — treat as success; bridge will close immediately.
	}
	return &anthropicStreamHandle{stream: &ssestreamHandle{
		underlying: adapter,
		primed:     true,
		primedOK:   primedOK,
	}}, nil, 0
}

// anthropicSDKStream is the concrete generic stream type from the SDK.
type anthropicSDKStream interface {
	Next() bool
	Current() anthropic.MessageStreamEventUnion
	Err() error
	Close() error
}

type sdkStreamAdapter struct {
	s anthropicSDKStream
}

func (a *sdkStreamAdapter) Next() bool                                  { return a.s.Next() }
func (a *sdkStreamAdapter) Current() anthropic.MessageStreamEventUnion  { return a.s.Current() }
func (a *sdkStreamAdapter) Err() error                                  { return a.s.Err() }
func (a *sdkStreamAdapter) Close() error                                { return a.s.Close() }

// bridgeStream pumps SDK MessageStreamEventUnion values into nanite StreamEvents.
func (a *Anthropic) bridgeStream(ctx context.Context, handle *anthropicStreamHandle, ch chan<- StreamEvent, span trace.Span) {
	var totalInput, totalOutput int
	defer func() {
		if handle != nil && handle.stream != nil && handle.stream.underlying != nil {
			_ = handle.stream.underlying.Close()
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
	s := handle.stream.underlying
	toolAcc := map[int64]*toolUseAccumulator{}

	step := func(ev anthropic.MessageStreamEventUnion) bool {
		select {
		case <-ctx.Done():
			ch <- StreamEvent{Type: "error", Error: "context cancelled"}
			return false
		default:
		}
		switch variant := ev.AsAny().(type) {
		case anthropic.MessageStartEvent:
			u := variant.Message.Usage
			if u.CacheCreationInputTokens > 0 || u.CacheReadInputTokens > 0 {
				log.Printf("provider: prompt cache — creation=%d read=%d input=%d",
					u.CacheCreationInputTokens, u.CacheReadInputTokens, u.InputTokens)
			}
			in := int(u.InputTokens)
			totalInput += in
			if a.RateTracker != nil && in > 0 {
				a.RateTracker.Record(in)
				avail, limit := a.RateTracker.Remaining()
				log.Printf("provider: recorded %d input tokens (rate budget: %d/%d)", in, avail, limit)
			}
			ch <- StreamEvent{Type: "usage", Usage: &Usage{
				InputTokens:         in,
				CacheCreationTokens: int(u.CacheCreationInputTokens),
				CacheReadTokens:     int(u.CacheReadInputTokens),
			}}
		case anthropic.ContentBlockStartEvent:
			block := variant.ContentBlock
			if block.Type == "tool_use" {
				toolAcc[variant.Index] = &toolUseAccumulator{id: block.ID, name: block.Name}
			}
		case anthropic.ContentBlockDeltaEvent:
			switch variant.Delta.Type {
			case "text_delta":
				ch <- StreamEvent{Type: "delta", Content: variant.Delta.Text}
			case "input_json_delta":
				if acc, ok := toolAcc[variant.Index]; ok {
					acc.inputJSON.WriteString(variant.Delta.PartialJSON)
				}
			}
		case anthropic.ContentBlockStopEvent:
			if acc, ok := toolAcc[variant.Index]; ok {
				ch <- StreamEvent{Type: "tool_use", ToolUse: acc.finalize()}
				delete(toolAcc, variant.Index)
			}
		case anthropic.MessageDeltaEvent:
			totalOutput += int(variant.Usage.OutputTokens)
			ch <- StreamEvent{Type: "usage", Usage: &Usage{
				OutputTokens: int(variant.Usage.OutputTokens),
				StopReason:   string(variant.Delta.StopReason),
			}}
		case anthropic.MessageStopEvent:
			ch <- StreamEvent{Type: "done"}
		}
		return true
	}

	// Process the primed event (if peekStream got one).
	if handle.stream.primed && handle.stream.primedOK {
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
	}
}

// Complete makes a non-streaming completion call.
func (a *Anthropic) Complete(ctx context.Context, systemPrompt string, messages []ChatMessage, model string) (string, error) {
	ctx, span := feotel.StartSpan(ctx, "nanite.provider.anthropic.complete")
	defer span.End()
	span.SetAttributes(
		attribute.String("nanite.provider", "anthropic"),
		attribute.String("nanite.model", model),
		attribute.Int("nanite.messages.count", len(messages)),
	)

	if a.apiKey == "" {
		span.SetStatus(codes.Error, "ANTHROPIC_API_KEY not set")
		return "", fmt.Errorf("ANTHROPIC_API_KEY not set")
	}
	a.ensureClient()

	if model == "" {
		model = models.DefaultChatModel()
	}

	params := anthropic.MessageNewParams{
		Model:     model,
		MaxTokens: 128,
		System:    a.buildSDKSystem(systemPrompt),
		Messages:  a.buildSDKMessages(messages),
	}

	var resp *anthropic.Message
	for attempt := 0; attempt <= a.Retry.MaxRetries; attempt++ {
		var err error
		resp, err = a.client.Messages.New(ctx, params,
			option.WithHeader("anthropic-beta", "prompt-caching-2024-07-31"),
		)
		if err == nil {
			break
		}
		apiErr := classifyAnthropicError(err)
		if !RetryableStatusCode(apiErr.StatusCode) || attempt == a.Retry.MaxRetries {
			return "", apiErr
		}
		delay := a.Retry.BackoffDelay(attempt, parseRetryAfterFromErr(err))
		log.Printf("provider: retryable error %d (attempt %d/%d), retrying in %s",
			apiErr.StatusCode, attempt+1, a.Retry.MaxRetries, delay)
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("context cancelled during retry: %w", ctx.Err())
		case <-time.After(delay):
		}
	}

	for _, block := range resp.Content {
		if block.Type == "text" {
			return strings.TrimSpace(block.Text), nil
		}
	}
	return "", nil
}

// Capabilities returns the capabilities supported by the Anthropic provider.
// Per-provider defaults come from pkg/models.ProviderDefaults so token
// limits and pricing stay co-located with the model catalog. Per-model
// overrides are available via models.MaxOutputFor / ContextWindowFor.
func (a *Anthropic) Capabilities() ProviderCapabilities {
	return capabilitiesFromRegistry("anthropic")
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

// toolUseAccumulator tracks state for an in-progress tool_use content block.
type toolUseAccumulator struct {
	id        string
	name      string
	inputJSON strings.Builder
}

func (acc *toolUseAccumulator) finalize() *ToolUseBlock {
	raw := acc.inputJSON.String()
	var input map[string]any
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &input); err != nil {
			input = map[string]any{"_raw": raw}
		}
	} else {
		input = map[string]any{}
	}
	return &ToolUseBlock{ID: acc.id, Name: acc.name, Input: input}
}

// classifyAnthropicError maps an SDK error to an APIError. The SDK's typed
// Error carries StatusCode; anything else is wrapped as status 0 (unknown),
// which RetryableStatusCode treats as non-retryable.
func classifyAnthropicError(err error) *APIError {
	if err == nil {
		return nil
	}
	var sdkErr *anthropic.Error
	if errors.As(err, &sdkErr) {
		raw := sdkErr.RawJSON()
		if len(raw) > maxAnthropicErrBody {
			raw = raw[:maxAnthropicErrBody]
		}
		return &APIError{
			StatusCode: sdkErr.StatusCode,
			Message:    raw,
			RetryAfter: parseRetryAfterFromErr(err),
		}
	}
	return &APIError{StatusCode: 0, Message: err.Error()}
}

// parseRetryAfterFromErr extracts Retry-After from the SDK error's Response.
func parseRetryAfterFromErr(err error) time.Duration {
	var sdkErr *anthropic.Error
	if errors.As(err, &sdkErr) && sdkErr.Response != nil {
		return ParseRetryAfter(sdkErr.Response.Header.Get("Retry-After"))
	}
	return 0
}

// isStreamClosedErr returns true when the error is a benign stream termination
// (context cancelled while we were already exiting, or an io.EOF on graceful close).
func isStreamClosedErr(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	return false
}

// ephemeralCache returns a non-zero CacheControlEphemeralParam. The zero value
// is omitted at marshal time (paramObj "omitzero" semantics), so we must set at
// least one field; TTL=5m is the API default.
func ephemeralCache() anthropic.CacheControlEphemeralParam {
	return anthropic.CacheControlEphemeralParam{TTL: anthropic.CacheControlEphemeralTTLTTL5m}
}

// estimatePromptTokens is a coarse token estimator used for rate-limit pacing
// when we no longer marshal the body ourselves. ~4 chars per token.
func estimatePromptTokens(systemPrompt string, messages []ChatMessage) int {
	total := len(systemPrompt)
	for _, m := range messages {
		total += len(m.Content)
		for _, b := range m.ContentBlocks {
			total += len(b.Text) + len(b.Content)
		}
	}
	return total / 4
}
