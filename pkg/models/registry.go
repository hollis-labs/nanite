// Package models is the canonical in-memory registry of model metadata —
// provider binding, context window, max output tokens, pricing, and per-model
// capability flags. It is the single source of truth consumed by:
//
//   - internal/store.SeedProviders       — rows written to the models table
//   - internal/store.estimateCost        — pricing for usage records
//   - internal/chat.InferProvider        — model → provider routing
//
// Runtime code should reach for ModelByID, ProviderFor, Pricing, or
// DefaultChatModel rather than hardcoding a model name or provider name.
//
// Historical models (retired model IDs that still appear in token_usage rows)
// live alongside current models with IsLegacy=true so cost estimation for
// archived sessions stays accurate.
package models

import (
	"sync"
)

// Capabilities is the per-model capability surface. Adapter-level defaults
// are exposed via ProviderDefaults; when a per-model override is set here it
// wins.
type Capabilities struct {
	SupportsToolCalling         bool
	SupportsStreaming           bool
	SupportsVision              bool
	SupportsSystemPromptCaching bool
	SupportsBatch               bool
	SupportsEmbedding           bool
}

// Model is a single model record.
type Model struct {
	// ID is the internal row id used by seed.go (e.g. "claude-sonnet").
	ID string
	// ModelID is the wire-level identifier sent to the provider API.
	ModelID string
	// DisplayName is the human-friendly label.
	DisplayName string
	// Provider is the internal provider_type (e.g. "anthropic", "openai").
	// Use this in lieu of name-prefix matching.
	Provider string

	ContextWindow int // Total context window (input + output combined).
	MaxOutput     int // Maximum output tokens per response.

	// Pricing is per million tokens, USD. Zero for local/free providers.
	InputPricePerM  float64
	OutputPricePerM float64

	Capabilities Capabilities

	// EmbeddingDimensions is non-zero for embedding models.
	EmbeddingDimensions int

	// IsLegacy marks retired model IDs kept around for historical usage rows.
	IsLegacy bool
}

// Default-model and default-provider constants were removed from this
// package in CW-20260526-0003. Runtime callers MUST resolve defaults
// through store.ResolveProviderAndModel (or the service-layer
// DefaultResolver interface that exposes it), which walks
// explicit args → user_settings.default_{provider,model} →
// providers.default_model. Seeders read the compile-time defaults from
// internal/store/seedcatalog, which is also the routing-floor for
// chat.InferProvider when no other heuristic identifies a provider.
//
// Background: a single Go literal terminating every "what model?"
// fallback chain hid the bare-alias `claude-sonnet-4` 404 bug — the bad
// value was set once and silently propagated to every callsite. The
// fallbacks now live in the DB so operators can fix without a recompile.

// ProviderCapabilityDefaults is the adapter-wide capability set a provider
// advertises when no per-model override is available. These are "upper bound"
// or "typical" values. Per-model overrides come from the Model record.
type ProviderCapabilityDefaults struct {
	SupportsStreamJSON          bool
	SupportsPreToolHooks        bool
	SupportsPostToolHooks       bool
	SupportsSystemPromptCaching bool
	SupportsToolCalling         bool
	SupportsBatch               bool
	SupportsImageInput          bool
	SupportsEmbedding           bool
	DefaultEmbeddingModel       string
	// DefaultMaxOutput is the provider's typical max_output cap. Adapters
	// should prefer the per-model MaxOutput when sending a request.
	DefaultMaxOutput int
	// DefaultContextWindow is a provider-wide upper bound fallback.
	DefaultContextWindow int
}

