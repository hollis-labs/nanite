package service

import (
	"sync"
	"time"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/tool/intent"
	"github.com/hollis-labs/nanite/pkg/models"
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
	if provName == "" {
		provName = models.DefaultProvider()
	}
	model := us.ToolClassifierModel
	if model == "" {
		model = us.SummarizerModel
	}
	if model == "" {
		model = models.DefaultChatModel()
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
