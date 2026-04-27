package chat

// hint_catalog_test.go — Unit tests for the hint catalog loader (F5 / CW-20260420-0022).

import (
	"strings"
	"testing"
)

// TestBuiltinHints_LoadsSuccessfully verifies the embedded catalog loads
// without error and returns a non-empty slice.
func TestBuiltinHints_LoadsSuccessfully(t *testing.T) {
	hints := BuiltinHints()
	if len(hints) == 0 {
		t.Fatalf("BuiltinHints() returned empty slice; catalog error: %v", BuiltinHintsErr())
	}
	if err := BuiltinHintsErr(); err != nil {
		t.Fatalf("BuiltinHintsErr() non-nil: %v", err)
	}
}

// TestBuiltinHints_MinimumCount verifies the catalog has at least 6 entries.
func TestBuiltinHints_MinimumCount(t *testing.T) {
	hints := BuiltinHints()
	if len(hints) < 6 {
		t.Errorf("expected at least 6 hints, got %d", len(hints))
	}
	t.Logf("catalog has %d hints", len(hints))
}

// TestBuiltinHints_ContainsCoreAffordances verifies the four v1 affordances
// (scratchpad, memory_recall, playbook_research, peer_query) are present.
func TestBuiltinHints_ContainsCoreAffordances(t *testing.T) {
	hints := BuiltinHints()
	required := []string{"scratchpad", "memory_recall", "playbook_research", "peer_query"}
	for _, id := range required {
		if _, ok := HintByID(hints, id); !ok {
			t.Errorf("core affordance %q missing from catalog", id)
		}
	}
}

// TestBuiltinHints_UniqueIDs verifies no duplicate IDs.
func TestBuiltinHints_UniqueIDs(t *testing.T) {
	hints := BuiltinHints()
	seen := map[string]bool{}
	for _, h := range hints {
		if seen[h.ID] {
			t.Errorf("duplicate hint id %q in catalog", h.ID)
		}
		seen[h.ID] = true
	}
}

// TestBuiltinHints_AllHaveBodies verifies every hint has a non-empty body.
func TestBuiltinHints_AllHaveBodies(t *testing.T) {
	hints := BuiltinHints()
	for _, h := range hints {
		if strings.TrimSpace(h.Body) == "" {
			t.Errorf("hint %q has empty body", h.ID)
		}
	}
}

// TestLoadHintsFromYAML_Valid verifies a well-formed YAML document parses
// correctly.
func TestLoadHintsFromYAML_Valid(t *testing.T) {
	input := []byte(`
hints:
  - id: test-hint
    affordance: test
    body: "A test hint body."
    priority: 10
  - id: another-hint
    affordance: another
    body: "Another hint."
    priority: 5
`)
	hints, err := LoadHintsFromYAML(input)
	if err != nil {
		t.Fatalf("LoadHintsFromYAML returned error: %v", err)
	}
	if len(hints) != 2 {
		t.Errorf("expected 2 hints, got %d", len(hints))
	}
	if hints[0].ID != "test-hint" {
		t.Errorf("hints[0].ID = %q, want %q", hints[0].ID, "test-hint")
	}
	if hints[1].Priority != 5 {
		t.Errorf("hints[1].Priority = %d, want 5", hints[1].Priority)
	}
}

// TestLoadHintsFromYAML_MissingID returns an error when id is empty.
func TestLoadHintsFromYAML_MissingID(t *testing.T) {
	input := []byte(`
hints:
  - id: ""
    affordance: test
    body: "body text"
`)
	_, err := LoadHintsFromYAML(input)
	if err == nil {
		t.Error("expected error for missing id, got nil")
	}
}

// TestLoadHintsFromYAML_MissingAffordance returns an error when affordance is empty.
func TestLoadHintsFromYAML_MissingAffordance(t *testing.T) {
	input := []byte(`
hints:
  - id: my-hint
    affordance: ""
    body: "body text"
`)
	_, err := LoadHintsFromYAML(input)
	if err == nil {
		t.Error("expected error for missing affordance, got nil")
	}
}

// TestLoadHintsFromYAML_MissingBody returns an error when body is empty.
func TestLoadHintsFromYAML_MissingBody(t *testing.T) {
	input := []byte(`
hints:
  - id: my-hint
    affordance: test
    body: ""
`)
	_, err := LoadHintsFromYAML(input)
	if err == nil {
		t.Error("expected error for missing body, got nil")
	}
}

// TestLoadHintsFromYAML_DuplicateID returns an error on duplicate ids.
func TestLoadHintsFromYAML_DuplicateID(t *testing.T) {
	input := []byte(`
hints:
  - id: dup
    affordance: one
    body: "first"
  - id: dup
    affordance: two
    body: "second"
`)
	_, err := LoadHintsFromYAML(input)
	if err == nil {
		t.Error("expected error for duplicate id, got nil")
	}
}

// TestLoadHintsFromYAML_BadYAML returns an error on malformed YAML.
func TestLoadHintsFromYAML_BadYAML(t *testing.T) {
	input := []byte(`hints: [unclosed`)
	_, err := LoadHintsFromYAML(input)
	if err == nil {
		t.Error("expected error for bad YAML, got nil")
	}
}

// TestHintByID_Found verifies HintByID returns the correct entry.
func TestHintByID_Found(t *testing.T) {
	catalog := []Hint{
		{ID: "alpha", Affordance: "alpha", Body: "A"},
		{ID: "beta", Affordance: "beta", Body: "B"},
	}
	h, ok := HintByID(catalog, "beta")
	if !ok {
		t.Error("HintByID: expected to find 'beta'")
	}
	if h.ID != "beta" {
		t.Errorf("HintByID: got ID %q, want %q", h.ID, "beta")
	}
}

// TestHintByID_NotFound verifies HintByID returns false on miss.
func TestHintByID_NotFound(t *testing.T) {
	catalog := []Hint{{ID: "alpha", Affordance: "alpha", Body: "A"}}
	_, ok := HintByID(catalog, "missing")
	if ok {
		t.Error("HintByID: expected not found, got found")
	}
}

// TestSummarizeHints verifies the summary slice mirrors the catalog fields.
func TestSummarizeHints(t *testing.T) {
	catalog := []Hint{
		{ID: "x", Affordance: "X-af", Body: "X body", Priority: 10},
		{ID: "y", Affordance: "Y-af", Body: "Y body", Priority: 5},
	}
	sums := SummarizeHints(catalog)
	if len(sums) != 2 {
		t.Fatalf("expected 2 summaries, got %d", len(sums))
	}
	if sums[0].ID != "x" || sums[0].Priority != 10 {
		t.Errorf("sums[0] mismatch: %+v", sums[0])
	}
	if sums[1].ID != "y" || sums[1].Affordance != "Y-af" {
		t.Errorf("sums[1] mismatch: %+v", sums[1])
	}
}