// ProviderDefaults is the lookup table of per-provider capability defaults.
// Adapters fill in any provider-specific quirks (batch support, hooks, etc.)
// here instead of hardcoding constants in Capabilities() methods.
//
// Step 6.5 (SP-20260508-0001) reduced the API-provider catalog to
// Anthropic + OpenAI. Defaults for gemini, mistral, azure-openai,
// openrouter, openzen, and ollama were removed alongside their adapters.
var ProviderDefaults = map[string]ProviderCapabilityDefaults{
	"anthropic": {
		SupportsStreamJSON:          true,
		SupportsSystemPromptCaching: true,
		SupportsToolCalling:         true,
		SupportsImageInput:          true,
		DefaultMaxOutput:            16384,
		DefaultContextWindow:        200000,
	},
	"openai": {
		SupportsStreamJSON:    true,
		SupportsImageInput:    true,
		SupportsEmbedding:     true,
		DefaultEmbeddingModel: "text-embedding-3-small",
		DefaultMaxOutput:      16384,
		DefaultContextWindow:  128000,
	},
}

// allModels is the canonical list. Keep lexically grouped by provider.
// Spot-check against audit 06 before editing pricing or model IDs.
var allModels = []Model{
	// --- Anthropic ---
	{
		ID: "claude-sonnet", ModelID: "claude-sonnet-4-20250514",
		DisplayName: "Claude Sonnet 4", Provider: "anthropic",
		ContextWindow: 200000, MaxOutput: 16000,
		InputPricePerM: 3.0, OutputPricePerM: 15.0,
		Capabilities: Capabilities{
			SupportsToolCalling: true, SupportsStreaming: true,
			SupportsVision: true, SupportsSystemPromptCaching: true,
		},
	},
	{
		ID: "claude-opus", ModelID: "claude-opus-4-20250514",
		DisplayName: "Claude Opus 4", Provider: "anthropic",
		ContextWindow: 200000, MaxOutput: 32000,
		InputPricePerM: 15.0, OutputPricePerM: 75.0,
		Capabilities: Capabilities{
			SupportsToolCalling: true, SupportsStreaming: true,
			SupportsVision: true, SupportsSystemPromptCaching: true,
		},
	},
	{
		// Audit 06: model ID date suffix should be re-verified against
		// Anthropic docs. Left as-is pending confirmation.
		ID: "claude-haiku", ModelID: "claude-haiku-4-5-20251001",
		DisplayName: "Claude Haiku 4.5", Provider: "anthropic",
		ContextWindow: 200000, MaxOutput: 8192,
		InputPricePerM: 1.0, OutputPricePerM: 5.0,
		Capabilities: Capabilities{
			SupportsToolCalling: true, SupportsStreaming: true,
			SupportsVision: true, SupportsSystemPromptCaching: true,
		},
	},

	// --- OpenAI ---
	{
		ID: "gpt-4o", ModelID: "gpt-4o", DisplayName: "GPT-4o",
		Provider: "openai", ContextWindow: 128000, MaxOutput: 16384,
		InputPricePerM: 2.5, OutputPricePerM: 10.0,
		Capabilities: Capabilities{
			SupportsToolCalling: true, SupportsStreaming: true,
			SupportsVision: true,
		},
	},
	{
		ID: "gpt-4o-mini", ModelID: "gpt-4o-mini", DisplayName: "GPT-4o Mini",
		Provider: "openai", ContextWindow: 128000, MaxOutput: 16384,
		InputPricePerM: 0.15, OutputPricePerM: 0.60,
		Capabilities: Capabilities{
			SupportsToolCalling: true, SupportsStreaming: true,
			SupportsVision: true,
		},
	},
	{
		ID: "o3", ModelID: "o3", DisplayName: "o3",
		Provider: "openai", ContextWindow: 200000, MaxOutput: 100000,
		InputPricePerM: 2.0, OutputPricePerM: 8.0,
		Capabilities: Capabilities{
			SupportsToolCalling: true, SupportsStreaming: true,
		},
	},
	{
		ID: "o4-mini", ModelID: "o4-mini", DisplayName: "o4-mini",
		Provider: "openai", ContextWindow: 200000, MaxOutput: 100000,
		InputPricePerM: 1.10, OutputPricePerM: 4.40,
		Capabilities: Capabilities{
			SupportsToolCalling: true, SupportsStreaming: true,
		},
	},

	// Removed Step 6.5 follow-up (SP-20260508-0001): Google Gemini, Mistral,
	// Azure OpenAI, and Ollama chat-model rows. Their runtime constructors
	// were deleted in commit 376390c — these rows were inert at runtime but
	// surfaced in the providers UI dropdown. Anthropic + OpenAI are the
	// only supported API vendors (parent CW-20260508-0008).

	// --- PTY CLIs (no token accounting — 0/0 is intentional) ---
	{ID: "claude-cli", ModelID: "claude-cli", DisplayName: "Claude CLI",
		Provider: "pty", Capabilities: Capabilities{SupportsToolCalling: true, SupportsStreaming: true}},
	{ID: "codex-cli", ModelID: "codex-cli", DisplayName: "Codex CLI",
		Provider: "pty-codex", Capabilities: Capabilities{SupportsToolCalling: true, SupportsStreaming: true}},
	{ID: "gemini-cli", ModelID: "gemini-cli", DisplayName: "Gemini CLI",
		Provider: "pty-gemini", Capabilities: Capabilities{SupportsToolCalling: true, SupportsStreaming: true}},
	{ID: "copilot-cli", ModelID: "copilot-cli", DisplayName: "Copilot CLI",
		Provider: "pty-copilot", Capabilities: Capabilities{SupportsStreaming: true}},
	{ID: "aider-cli", ModelID: "aider-cli", DisplayName: "Aider CLI",
		Provider: "pty-aider", Capabilities: Capabilities{SupportsStreaming: true}},
	{ID: "junie-cli", ModelID: "junie-cli", DisplayName: "Junie CLI",
		Provider: "pty-junie", Capabilities: Capabilities{SupportsToolCalling: true, SupportsStreaming: true}},
	{ID: "kiro-cli", ModelID: "kiro-cli", DisplayName: "Kiro CLI",
		Provider: "pty-kiro", Capabilities: Capabilities{SupportsStreaming: true}},
	{ID: "qwen-cli", ModelID: "qwen-cli", DisplayName: "Qwen CLI",
		Provider: "pty-qwen", Capabilities: Capabilities{SupportsToolCalling: true, SupportsStreaming: true}},

	// Removed Step 6.5 follow-up (SP-20260508-0001): OpenRouter (or-*) and
	// OpenZen (oz-*) gateway model rows. Their runtime constructors were
	// deleted in commit 376390c.

	// --- Legacy pricing rows (not seeded; used for historical token_usage) ---
	{ModelID: "claude-haiku-3-20250307", Provider: "anthropic",
		InputPricePerM: 0.25, OutputPricePerM: 1.25, IsLegacy: true},
	{ModelID: "claude-3-5-sonnet-20241022", Provider: "anthropic",
		InputPricePerM: 3.0, OutputPricePerM: 15.0, IsLegacy: true},
	{ModelID: "claude-3-5-haiku-20241022", Provider: "anthropic",
		InputPricePerM: 1.0, OutputPricePerM: 5.0, IsLegacy: true},
	{ModelID: "claude-3-opus-20240229", Provider: "anthropic",
		InputPricePerM: 15.0, OutputPricePerM: 75.0, IsLegacy: true},
	{ModelID: "claude-3-sonnet-20240229", Provider: "anthropic",
		InputPricePerM: 3.0, OutputPricePerM: 15.0, IsLegacy: true},
	{ModelID: "claude-3-haiku-20240307", Provider: "anthropic",
		InputPricePerM: 0.25, OutputPricePerM: 1.25, IsLegacy: true},
	{ModelID: "gpt-4-turbo", Provider: "openai",
		InputPricePerM: 10.0, OutputPricePerM: 30.0, IsLegacy: true},

	// --- Embedding models ---
	// Step 6.5 (SP-20260508-0001) reduced the embedder catalog to OpenAI
	// only; gemini text-embedding-004, mistral-embed, and ollama
	// nomic-embed-text / all-minilm / mxbai-embed-large rows have been
	// removed alongside their adapters.
	{ModelID: "text-embedding-3-small", Provider: "openai",
		EmbeddingDimensions: 1536,
		Capabilities:        Capabilities{SupportsEmbedding: true}},
	{ModelID: "text-embedding-3-large", Provider: "openai",
		EmbeddingDimensions: 3072,
		Capabilities:        Capabilities{SupportsEmbedding: true}},
	{ModelID: "text-embedding-ada-002", Provider: "openai",
		EmbeddingDimensions: 1536,
		Capabilities:        Capabilities{SupportsEmbedding: true}, IsLegacy: true},
}

