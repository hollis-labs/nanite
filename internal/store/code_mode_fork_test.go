package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// This fake represents a trusted host decision only in a private test. It does
// not create an issuer, authenticate claimed actors or wire a production port.
type codeModeTestVerifier struct {
	callerErr, forkErr, historyErr error
	limits                         CodeModeContextLimits
	onFork                         func()
	onHistory                      func()
	callerCalls, forkCalls         int
	historyCalls                   int
}

func (v *codeModeTestVerifier) VerifyCaller(context.Context, string) (CodeModeContextLimits, error) {
	v.callerCalls++
	return v.limits, v.callerErr
}
func (v *codeModeTestVerifier) VerifyFork(_ context.Context, _ CodeModeParentSnapshot, request CodeModeForkRequest) error {
	v.forkCalls++
	if len(request.Inputs) > 0 {
		request.Inputs[0] = '!' // Callback receives a detached request copy.
	}
	if v.onFork != nil {
		v.onFork()
	}
	return v.forkErr
}
func (v *codeModeTestVerifier) VerifyHistory(context.Context, CodeModeHistorySnapshot, CodeModeHistoryRequest) error {
	v.historyCalls++
	if v.onHistory != nil {
		v.onHistory()
	}
	return v.historyErr
}

func codeModePrivateHost() *codeModeTestVerifier {
	return &codeModeTestVerifier{limits: CodeModeContextLimits{DefaultLastN: 1, MaxLastN: 8, MaxMessages: 32, MaxBytes: 8192, HistoryMaxMessages: 64, HistoryMaxBytes: 65536}}
}

func codeModeParent(t *testing.T, s *Store) *Session {
	t.Helper()
	pin, err := s.InstallAgentDefinition(t.Context(), definitionTestBytes("def:code-mode-parent", "1", "Pinned parent instructions."), nil)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := json.Marshal(map[string]string{"definition_id": pin.ID, "revision": pin.Revision, "semantic_digest": pin.Digest})
	if err != nil {
		t.Fatal(err)
	}
	view := &Session{Provider: "private-host-model", Model: "parent-model", Metadata: `{"operator_token":"SECRET","approval":"PRIVATE","scratchpad":"not explicit"}`}
	cfg := `{"instructions":"Pinned parent instructions.","permission_profile":"default","model":{"provider":"private-host-model","model":"parent-model"}}`
	if operationErr := s.CreateDefinedCognitiveSession(t.Context(), view, "", CognitiveViewRecord{DefinitionRefJSON: string(ref), ChatConfigJSON: cfg}); operationErr != nil {
		t.Fatal(operationErr)
	}
	for i := 0; i < 3; i++ {
		for _, msg := range []*Message{
			{Role: "user", Content: fmt.Sprintf("needle goal %d", i)},
			{Role: "assistant", Content: fmt.Sprintf(`{"v":1,"text":"answer %d","envelopes":[{"approval":"PRIVATE"}],"tool_calls":[{"id":"parent-tool","status":"success"}]}`, i), Envelope: `{"pending":"PRIVATE"}`, Metadata: `{"thinking_blocks":[{"signature":"SECRET"}]}`},
			{Role: "tool", Content: fmt.Sprintf("needle result %d", i)},
		} {
			msg.SessionID, msg.AgentID = view.ID, "claimed-parent-label"
			if operationErr := s.CreateMessage(t.Context(), msg); operationErr != nil {
				t.Fatal(operationErr)
			}
		}
	}
	return view
}

