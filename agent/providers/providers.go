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
//
// A named provider with auth warden sends a short-lived Warden token instead
// of a key. Build a Warden once from the agent's identity.yaml (NewWarden) and
// pass it in Options.Warden to every provider that needs one, so they share
// one token cache.
package providers

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/hollis-labs/libs/core/panics"
	"github.com/hollis-labs/libs/llm/core/llmcontracts"
	"github.com/hollis-labs/libs/security/redact"
	"github.com/hollis-labs/libs/security/secrets"
	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/agent/authored"
	internalauthored "github.com/hollis-labs/nanite/internal/authored"
	"github.com/hollis-labs/nanite/internal/provider"
	"github.com/hollis-labs/nanite/internal/warden"
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
	// Warden mints tokens for a named provider with auth warden (NewNamed);
	// such a provider cannot be built without it. Built-in providers (New)
	// ignore it.
	Warden *Warden
}

// Warden mints and renews the short-lived Warden tokens of providers with
// auth warden, caching one token per audience and scope set. Each mint
// rotates the agent's parent credential under the same file lock Nanite and
// the Warden client take, so a Warden can share its credential file with a
// running agent.
type Warden struct{ src *warden.Source }

// WardenOptions are a Warden's hooks.
type WardenOptions struct {
	// Redactor receives every token and parent credential minted. Pass the
	// same scope as Options.Redactor; nil is a new scope that nothing reads.
	Redactor *redact.Scope
	// Logger reports mints and grant requests, never their secrets; nil
	// discards them.
	Logger *slog.Logger
	// OnAuthWaiting sees each grant request filed, or found still pending,
	// because the agent has no grant for a provider's audience. The request
	// that needed the token fails with an error naming it.
	OnAuthWaiting func(agent.AuthWaiting)
}

// NewWarden builds a Warden from an agent's identity (authored.LoadIdentity),
// which must have a warden section. It reads nothing until the first token
// is needed.
func NewWarden(id authored.Identity, opts WardenOptions) (*Warden, error) {
	if id.Warden == nil {
		return nil, errors.New("providers: identity has no warden section")
	}
	if err := id.Validate(""); err != nil {
		return nil, fmt.Errorf("providers: identity: %w", err)
	}
	var onWaiting func(warden.Waiting)
	if opts.OnAuthWaiting != nil {
		onWaiting = func(w warden.Waiting) { opts.OnAuthWaiting(agent.AuthWaiting(w)) }
	}
	src, err := internalauthored.NewWardenSource(id, opts.Redactor, opts.Logger, onWaiting)
	if err != nil {
		return nil, err
	}
	return &Warden{src: src}, nil
}

// New builds a built-in provider. It fails on an unknown name or an invalid
// gateway; a key that cannot be resolved fails the first request instead.
func New(cfg Config) (llmcontracts.Provider, error) {
	return provider.New(provider.Config{Provider: cfg.Name, Gateway: cfg.Gateway,
		Secrets: cfg.Options.Secrets, Redactor: cfg.Options.Redactor, Panics: cfg.Options.Panics})
}

// NewNamed builds a named provider from its spec, as providers.yaml or
// agent.Options.Providers define one: an endpoint, a wire (authored.Wire*)
// and a key reference, or auth warden with an audience and scopes. The spec
// is validated first. Auth warden needs Options.Warden.
func NewNamed(spec authored.ProviderSpec, opts Options) (llmcontracts.Provider, error) {
	cfg := provider.Config{Secrets: opts.Secrets, Redactor: opts.Redactor, Panics: opts.Panics}
	if spec.Auth == authored.AuthWarden {
		if opts.Warden == nil {
			return nil, fmt.Errorf("provider %s: auth warden needs Options.Warden (NewWarden)", spec.Name)
		}
		cfg.Warden = opts.Warden.src
	}
	return provider.NewNamed(spec, cfg)
}
