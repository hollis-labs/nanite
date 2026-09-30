package service

import (
	"context"

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

	// Seams for TestConnection; tests replace them.
	resolveKey  func(providerID, envKey string) (key, source string)
	newVerifier func(spec APIProviderSpec, key string) keyVerifier
}

func NewProviderConfigService(st ProviderStore) *ProviderConfigService {
	return &ProviderConfigService{
		store:       st,
		resolveKey:  ResolveAPIKey,
		newVerifier: defaultKeyVerifier,
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
