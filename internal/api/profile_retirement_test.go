package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

func TestProfileRetirementAPIRequiresPersistedExport(t *testing.T) {
	a, mux := newTestAPI(t)
	a.registerProfileRetirementRoutes(mux)
	p, err := a.Services.AgentConfig.Create(&store.AgentProfile{Name: "Test", Slug: "api-retirement", SystemPrompt: "private secret fixture"}, nil)
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
	a.registerProfileRetirementRoutes(mux)
	for _, p := range []*store.AgentProfile{{Name: "Builtin", Slug: "builtin-retirement", Source: "builtin", SystemPrompt: "x"}, {Name: "Plugin", Slug: "plugin-retirement", Source: "user", PluginID: "fixture-plugin", SystemPrompt: "x"}} {
		if err := a.store.CreateAgent(t.Context(), p); err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/agents/"+p.ID+"/retirement-export", nil))
		if w.Code != http.StatusForbidden {
			t.Fatal("protected source export accepted", w.Code)
		}
	}
}
