// Package provider wires native model adapters and gateway credentials.
//
// # Empty output
//
// Every adapter follows one contract for a model that produces nothing:
//
//   - Complete returns an error wrapping failure.ErrEmptyResponse when the
//     response has no usable text: no choices, no text blocks, or only
//     whitespace. It never returns "" with a nil error. This holds for the
//     Anthropic Messages adapter and both OpenAI paths (Chat Completions and
//     Responses). Callers that can tolerate emptiness test for it with
//     errors.Is.
//   - StreamChat reports a stream that ends normally with no text and no
//     tool calls the same way on every adapter: a usage event (when the
//     provider sends one) and then EventDone, with no error. An empty step is
//     not a failure on the streaming path, because a model may legitimately
//     end its turn with nothing to add after a tool result. The engine
//     finishes such a turn with an empty reply.
package provider

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"github.com/hollis-labs/libs/core/panics"
	"github.com/hollis-labs/libs/llm/core/llmcontracts"
	"github.com/hollis-labs/libs/security/redact"
	"github.com/hollis-labs/libs/security/secrets"
	"github.com/hollis-labs/nanite/internal/provider/anthropic"
	"github.com/hollis-labs/nanite/internal/provider/openai"
	"github.com/hollis-labs/nanite/internal/provider/station"
	"github.com/hollis-labs/nanite/internal/warden"
)

type Gateway struct {
	BaseURL string
	Headers map[string]string
	KeyRef  string
	Auth    string
}

type Config struct {
	Provider string
	Gateway  Gateway
	Secrets  *secrets.Resolver
	Redactor *redact.Scope
	// Panics contains panics in the adapters' stream goroutines. Nil uses a
	// guard that withholds panic values and reports to standard error.
	Panics *panics.Guard
	// Warden mints the tokens for named providers with auth warden; nil
	// when the agent has no identity.yaml warden section.
	Warden *warden.Source
}

func New(cfg Config) (llmcontracts.Provider, error) {
	gateway := station.Settings{BaseURL: cfg.Gateway.BaseURL, Headers: maps.Clone(cfg.Gateway.Headers), Auth: cfg.Gateway.Auth}
	if gateway.BaseURL != "" || len(gateway.Headers) > 0 || gateway.Auth != "" {
		if err := gateway.Validate(); err != nil {
			return nil, err
		}
	}
	if cfg.Redactor == nil {
		cfg.Redactor = redact.NewScope()
	}
	resolver := cfg.Secrets
	if resolver == nil {
		resolver = secrets.New()
	}
	load := func(ctx context.Context) (string, error) {
		value, st, err := ResolveKey(ctx, cfg.Provider, cfg.Gateway, resolver)
		if err != nil {
			return "", err
		}
		label := "provider key"
		if name, ok := strings.CutPrefix(st.Source, "env:"); ok {
			label = name
		}
		cfg.Redactor.Add(label, value)
		return value, nil
	}
	switch cfg.Provider {
	case "anthropic":
		client := anthropic.New()
		client.SetPanicGuard(cfg.Panics)
		if gateway.BaseURL != "" {
			client.SetGateway(gateway)
		}
		client.SetKeyFunc(load)
		return client, nil
	case "openai":
		client := openai.New("", nil)
		client.SetPanicGuard(cfg.Panics)
		if gateway.BaseURL != "" {
			client.SetGateway(gateway)
		}
		client.SetKeyFunc(load)
		return client, nil
	default:
		return nil, fmt.Errorf("unsupported provider %q", cfg.Provider)
	}
}
