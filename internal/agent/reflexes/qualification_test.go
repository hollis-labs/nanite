package reflexes

// Qualification replays Nanite-authored fixtures through both implementations.
// The independent pre-adoption algorithms live only in *_reference_*_test.go.
// Both the released library and production host wrappers must match their full
// persisted metadata and effects; no trace normalization masks a divergence.
import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	shared "github.com/hollis-labs/go-reflexes"
	"github.com/hollis-labs/nanite/internal/store"
)

var qualificationNow = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

func qualificationJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func qualificationCopy[T any](t *testing.T, v any) T {
	t.Helper()
	var out T
	if err := json.Unmarshal([]byte(qualificationJSON(t, v)), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// State JSON transfers the first-class live signals without rewriting authored
// trigger_spec or inserting classification into generic library attributes.
func qualificationState(t *testing.T, state State) shared.State {
	return qualificationCopy[shared.State](t, state)
}

type qualificationResult struct {
	Actions   any
	Outcomes  any
	Traces    []map[string]any
	Counts    map[string]int64
	LastFired map[string]bool
	Effects   []string
}

// Capture real persisted metadata, never transcribed library goldens. Database
// row timestamps and last_fired_at wall clocks are excluded; firing increments,
// metadata and effect ordering are compared. IDs and candidate dates are fixed.
func qualificationPersisted(t *testing.T, st *store.Store, actions, outcomes any, effects []string) qualificationResult {
	t.Helper()
	out := qualificationResult{Actions: actions, Outcomes: outcomes, Counts: map[string]int64{}, LastFired: map[string]bool{}, Effects: effects, Traces: []map[string]any{}}
	rows, err := st.DB.Query(`SELECT metadata FROM event_log WHERE category='reflex' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		out.Traces = append(out.Traces, qualificationCopy[map[string]any](t, json.RawMessage(raw)))
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err = rows.Close(); err != nil {
		t.Fatal(err)
	}
	rows, err = st.DB.Query(`SELECT id, fired_count, COALESCE(last_fired_at,'') FROM agent_reflexes ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		var n int64
		var firedAt string
		if err = rows.Scan(&id, &n, &firedAt); err != nil {
			t.Fatal(err)
		}
		out.Counts[id] = n
		out.LastFired[id] = firedAt != ""
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err = rows.Close(); err != nil {
		t.Fatal(err)
	}
	return out
}

func qualificationStore(t *testing.T, candidates []store.AgentReflex) *store.Store {
	t.Helper()
	st := newReflexTestStore(t) // storetest.New, fully migrated isolated fixture.
	ctx := context.Background()
	if err := st.CreateAgent(ctx, &store.AgentProfile{ID: "qual-agent", Slug: "qual-agent", Name: "Qualification", Class: "advisor", SystemPrompt: "fixture"}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateSession(ctx, &store.Session{ID: "qual-session", Title: "Qualification"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO workflow_runs (id, started_at) VALUES ('qual-run','2026-09-30 00:00:00')`); err != nil {
		t.Fatal(err)
	}
	for _, r := range candidates {
		if _, err := st.InsertAgentReflex(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func qualificationReflex(id, kind string, priority int64) store.AgentReflex {
	r := alwaysFireReflex(id, id, kind, priority, "2026-09-30 00:00:00")
	r.AgentID, r.Status, r.ProvenanceTier = "qual-agent", store.ReflexStatusActive, "operator"
	return r
}

// Source gates stay in Nanite. Both engines receive exactly these stored rows.
func qualificationCandidates(t *testing.T, st *store.Store, scope string) []store.AgentReflex {
	t.Helper()
	ctx := context.Background()
	var rows []store.AgentReflex
	var err error
	switch scope {
	case "workflow":
		rows, err = st.ListAgentReflexesForWorkflowRun(ctx, "qual-run", "qual-agent", "advisor")
	case "loop":
		rows, err = st.ListAgentReflexesForLoopRun(ctx, "qual-loop")
	default:
		rows, err = st.ListAgentReflexesForAgent(ctx, "qual-agent", "advisor")
	}
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func qualificationReplay(t *testing.T, candidates []store.AgentReflex, state State, scope string, failHook bool) (qualificationResult, qualificationResult) {
	t.Helper()
	state.SessionID, state.AgentID, state.AgentClass = "qual-session", "qual-agent", "advisor"
	ns, ls, ps := qualificationStore(t, candidates), qualificationStore(t, candidates), qualificationStore(t, candidates)
	nr, lr := qualificationCandidates(t, ns, scope), qualificationCandidates(t, ls, scope)
	if qualificationJSON(t, nr) != qualificationJSON(t, lr) {
		t.Fatal("different host candidate inputs")
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()
	neffects, leffects := []string{}, []string{}
	newHostExecutor := func(effects *[]string) *Executor {
		nx := &Executor{Logger: logger}
		nx.Halt = func(context.Context, string, string, map[string]interface{}) error {
			*effects = append(*effects, "halt_session")
			if failHook {
				return errors.New("fixture failure")
			}
			return nil
		}
		nx.Schedule = func(context.Context, string, map[string]interface{}) error {
			*effects = append(*effects, "add_schedule")
			if failHook {
				return errors.New("fixture failure")
			}
			return nil
		}
		nx.SendMessage = func(context.Context, string, map[string]interface{}) error {
			*effects = append(*effects, "send_message")
			if failHook {
				return errors.New("fixture failure")
			}
			return nil
		}
		return nx
	}
	nx := newHostExecutor(&neffects)
	peffects := []string{}
	px := newHostExecutor(&peffects)
	lx := shared.NewExecutor(logger)
	// These adapters are host-owned in both versions. resume_loop_run is a
	// staged decision at Resolve, with the real runtime effect at its call site.
	lx.Stage("resume_loop_run")
	for _, kind := range []string{"halt_session", "add_schedule", "send_message"} {
		lx.Handle(kind, shared.HandlerFunc(func(_ context.Context, f shared.Firing) error {
			leffects = append(leffects, f.Reflex.ActionKind)
			if failHook {
				return errors.New("fixture failure")
			}
			return nil
		}))
	}
	nk := func(ctx context.Context, kind string) (*store.ReflexActionKind, error) {
		return ns.GetReflexActionKind(ctx, kind)
	}
	lk := func(ctx context.Context, kind string) (*shared.ActionKind, error) {
		k, err := ls.GetReflexActionKind(ctx, kind)
		if err != nil {
			return nil, err
		}
		v := qualificationCopy[shared.ActionKind](t, k)
		return &v, nil
	}
	nc := func(r store.AgentReflex) bool {
		k, err := nk(ctx, r.ActionKind)
		var d *int64
		if err == nil && k != nil {
			d = k.DefaultRecurrenceSeconds
		}
		return referenceRecentlyFired(r, qualificationNow, referenceEffectiveCooldown(d, r.RecurrenceOverrideSeconds))
	}
	lc := func(r shared.Reflex) bool {
		k, err := lk(ctx, r.ActionKind)
		var d *int64
		if err == nil && k != nil {
			d = k.DefaultRecurrenceSeconds
		}
		return shared.RecentlyFired(r, qualificationNow, shared.EffectiveCooldown(d, r.RecurrenceOverrideSeconds))
	}
	na, no, err := referenceResolve(ctx, nr, state, nx, nc, nk)
	if err != nil {
		t.Fatal(err)
	}
	la, lo, err := shared.Resolve(ctx, qualificationCopy[[]shared.Reflex](t, lr), qualificationState(t, state), lx, lc, lk)
	if err != nil {
		t.Fatal(err)
	}
	referenceEmitFirings(ctx, ns, nil, na, no, state, FiringContext{AgentID: state.AgentID, AgentClass: state.AgentClass}, logger)
	shared.EmitFirings(ctx, ls, nil, la, lo, qualificationState(t, state), shared.FiringContext{AgentID: state.AgentID, AgentClass: state.AgentClass}, logger)
	pr := qualificationCandidates(t, ps, scope)
	pa, po, err := Resolve(ctx, pr, state, px, func(r store.AgentReflex) bool {
		k, lookupErr := ps.GetReflexActionKind(ctx, r.ActionKind)
		var d *int64
		if lookupErr == nil && k != nil {
			d = k.DefaultRecurrenceSeconds
		}
		return RecentlyFired(r, qualificationNow, EffectiveCooldown(d, r.RecurrenceOverrideSeconds))
	}, ps.GetReflexActionKind)
	if err != nil {
		t.Fatal(err)
	}
	EmitFirings(ctx, ps, nil, pa, po, state, FiringContext{AgentID: state.AgentID, AgentClass: state.AgentClass}, logger)
	n := qualificationPersisted(t, ns, na.Actions, no, neffects)
	production := qualificationPersisted(t, ps, pa.Actions, po, peffects)
	if qualificationJSON(t, n) != qualificationJSON(t, production) {
		t.Fatalf("production adapter divergence input=%s state=%s reference=%s production=%s", qualificationJSON(t, pr), qualificationJSON(t, state), qualificationJSON(t, n), qualificationJSON(t, production))
	}
	return n, qualificationPersisted(t, ls, la.Actions, lo, leffects)
}

func TestGoReflexesQualification_ResolveTraces(t *testing.T) {
	// Derived from resolve_test.go, halt_preemption_test.go, recurrence_test.go
	// and loop_resume_reflex_test.go. All results and traces come from execution.
	reminder := qualificationReflex("reminder", "inject_reminder", 999)
	halt := qualificationReflex("halt", "halt_session", 10)
	low := qualificationReflex("low", "force_tool_choice", 10)
	high := qualificationReflex("high", "force_tool_choice", 90)
	tie := qualificationReflex("tie", "force_tool_choice", 90)
	tie.CreatedAt = "2026-09-29 00:00:00"
	cooldown := qualificationReflex("cooldown", "inject_reminder", 1)
	cooldown.LastFiredAt = qualificationNow.Add(-time.Minute).Format(time.RFC3339)
	never := qualificationReflex("never", "inject_reminder", 2)
	never.TriggerSpec = `{"name":"absent"}`
	malformed := qualificationReflex("malformed", "inject_reminder", 1001)
	malformed.TriggerSpec = `{`
	badAction := qualificationReflex("bad-action", "inject_reminder", 1000)
	badAction.ActionSpec = `{`
	schedule := qualificationReflex("schedule", "add_schedule", 5)
	message := qualificationReflex("message", "send_message", 6)
	resume := qualificationReflex("resume", "resume_loop_run", 7)
	resume.ActionSpec = `{"loop_run_id":"qual-loop"}`
	workflow := qualificationReflex("workflow", "dispatch_to_agent", 8)
	workflow.WorkflowRunID = "qual-run"
	workflow.ActionSpec = `{"agent_slug":"planner"}`
	zero := int64(0)
	noCooldownRow := cooldown
	noCooldownRow.ID, noCooldownRow.Name, noCooldownRow.RecurrenceOverrideSeconds = "no-cooldown", "no-cooldown", &zero
	cases := []struct {
		name    string
		rows    []store.AgentReflex
		scope   string
		fail    bool
		winners []string
	}{
		{"all-applicable", []store.AgentReflex{reminder, schedule, message}, "", false, []string{"reminder", "message", "schedule"}},
		{"halt-preempts-higher-reminder", []store.AgentReflex{reminder, halt}, "", false, []string{"halt"}},
		{"priority-and-created-tie", []store.AgentReflex{low, high, tie}, "", false, []string{"tie"}},
		{"cooldown-vs-no-trigger", []store.AgentReflex{cooldown, never, noCooldownRow}, "", false, []string{"no-cooldown"}},
		{"parse-failures-continue", []store.AgentReflex{malformed, badAction, reminder}, "", false, []string{"reminder"}},
		{"hook-failures-still-count", []store.AgentReflex{schedule, message}, "", true, []string{"message", "schedule"}},
		{"workflow-candidates", []store.AgentReflex{workflow, reminder}, "workflow", false, []string{"workflow"}},
		{"loop-candidates", []store.AgentReflex{resume, reminder}, "loop", false, []string{"resume"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n, l := qualificationReplay(t, c.rows, State{Events: []EventSignal{{EventType: "probe"}}}, c.scope, c.fail)
			if qualificationJSON(t, n) != qualificationJSON(t, l) {
				t.Fatalf("divergence input=%s\nnanite=%s\nlib=%s", qualificationJSON(t, c.rows), qualificationJSON(t, n), qualificationJSON(t, l))
			}
			actions := qualificationCopy[[]AppliedAction](t, n.Actions)
			got := []string{}
			for _, a := range actions {
				got = append(got, a.ReflexID)
			}
			if !reflect.DeepEqual(got, c.winners) {
				t.Fatalf("Nanite winners=%v want=%v", got, c.winners)
			}
			t.Logf("equivalent nanite=%s lib=%s", qualificationJSON(t, n), qualificationJSON(t, l))
		})
	}
}

func TestGoReflexesQualification_SeedPredicates(t *testing.T) {
	// Reuse Nanite's authored regression states rather than library goldens.
	cases := []struct {
		name, class, seed string
		state             State
		want              bool
	}{
		{"echo", "process", "drift_detector_echo", fixtureEchoState(), true},
		{"healthy-compression", "process", "drift_detector_echo", fixtureHealthyCompressionState(), false},
		{"runaway", "process", "runaway_superlative_detector", fixtureSuperlativeState(), true},
		{"single-superlative", "process", "runaway_superlative_detector", fixtureSingleSuperlativeState(), false},
		{"open-subagent", "advisor", "dispatch_to_agent_open_subagent", State{ScopeTier: "open", ExecutionPattern: "subagent"}, true},
		{"closed-subagent", "advisor", "dispatch_to_agent_open_subagent", State{ScopeTier: "small", ExecutionPattern: "subagent"}, false},
		{"open-inline", "advisor", "dispatch_to_agent_open_subagent", State{ScopeTier: "open", ExecutionPattern: "inline"}, false},
		{"unclassified", "advisor", "dispatch_to_agent_open_subagent", State{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kind, spec := seedTriggerSpec(t, c.class, c.seed)
			n, ne := referenceEvaluateTrigger(kind, spec, c.state)
			l, le := shared.EvaluateTrigger(kind, spec, qualificationState(t, c.state))
			pv, perr := EvaluateTrigger(kind, spec, c.state)
			if pv != n || perr != nil {
				t.Fatalf("production adapter input=%s spec=%s reference=%v/%v production=%v/%v", qualificationJSON(t, c.state), spec, n, ne, pv, perr)
			}
			if ne != nil || le != nil || n != c.want || l != n {
				t.Fatalf("input=%s spec=%s nanite=%v/%v lib=%v/%v", qualificationJSON(t, c.state), spec, n, ne, l, le)
			}
			r := qualificationReflex(c.seed, "halt_session", 10)
			r.TriggerKind, r.TriggerSpec = kind, spec
			for _, seed := range BaseSeeds() {
				if seed.Name == c.seed && seed.ClassTag == c.class {
					r.ActionKind = seed.ActionKind
					r.ActionSpec = qualificationJSON(t, seed.ActionSpec)
					r.ProvenanceTier = "system"
					r.CreatedBy = "system"
				}
			}
			nr, lr := qualificationReplay(t, []store.AgentReflex{r}, c.state, "", false)
			if qualificationJSON(t, nr) != qualificationJSON(t, lr) {
				t.Fatalf("nanite=%s lib=%s", qualificationJSON(t, nr), qualificationJSON(t, lr))
			}
		})
	}
}

func TestGoReflexesQualification_DispatchEquivalence(t *testing.T) {
	// Real dispatch seed from TestAttemptReflexDispatch_RealSeededReflex_
	// ScopeTierOpenSubagent_RoutesToPlanner. No translation of stored predicates.
	kind, spec := seedTriggerSpec(t, "advisor", "dispatch_to_agent_open_subagent")
	cases := []struct{ name, spec string }{
		{"scope-tier", `{"kind":"scope_tier","value":"open"}`},
		{"execution-pattern", `{"kind":"execution_pattern","value":"subagent"}`},
		{"real-open-subagent-seed", spec},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			state := State{SessionID: "qual-session", AgentID: "qual-agent", AgentClass: "advisor", ScopeTier: "open", ExecutionPattern: "subagent"}
			n, ne := referenceEvaluateTrigger(kind, c.spec, state)
			l, le := shared.EvaluateTrigger(kind, c.spec, qualificationState(t, state))
			pv, perr := EvaluateTrigger(kind, c.spec, state)
			if pv != n || perr != nil {
				t.Fatalf("production adapter input=%s spec=%s reference=%v/%v production=%v/%v", qualificationJSON(t, state), c.spec, n, ne, pv, perr)
			}
			if !n || ne != nil || l != n || le != nil {
				t.Fatalf("input=%s spec=%s nanite=%v/%v lib=%v/%v", qualificationJSON(t, state), c.spec, n, ne, l, le)
			}
			r := qualificationReflex("dispatch", "dispatch_to_agent", 10)
			r.TriggerKind, r.TriggerSpec, r.ActionSpec = kind, c.spec, `{"agent_slug":"planner"}`
			if c.name == "real-open-subagent-seed" {
				for _, seed := range BaseSeeds() {
					if seed.ClassTag == "advisor" && seed.Name == "dispatch_to_agent_open_subagent" {
						r.Name = seed.Name
						r.ActionSpec = qualificationJSON(t, seed.ActionSpec)
						r.CreatedBy = "system"
						r.ProvenanceTier = "system"
					}
				}
			}
			nr, lr := qualificationReplay(t, []store.AgentReflex{r}, state, "", false)
			if qualificationJSON(t, nr) != qualificationJSON(t, lr) {
				t.Fatalf("input=%s state=%s nanite=%s lib=%s", qualificationJSON(t, r), qualificationJSON(t, state), qualificationJSON(t, nr), qualificationJSON(t, lr))
			}
			if len(qualificationCopy[[]AppliedAction](t, nr.Actions)) != 1 || nr.Counts[r.ID] != 1 || len(nr.Traces) != 1 {
				t.Fatal("dispatch must select and count one planner action")
			}
			t.Logf("equivalent input=%s state=%s nanite=%s lib=%s", qualificationJSON(t, r), qualificationJSON(t, state), qualificationJSON(t, nr), qualificationJSON(t, lr))
		})
	}
}

func TestGoReflexesQualification_TraceEquivalence(t *testing.T) {
	// Event dispatch isolates trace placement from classification predicates.
	r := qualificationReflex("dispatch-event", "dispatch_to_agent", 10)
	r.ActionSpec = `{"agent_slug":"planner"}`
	for _, c := range []struct{ name, scope, pattern string }{
		{"both", "open", "subagent"}, {"scope-only", "Open", ""},
		{"pattern-only", "", "Background"}, {"unset", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			state := State{ScopeTier: c.scope, ExecutionPattern: c.pattern, Events: []EventSignal{{EventType: "probe"}}}
			n, l := qualificationReplay(t, []store.AgentReflex{r}, state, "", false)
			if qualificationJSON(t, n) != qualificationJSON(t, l) {
				t.Fatalf("input=%s state=%s nanite=%s lib=%s", qualificationJSON(t, r), qualificationJSON(t, state), qualificationJSON(t, n), qualificationJSON(t, l))
			}
			if len(n.Traces) != 1 {
				t.Fatal("missing real trace")
			}
			for key, want := range map[string]string{"scope_tier": c.scope, "execution_pattern": c.pattern} {
				got, present := n.Traces[0][key]
				if present != (want != "") || present && got != want {
					t.Fatalf("signal %s=%v present=%v, want %q", key, got, present, want)
				}
			}
			if _, present := l.Traces[0]["attrs"]; present {
				t.Fatal("classification must not be duplicated into attrs")
			}
			t.Logf("equivalent input=%s state=%s nanite=%s lib=%s", qualificationJSON(t, r), qualificationJSON(t, state), qualificationJSON(t, n), qualificationJSON(t, l))
		})
	}
}

func TestGoReflexesQualification_ScalarPredicateEquivalence(t *testing.T) {
	for _, kind := range []string{"scope_tier", "execution_pattern"} {
		for _, c := range []struct {
			name, signal, args string
			want               bool
		}{
			{"default", "open", `"value":"open"`, true},
			{"equals", "open", `"op":"=","value":"open"`, true},
			{"double-equals", "open", `"op":"==","value":"open"`, true},
			{"empty-op", "open", `"op":"","value":"open"`, true},
			{"non-string-op", "open", `"op":42,"value":"open"`, true},
			{"different", "open", `"value":"large"`, false},
			{"not-equal", "open", `"op":"!=","value":"large"`, true},
			{"not-equal-same", "open", `"op":"!=","value":"open"`, false},
			{"case", "Open", `"value":"open"`, false},
			{"unknown-op", "open", `"op":"contains","value":"open"`, false},
			{"unset", "", `"value":"open"`, false},
			{"unset-not-equal", "", `"op":"!=","value":"open"`, true},
			{"empty-value", "", `"value":""`, true},
			{"missing-value", "", `"window":99`, true},
			{"non-string-value", "", `"value":42`, true},
			{"null-value", "", `"value":null`, true},
			{"set-missing-value", "open", `"window":99`, false},
			{"set-non-string-value", "open", `"value":42`, false},
			{"no-history-guard", "open", `"window":99,"value":"open"`, true},
		} {
			t.Run(kind+"/"+c.name, func(t *testing.T) {
				state := State{}
				if kind == "scope_tier" {
					state.ScopeTier = c.signal
					state.ExecutionPattern = "other"
				} else {
					state.ExecutionPattern = c.signal
					state.ScopeTier = "other"
				}
				spec := `{"kind":"` + kind + `",` + c.args + `}`
				n, ne := referenceEvaluateTrigger("predicate", spec, state)
				l, le := shared.EvaluateTrigger("predicate", spec, qualificationState(t, state))
				pv, perr := EvaluateTrigger("predicate", spec, state)
				if pv != n || perr != nil {
					t.Fatalf("production adapter input=%s spec=%s reference=%v/%v production=%v/%v", qualificationJSON(t, state), spec, n, ne, pv, perr)
				}
				if ne != nil || le != nil || n != c.want || l != n {
					t.Fatalf("input=%s spec=%s nanite=%v/%v lib=%v/%v want=%v", qualificationJSON(t, state), spec, n, ne, l, le, c.want)
				}
			})
		}
	}
}

// Compile-time reminder that production Source/TraceStore ownership is Nanite's.
var _ shared.TraceStore = (*store.Store)(nil)

type qualificationSource struct {
	t  *testing.T
	st *store.Store
}

func (s qualificationSource) Candidates(_ context.Context, _, _ string) ([]shared.Reflex, error) {
	return qualificationCopy[[]shared.Reflex](s.t, qualificationCandidates(s.t, s.st, "")), nil
}
func (s qualificationSource) ActionKinds(ctx context.Context) ([]shared.ActionKind, error) {
	kinds, err := s.st.ListReflexActionKinds(ctx)
	if err != nil {
		return nil, err
	}
	return qualificationCopy[[]shared.ActionKind](s.t, kinds), nil
}

type qualificationFilters struct {
	hooks *fakeReflexPluginHooks
}

func (f qualificationFilters) FilterState(_ context.Context, s shared.State) (shared.State, error) {
	f.hooks.stateFilters++
	return s, nil
}
func (f qualificationFilters) FilterAction(_ context.Context, a shared.AppliedAction, _ map[string]any) (shared.AppliedAction, error) {
	f.hooks.actionFilters++
	a.Spec["body"] = "filtered"
	return a, nil
}
func (f qualificationFilters) Fired(_ string, _ map[string]any)  { f.hooks.fired++ }
func (f qualificationFilters) Staged(_ string, _ map[string]any) { f.hooks.staged++ }

func TestGoReflexesQualification_CollectedStateEngineTrace(t *testing.T) {
	// Replays TestStateCollector_CollectsUserMessagesAndStructuredRefs data
	// through the actual collector, Engine.EvaluateState and shared.Engine.Run.
	r := qualificationReflex("structured", "inject_reminder", 10)
	r.TriggerKind = "predicate"
	r.TriggerSpec = `{"kind":"AND","clauses":[{"kind":"user_regex_window","window":1,"pattern":"document.*ticket"},{"kind":"tool_name_window","window":1,"names":["torque_task_create"]},{"kind":"envelope_type_window","window":1,"names":["options"]}]}`
	r.ActionSpec = `{"body":"original"}`
	dispatch := qualificationReflex("excluded-dispatch", "dispatch_to_agent", 20)
	resume := qualificationReflex("excluded-resume", "resume_loop_run", 30)
	ns, ls, ps := qualificationStore(t, []store.AgentReflex{r, dispatch, resume}), qualificationStore(t, []store.AgentReflex{r, dispatch, resume}), qualificationStore(t, []store.AgentReflex{r, dispatch, resume})
	ctx := context.Background()
	for _, st := range []*store.Store{ns, ls, ps} {
		for _, msg := range []store.Message{
			{ID: "u1", SessionID: "qual-session", Role: "user", Content: "Let's document that and create ticket NAN-128.", CreatedAt: "2026-09-30 00:00:01"},
			{ID: "a1", SessionID: "qual-session", Role: "assistant", Content: `{"v":1,"text":"Done.","tool_calls":[{"name":"torque_task_create"}],"envelopes":[{"type":"options"}]}`, CreatedAt: "2026-09-30 00:00:02"},
		} {
			if err := st.CreateMessage(ctx, &msg); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := st.DB.Exec(`UPDATE messages SET created_at=CASE id WHEN 'u1' THEN '2026-09-30T00:00:01Z' ELSE '2026-09-30T00:00:02Z' END WHERE session_id='qual-session'`); err != nil {
			t.Fatal(err)
		}
		if err := st.RecordUsage(ctx, "qual-session", "a1", "test-model", 11, 7, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	state, err := (&StateCollector{Store: ns, Window: 2}).Collect(ctx, "qual-session", "qual-agent", "advisor")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Messages) != 1 || len(state.Messages[0].ToolNames) != 1 || len(state.UserMessages) != 1 {
		t.Fatal("collector fixture failed")
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	nh, lh := &fakeReflexPluginHooks{}, &fakeReflexPluginHooks{}
	ne := NewEngine(ns, logger)
	ne.SetPluginHooks(nh)
	source := qualificationSource{t, ls}
	le, err := shared.New(source, source, shared.WithTrace(ls), shared.WithLogger(logger), shared.WithClock(func() time.Time { return qualificationNow }), shared.WithFilters(qualificationFilters{lh}))
	if err != nil {
		t.Fatal(err)
	}
	na, err := ne.referenceEvaluateState(ctx, "qual-agent", "advisor", state)
	if err != nil {
		t.Fatal(err)
	}
	lstate := qualificationState(t, state)
	la, err := le.Run(ctx, shared.RunInput{AgentID: "qual-agent", AgentClass: "advisor", State: &lstate, ExcludeKinds: []string{"dispatch_to_agent", "resume_loop_run"}})
	if err != nil {
		t.Fatal(err)
	}
	n, l := qualificationPersisted(t, ns, na.Actions, nil, nil), qualificationPersisted(t, ls, la.Applied.Actions, nil, nil)
	ph := &fakeReflexPluginHooks{}
	pe := NewEngine(ps, logger)
	pe.SetPluginHooks(ph)
	pa, err := pe.EvaluateState(ctx, "qual-agent", "advisor", state)
	if err != nil {
		t.Fatal(err)
	}
	production := qualificationPersisted(t, ps, pa.Actions, nil, nil)
	if qualificationJSON(t, n) != qualificationJSON(t, production) || *nh != *ph {
		t.Fatalf("production collected input=%s reference=%s production=%s filters=%+v/%+v", qualificationJSON(t, state), qualificationJSON(t, n), qualificationJSON(t, production), nh, ph)
	}

	if qualificationJSON(t, n) != qualificationJSON(t, l) {
		t.Fatalf("input=%s nanite=%s lib=%s", qualificationJSON(t, state), qualificationJSON(t, n), qualificationJSON(t, l))
	}
	if n.Counts[r.ID] != 1 || n.Counts[dispatch.ID] != 0 || n.Counts[resume.ID] != 0 || len(n.Traces) != 1 {
		t.Fatal("generic-pass result changed")
	}
	if *nh != *lh || nh.stateFilters != 1 || nh.actionFilters != 1 || nh.fired != 1 || nh.staged != 1 {
		t.Fatalf("filter/observer counts nanite=%+v lib=%+v", nh, lh)
	}
	t.Logf("equivalent collected input=%s nanite=%s lib=%s", qualificationJSON(t, state), qualificationJSON(t, n), qualificationJSON(t, l))
}

func TestGoReflexesQualification_PluginCandidateEligibility(t *testing.T) {
	// Exercise all three real #425 candidate queries, including revocation,
	// paused rows and opt-out. Catalog retention is independent of execution.
	for _, scope := range []string{"agent", "workflow", "loop"} {
		t.Run(scope, func(t *testing.T) {
			core := qualificationReflex("core", "inject_reminder", 10)
			plugin := qualificationReflex("plugin", "inject_reminder", 20)
			plugin.CreatedBy, plugin.ProvenanceTier, plugin.OptOutAllowed = "plugin:qualification", "plugin", true
			if scope == "workflow" {
				core.WorkflowRunID, plugin.WorkflowRunID = "qual-run", "qual-run"
			}
			if scope == "loop" {
				core.ActionKind, plugin.ActionKind = "resume_loop_run", "resume_loop_run"
				core.ActionSpec, plugin.ActionSpec = `{"loop_run_id":"qual-loop"}`, `{"loop_run_id":"qual-loop"}`
			}
			st := qualificationStore(t, []store.AgentReflex{core, plugin})
			ctx := context.Background()
			for _, phase := range []struct {
				name                   string
				active, optout, paused bool
				want                   int
			}{
				{"inactive", false, false, false, 1}, {"active", true, false, false, 2}, {"opted-out", true, true, false, 1}, {"paused", true, false, true, 1}, {"revoked", false, false, false, 1},
			} {
				t.Run(phase.name, func(t *testing.T) {
					if phase.active {
						st.SetPluginReflexGate(func(r store.AgentReflex) bool { return r.ID == plugin.ID })
					} else {
						st.SetPluginReflexGate(nil)
					}
					if phase.optout {
						if err := st.SetAgentReflexOptOut(ctx, "qual-agent", plugin.ID); err != nil {
							t.Fatal(err)
						}
					} else {
						if err := st.ClearAgentReflexOptOut(ctx, "qual-agent", plugin.ID); err != nil {
							t.Fatal(err)
						}
					}
					status := store.ReflexStatusActive
					if phase.paused {
						status = store.ReflexStatusPaused
					}
					if _, err := st.DB.Exec(`UPDATE agent_reflexes SET status=? WHERE id=?`, status, plugin.ID); err != nil {
						t.Fatal(err)
					}
					rows := qualificationCandidates(t, st, scope)
					// Loop scopes are lifecycle-bound, not agent opt-out-bound (the query
					// deliberately has no agent argument). Preserve that host distinction.
					want := phase.want
					if scope == "loop" && phase.optout {
						want = 2
					}
					if len(rows) != want {
						t.Fatalf("candidate count=%d want=%d", len(rows), want)
					}
					state := State{SessionID: "qual-session", Events: []EventSignal{{EventType: "probe"}}}
					nx := testExecutor()
					lx := shared.NewExecutor(slog.Default())
					lx.Stage("resume_loop_run")
					na, _, err := Resolve(ctx, rows, state, nx, noCooldown, kindLookupFixed("all_applicable"))
					if err != nil {
						t.Fatal(err)
					}
					la, _, err := shared.Resolve(ctx, qualificationCopy[[]shared.Reflex](t, rows), qualificationState(t, state), lx, nil, func(_ context.Context, k string) (*shared.ActionKind, error) {
						return &shared.ActionKind{Name: k, CombiningAlgorithm: "all_applicable"}, nil
					})
					if err != nil {
						t.Fatal(err)
					}
					if qualificationJSON(t, na.Actions) != qualificationJSON(t, la.Actions) || len(na.Actions) != want {
						t.Fatal("source eligibility mismatch")
					}
					catalog, err := st.ListAllAgentReflexes(ctx, "qual-agent")
					if err != nil || len(catalog) != 2 {
						t.Fatalf("catalog retention: %v %+v", err, catalog)
					}
				})
			}
		})
	}
}

// Observer payloads must retain Nanite's concrete action type as well as their
// JSON shape and Fired-before-Staged ordering across the library seam.
type qualificationObservers struct {
	fakeReflexPluginHooks
	t       *testing.T
	records []string
}

func (h *qualificationObservers) record(phase, sessionID string, data map[string]any) {
	h.t.Helper()
	if _, ok := data["action"].(AppliedAction); !ok {
		h.t.Fatalf("%s observer action type = %T", phase, data["action"])
	}
	h.records = append(h.records, phase+":"+sessionID+":"+qualificationJSON(h.t, data))
}
func (h *qualificationObservers) EmitReflexFired(sessionID string, data map[string]any) {
	h.record("fired", sessionID, data)
}
func (h *qualificationObservers) EmitReflexActionStaged(sessionID string, data map[string]any) {
	h.record("staged", sessionID, data)
}

func TestGoReflexesQualification_HostEffectsAndObservers(t *testing.T) {
	for _, kind := range []string{"halt_session", "add_schedule", "send_message"} {
		t.Run(kind, func(t *testing.T) {
			row := qualificationReflex("effect", kind, 10)
			row.ActionSpec = `{"body":"before"}`
			state := State{SessionID: "qual-session", AgentID: "qual-agent", AgentClass: "advisor", Events: []EventSignal{{EventType: "probe"}}}
			results := []qualificationResult{}
			observations := [][]string{}
			for _, useReference := range []bool{true, false} {
				st := qualificationStore(t, []store.AgentReflex{row})
				ctx := context.Background()
				effects := []string{}
				executor := &Executor{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
				executor.Halt = func(_ context.Context, session, reason string, evidence map[string]interface{}) error {
					effects = append(effects, session+":"+reason+":"+qualificationJSON(t, evidence))
					return errors.New("halt fixture failure")
				}
				mutate := func(_ context.Context, agentID string, spec map[string]interface{}) error {
					effects = append(effects, agentID+":"+qualificationJSON(t, spec))
					spec["body"] = "callback-edited"
					return errors.New("effect fixture failure")
				}
				executor.Schedule, executor.SendMessage = mutate, mutate
				resolve := Resolve
				emit := EmitFirings
				if useReference {
					resolve = referenceResolve
					emit = referenceEmitFirings
				}
				rows := qualificationCandidates(t, st, "")
				applied, outcomes, err := resolve(ctx, rows, state, executor, noCooldown, st.GetReflexActionKind)
				if err != nil {
					t.Fatal(err)
				}
				hooks := &qualificationObservers{t: t}
				emit(ctx, st, hooks, applied, outcomes, state, FiringContext{AgentID: state.AgentID, AgentClass: state.AgentClass, ExtraMetadata: map[string]any{"matched_input_excerpt": "audit"}}, executor.Logger)
				results = append(results, qualificationPersisted(t, st, applied.Actions, outcomes, effects))
				observations = append(observations, hooks.records)
			}
			if qualificationJSON(t, results[0]) != qualificationJSON(t, results[1]) || !reflect.DeepEqual(observations[0], observations[1]) {
				t.Fatalf("input=%s state=%s reference=%s production=%s observers=%v", qualificationJSON(t, row), qualificationJSON(t, state), qualificationJSON(t, results[0]), qualificationJSON(t, results[1]), observations)
			}
			if len(observations[1]) != 2 || results[1].Counts[row.ID] != 1 {
				t.Fatal("failed effect must still emit Fired/Staged and count the firing")
			}
			if kind != "halt_session" && qualificationCopy[[]AppliedAction](t, results[1].Actions)[0].Spec["body"] != "callback-edited" {
				t.Fatal("callback spec edits lost")
			}
		})
	}
}

// attr is a library extension outside Nanite's contract. These authored
// predicates were rejected before adoption and must never gain a live effect.
func TestGoReflexesQualification_UnsupportedAttr(t *testing.T) {
	specs := []struct{ name, spec string }{
		{"top-level", `{"kind":"attr","key":"region","op":"!=","value":"private"}`},
		{"and", `{"kind":"AND","clauses":[{"kind":"scope_tier","value":"open"},{"kind":"attr","key":"region","op":"!=","value":"private"}]}`},
		{"or", `{"kind":"OR","clauses":[{"kind":"attr","key":"region","op":"!=","value":"private"},{"kind":"scope_tier","value":"closed"}]}`},
		{"deep", `{"kind":"AND","clauses":[{"kind":"scope_tier","value":"open"},{"kind":"OR","clauses":[{"kind":"AND","clauses":[{"kind":"attr","key":"region","op":"!=","value":"private"}]}]}]}`},
	}
	for _, c := range specs {
		t.Run(c.name, func(t *testing.T) {
			state := State{SessionID: "qual-session", AgentID: "qual-agent", AgentClass: "advisor", ScopeTier: "open"}
			n, ne := referenceEvaluateTrigger("predicate", c.spec, state)
			p, pe := EvaluateTrigger("predicate", c.spec, state)
			l, le := shared.EvaluateTrigger("predicate", c.spec, qualificationState(t, state))
			const wantError = `unknown predicate kind "attr"`
			if n || p || ne == nil || pe == nil || ne.Error() != wantError || pe.Error() != wantError || !l || le != nil {
				t.Fatalf("input=%s spec=%s reference=%v/%v production=%v/%v library=%v/%v", qualificationJSON(t, state), c.spec, n, ne, p, pe, l, le)
			}
			bad := qualificationReflex("unsupported", "dispatch_to_agent", 99)
			bad.TriggerKind, bad.TriggerSpec, bad.ActionSpec = "predicate", c.spec, `{"agent_slug":"planner"}`
			good := qualificationReflex("supported", "dispatch_to_agent", 10)
			good.TriggerKind, good.TriggerSpec, good.ActionSpec = "predicate", `{"kind":"scope_tier","value":"open"}`, `{"agent_slug":"planner"}`
			results := []qualificationResult{}
			for _, useReference := range []bool{true, false} {
				st := qualificationStore(t, []store.AgentReflex{bad, good})
				ctx := context.Background()
				resolve, emit := Resolve, EmitFirings
				if useReference {
					resolve, emit = referenceResolve, referenceEmitFirings
				}
				applied, outcomes, err := resolve(ctx, qualificationCandidates(t, st, ""), state, testExecutor(), noCooldown, st.GetReflexActionKind)
				if err != nil {
					t.Fatal(err)
				}
				if len(outcomes) != 2 || outcomes[0].ReflexID != bad.ID || outcomes[0].TriggerError != wantError || outcomes[0].TriggerFired || outcomes[0].Selected {
					t.Fatalf("rejection outcome=%+v", outcomes)
				}
				emit(ctx, st, nil, applied, outcomes, state, FiringContext{AgentID: state.AgentID, AgentClass: state.AgentClass}, slog.Default())
				results = append(results, qualificationPersisted(t, st, applied.Actions, outcomes, nil))
			}
			if qualificationJSON(t, results[0]) != qualificationJSON(t, results[1]) {
				t.Fatalf("input=%s reference=%s production=%s", c.spec, qualificationJSON(t, results[0]), qualificationJSON(t, results[1]))
			}
			if results[1].Counts[bad.ID] != 0 || results[1].Counts[good.ID] != 1 {
				t.Fatal("unsupported candidate fired or suppressed supported candidate")
			}
			t.Logf("host rejection preserved input=%s reference=%s production=%s; direct library accepts attr=true", c.spec, qualificationJSON(t, results[0]), qualificationJSON(t, results[1]))
		})
	}
}

func TestEvaluateTrigger_RejectsAttrInShortCircuitedBranches(t *testing.T) {
	// Vocabulary rejection happens before delegation, even when another clause
	// would otherwise short-circuit. It must not depend on current signal values.
	for _, spec := range []string{
		`{"kind":"OR","clauses":[{"kind":"scope_tier","value":"open"},{"kind":"attr","key":"region","op":"!=","value":"private"}]}`,
		`{"kind":"AND","clauses":[{"kind":"scope_tier","value":"closed"},{"kind":"attr","key":"region","op":"!=","value":"private"}]}`,
	} {
		fired, err := EvaluateTrigger("predicate", spec, State{ScopeTier: "open"})
		if fired || err == nil || err.Error() != `unknown predicate kind "attr"` {
			t.Fatalf("spec=%s fired=%v err=%v", spec, fired, err)
		}
	}
}
