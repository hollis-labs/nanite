package service

import (
	"os"
	"sync"
	"time"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/tool/intent"
)

// toolCacheOverrideStore is an ephemeral, per-process map of session ID →
// /tools on|off pin. T6 populates this via the `/tools` slash command handler;
// the ContextService reads it when classifying intent.
type toolCacheOverrideStore struct {
	mu    sync.RWMutex
	state map[string]intent.Override
}

func newToolCacheOverrideStore() *toolCacheOverrideStore {
	return &toolCacheOverrideStore{state: make(map[string]intent.Override)}
}

// Get implements ToolCacheOverrideStore.
func (s *toolCacheOverrideStore) Get(sessionID string) intent.Override {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state[sessionID]
}

// Set pins the override for a session; T6 calls this from the /tools handler.
// OverrideNone clears the pin.
func (s *toolCacheOverrideStore) Set(sessionID string, o intent.Override) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if o == intent.OverrideNone {
		delete(s.state, sessionID)
		return
	}
	s.state[sessionID] = o
}

// buildToolIntentClassifier constructs the per-container BrokerClassifier. The
// LLM layer reuses the classifier provider/model (falling back to the
// summarizer + chat defaults) resolved from UserSettings.
//
// On construction we honour the user's current setting; if they change the
// classifier mode or timeout at runtime, a restart picks up the change. This
// mirrors BuildSummarizer's behaviour (context.go S3a pattern).
func buildToolIntentClassifier(reg *provider.Registry, s *store.Store) intent.Classifier {
	rules := intent.NewRulesClassifier()
	if s == nil {
		return intent.NewBrokerClassifier(rules, nil)
	}
	us, err := s.GetUserSettings()
	if err != nil || us == nil {
		return intent.NewBrokerClassifier(rules, nil)
	}
	if us.ToolClassifierMode == "rules" {
		return intent.NewBrokerClassifier(rules, nil)
	}

	provName := us.ToolClassifierProvider
	if provName == "" {
		provName = us.SummarizerProvider
	}
	model := us.ToolClassifierModel
	if model == "" {
		model = us.SummarizerModel
	}
	// Resolver fills any remaining gap from user_settings →
	// providers.default_model (CW-20260526-0003). Empty result on error
	// is intentional — the registry lookup below treats unknown providers
	// as "no LLM classifier," and the broker-only path still works.
	if provName == "" || model == "" {
		if rp, rm, err := s.ResolveProviderAndModel(provName, model); err == nil {
			provName = rp
			model = rm
		}
	}

	var llm intent.Classifier
	if reg != nil {
		if prov, ok := reg.Get(provName); ok && prov != nil {
			timeout := time.Duration(us.ToolClassifierTimeoutMS) * time.Millisecond
			llm = intent.NewLLMClassifier(prov, model, timeout)
		}
	}
	if us.ToolClassifierMode == "llm" && llm != nil {
		return llm
	}
	return intent.NewBrokerClassifier(rules, llm)
}

// buildRepairConfig wires the C2 LLM-augmented repair pipeline
// (CW-20260429-0008) onto the toolService. Returns nil when the env
// kill switch is off OR when no provider is resolvable — Execute then
// falls through to the C1 structured envelope.
//
// Provider resolution: NANITE_REPAIR_PROVIDER overrides; otherwise the
// utility provider (configured at the harness level for cheap utility
// calls like summarization / classification); otherwise the user's
// default chat provider; otherwise the platform default.
//
// Model resolution: NANITE_REPAIR_MODEL overrides; otherwise the
// recoverpkg.DefaultRepairModel constant ("claude-haiku-4-5").
//
// Timeout resolution: NANITE_REPAIR_TIMEOUT_MS overrides; otherwise
// recoverpkg.DefaultRepairTimeout. The package consts live in
// internal/recover so we don't duplicate the values; they are the
// SoT.
func buildRepairConfig(reg *provider.Registry, s *store.Store, utilityProvider string) *RepairConfig {
	if reg == nil {
		return nil
	}
	// Operator-level kill switch. Use the same parsing helper as
	// runtime (autoRepairEnvEnabled) so wiring-time and call-time
	// agree on what counts as "disabled" — case-insensitive, trimmed.
	if !autoRepairEnvEnabled() {
		return nil
	}

	// Provider resolution. NANITE_REPAIR_PROVIDER → caller's
	// utility provider → user_settings.utility_provider →
	// resolver (user_settings.default_provider → providers.default_model
	// keyed provider). CW-20260526-0003: terminal Go-literal fallback
	// removed; if the chain is dry, the repair pipeline declines rather
	// than guessing.
	provName := os.Getenv("NANITE_REPAIR_PROVIDER")
	if provName == "" {
		provName = utilityProvider
	}
	if provName == "" && s != nil {
		if us, err := s.GetUserSettings(); err == nil && us != nil {
			if us.UtilityProvider != "" {
				provName = us.UtilityProvider
			}
		}
	}
	if provName == "" && s != nil {
		if rp, _, err := s.ResolveProviderAndModel("", ""); err == nil {
			provName = rp
		}
	}
	if provName == "" {
		return nil
	}

	prov, ok := reg.Get(provName)
	if !ok || prov == nil {
		return nil
	}

	// Timeout resolution. Empty / invalid env → 0 → DefaultRepairTimeout.
	var timeout time.Duration
	if raw := os.Getenv("NANITE_REPAIR_TIMEOUT_MS"); raw != "" {
		if ms, err := time.ParseDuration(raw + "ms"); err == nil && ms > 0 {
			timeout = ms
		}
	}

	model := os.Getenv("NANITE_REPAIR_MODEL")

	rc := &RepairConfig{
		Provider: prov,
		Model:    model, // empty → recover package default
		Timeout:  timeout,
	}
	if s != nil {
		rc.SettingsReader = s
	}
	return rc
}
