package engine

import (
	"context"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/definition"
	"github.com/hollis-labs/nanite/internal/events"
)

// Complete makes one model request outside any conversation, for a host
// helper such as the front desk's planner: the provider and model the use
// selects (falling back to chat as usual), a system prompt and one user
// message. The prompt is scrubbed of secrets first.
func (e *Engine) Complete(ctx context.Context, use, system, prompt string, maxTokens int) (string, error) {
	def, err := definition.Load(e.cfg.Dir, e.cfg.GlobalSkillsDir)
	if err != nil {
		return "", err
	}
	p, sel, err := e.provider(def, use)
	if err != nil {
		return "", err
	}
	if maxTokens <= 0 {
		maxTokens = 2048
	}
	spanCtx, endSpan := events.ModelSpan(ctx, e.scrub.Text(sel.Model))
	defer endSpan()
	return p.Complete(spanCtx, llmtypes.ChatRequest{Model: sel.Model, SystemPrompt: system, MaxTokens: maxTokens,
		Messages: []llmtypes.ChatMessage{{Role: "user", Content: e.scrub.Text(prompt)}}})
}
