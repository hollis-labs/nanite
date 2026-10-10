package service

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestRetiredDispatchFactPortsRefuseHistoricalCandidatesConsistently(t *testing.T) {
	st, actor, session := newRetiredDispatchFixture(t, "retired-fact-ports")
	insertRetainedDispatchRule(t, st, store.AgentReflex{Name: "retained-parity", Priority: 100})
	before := retainedDispatchSnapshot(t, st)
	service := NewReflexService(st)
	rows, err := service.ListForAgent(t.Context(), actor.ID, "advisor")
	if rows != nil || !errors.Is(err, store.ErrImmutableAgentProfile) {
		t.Fatalf("service candidate read=%+v err=%v", rows, err)
	}
	transport := NewSelfToolsTransport(st)
	rows, err = transport.Writes.Dispatch.ListAgentReflexesForAgent(t.Context(), actor.ID, "advisor")
	if rows != nil || !errors.Is(err, store.ErrImmutableAgentProfile) {
		t.Fatalf("self-tool candidate read=%+v err=%v", rows, err)
	}
	rows, err = transport.Writes.Dispatch.ListAgentReflexesForWorkflowRun(t.Context(), "retained-run", actor.ID, "advisor")
	if rows != nil || !errors.Is(err, store.ErrImmutableAgentProfile) {
		t.Fatalf("self-tool scoped candidate read=%+v err=%v", rows, err)
	}
	if after := retainedDispatchSnapshot(t, st); !reflect.DeepEqual(before, after) {
		t.Fatal("candidate facts changed historical rules/authority")
	}
	assertRetainedDispatchInert(t, st, actor.ID, session.ID, "please research this", classifiedRetiredDispatchTurn(t, session.ID, "please research this"))
}

func TestRetiredDispatchAuthoringRefusesWithoutChangingHistoricalRules(t *testing.T) {
	st, actor, _ := newRetiredDispatchFixture(t, "retired-authoring")
	const id = "retained-authoring"
	retained := store.AgentReflex{ID: id, AgentID: "historical-retired-authoring", Name: "retained-authoring", ClassTag: "advisor", TriggerKind: store.ReflexTriggerPredicate, TriggerSpec: `{"kind":"user_regex_window","window":1,"pattern":".*"}`, ActionKind: store.ReflexActionDispatchToAgent, ActionSpec: `{"agent_slug":"planner","confidence":0.9,"reason":"private retained routing"}`, Status: store.ReflexStatusActive, FiredCount: 19}
	insertRetainedDispatchRule(t, st, retained)
	before := retainedDispatchSnapshot(t, st)
	changed := retained
	changed.Name = "replacement"
	changed.FiredCount = 0
	newID, err := st.InsertAgentReflex(t.Context(), changed)
	if newID != "" || !errors.Is(err, store.ErrImmutableAgentProfile) {
		t.Fatalf("insert=%q err=%v", newID, err)
	}
	inserted, err := st.InsertAgentReflexIfAbsent(t.Context(), changed)
	if inserted || !errors.Is(err, store.ErrImmutableAgentProfile) {
		t.Fatalf("retry insert=%v err=%v", inserted, err)
	}
	for _, err = range []error{st.UpdateAgentReflex(t.Context(), changed), st.BumpAgentReflexFired(t.Context(), id, time.Now()), st.DeleteAgentReflex(t.Context(), id)} {
		if !errors.Is(err, store.ErrImmutableAgentProfile) {
			t.Fatalf("mutable rule operation: %v", err)
		}
	}
	service := NewReflexService(st)
	result, err := service.Create(t.Context(), changed)
	if result != nil || !errors.Is(err, store.ErrImmutableAgentProfile) {
		t.Fatalf("service create=%+v err=%v", result, err)
	}
	result, err = service.Patch(t.Context(), actor.ID, id, ReflexPatch{Name: &changed.Name})
	if result != nil || !errors.Is(err, store.ErrImmutableAgentProfile) {
		t.Fatalf("service patch=%+v err=%v", result, err)
	}
	if err = service.DeleteOwned(t.Context(), actor.ID, id); !errors.Is(err, store.ErrImmutableAgentProfile) {
		t.Fatalf("service delete=%v", err)
	}
	if after := retainedDispatchSnapshot(t, st); !reflect.DeepEqual(before, after) {
		t.Fatalf("retired authoring changed retained rows: before=%v after=%v", before, after)
	}
}
