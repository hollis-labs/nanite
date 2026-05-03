package context

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// Glass-3 (CW-20260502-0011, SP-20260502-0001) — ValidateHandoff acceptance
// matrix. Six required cases per the boot-prompt verification list:
//   1. valid minimum (only required fields)
//   2. valid maximum (every field at its cap, total under SlotHandoffMaxTokens)
//   3. oversize total
//   4. oversize per-field (recent_decisions[i], session_intent, next_step_anchor)
//   5. missing required field (session_intent, next_step_anchor)
//   6. malformed input

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestValidateHandoff_ValidMinimum(t *testing.T) {
	payload := mustJSON(t, HandoffPayload{
		SessionIntent:  "Refactoring the auth middleware.",
		NextStepAnchor: "Resume from internal/middleware/auth.go:42.",
	})
	hp, err := ValidateHandoff(payload)
	if err != nil {
		t.Fatalf("expected success on minimum valid payload, got: %v", err)
	}
	if hp.SessionIntent == "" || hp.NextStepAnchor == "" {
		t.Fatalf("expected required fields populated, got %+v", hp)
	}
	if len(hp.RecentDecisions) != 0 || len(hp.ActivePointers) != 0 {
		t.Fatalf("expected optional fields empty on minimum payload, got %+v", hp)
	}
}

func TestValidateHandoff_ValidMaximum(t *testing.T) {
	// Each field tuned just under its cap so total stays under the slot cap.
	// All field caps are token counts via DefaultEstimator (chars/4):
	//   session_intent     ≤ 200 tokens ≈ 800 chars
	//   next_step_anchor   ≤ 100 tokens ≈ 400 chars
	//   recent_decisions   ≤ 3 items, each ≤ 100 tokens (≈ 400 chars)
	//   active_pointers    ≤ 5 items (no per-pointer cap; total cap applies)
	intent := strings.Repeat("a", HandoffSessionIntentMaxTokens*4-4)   // ≈ 199 tokens
	next := strings.Repeat("b", HandoffNextStepAnchorMaxTokens*4-4)    // ≈ 99 tokens
	dec := strings.Repeat("c", HandoffRecentDecisionItemMaxTokens*4-4) // ≈ 99 tokens

	payload := mustJSON(t, HandoffPayload{
		SessionIntent:  intent,
		NextStepAnchor: next,
		RecentDecisions: []string{
			dec, dec, dec, // exactly 3 items, the cap
		},
		ActivePointers: []HandoffPointer{
			{CacheKey: "k1", Label: "l1", Purpose: "p1"},
			{CacheKey: "k2", Label: "l2", Purpose: "p2"},
			{CacheKey: "k3", Label: "l3", Purpose: "p3"},
			{CacheKey: "k4", Label: "l4", Purpose: "p4"},
			{CacheKey: "k5", Label: "l5", Purpose: "p5"}, // exactly 5 items
		},
	})

	hp, err := ValidateHandoff(payload)
	if err != nil {
		t.Fatalf("expected success on at-cap payload, got: %v", err)
	}
	if len(hp.RecentDecisions) != HandoffRecentDecisionsMax {
		t.Fatalf("RecentDecisions length: got %d, want %d", len(hp.RecentDecisions), HandoffRecentDecisionsMax)
	}
	if len(hp.ActivePointers) != HandoffActivePointersMax {
		t.Fatalf("ActivePointers length: got %d, want %d", len(hp.ActivePointers), HandoffActivePointersMax)
	}
}

func TestValidateHandoff_OversizeTotal(t *testing.T) {
	// Build a payload where each individual field is under its per-field cap,
	// but the sum exceeds SlotHandoffMaxTokens. With 3 RecentDecisions each at
	// ~99 tokens (≈ 297) plus 5 pointers' purpose strings of ~600 chars each
	// (≈ 750 tokens), session_intent at ~199 tokens, and next_step_anchor at
	// ~99 tokens, the total clears 1500.
	intent := strings.Repeat("a", HandoffSessionIntentMaxTokens*4-4)   // ≈ 199 tokens
	next := strings.Repeat("b", HandoffNextStepAnchorMaxTokens*4-4)    // ≈ 99 tokens
	dec := strings.Repeat("c", HandoffRecentDecisionItemMaxTokens*4-4) // ≈ 99 tokens × 3 = 297
	bigPurpose := strings.Repeat("d", 1200)                            // ≈ 300 tokens × 5 = 1500
	// Total: 199 + 99 + 297 + 1500 = 2095 tokens, well over SlotHandoffMaxTokens (1500).

	payload := mustJSON(t, HandoffPayload{
		SessionIntent:   intent,
		NextStepAnchor:  next,
		RecentDecisions: []string{dec, dec, dec},
		ActivePointers: []HandoffPointer{
			{CacheKey: "k1", Label: "l1", Purpose: bigPurpose},
			{CacheKey: "k2", Label: "l2", Purpose: bigPurpose},
			{CacheKey: "k3", Label: "l3", Purpose: bigPurpose},
			{CacheKey: "k4", Label: "l4", Purpose: bigPurpose},
			{CacheKey: "k5", Label: "l5", Purpose: bigPurpose},
		},
	})

	_, err := ValidateHandoff(payload)
	if err == nil {
		t.Fatal("expected oversize-total error, got nil")
	}
	if !errors.Is(err, ErrHandoffOversize) {
		t.Fatalf("expected ErrHandoffOversize, got: %v", err)
	}
	if !strings.Contains(err.Error(), "total") {
		t.Fatalf("expected error to indicate 'total' field, got: %v", err)
	}
}

