package context

import (
	"encoding/json"
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"
)

// HandoffPayload is the structured contract for SlotHandoff content.
// Self-authored by the agent pre-compaction; auto-injected post-compaction
// (Glass-4 wires the flow). Cap: SlotHandoffMaxTokens (~1500). Feature-sized
// — not a dumping ground.
//
// Field caps (enforced by ValidateHandoff):
//   - SessionIntent: required, ≤ HandoffSessionIntentMaxTokens (200) tokens
//   - RecentDecisions: ≤ HandoffRecentDecisionsMax (3) items, each ≤ HandoffRecentDecisionItemMaxTokens (100) tokens
//   - ActivePointers: ≤ HandoffActivePointersMax (5) items
//   - NextStepAnchor: required, ≤ HandoffNextStepAnchorMaxTokens (100) tokens
//   - Total estimated tokens: ≤ SlotHandoffMaxTokens
type HandoffPayload struct {
	SessionIntent   string           `yaml:"session_intent" json:"session_intent"`
	RecentDecisions []string         `yaml:"recent_decisions" json:"recent_decisions"`
	ActivePointers  []HandoffPointer `yaml:"active_pointers" json:"active_pointers"`
	NextStepAnchor  string           `yaml:"next_step_anchor" json:"next_step_anchor"`
}

// HandoffPointer is one entry in HandoffPayload.ActivePointers — a labeled
// pointer to a harness-cached resource the agent wants to remember the
// existence of without paying the full token cost in the handoff itself.
type HandoffPointer struct {
	CacheKey string `yaml:"cache_key" json:"cache_key"`
	Label    string `yaml:"label" json:"label"`
	Purpose  string `yaml:"purpose" json:"purpose"`
}

// Per-field caps for HandoffPayload validation. Total payload is also gated
// by SlotHandoffMaxTokens (defined in slot.go).
const (
	HandoffSessionIntentMaxTokens      = 200
	HandoffRecentDecisionsMax          = 3
	HandoffRecentDecisionItemMaxTokens = 100
	HandoffActivePointersMax           = 5
	HandoffNextStepAnchorMaxTokens     = 100
)

// ErrHandoffMalformed is returned when the input bytes do not parse as JSON
// or YAML. ErrHandoffMissingField is returned when a required field is empty.
// ErrHandoffOversize is returned when a per-field or total cap is exceeded.
// Wrapped errors carry the offending field name in their message.
var (
	ErrHandoffMalformed    = errors.New("handoff: malformed payload")
	ErrHandoffMissingField = errors.New("handoff: required field missing")
	ErrHandoffOversize     = errors.New("handoff: payload exceeds cap")
)

// ValidateHandoff parses + validates a handoff payload. JSON is tried first
// (unambiguous, matches provider response shape); YAML is the fallback for
// human-authored handoffs that prefer block scalars. Returns the parsed
// payload on success.
//
// Token counts use DefaultEstimator (chars/4) — the same heuristic used
// across the context package for budget allocation.
func ValidateHandoff(payload []byte) (*HandoffPayload, error) {
	if len(payload) == 0 {
		return nil, fmt.Errorf("%w: empty input", ErrHandoffMalformed)
	}

	hp, err := parseHandoff(payload)
	if err != nil {
		return nil, err
	}

	if err := validateHandoffFields(hp); err != nil {
		return nil, err
	}

	return hp, nil
}

func parseHandoff(payload []byte) (*HandoffPayload, error) {
	var hp HandoffPayload
	if jsonErr := json.Unmarshal(payload, &hp); jsonErr == nil {
		return &hp, nil
	}
	// YAML fallback — covers human-authored block-scalar handoffs.
	hp = HandoffPayload{}
	if yamlErr := yaml.Unmarshal(payload, &hp); yamlErr != nil {
		return nil, fmt.Errorf("%w: not valid JSON or YAML: %w", ErrHandoffMalformed, yamlErr)
	}
	return &hp, nil
}

func validateHandoffFields(hp *HandoffPayload) error {
	est := DefaultEstimator{}

	if hp.SessionIntent == "" {
		return fmt.Errorf("%w: session_intent", ErrHandoffMissingField)
	}
	if hp.NextStepAnchor == "" {
		return fmt.Errorf("%w: next_step_anchor", ErrHandoffMissingField)
	}

	if n := est.Estimate(hp.SessionIntent); n > HandoffSessionIntentMaxTokens {
		return fmt.Errorf("%w: session_intent (%d > %d tokens)",
			ErrHandoffOversize, n, HandoffSessionIntentMaxTokens)
	}
	if n := est.Estimate(hp.NextStepAnchor); n > HandoffNextStepAnchorMaxTokens {
		return fmt.Errorf("%w: next_step_anchor (%d > %d tokens)",
			ErrHandoffOversize, n, HandoffNextStepAnchorMaxTokens)
	}

	if len(hp.RecentDecisions) > HandoffRecentDecisionsMax {
		return fmt.Errorf("%w: recent_decisions (%d > %d items)",
			ErrHandoffOversize, len(hp.RecentDecisions), HandoffRecentDecisionsMax)
	}
	for i, d := range hp.RecentDecisions {
		if n := est.Estimate(d); n > HandoffRecentDecisionItemMaxTokens {
			return fmt.Errorf("%w: recent_decisions[%d] (%d > %d tokens)",
				ErrHandoffOversize, i, n, HandoffRecentDecisionItemMaxTokens)
		}
	}

	if len(hp.ActivePointers) > HandoffActivePointersMax {
		return fmt.Errorf("%w: active_pointers (%d > %d items)",
			ErrHandoffOversize, len(hp.ActivePointers), HandoffActivePointersMax)
	}

	if n := totalHandoffTokens(hp, est); n > SlotHandoffMaxTokens {
		return fmt.Errorf("%w: total (%d > %d tokens)",
			ErrHandoffOversize, n, SlotHandoffMaxTokens)
	}

	return nil
}

// totalHandoffTokens sums the estimated token cost of every text field in the
// payload. Pointers are counted as label+purpose+cache_key text; struct/JSON
// envelope overhead is not modeled because the caller controls the
// serialization (and the cap is a budget guardrail, not an exact accounting).
func totalHandoffTokens(hp *HandoffPayload, est TokenEstimator) int {
	total := est.Estimate(hp.SessionIntent) + est.Estimate(hp.NextStepAnchor)
	for _, d := range hp.RecentDecisions {
		total += est.Estimate(d)
	}
	for _, p := range hp.ActivePointers {
		total += est.Estimate(p.CacheKey) + est.Estimate(p.Label) + est.Estimate(p.Purpose)
	}
	return total
}
