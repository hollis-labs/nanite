package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

var contextResolverViewKeys = []string{
	"id", "agent_id", "slot_name", "kind", "run", "cwd", "timeout", "url",
	"headers_json", "response_format", "json_path", "enabled", "created_at", "updated_at",
}

func TestContextResolverViewJSON(t *testing.T) {
	var r store.AgentContextResolver
	populate(t, &r)
	assertKeys(t, "ContextResolverView", mustJSON(t, contextResolverToView(&r)), contextResolverViewKeys)
	assertSameJSON(t, "populated", contextResolverToView(&r), r)
	assertSameJSON(t, "zero", contextResolverToView(&store.AgentContextResolver{}), store.AgentContextResolver{})
	assertSameJSON(t, "empty list", contextResolversToView([]store.AgentContextResolver{}), []store.AgentContextResolver{})
	assertSameJSON(t, "nil list", contextResolversToView(nil), []store.AgentContextResolver(nil))
}

func resolverAgents(t *testing.T, a *testAPI) (*store.AgentProfile, *store.AgentProfile) {
	t.Helper()
	agentA := &store.AgentProfile{Name: "Resolver A", Slug: "b3a-resolver-a", SystemPrompt: "x", Class: "advisor"}
	agentB := &store.AgentProfile{Name: "Resolver B", Slug: "b3a-resolver-b", SystemPrompt: "x", Class: "advisor"}
	for _, ag := range []*store.AgentProfile{agentA, agentB} {
		if err := a.store.CreateAgent(context.Background(), ag); err != nil {
			t.Fatalf("CreateAgent: %v", err)
		}
	}
	return agentA, agentB
}

func TestContextResolvers_StatusAndErrorBodies(t *testing.T) {
	a, mux := newTestAPI(t)
	agentA, agentB := resolverAgents(t, a)
	baseA := "/api/agents/" + agentA.ID + "/context-resolvers"
	baseB := "/api/agents/" + agentB.ID + "/context-resolvers"

	if w := mcpDo(mux, "GET", baseA, ""); w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("empty list: %d %s", w.Code, w.Body.String())
	}
	if w := mcpDo(mux, "POST", baseA, `{"slot_name":"s","kind":"bogus"}`); w.Code != http.StatusBadRequest ||
		errorBody(t, w) != `agent_context_resolvers: kind "bogus" invalid: must be 'cmd' or 'http'` {
		t.Fatalf("create invalid: %d %s", w.Code, w.Body.String())
	}
	w := mcpDo(mux, "POST", baseA, `{"slot_name":"weather","kind":"cmd","run":"printf hi","cwd":"/tmp","timeout":"5s"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created ContextResolverView
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || created.ID == "" || !created.Enabled {
		t.Fatalf("create body %s: %v", w.Body.String(), err)
	}

	for _, c := range []struct {
		name, method, path, body string
		code                     int
		msg                      string
	}{
		{"get unknown", "GET", baseA + "/nope", "", 404, "context resolver not found"},
		{"get other agent's is 404", "GET", baseB + "/" + created.ID, "", 404, "context resolver not found"},
		{"patch other agent's is 400", "PATCH", baseB + "/" + created.ID, `{"run":"x"}`, 400, "cannot patch a different agent's context resolver through this endpoint"},
		{"patch other agent's beats bad body", "PATCH", baseB + "/" + created.ID, `not json`, 400, "cannot patch a different agent's context resolver through this endpoint"},
		{"delete other agent's is 400", "DELETE", baseB + "/" + created.ID, "", 400, "cannot delete a different agent's context resolver through this endpoint"},
		{"patch unknown", "PATCH", baseA + "/nope", `{}`, 404, "context resolver not found"},
		{"patch invalid", "PATCH", baseA + "/" + created.ID, `{"kind":"http"}`, 400, `agent_context_resolvers: kind "http" requires url`},
		{"delete unknown", "DELETE", baseA + "/nope", "", 404, "context resolver not found"},
	} {
		rec := mcpDo(mux, c.method, c.path, c.body)
		if rec.Code != c.code || errorBody(t, rec) != c.msg {
			t.Fatalf("%s: %d %s, want %d %q", c.name, rec.Code, rec.Body.String(), c.code, c.msg)
		}
	}

	// PATCH carries every field it does not send.
	w = mcpDo(mux, "PATCH", baseA+"/"+created.ID, `{"run":"printf bye","enabled":false}`)
	var patched ContextResolverView
	if err := json.Unmarshal(w.Body.Bytes(), &patched); err != nil || w.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", w.Code, w.Body.String())
	}
	if patched.Run != "printf bye" || patched.Enabled || patched.CWD != "/tmp" || patched.Timeout != "5s" || patched.SlotName != "weather" {
		t.Fatalf("patched = %+v", patched)
	}

	w = mcpDo(mux, "DELETE", baseA+"/"+created.ID, "")
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"id":"`+created.ID+`","status":"deleted"}` {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
}

const resolverHeaderSecret = "rsv_notarealtokenbutlongenough" // #nosec G101 -- fake token, not a credential

// TestContextResolvers_HeadersReturnedUnredacted_CurrentBehaviour_PendingCW20260930_0186
// pins that an http resolver's headers_json is returned as stored by
// create, list, get and patch. CW-20260930-0186 decides whether it should
// be redacted; whoever changes that flips this test.
func TestContextResolvers_HeadersReturnedUnredacted_CurrentBehaviour_PendingCW20260930_0186(t *testing.T) {
	a, mux := newTestAPI(t)
	agentA, _ := resolverAgents(t, a)
	base := "/api/agents/" + agentA.ID + "/context-resolvers"
	body := `{"slot_name":"api","kind":"http","url":"http://127.0.0.1:1/x","headers_json":"{\"Authorization\":\"Bearer ` + resolverHeaderSecret + `\"}"}`
	w := mcpDo(mux, "POST", base, body)
	var created ContextResolverView
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	for _, c := range []struct{ name, method, path, body string }{
		{"create", "", "", ""},
		{"list", "GET", base, ""},
		{"get", "GET", base + "/" + created.ID, ""},
		{"patch", "PATCH", base + "/" + created.ID, `{"slot_name":"api2"}`},
	} {
		got := w.Body.String()
		if c.method != "" {
			got = mcpDo(mux, c.method, c.path, c.body).Body.String()
		}
		if !strings.Contains(got, resolverHeaderSecret) {
			t.Errorf("%s: header value no longer returned; if CW-20260930-0186 landed, flip this test: %s", c.name, got)
		}
	}
}
