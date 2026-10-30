package models

import "testing"

func TestStationAliasCatalog(t *testing.T) {
	want := map[string]string{
		"go-glm": ProviderStationOpenAI, "openai-mini": ProviderStationOpenAI, "gemini-flash": ProviderStationOpenAI,
		"openrouter-auto": ProviderStationOpenAI, "hf-llama": ProviderStationOpenAI, "local-light": ProviderStationOpenAI,
		"go-gpt": ProviderStationOpenAI, "go-haiku": ProviderStationAnthropic, "anthropic-haiku": ProviderStationAnthropic,
	}
	seeded := map[string]Model{}
	for _, m := range AllSeeded() {
		seeded[m.ModelID] = m
	}
	for alias, provider := range want {
		m, ok := seeded[alias]
		if !ok {
			t.Errorf("alias %s is not seeded", alias)
			continue
		}
		if ProviderFor(alias) != provider || m.Provider != provider {
			t.Errorf("%s provider = %q, want %q", alias, ProviderFor(alias), provider)
		}
		if m.ContextWindow <= 0 || m.MaxOutput <= 0 || m.MaxOutput >= m.ContextWindow {
			t.Errorf("%s limits = %d/%d", alias, m.ContextWindow, m.MaxOutput)
		}
		if ContextWindowFor(alias) != m.ContextWindow || MaxOutputFor(alias) != m.MaxOutput {
			t.Errorf("%s: lookups disagree with the table", alias)
		}
		if PriceKnown(alias) {
			t.Errorf("%s price must be unknown, not zero", alias)
		}
		// No limit is documented for any alias: all stay marked UNKNOWN.
		if !m.LimitsUnknown || LimitsKnown(alias) {
			t.Errorf("%s limits must be marked unknown", alias)
		}
		if !IsStationProvider(m.Provider) || !IsRealModel(alias) {
			t.Errorf("%s is not a real station model", alias)
		}
	}
	// The small aliases must not inherit the 200000 fallback.
	if ContextWindowFor("local-light") >= DefaultContextWindowTokens || ContextWindowFor("hf-llama") >= DefaultContextWindowTokens {
		t.Error("local-light / hf-llama fell back to the default context window")
	}
	// Tool support is part of the record.
	if seeded["local-light"].Capabilities.SupportsToolCalling || !seeded["go-glm"].Capabilities.SupportsToolCalling {
		t.Error("tool support not as cataloged")
	}
	// Only the tool-call probes (go-glm, go-gpt, go-haiku) document tool support.
	for alias := range want {
		documented := alias == "go-glm" || alias == "go-gpt" || alias == "go-haiku"
		if seeded[alias].ToolsUnknown == documented {
			t.Errorf("%s ToolsUnknown = %v", alias, seeded[alias].ToolsUnknown)
		}
	}
	if !LimitsKnown("claude-opus-5-5") || LimitsKnown("no-such-model") {
		t.Error("LimitsKnown wrong for a documented or unregistered model")
	}
	// Existing models keep a known price.
	if !PriceKnown("claude-opus-5-5") {
		t.Error("a priced model reported unknown")
	}
	if PriceKnown("no-such-model") {
		t.Error("an unregistered model reported a known price")
	}
}

func TestUsesLegacyMaxTokens(t *testing.T) {
	if !UsesLegacyMaxTokens("go-glm") {
		t.Error("go-glm takes max_tokens")
	}
	for _, id := range []string{"openai-mini", "gpt-5.6", "no-such-model"} {
		if UsesLegacyMaxTokens(id) {
			t.Errorf("%s must keep max_completion_tokens", id)
		}
	}
}

func TestStationWire(t *testing.T) {
	for id, want := range map[string]string{
		"go-gpt": WireResponses, "go-glm": WireChat, "openai-mini": WireChat, "local-light": WireChat,
		"gpt-5.6": "", "no-such-model": "",
	} {
		if got := WireFor(id); got != want {
			t.Errorf("WireFor(%q) = %q, want %q", id, got, want)
		}
	}
}
