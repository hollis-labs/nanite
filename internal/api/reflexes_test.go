package api

import "testing"

func TestRetiredReflexPublicMuxCannotMutateExecuteOrReviewHistoricalRules(t *testing.T) {
	a, mux := newTestAPI(t)
	const owner = "historical-reflex-api"
	retiredAPIHistoricalProfile(t, a, owner, "user")
	if _, err := a.store.DB.ExecContext(t.Context(), `INSERT INTO agent_reflexes(id,agent_id,name,trigger_kind,trigger_spec,action_kind,action_spec,status,priority,fired_count,last_fired_at,created_by,opt_out_allowed,provenance_tier,recurrence_override_seconds) VALUES('historical-reflex',?,'edited','event','{"name":"probe"}','inject_reminder','{"body":"private retained rule"}','paused',77,9,'2026-10-01','operator',1,'operator',300)`, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.DB.ExecContext(t.Context(), `INSERT INTO agent_reflex_opt_outs(agent_id,reflex_id) VALUES(?,'historical-reflex')`, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.DB.ExecContext(t.Context(), `INSERT INTO pending_reflexes(id,proposed_by,target_agent_id,name,trigger_kind,trigger_spec,action_kind,action_spec,rationale) VALUES('historical-pending','private-fixture',?,'pending','event','{"name":"probe"}','inject_reminder','{"body":"private pending"}','retained rationale')`, owner); err != nil {
		t.Fatal(err)
	}
	queries := []string{`SELECT * FROM agent_reflexes ORDER BY id`, `SELECT * FROM agent_reflex_opt_outs ORDER BY agent_id,reflex_id`, `SELECT * FROM pending_reflexes ORDER BY id`, `SELECT * FROM plugin_reflex_seed_bindings ORDER BY plugin_id,seed_id,agent_id`, `SELECT * FROM actor_reflex_state ORDER BY actor_uri,bundle_digest,rule_id`}
	before := make([][][]any, len(queries))
	for i, q := range queries {
		before[i] = retiredAPISnapshot(t, a, q)
	}
	for _, agentID := range []string{owner, "missing-agent", "file-default"} {
		base := "/api/agents/" + agentID + "/reflexes"
		for _, c := range []struct{ method, path, body string }{
			{"GET", base, ""}, {"POST", base, `{"name":"new","trigger_kind":"event","trigger_spec":"{}","action_kind":"inject_reminder","action_spec":"{}","recurrence_override_seconds":300}`},
			{"PATCH", base + "/historical-reflex", `{"priority":9,"recurrence_override_seconds":600}`},
			{"PATCH", base + "/historical-reflex", `{"recurrence_override_seconds":0}`},
			{"PATCH", base + "/historical-reflex", "not json"}, {"DELETE", base + "/historical-reflex", ""},
			{"POST", base + "/historical-reflex/opt-out", ""}, {"DELETE", base + "/historical-reflex/opt-out", ""},
		} {
			requireRetiredAPI(t, retiredAPIRequest(t, mux, c.method, c.path, c.body))
		}
	}
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/pending/reflexes?status=pending", ""},
		{"POST", "/api/pending/reflexes/historical-pending/approve", `{"reviewed_by":"operator"}`},
		{"POST", "/api/pending/reflexes/historical-pending/reject", `{"reviewed_by":"operator","reason":"changed"}`},
		{"POST", "/api/pending/reflexes/missing/approve", "not json"},
		{"POST", "/api/reflexes/validate", `{"trigger_kind":"predicate","trigger_spec":"{}","action_kind":"dispatch_to_agent","action_spec":"{}","state":{"tool_calls":0}}`},
		{"POST", "/api/reflexes/validate", "not json"},
	} {
		requireRetiredAPI(t, retiredAPIRequest(t, mux, c.method, c.path, c.body))
	}
	for i, q := range queries {
		retiredAPIHistoryUnchanged(t, a, q, before[i])
	}
}
