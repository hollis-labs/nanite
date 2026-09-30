package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

func postCreateSession(t *testing.T, a *API, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	rec := httptest.NewRecorder()
	a.handleCreateSession(rec, httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewReader(raw)))
	return rec
}

// A session that names an unknown or invalid harness profile is refused at
// creation, and a valid selection is stored where the chat loop reads it.
func TestCreateSession_HarnessProfileSelection(t *testing.T) {
	a, s := newToolCallTestAPI(t)

	rec := postCreateSession(t, a, map[string]any{"harness_profile": "does-not-exist"})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "unknown harness profile") {
		t.Errorf("unknown profile: %d %s", rec.Code, rec.Body.String())
	}
	rec = postCreateSession(t, a, map[string]any{"harness_profile": "dev", "harness_overrides": map[string]any{"harness": map[string]any{"hard_ceiling": -1}}})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid overrides: %d %s", rec.Code, rec.Body.String())
	}

	rec = postCreateSession(t, a, map[string]any{"harness_profile": "conservative", "harness_overrides": map[string]any{"harness": map[string]any{"hard_ceiling": 9}}})
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("valid selection: %d %s", rec.Code, rec.Body.String())
	}
	var sess store.Session
	if err := json.Unmarshal(rec.Body.Bytes(), &sess); err != nil || sess.ID == "" {
		t.Fatalf("response: %v %s", err, rec.Body.String())
	}
	got, err := s.GetSession(t.Context(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	profile, overrides, err := service.SessionHarnessSelection(got.Metadata)
	if err != nil || profile != "conservative" || overrides.Harness.HardCeiling == nil || *overrides.Harness.HardCeiling != 9 {
		t.Errorf("stored selection = %q %+v (%v) from %s", profile, overrides, err, got.Metadata)
	}

	// No selection stores nothing and still works.
	rec = postCreateSession(t, a, map[string]any{})
	if rec.Code >= 300 {
		t.Errorf("plain create: %d", rec.Code)
	}
}