func codeModeState(t *testing.T, s *Store) map[string]string {
	t.Helper()
	state := map[string]string{}
	for _, name := range []string{"sessions", "messages", "cognitive_views", "cognitive_turns", "session_actor_bindings", "agent_actor_bindings", "actor_granted_tools", "agent_profiles", "envelope_instances", "event_log"} {
		rows, err := s.DB.QueryContext(t.Context(), fmt.Sprintf(`SELECT * FROM %q ORDER BY rowid`, name))
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		var all [][]any
		for rows.Next() {
			values, dest := make([]any, len(columns)), make([]any, len(columns))
			for i := range values {
				dest[i] = &values[i]
			}
			if operationErr := rows.Scan(dest...); operationErr != nil {
				t.Fatal(operationErr)
			}
			for i, value := range values {
				if bytes, ok := value.([]byte); ok {
					values[i] = string(bytes)
				}
			}
			all = append(all, values)
		}
		if operationErr := rows.Err(); operationErr != nil {
			t.Fatal(operationErr)
		}
		if operationErr := rows.Close(); operationErr != nil {
			t.Fatal(operationErr)
		}
		encoded, err := json.Marshal(all)
		if err != nil {
			t.Fatal(err)
		}
		state[name] = string(encoded)
	}
	return state
}

func TestCodeModeForkHostRefusalBeforeEffects(t *testing.T) {
	s := newTestStore(t)
	parent := codeModeParent(t, s)
	before := codeModeState(t, s)
	for _, test := range []struct {
		name     string
		verifier CodeModeForkVerifier
	}{
		{"missing host port", nil},
		{"foreign caller", &codeModeTestVerifier{callerErr: ErrVerifiedActorRequired}},
		{"stale host binding", &codeModeTestVerifier{callerErr: ErrVerifiedActorRequired}},
		{"denied parent visibility", &codeModeTestVerifier{limits: codeModePrivateHost().limits, forkErr: ErrVerifiedActorRequired}},
	} {
		t.Run(test.name, func(t *testing.T) {
			view, err := s.ForkCodeModeSession(t.Context(), parent.ID, CodeModeForkOptions{Verifier: test.verifier, Request: CodeModeForkRequest{Goal: "bounded task"}})
			if view != nil || !errors.Is(err, ErrVerifiedActorRequired) || !reflect.DeepEqual(before, codeModeState(t, s)) {
				t.Fatalf("refused fork changed source/authority: %+v,%v", view, err)
			}
		})
	}
}

