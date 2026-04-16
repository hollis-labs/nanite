package intent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/go-providers/provider"
)

// stubProvider lets us control Complete's output for classifier tests.
type stubProvider struct {
	reply        string
	err          error
	capturedReq  *provider.ChatRequest
	sleepBefore  time.Duration
}

func (s *stubProvider) StreamChat(context.Context, provider.ChatRequest) (<-chan provider.StreamEvent, error) {
	return nil, errors.New("not implemented")
}
func (s *stubProvider) Complete(ctx context.Context, req provider.ChatRequest) (string, error) {
	s.capturedReq = &req
	if s.sleepBefore > 0 {
		select {
		case <-time.After(s.sleepBefore):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if s.err != nil {
		return "", s.err
	}
	return s.reply, nil
}
func (s *stubProvider) Capabilities() provider.ProviderCapabilities {
	return provider.ProviderCapabilities{}
}

func TestLLMClassifier_PromptShape(t *testing.T) {
	sp := &stubProvider{reply: `{"hydrate": false, "categories": [], "reasoning": "chitchat"}`}
	c := NewLLMClassifier(sp, "test-model", 0)

	_, err := c.Classify(context.Background(), Input{
		UserTurn:            "hi there",
		AvailableCategories: []string{"search", "code-exec"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sp.capturedReq == nil {
		t.Fatal("provider not called")
	}
	if sp.capturedReq.Model != "test-model" {
		t.Errorf("model: got %q, want %q", sp.capturedReq.Model, "test-model")
	}
	if !strings.Contains(sp.capturedReq.SystemPrompt, "search, code-exec") {
		t.Errorf("system prompt missing categories: %q", sp.capturedReq.SystemPrompt)
	}
	if len(sp.capturedReq.Messages) != 1 || sp.capturedReq.Messages[0].Role != "user" || sp.capturedReq.Messages[0].Content != "hi there" {
		t.Errorf("user message shape wrong: %+v", sp.capturedReq.Messages)
	}
}

func TestLLMClassifier_ParsesHydrateTrue(t *testing.T) {
	sp := &stubProvider{reply: `{"hydrate": true, "categories": ["search"], "reasoning": "asked to find something"}`}
	c := NewLLMClassifier(sp, "m", 0)
	r, err := c.Classify(context.Background(), Input{
		UserTurn:            "find X",
		AvailableCategories: []string{"search", "code-exec"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !r.Hydrate {
		t.Fatalf("expected hydrate=true")
	}
	if len(r.Categories) != 1 || r.Categories[0] != "search" {
		t.Fatalf("categories: got %v", r.Categories)
	}
	if r.Source != SourceLLM {
		t.Fatalf("source: got %q, want %q", r.Source, SourceLLM)
	}
	if !strings.Contains(r.Reasoning, "asked to find") {
		t.Fatalf("reasoning: %q", r.Reasoning)
	}
}

func TestLLMClassifier_ParsesHydrateFalse(t *testing.T) {
	sp := &stubProvider{reply: `{"hydrate": false, "categories": [], "reasoning": "pure chat"}`}
	c := NewLLMClassifier(sp, "m", 0)
	r, _ := c.Classify(context.Background(), Input{UserTurn: "how are you", AvailableCategories: []string{"search"}})
	if r.Hydrate {
		t.Fatalf("expected hydrate=false")
	}
	if r.Source != SourceLLM {
		t.Fatalf("source: got %q", r.Source)
	}
}

func TestLLMClassifier_FiltersUnknownCategories(t *testing.T) {
	sp := &stubProvider{reply: `{"hydrate": true, "categories": ["search", "bogus", "code-exec"], "reasoning": "x"}`}
	c := NewLLMClassifier(sp, "m", 0)
	r, _ := c.Classify(context.Background(), Input{
		UserTurn:            "do the thing",
		AvailableCategories: []string{"search"},
	})
	if !r.Hydrate {
		t.Fatalf("expected hydrate=true")
	}
	if len(r.Categories) != 1 || r.Categories[0] != "search" {
		t.Fatalf("should filter to available only; got %v", r.Categories)
	}
}

func TestLLMClassifier_MalformedJSONFailsOpen(t *testing.T) {
	sp := &stubProvider{reply: `this is not JSON`}
	c := NewLLMClassifier(sp, "m", 0)
	r, err := c.Classify(context.Background(), Input{UserTurn: "x", AvailableCategories: []string{"search"}})
	if err != nil {
		t.Fatalf("no error expected from fail-open; got %v", err)
	}
	if !r.Hydrate {
		t.Fatalf("fail-open must hydrate=true")
	}
	if r.Source != SourceFallback {
		t.Fatalf("source: got %q, want %q", r.Source, SourceFallback)
	}
	if !strings.Contains(r.Reasoning, "Fail-open") {
		t.Fatalf("reasoning should indicate fail-open; got %q", r.Reasoning)
	}
}

func TestLLMClassifier_ProviderErrorFailsOpen(t *testing.T) {
	sp := &stubProvider{err: errors.New("boom")}
	c := NewLLMClassifier(sp, "m", 0)
	r, err := c.Classify(context.Background(), Input{UserTurn: "x", AvailableCategories: []string{"search"}})
	if err == nil {
		t.Fatalf("expected error passthrough so broker can log")
	}
	if !r.Hydrate || r.Source != SourceFallback {
		t.Fatalf("fail-open result wrong on provider error: %+v", r)
	}
}

func TestLLMClassifier_TimeoutFailsOpen(t *testing.T) {
	sp := &stubProvider{reply: `{"hydrate": false, "categories": [], "reasoning": "x"}`, sleepBefore: 50 * time.Millisecond}
	c := NewLLMClassifier(sp, "m", 5*time.Millisecond)
	r, _ := c.Classify(context.Background(), Input{UserTurn: "x", AvailableCategories: []string{"search"}})
	if !r.Hydrate || r.Source != SourceFallback {
		t.Fatalf("timeout should fail-open; got %+v", r)
	}
}

func TestLLMClassifier_EmptyUserTurn(t *testing.T) {
	sp := &stubProvider{}
	c := NewLLMClassifier(sp, "m", 0)
	r, err := c.Classify(context.Background(), Input{UserTurn: "", AvailableCategories: []string{"search"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Hydrate {
		t.Fatalf("empty turn should not hydrate; got %+v", r)
	}
	if sp.capturedReq != nil {
		t.Fatalf("empty turn should skip provider call")
	}
}

func TestLLMClassifier_ExtractJSONFromWrapped(t *testing.T) {
	sp := &stubProvider{reply: "```json\n{\"hydrate\": true, \"categories\": [\"search\"], \"reasoning\": \"yep\"}\n```"}
	c := NewLLMClassifier(sp, "m", 0)
	r, _ := c.Classify(context.Background(), Input{UserTurn: "find x", AvailableCategories: []string{"search"}})
	if !r.Hydrate || r.Source != SourceLLM {
		t.Fatalf("should parse JSON out of code fence; got %+v", r)
	}
}

func TestLLMClassifier_NilProviderFailsOpen(t *testing.T) {
	c := NewLLMClassifier(nil, "m", 0)
	r, _ := c.Classify(context.Background(), Input{UserTurn: "x", AvailableCategories: []string{"search"}})
	if r.Source != SourceFallback {
		t.Fatalf("nil provider should fail-open; got %+v", r)
	}
}
