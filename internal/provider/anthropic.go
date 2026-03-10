package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	tiamatotel "github.com/hollis-labs/tiamat-otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const anthropicAPI = "https://api.anthropic.com/v1/messages"

// Anthropic implements the Provider interface for the Anthropic Messages API.
type Anthropic struct {
	apiKey         string
	client         *http.Client
	Retry          RetryConfig
	OnStatus       StatusCallback // optional; called during retries to report status
	CircuitBreaker *CircuitBreaker
	OnCircuitOpen  func() // called when the circuit breaker trips
	RateTracker    *TokenRateTracker
}

// NewAnthropic creates a new Anthropic provider. It reads ANTHROPIC_API_KEY from the environment.
func NewAnthropic() *Anthropic {
	return &Anthropic{
		apiKey:         os.Getenv("ANTHROPIC_API_KEY"),
		client:         &http.Client{},
		Retry:          DefaultRetryConfig(),
		CircuitBreaker: NewCircuitBreaker(3),
		RateTracker:    NewTokenRateTracker(30000),
	}
}

// anthropicRequest is the request body for the Anthropic Messages API.
type anthropicRequest struct {
	Model     string `json:"model"`
	MaxTokens int    `json:"max_tokens"`
	System    any    `json:"system,omitempty"`
	Messages  []any  `json:"messages"`
	Stream    bool   `json:"stream"`
	Tools     []any  `json:"tools,omitempty"`
}

// buildSystemBlocks wraps a system prompt in a content block with cache_control ephemeral.
func buildSystemBlocks(systemPrompt string) []map[string]any {
	if systemPrompt == "" {
		return nil
	}
	return []map[string]any{
		{
			"type": "text",
			"text": systemPrompt,
			"cache_control": map[string]string{
				"type": "ephemeral",
			},
		},
	}
}

// buildToolsWithCacheControl converts tool definitions to []any and marks the
// last tool with cache_control ephemeral so Anthropic caches the tool list.
func buildToolsWithCacheControl(tools []ToolDefinition) []any {
	if len(tools) == 0 {
		return nil
	}
	result := make([]any, len(tools))
	for i, t := range tools {
		entry := map[string]any{
			"name":         t.Name,
			"description":  t.Description,
			"input_schema": t.InputSchema,
		}
		if i == len(tools)-1 {
			entry["cache_control"] = map[string]string{"type": "ephemeral"}
		}
		result[i] = entry
	}
	return result
}

// marshalMessages converts ChatMessage slice to the Anthropic API format.
// Simple text messages use {"role": "...", "content": "..."}.
// Multi-block messages use {"role": "...", "content": [...]}.
// The last 2 user messages are marked with cache_control ephemeral for prompt caching.
func marshalMessages(messages []ChatMessage) []any {
	// Find the indices of the last 2 user messages for cache marking.
	userIndices := make([]int, 0, 2)
	for i := len(messages) - 1; i >= 0 && len(userIndices) < 2; i-- {
		if messages[i].Role == "user" {
			userIndices = append(userIndices, i)
		}
	}
	cacheSet := make(map[int]bool, len(userIndices))
	for _, idx := range userIndices {
		cacheSet[idx] = true
	}

	result := make([]any, len(messages))
	for i, m := range messages {
		shouldCache := cacheSet[i]

		if len(m.ContentBlocks) > 0 {
			// Multi-block message (tool results, tool use responses).
			// If caching, add cache_control to the last block.
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
					if j == len(m.ContentBlocks)-1 {
						block["cache_control"] = map[string]string{"type": "ephemeral"}
					}
					blocks[j] = block
				}
				result[i] = map[string]any{
					"role":    m.Role,
					"content": blocks,
				}
			} else {
				result[i] = map[string]any{
					"role":    m.Role,
					"content": m.ContentBlocks,
				}
			}
		} else {
			// Simple text message.
			if shouldCache {
				result[i] = map[string]any{
					"role": m.Role,
					"content": []map[string]any{
						{
							"type": "text",
							"text": m.Content,
							"cache_control": map[string]string{
								"type": "ephemeral",
							},
						},
					},
				}
			} else {
				result[i] = map[string]any{
					"role":    m.Role,
					"content": m.Content,
				}
			}
		}
	}
	return result
}

// StreamChat implements Provider.StreamChat using Anthropic's streaming SSE API.
func (a *Anthropic) StreamChat(ctx context.Context, systemPrompt string, messages []ChatMessage, model string) (<-chan StreamEvent, error) {
	return a.streamChatInternal(ctx, systemPrompt, messages, model, nil)
}

