package service

import (
	"context"
	"sync"

	"github.com/hollis-labs/nanite/internal/secrets"
	"github.com/hollis-labs/nanite/internal/store"
)

// ProviderConfigService is the transport-facing home for the operator's
// provider and model configuration rows. It reads and writes the same
// ProviderStore the chat service resolves providers and models through.
//
// It is a pass-through today. How the provider pickers merge these rows
// with the registered-provider catalog, which rows are visible, and how
// key/registration status is composed still live in the API handlers,
// because each endpoint shapes them differently (CW-20260930-0083).
type ProviderConfigService struct {
	store ProviderStore

	// Seams for TestConnection and SetAPIKey; tests replace them.
	resolveKey   func(providerID, envKey string) (key, source string)
	newVerifier  func(spec APIProviderSpec, key string) keyVerifier
	setSecret    func(key, value string) error
	deleteSecret func(key string) error

	// keyMu serializes SetAPIKey and guards registry/catalog, the live
	// provider runtime it swaps adapters in (SetProviderRuntime).
	keyMu    sync.Mutex
	registry providerRegistry
	catalog  providerCatalogWriter
}

func NewProviderConfigService(st ProviderStore) *ProviderConfigService {
	return &ProviderConfigService{
		store:        st,
		resolveKey:   ResolveAPIKey,
		newVerifier:  defaultKeyVerifier,
		setSecret:    secrets.Set,
		deleteSecret: secrets.DeleteChecked,
	}
}

// List returns every provider row.
func (s *ProviderConfigService) List(ctx context.Context) ([]store.ProviderConfig, error) {
	return s.store.ListProviders(ctx)
}

// Get returns one provider row.
func (s *ProviderConfigService) Get(ctx context.Context, id string) (*store.ProviderConfig, error) {
	return s.store.GetProvider(ctx, id)
}

// Update applies the non-nil fields of u to a provider row. An unknown id
// is not an error; it updates nothing.
func (s *ProviderConfigService) Update(ctx context.Context, id string, u store.ProviderUpdate) error {
	return s.store.UpdateProvider(ctx, id, u)
}

// ListModels returns every model row, joined with its provider's type.
func (s *ProviderConfigService) ListModels(ctx context.Context) ([]store.Model, error) {
	return s.store.ListModels(ctx)
}