func TestCodeModeForkContextAndPinIsolation(t *testing.T) {
	s := newTestStore(t)
	parent := codeModeParent(t, s)
	original, err := s.GetCognitiveView(t.Context(), parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	verifier := codeModePrivateHost()
	view, err := s.ForkCodeModeSession(t.Context(), parent.ID, CodeModeForkOptions{Verifier: verifier, Request: CodeModeForkRequest{Goal: "compute result", Inputs: json.RawMessage(`{"value":7}`), PinnedHandoff: "explicit handoff", Scratchpad: "explicit scratchpad"}})
	if err != nil {
		t.Fatal(err)
	}
	if view.ID == parent.ID || view.MessageCount != 4 || view.Provider != parent.Provider || view.Model != parent.Model || strings.Contains(view.Metadata, "SECRET") || strings.Contains(view.Metadata, "PRIVATE") || !strings.Contains(view.Metadata, `"fork_kind":"code_mode"`) {
		t.Fatalf("unsafe fork view: %+v", view)
	}
	forkPin, err := s.GetCognitiveView(t.Context(), view.ID)
	if err != nil || forkPin.DefinitionRefJSON != original.DefinitionRefJSON {
		t.Fatalf("immutable pin changed: %+v,%v", forkPin, err)
	}
	messages, err := s.ListMessages(t.Context(), view.ID, 64)
	if err != nil || len(messages) != 4 {
		t.Fatalf("last whole group+bootstrap: %+v,%v", messages, err)
	}
	if messages[0].Content != "needle goal 2" || messages[1].Content != "answer 2" || messages[2].Role != "assistant" || !strings.Contains(messages[2].Content, "needle result 2") || !strings.Contains(messages[3].Content, `"value":7`) || !strings.Contains(messages[3].Content, "explicit handoff") || !strings.Contains(messages[3].Content, "explicit scratchpad") {
		t.Fatalf("fork context or callback isolation failed: %+v", messages)
	}
	for _, m := range messages {
		if m.AgentID != "" || m.Envelope != "" || m.Metadata != "{}" || strings.Contains(m.Content, "PRIVATE") || strings.Contains(m.Content, "SECRET") || strings.Contains(m.Content, "parent-tool") {
			t.Fatalf("authority/reference state copied: %+v", m)
		}
	}
	var bindings, grants int
	if operationErr := s.DB.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM session_actor_bindings WHERE session_id=?),(SELECT count(*) FROM actor_granted_tools)`, view.ID).Scan(&bindings, &grants); operationErr != nil || bindings != 0 || grants != 0 {
		t.Fatalf("fork created authority bindings=%d grants=%d: %v", bindings, grants, operationErr)
	}
	parentMessages, err := s.ListMessages(t.Context(), parent.ID, 64)
	if err != nil || len(parentMessages) != 9 || !strings.Contains(parentMessages[1].Content, "PRIVATE") {
		t.Fatalf("parent trace changed: %+v,%v", parentMessages, err)
	}
	for _, fork := range []func() (*Session, error){
		func() (*Session, error) {
			return s.ForkCodeModeSession(t.Context(), view.ID, CodeModeForkOptions{Verifier: verifier, Request: CodeModeForkRequest{Goal: "nested"}})
		},
		func() (*Session, error) { return s.ForkSession(t.Context(), view.ID, nil, true) },
	} {
		before := codeModeState(t, s)
		if nested, err := fork(); nested != nil || !errors.Is(err, ErrCodeModeNested) || !reflect.DeepEqual(before, codeModeState(t, s)) {
			t.Fatalf("nested fork admitted: %+v,%v", nested, err)
		}
	}
}

func TestCodeModeForkBoundsAndUnsupportedOverrides(t *testing.T) {
	s := newTestStore(t)
	parent := codeModeParent(t, s)
	before := codeModeState(t, s)
	for _, test := range []struct {
		request CodeModeForkRequest
		want    error
	}{
		{CodeModeForkRequest{Goal: "x", LastN: 9}, ErrCodeModeContextLimit},
		{CodeModeForkRequest{Goal: strings.Repeat("x", 8193)}, ErrCodeModeContextLimit},
		{CodeModeForkRequest{Goal: "x", ToolGrants: []string{"admin"}}, ErrCodeModeEscalation},
		{CodeModeForkRequest{Goal: "x", PermissionPosture: "allow"}, ErrCodeModeEscalation},
		{CodeModeForkRequest{Goal: "x", Profile: "privileged"}, ErrCodeModeEscalation},
		{CodeModeForkRequest{Goal: "x", Model: "broader-model"}, ErrCodeModeEscalation},
		{CodeModeForkRequest{Goal: "x", Provider: "other-host"}, ErrCodeModeEscalation},
	} {
		view, err := s.ForkCodeModeSession(t.Context(), parent.ID, CodeModeForkOptions{Verifier: codeModePrivateHost(), Request: test.request})
		if view != nil || !errors.Is(err, test.want) || !reflect.DeepEqual(before, codeModeState(t, s)) {
			t.Fatalf("unsupported override/budget admitted: %+v,%v want %v", view, err, test.want)
		}
	}
	full, err := s.ForkCodeModeSession(t.Context(), parent.ID, CodeModeForkOptions{Verifier: codeModePrivateHost(), Request: CodeModeForkRequest{Goal: "explicit full context", FullHistory: true}})
	if err != nil || full.MessageCount != 10 {
		t.Fatalf("explicit bounded full history: %+v,%v", full, err)
	}
}

func TestCodeModeForkRevalidatesAfterHostPolicy(t *testing.T) {
	s := newTestStore(t)
	parent := codeModeParent(t, s)
	verifier := codeModePrivateHost()
	verifier.onFork = func() {
		if operationErr := s.UpdateSessionMetadata(t.Context(), parent.ID, `{"host_revision":"changed"}`); operationErr != nil {
			t.Fatal(operationErr)
		}
	}
	view, err := s.ForkCodeModeSession(t.Context(), parent.ID, CodeModeForkOptions{Verifier: verifier, Request: CodeModeForkRequest{Goal: "x"}})
	if view != nil || !errors.Is(err, ErrCodeModeSnapshot) {
		t.Fatalf("callback-time drift admitted: %+v,%v", view, err)
	}
	var count int
	if operationErr := s.DB.QueryRowContext(t.Context(), `SELECT count(*) FROM sessions`).Scan(&count); operationErr != nil || count != 1 {
		t.Fatalf("stale admission wrote child: %d,%v", count, operationErr)
	}
}

func TestOrdinaryForkRejectsActorTransferAndRetainsSafeContext(t *testing.T) {
	s := newTestStore(t)
	parent := codeModeParent(t, s)
	actor := makeTestAgent(t, s, "ordinary-fork")
	if operationErr := s.EnsureSessionAgent(t.Context(), parent.ID, actor.ID, "", true); operationErr != nil {
		t.Fatal(operationErr)
	}
	before := codeModeState(t, s)
	for _, include := range []bool{false, true} {
		fork, err := s.ForkSession(t.Context(), parent.ID, nil, include)
		if fork != nil || !errors.Is(err, ErrVerifiedActorRequired) || !reflect.DeepEqual(before, codeModeState(t, s)) {
			t.Fatalf("parent receipt created child authority: %+v,%v", fork, err)
		}
	}
	if _, err := s.DB.ExecContext(t.Context(), `DELETE FROM session_actor_bindings WHERE session_id=?`, parent.ID); err != nil {
		t.Fatal(err)
	}
	before = codeModeState(t, s)
	if fork, err := s.ForkSession(t.Context(), parent.ID, &Session{Model: "other-model"}, true); fork != nil || !errors.Is(err, ErrCodeModeEscalation) || !reflect.DeepEqual(before, codeModeState(t, s)) {
		t.Fatalf("ordinary fork replaced native host selection: %+v,%v", fork, err)
	}
	fork, err := s.ForkSession(t.Context(), parent.ID, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	pin, err := s.GetCognitiveView(t.Context(), fork.ID)
	if err != nil || pin.ChatConfigJSON == "" || strings.Contains(fork.Metadata, "SECRET") || strings.Contains(fork.Metadata, "PRIVATE") {
		t.Fatalf("ordinary context/pin unsafe: %+v,%v", fork, err)
	}
	messages, err := s.ListMessages(t.Context(), fork.ID, 64)
	if err != nil || len(messages) != 9 {
		t.Fatalf("ordinary context changed: %+v,%v", messages, err)
	}
	for _, m := range messages {
		if m.AgentID != "" || m.Envelope != "" || m.Metadata != "{}" || strings.Contains(m.Content, "PRIVATE") {
			t.Fatalf("ordinary fork copied runtime state: %+v", m)
		}
	}
}

func TestCodeModeForkCancellationAndConfiguredGroupBounds(t *testing.T) {
	s := newTestStore(t)
	parent := codeModeParent(t, s)
	before := codeModeState(t, s)
	for _, duringPolicy := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		verifier := codeModePrivateHost()
		if duringPolicy {
			verifier.onFork = cancel
		} else {
			cancel()
		}
		fork, err := s.ForkCodeModeSession(ctx, parent.ID, CodeModeForkOptions{Verifier: verifier, Request: CodeModeForkRequest{Goal: "canceled"}})
		cancel()
		if fork != nil || !errors.Is(err, context.Canceled) || !reflect.DeepEqual(before, codeModeState(t, s)) {
			t.Fatalf("canceled admission effects: %+v,%v", fork, err)
		}
	}
	verifier := codeModePrivateHost()
	verifier.limits.MaxMessages = 3 // Whole group plus bootstrap cannot fit.
	if fork, err := s.ForkCodeModeSession(t.Context(), parent.ID, CodeModeForkOptions{Verifier: verifier, Request: CodeModeForkRequest{Goal: "bounded"}}); fork != nil || !errors.Is(err, ErrCodeModeContextLimit) || !reflect.DeepEqual(before, codeModeState(t, s)) {
		t.Fatalf("message budget split group or omitted bootstrap: %+v,%v", fork, err)
	}
	verifier = codeModePrivateHost()
	verifier.limits.DefaultLastN = 2
	fork, err := s.ForkCodeModeSession(t.Context(), parent.ID, CodeModeForkOptions{Verifier: verifier, Request: CodeModeForkRequest{Goal: "host-configured context"}})
	if err != nil || fork.MessageCount != 7 {
		t.Fatalf("host-configurable coherent groups: %+v,%v", fork, err)
	}
	messages, err := s.ListMessages(t.Context(), fork.ID, 64)
	if err != nil || messages[0].Content != "needle goal 1" || !strings.Contains(messages[2].Content, "needle result 1") || messages[3].Content != "needle goal 2" {
		t.Fatalf("configured context orphaned call/result group: %+v,%v", messages, err)
	}
}

func TestCodeModeForkRefusesUninstalledPinModelDriftAndOversizedParent(t *testing.T) {
	s := newTestStore(t)
	parent := codeModeParent(t, s)
	pin, err := s.GetCognitiveView(t.Context(), parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, operationErr := s.DB.ExecContext(t.Context(), `UPDATE cognitive_views SET definition_ref_json=? WHERE session_view_id=?`, `{"definition_id":"def:code-mode-parent","revision":"1","semantic_digest":"claimed"}`, parent.ID); operationErr != nil {
		t.Fatal(operationErr)
	}
	before := codeModeState(t, s)
	fork, err := s.ForkCodeModeSession(t.Context(), parent.ID, CodeModeForkOptions{Verifier: codeModePrivateHost(), Request: CodeModeForkRequest{Goal: "x"}})
	if fork != nil || !errors.Is(err, ErrDefinitionContent) || !reflect.DeepEqual(before, codeModeState(t, s)) {
		t.Fatalf("claimed definition pin created child: %+v,%v", fork, err)
	}
	if _, operationErr := s.DB.ExecContext(t.Context(), `UPDATE cognitive_views SET definition_ref_json=? WHERE session_view_id=?`, pin.DefinitionRefJSON, parent.ID); operationErr != nil {
		t.Fatal(operationErr)
	}
	if _, operationErr := s.DB.ExecContext(t.Context(), `UPDATE sessions SET model='unapproved-selection' WHERE id=?`, parent.ID); operationErr != nil {
		t.Fatal(operationErr)
	}
	before = codeModeState(t, s)
	fork, err = s.ForkCodeModeSession(t.Context(), parent.ID, CodeModeForkOptions{Verifier: codeModePrivateHost(), Request: CodeModeForkRequest{Goal: "x"}})
	if fork != nil || !errors.Is(err, ErrCodeModeSnapshot) || !reflect.DeepEqual(before, codeModeState(t, s)) {
		t.Fatalf("session presentation replaced approved model: %+v,%v", fork, err)
	}
	if _, operationErr := s.DB.ExecContext(t.Context(), `UPDATE sessions SET model=? WHERE id=?`, parent.Model, parent.ID); operationErr != nil {
		t.Fatal(operationErr)
	}
	if operationErr := s.CreateMessage(t.Context(), &Message{SessionID: parent.ID, Role: "user", Content: strings.Repeat("x", 65537)}); operationErr != nil {
		t.Fatal(operationErr)
	}
	before = codeModeState(t, s)
	fork, err = s.ForkCodeModeSession(t.Context(), parent.ID, CodeModeForkOptions{Verifier: codeModePrivateHost(), Request: CodeModeForkRequest{Goal: "x"}})
	if fork != nil || !errors.Is(err, ErrCodeModeContextLimit) || !reflect.DeepEqual(before, codeModeState(t, s)) {
		t.Fatalf("oversized snapshot created partial child: %+v,%v", fork, err)
	}
}
