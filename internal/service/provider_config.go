package service

import (
	"context"
	"io"
	"sync"

	secrets "github.com/hollis-labs/libs/security/secrets"

	"github.com/hollis-labs/nanite/internal/llm/station"
	"github.com/hollis-labs/nanite/internal/providerkeys"
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

	// keys resolves and writes provider keys by reference; newVerifier builds
	// the throwaway adapter TestConnection checks a key on. Tests replace both.
	keys        providerKeys
	newVerifier func(spec APIProviderSpec, key string) keyVerifier

	// keyMu serializes SetAPIKey and guards registry/catalog, the live
	// provider runtime it swaps adapters in (SetProviderRuntime).
	keyMu    sync.Mutex
	registry providerRegistry
	catalog  providerCatalogWriter
}

// providerKeys is the part of *providerkeys.Manager the service uses.
type providerKeys interface {
	Resolve(ctx context.Context, providerID string) (secrets.Secret, providerkeys.Report, error)
	Status(ctx context.Context, providerID string) providerkeys.Status
	Func(providerID string) func(context.Context) (string, error)
	SetKey(ctx context.Context, providerID string, r io.Reader) error
	ClearKey(ctx context.Context, providerID string) error
}

// NewProviderConfigService returns the service. keys resolves provider keys;
// nil builds the default manager over the OS keychain and st's key_ref
// settings.
func NewProviderConfigService(st ProviderStore, keys ...providerKeys) *ProviderConfigService {
	s := &ProviderConfigService{store: st, newVerifier: defaultKeyVerifier}
	if len(keys) > 0 && keys[0] != nil {
		s.keys = keys[0]
	} else {
		s.keys = DefaultProviderKeys(st)
	}
	return s
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
	if u.Settings != nil {
		if err := providerkeys.ValidateSettings(*u.Settings); err != nil {
			return err
		}
	}
	if u.Settings != nil {
		if spec, ok := APIProviderSpecByID(id); ok && spec.Station {
			if err := station.ValidateSettings(*u.Settings); err != nil {
				return err
			}
		}
	}
	if err := s.store.UpdateProvider(ctx, id, u); err != nil {
		return err
	}
	if u.Settings != nil {
		s.reregisterAfterSettings(ctx, id)
	}
	return nil
}

// ListModels returns every model row, joined with its provider's type.
func (s *ProviderConfigService) ListModels(ctx context.Context) ([]store.Model, error) {
	return s.store.ListModels(ctx)
}