var (
	once           sync.Once
	byModelID      map[string]*Model
	byInternalID   map[string]*Model
)

// catalogMu guards catalogOverlay. Separate from the registry maps (which are
// written once during build and then read-only) so catalog syncs don't block
// normal lookups any longer than a pointer swap.
var (
	catalogMu      sync.RWMutex
	catalogOverlay map[string]Model // modelID → enriched Model; nil until first sync
)

// CatalogInput carries the per-model data extracted from an external catalog
// (e.g. models.dev). Only non-zero values are applied; zero means "use registry
// default." This keeps pkg/models free of any direct catalog dependency.
type CatalogInput struct {
	ContextWindows  map[string]int     // modelID → context window (tokens)
	MaxOutputTokens map[string]int     // modelID → max output tokens
	InputPricing    map[string]float64 // modelID → USD per million input tokens
	OutputPricing   map[string]float64 // modelID → USD per million output tokens
}

// SyncFromCatalog atomically replaces the catalog overlay with fresh data from
// input. Every call to Pricing, MaxOutputFor, ContextWindowFor, and ByModelID
// will prefer overlay values over the static registry after this returns.
//
// The overlay is built by merging the static registry with catalog data so that
// capability flags, provider bindings, and other fields not present in the
// catalog are always available.
//
// Models present in the catalog but absent from the static registry are added
// as overlay-only entries (partial records — pricing/window data only).
//
// SyncFromCatalog is safe for concurrent use and idempotent.
func SyncFromCatalog(input CatalogInput) {
	ensureBuilt()

	// Collect the union of all model IDs present in the input.
	seen := make(map[string]struct{})
	for id := range input.ContextWindows {
		seen[id] = struct{}{}
	}
	for id := range input.MaxOutputTokens {
		seen[id] = struct{}{}
	}
	for id := range input.InputPricing {
		seen[id] = struct{}{}
	}
	for id := range input.OutputPricing {
		seen[id] = struct{}{}
	}

	overlay := make(map[string]Model, len(seen))
	for modelID := range seen {
		// Start from the static registry so capability flags are preserved.
		base := Model{ModelID: modelID}
		if known, ok := byModelID[modelID]; ok {
			base = *known
		}
		if v := input.ContextWindows[modelID]; v > 0 {
			base.ContextWindow = v
		}
		if v := input.MaxOutputTokens[modelID]; v > 0 {
			base.MaxOutput = v
		}
		if v := input.InputPricing[modelID]; v > 0 {
			base.InputPricePerM = v
		}
		if v := input.OutputPricing[modelID]; v > 0 {
			base.OutputPricePerM = v
		}
		overlay[modelID] = base
	}

	catalogMu.Lock()
	catalogOverlay = overlay
	catalogMu.Unlock()
}

