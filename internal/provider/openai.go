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

const openaiAPI = "https://api.openai.com/v1/chat/completions"

// OpenAI implements the Provider interface for the OpenAI Chat Completions API.
type OpenAI struct {
	apiKey string
	client *http.Client
}

// NewOpenAI creates a new OpenAI provider. It reads OPENAI_API_KEY from the environment.
func NewOpenAI() *OpenAI {
	return &OpenAI{
		apiKey: os.Getenv("OPENAI_API_KEY"),
		client: &http.Client{},
	}
}

// openaiRequest is the request body for the OpenAI Chat Completions API.
type openaiRequest struct {
	Model    string          `json:"model"`
	Messages []openaiMessage `json:"messages"`
	Stream   bool            `json:"stream"`
}

type openaiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// StreamChat implements Provider.StreamChat using OpenAI's streaming SSE API.
func (o *OpenAI) StreamChat(ctx context.Context, systemPrompt string, messages []ChatMessage, model string) (<-chan StreamEvent, error) {
	if o.apiKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY not set")
	}

	if model == "" {
		model = "gpt-4o"
	}

	// Build messages array with system prompt first.
	msgs := make([]openaiMessage, 0, len(messages)+1)
	if systemPrompt != "" {
		msgs = append(msgs, openaiMessage{Role: "system", Content: systemPrompt})
	}
	for _, m := range messages {
		msgs = append(msgs, openaiMessage{Role: m.Role, Content: m.Content})
	}

	body := openaiRequest{
		Model:    model,
		Messages: msgs,
		Stream:   true,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", openaiAPI, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai API error %d: %s", resp.StatusCode, string(errBody))
	}

	ch := make(chan StreamEvent, 64)
	go o.readSSE(ctx, resp.Body, ch)
	return ch, nil
}

// readSSE parses the SSE stream from OpenAI and emits StreamEvents.
func (o *OpenAI) readSSE(ctx context.Context, body io.ReadCloser, ch chan<- StreamEvent) {
	defer close(ch)
	defer body.Close()

	scanner := bufio.NewScanner(body)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			ch <- StreamEvent{Type: "error", Error: "context cancelled"}
			return
		default:
		}

		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")

		if data == "[DONE]" {
			ch <- StreamEvent{Type: "done"}
			return
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}

		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if len(chunk.Choices) > 0 {
			delta := chunk.Choices[0].Delta.Content
			if delta != "" {
				ch <- StreamEvent{Type: "delta", Content: delta}
			}

			if chunk.Choices[0].FinishReason != nil {
				reason := *chunk.Choices[0].FinishReason
				ch <- StreamEvent{
					Type: "usage",
					Usage: &Usage{
						StopReason: reason,
					},
				}
			}
		}

		if chunk.Usage != nil {
			ch <- StreamEvent{
				Type: "usage",
				Usage: &Usage{
					InputTokens:  chunk.Usage.PromptTokens,
					OutputTokens: chunk.Usage.CompletionTokens,
				},
			}
		}
	}

	if err := scanner.Err(); err != nil {
		ch <- StreamEvent{Type: "error", Error: fmt.Sprintf("read stream: %v", err)}
	}
}

// StreamChatWithTools delegates to StreamChat, ignoring tools (not yet supported for OpenAI).
func (o *OpenAI) StreamChatWithTools(ctx context.Context, systemPrompt string, messages []ChatMessage, model string, tools []ToolDefinition) (<-chan StreamEvent, error) {
	return o.StreamChat(ctx, systemPrompt, messages, model)
}

// Complete makes a non-streaming completion call to OpenAI.
func (o *OpenAI) Complete(ctx context.Context, systemPrompt string, messages []ChatMessage, model string) (string, error) {
	if o.apiKey == "" {
		return "", fmt.Errorf("OPENAI_API_KEY not set")
	}

	if model == "" {
		model = "gpt-4o"
	}

	msgs := make([]openaiMessage, 0, len(messages)+1)
	if systemPrompt != "" {
		msgs = append(msgs, openaiMessage{Role: "system", Content: systemPrompt})
	}
	for _, m := range messages {
		msgs = append(msgs, openaiMessage{Role: m.Role, Content: m.Content})
	}

	body := openaiRequest{
		Model:    model,
		Messages: msgs,
		Stream:   false,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", openaiAPI, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)

	resp, err := o.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("openai API error %d: %s", resp.StatusCode, string(errBody))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if len(result.Choices) > 0 {
		return strings.TrimSpace(result.Choices[0].Message.Content), nil
	}
	return "", nil
}

// Capabilities returns the capabilities supported by the OpenAI provider.
func (o *OpenAI) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{
		SupportsStreamJSON:          true,  // OpenAI supports streaming responses
		SupportsPreToolHooks:        false, // No direct pre-tool hook support
		SupportsPostToolHooks:       false, // No direct post-tool hook support
		SupportsSystemPromptCaching: false, // No prompt caching support in current implementation
		SupportsToolCalling:         false, // Tool calling not implemented (StreamChatWithTools ignores tools)
		SupportsBatch:               false, // No batch API support in current implementation
		SupportsImageInput:          true,  // GPT-4o and GPT-4 Turbo support image inputs
		MaxTokens:                   128000, // GPT-4o supports up to 128k tokens
	}
}
