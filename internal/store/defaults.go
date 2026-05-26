package store

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/hollis-labs/nanite/internal/store/seedcatalog"
)

// ErrNoDefaultModel is returned by ResolveProviderAndModel and
// DefaultModelForProvider when no chain element supplies a default. The
// remedy is operator-side: set providers.default_model (or
// user_settings.default_model) for the affected provider. Callers should
// surface this verbatim so the operator sees the actionable instruction.
var ErrNoDefaultModel = errors.New("no default model configured")

// DefaultModelForProvider returns providers.default_model for the given
// provider_type. Multiple provider rows can share a provider_type (legacy
// shape); the first row with a non-empty default_model wins.
//
// Returns ErrNoDefaultModel wrapped in a contextual error when no row
// supplies a value, so callers can pattern-match without losing the
// "which provider was being looked up" detail.
func (s *Store) DefaultModelForProvider(providerType string) (string, error) {
	if providerType == "" {
		return "", fmt.Errorf("%w: empty provider_type", ErrNoDefaultModel)
	}
	var model string
	err := s.DB.QueryRow(
		`SELECT default_model FROM providers
		 WHERE provider_type = ? AND COALESCE(default_model, '') <> ''
		 ORDER BY id
		 LIMIT 1`,
		providerType,
	).Scan(&model)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w for provider_type=%q (set providers.default_model)", ErrNoDefaultModel, providerType)
	}
	if err != nil {
		return "", fmt.Errorf("query providers.default_model: %w", err)
	}
	return model, nil
}

// ResolveProviderAndModel is the SSOT for "what provider + model should
// this call use?" Returns (provider, model, err).
//
// Walks for provider (permissive — never errors):
//
//  1. explicitProvider (caller-supplied).
//  2. user_settings.default_provider.
//  3. seedcatalog.DefaultProviderType (compile-time routing floor).
//
// Walks for model (errors if dry):
//
//  1. explicitModel (caller-supplied).
//  2. user_settings.default_model.
//  3. providers.default_model for the resolved provider_type.
//
// Returns ErrNoDefaultModel (wrapped) only when no chain element supplies
// a model. The provider return is best-effort: callers that perform their
// own provider routing (e.g. chat_generate's resolveProvider →
// chat.InferProvider) can override it based on the model string.
//
// CW-20260526-0003: introduced to replace the scattered
// `session.Model → agent.DefaultModel → models.DefaultChatModel()` chain
// that silently dead-ended on a Go literal. Operators now change defaults
// via user_settings or providers.default_model with no recompile. The
// permissive-provider shape was added in response to PR #220 review —
// hard-erroring on missing provider broke fresh installs (empty
// user_settings) and the "session has model, downstream infers provider"
// path.
func (s *Store) ResolveProviderAndModel(explicitProvider, explicitModel string) (string, string, error) {
	provider := explicitProvider
	model := explicitModel

	if provider == "" || model == "" {
		us, err := s.GetUserSettings()
		if err != nil {
			return "", "", fmt.Errorf("resolve defaults: load user_settings: %w", err)
		}
		if provider == "" {
			provider = us.DefaultProvider
		}
		if model == "" {
			model = us.DefaultModel
		}
	}

	// Provider falls back to the seed routing floor so a fresh install
	// (empty user_settings.default_provider, no operator config yet) still
	// returns a usable hint for the per-provider default_model lookup.
	if provider == "" {
		provider = seedcatalog.DefaultProviderType
	}

	if model == "" {
		def, err := s.DefaultModelForProvider(provider)
		if err != nil {
			return "", "", err
		}
		model = def
	}

	return provider, model, nil
}
