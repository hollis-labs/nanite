package engine

import (
	"context"
	"encoding/json"

	ctxpkg "github.com/hollis-labs/libs/agent/core/context"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/definition"
	"github.com/hollis-labs/nanite/internal/events"
)

// Complete makes one model request outside any conversation, for a host
// helper such as the front desk's planner: the provider and model the use
// selects (falling back to chat as usual), a system prompt and one user
// message. The prompt is scrubbed of secrets first.
func (e *Engine) Complete(ctx context.Context, use, system, prompt string, maxTokens int) (string, error) {
	if err := e.checkDaily(); err != nil {
		return "", err
	}
	e.mu.Lock()
	stopped := e.stopped || e.closed
	e.mu.Unlock()
	if stopped {
		return "", ErrStopped
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(e.stopCtx, cancel)
	defer func() { stop(); cancel() }()
	var def definition.Definition
	var err error
	if p := profileFrom(ctx); p != nil {
		def = p.definition
	} else {
		def, err = definition.Load(e.cfg.Dir, e.cfg.GlobalSkillsDir)
	}
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
	req := llmtypes.ChatRequest{Model: sel.Model, SystemPrompt: system, MaxTokens: maxTokens,
		Messages: []llmtypes.ChatMessage{{Role: "user", Content: e.scrub.Text(prompt)}}}
	text, err := p.Complete(spanCtx, req)
	if err == nil {
		raw, _ := json.Marshal(req)
		est := ctxpkg.DefaultEstimator{}
		err = e.spendDaily(ctx, "", est.Estimate(string(raw))+est.Estimate(text))
	}
	return text, err
}
