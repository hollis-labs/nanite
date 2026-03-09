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
	"strings"
	"time"
)

const anthropicAPI = "https://api.anthropic.com/v1/messages"

// Anthropic implements the Provider interface for the Anthropic Messages API.
type Anthropic struct {
	apiKey   string
	client   *http.Client
	Retry    RetryConfig
	OnStatus StatusCallback // optional; called during retries to report status
}

// NewAnthropic creates a new Anthropic provider. It reads ANTHROPIC_API_KEY from the environment.
func NewAnthropic() *Anthropic {
	return &Anthropic{
		apiKey: os.Getenv("ANTHROPIC_API_KEY"),
		client: &http.Client{},
		Retry:  DefaultRetryConfig(),
	}
}

// anthropicRequest is the request body for the Anthropic Messages API.
type anthropicRequest struct {
	Model     string           `json:"model"`
	MaxTokens int              `json:"max_tokens"`
	System    string           `json:"system,omitempty"`
	Messages  []any            `json:"messages"`
	Stream    bool             `json:"stream"`
	Tools     []ToolDefinition `json:"tools,omitempty"`
}

// marshalMessages converts ChatMessage slice to the Anthropic API format.
// Simple text messages use {"role": "...", "content": "..."}.
// Multi-block messages use {"role": "...", "content": [...]}.
func marshalMessages(messages []ChatMessage) []any {
	result := make([]any, len(messages))
	for i, m := range messages {
		if len(m.ContentBlocks) > 0 {
			// Multi-block message (tool results, tool use responses).
			result[i] = map[string]any{
				"role":    m.Role,
				"content": m.ContentBlocks,
			}
		} else {
			// Simple text message.
			result[i] = map[string]any{
				"role":    m.Role,
				"content": m.Content,
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
	if a.apiKey == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY not set")
	}

	if model == "" {
		model = "claude-sonnet-4-20250514"
	}

	body := anthropicRequest{
		Model:     model,
		MaxTokens: 16384,
		System:    systemPrompt,
		Messages:  marshalMessages(messages),
		Stream:    true,
	}
	if len(tools) > 0 {
		body.Tools = tools
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
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

		resp, err = a.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("send request: %w", err)
		}

		if resp.StatusCode == http.StatusOK {
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

	ch := make(chan StreamEvent, 64)
	go a.readSSE(ctx, resp.Body, ch)
	return ch, nil
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
					InputTokens int `json:"input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(data), &payload); err == nil {
			ch <- StreamEvent{
				Type: "usage",
				Usage: &Usage{
					InputTokens: payload.Message.Usage.InputTokens,
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
	if a.apiKey == "" {
		return "", fmt.Errorf("ANTHROPIC_API_KEY not set")
	}

	if model == "" {
		model = "claude-sonnet-4-20250514"
	}

	body := anthropicRequest{
		Model:     model,
		MaxTokens: 128,
		System:    systemPrompt,
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
