package service

import (
	"context"
	"os"
	"time"

	embedcontracts "github.com/hollis-labs/go-embed-contracts"
	"github.com/hollis-labs/go-providers/provider"
	nllmopenai "github.com/hollis-labs/nanite/internal/llm/openai"
	"github.com/hollis-labs/nanite/internal/secrets"
)

// Embedding status values returned by SelectEmbedder. Surfaced by the settings
// API and consumed by the first-turn warning envelope.
const (
	EmbeddingStatusActive             = "active"
	EmbeddingStatusDisabled           = "disabled"
	EmbeddingStatusMissingCredentials = "missing_credentials"
	EmbeddingStatusUnreachable        = "unreachable"
)

// EmbedderSettings is the subset of UserSettings consumed by SelectEmbedder.
// Decoupling from store.UserSettings keeps the helper testable without a store.
type EmbedderSettings struct {
	Mode     string // "disabled" | "explicit"
	Provider string // openai | azure_openai | ollama | gemini | mistral | ""
	Model    string
}

// SupportedEmbeddingProviders is the ordered list of provider IDs surfaced in
// the Settings UI dropdown. These are the providers in go-providers that
// implement the Embedder interface.
var SupportedEmbeddingProviders = []string{
	"openai",
	"azure_openai",
	"ollama",
	"gemini",
	"mistral",
}

// defaultEmbeddingModels maps each supported provider to the model used when
// the user hasn't specified one explicitly. Keeps container wiring coherent
// (a provider with an empty model would otherwise call Embed with "" and fail).
var defaultEmbeddingModels = map[string]string{
	"openai":       "text-embedding-3-large",
	"azure_openai": "text-embedding-3-large",
	"ollama":       "nomic-embed-text",
	"gemini":       "text-embedding-004",
	"mistral":      "mistral-embed",
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
	// ProbeOllama returns nil when the local Ollama instance is reachable and
	// the requested embedding model responds. Used to flag "unreachable".
	ProbeOllama func(ctx context.Context, model string) error
	// ProbeTimeout bounds the Ollama probe. Zero uses the default 3s.
	ProbeTimeout time.Duration
}

// DefaultEmbedderSelectDeps wires SelectEmbedder against live keychain,
// environment, and a real Ollama probe.
func DefaultEmbedderSelectDeps() EmbedderSelectDeps {
	return EmbedderSelectDeps{
		LookupSecret: func(providerID string) string {
			return secrets.Get(secrets.ProviderKeyName(providerID))
		},
		Getenv: os.Getenv,
		ProbeOllama: func(ctx context.Context, model string) error {
			p := provider.NewOllama()
			_, err := p.Embed(ctx, "probe", model)
			return err
		},
		ProbeTimeout: 3 * time.Second,
	}
}

// SelectEmbedder resolves an Embedder from user settings. Returns nil and a
// non-"active" status when no embedder should be wired; returns a configured
// Embedder and EmbeddingStatusActive when it should.
//
// Behaviour:
//   - Mode "" or "disabled" → (nil, "", "disabled").
//   - Provider "" → (nil, "", "disabled").
//   - Provider unsupported → (nil, "", "disabled") — silently treated as off so
//     a stale DB value can't crash the container.
//   - Credential-requiring provider with no key → (nil, model, "missing_credentials").
//   - Ollama probe fails → (nil, model, "unreachable").
//   - All checks pass → (embedder, model, "active").
func SelectEmbedder(ctx context.Context, s EmbedderSettings, deps EmbedderSelectDeps) (embedcontracts.Embedder, string, string) {
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

	case "azure_openai":
		key := deps.LookupSecret("azure_openai-001")
		if key == "" {
			key = deps.Getenv("AZURE_OPENAI_API_KEY")
		}
		// NewAzureOpenAI reads endpoint/deployment/api-version from env and
		// will error at first use if any is missing — validate up-front so
		// embedding_status reflects reality instead of deferring the failure.
		endpoint := deps.Getenv("AZURE_OPENAI_ENDPOINT")
		deployment := deps.Getenv("AZURE_OPENAI_DEPLOYMENT")
		apiVersion := deps.Getenv("AZURE_OPENAI_API_VERSION")
		if key == "" || endpoint == "" || deployment == "" || apiVersion == "" {
			return nil, model, EmbeddingStatusMissingCredentials
		}
		p := provider.NewAzureOpenAI()
		p.SetAPIKey(key)
		return p, model, EmbeddingStatusActive

	case "gemini":
		key := deps.LookupSecret("gemini-001")
		if key == "" {
			key = deps.Getenv("GEMINI_API_KEY")
		}
		if key == "" {
			return nil, model, EmbeddingStatusMissingCredentials
		}
		p := provider.NewGemini()
		p.SetAPIKey(key)
		return p, model, EmbeddingStatusActive

	case "mistral":
		key := deps.LookupSecret("mistral-001")
		if key == "" {
			key = deps.Getenv("MISTRAL_API_KEY")
		}
		if key == "" {
			return nil, model, EmbeddingStatusMissingCredentials
		}
		p := provider.NewMistral()
		p.SetAPIKey(key)
		return p, model, EmbeddingStatusActive

	case "ollama":
		// Ollama is local, no credential — probe reachability instead.
		timeout := deps.ProbeTimeout
		if timeout <= 0 {
			timeout = 3 * time.Second
		}
		probeCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		if err := deps.ProbeOllama(probeCtx, model); err != nil {
			return nil, model, EmbeddingStatusUnreachable
		}
		return provider.NewOllama(), model, EmbeddingStatusActive
	}

	return nil, model, EmbeddingStatusDisabled
}
