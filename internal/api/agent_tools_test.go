package api

import (
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// A private historical profile supplies audited data, not an actor or an
// identity produced by the retired HTTP profile/grant writers.
func historicalToolGrantFixture(t *testing.T, a *testAPI, slug string) store.AgentProfile {
	t.Helper()
	retiredAPIHistoricalProfile(t, a, slug, "user")
	p, err := a.store.GetHistoricalAgentProfile(t.Context(), slug)
	if err != nil {
		t.Fatal(err)
	}
	return *p
}

func TestRetiredAgentToolGrantPublicMuxCannotIssueRevokeOrReplayHistoricalAuthority(t *testing.T) {
	a, mux := newTestAPI(t)
	p := historicalToolGrantFixture(t, a, "retained-tool-grant-api")
	host := capabilityHostWithoutActor(t, a, "tool-grant-api-host", "user")
	toolID, err := a.store.UpsertKnownTool(t.Context(), "dev_read", "builtin", "available", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.DB.ExecContext(t.Context(), `UPDATE agent_profiles SET tools='["*"]',role_tools='["dev_read"]',tool_permissions='{"deny_list":["dev_*"]}' WHERE id=?`, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.DB.ExecContext(t.Context(), `INSERT INTO agent_tools(agent_id,tool_id,granted_via,created_at) VALUES(?,?,'historical-explicit','retained-created')`, p.ID, toolID); err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.DB.ExecContext(t.Context(), `INSERT INTO agent_dispatch_tool_allowlist(agent_id,tool_id,created_at) VALUES(?,?,'retained-created')`, p.ID, toolID); err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.DB.ExecContext(t.Context(), `INSERT INTO agent_tools_legacy_backfill(agent_id,created_at) VALUES(?,'retained-created')`, p.ID); err != nil {
		t.Fatal(err)
	}
	queries := []string{`SELECT * FROM agent_profiles ORDER BY id`, `SELECT * FROM agent_profile_revisions ORDER BY sequence`, `SELECT * FROM agent_tools ORDER BY agent_id,tool_id`, `SELECT * FROM agent_dispatch_tool_allowlist ORDER BY agent_id,tool_id`, `SELECT * FROM agent_tools_legacy_backfill ORDER BY agent_id`, `SELECT * FROM actor_granted_tools ORDER BY agent_id,tool_id`, `SELECT * FROM actor_dispatch_tool_allowlist ORDER BY agent_id,tool_id`, `SELECT * FROM agent_actor_bindings ORDER BY actor_uri`, `SELECT * FROM agent_host_settings ORDER BY id`}
	before := make([][][]any, len(queries))
	for i, q := range queries {
		before[i] = retiredAPISnapshot(t, a, q)
	}
	for _, id := range []string{p.ID, host.ID, "missing-agent", "urn:tether:claimed-agent"} {
		base := "/api/agents/" + id + "/tools"
		for _, body := range []string{`{"tool_id":"` + toolID + `"}`, `{"tool_id":"` + toolID + `","granted_via":"explicit","actor_uri":"urn:tether:claimed-agent"}`, `{"tool_id":"missing-tool"}`, `{}`, "not json", "null"} {
			t.Run(id+"/"+body, func(t *testing.T) {
				requireRetiredAPI(t, retiredAPIRequest(t, mux, "POST", base, body))
				for i, q := range queries {
					retiredAPIHistoryUnchanged(t, a, q, before[i])
				}
			})
		}
		for _, tool := range []string{toolID, "missing-tool"} {
			t.Run(id+"/revoke/"+tool, func(t *testing.T) {
				requireRetiredAPI(t, retiredAPIRequest(t, mux, "DELETE", base+"/"+tool, ""))
				for i, q := range queries {
					retiredAPIHistoryUnchanged(t, a, q, before[i])
				}
			})
		}
	}
}

func TestRetiredAgentToolProfilePublicMuxCannotMintOrMutateHistoricalGrantTarget(t *testing.T) {
	a, mux := newTestAPI(t)
	p := historicalToolGrantFixture(t, a, "retained-profile-grant-target")
	queries := []string{`SELECT * FROM agent_profiles ORDER BY id`, `SELECT * FROM agent_profile_revisions ORDER BY sequence`, `SELECT * FROM agent_host_settings ORDER BY id`, `SELECT * FROM agent_actor_bindings ORDER BY actor_uri`, `SELECT * FROM actor_granted_tools ORDER BY agent_id,tool_id`}
	before := make([][][]any, len(queries))
	for i, q := range queries {
		before[i] = retiredAPISnapshot(t, a, q)
	}
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/api/agents", `{"name":"Grant target","slug":"claimed-target","system_prompt":"new","tools":"[\"*\"]","tether_urn":"urn:claimed"}`},
		{"POST", "/api/agents", "not json"}, {"PUT", "/api/agents/" + p.ID, `{"tools":"[\"*\"]","tool_permissions":"{}"}`},
		{"DELETE", "/api/agents/" + p.ID, ""}, {"POST", "/api/agents/" + p.ID + "/copy-to-managed", `{}`},
	} {
		requireRetiredAPI(t, retiredAPIRequest(t, mux, c.method, c.path, c.body))
		for i, q := range queries {
			retiredAPIHistoryUnchanged(t, a, q, before[i])
		}
	}
}
