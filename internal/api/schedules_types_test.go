package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// TestAgentScheduleViewJSONKeys pins AgentScheduleView to the key set
// store.AgentSchedule emitted when it was serialized directly.
func TestAgentScheduleViewJSONKeys(t *testing.T) {
	want := []string{
		"agent_id", "body", "created_at", "created_by", "expires_at",
		"fired_count", "id", "job_payload", "job_type", "last_fired_at",
		"max_retries", "name", "next_run", "on_fail", "priority",
		"schedule_kind", "schedule_spec", "session_id", "status",
	}
	if got := jsonKeys(t, agentScheduleToView(&store.AgentSchedule{})); !reflect.DeepEqual(got, want) {
		t.Fatalf("keys = %v\nwant   %v", got, want)
	}
	raw, _ := json.Marshal(agentSchedulesToView(nil))
	if string(raw) != "[]" {
		t.Fatalf("empty list marshaled %s, want []", raw)
	}
}

// A missing schedule is reported (404) before a malformed PATCH body is read.
func TestPatchScheduleNotFoundBeforeBodyDecode(t *testing.T) {
	_, mux := newTestAPI(t)
	req := httptest.NewRequest(http.MethodPatch, "/api/schedules/no-such-schedule", strings.NewReader(`{not json`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404; body %s", w.Code, w.Body.String())
	}
	if msg := errorBody(t, w); msg != "schedule not found" {
		t.Fatalf("error %q, want %q", msg, "schedule not found")
	}
}
