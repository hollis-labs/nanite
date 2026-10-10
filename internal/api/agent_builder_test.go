package api

import "testing"

func TestRetiredAgentBuilderPublicMuxCannotAuthorOrLaunch(t *testing.T) {
	a, mux := newTestAPI(t)
	retiredAPIHistoricalProfile(t, a, "historical-builder-user", "user")
	retiredAPIHistoricalProfile(t, a, "historical-builder-internal", "internal")
	queries := []string{`SELECT * FROM agent_profiles ORDER BY id`, `SELECT * FROM agent_profile_revisions ORDER BY sequence`, `SELECT * FROM agent_definitions ORDER BY definition_id,revision`, `SELECT * FROM agent_host_settings ORDER BY id`, `SELECT * FROM agent_actor_bindings ORDER BY actor_uri`, `SELECT * FROM durable_agent_instances ORDER BY id`, `SELECT * FROM sessions ORDER BY id`}
	before := make([][][]any, len(queries))
	for i, q := range queries {
		before[i] = retiredAPISnapshot(t, a, q)
	}
	for _, endpoint := range []string{"dry-run", "draft", "review"} {
		for _, body := range []string{
			`{"schema_version":"1","mode":"create_profile_and_instance","profile":{"name":"New","slug":"new-builder","system_prompt":"new"},"durable_instance":{"create":true,"start":true,"recipe_id":"project-advisor"},"operator_notification":{"target_kind":"operator","target_id":"fixture"}}`,
			`{"mode":"update_profile","profile":{"id":"historical-builder-user","system_prompt":"changed","role_tools":"[\"all\"]"}}`,
			`{"mode":"update_profile","profile":{"id":"historical-builder-internal","system_prompt":"changed"}}`,
			`{"intake_text":"Create a watcher","requested_lifecycle_class":"process","actor_uri":"urn:claimed"}`,
			`{"current_draft":{"profile":{"slug":"claimed","can_execute":true}}}`, "not json", "null", "",
		} {
			requireRetiredAPI(t, retiredAPIRequest(t, mux, "POST", "/api/agent-builder/"+endpoint, body))
			for i, q := range queries {
				retiredAPIHistoryUnchanged(t, a, q, before[i])
			}
		}
	}
}
