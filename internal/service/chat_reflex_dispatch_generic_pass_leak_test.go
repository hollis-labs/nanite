package service

import (
	"errors"
	"reflect"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
	ctxpkg "github.com/hollis-labs/substrate/agent/contextwindow"
)

func newRetiredDispatchFixture(t *testing.T, slug string) (*store.Store, *store.AgentProfile, *store.Session) {
	t.Helper()
	st := newDurableAgentServiceTestStore(t)
	prior := &store.AgentProfile{Name: "Prior authored actor", Slug: slug, SystemPrompt: "Private authored prompt"}
	if err := persistTestActor(t.Context(), st, prior); err != nil {
		t.Fatal(err)
	}
	historical := &store.AgentProfile{ID: "historical-" + slug, Name: "Retained namesake", Slug: slug, SystemPrompt: "Private retained prompt"}
	if err := storetest.HistoricalProfile(t.Context(), st, historical); err != nil {
		t.Fatal(err)
	}
	session := &store.Session{ID: "session-" + slug, Title: "Private prior session"}
	if err := st.CreateSession(t.Context(), session); err != nil {
		t.Fatal(err)
	}
	return st, prior, session
}

// Raw old-table rows model retained audit history, never authoring or grants.
func insertRetainedDispatchRule(t *testing.T, st *store.Store, row store.AgentReflex) {
	t.Helper()
	if row.ID == "" {
		row.ID = "retained-" + row.Name
	}
	if row.ClassTag == "" {
		row.ClassTag = "advisor"
	}
	if row.TriggerKind == "" {
		row.TriggerKind = store.ReflexTriggerPredicate
	}
	if row.TriggerSpec == "" {
		row.TriggerSpec = `{"kind":"user_regex_window","window":1,"pattern":".*"}`
	}
	if row.ActionKind == "" {
		row.ActionKind = store.ReflexActionDispatchToAgent
	}
	if row.ActionSpec == "" {
		row.ActionSpec = `{"agent_slug":"retained-target","confidence":0.99,"reason":"Private retained rule"}`
	}
	if row.Status == "" {
		row.Status = store.ReflexStatusActive
	}
	var agentID, runID any
	if row.AgentID != "" {
		agentID = row.AgentID
	}
	if row.WorkflowRunID != "" {
		runID = row.WorkflowRunID
	}
	_, err := st.DB.ExecContext(t.Context(), `INSERT INTO agent_reflexes(id,agent_id,class_tag,name,trigger_kind,trigger_spec,action_kind,action_spec,status,priority,fired_count,last_fired_at,created_at,created_by,opt_out_allowed,provenance_tier,recurrence_override_seconds,workflow_run_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,'2026-09-01','historical-operator',1,'operator',?,?)`, row.ID, agentID, row.ClassTag, row.Name, row.TriggerKind, row.TriggerSpec, row.ActionKind, row.ActionSpec, row.Status, row.Priority, row.FiredCount, row.LastFiredAt, row.RecurrenceOverrideSeconds, runID)
	if err != nil {
		t.Fatal(err)
	}
}

func retainedDispatchSnapshot(t *testing.T, st *store.Store) map[string][][]any {
	t.Helper()
	out := durableAuthoritySnapshot(t, st)
	for _, query := range []string{
		`SELECT * FROM agent_reflexes ORDER BY id`, `SELECT * FROM agent_reflex_opt_outs ORDER BY agent_id,reflex_id`,
		`SELECT * FROM pending_reflexes ORDER BY id`, `SELECT * FROM team_run_members ORDER BY id`, `SELECT * FROM actor_team_run_members ORDER BY id`,
		`SELECT * FROM agent_tools ORDER BY agent_id,tool_id`, `SELECT * FROM actor_granted_tools ORDER BY agent_id,tool_id`,
		`SELECT * FROM event_log WHERE category='reflex' ORDER BY id`,
	} {
		out[query] = immutableConfigSnapshot(t, st, query)
	}
	return out
}

func assertRetainedDispatchInert(t *testing.T, st *store.Store, agentID, sessionID, message string, ls *loopState) {
	t.Helper()
	tools := &recordingReflexDispatchToolService{}
	svc := &chatServiceImpl{store: st, reflexEngine: reflexes.NewEngine(st, nil), tools: tools}
	before := retainedDispatchSnapshot(t, st)
	ch := make(chan chat.StreamEvent, 8)
	outcome := svc.attemptReflexDispatch(t.Context(), sessionID, "private-turn", message, agentID, "advisor", ls, ch)
	close(ch)
	if outcome != (reflexDispatchOutcome{}) {
		t.Fatalf("historical rule steered turn: %+v", outcome)
	}
	if len(tools.calls) != 0 {
		t.Fatalf("historical rule invoked tools: %+v", tools.calls)
	}
	for event := range ch {
		t.Fatalf("historical dispatch emitted stream event: %+v", event)
	}
	if after := retainedDispatchSnapshot(t, st); !reflect.DeepEqual(before, after) {
		t.Fatalf("historical dispatch changed retained rows/authority: before=%v after=%v", before, after)
	}
}

