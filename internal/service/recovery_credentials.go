package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	"github.com/hollis-labs/go-providers/provider"
	nllmanthropic "github.com/hollis-labs/nanite/internal/llm/anthropic"
	nllmopenai "github.com/hollis-labs/nanite/internal/llm/openai"
	"github.com/hollis-labs/nanite/internal/recovery/broker"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
	"github.com/hollis-labs/nanite/internal/secrets"
)

// Compile-time assertion: the adapter satisfies broker.CredentialOps.
// This is the contract the broker invokes when handling
// RemediationRefreshCredentials.
var _ broker.CredentialOps = (*recoveryCredentialsAdapter)(nil)

// recoveryCredentialsAdapter satisfies broker.CredentialOps for the
// recovery broker. It is invoked when the classifier sees stderr matching
// 401 / 403 / unauthorized for a failed agent session.
//
// Credential model (verified 2026-05-09):
//
//   - Direct API providers (anthropic / openai, used by nanite's chat
//     utility paths via internal/llm/anthropic + internal/llm/openai)
//     resolve their key from the OS keychain at boot via secrets.Get,
//     then cache it on an in-memory client via SetAPIKey. Refresh =
//     re-read keychain → call SetAPIKey to rebuild the SDK with the new
//     key.
//   - CLI providers (claude / codex / opencode) spawned via agent.Boot
//     manage their own auth (e.g. `claude login`). nanite does NOT inject
//     ANTHROPIC_API_KEY etc. into their env (see internal/runtime/agent/env.go
//     inheritedEnvKeys + the "profile-derived env is Phase 4" comment).
//     There is nothing for the broker to refresh from the nanite side —
//     return an error so the broker escalates to Permanent rather than
//     issuing a no-op retry that will hit the same auth wall.
//
// keyringLookup / setAnthropicKey / setOpenAIKey are package-level seams
// so unit tests can drive the adapter without hitting the real keychain
// or the real provider clients.
type recoveryCredentialsAdapter struct {
	agents    runtimeagent.AgentProfiles
	providers *provider.Registry

	// keyringLookup mirrors secrets.Get; tests override.
	keyringLookup func(key string) string

	// providerKeyName mirrors secrets.ProviderKeyName; tests override.
	providerKeyName func(providerID string) string
}

// newRecoveryCredentialsAdapter wires the adapter against the production
// keychain + ProviderKeyName helpers. The agents resolver and the
// provider registry are required; both are passed by BuildAgentDependencies.
//
// providers may be nil in lightweight test setups where no API providers
// are wired (Refresh on a CLI profile still works; Refresh on an API
// profile errors with "registry not wired").
func newRecoveryCredentialsAdapter(agents runtimeagent.AgentProfiles, providers *provider.Registry) *recoveryCredentialsAdapter {
	return &recoveryCredentialsAdapter{
		agents:          agents,
		providers:       providers,
		keyringLookup:   secrets.Get,
		providerKeyName: secrets.ProviderKeyName,
	}
}

// providerKind enumerates the broker-relevant credential surfaces. The
// boot path registers API providers under their bare name ("anthropic" /
// "openai") and the keychain stores them under "provider-api-key:<provID>"
// (anthropic-001 / openai-001). CLI providers (claude / codex / opencode)
// are not in the API registry; nanite has no key to refresh for them.
type providerKind int

const (
	providerKindUnknown providerKind = iota
	providerKindAPIAnthropic
	providerKindAPIOpenAI
	providerKindCLIManaged
)

// classifyProvider maps the agent profile's DefaultProvider to a kind +
// the keychain provID. Mapping is intentionally strict: unknown names
// surface as providerKindUnknown so the adapter errors with a clear
// message rather than silently no-op'ing.
func classifyProvider(name string) (providerKind, string) {
	switch name {
	case "anthropic":
		return providerKindAPIAnthropic, "anthropic-001"
	case "openai":
		return providerKindAPIOpenAI, "openai-001"
	case "claude", "codex", "opencode",
		"pty-claude", "pty-codex", "pty-opencode":
		return providerKindCLIManaged, ""
	default:
		return providerKindUnknown, ""
	}
}

