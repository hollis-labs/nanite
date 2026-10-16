package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	llmcontracts "github.com/hollis-labs/libs/llm/core/llmcontracts"
	secrets "github.com/hollis-labs/libs/security/secrets"
	"github.com/hollis-labs/nanite/internal/providercatalog"
	"github.com/hollis-labs/nanite/internal/providerkeys"
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
	Get(name string) (providercatalog.Entry, bool)
	Add(e providercatalog.Entry)
	Remove(name string) bool
}

// ProviderCredentialStoreError exposes safe recovery guidance without returning
// the backend's error text (which may include credentials or host details).
// Unwrap retains the original cause for internal error classification.
type ProviderCredentialStoreError struct {
	Operation           string
	EnvironmentVariable string
	Cause               error
}

func (e *ProviderCredentialStoreError) Error() string {
	if errors.Is(e.Cause, providerkeys.ErrReadOnlyRef) || errors.Is(e.Cause, providerkeys.ErrInvalidKeyRef) {
		return fmt.Sprintf("could not %s the provider credential: %s. The running provider was not changed", e.Operation, e.Cause)
	}
	var se *secrets.Error
	if errors.As(e.Cause, &se) {
		return fmt.Sprintf("could not %s the provider credential: %s (%s). The credential update could not be confirmed; the running provider was not changed", e.Operation, se.Guidance(), se.Code())
	}
	guidance := "configure an OS credential store, or set a key_ref to a reachable secret reference"
	if e.EnvironmentVariable != "" {
		guidance = fmt.Sprintf("configure an OS credential store, or enable %s=1 and set %s in the service environment", providerkeys.EnvEnableVar, e.EnvironmentVariable)
	}
	return fmt.Sprintf("could not %s the provider credential: credential store unavailable; %s. The credential update could not be confirmed; the running provider was not changed", e.Operation, guidance)
}
func (e *ProviderCredentialStoreError) Unwrap() error { return e.Cause }

func credentialStoreError(id, operation string, cause error) error {
	return &ProviderCredentialStoreError{Operation: operation, EnvironmentVariable: ProviderKeyEnvName(id), Cause: cause}
}

// APIKeyResult is what SetAPIKey reports back.
type APIKeyResult struct {
	// HasKey is whether a key was saved (false when cleared).
	HasKey bool
	// KeySource is where the key the provider now runs on comes from, as the
	// resolver names it: "keychain", "keychain:legacy:nanite", "helper:<n>",
	// "file", "op", "env:<VAR>", or "" when it has none. Clearing the saved
	// key while an enabled environment variable is set leaves the provider
	// running on "env:<VAR>", as the next boot would.
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

// SetAPIKey stores (or, for an empty key, deletes) a provider's saved key
// through its key reference and makes the result live without a restart
// (CW-20260930-0101).
//
// For a provider Nanite has an adapter for, the key is re-resolved the way
// startup resolves it (stored ref, keychain default, opt-in environment) and:
//   - a usable key registers a freshly built adapter under the provider's
//     name, replacing the old one. The adapter fetches its key when first
//     used; the old adapter is never mutated, so a turn already running
//     finishes on the old key. The fresh adapter starts with a new rate
//     tracker and circuit breaker, which fits a key that may belong to
//     another org;
//   - no key at all unregisters the provider and drops it from the catalog.
//
// Any other row only has its key written.
//
// The whole sequence runs under one lock so two saves cannot interleave one's
// write with the other's registration.
func (s *ProviderConfigService) SetAPIKey(ctx context.Context, id, key string) (APIKeyResult, error) {
	s.keyMu.Lock()
	defer s.keyMu.Unlock()

	if err := ctx.Err(); err != nil {
		return APIKeyResult{}, err
	}
	if key == "" {
		if err := s.keys.ClearKey(ctx, id); err != nil {
			return APIKeyResult{}, credentialStoreError(id, "clear", err)
		}
	} else if err := s.keys.SetKey(ctx, id, strings.NewReader(key)); err != nil {
		return APIKeyResult{}, credentialStoreError(id, "save", err)
	}
	result := APIKeyResult{HasKey: key != ""}

	spec, ok := APIProviderSpecByID(id)
	if !ok {
		if key != "" {
			result.KeySource = "keychain"
		}
		return result, nil
	}

	status := s.keys.Status(ctx, id)
	result.KeySource = status.Source
	if s.registry == nil {
		return result, nil
	}
	if status.Usable {
		s.registry.Register(spec.Name, spec.NewLazyProvider(s.keys.Func(spec.ProviderID)))
		if s.catalog != nil {
			s.addToCatalogInSpecOrder(spec)
		}
		slog.InfoContext(ctx, "provider re-registered with new key", "provider", spec.Name, "key_source", status.Source)
		return result, nil
	}
	s.registry.Unregister(spec.Name)
	if s.catalog != nil {
		s.catalog.Remove(spec.Name)
	}
	slog.InfoContext(ctx, "provider unregistered: no key left", "provider", spec.Name, "reason", status.Code)
	return result, nil
}

// addToCatalogInSpecOrder upserts spec's catalog entry. Catalog.Add appends a
// new name, so a provider cleared and then saved again would move to the end
// of the pickers; instead the entries of later specs are lifted and re-added
// after it, keeping startup's registration order.
func (s *ProviderConfigService) addToCatalogInSpecOrder(spec APIProviderSpec) {
	entry := providercatalog.Entry{Name: spec.Name, DisplayName: spec.DisplayName, RowID: spec.ProviderID}
	if _, ok := s.catalog.Get(spec.Name); ok {
		s.catalog.Add(entry)
		return
	}
	specs := APIProviderSpecs()
	var later []providercatalog.Entry
	seen := false
	for _, sp := range specs {
		if sp.Name == spec.Name {
			seen = true
			continue
		}
		if !seen {
			continue
		}
		if e, ok := s.catalog.Get(sp.Name); ok {
			later = append(later, e)
			s.catalog.Remove(sp.Name)
		}
	}
	s.catalog.Add(entry)
	for _, e := range later {
		s.catalog.Add(e)
	}
}
