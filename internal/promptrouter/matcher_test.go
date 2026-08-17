package promptrouter_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/classify"
	"github.com/hollis-labs/nanite/internal/promptrouter"
)

// allBuiltins is the full builtin set, merged and sorted as the matcher expects.
func allBuiltins(t *testing.T) []promptrouter.Reflex {
	t.Helper()
	return promptrouter.BuiltinReflexes()
}

// ── 1. Every builtin fires on its documented example input ──────────────────

func TestMatcher_BuiltinReflexes_ExampleInputs(t *testing.T) {
	cases := []struct {
		input        string
		m1Tier       classify.ScopeTier
		m1Pattern    classify.ExecutionPattern
		wantReflexID string
	}{
		{
			// docs/promptrouter-catalog.md test-case table row 1
			input:        "Let's plan out the migration strategy for the auth service",
			m1Tier:       classify.TierOpen,
			m1Pattern:    classify.PatternSubagent,
			wantReflexID: "planner-mention",
		},
		{
			// row 2
			input:        "Research how other projects handle rate limiting",
			m1Tier:       classify.TierSmall,
			m1Pattern:    classify.PatternInline,
			wantReflexID: "researcher-mention",
		},
		{
			// row 3
			input:        "Can you review this PR and give me your assessment?",
			m1Tier:       classify.TierSmall,
			m1Pattern:    classify.PatternInline,
			wantReflexID: "reviewer-mention",
		},
		{
			// row 4 — strategist needs medium+ tier guard
			input:        "Brainstorm some options for the caching layer — tradeoffs matter",
			m1Tier:       classify.TierMedium,
			m1Pattern:    classify.PatternInline,
			wantReflexID: "strategist-mention",
		},
		{
			// row 5
			input:        "Document the new playbook API in the KB",
			m1Tier:       classify.TierSmall,
			m1Pattern:    classify.PatternInline,
			wantReflexID: "documentor-mention",
		},
		{
			// row 6
			input:        "Implement the reflex matcher module",
			m1Tier:       classify.TierSmall,
			m1Pattern:    classify.PatternInline,
			wantReflexID: "worker-execute",
		},
		{
			// row 7 — background-long-task (highest priority: 25)
			input:        "Index the whole codebase in the background tonight",
			m1Tier:       classify.TierOpen,
			m1Pattern:    classify.PatternBackground,
			wantReflexID: "background-long-task",
		},
		{
			// row 8 — planner-large-task needs large+ tier guard
			input:        "Break this down into phases — it's a big project",
			m1Tier:       classify.TierLarge,
			m1Pattern:    classify.PatternSubagent,
			wantReflexID: "planner-large-task",
		},
	}

	reflexes := allBuiltins(t)
	for _, tc := range cases {
		t.Run(tc.wantReflexID, func(t *testing.T) {
			got, ok := promptrouter.Match(tc.input, tc.m1Tier, tc.m1Pattern, reflexes)
			if !ok {
				t.Fatalf("Match(%q): no match, want %q", tc.input, tc.wantReflexID)
			}
			if got.Reflex.ID != tc.wantReflexID {
				t.Errorf("Match(%q): got reflex %q, want %q", tc.input, got.Reflex.ID, tc.wantReflexID)
			}
		})
	}
}

// ── 2. Priority ordering ─────────────────────────────────────────────────────

func TestMatcher_PriorityOrdering(t *testing.T) {
	// "review and fix" should fire reviewer-mention (priority 15) not
	// worker-execute (priority 10), because reviewer comes first.
	input := "review and fix the authentication flow"
	got, ok := promptrouter.Match(input, classify.TierSmall, classify.PatternInline, allBuiltins(t))
	if !ok {
		t.Fatalf("expected a match for %q, got none", input)
	}
	if got.Reflex.ID != "reviewer-mention" {
		t.Errorf("expected reviewer-mention (priority 15) to beat worker-execute (priority 10), got %q", got.Reflex.ID)
	}
}

// ── 3. Miss falls through ────────────────────────────────────────────────────

func TestMatcher_Miss_ReturnsFalse(t *testing.T) {
	// Totally unrelated input should not match any builtin promptrouter.
	input := "hello there"
	_, ok := promptrouter.Match(input, classify.TierTrivial, classify.PatternInline, allBuiltins(t))
	if ok {
		t.Errorf("Match(%q): expected no match, got one", input)
	}
}

// ── 4. Tier guard: strategist requires medium+ ───────────────────────────────

