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

func TestProtectedRetirementResidualPublicMuxKeepContract(t *testing.T) {
	a, mux := newTestAPI(t)
	verified, verifiedErr := service.EmbeddedDefinition()
	if verifiedErr != nil {
		t.Fatal(verifiedErr)
	}
	cfg, verifiedErr := service.MapChatDefinition(verified)
	if verifiedErr != nil {
		t.Fatal(verifiedErr)
	}
	keep := &store.AgentProfile{Name: "General Chat", Slug: "private-api-general-chat", SystemPrompt: cfg.Instructions}
	target := &store.AgentProfile{Name: "Protected", Slug: "private-api-protected", Source: "internal", SystemPrompt: "private archive-only body"}
	for _, p := range []*store.AgentProfile{keep, target} {
		if err := storetest.HistoricalProfile(t.Context(), a.store, p); err != nil {
			t.Fatal(err)
		}
	}
	settings, verifiedErr := json.Marshal(map[string]any{"provisioned_definition_ref": verified.Ref})
	if verifiedErr != nil {
		t.Fatal(verifiedErr)
	}
	if _, err := a.store.DB.ExecContext(t.Context(), `UPDATE agent_profiles SET settings=? WHERE id=?`, string(settings), keep.ID); err != nil {
		t.Fatal(err)
	}
	keep, verifiedErr = a.store.GetHistoricalAgentProfile(t.Context(), keep.ID)
	if verifiedErr != nil {
		t.Fatal(verifiedErr)
	}
	call := func(route string, request any) *httptest.ResponseRecorder {
		t.Helper()
		data, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, route, bytes.NewReader(data)))
		return recorder
	}
	path := "/api/agents/" + target.ID
	exported := call(path+"/retirement-export", map[string]any{"include_protected": true})
	if exported.Code != http.StatusCreated || bytes.Contains(exported.Body.Bytes(), []byte(target.SystemPrompt)) {
		t.Fatal("private export failed/leaked", exported.Code, exported.Body.String())
	}
	var receipt service.ProfileRetirementReceipt
	if err := json.Unmarshal(exported.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	request := map[string]any{"include_protected": true, "export_id": receipt.ExportID, "digest": receipt.Digest, "actor": "caller-selected audit text", "reason": "private fixture"}
	for _, invalid := range []string{"missing", "wrong-revision", "wrong-id", "caller-pin"} {
		request["general_chat_id"] = keep.ID
		request["general_chat_revision"] = keep.Revision
		want := http.StatusConflict
		switch invalid {
		case "missing":
			delete(request, "general_chat_revision")
		case "wrong-revision":
			request["general_chat_revision"] = "stale"
		case "wrong-id":
			request["general_chat_id"] = "fresh-or-unknown-setting-id"
		case "caller-pin":
			request["definition_ref"] = verified.Ref
			want = http.StatusBadRequest
		}
		result := call(path+"/retire", request)
		if result.Code != want {
			t.Fatal("public keep contract ignored", invalid, result.Code, result.Body.String())
		}
		delete(request, "definition_ref")
		if _, err := a.store.GetHistoricalAgentProfile(t.Context(), target.ID); err != nil {
			t.Fatal("refusal deleted target", err)
		}
		if row, err := a.store.GetRetiredAgentProfile(t.Context(), target.ID); err != nil || row != nil {
			t.Fatal("refusal wrote audit", row, err)
		}
	}
	request["general_chat_id"] = keep.ID
	request["general_chat_revision"] = keep.Revision
	retired := call(path+"/retire", request)
	if retired.Code != http.StatusOK {
		t.Fatal("protected retirement failed", retired.Code, retired.Body.String())
	}
	if _, err := a.store.GetHistoricalAgentProfile(t.Context(), keep.ID); err != nil {
		t.Fatal("kept row lost", err)
	}
	if row, err := a.store.GetRetiredAgentProfile(t.Context(), target.ID); err != nil || row == nil || row.Actor != "caller-selected audit text" {
		t.Fatal("audit label lost", row, err)
	}
	retry := call(path+"/retire", request)
	if retry.Code != http.StatusOK || !bytes.Equal(retired.Body.Bytes(), retry.Body.Bytes()) {
		t.Fatal("public retry lost committed receipt", retry.Code, retry.Body.String())
	}
}
