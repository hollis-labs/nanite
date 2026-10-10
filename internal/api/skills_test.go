package api

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

// The old profile UUID mutation facade cannot issue or revoke actor authority.
// Refusal applies before parsing or effects, including claims of a valid actor.
func TestRetiredAgentSkillMutationsPreserveAuthorityAndHistory(t *testing.T) {
	a, mux := newTestAPI(t)
	history := retiredAgentHistory(t, a, "user")
	actor := &store.AgentProfile{Name: "Prior skill actor", Slug: "prior-skill-actor"}
	if err := storetest.PriorAuthorizedActor(t.Context(), a.store, actor); err != nil {
		t.Fatal(err)
	}
	before := retiredAgentState(t, a)
	for _, id := range []string{history.ID, actor.ID, "missing"} {
		for _, req := range []struct{ method, suffix, body string }{
			{"POST", "/skills", `{"skill_id":"claimed-skill"}`},
			{"DELETE", "/skills/claimed-skill", ""},
			{"POST", "/skills/claimed-skill/grant", `{"granted_by":"operator","capabilities":{"network":{"allow":true}}}`},
			{"DELETE", "/skills/claimed-skill/grant", ""},
			{"POST", "/skills/claimed-skill/grant", "{"},
		} {
			retiredAgentRequest(t, mux, req.method, "/api/agents/"+url.PathEscape(id)+req.suffix, req.body)
			assertRetiredAgentState(t, a, before)
		}
	}
}

func errorBody(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", w.Body.String(), err)
	}
	return body["error"]
}
