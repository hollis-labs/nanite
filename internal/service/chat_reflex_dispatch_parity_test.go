package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/classify"
	"github.com/hollis-labs/nanite/internal/dispatch"
	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/selftools"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

type reflexDispatchParityResult struct {
	Matched    bool
	ReflexID   string
	ReflexName string
	AgentSlug  string
}

type reflexDispatchParitySpawner struct {
	calls []dispatch.SpawnRequest
}

func (s *reflexDispatchParitySpawner) Spawn(_ context.Context, req dispatch.SpawnRequest) (*dispatch.SpawnResult, error) {
	s.calls = append(s.calls, req)
	return &dispatch.SpawnResult{Summary: "parity dispatch complete"}, nil
}

// TestDispatchToAgentReflexInterpretationParity is the AD-19 sync guard for
// the two intentionally independent dispatch_to_agent evaluators. It drives
// the real upstream attemptReflexDispatch call site and the real downstream
// task_execute/selftools path against separate, identically seeded stores so
// each side's telemetry/fired_count side effects cannot influence the other.
func TestDispatchToAgentReflexInterpretationParity(t *testing.T) {
	const agentID = "agent-dispatch-parity"

	cases := []struct {
		name      string
		sessionID string
		message   string
		setup     func(t *testing.T, st *store.Store)
		want      reflexDispatchParityResult
	}{
		{
			name:      "user regex match ignores non-dispatch rows",
			sessionID: "sess-parity-user-regex",
			message:   "please route probe-global-token now",
			setup: func(t *testing.T, st *store.Store) {
				insertParityReflex(t, st, store.AgentReflex{
					ID:          "rfx-parity-non-dispatch",
					ClassTag:    "advisor",
					Name:        "parity_non_dispatch_high_priority",
					TriggerKind: store.ReflexTriggerPredicate,
					TriggerSpec: `{"kind":"user_regex_window","window":1,"pattern":"probe-global-token"}`,
					ActionKind:  store.ReflexActionInjectReminder,
					ActionSpec:  `{"body":"not a dispatch candidate"}`,
					Priority:    90,
				})
				insertParityReflex(t, st, store.AgentReflex{
					ID:          "rfx-parity-user-regex",
					ClassTag:    "advisor",
					Name:        "parity_user_regex",
					TriggerKind: store.ReflexTriggerPredicate,
					TriggerSpec: `{"kind":"user_regex_window","window":1,"pattern":"probe-global-token"}`,
					ActionKind:  store.ReflexActionDispatchToAgent,
					ActionSpec:  `{"agent_slug":"parity-global","confidence":0.81,"reason":"parity user regex"}`,
					Priority:    40,
				})
			},
			want: reflexDispatchParityResult{
				Matched:    true,
				ReflexID:   "rfx-parity-user-regex",
				ReflexName: "parity_user_regex",
				AgentSlug:  "parity-global",
			},
		},
		{
			name:      "scope tier and execution pattern match",
			sessionID: "sess-parity-scope-pattern",
			message:   "scaffold the parity routing module from a blank slate",
			setup: func(t *testing.T, st *store.Store) {
				insertParityReflex(t, st, store.AgentReflex{
					ID:          "rfx-parity-scope-pattern",
					ClassTag:    "advisor",
					Name:        "parity_scope_pattern",
					TriggerKind: store.ReflexTriggerPredicate,
					TriggerSpec: `{"kind":"AND","clauses":[{"kind":"scope_tier","op":"=","value":"open"},{"kind":"execution_pattern","op":"=","value":"subagent"}]}`,
					ActionKind:  store.ReflexActionDispatchToAgent,
					ActionSpec:  `{"agent_slug":"parity-open-subagent","confidence":0.82,"reason":"parity scope pattern"}`,
					Priority:    50,
				})
			},
			want: reflexDispatchParityResult{
				Matched:    true,
				ReflexID:   "rfx-parity-scope-pattern",
				ReflexName: "parity_scope_pattern",
				AgentSlug:  "parity-open-subagent",
			},
		},
		{
			name:      "first applicable priority winner",
			sessionID: "sess-parity-priority",
			message:   "probe-priority-token needs a route",
			setup: func(t *testing.T, st *store.Store) {
				insertParityReflex(t, st, store.AgentReflex{
					ID:          "rfx-parity-priority-winner",
					ClassTag:    "advisor",
					Name:        "parity_priority_winner",
					TriggerKind: store.ReflexTriggerPredicate,
					TriggerSpec: `{"kind":"user_regex_window","window":1,"pattern":"probe-priority-token"}`,
					ActionKind:  store.ReflexActionDispatchToAgent,
					ActionSpec:  `{"agent_slug":"parity-priority-winner","confidence":0.91,"reason":"parity priority winner"}`,
					Priority:    80,
				})
				insertParityReflex(t, st, store.AgentReflex{
					ID:          "rfx-parity-priority-loser",
					ClassTag:    "advisor",
					Name:        "parity_priority_loser",
					TriggerKind: store.ReflexTriggerPredicate,
					TriggerSpec: `{"kind":"user_regex_window","window":1,"pattern":"probe-priority-token"}`,
					ActionKind:  store.ReflexActionDispatchToAgent,
					ActionSpec:  `{"agent_slug":"parity-priority-loser","confidence":0.61,"reason":"parity priority loser"}`,
					Priority:    10,
				})
			},
			want: reflexDispatchParityResult{
				Matched:    true,
				ReflexID:   "rfx-parity-priority-winner",
				ReflexName: "parity_priority_winner",
				AgentSlug:  "parity-priority-winner",
			},
		},
		{
			name:      "run scoped row wins inside its run",
			sessionID: "sess-parity-run-a",
			message:   "probe-run-token needs a route",
			setup: func(t *testing.T, st *store.Store) {
				insertTestTeamRunMember(t, st, "sess-parity-run-a", "run-parity-a")
				insertParityReflex(t, st, store.AgentReflex{
					ID:          "rfx-parity-run-global",
					ClassTag:    "advisor",
					Name:        "parity_run_global",
					TriggerKind: store.ReflexTriggerPredicate,
					TriggerSpec: `{"kind":"user_regex_window","window":1,"pattern":"probe-run-token"}`,
					ActionKind:  store.ReflexActionDispatchToAgent,
					ActionSpec:  `{"agent_slug":"parity-run-global","confidence":0.62,"reason":"parity global run"}`,
					Priority:    20,
				})
				insertParityReflex(t, st, store.AgentReflex{
					ID:            "rfx-parity-run-scoped",
					ClassTag:      "advisor",
					Name:          "parity_run_scoped",
					TriggerKind:   store.ReflexTriggerPredicate,
					TriggerSpec:   `{"kind":"user_regex_window","window":1,"pattern":"probe-run-token"}`,
					ActionKind:    store.ReflexActionDispatchToAgent,
					ActionSpec:    `{"agent_slug":"parity-run-scoped","confidence":0.93,"reason":"parity run scoped"}`,
					Priority:      70,
					WorkflowRunID: "run-parity-a",
				})
			},
			want: reflexDispatchParityResult{
				Matched:    true,
				ReflexID:   "rfx-parity-run-scoped",
				ReflexName: "parity_run_scoped",
				AgentSlug:  "parity-run-scoped",
			},
		},
		{
			name:      "run scoped row invisible without matching run",
			sessionID: "sess-parity-no-run",
			message:   "probe-run-invisible-token needs a route",
			setup: func(t *testing.T, st *store.Store) {
				insertTestWorkflowRun(t, st, "run-parity-invisible")
				insertParityReflex(t, st, store.AgentReflex{
					ID:            "rfx-parity-run-invisible",
					ClassTag:      "advisor",
					Name:          "parity_run_invisible",
					TriggerKind:   store.ReflexTriggerPredicate,
					TriggerSpec:   `{"kind":"user_regex_window","window":1,"pattern":"probe-run-invisible-token"}`,
					ActionKind:    store.ReflexActionDispatchToAgent,
					ActionSpec:    `{"agent_slug":"parity-run-invisible","confidence":0.93,"reason":"parity run invisible"}`,
					Priority:      70,
					WorkflowRunID: "run-parity-invisible",
				})
			},
			want: reflexDispatchParityResult{},
		},
		{
			name:      "recurrence override suppresses pre-fired row",
			sessionID: "sess-parity-cooldown",
			message:   "probe-cooldown-parity-token needs a route",
			setup: func(t *testing.T, st *store.Store) {
				override := int64(3600)
				insertParityReflex(t, st, store.AgentReflex{
					ID:                        "rfx-parity-cooldown",
					ClassTag:                  "advisor",
					Name:                      "parity_cooldown",
					TriggerKind:               store.ReflexTriggerPredicate,
					TriggerSpec:               `{"kind":"user_regex_window","window":1,"pattern":"probe-cooldown-parity-token"}`,
					ActionKind:                store.ReflexActionDispatchToAgent,
					ActionSpec:                `{"agent_slug":"parity-cooldown","confidence":0.88,"reason":"parity cooldown"}`,
					Priority:                  60,
					FiredCount:                1,
					LastFiredAt:               time.Now().UTC().Format(time.RFC3339),
					RecurrenceOverrideSeconds: &override,
				})
			},
			want: reflexDispatchParityResult{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			serviceStore := newReflexDispatchParityStore(t, "service", agentID)
			tc.setup(t, serviceStore)
			selftoolsStore := newReflexDispatchParityStore(t, "selftools", agentID)
			tc.setup(t, selftoolsStore)

			serviceGot := runServiceReflexDispatchParity(t, serviceStore, tc.sessionID, agentID, tc.message)
			selftoolsGot := runSelftoolsReflexDispatchParity(t, selftoolsStore, tc.sessionID, agentID, tc.message)

			assertReflexDispatchParityResult(t, "attemptReflexDispatch", serviceGot, tc.want)
			assertReflexDispatchParityResult(t, "task_execute", selftoolsGot, tc.want)
			if serviceGot != selftoolsGot {
				t.Fatalf("dispatch_to_agent reflex interpretation diverged:\nattemptReflexDispatch = %+v\ntask_execute = %+v", serviceGot, selftoolsGot)
			}
		})
	}
}

