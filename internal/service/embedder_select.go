package service

import (
	"context"
	"os"

	embedcontracts "github.com/hollis-labs/go-embed-contracts"
	nllmopenai "github.com/hollis-labs/nanite/internal/llm/openai"
	"github.com/hollis-labs/nanite/internal/secrets"
)

// Embedding status values returned by SelectEmbedder. Surfaced by the settings
// API and consumed by the first-turn warning envelope.
//
// Step 6.5 (SP-20260508-0001) reduced the embedder catalog to OpenAI only.
// "Unreachable" was an Ollama-era status indicating a probe failure against a
// local server — no remaining provider has a reachability check, so the value
// has been retired.
const (
	EmbeddingStatusActive             = "active"
	EmbeddingStatusDisabled           = "disabled"
	EmbeddingStatusMissingCredentials = "missing_credentials"
)

// EmbedderSettings is the subset of UserSettings consumed by SelectEmbedder.
// Decoupling from store.UserSettings keeps the helper testable without a store.
type EmbedderSettings struct {
	Mode     string // "disabled" | "explicit"
	Provider string // openai | ""
	Model    string
}

// SupportedEmbeddingProviders is the ordered list of provider IDs surfaced in
// the Settings UI dropdown. Step 6.5 (SP-20260508-0001) reduced this set to
// OpenAI only — non-OpenAI vendors (azure_openai, gemini, mistral, ollama)
// are out of release scope per parent CW-20260508-0008.
var SupportedEmbeddingProviders = []string{
	"openai",
}

// defaultEmbeddingModels maps each supported provider to the model used when
// the user hasn't specified one explicitly. Keeps container wiring coherent
// (a provider with an empty model would otherwise call Embed with "" and fail).
var defaultEmbeddingModels = map[string]string{
	"openai": "text-embedding-3-large",
}

// IsSupportedEmbeddingProvider reports whether id is one of the embedding-
// capable providers surfaced in the Settings UI.
func IsSupportedEmbeddingProvider(id string) bool {
	for _, p := range SupportedEmbeddingProviders {
		if p == id {
			return true
		}
	}
	return false
}

// EmbedderSelectDeps are overridable hooks for SelectEmbedder. Production code
// uses the defaults (DefaultEmbedderSelectDeps); tests inject fakes.
type EmbedderSelectDeps struct {
	// LookupSecret returns the API key for the given provider ID, or "" if absent.
	LookupSecret func(providerID string) string
	// Getenv returns the named environment variable; defaults to os.Getenv.
	Getenv func(key string) string
}

// DefaultEmbedderSelectDeps wires SelectEmbedder against live keychain and
// environment.
func DefaultEmbedderSelectDeps() EmbedderSelectDeps {
	return EmbedderSelectDeps{
		LookupSecret: func(providerID string) string {
			return secrets.Get(secrets.ProviderKeyName(providerID))
		},
		Getenv: os.Getenv,
	}
}

// SelectEmbedder resolves an Embedder from user settings. Returns nil and a
// non-"active" status when no embedder should be wired; returns a configured
// Embedder and EmbeddingStatusActive when it should.
//
// Behavior:
//   - Mode "" or "disabled" → (nil, "", "disabled").
//   - Provider "" → (nil, "", "disabled").
//   - Provider unsupported → (nil, "", "disabled") — silently treated as off so
//     a stale DB value can't crash the container.
//   - Credential-requiring provider with no key → (nil, model, "missing_credentials").
//   - All checks pass → (embedder, model, "active").
func SelectEmbedder(ctx context.Context, s EmbedderSettings, deps EmbedderSelectDeps) (embedcontracts.Embedder, string, string) {
	_ = ctx
	if s.Mode == "" || s.Mode == "disabled" {
		return nil, "", EmbeddingStatusDisabled
	}
	if s.Provider == "" {
		return nil, "", EmbeddingStatusDisabled
	}
	if !IsSupportedEmbeddingProvider(s.Provider) {
		return nil, "", EmbeddingStatusDisabled
	}

	model := s.Model
	if model == "" {
		model = defaultEmbeddingModels[s.Provider]
	}

	switch s.Provider {
	case "openai":
		key := deps.LookupSecret("openai-001")
		if key == "" {
			key = deps.Getenv("OPENAI_API_KEY")
		}
		if key == "" {
			return nil, model, EmbeddingStatusMissingCredentials
		}
		// CW-20260508-0012: SDK-backed Embedder (replaces deleted go-providers
		// HTTP openai client). Implements embedcontracts.Embedder; nil http
		// client uses the openai-go default, matching prior behavior.
		return nllmopenai.NewEmbedder(key, nil), model, EmbeddingStatusActive
	}

	return nil, model, EmbeddingStatusDisabled
}
