package agent_test

import (
	"context"
	"testing"

	"github.com/hollis-labs/libs/llm/core/llmcontracts"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/internal/provider/fake"
)

type limitProvider struct {
	llmcontracts.Provider
	tokens []int
}

func (p *limitProvider) StreamChat(ctx context.Context, req llmtypes.ChatRequest) (<-chan llmtypes.StreamEvent, error) {
	p.tokens = append(p.tokens, req.MaxTokens)
	return p.Provider.StreamChat(ctx, req)
}

func TestEmbeddingRuntimeLimits(t *testing.T) {
	for _, invalid := range []agent.Options{{MaxSteps: -1}, {MaxOutputTokens: -1}, {DailyTokenBudget: -1}} {
		invalid.Dir = t.TempDir()
		writeAgent(t, invalid.Dir)
		invalid.GlobalSkillsDir = "-"
		invalid.Provider = fake.New()
		if a, err := agent.Open(t.Context(), invalid); err == nil {
			a.Close()
			t.Fatal("negative limit accepted")
		}
	}
	root := t.TempDir()
	writeAgent(t, root)
	p := &limitProvider{Provider: fake.New(fake.Response{Events: []llmtypes.StreamEvent{{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{ID: "unknown", Name: "unknown"}}, {Type: llmtypes.EventDone}}}, fake.Text("closing"))}
	a, err := agent.Open(t.Context(), agent.Options{Dir: root, GlobalSkillsDir: "-", Provider: p, MaxSteps: 1, MaxOutputTokens: 1234})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	c, err := a.NewConversation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	run, err := c.Send(t.Context(), agent.Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	for range run.Events() {
	}
	result, err := run.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != "max_steps" || result.Text != "closing" || len(p.tokens) != 2 {
		t.Fatalf("result %+v tokens %v", result, p.tokens)
	}
	for _, tokens := range p.tokens {
		if tokens != 1234 {
			t.Fatalf("tokens=%d", tokens)
		}
	}
}
