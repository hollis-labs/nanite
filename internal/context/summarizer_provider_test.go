package context

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/go-providers/provider"
)

// mockProvider implements provider.Provider for summarizer tests.
type mockProvider struct {
	completeFn func(ctx context.Context, in provider.ChatRequest) (string, error)
}

func (m *mockProvider) StreamChat(ctx context.Context, in provider.ChatRequest) (<-chan provider.StreamEvent, error) {
	return nil, nil
}
func (m *mockProvider) Complete(ctx context.Context, in provider.ChatRequest) (string, error) {
	return m.completeFn(ctx, in)
}
func (m *mockProvider) Capabilities() provider.ProviderCapabilities {
	return provider.ProviderCapabilities{}
}

func TestProviderSummarizer_PassesModeSystemPrompt(t *testing.T) {
	var gotSystem string
	var gotModel string
	var gotMessages int
	mock := &mockProvider{
		completeFn: func(_ context.Context, in provider.ChatRequest) (string, error) {
			gotSystem = in.SystemPrompt
			gotModel = in.Model
			gotMessages = len(in.Messages)
			return "summary-text", nil
		},
	}
	s := NewProviderSummarizer(mock, "test-model")
	sys := summarySystemPrompt(CompactionModeCode)
	got, err := s.Summarize(context.Background(), sys, []provider.ChatMessage{
		{Role: "user", Content: "hello"},
	})
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if got != "summary-text" {
		t.Errorf("result = %q, want summary-text", got)
	}
	if !strings.Contains(gotSystem, "code changes") {
		t.Errorf("code-mode prompt not passed through; got: %q", gotSystem)
	}
	if gotModel != "test-model" {
		t.Errorf("model = %q, want test-model", gotModel)
	}
	if gotMessages != 1 {
		t.Errorf("messages = %d, want 1", gotMessages)
	}
}

func TestProviderSummarizer_WrapsError(t *testing.T) {
	mock := &mockProvider{
		completeFn: func(_ context.Context, _ provider.ChatRequest) (string, error) {
			return "", errors.New("boom")
		},
	}
	s := NewProviderSummarizer(mock, "m")
	_, err := s.Summarize(context.Background(), "sys", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected wrapped error, got: %v", err)
	}
}

func TestProviderSummarizer_NilProvider(t *testing.T) {
	s := &ProviderSummarizer{Provider: nil}
	_, err := s.Summarize(context.Background(), "sys", nil)
	if err == nil {
		t.Fatal("expected error when provider is nil")
	}
}
