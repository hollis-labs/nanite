package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// TestReflexViewJSONKeys pins each view to the key set the store row emitted
// when it was serialized directly. Zero values are marshaled deliberately:
// no key may disappear through omitempty.
func TestReflexViewJSONKeys(t *testing.T) {
	cases := []struct {
		name string
		view any
		want []string
	}{
		{"agent reflex", agentReflexToView(&store.AgentReflex{}), []string{
			"action_kind", "action_spec", "agent_id", "class_tag", "created_at",
			"created_by", "fired_count", "id", "last_fired_at", "name",
			"opt_out_allowed", "priority", "provenance_tier",
			"recurrence_override_seconds", "status", "trigger_kind",
			"trigger_spec", "workflow_run_id",
		}},
		{"pending reflex", pendingReflexesToView([]store.PendingReflex{{}})[0], []string{
			"action_kind", "action_spec", "id", "name", "proposed_at",
			"proposed_by", "rationale", "reviewed_at", "reviewed_by", "status",
			"target_agent_id", "trigger_kind", "trigger_spec",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := jsonKeys(t, tc.view); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("keys = %v\nwant   %v", got, tc.want)
			}
		})
	}
}

// An unset recurrence override has always reached the UI as null.
func TestAgentReflexViewRecurrenceOverrideNull(t *testing.T) {
	raw, err := json.Marshal(agentReflexToView(&store.AgentReflex{}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Contains(raw, []byte(`"recurrence_override_seconds":null`)) {
		t.Fatalf("marshaled %s, want recurrence_override_seconds:null", raw)
	}
	n := int64(600)
	raw, _ = json.Marshal(agentReflexToView(&store.AgentReflex{RecurrenceOverrideSeconds: &n}))
	if !bytes.Contains(raw, []byte(`"recurrence_override_seconds":600`)) {
		t.Fatalf("marshaled %s, want recurrence_override_seconds:600", raw)
	}
}

func TestReflexViewEmptyListsMarshalAsArray(t *testing.T) {
	cases := map[string]any{
		"agent reflexes":   agentReflexesToView(nil),
		"pending reflexes": pendingReflexesToView(nil),
	}
	for name, v := range cases {
		raw, _ := json.Marshal(v)
		if string(raw) != "[]" {
			t.Fatalf("%s: marshaled %s, want []", name, raw)
		}
	}
}

// A valid definition has always produced "errors": null from the validate
// endpoint, not [].
func TestValidateReflexEndpointValidErrorsNull(t *testing.T) {
	_, mux := newTestAPI(t)
	body := `{"trigger_kind":"predicate","trigger_spec":"{\"kind\":\"tool_calls_window\",\"window\":1,\"op\":\"=\",\"value\":0}","action_kind":"inject_reminder","action_spec":"{\"text\":\"hi\"}"}`
	req := httptest.NewRequest(http.MethodPost, "/api/reflexes/validate", strings.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d; body %s", w.Code, w.Body.String())
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(got["valid"]) != "true" || string(got["errors"]) != "null" {
		t.Fatalf("valid=%s errors=%s, want true and null; body %s", got["valid"], got["errors"], w.Body.String())
	}
}

// A missing reflex (404) and another agent's reflex (400 ownership) are both
// reported before a malformed body is read.
func TestPatchAgentReflexPrecedenceBeforeBodyDecode(t *testing.T) {
	a, mux := newTestAPI(t)
	owner := createTestAgent(t, a, "reflex-owner-agent")
	caller := createTestAgent(t, a, "reflex-caller-agent")
	id, err := a.Services.Store.InsertAgentReflex(context.Background(), store.AgentReflex{
		AgentID:     owner.ID,
		Name:        "owned",
		TriggerKind: store.ReflexTriggerPredicate,
		TriggerSpec: `{"kind":"tool_calls_window","window":1,"op":"=","value":0}`,
		ActionKind:  store.ReflexActionInjectReminder,
		ActionSpec:  `{"text":"hi"}`,
		CreatedBy:   "operator",
	})
	if err != nil {
		t.Fatalf("InsertAgentReflex: %v", err)
	}

	cases := []struct {
		name, path string
		code       int
		msg        string
	}{
		{"missing", "/api/agents/" + caller.ID + "/reflexes/no-such-reflex", http.StatusNotFound, "reflex not found"},
		{"foreign", "/api/agents/" + caller.ID + "/reflexes/" + id, http.StatusBadRequest, "cannot patch inherited or different-agent reflex through this endpoint"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodPatch, tc.path, strings.NewReader(`{not json`))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != tc.code {
			t.Fatalf("%s: status %d, want %d; body %s", tc.name, w.Code, tc.code, w.Body.String())
		}
		if msg := errorBody(t, w); msg != tc.msg {
			t.Fatalf("%s: error %q, want %q", tc.name, msg, tc.msg)
		}
	}
}
