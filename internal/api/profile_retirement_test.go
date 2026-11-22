package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

func TestProfileRetirementAPIRequiresPersistedExport(t *testing.T) {
	a, mux := newTestAPI(t)
	p, err := apiHistoricalRetirementFixture(t, a, &store.AgentProfile{Name: "Test", Slug: "api-retirement", SystemPrompt: "private secret fixture"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/agents/" + p.Profile.ID
	call := func(route string, body []byte) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, route, bytes.NewReader(body)))
		return w
	}
	refusal := call(path+"/retire", []byte(`{"export_id":"../missing","digest":"sha256:missing"}`))
	if refusal.Code != http.StatusConflict {
		t.Fatal("unexported deletion accepted", refusal.Code)
	}
	exported := call(path+"/retirement-export", nil)
	if exported.Code != http.StatusCreated {
		t.Fatal("export failed", exported.Code, exported.Body.String())
	}
	if bytes.Contains(exported.Body.Bytes(), []byte("private secret fixture")) {
		t.Fatal("export endpoint exposed private payload")
	}
	var receipt service.ProfileRetirementReceipt
	if err = json.Unmarshal(exported.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]string{"export_id": receipt.ExportID, "digest": receipt.Digest})
	if err != nil {
		t.Fatal(err)
	}
	malformed := call(path+"/retire", append(append([]byte{}, raw...), []byte(` {}`)...))
	if malformed.Code != http.StatusBadRequest {
		t.Fatal("trailing JSON accepted", malformed.Code)
	}
	retired := call(path+"/retire", raw)
	if retired.Code != http.StatusOK {
		t.Fatal("retirement failed", retired.Code, retired.Body.String())
	}
	var completed service.ProfileRetirementReceipt
	if err = json.Unmarshal(retired.Body.Bytes(), &completed); err != nil || !completed.Retired || !completed.ReceiptPersisted {
		t.Fatal("retirement outcome lost", err)
	}
}

func TestProfileRetirementAPIProtectsBuiltinAndPluginOwnership(t *testing.T) {
	a, mux := newTestAPI(t)
	for _, p := range []*store.AgentProfile{{Name: "Builtin", Slug: "builtin-retirement", Source: "builtin", SystemPrompt: "x"}, {Name: "Plugin", Slug: "plugin-retirement", Source: "user", PluginID: "fixture-plugin", SystemPrompt: "x"}} {
		if err := storetest.HistoricalProfile(t.Context(), a.store, p); err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/agents/"+p.ID+"/retirement-export", nil))
		if w.Code != http.StatusForbidden {
			t.Fatal("protected source export accepted", w.Code)
		}
	}
}

func TestProfileRetirementAPIRejectsStateChangedAfterExport(t *testing.T) {
	a, mux := newTestAPI(t)
	created, err := apiHistoricalRetirementFixture(t, a, &store.AgentProfile{Name: "Test", Slug: "retirement-cas-public", SystemPrompt: "original"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/agents/" + created.Profile.ID
	exported := httptest.NewRecorder()
	mux.ServeHTTP(exported, httptest.NewRequest(http.MethodPost, path+"/retirement-export", nil))
	if exported.Code != http.StatusCreated {
		t.Fatal(exported.Code, exported.Body.String())
	}
	var receipt service.ProfileRetirementReceipt
	if err := json.Unmarshal(exported.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	changed := httptest.NewRecorder()
	mux.ServeHTTP(changed, httptest.NewRequest(http.MethodPut, path, bytes.NewBufferString(`{"system_prompt":"changed"}`)))
	if changed.Code != http.StatusGone {
		t.Fatal(changed.Code, changed.Body.String())
	}
	// Simulate an out-of-band historical writer in this private database. The
	// retired public PUT must remain unavailable, while export CAS must still
	// detect a historical change made between export and retirement.
	if _, historicalWriteErr := a.store.DB.ExecContext(t.Context(), `UPDATE agent_profiles SET system_prompt='changed' WHERE id=?`, created.Profile.ID); historicalWriteErr != nil {
		t.Fatal(historicalWriteErr)
	}
	request, err := json.Marshal(map[string]string{"export_id": receipt.ExportID, "digest": receipt.Digest})
	if err != nil {
		t.Fatal(err)
	}
	retired := httptest.NewRecorder()
	mux.ServeHTTP(retired, httptest.NewRequest(http.MethodPost, path+"/retire", bytes.NewReader(request)))
	if retired.Code != http.StatusConflict {
		t.Fatal(retired.Code, retired.Body.String())
	}
	retained, err := a.store.GetHistoricalAgentProfile(t.Context(), created.Profile.ID)
	if err != nil || retained.SystemPrompt != "changed" {
		t.Fatal(retained, err)
	}
}

func apiHistoricalRetirementFixture(t *testing.T, a *testAPI, p *store.AgentProfile, _ any) (*service.AgentConfigResult, error) {
	t.Helper()
	if err := storetest.HistoricalProfile(t.Context(), a.store, p); err != nil {
		return nil, err
	}
	return &service.AgentConfigResult{Profile: p, Revision: p.Revision}, nil
}
