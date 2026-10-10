package api

import (
	"bytes"
	"encoding/json"
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

func TestRetiredReflexValidateAndPatchRefuseBeforeDecode(t *testing.T) {
	a, mux := newTestAPI(t)
	retiredAPIHistoricalProfile(t, a, "typed-reflex-retained", "user")
	const query = `SELECT * FROM agent_profiles ORDER BY id`
	before := retiredAPISnapshot(t, a, query)
	for _, path := range []string{"/api/reflexes/validate", "/api/agents/typed-reflex-retained/reflexes/missing"} {
		method := "POST"
		if strings.Contains(path, "/agents/") {
			method = "PATCH"
		}
		for _, body := range []string{"not json", `{"trigger_kind":"event","trigger_spec":"{}","action_kind":"inject_reminder","action_spec":"{\"text\":\"hi\"}"}`} {
			requireRetiredAPI(t, retiredAPIRequest(t, mux, method, path, body))
		}
	}
	retiredAPIHistoryUnchanged(t, a, query, before)
}
