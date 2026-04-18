// Audit trail — Engine TASK-20260412-007 (2026-04-12).
//
// SDK adoption deferred. This adapter remains the hand-rolled HTTP client
// rather than swapping to google/generative-ai-go. Rationale:
//
// generative-ai-go v0.20.1's genai.NewClient path (REST) internally wraps
// requests with googleapi/transport.APIKey, which sets the API key as a
// ?key=<value> URL query parameter. Swapping to the SDK would preserve the
// audit finding we need to close (credential leak via URL query strings in
// logs/proxies).
//
// Evidence (pinned module versions):
//   - github.com/google/generative-ai-go@v0.20.1/genai/client.go:65
//     genai.NewClient → NewGenerativeRESTClient.
//   - cloud.google.com/go/ai@v0.8.0/generativelanguage/apiv1beta/generative_client.go:398-402
//     NewGenerativeRESTClient → httptransport.NewClient.
//   - google.golang.org/api@v0.189.0/transport/http/dial.go:174-179
//     httptransport wraps the client with googleapi/transport.APIKey.
//   - google.golang.org/api@v0.189.0/googleapi/transport/apikey.go:18-44
//     RoundTripper sets ?key= in the URL query. Package doc marks it Deprecated.
//
// The gRPC path (genai.NewGenerativeClient) correctly uses x-goog-api-key
// gRPC metadata, but it is not reachable through the high-level
// genai.NewClient entry point.
//
// Mitigation in this file: send the API key via the x-goog-api-key HTTP
// header (an accepted alternative documented by the Gemini REST API) and
// never append ?key= to outgoing URLs. Error-response bodies are capped via
// io.LimitReader to match the Anthropic adapter's pattern (see ADAPTER_PATTERN.md).
//
// Revisit criterion: retry the SDK swap once google.golang.org/api ships an
// HTTP transport that sends API keys via x-goog-api-key by default, or once
// generative-ai-go exposes a REST client that does.

package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/internal/safego"
	"github.com/hollis-labs/nanite/pkg/models"
)

const geminiAPI = "https://generativelanguage.googleapis.com/v1beta/models"

// maxGeminiErrBody caps forwarded API-error response bytes. The hand-rolled
// client does not cap response bodies by default; we cap what we decode from
// error paths ourselves to prevent a hostile response from OOMing the process.
// Matches the Anthropic adapter's classifyAnthropicError pattern — see
// ADAPTER_PATTERN.md §8.
const maxGeminiErrBody = 1 << 20 // 1 MiB

var _ Embedder = (*Gemini)(nil)

// Gemini implements the Provider interface for the Google Gemini API.
//
// The adapter carries the same decorator-chain fields as every other adapter
// in this package (retry, circuit breaker, rate tracker, status callbacks) so
// Gemini calls participate in the shared resilience envelope even though the
// transport is a hand-rolled HTTP client rather than an SDK.
type Gemini struct {
	apiKey string
	client *http.Client

	Retry          RetryConfig
	OnStatus       StatusCallback
	CircuitBreaker *CircuitBreaker
	OnCircuitOpen  func()
	RateTracker    *TokenRateTracker
}

// NewGemini creates a new Gemini provider. It reads GOOGLE_API_KEY from the environment.
func NewGemini() *Gemini {
	return &Gemini{
		apiKey:         "",
		client:         &http.Client{},
		Retry:          DefaultRetryConfig(),
		CircuitBreaker: NewCircuitBreaker(3),
		RateTracker:    NewTokenRateTracker(30000),
	}
}

