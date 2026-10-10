package loop

import (
	"errors"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/store"
)

func TestTickResumeBridge_RetainedRuleRefusesWithoutFallback(t *testing.T) {
	for _, test := range []struct {
		name string
		spec string
		want bool
	}{
		{"trigger-false", `{"kind":"scope_tier","value":"some-tier-that-will-never-match"}`, false},
		{"trigger-true", `{"kind":"scope_tier"}`, true},
		{"no-rule", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			st, eng, exec, loopRunID := retainedWaitingLoop(t)
			if test.spec != "" {
				// The pure predicate remains usable. Its truth cannot grant
				// permission to resume through a retired mutable rule.
				got, err := reflexes.EvaluateTrigger(store.ReflexTriggerPredicate, test.spec, reflexes.State{})
				if err != nil || got != test.want {
					t.Fatalf("pure predicate=%v,%v want %v", got, err, test.want)
				}
				seedRetainedResumeRule(t, st, loopRunID, store.ReflexTriggerPredicate, test.spec)
			}
			before := retainedLoopResumeState(t, st)
			bridge := NewTickResumeBridge(eng, reflexes.NewEngine(st, nil))
			result, err := bridge.Resume(t.Context(), loopRunID)
			if !errors.Is(err, store.ErrImmutableAgentProfile) || result != (LoopResult{}) {
				t.Fatalf("tick resume=%+v,%v want retired-rule refusal", result, err)
			}
			if exec.calls != 0 {
				t.Fatalf("refused tick executed %d steps", exec.calls)
			}
			assertRetainedLoopResumeState(t, st, before)
			lr, err := st.GetLoopRun(t.Context(), loopRunID)
			if err != nil || lr.Status != store.LoopRunStatusWaitingOnEscalation || lr.CurrentIteration != 2 {
				t.Fatalf("retained waiting loop changed: %+v,%v", lr, err)
			}
		})
	}
}

func TestTickResumeBridge_MissingDependenciesAndRunControls(t *testing.T) {
	st, eng, _, _ := retainedWaitingLoop(t)
	reflexEngine := reflexes.NewEngine(st, nil)
	for _, bridge := range []*TickResumeBridge{nil, NewTickResumeBridge(nil, reflexEngine), NewTickResumeBridge(eng, nil)} {
		result, err := bridge.Resume(t.Context(), "missing")
		if err == nil || errors.Is(err, store.ErrImmutableAgentProfile) || result != (LoopResult{}) {
			t.Fatalf("missing dependency result=%+v err=%v", result, err)
		}
	}
	bridge := NewTickResumeBridge(eng, reflexEngine)
	result, err := bridge.Resume(t.Context(), "")
	if err == nil || errors.Is(err, store.ErrImmutableAgentProfile) || result != (LoopResult{}) {
		t.Fatalf("empty run result=%+v err=%v", result, err)
	}
	before := retainedLoopResumeState(t, st)
	result, err = bridge.Resume(t.Context(), "missing")
	if !errors.Is(err, store.ErrImmutableAgentProfile) || result != (LoopResult{}) {
		t.Fatalf("unknown run must refuse before lookup: %+v,%v", result, err)
	}
	assertRetainedLoopResumeState(t, st, before)
}
