package api

import (
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

const resolverHeaderSecret = "rsv_privatefixtureonly" // #nosec G101 -- fake test token

func TestRetiredContextResolverPublicMuxDoesNotExposeHistoricalHeaders(t *testing.T) {
	a, mux := newTestAPI(t)
	const owner = "historical-header-owner"
	retiredAPIHistoricalProfile(t, a, owner, "plugin")
	headers := `{"Authorization":"Bearer ` + resolverHeaderSecret + `"}`
	if _, err := a.store.DB.ExecContext(t.Context(), `INSERT INTO agent_context_resolvers(id,agent_id,slot_name,kind,url,headers_json,response_format,enabled,created_at,updated_at) VALUES('historical-headers',?,'api','http','https://example.invalid/private',?,'json',1,'retained-created','retained-updated')`, owner, headers); err != nil {
		t.Fatal(err)
	}
	const query = `SELECT * FROM agent_context_resolvers ORDER BY id`
	before := retiredAPISnapshot(t, a, query)
	base := "/api/agents/" + owner + "/context-resolvers"
	for _, c := range []struct{ method, path, body string }{
		{"GET", base, ""}, {"GET", base + "/historical-headers", ""},
		{"POST", base, `{"slot_name":"api","kind":"http","url":"https://example.invalid","headers_json":"private"}`},
		{"PATCH", base + "/historical-headers", `{"slot_name":"changed"}`},
	} {
		w := retiredAPIRequest(t, mux, c.method, c.path, c.body)
		requireRetiredAPI(t, w)
		if strings.Contains(w.Body.String(), resolverHeaderSecret) {
			t.Fatal("historical header exposed through retired surface")
		}
	}
	retiredAPIHistoryUnchanged(t, a, query, before)
}