// doGeminiRequest issues an HTTP request using the decorator chain: circuit
// breaker short-circuit, rate-tracker pacing, retry-with-backoff on retryable
// status codes. On success it returns the *http.Response with Body still open
// for the caller to consume (and close). On terminal failure it returns an
// *APIError (or a wrapped ctx / transport error). The caller is responsible
// for preserving request headers — req.GetBody is set by the helper so the
// retry loop can rebuild the body on each attempt.
//
// estimatedTokens is used for rate-tracker pacing before the call and for
// Record() after success; pass 0 to skip pacing but still allow output
// accounting via the returned response.
func (g *Gemini) doGeminiRequest(ctx context.Context, method, urlStr string, payload []byte, estimatedTokens int) (*http.Response, error) {
	if g.CircuitBreaker != nil && g.CircuitBreaker.IsOpen() {
		if g.OnCircuitOpen != nil {
			g.OnCircuitOpen()
		}
		return nil, fmt.Errorf("circuit breaker open: provider rate limited after multiple retries")
	}

	if g.RateTracker != nil && estimatedTokens > 0 {
		if wait := g.RateTracker.WaitTime(estimatedTokens); wait > 0 {
			avail, limit := g.RateTracker.Remaining()
			if estimatedTokens > limit {
				slog.Warn("provider: request exceeds per-minute rate limit, proceeding anyway",
					"provider", "gemini", "est_tokens", estimatedTokens, "limit", limit)
			}
			slog.Info("provider: pacing for rate limit budget",
				"provider", "gemini", "wait", wait.Round(time.Millisecond),
				"est_tokens", estimatedTokens, "available", avail, "limit", limit)
			if err := PacingWait(ctx, wait, g.OnStatus); err != nil {
				return nil, fmt.Errorf("context cancelled during rate limit wait: %w", err)
			}
		}
	}

	var lastAPIErr *APIError
	for attempt := 0; attempt <= g.Retry.MaxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, urlStr, bytes.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-goog-api-key", g.apiKey)

		resp, err := g.client.Do(req)
		if err != nil {
			// Transport-level error — non-retryable here (context/dial).
			if g.CircuitBreaker != nil && attempt == g.Retry.MaxRetries {
				if tripped := g.CircuitBreaker.RecordFailure(); tripped {
					slog.Warn("provider: circuit breaker tripped after consecutive failures", "provider", "gemini")
					if g.OnCircuitOpen != nil {
						g.OnCircuitOpen()
					}
				}
			}
			return nil, fmt.Errorf("send request: %w", err)
		}

		if resp.StatusCode == http.StatusOK {
			if g.CircuitBreaker != nil {
				g.CircuitBreaker.RecordSuccess()
			}
			if g.RateTracker != nil && estimatedTokens > 0 {
				g.RateTracker.Record(estimatedTokens)
				avail, limit := g.RateTracker.Remaining()
				slog.Debug("provider: recorded input tokens",
					"provider", "gemini", "tokens", estimatedTokens, "available", avail, "limit", limit)
			}
			if g.OnStatus != nil && attempt > 0 {
				g.OnStatus(fmt.Sprintf("Recovered after %d retries.", attempt))
			}
			return resp, nil
		}

		// Non-2xx: classify, cap body, decide retry.
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxGeminiErrBody))
		_ = resp.Body.Close()
		retryAfter := ParseRetryAfter(resp.Header.Get("Retry-After"))
		apiErr := &APIError{
			StatusCode: resp.StatusCode,
			Message:    string(errBody),
			RetryAfter: retryAfter,
		}
		lastAPIErr = apiErr

		if !RetryableStatusCode(apiErr.StatusCode) || attempt == g.Retry.MaxRetries {
			if g.CircuitBreaker != nil && attempt == g.Retry.MaxRetries {
				if tripped := g.CircuitBreaker.RecordFailure(); tripped {
					slog.Warn("provider: circuit breaker tripped after consecutive failures", "provider", "gemini")
					if g.OnCircuitOpen != nil {
						g.OnCircuitOpen()
					}
				}
			}
			if g.OnStatus != nil {
				g.OnStatus(fmt.Sprintf("Gemini request failed: status %d", apiErr.StatusCode))
			}
			return nil, apiErr
		}

		delay := g.Retry.BackoffDelay(attempt, retryAfter)
		slog.Info("provider: retryable error, retrying",
			"provider", "gemini", "status", apiErr.StatusCode,
			"attempt", attempt+1, "max", g.Retry.MaxRetries, "delay", delay)
		if g.OnStatus != nil {
			g.OnStatus(fmt.Sprintf("Rate limited, retrying in %s... (attempt %d/%d)",
				delay.Round(time.Millisecond), attempt+1, g.Retry.MaxRetries))
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context cancelled during retry: %w", ctx.Err())
		case <-time.After(delay):
		}
	}

	// Loop exhausted without a success or terminal branch (shouldn't happen).
	if lastAPIErr != nil {
		return nil, lastAPIErr
	}
	return nil, fmt.Errorf("gemini: retry loop exhausted")
}

// geminiRequest is the request body for the Gemini generateContent API.
type geminiRequest struct {
	Contents         []geminiContent        `json:"contents"`
	SystemInstruct   *geminiContent         `json:"systemInstruction,omitempty"`
	GenerationConfig *geminiGenerationConfig `json:"generationConfig,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text,omitempty"`
}

