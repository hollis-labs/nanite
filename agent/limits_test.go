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

func TestHistoryHidesClosingPromptAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	writeAgent(t, dir)
	provider := fake.New(fake.Response{Events: []llmtypes.StreamEvent{{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{ID: "unknown", Name: "unknown"}}, {Type: llmtypes.EventDone}}}, fake.Text("Work remains."))
	a, err := agent.Open(t.Context(), agent.Options{Dir: dir, GlobalSkillsDir: "-", Provider: provider, MaxSteps: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	c, err := a.NewConversation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// The prefix in user text is not a metadata marker and must stay visible.
	const request = "Explain nanite_closing_reply:max_steps"
	run, err := c.Send(t.Context(), agent.Input{Text: request})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	check := func(c *agent.Conversation) {
		t.Helper()
		messages, err := c.History(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if len(messages) != 4 {
			t.Fatalf("history has %d messages, want request/call/result/reply: %+v", len(messages), messages)
		}
		if messages[0].Role != "user" || messages[0].Blocks[len(messages[0].Blocks)-1].Text != request {
			t.Fatalf("lost user message: %+v", messages[0])
		}
		last := messages[len(messages)-1]
		if last.Role != "assistant" || last.Blocks[0].Text != "Work remains." {
			t.Fatalf("lost closing reply: %+v", last)
		}
		if last.Seq-messages[len(messages)-2].Seq != 2 {
			t.Fatal("history renumbered stored sequence numbers")
		}
	}
	check(c)
	id := c.ID()
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = agent.Open(t.Context(), agent.Options{Dir: dir, GlobalSkillsDir: "-", Provider: fake.New()})
	if err != nil {
		t.Fatal(err)
	}
	c, err = a.Conversation(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	check(c)
}
