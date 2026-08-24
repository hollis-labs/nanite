// TASKS/phase-4/09-fix-dispatch-to-agent-generic-pass-leak.md — the
// "Done means" real-session verification: a message matching
// dispatch_to_agent_researcher_mention's phrase list, run through the
// SAME two call sites generateResponse invokes on the same turn (in the
// same order: evaluateAndInjectReflexes at chat_generate.go:523, then
// attemptReflexDispatch at chat_generate.go:670), against a real
// *store.Store with the real seeded reflex catalog (reflexes.
// SeedBaseReflexes — not a hand-authored fixture) and a real persisted
// user message (StateCollector.Collect's recentMessagesByRole reads
// exactly this row, mirroring chat.go's CreateMessage(userMsg) call that
// runs before generateResponse in production).
//
// Exercising a genuinely live HTTP/LLM session isn't feasible in this
// worker environment (no live provider credentials — the same
// constraint chat_reflex_dispatch_integration_test.go's header comment
// documents for the dedicated-path-only coverage it added). This test
// instead calls the real, unmodified production functions directly, in
// production's own call order, against a real on-disk SQLite store, and
// asserts on the real event_log/fired_count rows those functions write —
// the closest equivalent available here to the "real session" the task
// asks for.
package service

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/chat"
	ctxpkg "github.com/hollis-labs/nanite/internal/context"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