func TestMatcher_TierGuard_StrategistRequiresMedium(t *testing.T) {
	// "brainstorm" with TierTrivial should NOT fire strategist-mention
	// because scope_tier_hint=medium is not satisfied.
	input := "brainstorm"
	_, ok := promptrouter.Match(input, classify.TierTrivial, classify.PatternInline, allBuiltins(t))
	if ok {
		t.Errorf("strategist-mention should not fire on TierTrivial input")
	}

	// Same input with TierMedium SHOULD fire.
	got, ok := promptrouter.Match(input, classify.TierMedium, classify.PatternInline, allBuiltins(t))
	if !ok {
		t.Errorf("strategist-mention should fire on TierMedium input")
	} else if got.Reflex.ID != "strategist-mention" {
		t.Errorf("expected strategist-mention, got %q", got.Reflex.ID)
	}
}

// ── 5. ExecutionPattern guard: background-long-task ─────────────────────────

func TestMatcher_PatternGuard_BackgroundLongTask(t *testing.T) {
	// "async" alone without PatternBackground should fire background-long-task
	// (phrase matches), but the guard requires PatternBackground exactly.
	// The phrase "async" IS in the trigger list; the guard is execution_pattern_hint.
	// Note: background-long-task has execution_pattern_hint=background,
	// so it only fires when m1Pattern == PatternBackground.
	input := "async please"

	// With non-background pattern: should NOT match background-long-task.
	_, ok := promptrouter.Match(input, classify.TierSmall, classify.PatternInline, allBuiltins(t))
	if ok {
		t.Errorf("background-long-task should not fire when m1Pattern != PatternBackground")
	}

	// With PatternBackground: should match.
	got, ok := promptrouter.Match(input, classify.TierSmall, classify.PatternBackground, allBuiltins(t))
	if !ok {
		t.Errorf("background-long-task should fire when m1Pattern == PatternBackground")
	} else if got.Reflex.ID != "background-long-task" {
		t.Errorf("expected background-long-task, got %q", got.Reflex.ID)
	}
}

// ── 6. User override beats builtin at priority >= 50 ────────────────────────

func TestMatcher_UserOverride_BeatBuiltin(t *testing.T) {
	// Synthetic user override with priority 60 (> max builtin 25).
	userOverride := promptrouter.Reflex{
		ID: "my-custom-reflex",
		Triggers: promptrouter.Triggers{
			UserPhraseAnyOf: []string{"research"},
		},
		ResolvesTo:  promptrouter.Resolution{Pattern: "planner", Role: "planner", Profile: "planner"},
		SideEffects: promptrouter.SideEffects{ModeSignal: "planning"},
		Priority:    60,
	}

	merged := promptrouter.MergeReflexes(promptrouter.BuiltinReflexes(), []promptrouter.Reflex{userOverride})

	// "research something" would normally fire researcher-mention (priority 15).
	// With user override at priority 60, it should fire my-custom-reflex first.
	got, ok := promptrouter.Match("research something", classify.TierSmall, classify.PatternInline, merged)
	if !ok {
		t.Fatal("expected a match, got none")
	}
	if got.Reflex.ID != "my-custom-reflex" {
		t.Errorf("expected user override (my-custom-reflex) to win, got %q", got.Reflex.ID)
	}
}

// ── 7. User override at priority < builtin does NOT beat builtin ─────────────

func TestMatcher_UserOverride_LowPriorityLoses(t *testing.T) {
	lowOverride := promptrouter.Reflex{
		ID: "low-priority-override",
		Triggers: promptrouter.Triggers{
			UserPhraseAnyOf: []string{"research"},
		},
		ResolvesTo: promptrouter.Resolution{Pattern: "worker", Role: "worker", Profile: "worker"},
		Priority:   5, // lower than researcher-mention (15)
	}

	merged := promptrouter.MergeReflexes(promptrouter.BuiltinReflexes(), []promptrouter.Reflex{lowOverride})
	got, ok := promptrouter.Match("research something", classify.TierSmall, classify.PatternInline, merged)
	if !ok {
		t.Fatal("expected a match, got none")
	}
	if got.Reflex.ID != "researcher-mention" {
		t.Errorf("expected researcher-mention to beat low-priority override, got %q", got.Reflex.ID)
	}
}

// ── 8. Log row written on match ──────────────────────────────────────────────