func newReflexDispatchParityStore(t *testing.T, name, agentID string) *store.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "reflex-dispatch-parity-"+name+".db")
	st, err := storetest.New(t, context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	if err := st.CreateAgent(context.Background(), &store.AgentProfile{
		ID:           agentID,
		Name:         "Dispatch Parity Agent",
		Slug:         agentID,
		Class:        "advisor",
		SystemPrompt: "test",
		Source:       "test",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	return st
}

func insertParityReflex(t *testing.T, st *store.Store, row store.AgentReflex) {
	t.Helper()
	if _, err := st.InsertAgentReflex(context.Background(), row); err != nil {
		t.Fatalf("InsertAgentReflex(%s): %v", row.Name, err)
	}
}

func runServiceReflexDispatchParity(t *testing.T, st *store.Store, sessionID, agentID, message string) reflexDispatchParityResult {
	t.Helper()
	assertParityClassification(t, message)
	tools := &recordingReflexDispatchToolService{}
	s := &chatServiceImpl{
		store:        st,
		reflexEngine: reflexes.NewEngine(st, nil),
		tools:        tools,
	}
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	classifyAndAttach(ls, sessionID, message, nil)
	ch := make(chan chat.StreamEvent, 4)
	out := s.attemptReflexDispatch(
		context.Background(),
		sessionID, "turn-"+sessionID, message,
		agentID, "advisor",
		ls, ch,
	)
	close(ch)
	if !out.Matched {
		return reflexDispatchParityResult{}
	}
	return reflexDispatchParityResult{
		Matched:    true,
		ReflexID:   out.ReflexID,
		ReflexName: out.ReflexName,
		AgentSlug:  out.AgentSlug,
	}
}

func runSelftoolsReflexDispatchParity(t *testing.T, st *store.Store, sessionID, agentID, message string) reflexDispatchParityResult {
	t.Helper()
	assertParityClassification(t, message)
	spawner := &reflexDispatchParitySpawner{}
	transport := selftools.NewSelfToolsTransport(st)
	transport.Dispatch = spawner
	ctx := mcp.WithCallerProfile(mcp.WithSessionID(context.Background(), sessionID), agentID)
	res, err := transport.CallTool(ctx, "task_execute", map[string]any{
		"session_id": sessionID,
		"message":    message,
	})
	if err != nil {
		t.Fatalf("task_execute: %v", err)
	}
	if res == nil {
		t.Fatalf("task_execute returned nil result")
	}
	if res.IsError {
		t.Fatalf("task_execute returned error result: %+v", res)
	}
	if len(spawner.calls) != 1 {
		t.Fatalf("task_execute spawned %d times, want 1", len(spawner.calls))
	}

	events, err := st.ListEvents(context.Background(), "reflex", 50)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	for i := range events {
		if events[i].SessionID == sessionID && events[i].EventType == store.ReflexActionDispatchToAgent {
			var meta struct {
				ReflexID   string         `json:"reflex_id"`
				ReflexName string         `json:"reflex_name"`
				Spec       map[string]any `json:"spec"`
			}
			if err := json.Unmarshal([]byte(events[i].Metadata), &meta); err != nil {
				t.Fatalf("event metadata for %s is not JSON: %v\nblob: %s", events[i].Detail, err, events[i].Metadata)
			}
			agentSlug, _ := meta.Spec["agent_slug"].(string)
			if agentSlug != "" && spawner.calls[0].Role != agentSlug {
				t.Fatalf("task_execute spawned role %q, but matched dispatch_to_agent spec agent_slug is %q", spawner.calls[0].Role, agentSlug)
			}
			return reflexDispatchParityResult{
				Matched:    true,
				ReflexID:   meta.ReflexID,
				ReflexName: meta.ReflexName,
				AgentSlug:  agentSlug,
			}
		}
	}
	return reflexDispatchParityResult{}
}

func assertParityClassification(t *testing.T, message string) {
	t.Helper()
	tier, pattern := classify.Classify(classify.IntentSignals{
		Message:         message,
		MessageTokenEst: len(message) / 4,
	})
	if !tier.IsValid() || !pattern.IsValid() {
		t.Fatalf("parity fixture message classified as invalid: (%s, %s)", tier, pattern)
	}
}

func assertReflexDispatchParityResult(t *testing.T, label string, got, want reflexDispatchParityResult) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %+v, want %+v", label, got, want)
	}
}
