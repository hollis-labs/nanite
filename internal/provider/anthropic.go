package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

const anthropicAPI = "https://api.anthropic.com/v1/messages"

// Anthropic implements the Provider interface for the Anthropic Messages API.
type Anthropic struct {
	apiKey string
	client *http.Client
}

// NewAnthropic creates a new Anthropic provider. It reads ANTHROPIC_API_KEY from the environment.
func NewAnthropic() *Anthropic {
	return &Anthropic{
		apiKey: os.Getenv("ANTHROPIC_API_KEY"),
		client: &http.Client{},
	}
}

// anthropicRequest is the request body for the Anthropic Messages API.
type anthropicRequest struct {
	Model     string              `json:"model"`
	MaxTokens int                 `json:"max_tokens"`
	System    string              `json:"system,omitempty"`
	Messages  []anthropicMessage  `json:"messages"`
	Stream    bool                `json:"stream"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// StreamChat implements Provider.StreamChat using Anthropic's streaming SSE API.
func (a *Anthropic) StreamChat(ctx context.Context, systemPrompt string, messages []ChatMessage, model string) (<-chan StreamEvent, error) {
	if a.apiKey == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY not set")
	}

	if model == "" {
		model = "claude-sonnet-4-20250514"
	}

	msgs := make([]anthropicMessage, len(messages))
	for i, m := range messages {
		msgs[i] = anthropicMessage{Role: m.Role, Content: m.Content}
	}

	body := anthropicRequest{
		Model:     model,
		MaxTokens: 8192,
		System:    systemPrompt,
		Messages:  msgs,
		Stream:    true,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", anthropicAPI, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", a.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("anthropic API error %d: %s", resp.StatusCode, string(errBody))
	}

	ch := make(chan StreamEvent, 64)
	go a.readSSE(ctx, resp.Body, ch)
	return ch, nil
}

// readSSE parses the SSE stream from Anthropic and emits StreamEvents.
func (a *Anthropic) readSSE(ctx context.Context, body io.ReadCloser, ch chan<- StreamEvent) {
	defer close(ch)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	var eventType string

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
			a.handleSSEData(eventType, data, ch)
			continue
		}
	}

	if err := scanner.Err(); err != nil {
		ch <- StreamEvent{Type: "error", Error: fmt.Sprintf("read stream: %v", err)}
	}
}

// handleSSEData processes a single SSE data payload based on event type.
func (a *Anthropic) handleSSEData(eventType, data string, ch chan<- StreamEvent) {
	switch eventType {
	case "content_block_delta":
		var payload struct {
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(data), &payload); err == nil && payload.Delta.Type == "text_delta" {
			ch <- StreamEvent{Type: "delta", Content: payload.Delta.Text}
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

	msgs := make([]anthropicMessage, len(messages))
	for i, m := range messages {
		msgs[i] = anthropicMessage{Role: m.Role, Content: m.Content}
	}

	body := anthropicRequest{
		Model:     model,
		MaxTokens: 128,
		System:    systemPrompt,
		Messages:  msgs,
		Stream:    false,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", anthropicAPI, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", a.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := a.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("anthropic API error %d: %s", resp.StatusCode, string(errBody))
	}

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