func TestDispatcher_LogRowWritten(t *testing.T) {
	var captured []promptrouter.ReflexMatchEntry
	logger := &fakeLogger{capture: &captured}

	merged := promptrouter.MergeReflexes(promptrouter.BuiltinReflexes(), nil)
	promptrouter.AssignRoleWithReflex(
		"Implement the reflex matcher module",
		classify.TierSmall,
		classify.PatternInline,
		merged,
		"sess-001",
		"turn-001",
		logger,
	)

	if len(captured) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(captured))
	}
	e := captured[0]
	if e.ReflexID != "worker-execute" {
		t.Errorf("logged reflex ID = %q, want worker-execute", e.ReflexID)
	}
	if e.Source != "reflex" {
		t.Errorf("logged source = %q, want reflex", e.Source)
	}
	if e.SessionID != "sess-001" {
		t.Errorf("logged session_id = %q, want sess-001", e.SessionID)
	}
	if e.TurnID != "turn-001" {
		t.Errorf("logged turn_id = %q, want turn-001", e.TurnID)
	}
}

// ── 9. No log row on miss ────────────────────────────────────────────────────

func TestDispatcher_NoLogOnMiss(t *testing.T) {
	var captured []promptrouter.ReflexMatchEntry
	logger := &fakeLogger{capture: &captured}

	merged := promptrouter.MergeReflexes(promptrouter.BuiltinReflexes(), nil)
	promptrouter.AssignRoleWithReflex(
		"hello there",
		classify.TierTrivial,
		classify.PatternInline,
		merged,
		"sess-002",
		"",
		logger,
	)

	if len(captured) != 0 {
		t.Errorf("expected no log entries on miss, got %d", len(captured))
	}
}

// ── 10. Nil logger does not panic ────────────────────────────────────────────

func TestDispatcher_NilLogger_NoPanic(t *testing.T) {
	merged := promptrouter.MergeReflexes(promptrouter.BuiltinReflexes(), nil)
	// Must not panic.
	promptrouter.AssignRoleWithReflex(
		"Implement the reflex matcher module",
		classify.TierSmall,
		classify.PatternInline,
		merged,
		"sess-003",
		"",
		nil,
	)
}

// ── 11. YAML loader parses a valid file ──────────────────────────────────────

func TestLoader_ValidYAML(t *testing.T) {
	dir := t.TempDir()
	content := `
id: test-custom
triggers:
  user_phrase_any_of:
    - "custom trigger"
  scope_tier_hint: small
resolves_to:
  pattern: worker
  role: worker
  profile: worker
side_effects:
  mode_signal: execute
  dispatch_via: executeTask
priority: 55
`
	if err := os.WriteFile(filepath.Join(dir, "custom.yaml"), []byte(content), 0644); err != nil {
		t.Fatalf("write test yaml: %v", err)
	}

	reflexes, err := promptrouter.LoadUserReflexes(dir)
	if err != nil {
		t.Fatalf("LoadUserReflexes: %v", err)
	}
	if len(reflexes) != 1 {
		t.Fatalf("expected 1 reflex, got %d", len(reflexes))
	}
	r := reflexes[0]
	if r.ID != "test-custom" {
		t.Errorf("ID = %q, want test-custom", r.ID)
	}
	if r.Priority != 55 {
		t.Errorf("Priority = %d, want 55", r.Priority)
	}
	if r.Triggers.ScopeTierHint != classify.TierSmall {
		t.Errorf("ScopeTierHint = %v, want TierSmall", r.Triggers.ScopeTierHint)
	}
}

// ── 12. YAML loader skips missing directory without error ─────────────────────

func TestLoader_MissingDir_NoError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	reflexes, err := promptrouter.LoadUserReflexes(dir)
	if err != nil {
		t.Fatalf("unexpected error for missing dir: %v", err)
	}
	if len(reflexes) != 0 {
		t.Errorf("expected 0 reflexes from missing dir, got %d", len(reflexes))
	}
}

// ── 13. Empty input returns no match ─────────────────────────────────────────

func TestMatcher_EmptyInput(t *testing.T) {
	_, ok := promptrouter.Match("", classify.TierSmall, classify.PatternInline, allBuiltins(t))
	if ok {
		t.Error("expected no match for empty input")
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

type fakeLogger struct {
	capture *[]promptrouter.ReflexMatchEntry
}

func (f *fakeLogger) LogReflexMatch(entry promptrouter.ReflexMatchEntry) error {
	*f.capture = append(*f.capture, entry)
	return nil
}