func build() {
	byModelID = make(map[string]*Model, len(allModels))
	byInternalID = make(map[string]*Model)
	for i := range allModels {
		m := &allModels[i]
		if m.ModelID != "" {
			// First registration wins — seeded rows register before legacy
			// rows with duplicate keys (none today, but keep the guard).
			if _, exists := byModelID[m.ModelID]; !exists {
				byModelID[m.ModelID] = m
			}
		}
		if m.ID != "" {
			byInternalID[m.ID] = m
		}
	}
}

func ensureBuilt() {
	once.Do(build)
}

// All returns every registered model. Callers must not mutate the slice.
func All() []Model {
	ensureBuilt()
	out := make([]Model, 0, len(allModels))
	for _, m := range allModels {
		if m.IsLegacy {
			continue
		}
		if m.EmbeddingDimensions > 0 {
			// Embedding-only rows aren't seeded as chat models.
			continue
		}
		out = append(out, m)
	}
	return out
}

// AllSeeded returns chat models intended for seed.go. Pointer-stable.
func AllSeeded() []Model { return All() }

// ByModelID looks up a model by its wire-level ID. The catalog overlay is
// checked first so updated pricing and limits take precedence over static data.
func ByModelID(id string) (Model, bool) {
	catalogMu.RLock()
	if m, ok := catalogOverlay[id]; ok {
		catalogMu.RUnlock()
		return m, true
	}
	catalogMu.RUnlock()

	ensureBuilt()
	m, ok := byModelID[id]
	if !ok {
		return Model{}, false
	}
	return *m, true
}

