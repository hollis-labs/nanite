package intent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	llmtypes "github.com/hollis-labs/go-llm-types"
)

// DefaultLLMTimeout bounds an LLMClassifier call. See UserSettings.
// ToolClassifierTimeoutMS for the user-tunable value.
const DefaultLLMTimeout = 500 * time.Millisecond

// LLMClassifier asks a provider whether the user turn requires tools. It is
// typically only reached when the rules layer's confidence is ambiguous (see
// BrokerClassifier). On error or malformed JSON it returns a fail-open result
// (hydrate=true, empty categories → "hydrate all") so the user's request is
// never stranded by a classifier issue (plan §D9).
type LLMClassifier struct {
	provider llmcontracts.Provider
	model    string
	timeout  time.Duration
}

// NewLLMClassifier constructs a classifier backed by the given provider+model.
// Pass 0 for timeout to use DefaultLLMTimeout.
func NewLLMClassifier(p llmcontracts.Provider, model string, timeout time.Duration) *LLMClassifier {
	if timeout <= 0 {
		timeout = DefaultLLMTimeout
	}
	return &LLMClassifier{provider: p, model: model, timeout: timeout}
}

// Classify calls the provider. D9: any error or malformed response yields a
// fail-open result rather than a hard failure.
func (c *LLMClassifier) Classify(ctx context.Context, in Input) (Result, error) {
	if c.provider == nil {
		return failOpen("classifier provider not configured"), nil
	}
	if in.UserTurn == "" {
		return Result{
			Hydrate:    false,
			Confidence: 0,
			Source:     SourceLLM,
			Reasoning:  "Empty user turn — nothing to classify.",
		}, nil
	}

	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	req := llmtypes.ChatRequest{
		Model:        c.model,
		SystemPrompt: systemPromptFor(in.AvailableCategories),
		Messages: []llmtypes.ChatMessage{
			{Role: "user", Content: in.UserTurn},
		},
	}

	raw, err := c.provider.Complete(callCtx, req)
	if err != nil {
		return failOpen(fmt.Sprintf("LLM classifier call failed: %v", err)), err
	}

	return parseLLMResponse(raw, in.AvailableCategories)
}

// systemPromptFor returns the classifier's system prompt — a short, structured
// instruction that yields a JSON object.
func systemPromptFor(cats []string) string {
	var b strings.Builder
	b.WriteString("You classify whether the user turn below needs tools to answer.\n")
	if len(cats) > 0 {
		b.WriteString("Available tool categories: ")
		b.WriteString(strings.Join(cats, ", "))
		b.WriteString(".\n")
	}
	b.WriteString("Respond with a single JSON object, no prose:\n")
	b.WriteString(`{"hydrate": <bool>, "categories": [<subset of available>], "reasoning": "<one short phrase>"}` + "\n")
	b.WriteString("Set hydrate=true only when tools are plausibly required.\n")
	b.WriteString("If hydrate=true, return the minimum category set that covers the need.")
	return b.String()
}

// llmResponseJSON mirrors the JSON shape the prompt requests.
type llmResponseJSON struct {
	Hydrate    bool     `json:"hydrate"`
	Categories []string `json:"categories"`
	Reasoning  string   `json:"reasoning"`
}

// parseLLMResponse parses the provider's completion text. Malformed JSON,
// unknown categories, or schema violations trigger fail-open.
func parseLLMResponse(raw string, available []string) (Result, error) {
	trimmed := extractJSONObject(raw)
	if trimmed == "" {
		return failOpen("LLM classifier returned no JSON object"), nil
	}

	var resp llmResponseJSON
	if err := json.Unmarshal([]byte(trimmed), &resp); err != nil {
		return failOpen(fmt.Sprintf("LLM classifier JSON parse failed: %v", err)), nil
	}

	if !resp.Hydrate {
		return Result{
			Hydrate:    false,
			Confidence: 0.9,
			Source:     SourceLLM,
			Reasoning:  truncateReason(resp.Reasoning, "No tool intent detected."),
		}, nil
	}

	allowed := toSet(available)
	picked := make([]string, 0, len(resp.Categories))
	for _, c := range resp.Categories {
		if _, ok := allowed[c]; ok {
			picked = append(picked, c)
		}
	}
	// If the LLM said hydrate but gave no usable categories, treat as "hydrate all".
	return Result{
		Hydrate:    true,
		Categories: picked,
		Confidence: 0.9,
		Source:     SourceLLM,
		Reasoning:  truncateReason(resp.Reasoning, "LLM flagged tool intent."),
	}, nil
}

// extractJSONObject returns the first balanced {...} substring of raw. Tolerant
// of model chatter wrapping the JSON (e.g., "```json\n{...}\n```").
func extractJSONObject(raw string) string {
	start := strings.IndexByte(raw, '{')
	if start < 0 {
		return ""
	}
	depth := 0
	for i := start; i < len(raw); i++ {
		switch raw[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return raw[start : i+1]
			}
		}
	}
	return ""
}

func truncateReason(r string, fallback string) string {
	r = strings.TrimSpace(r)
	if r == "" {
		return fallback
	}
	if len(r) > 160 {
		return r[:160] + "…"
	}
	return r
}

// failOpen returns a hydrate=all result tagged with the fallback source. Used
// by D9 whenever the classifier can't produce a reliable answer.
func failOpen(reason string) Result {
	return Result{
		Hydrate:    true,
		Categories: nil, // empty = hydrate all
		Confidence: 0,
		Source:     SourceFallback,
		Reasoning:  "Fail-open (hydrated all): " + reason,
	}
}
