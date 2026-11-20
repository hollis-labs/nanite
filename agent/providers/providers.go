// Package providers builds the model providers Nanite runs on, for a host
// that wants one outside an agent: a live eval, a workbench, a one-off
// completion. agent.Open builds the same providers itself from host.yaml and
// providers.yaml; a host passes one here only to choose it explicitly
// (agent.Options.Provider).
//
// A provider is built with a reference to its key, never the key: the key is
// resolved on each request through a libs secrets resolver. Each resolved key
// is added to Options.Redactor. Pass the scope your logs and traces are
// scrubbed with (redact.New(redact.WithScope(scope))); with no Redactor the
// key goes to a scope nothing reads, and nothing scrubs it.
package providers

import (
	"github.com/hollis-labs/libs/core/panics"
	"github.com/hollis-labs/libs/llm/core/llmcontracts"
	"github.com/hollis-labs/libs/security/redact"
	"github.com/hollis-labs/libs/security/secrets"
	"github.com/hollis-labs/nanite/agent"
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
	// Gateway is how requests are sent, as for agent.Open:
	//   - KeyRef is a secrets reference (keychain://..., env://...,
	//     file://...) for the key, with or without a BaseURL. The key is
	//     looked up there first, then at keychain://nanite/provider/<Name>,
	//     then, only when NANITE_PROVIDER_KEY_ENV=1, in ANTHROPIC_API_KEY or
	//     OPENAI_API_KEY. With a third-party BaseURL, make sure KeyRef
	//     resolves: if it does not, the provider's own key is sent there.
	//   - BaseURL routes requests through a gateway; empty uses the
	//     provider's own endpoint. Headers and Auth need a BaseURL.
	//   - Headers are sent on every request and must not carry credentials.
	//   - Auth is "" (the wire's own scheme) or "bearer", which sends the
	//     key as a bearer token on the Anthropic wire; OpenAI always uses a
	//     bearer token.
	Gateway agent.Gateway
	// Options are shared with NewNamed.
	Options Options
}

// Options are what every provider shares.
type Options struct {
	// Secrets resolves key references; nil is the standard resolver.
	Secrets *secrets.Resolver
	// Redactor receives each resolved key. Pass the scope the host scrubs
	// with; nil is a new scope that nothing reads.
	Redactor *redact.Scope
	// Panics contains panics in the provider's stream goroutines; nil
	// withholds panic values and reports to standard error.
	Panics *panics.Guard
}

// New builds a built-in provider. It fails on an unknown name or an invalid
// gateway; a key that cannot be resolved fails the first request instead.
func New(cfg Config) (llmcontracts.Provider, error) {
	return provider.New(provider.Config{Provider: cfg.Name, Gateway: cfg.Gateway,
		Secrets: cfg.Options.Secrets, Redactor: cfg.Options.Redactor, Panics: cfg.Options.Panics})
}

// NewNamed builds a named provider from its spec, as providers.yaml or
// agent.Options.Providers define one: an endpoint, a wire (authored.Wire*)
// and a key reference. The spec is validated first.
func NewNamed(spec authored.ProviderSpec, opts Options) (llmcontracts.Provider, error) {
	return provider.NewNamed(spec, provider.Config{Secrets: opts.Secrets, Redactor: opts.Redactor, Panics: opts.Panics})
}
