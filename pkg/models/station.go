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
// SOURCES AND WHAT IS UNKNOWN. The only documented source is the Station
// gateway adoption guide (hollis-labs/station docs/llm-gateway-adoption.md,
// CW-20261017-0031, "Explicit model routes") and its scripts/gateway-smoke.py
// probes. The guide documents each alias's endpoint (wire) and upstream model,
// the token parameter (max_tokens for Messages and go-glm), and that a native
// tool-call probe passed for go-glm, go-gpt and go-haiku. It publishes NO
// context window, NO max output and NO vision support for any alias, and the
// authenticated GET /models lists names only (unauthenticated it answers 401).
//
// So every ContextWindow and MaxOutput below is UNKNOWN: it is a conservative
// floor kept so the broker never packs more than fits, not a measurement
// (a floor that is too low only compacts earlier; one that is too high breaks
// requests, see catalog_agreement_test.go). LimitsUnknown marks that, and it is
// surfaced as "limits_unknown" in the models-table pricing JSON. Likewise
// ToolsUnknown marks an alias whose tool support no probe covers, and vision
// support is UNKNOWN for every alias (never documented). Replace a floor and
// clear its flag when the gateway publishes the figure.
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
		ID: "station-go-glm", ModelID: "go-glm", DisplayName: "Station go-glm", Provider: ProviderStationOpenAI,
		// Source: station adoption guide, route /chat/completions -> Go glm-5.3-flash. Context window and max output UNKNOWN (not published); values are floors. Tool calls: documented (smoke tool-call probe passed). Vision UNKNOWN.
		ContextWindow: 128000, MaxOutput: 8192, PriceUnknown: true, LimitsUnknown: true,
		// go-glm takes max_tokens, not max_completion_tokens.
		LegacyMaxTokens: true,
		Capabilities:    Capabilities{SupportsToolCalling: true, SupportsStreaming: true},
	},
	{
		ID: "station-openai-mini", ModelID: "openai-mini", DisplayName: "Station openai-mini", Provider: ProviderStationOpenAI,
		// Source: station adoption guide, route /chat/completions -> OpenAI gpt-5-mini. Context window and max output UNKNOWN (not published); values are floors. Tool support, vision UNKNOWN (no probe, not documented); value as catalogued is unverified.
		ContextWindow: 128000, MaxOutput: 16384, PriceUnknown: true, LimitsUnknown: true, ToolsUnknown: true,
		Capabilities: Capabilities{SupportsToolCalling: true, SupportsStreaming: true, SupportsVision: true},
	},
	{
		ID: "station-gemini-flash", ModelID: "gemini-flash", DisplayName: "Station gemini-flash", Provider: ProviderStationOpenAI,
		// Source: station adoption guide, route /chat/completions -> Gemini gemini-2.5-flash. Context window and max output UNKNOWN (not published); values are floors. Tool support, vision UNKNOWN (no probe, not documented); value as catalogued is unverified.
		ContextWindow: 128000, MaxOutput: 8192, PriceUnknown: true, LimitsUnknown: true, ToolsUnknown: true,
		Capabilities: Capabilities{SupportsToolCalling: true, SupportsStreaming: true},
	},
	{
		ID: "station-openrouter-auto", ModelID: "openrouter-auto", DisplayName: "Station openrouter-auto", Provider: ProviderStationOpenAI,
		// Source: station adoption guide, route /chat/completions -> OpenRouter openrouter/auto (upstream varies). Context window and max output UNKNOWN (not published); values are floors. Tool support, vision UNKNOWN (no probe, not documented); value as catalogued is unverified.
		ContextWindow: 128000, MaxOutput: 8192, PriceUnknown: true, LimitsUnknown: true, ToolsUnknown: true,
		Capabilities: Capabilities{SupportsToolCalling: true, SupportsStreaming: true},
	},
	{
		ID: "station-hf-llama", ModelID: "hf-llama", DisplayName: "Station hf-llama", Provider: ProviderStationOpenAI,
		// Source: station adoption guide, route /chat/completions -> HF meta-llama/Llama-3.1-8B-Instruct. Context window and max output UNKNOWN (not published); values are floors. No-tools is an UNKNOWN conservative choice, not documented.
		ContextWindow: 32768, MaxOutput: 4096, PriceUnknown: true, LimitsUnknown: true, ToolsUnknown: true,
		Capabilities: Capabilities{SupportsStreaming: true},
	},
	{
		ID: "station-local-light", ModelID: "local-light", DisplayName: "Station local-light", Provider: ProviderStationOpenAI,
		// Source: station adoption guide, route /chat/completions -> Local qwen3-4b-instruct-2507. Context window and max output UNKNOWN (not published); values are floors. No-tools is an UNKNOWN conservative choice, not documented.
		ContextWindow: 8192, MaxOutput: 2048, PriceUnknown: true, LimitsUnknown: true, ToolsUnknown: true,
		Capabilities: Capabilities{SupportsStreaming: true},
	},

	// --- messages aliases (station-anthropic) ---
	{
		ID: "station-go-haiku", ModelID: "go-haiku", DisplayName: "Station go-haiku", Provider: ProviderStationAnthropic,
		// Source: station adoption guide, route /messages -> Go claude-haiku-5-5. Context window and max output UNKNOWN (not published); values are floors. Tool calls: documented (smoke tool-call probe passed). Vision UNKNOWN.
		ContextWindow: 200000, MaxOutput: 8192, PriceUnknown: true, LimitsUnknown: true,
		Capabilities: Capabilities{SupportsToolCalling: true, SupportsStreaming: true, SupportsVision: true},
	},
	{
		ID: "station-anthropic-haiku", ModelID: "anthropic-haiku", DisplayName: "Station anthropic-haiku", Provider: ProviderStationAnthropic,
		// Source: station adoption guide, route /messages -> Anthropic claude-haiku-5-5. Context window and max output UNKNOWN (not published); values are floors. Tool support, vision UNKNOWN (no probe, not documented); value as catalogued is unverified.
		ContextWindow: 200000, MaxOutput: 8192, PriceUnknown: true, LimitsUnknown: true, ToolsUnknown: true,
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

// LimitsKnown reports whether a model's context window and max output are
// documented figures. It is false for an unregistered model and for a gateway
// alias whose limits are UNKNOWN floors.
func LimitsKnown(modelID string) bool {
	m, ok := ByModelID(modelID)
	return ok && !m.LimitsUnknown
}

// PriceKnown reports whether Nanite knows what a model costs. It is false for
// an unregistered model and for one the registry marks PriceUnknown (a gateway
// alias), so a caller shows "unknown" there rather than the 0 Pricing returns.
func PriceKnown(modelID string) bool {
	m, ok := ByModelID(modelID)
	return ok && !m.PriceUnknown
}

// UsesLegacyMaxTokens reports whether a model's chat/completions endpoint takes
// max_tokens instead of max_completion_tokens.
func UsesLegacyMaxTokens(modelID string) bool {
	m, ok := ByModelID(modelID)
	return ok && m.LegacyMaxTokens
}
