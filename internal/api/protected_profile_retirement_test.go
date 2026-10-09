package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

func TestProtectedProfileRetirementPublicRoutes(t *testing.T) {
	a, mux := newTestAPI(t)
	verified, err := service.EmbeddedDefinition()
	if err != nil {
		t.Fatal(err)
	}
	a.Services.CognitiveViews.Models = service.ModelAuthorizerFunc(func(context.Context, service.DefinitionRef, *service.ModelSelection) (service.ModelSelection, error) {
		return service.ModelSelection{Provider: "fixture-provider", Model: "fixture-model"}, nil
	})
	keep, err := a.Services.AgentConfig.ProvisionGeneralChat(t.Context(), a.Services.CognitiveViews, service.ProvisionGeneralChatRequest{Name: "General chat", Slug: "protected-keep-chat", DefinitionRef: verified.Ref})
	if err != nil {
		t.Fatal(err)
	}
	p := &store.AgentProfile{Name: "Protected", Slug: "protected-public", SystemPrompt: "private protected fixture secret", Source: "system"}
	if checkErr := a.store.CreateAgent(t.Context(), p); checkErr != nil {
		t.Fatal(checkErr)
	}
	req := service.ProtectedRetirementRequest{Acknowledge: true, Actor: "fixture-owner", Reason: "Keep chat only", KeepProfileID: keep.Agent.Profile.ID, KeepRevision: keep.Agent.Revision}
	call := func(route string, request any) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/agents/"+p.ID+route, bytes.NewReader(raw)))
		return w
	}
	if w := call("/protected-retirement-export", map[string]any{"actor": "fixture-owner", "grants": []string{"all"}}); w.Code != http.StatusBadRequest {
		t.Fatal("unknown authority input admitted", w.Code)
	}
	unacknowledged := req
	unacknowledged.Acknowledge = false
	if w := call("/protected-retirement-export", unacknowledged); w.Code != http.StatusForbidden {
		t.Fatal("protected authority implicit", w.Code)
	}
	export := call("/protected-retirement-export", req)
	if export.Code != http.StatusCreated {
		t.Fatal("production export route missing/refused", export.Code, export.Body.String())
	}
	if bytes.Contains(export.Body.Bytes(), []byte(p.SystemPrompt)) {
		t.Fatal("private export exposed")
	}
	var receipt service.ProtectedRetirementReceipt
	if checkErr := json.Unmarshal(export.Body.Bytes(), &receipt); checkErr != nil {
		t.Fatal(checkErr)
	}
	request := struct {
		service.ProtectedRetirementRequest
		ExportID string `json:"export_id"`
		Digest   string `json:"digest"`
	}{req, receipt.ExportID, receipt.Digest}
	request.Actor = "different-owner"
	if w := call("/protected-retire", request); w.Code != http.StatusConflict {
		t.Fatal("attribution replacement admitted", w.Code)
	}
	request.Actor = req.Actor
	if _, checkErr := a.store.DB.ExecContext(t.Context(), `UPDATE agent_profiles SET description='changed' WHERE id=?`, p.ID); checkErr != nil {
		t.Fatal(checkErr)
	}
	if w := call("/protected-retire", request); w.Code != http.StatusConflict {
		t.Fatal("stale protected export accepted", w.Code)
	}
	export = call("/protected-retirement-export", req)
	if export.Code != http.StatusCreated {
		t.Fatal(export.Code, export.Body.String())
	}
	if checkErr := json.Unmarshal(export.Body.Bytes(), &receipt); checkErr != nil {
		t.Fatal(checkErr)
	}
	request.ExportID = receipt.ExportID
	request.Digest = receipt.Digest
	w := call("/protected-retire", request)
	if w.Code != http.StatusOK {
		t.Fatal("protected retirement refused", w.Code, w.Body.String())
	}
	if checkErr := json.Unmarshal(w.Body.Bytes(), &receipt); checkErr != nil || !receipt.Retired || !receipt.ReceiptPersisted {
		t.Fatal("receipt missing", checkErr)
	}
	if _, checkErr := a.store.GetAgent(t.Context(), req.KeepProfileID); checkErr != nil {
		t.Fatal("retained logical identity lost", checkErr)
	}
}