// StreamChatWithTools implements Provider.StreamChatWithTools using Anthropic's streaming SSE API with tool definitions.
func (a *Anthropic) StreamChatWithTools(ctx context.Context, systemPrompt string, messages []ChatMessage, model string, tools []ToolDefinition) (<-chan StreamEvent, error) {
	return a.streamChatInternal(ctx, systemPrompt, messages, model, tools)
}

// streamChatInternal is the shared implementation for StreamChat and StreamChatWithTools.
func (a *Anthropic) streamChatInternal(ctx context.Context, systemPrompt string, messages []ChatMessage, model string, tools []ToolDefinition) (<-chan StreamEvent, error) {
	ctx, span := tiamatotel.StartSpan(ctx, "mentat-chat.provider.anthropic.stream")
	span.SetAttributes(
		attribute.String("mentat.provider", "anthropic"),
		attribute.String("mentat.model", model),
		attribute.Int("mentat.messages.count", len(messages)),
		attribute.Int("mentat.tools.count", len(tools)),
	)

	if a.apiKey == "" {
		span.SetStatus(codes.Error, "ANTHROPIC_API_KEY not set")
		span.End()
		return nil, fmt.Errorf("ANTHROPIC_API_KEY not set")
	}

	if model == "" {
		model = "claude-sonnet-4-20250514"
	}

	body := anthropicRequest{
		Model:     model,
		MaxTokens: 16384,
		System:    buildSystemBlocks(systemPrompt),
		Messages:  marshalMessages(messages),
		Stream:    true,
	}
	if len(tools) > 0 {
		body.Tools = buildToolsWithCacheControl(tools)
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// Check if the circuit breaker is open before attempting.
	if a.CircuitBreaker != nil && a.CircuitBreaker.IsOpen() {
		span.SetStatus(codes.Error, "circuit breaker open")
		span.End()
		return nil, fmt.Errorf("circuit breaker open: provider rate limited after multiple retries")
	}

	requestStart := time.Now()

	// Rate-limit pacing: estimate request tokens and wait if necessary.
	if a.RateTracker != nil {
		estimatedTokens := len(payload) / 4
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

	// Retry loop with exponential backoff for rate limits and server errors.
	var resp *http.Response
	var lastErr error
	for attempt := 0; attempt <= a.Retry.MaxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "POST", anthropicAPI, bytes.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", a.apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("anthropic-beta", "prompt-caching-2024-07-31")

		resp, err = a.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("send request: %w", err)
		}

		if resp.StatusCode == http.StatusOK {
			if a.CircuitBreaker != nil {
				a.CircuitBreaker.RecordSuccess()
			}
			// Calibrate rate tracker from response headers.
			a.calibrateRateTracker(resp)
			break // success
		}

		// Read the error body.
		errBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		apiErr := &APIError{
			StatusCode: resp.StatusCode,
			Message:    string(errBody),
			RetryAfter: ParseRetryAfter(resp.Header.Get("Retry-After")),
		}

		if !RetryableStatusCode(resp.StatusCode) || attempt == a.Retry.MaxRetries {
			// Record failure on circuit breaker when retries are exhausted.
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
			span.SetAttributes(attribute.Int("mentat.http.status", resp.StatusCode))
			span.End()
			return nil, apiErr
		}

		// Calculate delay.
		delay := a.Retry.BackoffDelay(attempt, apiErr.RetryAfter)
		log.Printf("provider: retryable error %d (attempt %d/%d), retrying in %s",
			resp.StatusCode, attempt+1, a.Retry.MaxRetries, delay)

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
		attribute.Int64("mentat.provider.latency_ms", time.Since(requestStart).Milliseconds()),
		attribute.Int("mentat.http.status", resp.StatusCode),
	)

	ch := make(chan StreamEvent, 64)
	go a.readSSEWithTracking(ctx, resp.Body, ch, span)
	return ch, nil
}

