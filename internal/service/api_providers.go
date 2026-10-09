package service

import (
	"log/slog"
	"os"
	"strconv"
	"strings"

	nllmanthropic "github.com/hollis-labs/nanite/internal/llm/anthropic"
	nllmopenai "github.com/hollis-labs/nanite/internal/llm/openai"
	"github.com/hollis-labs/nanite/internal/secrets"
	llmcontracts "github.com/hollis-labs/substrate/llm-core/llmcontracts"
)

// Key sources reported by ResolveAPIKey.
const (
	APIKeySourceKeychain    = "keychain"
	APIKeySourceEnvironment = "environment"
)

// APIProviderSpec is one HTTP API provider Nanite has an adapter for.
//
// CW-20260526-0001: the spec carries the catalog metadata (DisplayName,
// ProviderID) inline with the registry registration so the dropdown
// auto-surfaces every successfully-registered provider. A new API provider
// is one literal in APIProviderSpecs instead of (a) initProviders +
// (b) seededProviders + (c) AllSeeded.
//
// It lives here rather than in cmd/nanite so the provider key endpoints can
// build the same adapters at runtime that startup builds (CW-20260930-0101).
type APIProviderSpec struct {
	// Name is the registry name, which is also the provider row's
	// provider_type.
	Name        string
	DisplayName string
	// ProviderID is the seeded provider row id, and names its keychain entry
	// (secrets.ProviderKeyName).
	ProviderID string
	// EnvKey is the conventional environment variable for this provider,
	// used when the keychain has nothing — see ResolveAPIKey.
	EnvKey string

	create func() llmcontracts.Provider
	setKey func(llmcontracts.Provider, string)
}

// NewProvider builds a fresh adapter authenticated with key.
func (s APIProviderSpec) NewProvider(key string) llmcontracts.Provider {
	p := s.create()
	s.setKey(p, key)
	return p
}

// APIProviderSpecs returns every HTTP API provider Nanite can register, in
// registration order.
func APIProviderSpecs() []APIProviderSpec {
	return []APIProviderSpec{
		{
			Name: "anthropic", DisplayName: "Anthropic", ProviderID: "anthropic-001", EnvKey: "ANTHROPIC_API_KEY",
			create: func() llmcontracts.Provider {
				ap := nllmanthropic.New()
				if v := os.Getenv("NANITE_PROVIDER_RATE_BUDGET_TPM"); v != "" {
					if n, err := strconv.Atoi(v); err == nil && n > 0 {
						ap.RateTracker.UpdateLimit(n)
						slog.Info("provider: rate-budget override applied via env",
							"provider", "anthropic", "tpm", n)
					}
				}
				return ap
			},
			setKey: func(p llmcontracts.Provider, k string) { p.(*nllmanthropic.Client).SetAPIKey(k) },
		},
		{
			Name: "openai", DisplayName: "OpenAI", ProviderID: "openai-001", EnvKey: "OPENAI_API_KEY",
			// CW-20260508-0012: SDK-backed wrapper (replaces deleted
			// go-providers HTTP openai client). Implements
			// llmcontracts.Provider; no rate-budget plumbing per spike
			// verdict (parity deferred to followup
			// followups.nanite.cw_20260508_0012.openai_rate_budget_parity).
			create: func() llmcontracts.Provider { return nllmopenai.New("", nil) },
			setKey: func(p llmcontracts.Provider, k string) { p.(*nllmopenai.Client).SetAPIKey(k) },
		},
	}
}

// APIProviderSpecByID returns the spec whose seeded row id is providerID.
func APIProviderSpecByID(providerID string) (APIProviderSpec, bool) {
	for _, spec := range APIProviderSpecs() {
		if spec.ProviderID == providerID {
			return spec, true
		}
	}
	return APIProviderSpec{}, false
}

// ResolveAPIKey returns the key for a provider row and where it came from:
// keychain first, then environment, or ("", "") when neither has one.
//
// A container has no OS keyring — the secret-service lookup fails with
// `exec: "dbus-launch": executable file not found in $PATH` — so a
// keychain-only lookup registers no providers at all, and chat is dead with
// only a WARN to say so. The environment is how a container is configured;
// refusing to read it makes Nanite unrunnable anywhere but a desktop.
func ResolveAPIKey(providerID, envKey string) (key, source string) {
	return resolveAPIKey(secrets.Get, os.Getenv, providerID, envKey)
}

func resolveAPIKey(secret, getenv func(string) string, providerID, envKey string) (string, string) {
	if k := strings.TrimSpace(secret(secrets.ProviderKeyName(providerID))); k != "" {
		return k, APIKeySourceKeychain
	}
	if envKey != "" {
		if k := strings.TrimSpace(getenv(envKey)); k != "" {
			return k, APIKeySourceEnvironment
		}
	}
	return "", ""
}