type geminiGenerationConfig struct {
	MaxOutputTokens int     `json:"maxOutputTokens,omitempty"`
	Temperature     float64 `json:"temperature,omitempty"`
}

// StreamChat implements Provider.StreamChat using Gemini's streaming SSE API.
func (g *Gemini) StreamChat(ctx context.Context, systemPrompt string, messages []ChatMessage, model string) (<-chan StreamEvent, error) {
	if g.apiKey == "" {
		return nil, fmt.Errorf("GOOGLE_API_KEY not set")
	}

	if model == "" {
		model = "gemini-2.5-flash"
	}

	body := g.buildRequest(model, systemPrompt, messages)

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/%s:streamGenerateContent?alt=sse", geminiAPI, model)
	estInput := estimatePromptTokens(systemPrompt, messages)
	resp, err := g.doGeminiRequest(ctx, "POST", url, payload, estInput)
	if err != nil {
		return nil, err
	}

	ch := make(chan StreamEvent, 64)
	safego.Go(ctx, "provider.gemini.readSSE", func() {
		g.readSSE(ctx, resp.Body, ch)
	})
	return ch, nil
}

// buildRequest converts Nanite messages to Gemini API format. Callers pass
// the model name so the per-model max_output from the canonical registry is
// honoured — previously every request was capped at 8192 regardless of the
// model's actual capability (audit 04).
func (g *Gemini) buildRequest(model, systemPrompt string, messages []ChatMessage) geminiRequest {
	maxOut := models.MaxOutputFor(model)
	if maxOut <= 0 {
		maxOut = 8192 // provider default
	}
	req := geminiRequest{
		GenerationConfig: &geminiGenerationConfig{
			MaxOutputTokens: maxOut,
		},
	}

	if systemPrompt != "" {
		req.SystemInstruct = &geminiContent{
			Parts: []geminiPart{{Text: systemPrompt}},
		}
	}

	for _, m := range messages {
		role := m.Role
		if role == "assistant" {
			role = "model"
		}
		content := m.Content
		if content == "" && len(m.ContentBlocks) > 0 {
			var parts []string
			for _, b := range m.ContentBlocks {
				if b.Text != "" {
					parts = append(parts, b.Text)
				}
				if b.Content != "" {
					parts = append(parts, b.Content)
				}
			}
			content = strings.Join(parts, "\n")
		}
		if content == "" {
			continue
		}
		req.Contents = append(req.Contents, geminiContent{
			Role:  role,
			Parts: []geminiPart{{Text: content}},
		})
	}

	return req
}

// readSSE parses the SSE stream from Gemini and emits StreamEvents.
func (g *Gemini) readSSE(ctx context.Context, body io.ReadCloser, ch chan<- StreamEvent) {
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

		var chunk struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"content"`
				FinishReason string `json:"finishReason"`
			} `json:"candidates"`
			UsageMetadata *struct {
				PromptTokenCount     int `json:"promptTokenCount"`
				CandidatesTokenCount int `json:"candidatesTokenCount"`
				TotalTokenCount      int `json:"totalTokenCount"`
			} `json:"usageMetadata"`
		}

		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if len(chunk.Candidates) > 0 {
			cand := chunk.Candidates[0]
			for _, part := range cand.Content.Parts {
				if part.Text != "" {
					ch <- StreamEvent{Type: "delta", Content: part.Text}
				}
			}

			if cand.FinishReason != "" && cand.FinishReason != "STOP" {
				ch <- StreamEvent{
					Type: "usage",
					Usage: &Usage{
						StopReason: strings.ToLower(cand.FinishReason),
					},
				}
			}
		}

		if chunk.UsageMetadata != nil {
			if g.RateTracker != nil && chunk.UsageMetadata.CandidatesTokenCount > 0 {
				g.RateTracker.Record(chunk.UsageMetadata.CandidatesTokenCount)
			}
			ch <- StreamEvent{
				Type: "usage",
				Usage: &Usage{
					InputTokens:  chunk.UsageMetadata.PromptTokenCount,
					OutputTokens: chunk.UsageMetadata.CandidatesTokenCount,
				},
			}
		}
	}

	// Emit done after stream ends.
	ch <- StreamEvent{Type: "done"}

	if err := scanner.Err(); err != nil {
		ch <- StreamEvent{Type: "error", Error: fmt.Sprintf("read stream: %v", err)}
	}
}

// StreamChatWithTools delegates to StreamChat (tool calling not yet implemented for Gemini HTTP).
func (g *Gemini) StreamChatWithTools(ctx context.Context, systemPrompt string, messages []ChatMessage, model string, tools []ToolDefinition) (<-chan StreamEvent, error) {
	return g.StreamChat(ctx, systemPrompt, messages, model)
}

// Complete makes a non-streaming completion call to Gemini.
func (g *Gemini) Complete(ctx context.Context, systemPrompt string, messages []ChatMessage, model string) (string, error) {
	if g.apiKey == "" {
		return "", fmt.Errorf("GOOGLE_API_KEY not set")
	}

	if model == "" {
		model = "gemini-2.5-flash"
	}

	body := g.buildRequest(model, systemPrompt, messages)

	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/%s:generateContent", geminiAPI, model)
	estInput := estimatePromptTokens(systemPrompt, messages)
	resp, err := g.doGeminiRequest(ctx, "POST", url, payload, estInput)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		UsageMetadata *struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
			TotalTokenCount      int `json:"totalTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if g.RateTracker != nil && result.UsageMetadata != nil && result.UsageMetadata.CandidatesTokenCount > 0 {
		g.RateTracker.Record(result.UsageMetadata.CandidatesTokenCount)
	}

	if len(result.Candidates) > 0 {
		var parts []string
		for _, p := range result.Candidates[0].Content.Parts {
			if p.Text != "" {
				parts = append(parts, p.Text)
			}
		}
		return strings.TrimSpace(strings.Join(parts, "")), nil
	}
	return "", nil
}