// calibrateRateTracker reads Anthropic rate-limit headers and updates the tracker.
func (a *Anthropic) calibrateRateTracker(resp *http.Response) {
	if a.RateTracker == nil {
		return
	}
	if limitStr := resp.Header.Get("x-ratelimit-limit-input-tokens"); limitStr != "" {
		if limit, err := strconv.Atoi(limitStr); err == nil && limit > 0 {
			a.RateTracker.UpdateLimit(limit)
			log.Printf("provider: rate limit calibrated to %d input tokens/min", limit)
		}
	}
	if remainStr := resp.Header.Get("x-ratelimit-remaining-input-tokens"); remainStr != "" {
		log.Printf("provider: rate limit remaining: %s input tokens", remainStr)
	}
	if resetStr := resp.Header.Get("x-ratelimit-reset-input-tokens"); resetStr != "" {
		log.Printf("provider: rate limit resets at: %s", resetStr)
	}
}

// readSSEWithTracking wraps readSSE to record input tokens via the rate tracker
// and finalize the provider span with token counts.
func (a *Anthropic) readSSEWithTracking(ctx context.Context, body io.ReadCloser, ch chan<- StreamEvent, span trace.Span) {
	// Create an intermediary channel to intercept usage events.
	inner := make(chan StreamEvent, 64)
	go a.readSSE(ctx, body, inner)

	var totalInput, totalOutput int
	defer func() {
		close(ch)
		if span != nil {
			span.SetAttributes(
				attribute.Int("mentat.provider.input_tokens", totalInput),
				attribute.Int("mentat.provider.output_tokens", totalOutput),
			)
			span.End()
		}
	}()
	for ev := range inner {
		// Record input tokens in the rate tracker when we see usage from message_start.
		if ev.Type == "usage" && ev.Usage != nil {
			if ev.Usage.InputTokens > 0 {
				totalInput += ev.Usage.InputTokens
				if a.RateTracker != nil {
					a.RateTracker.Record(ev.Usage.InputTokens)
					avail, limit := a.RateTracker.Remaining()
					log.Printf("provider: recorded %d input tokens (rate budget: %d/%d)", ev.Usage.InputTokens, avail, limit)
				}
			}
			if ev.Usage.OutputTokens > 0 {
				totalOutput += ev.Usage.OutputTokens
			}
		}
		ch <- ev
	}
}

// toolUseAccumulator tracks state for an in-progress tool_use content block.
type toolUseAccumulator struct {
	id        string
	name      string
	inputJSON strings.Builder
}

// readSSE parses the SSE stream from Anthropic and emits StreamEvents.
func (a *Anthropic) readSSE(ctx context.Context, body io.ReadCloser, ch chan<- StreamEvent) {
	defer close(ch)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	// Increase buffer size for large tool input JSON.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var eventType string
	var currentToolUse *toolUseAccumulator
	var currentBlockIdx int
	_ = currentBlockIdx // tracked for correlation

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			ch <- StreamEvent{Type: "error", Error: "context cancelled"}
			return
		default:
		}

		line := scanner.Text()

		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
			continue
		}

		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			a.handleSSEData(eventType, data, ch, &currentToolUse, &currentBlockIdx)
			continue
		}
	}

	if err := scanner.Err(); err != nil {
		ch <- StreamEvent{Type: "error", Error: fmt.Sprintf("read stream: %v", err)}
	}
}