// Refresh re-resolves the API credentials for the named agent profile.
// Bounded by the caller's context (the broker passes a 10s timeout).
//
// Returns an error in these cases (the broker treats any non-nil err as
// a remediation failure → escalate to Permanent):
//
//   - agentProfile cannot be resolved (no panic; clean wrapped error).
//   - profile.DefaultProvider is empty or maps to an unknown provider.
//   - profile maps to a CLI-managed provider — nanite has no key to
//     refresh (returns errCLIManagedAuth so callers/tests can identify
//     this case explicitly).
//   - keychain returns an empty key for an API provider (likely user
//     never configured it, or keychain access is denied under the
//     current process's launchd context).
//   - the registered provider can't be type-asserted to a known SetAPIKey
//     surface.
//
// Returns nil on successful refresh: the API provider's cached SDK
// client is rebuilt with the freshly-read key and subsequent requests
// use the new credential.
func (a *recoveryCredentialsAdapter) Refresh(ctx context.Context, agentProfile string) error {
	if a == nil {
		return errors.New("recovery: credentials adapter is nil")
	}
	if a.agents == nil {
		return errors.New("recovery: credentials adapter: agents resolver not wired")
	}

	// Honor the caller-imposed deadline before any work — nanite's
	// keychain wrapper is synchronous (no ctx-aware variant), and the
	// SetAPIKey rebuild is in-memory + fast, but a canceled ctx should
	// short-circuit the whole flow.
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("recovery: credentials refresh aborted: %w", err)
	}

	profile, err := a.agents.GetOrDefault(agentProfile)
	if err != nil {
		return fmt.Errorf("recovery: resolve agent profile %q: %w", agentProfile, err)
	}
	if profile == nil {
		return fmt.Errorf("recovery: resolve agent profile %q: nil profile", agentProfile)
	}

	provName := profile.DefaultProvider
	if provName == "" {
		return fmt.Errorf("recovery: agent profile %q has no DefaultProvider; cannot refresh credentials",
			agentProfile)
	}

	kind, provID := classifyProvider(provName)
	switch kind {
	case providerKindCLIManaged:
		// claude / codex / opencode manage auth in their own state dirs
		// (e.g. ~/.claude/auth.json after `claude login`). nanite has no
		// hook to refresh that auth surface. Return a sentinel-shaped
		// error so the broker escalates to Permanent immediately rather
		// than retrying a session that will hit the same auth wall.
		slog.Warn("recovery: credential refresh requested for CLI-managed provider — nanite cannot remediate",
			"agent_profile", agentProfile,
			"provider", provName)
		return fmt.Errorf("recovery: provider %q manages its own auth; nanite cannot refresh credentials (run the provider's login flow)",
			provName)
	case providerKindUnknown:
		return fmt.Errorf("recovery: unknown provider %q for agent profile %q; cannot map to keychain entry",
			provName, agentProfile)
	}

	// API provider path. Re-read the keychain for the canonical provID,
	// then push the fresh key into the cached SDK client.
	if a.providers == nil {
		return errors.New("recovery: credentials adapter: provider registry not wired")
	}

	if a.keyringLookup == nil || a.providerKeyName == nil {
		return errors.New("recovery: credentials adapter: keyring seams not wired")
	}

	key := a.keyringLookup(a.providerKeyName(provID))
	if key == "" {
		return fmt.Errorf("recovery: keychain has no key for provider %q (provID %q); cannot refresh",
			provName, provID)
	}

	p, ok := a.providers.Get(provName)
	if !ok || p == nil {
		// Boot path skips Register when the keychain key was empty at
		// boot — re-reading the keychain now (key was non-empty above)
		// could in principle resurrect the provider, but that's a
		// composition-root concern (Register is package-private to
		// initProviders). For now we treat this as an error so the
		// broker escalates and the user restarts nanite to pick up the
		// newly-set key.
		return fmt.Errorf("recovery: provider %q not registered in registry; restart nanite after configuring the keychain entry",
			provName)
	}

	if err := setAPIKeyFor(kind, p, key); err != nil {
		return fmt.Errorf("recovery: set API key on provider %q: %w", provName, err)
	}

	slog.Info("recovery: credentials refreshed",
		"agent_profile", agentProfile,
		"provider", provName)
	return nil
}

// setAPIKeyFor type-asserts the registered provider to its concrete
// SetAPIKey surface. Kept distinct from Refresh so unit tests can drive
// the dispatch without registering real *anthropic.Client instances.
func setAPIKeyFor(kind providerKind, p llmcontracts.Provider, key string) error {
	switch kind {
	case providerKindAPIAnthropic:
		c, ok := p.(*nllmanthropic.Client)
		if !ok {
			return fmt.Errorf("registered anthropic provider has unexpected concrete type %T", p)
		}
		c.SetAPIKey(key)
		return nil
	case providerKindAPIOpenAI:
		c, ok := p.(*nllmopenai.Client)
		if !ok {
			return fmt.Errorf("registered openai provider has unexpected concrete type %T", p)
		}
		c.SetAPIKey(key)
		return nil
	default:
		return fmt.Errorf("setAPIKeyFor: unsupported provider kind %v", kind)
	}
}
