package reflexes

// TASKS/reflex-taxonomy/03-shared-decision-engine.md's Done-means: direct
// unit coverage for Resolve()'s combining-algorithm logic (Facet 2,
// docs/engineering/architecture/10-reflex-action-taxonomy.md), independent
// of any of the three real call sites. The two named regressions (the
// force_tool_choice bug and same-pass halt preemption) are also covered
// end-to-end: halt_preemption_test.go in this package exercises the
// same-pass preemption through Engine.EvaluateState, and
// internal/service/chat_reflexes_force_tool_choice_regression_test.go
// covers the full formatReflexReminder-level assertion the task's Done-means
// names explicitly.

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// captureSlogDefault swaps the package-level slog default logger for one
// writing to an in-memory buffer for the duration of the test, restoring
// the prior default via t.Cleanup. Used by
// TASKS/reflex-taxonomy/08-fix-resolve-fail-open-visibility.md's tests —
// Resolve()'s kind-lookup-failure/empty-CombiningAlgorithm Warn logging
// goes through the package-level slog logger (see resolve.go's doc
// comment for why: it's the one place all three real call sites'
// kindLookup failures funnel through), so asserting on it means capturing
// that default logger's output rather than a caller-injected one.
func captureSlogDefault(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func noCooldown(store.AgentReflex) bool { return false }

func kindLookupFixed(algo string) ActionKindLookup {
	return func(_ context.Context, name string) (*store.ReflexActionKind, error) {
		return &store.ReflexActionKind{Name: name, CombiningAlgorithm: algo}, nil
	}
}

// kindLookupPerKind resolves each kind's algorithm from a map, so a single
// Resolve() call can exercise multiple action kinds with different
// combining algorithms in one pass (e.g. the deny_overrides preemption
// test below).
func kindLookupPerKind(byKind map[string]string) ActionKindLookup {
	return func(_ context.Context, name string) (*store.ReflexActionKind, error) {
		algo, ok := byKind[name]
		if !ok {
			return nil, store.ErrReflexActionKindNotFound
		}
		return &store.ReflexActionKind{Name: name, CombiningAlgorithm: algo}, nil
	}
}

func alwaysFireReflex(id, name, actionKind string, priority int64, createdAt string) store.AgentReflex {
	return store.AgentReflex{
		ID:          id,
		Name:        name,
		TriggerKind: store.ReflexTriggerEvent,
		TriggerSpec: `{"name":"probe"}`,
		ActionKind:  actionKind,
		ActionSpec:  `{}`,
		Priority:    priority,
		CreatedAt:   createdAt,
	}
}

func neverFireReflex(id, name, actionKind string) store.AgentReflex {
	return store.AgentReflex{
		ID:          id,
		Name:        name,
		TriggerKind: store.ReflexTriggerEvent,
		TriggerSpec: `{"name":"a-different-event-that-never-happens"}`,
		ActionKind:  actionKind,
		ActionSpec:  `{}`,
	}
}

func fixtureState() State {
	return State{
		SessionID: "sess-resolve",
		Events:    []EventSignal{{EventType: "probe"}},
	}
}

func testExecutor() *Executor {
	return &Executor{Logger: slog.Default()}
}

// TestResolve_AllApplicable_SelectsEveryEligibleCandidate proves the
// all_applicable algorithm's current-default behavior: every eligible,
// fired candidate of a kind is selected, not just one.
func TestResolve_AllApplicable_SelectsEveryEligibleCandidate(t *testing.T) {
	candidates := []store.AgentReflex{
		alwaysFireReflex("r1", "reminder-1", store.ReflexActionInjectReminder, 10, "2026-01-01 00:00:00"),
		alwaysFireReflex("r2", "reminder-2", store.ReflexActionInjectReminder, 5, "2026-01-01 00:00:01"),
	}
	applied, outcomes, err := Resolve(context.Background(), candidates, fixtureState(), testExecutor(), noCooldown, kindLookupFixed("all_applicable"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(applied.Actions) != 2 {
		t.Fatalf("Actions = %d, want 2 (all_applicable selects every eligible candidate)", len(applied.Actions))
	}
	for _, oc := range outcomes {
		if !oc.Selected {
			t.Errorf("candidate %s Selected = false, want true", oc.ReflexID)
		}
		if oc.CombiningAlgorithm != "all_applicable" {
			t.Errorf("candidate %s CombiningAlgorithm = %q, want all_applicable", oc.ReflexID, oc.CombiningAlgorithm)
		}
	}
}

// TestResolve_FirstApplicable_HigherPriorityWins proves the
// first_applicable algorithm selects only the highest-priority eligible
// candidate of a kind — this is the force_tool_choice bug's fix at the
// primitive level (docs/engineering/architecture/
// 10-reflex-action-taxonomy.md's Facet 2 table: force_tool_choice is
// reclassified first_applicable specifically to fix two competing
// directives both landing in the same reminder).
func TestResolve_FirstApplicable_HigherPriorityWins(t *testing.T) {
	low := alwaysFireReflex("low", "force-low", store.ReflexActionForceToolChoice, 10, "2026-01-01 00:00:00")
	high := alwaysFireReflex("high", "force-high", store.ReflexActionForceToolChoice, 90, "2026-01-01 00:00:01")
	candidates := []store.AgentReflex{low, high}

	applied, outcomes, err := Resolve(context.Background(), candidates, fixtureState(), testExecutor(), noCooldown, kindLookupFixed("first_applicable"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(applied.Actions) != 1 {
		t.Fatalf("Actions = %d, want exactly 1 (first_applicable)", len(applied.Actions))
	}
	if applied.FiredReflexes[0].ID != "high" {
		t.Fatalf("winner = %q, want %q (higher priority)", applied.FiredReflexes[0].ID, "high")
	}
	for _, oc := range outcomes {
		wantSelected := oc.ReflexID == "high"
		if oc.Selected != wantSelected {
			t.Errorf("candidate %s Selected = %v, want %v", oc.ReflexID, oc.Selected, wantSelected)
		}
	}
}

// TestResolve_FirstApplicable_EqualPriority_EarlierCreatedAtWins proves the
// documented tie-break: when priority is equal, the earlier created_at
// candidate wins (matching Store.ListAgentReflexesForAgent's own
// `ORDER BY priority DESC, created_at ASC`).
func TestResolve_FirstApplicable_EqualPriority_EarlierCreatedAtWins(t *testing.T) {
	earlier := alwaysFireReflex("earlier", "force-earlier", store.ReflexActionForceToolChoice, 50, "2026-01-01 00:00:00")
	later := alwaysFireReflex("later", "force-later", store.ReflexActionForceToolChoice, 50, "2026-01-02 00:00:00")
	// Deliberately given out of chronological order to prove Resolve does
	// its own tie-break rather than trusting input order.
	candidates := []store.AgentReflex{later, earlier}

	applied, _, err := Resolve(context.Background(), candidates, fixtureState(), testExecutor(), noCooldown, kindLookupFixed("first_applicable"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(applied.Actions) != 1 {
		t.Fatalf("Actions = %d, want exactly 1", len(applied.Actions))
	}
	if applied.FiredReflexes[0].ID != "earlier" {
		t.Fatalf("winner = %q, want %q (earlier created_at, equal priority)", applied.FiredReflexes[0].ID, "earlier")
	}
}

// TestResolve_DenyOverrides_ShortCircuitsOtherKinds is the same-pass halt
// preemption regression at the Resolve() primitive level: a deny_overrides
// kind (halt_session, in production) and an unrelated all_applicable kind
// (inject_reminder) both have an eligible, fired candidate in the same
// pass. Resolve()'s output must contain ONLY the deny_overrides winner —
// the design doc's "no other action, of any kind, gets applied this tick."
func TestResolve_DenyOverrides_ShortCircuitsOtherKinds(t *testing.T) {
	halt := alwaysFireReflex("halt-1", "halt-reflex", store.ReflexActionHaltSession, 100, "2026-01-01 00:00:00")
	reminder := alwaysFireReflex("reminder-1", "reminder-reflex", store.ReflexActionInjectReminder, 999, "2026-01-01 00:00:00")
	candidates := []store.AgentReflex{halt, reminder}

	algoLookup := kindLookupPerKind(map[string]string{
		store.ReflexActionHaltSession:    "deny_overrides",
		store.ReflexActionInjectReminder: "all_applicable",
	})

	applied, outcomes, err := Resolve(context.Background(), candidates, fixtureState(), testExecutor(), noCooldown, algoLookup)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(applied.Actions) != 1 {
		t.Fatalf("Actions = %d, want exactly 1 (deny_overrides preempts the whole pass)", len(applied.Actions))
	}
	if applied.FiredReflexes[0].ID != "halt-1" {
		t.Fatalf("selected action = %q, want the halt_session reflex", applied.FiredReflexes[0].ID)
	}
	if applied.Actions[0].ActionKind != store.ReflexActionHaltSession {
		t.Fatalf("selected action kind = %q, want halt_session", applied.Actions[0].ActionKind)
	}
	for _, oc := range outcomes {
		wantSelected := oc.ReflexID == "halt-1"
		if oc.Selected != wantSelected {
			t.Errorf("candidate %s Selected = %v, want %v — inject_reminder's higher priority (999) must not matter under deny_overrides", oc.ReflexID, oc.Selected, wantSelected)
		}
	}
}

// TestResolve_CooldownSuppressed_DistinctFromTriggerFalse proves the
// task's own brief: a candidate whose trigger fires but is in cooldown is
// "not eligible," a fact the CandidateOutcome must keep distinguishable
// from a candidate whose trigger simply never fired.
func TestResolve_CooldownSuppressed_DistinctFromTriggerFalse(t *testing.T) {
	suppressed := alwaysFireReflex("suppressed", "cooldown-probe", store.ReflexActionInjectReminder, 10, "2026-01-01 00:00:00")
	neverFires := neverFireReflex("never-fires", "no-trigger-probe", store.ReflexActionInjectReminder)
	candidates := []store.AgentReflex{suppressed, neverFires}

	cooldownFn := func(r store.AgentReflex) bool { return r.ID == "suppressed" }

	applied, outcomes, err := Resolve(context.Background(), candidates, fixtureState(), testExecutor(), cooldownFn, kindLookupFixed("all_applicable"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(applied.Actions) != 0 {
		t.Fatalf("Actions = %d, want 0 (both candidates are ineligible, for different reasons)", len(applied.Actions))
	}

	var byID = map[string]CandidateOutcome{}
	for _, oc := range outcomes {
		byID[oc.ReflexID] = oc
	}
	suppressedOC := byID["suppressed"]
	if !suppressedOC.TriggerFired {
		t.Errorf("suppressed candidate TriggerFired = false, want true (its trigger did fire, cooldown made it ineligible)")
	}
	if !suppressedOC.CooldownSuppressed {
		t.Errorf("suppressed candidate CooldownSuppressed = false, want true")
	}
	if suppressedOC.Eligible {
		t.Errorf("suppressed candidate Eligible = true, want false")
	}

	neverOC := byID["never-fires"]
	if neverOC.TriggerFired {
		t.Errorf("never-fires candidate TriggerFired = true, want false")
	}
	if neverOC.CooldownSuppressed {
		t.Errorf("never-fires candidate CooldownSuppressed = true, want false — it was never eligible to begin with, cooldown is irrelevant to it")
	}
}

// TestResolve_KindLookupFailure_DefaultsToAllApplicable proves an
// unresolvable action_kind fails open to all_applicable (today's universal
// pre-taxonomy behavior) rather than erroring the whole pass.
func TestResolve_KindLookupFailure_DefaultsToAllApplicable(t *testing.T) {
	candidates := []store.AgentReflex{
		alwaysFireReflex("r1", "reminder-1", store.ReflexActionInjectReminder, 10, "2026-01-01 00:00:00"),
		alwaysFireReflex("r2", "reminder-2", store.ReflexActionInjectReminder, 5, "2026-01-01 00:00:01"),
	}
	failingLookup := func(_ context.Context, _ string) (*store.ReflexActionKind, error) {
		return nil, store.ErrReflexActionKindNotFound
	}
	applied, _, err := Resolve(context.Background(), candidates, fixtureState(), testExecutor(), noCooldown, failingLookup)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(applied.Actions) != 2 {
		t.Fatalf("Actions = %d, want 2 (unresolvable kind should fail open to all_applicable)", len(applied.Actions))
	}
}

// TestResolve_NilExecutor_ReturnsError proves Resolve rejects a genuine
// caller-programming error rather than silently no-op'ing.
func TestResolve_NilExecutor_ReturnsError(t *testing.T) {
	candidates := []store.AgentReflex{
		alwaysFireReflex("r1", "reminder-1", store.ReflexActionInjectReminder, 10, "2026-01-01 00:00:00"),
	}
	_, _, err := Resolve(context.Background(), candidates, fixtureState(), nil, noCooldown, kindLookupFixed("all_applicable"))
	if err == nil {
		t.Fatal("Resolve with a nil Executor: err = nil, want a non-nil error")
	}
}

// TestResolve_KindLookupFailure_LogsWarning is
// TASKS/reflex-taxonomy/08-fix-resolve-fail-open-visibility.md's primary
// Done-means assertion: a kindLookup error must no longer be silently
// swallowed — it fails open to all_applicable exactly as before (see
// TestResolve_KindLookupFailure_DefaultsToAllApplicable, unchanged), but
// now also produces a Warn-level log line naming the action kind and the
// underlying lookup error — this is exactly the case engine.go's
// kindLookup closure's own "action kind %q not cached" error used to hit
// with zero logging anywhere in the chain.
func TestResolve_KindLookupFailure_LogsWarning(t *testing.T) {
	buf := captureSlogDefault(t)

	candidates := []store.AgentReflex{
		alwaysFireReflex("r1", "reminder-1", store.ReflexActionInjectReminder, 10, "2026-01-01 00:00:00"),
	}
	lookupErr := fmt.Errorf("action kind %q not cached", store.ReflexActionInjectReminder)
	failingLookup := func(_ context.Context, _ string) (*store.ReflexActionKind, error) {
		return nil, lookupErr
	}

	applied, _, err := Resolve(context.Background(), candidates, fixtureState(), testExecutor(), noCooldown, failingLookup)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// Fail-open behavior itself must be unchanged — still all_applicable.
	if len(applied.Actions) != 1 {
		t.Fatalf("Actions = %d, want 1 (unresolvable kind still fails open to all_applicable)", len(applied.Actions))
	}

	logged := buf.String()
	if !strings.Contains(logged, "level=WARN") {
		t.Errorf("log output missing a Warn-level line; got:\n%s", logged)
	}
	if !strings.Contains(logged, store.ReflexActionInjectReminder) {
		t.Errorf("log output missing the action kind %q; got:\n%s", store.ReflexActionInjectReminder, logged)
	}
	if !strings.Contains(logged, "not cached") {
		t.Errorf("log output missing the underlying lookup error text; got:\n%s", logged)
	}
}

// TestResolve_EmptyCombiningAlgorithm_LogsWarning covers the second half
// of the same fail-open: kindLookup succeeds but returns a row with an
// empty CombiningAlgorithm (e.g. a reflex_action_kinds row edited
// directly, bypassing the taxonomy's seed data — no CRUD surface
// validates this column). Same fail-open target (all_applicable), same
// Warn-level visibility requirement.
func TestResolve_EmptyCombiningAlgorithm_LogsWarning(t *testing.T) {
	buf := captureSlogDefault(t)

	candidates := []store.AgentReflex{
		alwaysFireReflex("r1", "reminder-1", store.ReflexActionInjectReminder, 10, "2026-01-01 00:00:00"),
	}
	emptyAlgoLookup := func(_ context.Context, name string) (*store.ReflexActionKind, error) {
		return &store.ReflexActionKind{Name: name, CombiningAlgorithm: ""}, nil
	}

	applied, _, err := Resolve(context.Background(), candidates, fixtureState(), testExecutor(), noCooldown, emptyAlgoLookup)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(applied.Actions) != 1 {
		t.Fatalf("Actions = %d, want 1 (empty combining_algorithm still fails open to all_applicable)", len(applied.Actions))
	}

	logged := buf.String()
	if !strings.Contains(logged, "level=WARN") {
		t.Errorf("log output missing a Warn-level line; got:\n%s", logged)
	}
	if !strings.Contains(logged, store.ReflexActionInjectReminder) {
		t.Errorf("log output missing the action kind %q; got:\n%s", store.ReflexActionInjectReminder, logged)
	}
}

// TestResolve_KindLookupSuccess_NoWarningLogged is the negative-space
// check for the two tests above: a clean, successful kindLookup must not
// produce any Warn-level noise — otherwise every normal pass would spam
// logs.
func TestResolve_KindLookupSuccess_NoWarningLogged(t *testing.T) {
	buf := captureSlogDefault(t)

	candidates := []store.AgentReflex{
		alwaysFireReflex("r1", "reminder-1", store.ReflexActionInjectReminder, 10, "2026-01-01 00:00:00"),
	}
	_, _, err := Resolve(context.Background(), candidates, fixtureState(), testExecutor(), noCooldown, kindLookupFixed("all_applicable"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if logged := buf.String(); strings.Contains(logged, "level=WARN") {
		t.Errorf("expected no Warn-level log line for a clean kindLookup; got:\n%s", logged)
	}
}
