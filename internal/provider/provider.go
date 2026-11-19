// Package provider wires native model adapters and gateway credentials.
package provider

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"

	"github.com/hollis-labs/libs/llm/core/llmcontracts"
	"github.com/hollis-labs/libs/security/redact"
	"github.com/hollis-labs/libs/security/secrets"
	"github.com/hollis-labs/nanite/internal/provider/anthropic"
	"github.com/hollis-labs/nanite/internal/provider/openai"
	"github.com/hollis-labs/nanite/internal/provider/station"
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
		var refs []string
		if cfg.Gateway.KeyRef != "" {
			refs = append(refs, cfg.Gateway.KeyRef)
		}
		refs = append(refs, "keychain://nanite/provider/"+cfg.Provider)
		var failures []error
		for _, ref := range refs {
			resolved, err := resolver.Resolve(ctx, ref)
			if err == nil {
				value := resolved.Secret.Reveal()
				cfg.Redactor.Add("provider key", value)
				return value, nil
			}
			failures = append(failures, err)
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
		}
		if os.Getenv("NANITE_PROVIDER_KEY_ENV") == "1" {
			name := "ANTHROPIC_API_KEY"
			if cfg.Provider == "openai" {
				name = "OPENAI_API_KEY"
			}
			if value := os.Getenv(name); value != "" {
				cfg.Redactor.Add(name, value)
				return value, nil
			}
		}
		return "", fmt.Errorf("resolve %s provider key: %w", cfg.Provider, errors.Join(failures...))
	}
	switch cfg.Provider {
	case "anthropic":
		client := anthropic.New()
		if gateway.BaseURL != "" {
			client.SetGateway(gateway)
		}
		client.SetKeyFunc(load)
		return client, nil
	case "openai":
		client := openai.New("", nil)
		if gateway.BaseURL != "" {
			client.SetGateway(gateway)
		}
		client.SetKeyFunc(load)
		return client, nil
	default:
		return nil, fmt.Errorf("unsupported provider %q", cfg.Provider)
	}
}