func TestValidateHandoff_OversizePerField(t *testing.T) {
	cases := []struct {
		name        string
		mutate      func(*HandoffPayload)
		fieldHint   string
	}{
		{
			name: "session_intent over cap",
			mutate: func(hp *HandoffPayload) {
				hp.SessionIntent = strings.Repeat("a", (HandoffSessionIntentMaxTokens+50)*4)
			},
			fieldHint: "session_intent",
		},
		{
			name: "next_step_anchor over cap",
			mutate: func(hp *HandoffPayload) {
				hp.NextStepAnchor = strings.Repeat("b", (HandoffNextStepAnchorMaxTokens+50)*4)
			},
			fieldHint: "next_step_anchor",
		},
		{
			name: "recent_decisions item over cap",
			mutate: func(hp *HandoffPayload) {
				hp.RecentDecisions = []string{
					"ok",
					strings.Repeat("c", (HandoffRecentDecisionItemMaxTokens+50)*4),
				}
			},
			fieldHint: "recent_decisions[1]",
		},
		{
			name: "recent_decisions too many items",
			mutate: func(hp *HandoffPayload) {
				hp.RecentDecisions = []string{"a", "b", "c", "d"} // 4 > cap of 3
			},
			fieldHint: "recent_decisions",
		},
		{
			name: "active_pointers too many items",
			mutate: func(hp *HandoffPayload) {
				hp.ActivePointers = make([]HandoffPointer, HandoffActivePointersMax+1)
			},
			fieldHint: "active_pointers",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hp := HandoffPayload{
				SessionIntent:  "intent.",
				NextStepAnchor: "anchor.",
			}
			tc.mutate(&hp)
			payload := mustJSON(t, hp)
			_, err := ValidateHandoff(payload)
			if err == nil {
				t.Fatal("expected oversize error, got nil")
			}
			if !errors.Is(err, ErrHandoffOversize) {
				t.Fatalf("expected ErrHandoffOversize, got: %v", err)
			}
			if !strings.Contains(err.Error(), tc.fieldHint) {
				t.Fatalf("expected error to mention %q, got: %v", tc.fieldHint, err)
			}
		})
	}
}

func TestValidateHandoff_MissingRequired(t *testing.T) {
	cases := []struct {
		name      string
		payload   HandoffPayload
		fieldHint string
	}{
		{
			name:      "missing session_intent",
			payload:   HandoffPayload{NextStepAnchor: "anchor."},
			fieldHint: "session_intent",
		},
		{
			name:      "missing next_step_anchor",
			payload:   HandoffPayload{SessionIntent: "intent."},
			fieldHint: "next_step_anchor",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateHandoff(mustJSON(t, tc.payload))
			if err == nil {
				t.Fatal("expected missing-field error, got nil")
			}
			if !errors.Is(err, ErrHandoffMissingField) {
				t.Fatalf("expected ErrHandoffMissingField, got: %v", err)
			}
			if !strings.Contains(err.Error(), tc.fieldHint) {
				t.Fatalf("expected error to mention %q, got: %v", tc.fieldHint, err)
			}
		})
	}
}

func TestValidateHandoff_Malformed(t *testing.T) {
	cases := [][]byte{
		nil,                       // empty input
		[]byte(""),                // empty input
		[]byte("{not json"),       // unterminated JSON, not valid YAML mapping
		[]byte("\x00\x01\x02"),    // binary garbage
	}
	for i, payload := range cases {
		_, err := ValidateHandoff(payload)
		if err == nil {
			t.Fatalf("case %d: expected error, got nil", i)
		}
		if !errors.Is(err, ErrHandoffMalformed) {
			t.Fatalf("case %d: expected ErrHandoffMalformed, got: %v", i, err)
		}
	}
}

func TestValidateHandoff_AcceptsYAMLFallback(t *testing.T) {
	// JSON-first parsing is the contract; YAML is the fallback for human-
	// authored handoffs. This test ensures the YAML branch is wired.
	yamlPayload := []byte(`session_intent: "Refactoring auth"
next_step_anchor: "Resume at internal/middleware/auth.go:42"
recent_decisions:
  - "Decided to keep handler signatures stable"
active_pointers:
  - cache_key: "abc"
    label: "spec"
    purpose: "RFC text"
`)
	hp, err := ValidateHandoff(yamlPayload)
	if err != nil {
		t.Fatalf("expected YAML payload to parse, got: %v", err)
	}
	if hp.SessionIntent == "" || hp.NextStepAnchor == "" {
		t.Fatalf("expected YAML payload to populate required fields, got %+v", hp)
	}
	if len(hp.RecentDecisions) != 1 || len(hp.ActivePointers) != 1 {
		t.Fatalf("expected YAML payload to populate optional fields, got %+v", hp)
	}
}

func TestSlotHandoff_RegisteredInOrder(t *testing.T) {
	// Glass-3 verification: SlotHandoff sits between SlotUserContext and
	// SlotConversation in SlotOrder. Glass-2's request_build telemetry walks
	// SlotOrder dynamically, so this placement governs where handoff_tokens
	// appears in the kv list.
	var idx = -1
	for i, n := range SlotOrder {
		if n == SlotHandoff {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("SlotHandoff not in SlotOrder")
	}
	if SlotOrder[idx-1] != SlotUserContext {
		t.Fatalf("expected SlotOrder[%d-1] == SlotUserContext, got %q", idx, SlotOrder[idx-1])
	}
	if SlotOrder[idx+1] != SlotConversation {
		t.Fatalf("expected SlotOrder[%d+1] == SlotConversation, got %q", idx, SlotOrder[idx+1])
	}
	if DefaultCompactable()[SlotHandoff] {
		t.Fatal("SlotHandoff must be non-compactable")
	}
}
