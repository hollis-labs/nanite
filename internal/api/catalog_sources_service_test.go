package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/nanite/internal/service"
)

func TestCatalogSourcesHTTP_ServiceCRUD(t *testing.T) {
	cs, _ := setupCatalogTestState(t)
	call := func(method, path, body, id string, handler http.HandlerFunc) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.SetPathValue("id", id)
		rr := httptest.NewRecorder()
		handler(rr, req)
		return rr
	}
	rr := call(http.MethodPost, "/", `{"name":"Probe","url":"https://catalog.example/probe","priority":77}`, "", cs.handleAddSource)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}
	var created CatalogSourceView
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Type != "custom" || !created.Enabled || created.Priority != 77 || created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("wire: %+v", created)
	}
	rr = call(http.MethodPut, "/", `{"enabled":false,"priority":0}`, created.ID, cs.handleUpdateSource)
	if rr.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rr.Code, rr.Body.String())
	}
	row, err := cs.store.GetCatalogSource(t.Context(), created.ID)
	if err != nil || row.Name != "Probe" || row.URL != "https://catalog.example/probe" || row.Enabled || row.Priority != 0 {
		t.Fatalf("patch state: %+v %v", row, err)
	}
	inputs, err := cs.sources.FetchInputs(t.Context())
	if err != nil || len(inputs) != 1 || inputs[0].URL != row.URL || inputs[0].Enabled {
		t.Fatalf("fetch inputs: %+v %v", inputs, err)
	}
	rr = call(http.MethodPut, "/", `{}`, "missing", cs.handleUpdateSource)
	if rr.Code != http.StatusNotFound || rr.Body.String() != `{"error":"source not found"}`+"\n" {
		t.Fatalf("missing: %d %s", rr.Code, rr.Body.String())
	}
	rr = call(http.MethodPost, "/", `{"name":"Duplicate","url":"https://catalog.example/probe"}`, "", cs.handleAddSource)
	if rr.Code != http.StatusConflict {
		t.Fatalf("duplicate: %d %s", rr.Code, rr.Body.String())
	}
	rr = call(http.MethodDelete, "/", "", created.ID, cs.handleDeleteSource)
	if rr.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rr.Code, rr.Body.String())
	}
	if _, err := cs.store.GetCatalogSource(t.Context(), created.ID); err == nil {
		t.Fatal("source survived delete")
	}
	// The missing-row wrapper remains inspectable without leaking store access.
	if err := cs.sources.Patch(t.Context(), "missing", service.CatalogSourcePatch{}); err == nil {
		t.Fatal("missing source accepted")
	}
}
