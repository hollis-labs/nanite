package models

// Station LLM gateway aliases (CW-20261024-0032).
//
// A gateway alias is a name the gateway maps to an upstream model of its own
// choosing, so none of them is in the models.dev catalog and none has a price
// Nanite can know. Each is bound to one of the two station providers by the
// wire the gateway serves it on: station-openai for chat/completions aliases,
// station-anthropic for messages aliases. Embedding aliases (embed-local,
// embed-openai) and the /systemone alias (jev) are not chat models and are
// not listed.
//
// The table is static on purpose. The gateway's GET /models lists names, not
// limits, discovery is off by default, and the Anthropic-wire list needs an
// anthropic-version header the adapter does not send for model listing.
//
// UNVERIFIED VALUES. The context window, output cap and tool support below are
// conservative lower bounds, not measurements: a limit that is too low only
// compacts earlier or truncates sooner, while one that is too high makes the
// broker pack more than fits or asks for more output than the upstream allows
// (see catalog_agreement_test.go). Replace each with the gateway's real figure
// once it is published; until then they are floors.
//
// PriceUnknown is true for every alias. The gateway owns the price, so Nanite
// shows cost as unknown instead of 0.

const (
	// ProviderStationOpenAI is the provider_type of the gateway's OpenAI wire.
	ProviderStationOpenAI = "station-openai"
	// ProviderStationAnthropic is the provider_type of the gateway's Anthropic wire.
	ProviderStationAnthropic = "station-anthropic"
)

var stationModels = []Model{
	// --- chat/completions aliases (station-openai) ---
	{
		ID: "station-go-glm", ModelID: "go-glm", DisplayName: "Station go-glm", Provider: ProviderStationOpenAI, Wire: WireChat,
		ContextWindow: 128000, MaxOutput: 8192, PriceUnknown: true,
		// go-glm takes max_tokens, not max_completion_tokens.
		LegacyMaxTokens: true,
		Capabilities:    Capabilities{SupportsToolCalling: true, SupportsStreaming: true},
	},
	{
		ID: "station-openai-mini", ModelID: "openai-mini", DisplayName: "Station openai-mini", Provider: ProviderStationOpenAI, Wire: WireChat,
		ContextWindow: 128000, MaxOutput: 16384, PriceUnknown: true,
		Capabilities: Capabilities{SupportsToolCalling: true, SupportsStreaming: true, SupportsVision: true},
	},
	{
		ID: "station-gemini-flash", ModelID: "gemini-flash", DisplayName: "Station gemini-flash", Provider: ProviderStationOpenAI, Wire: WireChat,
		ContextWindow: 128000, MaxOutput: 8192, PriceUnknown: true,
		Capabilities: Capabilities{SupportsToolCalling: true, SupportsStreaming: true},
	},
	{
		ID: "station-openrouter-auto", ModelID: "openrouter-auto", DisplayName: "Station openrouter-auto", Provider: ProviderStationOpenAI, Wire: WireChat,
		ContextWindow: 128000, MaxOutput: 8192, PriceUnknown: true,
		Capabilities: Capabilities{SupportsToolCalling: true, SupportsStreaming: true},
	},
	{
		ID: "station-hf-llama", ModelID: "hf-llama", DisplayName: "Station hf-llama", Provider: ProviderStationOpenAI, Wire: WireChat,
		ContextWindow: 32768, MaxOutput: 4096, PriceUnknown: true,
		Capabilities: Capabilities{SupportsStreaming: true},
	},
	{
		ID: "station-local-light", ModelID: "local-light", DisplayName: "Station local-light", Provider: ProviderStationOpenAI, Wire: WireChat,
		ContextWindow: 8192, MaxOutput: 2048, PriceUnknown: true,
		Capabilities: Capabilities{SupportsStreaming: true},
	},

	// --- responses-only alias (station-openai) ---
	// go-gpt is served at /responses only; chat/completions answers 400
	// model_not_available, so the wire is pinned here rather than inferred
	// from the name (CW-20261024-0013). Limits are the same unverified floors.
	{
		ID: "station-go-gpt", ModelID: "go-gpt", DisplayName: "Station go-gpt", Provider: ProviderStationOpenAI, Wire: WireResponses,
		ContextWindow: 128000, MaxOutput: 16384, PriceUnknown: true,
		Capabilities: Capabilities{SupportsToolCalling: true, SupportsStreaming: true, SupportsVision: true},
	},

	// --- messages aliases (station-anthropic) ---
	{
		ID: "station-go-haiku", ModelID: "go-haiku", DisplayName: "Station go-haiku", Provider: ProviderStationAnthropic,
		ContextWindow: 200000, MaxOutput: 8192, PriceUnknown: true,
		Capabilities: Capabilities{SupportsToolCalling: true, SupportsStreaming: true, SupportsVision: true},
	},
	{
		ID: "station-anthropic-haiku", ModelID: "anthropic-haiku", DisplayName: "Station anthropic-haiku", Provider: ProviderStationAnthropic,
		ContextWindow: 200000, MaxOutput: 8192, PriceUnknown: true,
		Capabilities: Capabilities{SupportsToolCalling: true, SupportsStreaming: true, SupportsVision: true},
	},
}

func init() {
	allModels = append(allModels, stationModels...)
}

// IsStationProvider reports whether provider is one of the gateway providers.
func IsStationProvider(provider string) bool {
	return provider == ProviderStationOpenAI || provider == ProviderStationAnthropic
}

// PriceKnown reports whether Nanite knows what a model costs. It is false for
// an unregistered model and for one the registry marks PriceUnknown (a gateway
// alias), so a caller shows "unknown" there rather than the 0 Pricing returns.
func PriceKnown(modelID string) bool {
	m, ok := ByModelID(modelID)
	return ok && !m.PriceUnknown
}

// WireFor returns the OpenAI endpoint pinned for a model (WireChat or
// WireResponses), or "" when none is and the adapter chooses by name.
func WireFor(modelID string) string {
	m, _ := ByModelID(modelID)
	return m.Wire
}

// UsesLegacyMaxTokens reports whether a model's chat/completions endpoint takes
// max_tokens instead of max_completion_tokens.
func UsesLegacyMaxTokens(modelID string) bool {
	m, ok := ByModelID(modelID)
	return ok && m.LegacyMaxTokens
}