func classifiedRetiredDispatchTurn(t *testing.T, sessionID, message string) *loopState {
	t.Helper()
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	classifyAndAttach(ls, sessionID, message, nil)
	tier, pattern := ls.Classification()
	if !tier.IsValid() || !pattern.IsValid() {
		t.Fatalf("invalid fixture classification: %s/%s", tier, pattern)
	}
	return ls
}

func TestRetiredGenericAndDedicatedPassesPreservePinnedContext(t *testing.T) {
	st, actor, session := newRetiredDispatchFixture(t, "generic-retired")
	insertRetainedDispatchRule(t, st, store.AgentReflex{Name: "retained-dispatch", FiredCount: 7})
	insertRetainedDispatchRule(t, st, store.AgentReflex{Name: "retained-reminder", ActionKind: store.ReflexActionInjectReminder, ActionSpec: `{"body":"must not replace pinned context"}`})
	if err := st.CreateMessage(t.Context(), &store.Message{SessionID: session.ID, Role: "user", Content: "please research the incident thoroughly"}); err != nil {
		t.Fatal(err)
	}
	window := ctxpkg.NewContextWindow(200000, ctxpkg.DefaultEstimator{})
	window.SetContent(ctxpkg.SlotUserContext, "Existing private context")
	slots := &SlotAssemblyResult{Window: window}
	svc := &chatServiceImpl{store: st, reflexEngine: reflexes.NewEngine(st, nil)}
	before := retainedDispatchSnapshot(t, st)
	actions, err := svc.evaluateAndInjectReflexes(t.Context(), session, actor, slots)
	if err != nil || len(actions) != 0 {
		t.Fatalf("unpinned historical candidates reached pinned pass: actions=%+v err=%v", actions, err)
	}
	if got := window.Slot(ctxpkg.SlotUserContext).Content; got != "Existing private context" {
		t.Fatalf("historical reminder changed context: %q", got)
	}
	legacy, err := st.GetHistoricalAgentProfile(t.Context(), "historical-generic-retired")
	if err != nil {
		t.Fatal(err)
	}
	actions, err = svc.evaluateAndInjectReflexes(t.Context(), session, legacy, slots)
	if actions != nil || !errors.Is(err, store.ErrImmutableAgentProfile) {
		t.Fatalf("historical projection evaluated: actions=%+v err=%v", actions, err)
	}
	if after := retainedDispatchSnapshot(t, st); !reflect.DeepEqual(before, after) {
		t.Fatal("generic pass changed retained history/authority")
	}
	assertRetainedDispatchInert(t, st, actor.ID, session.ID, "please research the incident thoroughly", classifiedRetiredDispatchTurn(t, session.ID, "please research the incident thoroughly"))
}

func TestRetiredDispatchAndHaltRulesAllowNativeTurnContinuation(t *testing.T) {
	f := newCharacterizationFixture(t, []characterizationProviderStep{{events: doneEvents("native answer survives")}})
	f.svc.reflexEngine = reflexes.NewEngine(f.st, nil)
	insertRetainedDispatchRule(t, f.st, store.AgentReflex{Name: "retained-native-dispatch", Priority: 10000, FiredCount: 3})
	insertRetainedDispatchRule(t, f.st, store.AgentReflex{Name: "retained-native-halt", ActionKind: store.ReflexActionHaltSession, ActionSpec: `{"reason":"must not halt native pin"}`, Priority: 10001})
	insertRetainedDispatchRule(t, f.st, store.AgentReflex{Name: "retained-native-reminder", ActionKind: store.ReflexActionInjectReminder, ActionSpec: `{"body":"PRIVATE OLD INJECTION"}`})
	before := retainedDispatchSnapshot(t, f.st)
	f.userContent = "please research the incident thoroughly"
	events := f.run(t, "assistant-retired-dispatch-continuation")
	if f.provider.callCount() != 1 {
		t.Fatalf("native provider calls=%d want one completed turn", f.provider.callCount())
	}
	delta := findEvent(events, "delta")
	if delta == nil || delta.Content != "native answer survives" || delta.Phase != chat.PhaseFinal || findEvent(events, "stream_end") == nil || findEvent(events, "error") != nil {
		t.Fatalf("native turn failed to continue: %+v", events)
	}
	if len(f.tools.calls()) != 0 || findEvent(events, "plugin_envelope") != nil {
		t.Fatalf("historical dispatch leaked tool/envelope: tools=%v events=%v", f.tools.calls(), eventTypes(events))
	}
	saved, err := f.st.GetSession(t.Context(), f.session)
	if err != nil {
		t.Fatal(err)
	}
	if saved.HaltedAt != nil || saved.HaltedReason != nil {
		t.Fatalf("historical halt changed native session: %+v", saved)
	}
	if after := retainedDispatchSnapshot(t, f.st); !reflect.DeepEqual(before, after) {
		t.Fatal("native continuation changed historical reflex/authority rows")
	}
}
