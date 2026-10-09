package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/nanite/internal/service"
)

func TestLogicalGeneralChatProvisionAPIRequiresExplicitVerifiedPin(t *testing.T) {
	a, mux := newTestAPI(t)
	verified, err := service.EmbeddedDefinition()
	if err != nil {
		t.Fatal(err)
	}
	a.Services.CognitiveViews.Models = service.ModelAuthorizerFunc(func(context.Context, service.DefinitionRef, *service.ModelSelection) (service.ModelSelection, error) {
		return service.ModelSelection{Provider: "fixture-provider", Model: "fixture-model"}, nil
	})
	request := service.ProvisionGeneralChatRequest{Name: "General chat", Slug: "api-general-chat", DefinitionRef: verified.Ref}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	call := func(body []byte) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/agents/provision/general-chat", bytes.NewReader(body)))
		return w
	}
	for _, bad := range [][]byte{[]byte(`{"name":"General","slug":"bad"}`), []byte(`{"name":"General","slug":"bad","grants":["all"]}`), append(append([]byte{}, raw...), []byte(` {}`)...)} {
		w := call(bad)
		if w.Code != http.StatusBadRequest {
			t.Fatal("invalid or authority-bearing input accepted", w.Code)
		}
	}
	w := call(raw)
	if w.Code != http.StatusCreated {
		t.Fatal("provisioning failed", w.Code, w.Body.String())
	}
	var result service.ProvisionGeneralChatResult
	if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Agent == nil || result.Agent.Profile.ID == "" || result.DefinitionRef != verified.Ref {
		t.Fatal("logical provisioning response missing identity/pin", err)
	}
	grants, err := a.store.ListAgentToolNames(t.Context(), result.Agent.Profile.ID)
	if err != nil || len(grants) != 0 {
		t.Fatal("provisioning request granted tools", err)
	}
	duplicate := call(raw)
	if duplicate.Code != http.StatusConflict {
		t.Fatal("logical provisioning replaced existing profile", duplicate.Code)
	}
}