// ProviderFor returns the provider_type bound to a model ID. Empty string if
// unknown; callers should fall back to DefaultProvider() in that case.
// Accepts either the wire-level ModelID or the internal row ID.
func ProviderFor(modelID string) string {
	if m, ok := ByModelID(modelID); ok {
		return m.Provider
	}
	ensureBuilt()
	if m, ok := byInternalID[modelID]; ok {
		return m.Provider
	}
	return ""
}

// Pricing returns (inputPerM, outputPerM) USD for a model ID. Returns
// (0, 0) for unregistered models — callers that record usage should
// surface unknown models so the registry/catalog can be updated rather
// than silently substitute pricing from an unrelated default model.
func Pricing(modelID string) (float64, float64) {
	if m, ok := ByModelID(modelID); ok {
		return m.InputPricePerM, m.OutputPricePerM
	}
	return 0, 0
}

// MaxOutputFor returns the per-model max output token cap. Falls back to the
// provider default, then to zero (which adapters treat as "provider-default").
func MaxOutputFor(modelID string) int {
	if m, ok := ByModelID(modelID); ok && m.MaxOutput > 0 {
		return m.MaxOutput
	}
	if d, ok := ProviderDefaults[ProviderFor(modelID)]; ok {
		return d.DefaultMaxOutput
	}
	return 0
}

// ContextWindowFor returns the per-model context window. Falls back to the
// provider default, then to DefaultContextWindow.
func ContextWindowFor(modelID string) int {
	if m, ok := ByModelID(modelID); ok && m.ContextWindow > 0 {
		return m.ContextWindow
	}
	if d, ok := ProviderDefaults[ProviderFor(modelID)]; ok && d.DefaultContextWindow > 0 {
		return d.DefaultContextWindow
	}
	return DefaultContextWindowTokens
}

// DefaultContextWindowTokens is the system-wide fallback context window,
// used when neither per-model nor per-provider data is available. Shared by
// internal/toolclient and internal/chat.
const DefaultContextWindowTokens = 200000

// EmbeddingDimensionsFor returns the output vector size for an embedding
// model. Returns 0 if the model is unknown or not an embedding model.
func EmbeddingDimensionsFor(modelID string) int {
	if m, ok := ByModelID(modelID); ok {
		return m.EmbeddingDimensions
	}
	return 0
}

// ProviderHasPrefix helps InferProvider match gateway-prefixed IDs.
//
// Step 6.5 (SP-20260508-0001) removed the OpenRouter adapter, which was the
// only consumer of provider-prefixed routing. The helper is retained as a
// no-op so InferProvider's call site stays stable; if a future gateway
// vendor returns, the prefix→provider map can be repopulated here.
func ProviderHasPrefix(modelID string) (string, bool) {
	ensureBuilt()
	_ = modelID
	return "", false
}
