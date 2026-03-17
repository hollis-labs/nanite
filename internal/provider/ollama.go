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

// Ollama implements the Provider interface for the local Ollama REST API.
type Ollama struct {
	host   string
	client *http.Client
}

// NewOllama creates a new Ollama provider. It reads OLLAMA_HOST from the environment,
// defaulting to http://localhost:11434.
func NewOllama() *Ollama {
	host := os.Getenv("OLLAMA_HOST")
	if host == "" {
		host = "http://localhost:11434"
	}
	return &Ollama{
		host:   strings.TrimRight(host, "/"),
		client: &http.Client{},
	}
}

// ollamaRequest is the request body for the Ollama /api/chat endpoint.
type ollamaRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
}

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// StreamChat implements Provider.StreamChat using Ollama's streaming API.
// Ollama streams newline-delimited JSON with message.content fields.
func (o *Ollama) StreamChat(ctx context.Context, systemPrompt string, messages []ChatMessage, model string) (<-chan StreamEvent, error) {
	if model == "" {
		model = "llama3.1"
	}

	// Build messages array with system prompt first.
	msgs := make([]ollamaMessage, 0, len(messages)+1)
	if systemPrompt != "" {
		msgs = append(msgs, ollamaMessage{Role: "system", Content: systemPrompt})
	}
	for _, m := range messages {
		msgs = append(msgs, ollamaMessage{Role: m.Role, Content: m.Content})
	}

	body := ollamaRequest{
		Model:    model,
		Messages: msgs,
		Stream:   true,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := o.host + "/api/chat"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama API error %d: %s", resp.StatusCode, string(errBody))
	}

	ch := make(chan StreamEvent, 64)
	go o.readStream(ctx, resp.Body, ch)
	return ch, nil
}

// StreamChatWithTools delegates to StreamChat, ignoring tools (not yet supported for Ollama).
func (o *Ollama) StreamChatWithTools(ctx context.Context, systemPrompt string, messages []ChatMessage, model string, tools []ToolDefinition) (<-chan StreamEvent, error) {
	return o.StreamChat(ctx, systemPrompt, messages, model)
}

// readStream parses the newline-delimited JSON stream from Ollama.
func (o *Ollama) readStream(ctx context.Context, body io.ReadCloser, ch chan<- StreamEvent) {
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
		if strings.TrimSpace(line) == "" {
			continue
		}

		var chunk struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Done               bool `json:"done"`
			PromptEvalCount    int  `json:"prompt_eval_count"`
			EvalCount          int  `json:"eval_count"`
		}

		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			continue
		}

		if chunk.Message.Content != "" {
			ch <- StreamEvent{Type: "delta", Content: chunk.Message.Content}
		}

		if chunk.Done {
			ch <- StreamEvent{
				Type: "usage",
				Usage: &Usage{
					InputTokens:  chunk.PromptEvalCount,
					OutputTokens: chunk.EvalCount,
					StopReason:   "end_turn",
				},
			}
			ch <- StreamEvent{Type: "done"}
			return
		}
	}

	if err := scanner.Err(); err != nil {
		ch <- StreamEvent{Type: "error", Error: fmt.Sprintf("read stream: %v", err)}
	}
}

// Complete makes a non-streaming completion call to Ollama.
func (o *Ollama) Complete(ctx context.Context, systemPrompt string, messages []ChatMessage, model string) (string, error) {
	if model == "" {
		model = "llama3.1"
	}

	msgs := make([]ollamaMessage, 0, len(messages)+1)
	if systemPrompt != "" {
		msgs = append(msgs, ollamaMessage{Role: "system", Content: systemPrompt})
	}
	for _, m := range messages {
		msgs = append(msgs, ollamaMessage{Role: m.Role, Content: m.Content})
	}

	body := ollamaRequest{
		Model:    model,
		Messages: msgs,
		Stream:   false,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := o.host + "/api/chat"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama API error %d: %s", resp.StatusCode, string(errBody))
	}

	var result struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	return strings.TrimSpace(result.Message.Content), nil
}

// Capabilities returns the capabilities supported by the Ollama provider.
func (o *Ollama) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{
		SupportsStreamJSON:          true,  // Ollama supports streaming responses
		SupportsPreToolHooks:        false, // No direct pre-tool hook support
		SupportsPostToolHooks:       false, // No direct post-tool hook support
		SupportsSystemPromptCaching: false, // No prompt caching support in current implementation
		SupportsToolCalling:         false, // Tool calling not implemented (StreamChatWithTools ignores tools)
		SupportsBatch:               false, // No batch API support in current implementation
		SupportsImageInput:          false, // Most Ollama models don't support image inputs (depends on model)
		MaxTokens:                   0,     // Variable depending on specific model loaded in Ollama
	}
}