// handleSSEData processes a single SSE data payload based on event type.
func (a *Anthropic) handleSSEData(eventType, data string, ch chan<- StreamEvent, currentToolUse **toolUseAccumulator, currentBlockIdx *int) {
	switch eventType {
	case "content_block_start":
		var payload struct {
			Index        int `json:"index"`
			ContentBlock struct {
				Type  string `json:"type"`
				ID    string `json:"id,omitempty"`
				Name  string `json:"name,omitempty"`
				Text  string `json:"text,omitempty"`
			} `json:"content_block"`
		}
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			return
		}
		*currentBlockIdx = payload.Index
		if payload.ContentBlock.Type == "tool_use" {
			*currentToolUse = &toolUseAccumulator{
				id:   payload.ContentBlock.ID,
				name: payload.ContentBlock.Name,
			}
		} else {
			*currentToolUse = nil
		}

	case "content_block_delta":
		var payload struct {
			Index int `json:"index"`
			Delta struct {
				Type           string `json:"type"`
				Text           string `json:"text"`
				PartialJSON    string `json:"partial_json,omitempty"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			return
		}

		switch payload.Delta.Type {
		case "text_delta":
			ch <- StreamEvent{Type: "delta", Content: payload.Delta.Text}
		case "input_json_delta":
			if *currentToolUse != nil {
				(*currentToolUse).inputJSON.WriteString(payload.Delta.PartialJSON)
			}
		}

	case "content_block_stop":
		if *currentToolUse != nil {
			tu := *currentToolUse
			var input map[string]any
			raw := tu.inputJSON.String()
			if raw != "" {
				if err := json.Unmarshal([]byte(raw), &input); err != nil {
					// If JSON parsing fails, send the raw string as a single "input" key.
					input = map[string]any{"_raw": raw}
				}
			} else {
				input = map[string]any{}
			}
			ch <- StreamEvent{
				Type: "tool_use",
				ToolUse: &ToolUseBlock{
					ID:    tu.id,
					Name:  tu.name,
					Input: input,
				},
			}
			*currentToolUse = nil
		}

	case "message_delta":
		var payload struct {
			Delta struct {
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &payload); err == nil {
			ch <- StreamEvent{
				Type: "usage",
				Usage: &Usage{
					OutputTokens: payload.Usage.OutputTokens,
					StopReason:   payload.Delta.StopReason,
				},
			}
		}

	case "message_start":
		var payload struct {
			Message struct {
				Usage struct {
					InputTokens         int `json:"input_tokens"`
					CacheCreationTokens int `json:"cache_creation_input_tokens"`
					CacheReadTokens     int `json:"cache_read_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(data), &payload); err == nil {
			u := payload.Message.Usage
			if u.CacheCreationTokens > 0 || u.CacheReadTokens > 0 {
				log.Printf("provider: prompt cache — creation=%d read=%d input=%d",
					u.CacheCreationTokens, u.CacheReadTokens, u.InputTokens)
			}
			ch <- StreamEvent{
				Type: "usage",
				Usage: &Usage{
					InputTokens:         u.InputTokens,
					CacheCreationTokens: u.CacheCreationTokens,
					CacheReadTokens:     u.CacheReadTokens,
				},
			}
		}

	case "message_stop":
		ch <- StreamEvent{Type: "done"}

	case "error":
		var payload struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &payload); err == nil {
			ch <- StreamEvent{Type: "error", Error: payload.Error.Message}
		} else {
			ch <- StreamEvent{Type: "error", Error: data}
		}
	}
}

// Complete makes a non-streaming completion call.
func (a *Anthropic) Complete(ctx context.Context, systemPrompt string, messages []ChatMessage, model string) (string, error) {
	ctx, span := tiamatotel.StartSpan(ctx, "mentat-chat.provider.anthropic.complete")
	defer span.End()
	span.SetAttributes(
		attribute.String("mentat.provider", "anthropic"),
		attribute.String("mentat.model", model),
		attribute.Int("mentat.messages.count", len(messages)),
	)

	if a.apiKey == "" {
		span.SetStatus(codes.Error, "ANTHROPIC_API_KEY not set")
		return "", fmt.Errorf("ANTHROPIC_API_KEY not set")
	}

	if model == "" {
		model = "claude-sonnet-4-20250514"
	}

	body := anthropicRequest{
		Model:     model,
		MaxTokens: 128,
		System:    buildSystemBlocks(systemPrompt),
		Messages:  marshalMessages(messages),
		Stream:    false,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	// Retry loop with exponential backoff for rate limits and server errors.
	var resp *http.Response
	for attempt := 0; attempt <= a.Retry.MaxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "POST", anthropicAPI, bytes.NewReader(payload))
		if err != nil {
			return "", fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", a.apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("anthropic-beta", "prompt-caching-2024-07-31")

		resp, err = a.client.Do(req)
		if err != nil {
			return "", fmt.Errorf("send request: %w", err)
		}

		if resp.StatusCode == http.StatusOK {
			break // success — fall through to decode
		}

		errBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		apiErr := &APIError{
			StatusCode: resp.StatusCode,
			Message:    string(errBody),
			RetryAfter: ParseRetryAfter(resp.Header.Get("Retry-After")),
		}

		if !RetryableStatusCode(resp.StatusCode) || attempt == a.Retry.MaxRetries {
			return "", apiErr
		}

		delay := a.Retry.BackoffDelay(attempt, apiErr.RetryAfter)
		log.Printf("provider: retryable error %d (attempt %d/%d), retrying in %s",
			resp.StatusCode, attempt+1, a.Retry.MaxRetries, delay)

		select {
		case <-ctx.Done():
			return "", fmt.Errorf("context cancelled during retry: %w", ctx.Err())
		case <-time.After(delay):
		}
	}
	defer resp.Body.Close()

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	for _, block := range result.Content {
		if block.Type == "text" {
			return strings.TrimSpace(block.Text), nil
		}
	}
	return "", nil
}
