package service

import (
	"context"
	"log/slog"

	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	"github.com/hollis-labs/nanite/internal/providercatalog"
	"github.com/hollis-labs/nanite/internal/secrets"
)

// providerRegistry is the part of provider.Registry that SetAPIKey swaps.
type providerRegistry interface {
	Register(name string, p llmcontracts.Provider)
	Unregister(name string) bool
}

// providerCatalogWriter is the part of providercatalog.Catalog that SetAPIKey
// keeps in step with the registry, so the model pickers list exactly the
// providers chat can use. A typed-nil *providercatalog.Catalog
// is fine: its methods are nil-receiver safe.
type providerCatalogWriter interface {
	Add(e providercatalog.Entry)
	Remove(name string) bool
}

// APIKeyResult is what SetAPIKey reports back.
type APIKeyResult struct {
	// HasKey is whether a keychain key was saved (false when cleared).
	HasKey bool
	// KeySource is where the key the provider now runs on comes from:
	// APIKeySourceKeychain, APIKeySourceEnvironment, or "" when it has none.
	// Clearing the keychain key while the provider's env var is set leaves it
	// running on the environment key, as the next boot would.
	KeySource string
}

// SetProviderRuntime wires the live provider registry and catalog that
// SetAPIKey updates. Without it SetAPIKey only writes the keychain, and a new
// key takes effect at the next boot.
func (s *ProviderConfigService) SetProviderRuntime(reg providerRegistry, cat providerCatalogWriter) {
	s.keyMu.Lock()
	defer s.keyMu.Unlock()
	s.registry = reg
	s.catalog = cat
}

// SetAPIKey stores (or, for an empty key, deletes) a provider row's keychain
// key and makes it live without a restart (CW-20260930-0101).
//
// For a provider Nanite has an adapter for, the key is re-resolved the way
// startup resolves it — keychain, then environment — and:
//   - a resolved key registers a freshly built adapter under the provider's
//     name, replacing the old one. The old adapter is never mutated: a turn
//     already running holds it and finishes on the old key; the next turn
//     gets the new one. The fresh adapter starts with a new rate tracker and
//     circuit breaker, which fits a key that may belong to another org;
//   - no key at all unregisters the provider and drops it from the catalog.
//
// Any other row only has its keychain entry written, as before.
//
// The whole sequence runs under one lock so two saves cannot interleave one's
// keychain write with the other's registration.
func (s *ProviderConfigService) SetAPIKey(ctx context.Context, id, key string) (APIKeyResult, error) {
	s.keyMu.Lock()
	defer s.keyMu.Unlock()

	keyName := secrets.ProviderKeyName(id)
	if key == "" {
		s.deleteSecret(keyName)
	} else if err := s.setSecret(keyName, key); err != nil {
		return APIKeyResult{}, err
	}
	result := APIKeyResult{HasKey: key != ""}

	spec, ok := APIProviderSpecByID(id)
	if !ok {
		if key != "" {
			result.KeySource = APIKeySourceKeychain
		}
		return result, nil
	}

	resolved, source := s.resolveKey(spec.ProviderID, spec.EnvKey)
	result.KeySource = source
	if s.registry == nil {
		return result, nil
	}
	if resolved != "" {
		s.registry.Register(spec.Name, spec.NewProvider(resolved))
		if s.catalog != nil {
			s.catalog.Add(providercatalog.Entry{Name: spec.Name, DisplayName: spec.DisplayName, RowID: spec.ProviderID})
		}
		slog.InfoContext(ctx, "provider re-registered with new key", "provider", spec.Name, "key_source", source)
		return result, nil
	}
	s.registry.Unregister(spec.Name)
	if s.catalog != nil {
		s.catalog.Remove(spec.Name)
	}
	slog.InfoContext(ctx, "provider unregistered: no key left", "provider", spec.Name)
	return result, nil
}
