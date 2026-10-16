package service

import (
	"context"
	"log/slog"
	"os"
	"strconv"

	llmcontracts "github.com/hollis-labs/libs/llm/core/llmcontracts"
	nllmanthropic "github.com/hollis-labs/nanite/internal/llm/anthropic"
	nllmopenai "github.com/hollis-labs/nanite/internal/llm/openai"
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
	// EnvKey is the conventional environment variable for this provider. It is
	// read only when the operator has enabled the environment fallback
	// (providerkeys.EnvEnableVar); see internal/providerkeys.
	EnvKey string

	create  func() llmcontracts.Provider
	setKey  func(llmcontracts.Provider, string)
	lazyKey func(llmcontracts.Provider, func(context.Context) (string, error))
}

// NewProvider builds a fresh adapter authenticated with key.
func (s APIProviderSpec) NewProvider(key string) llmcontracts.Provider {
	p := s.create()
	s.setKey(p, key)
	return p
}

// NewLazyProvider builds a fresh adapter whose key is fetched by keyFn when a
// request first needs it, not now. The adapter never holds a key it has not
// been asked to use, and a failed fetch surfaces on that request as keyFn's
// own (typed) error.
func (s APIProviderSpec) NewLazyProvider(keyFn func(context.Context) (string, error)) llmcontracts.Provider {
	p := s.create()
	s.lazyKey(p, keyFn)
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
			lazyKey: func(p llmcontracts.Provider, fn func(context.Context) (string, error)) {
				p.(*nllmanthropic.Client).SetKeyFunc(fn)
			},
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
			lazyKey: func(p llmcontracts.Provider, fn func(context.Context) (string, error)) {
				p.(*nllmopenai.Client).SetKeyFunc(fn)
			},
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
