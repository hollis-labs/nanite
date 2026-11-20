// Package providers builds the model providers Nanite runs on, for a host
// that wants one outside an agent: a live eval, a workbench, a one-off
// completion. agent.Open builds the same providers itself from host.yaml and
// providers.yaml; a host passes one here only to choose it explicitly
// (agent.Options.Provider).
//
// A provider is built with a reference to its key, never the key: the key is
// resolved on each request through a libs secrets resolver and added to a
// redaction scope.
package providers

import (
	"github.com/hollis-labs/libs/core/panics"
	"github.com/hollis-labs/libs/llm/core/llmcontracts"
	"github.com/hollis-labs/libs/security/redact"
	"github.com/hollis-labs/libs/security/secrets"
	"github.com/hollis-labs/nanite/agent/authored"
	"github.com/hollis-labs/nanite/internal/provider"
)

// Built-in provider names (authored.BuiltinProviders lists them).
const (
	Anthropic = provider.BuiltinAnthropic
	OpenAI    = provider.BuiltinOpenAI
)

// Config selects a built-in provider and how it is reached.
type Config struct {
	// Name is the built-in provider: Anthropic or OpenAI.
	Name string
	// KeyRef is a secrets reference (keychain://..., env://..., file://...)
	// for the key. The key is looked up there first, then at
	// keychain://nanite/provider/<Name>, then, only when
	// NANITE_PROVIDER_KEY_ENV=1, in ANTHROPIC_API_KEY or OPENAI_API_KEY.
	KeyRef string
	// BaseURL, Headers and Auth send requests through a model gateway, as
	// agent.Gateway does; empty BaseURL uses the provider's own endpoint.
	// Headers must not carry credentials.
	BaseURL string
	Headers map[string]string
	Auth    string
	Options
}

// Options are what every provider shares.
type Options struct {
	// Secrets resolves key references; nil is the standard resolver.
	Secrets *secrets.Resolver
	// Redactor receives each resolved key; nil is a new scope.
	Redactor *redact.Scope
	// Panics contains panics in the provider's stream goroutines; nil
	// withholds panic values and reports to standard error.
	Panics *panics.Guard
}

// New builds a built-in provider. It fails on an unknown name or an invalid
// gateway; a key that cannot be resolved fails the first request instead.
func New(cfg Config) (llmcontracts.Provider, error) {
	return provider.New(provider.Config{
		Provider: cfg.Name,
		Gateway:  provider.Gateway{BaseURL: cfg.BaseURL, Headers: cfg.Headers, KeyRef: cfg.KeyRef, Auth: cfg.Auth},
		Secrets:  cfg.Secrets, Redactor: cfg.Redactor, Panics: cfg.Panics,
	})
}

// NewNamed builds a named provider from its spec, as providers.yaml or
// agent.Options.Providers define one: an endpoint, a wire (anthropic,
// openai, openai-chat or openai-responses) and a key reference. The spec is
// validated first.
func NewNamed(spec authored.ProviderSpec, opts Options) (llmcontracts.Provider, error) {
	return provider.NewNamed(spec, provider.Config{Secrets: opts.Secrets, Redactor: opts.Redactor, Panics: opts.Panics})
}
