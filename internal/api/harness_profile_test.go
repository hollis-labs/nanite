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

// The endpoint reports what the session's next turn would run under, with the
// source of each value, honoring the session's selection and ?model=.
func TestSessionHarnessProfileEndpoint(t *testing.T) {
	a, s := newToolCallTestAPI(t)
	rec := postCreateSession(t, a, map[string]any{"harness_profile": "conservative", "model": "claude-opus-5"})
	var sess store.Session
	if err := json.Unmarshal(rec.Body.Bytes(), &sess); err != nil || sess.ID == "" {
		t.Fatalf("create: %v %s", err, rec.Body.String())
	}
	get := func(target string) (*httptest.ResponseRecorder, map[string]any) {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.SetPathValue("id", sess.ID)
		w := httptest.NewRecorder()
		a.handleGetSessionHarnessProfile(w, req)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w, out
	}
	w, out := get("/api/sessions/" + sess.ID + "/harness-profile")
	if w.Code != http.StatusOK || out["profile"] != "conservative" || out["model"] != "claude-opus-5" {
		t.Fatalf("response: %d %s", w.Code, w.Body.String())
	}
	values, _ := out["values"].(map[string]any)
	sources, _ := out["sources"].(map[string]any)
	hc, _ := sources["hard_ceiling"].(map[string]any)
	if values["hard_ceiling"].(float64) != 100 || hc["layer"] != "profile:conservative" {
		t.Errorf("hard_ceiling = %v from %v", values["hard_ceiling"], hc)
	}
	if !strings.HasPrefix(out["digest"].(string), "sha256:") {
		t.Errorf("digest = %v", out["digest"])
	}
	if _, out = get("/api/sessions/" + sess.ID + "/harness-profile?model=gpt-5"); out["model"] != "gpt-5" {
		t.Errorf("?model= ignored: %v", out["model"])
	}
	// A stored selection that no longer resolves is reported, not hidden.
	if err := s.UpdateSessionMetadata(t.Context(), sess.ID, `{"harness_profile":"gone"}`); err != nil {
		t.Fatal(err)
	}
	if w, _ = get("/api/sessions/" + sess.ID + "/harness-profile"); w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "unknown harness profile") {
		t.Errorf("stale selection: %d %s", w.Code, w.Body.String())
	}
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.SetPathValue("id", "no-such-session")
	nf := httptest.NewRecorder()
	a.handleGetSessionHarnessProfile(nf, req)
	if nf.Code != http.StatusNotFound {
		t.Errorf("missing session: %d", nf.Code)
	}
}