// TestGenericReflexPass_DoesNotDuplicateDispatchToAgentFiring is the
// end-to-end regression check for the bug: on the SAME turn, both
// evaluateAndInjectReflexes (the generic per-turn pass) and
// attemptReflexDispatch (the dedicated dispatch pass) evaluate the same
// dispatch_to_agent_researcher_mention reflex against the same
// "please research the incident thoroughly" message. Only the dedicated
// pass may produce a real effect.
func TestGenericReflexPass_DoesNotDuplicateDispatchToAgentFiring(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "reflex-generic-pass-leak.db")
	st, err := storetest.New(t, ctx, dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close(context.

			// Real seed data — the same call container.go makes at boot,
			// including dispatch_to_agent_researcher_mention (seeds.go).
			Background())
	})

	if _, err := reflexes.SeedBaseReflexes(ctx, st, nil); err != nil {
		t.Fatalf("SeedBaseReflexes: %v", err)
	}

	const sessionID = "sess-generic-pass-leak"
	const agentID = "agent-generic-pass-leak"
	const userMsgID = "u-generic-pass-leak"
	// Deliberately contains "research" (researcher-mention's phrase
	// list) and none of the other 5 migrated dispatch_to_agent phrase
	// catalogs' phrases, so exactly one dispatch_to_agent reflex is a
	// candidate for both passes below.
	const userContent = "please research the incident thoroughly"

	agent := &store.AgentProfile{
		ID:           agentID,
		Name:         "Generic Pass Leak Probe",
		Slug:         "generic-pass-leak-probe",
		Class:        "advisor",
		SystemPrompt: "test",
		Source:       "test",
	}
	if err := st.CreateAgent(context.Background(), agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	session := &store.Session{ID: sessionID, Title: "generic pass leak probe"}
	if err := st.CreateSession(context.Background(), session); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	// Persisted BEFORE either pass runs, mirroring chat.go's
	// CreateMessage(userMsg) call, which runs before generateResponse —
	// StateCollector.Collect (the generic pass's state source) only ever
	// sees already-committed rows.
	if err := st.CreateMessage(context.Background(), &store.Message{
		ID:        userMsgID,
		SessionID: sessionID,
		Role:      "user",
		Content:   userContent,
	}); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	tools := &recordingReflexDispatchToolService{}
	s := &chatServiceImpl{
		store:        st,
		reflexEngine: reflexes.NewEngine(st, nil),
		tools:        tools,
	}

	dispatchReflex, err := findAgentReflexByName(ctx, st, "dispatch_to_agent_researcher_mention")
	if err != nil {
		t.Fatalf("lookup dispatch_to_agent_researcher_mention: %v", err)
	}
	if dispatchReflex.FiredCount != 0 {
		t.Fatalf("setup: dispatch_to_agent_researcher_mention FiredCount = %d, want 0 before either pass runs", dispatchReflex.FiredCount)
	}

	// --- Pass 1: the GENERIC per-turn pass (chat_generate.go:523's own
	// call site). This is the pass under test — before the fix, it would
	// treat Executor.Apply's dispatch_to_agent no-op as a real fire. ---
	cw := ctxpkg.NewContextWindow(200_000, ctxpkg.DefaultEstimator{})
	slotResult := &SlotAssemblyResult{Window: cw}
	genericActions := s.evaluateAndInjectReflexes(ctx, session, agent, slotResult)

	var genericSawDispatchReflex bool
	for _, a := range genericActions {
		if a.ReflexID == dispatchReflex.ID {
			genericSawDispatchReflex = true
		}
	}
	if genericSawDispatchReflex {
		t.Errorf("evaluateAndInjectReflexes (generic pass) surfaced an action for dispatch_to_agent_researcher_mention — it must be invisible to this pass")
	}

	afterGeneric, err := st.GetAgentReflex(ctx, dispatchReflex.ID)
	if err != nil {
		t.Fatalf("GetAgentReflex after generic pass: %v", err)
	}
	if afterGeneric.FiredCount != 0 {
		t.Errorf("FiredCount after generic pass = %d, want 0 — the generic pass must not bump fired_count for this reflex", afterGeneric.FiredCount)
	}

	// TASKS/reflex-taxonomy/06-unified-reflex-telemetry.md: the generic
	// pass's own event_log write no longer uses a fixed "reflex_action"
	// event_type literal (it's now the fired action_kind, per
	// reflexes.EmitFirings) — checking by reflex name (Detail) rather than
	// a specific event_type string is the shape-agnostic way to assert "no
	// row for this reflex was written," regardless of which action_kind
	// string a future kind might use.
	genericReflexActionEvents := countEventLogRowsForReflex(t, st, "dispatch_to_agent_researcher_mention")
	if genericReflexActionEvents != 0 {
		t.Errorf("event_log has %d row(s) naming dispatch_to_agent_researcher_mention after the generic pass, want 0", genericReflexActionEvents)
	}

	// --- Pass 2: the DEDICATED dispatch pass (chat_generate.go:670's own
	// call site) — the ONLY pass allowed to produce a real effect. ---
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	classifyAndAttach(ls, sessionID, userContent, nil)

	ch := make(chan chat.StreamEvent, 4)
	out := s.attemptReflexDispatch(ctx, sessionID, "turn-generic-pass-leak", userContent, agentID, "advisor", ls, ch)
	close(ch)

	if !out.Matched {
		t.Fatalf("attemptReflexDispatch: Matched = false, want true (dispatch_to_agent_researcher_mention should fire on the dedicated pass)")
	}
	if out.ReflexName != "dispatch_to_agent_researcher_mention" {
		t.Errorf("ReflexName = %q, want dispatch_to_agent_researcher_mention", out.ReflexName)
	}

	afterDedicated, err := st.GetAgentReflex(ctx, dispatchReflex.ID)
	if err != nil {
		t.Fatalf("GetAgentReflex after dedicated pass: %v", err)
	}
	if afterDedicated.FiredCount != 1 {
		t.Errorf("FiredCount after dedicated pass = %d, want exactly 1 (one real dispatch, no duplicate from the generic pass)", afterDedicated.FiredCount)
	}

	events, err := st.ListEvents(context.Background(), "", 50)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var dispatchEvents, otherReflexEventsForThisReflex int
	for _, e := range events {
		if e.SessionID != sessionID {
			continue
		}
		switch {
		case e.EventType == "dispatch_to_agent":
			dispatchEvents++
		case e.Detail == "dispatch_to_agent_researcher_mention":
			// Any OTHER event_type naming this same reflex would mean the
			// generic pass wrote a redundant row for it too — shape-agnostic
			// check, see the matching comment above.
			otherReflexEventsForThisReflex++
		}
	}
	if dispatchEvents != 1 {
		t.Errorf("event_log dispatch_to_agent rows = %d, want exactly 1 (the real dedicated-pass dispatch)", dispatchEvents)
	}
	if otherReflexEventsForThisReflex != 0 {
		t.Errorf("event_log has %d other row(s) naming dispatch_to_agent_researcher_mention, want 0 — the generic pass must not have written a redundant row for this turn", otherReflexEventsForThisReflex)
	}
}

// findAgentReflexByName is a small test helper: SeedBaseReflexes doesn't
// return per-row IDs, so tests that need to assert on a specific seeded
// row's fired_count/event_log footprint look it up via
// ListAgentReflexesForAgent (empty agentID — class-bound rows only,
// which is what every seed in seeds.go is).
func findAgentReflexByName(ctx context.Context, st *store.Store, name string) (*store.AgentReflex, error) {
	rows, err := st.ListAgentReflexesForAgent(ctx, "", "advisor")
	if err != nil {
		return nil, err
	}
	for i := range rows {
		if rows[i].Name == name {
			return &rows[i], nil
		}
	}
	return nil, store.ErrAgentReflexNotFound
}

// countEventLogRowsForReflex counts event_log rows whose Detail names
// reflexName, regardless of event_type — TASKS/reflex-taxonomy/
// 06-unified-reflex-telemetry.md made event_type per-action-kind (the
// fired reflex's own action_kind) rather than a fixed "reflex_action"
// literal, so a shape-agnostic count by reflex name is the stable check.
func countEventLogRowsForReflex(t *testing.T, st *store.Store, reflexName string) int {
	t.Helper()
	events, err := st.ListEvents(context.Background(), "", 100)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	count := 0
	for _, e := range events {
		if e.Detail == reflexName {
			count++
		}
	}
	return count
}
