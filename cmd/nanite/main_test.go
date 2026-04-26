package main

import (
	"context"
	"testing"

	"github.com/hollis-labs/go-providers/provider"
)

type stubProvider struct{}

func (stubProvider) StreamChat(context.Context, provider.ChatRequest) (<-chan provider.StreamEvent, error) {
	return nil, nil
}

func (stubProvider) Complete(context.Context, provider.ChatRequest) (string, error) {
	return "", nil
}

func (stubProvider) Capabilities() provider.ProviderCapabilities {
	return provider.ProviderCapabilities{}
}

func TestRegisterLegacyPTYAliasPrefersRegisteredClaudeProvider(t *testing.T) {
	registry := provider.NewRegistry()
	claudePTY := stubProvider{}
	registry.Register("pty-claude", claudePTY)

	registerLegacyPTYAlias(registry)

	got, ok := registry.Get("pty")
	if !ok {
		t.Fatal("expected pty alias to be registered")
	}
	if got != claudePTY {
		t.Fatal("expected pty alias to reuse registered pty-claude provider")
	}
}