// Capabilities returns the capabilities supported by the Gemini provider.
func (g *Gemini) Capabilities() ProviderCapabilities {
	return capabilitiesFromRegistry("gemini")
}

// gemini embedding types (unexported)

type geminiBatchEmbedRequest struct {
	Requests []geminiEmbedRequest `json:"requests"`
}

type geminiEmbedRequest struct {
	Model   string        `json:"model"`
	Content geminiContent `json:"content"`
}

type geminiBatchEmbedResponse struct {
	Embeddings []geminiEmbeddingValue `json:"embeddings"`
}

type geminiEmbeddingValue struct {
	Values []float64 `json:"values"`
}

// Embed generates an embedding vector for a single text input.
func (g *Gemini) Embed(ctx context.Context, text string, model string) (*EmbeddingResult, error) {
	results, err := g.EmbedBatch(ctx, []string{text}, model)
	if err != nil {
		return nil, err
	}
	return &results[0], nil
}

// EmbedBatch generates embedding vectors for multiple texts in a single API call.
func (g *Gemini) EmbedBatch(ctx context.Context, texts []string, model string) ([]EmbeddingResult, error) {
	if g.apiKey == "" {
		return nil, fmt.Errorf("GOOGLE_API_KEY not set")
	}

	if model == "" {
		model = "text-embedding-004"
	}

	// Build batch request.
	batchReq := geminiBatchEmbedRequest{
		Requests: make([]geminiEmbedRequest, len(texts)),
	}
	for i, text := range texts {
		batchReq.Requests[i] = geminiEmbedRequest{
			Model:   "models/" + model,
			Content: geminiContent{Parts: []geminiPart{{Text: text}}},
		}
	}

	payload, err := json.Marshal(batchReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/%s:batchEmbedContents", geminiAPI, model)
	// Rough input-token estimate across all texts (~4 chars/token).
	totalChars := 0
	for _, t := range texts {
		totalChars += len(t)
	}
	estInput := totalChars / 4
	resp, err := g.doGeminiRequest(ctx, "POST", url, payload, estInput)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var batchResp geminiBatchEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&batchResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	results := make([]EmbeddingResult, len(batchResp.Embeddings))
	for i, emb := range batchResp.Embeddings {
		vec := make([]float32, len(emb.Values))
		for j, v := range emb.Values {
			vec[j] = float32(v)
		}
		results[i] = EmbeddingResult{
			Embedding: vec,
		}
	}

	return results, nil
}

// EmbeddingDimensions returns the output dimensions for the given model.
// Returns 0 if the model is unknown.
func (g *Gemini) EmbeddingDimensions(model string) int {
	return models.EmbeddingDimensionsFor(model)
}
