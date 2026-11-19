package provider

import (
	"errors"
	"testing"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/libs/security/secrets/secretstest"
	"github.com/hollis-labs/nanite/internal/provider/anthropic"
)

func TestNewAnthropicResolvesEnvWithDefaultConfig(t *testing.T) {
	secretstest.Install(t)
	t.Setenv("NANITE_PROVIDER_KEY_ENV", "1")
	t.Setenv("ANTHROPIC_API_KEY", "fixture-environment-key")
	p, err := New(Config{Provider: "anthropic"})
	if err != nil {
		t.Fatal(err)
	}
	// The adapter resolves its key before validating the request model.
	// Omitting the model exercises lazy resolution without an HTTP request.
	_, err = p.Complete(t.Context(), llmtypes.ChatRequest{})
	if !errors.Is(err, anthropic.ErrModelRequired) {
		t.Fatalf("expected model validation after env key resolution, got %v", err)
	}
}
