package server

import (
	"encoding/json"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/hollis-labs/nanite/internal/config"
)

// /api/health reports the warnings it was given, such as agent sandbox
// protection being off (CW-20261001-0143), and omits the key otherwise.
func TestHealthReportsWarnings(t *testing.T) {
	s := newTestServer(t, config.HTTPConfig{})
	get := func() map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		s.handleHealth(w, httptest.NewRequest("GET", "/api/health", nil))
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	if body := get(); body["status"] != "ok" || body["warnings"] != nil {
		t.Errorf("health without warnings = %v", body)
	}
	s.SetHealthWarnings([]string{"agent sandbox protection is off"})
	body := get()
	got, _ := body["warnings"].([]any)
	if body["status"] != "ok" || !slices.Equal(got, []any{"agent sandbox protection is off"}) {
		t.Errorf("health with a warning = %v", body)
	}
}
