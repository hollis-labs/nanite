package service

import (
	"testing"

	svcerr "github.com/hollis-labs/libs/util/svcerr"
	"github.com/hollis-labs/nanite/internal/store"
)

func TestImmutableAgentRevisionRestorePreservesCurrentHistoricalAuthorityAndProvenance(t *testing.T) {
	svc, st, _ := newAgentConfigTestService(t)
	p := immutableConfigHistoricalFixture(t, st, store.AgentProfile{Name: "Original", Slug: "retained-restore", SystemPrompt: "Historical private prompt", Source: "user"})
	originalRevision := p.Revision
	toolID, err := st.UpsertKnownTool(t.Context(), "restore-tool", "builtin", "available", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB.ExecContext(t.Context(), `INSERT INTO roles(id,slug,name,system_prompt,created_at,updated_at) VALUES('retained-role','retained-role','Role','Retained role body','retained-created','retained-updated')`); err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB.ExecContext(t.Context(), `UPDATE agent_profiles SET role_id='retained-role',role_tools='["restore-tool"]',default_trust_tier='trusted',description='retained provenance',source_ref='/provenance/retained.md',imported_at='2026-10-08',origin_system='host-import',tether_urn='urn:tether:historical',system_prompt='Current retained prompt' WHERE id=?`, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB.ExecContext(t.Context(), `INSERT INTO agent_dispatch_tool_allowlist(agent_id,tool_id,created_at) VALUES(?,?,'retained-created')`, p.ID, toolID); err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB.ExecContext(t.Context(), `INSERT INTO agent_known_skills(agent_id,skill_name,pinned,approved_content_hash,granted_by,granted_at) VALUES(?,'retained-skill',1,'retained-content-hash','historical-operator','retained-granted-at')`, p.ID); err != nil {
		t.Fatal(err)
	}
	p, err = st.GetHistoricalAgentProfile(t.Context(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	queries := []string{`SELECT * FROM agent_profiles ORDER BY id`, `SELECT * FROM agent_profile_revisions ORDER BY sequence`, `SELECT * FROM roles ORDER BY id`, `SELECT * FROM agent_tools ORDER BY agent_id,tool_id`, `SELECT * FROM agent_dispatch_tool_allowlist ORDER BY agent_id,tool_id`, `SELECT * FROM agent_known_skills ORDER BY agent_id,skill_name`, `SELECT * FROM agent_host_settings ORDER BY id`, `SELECT * FROM agent_actor_bindings ORDER BY actor_uri`, `SELECT * FROM actor_granted_tools ORDER BY agent_id,tool_id`}
	before := make([][][]any, len(queries))
	for i, q := range queries {
		before[i] = immutableConfigSnapshot(t, st, q)
	}
	for _, revision := range []string{p.Revision, "stale", ""} {
		result, restoreErr := svc.RestoreRevision(t.Context(), p.ID, originalRevision, revision)
		requireImmutableConfig(t, result, restoreErr)
	}
	for i, q := range queries {
		immutableConfigUnchanged(t, st, q, before[i])
	}
	rows, err := svc.ListRevisions(t.Context(), p.ID, 100, 0)
	if err != nil || len(rows) != 2 || rows[0].ID != p.Revision || rows[1].ID != originalRevision || rows[1].Profile.SystemPrompt != "Historical private prompt" || rows[0].Profile.SystemPrompt != p.SystemPrompt {
		t.Fatalf("read-only historical revisions lost data: %+v %v", rows, err)
	}
}

func TestImmutableAgentRevisionRestoreRefusesOwnedForeignAndMissingHistory(t *testing.T) {
	svc, st, _ := newAgentConfigTestService(t)
	first := immutableConfigHistoricalFixture(t, st, store.AgentProfile{Name: "First", Slug: "first-history", SystemPrompt: "First", Source: "user"})
	for _, source := range []string{"user", "managed_file", "internal", "builtin", "plugin", "system", "import"} {
		p := immutableConfigHistoricalFixture(t, st, store.AgentProfile{Name: "History", Slug: "history-" + source, SystemPrompt: "Retained", Source: source})
		const query = `SELECT * FROM agent_profile_revisions ORDER BY sequence`
		before := immutableConfigSnapshot(t, st, query)
		for _, historicalID := range []string{p.Revision, first.Revision, "missing-history"} {
			result, err := svc.RestoreRevision(t.Context(), p.ID, historicalID, p.Revision)
			requireImmutableConfig(t, result, err)
		}
		immutableConfigUnchanged(t, st, query, before)
		retained, err := st.GetHistoricalAgentProfile(t.Context(), p.ID)
		if err != nil || retained.Revision != p.Revision || retained.SystemPrompt != p.SystemPrompt {
			t.Fatalf("refused restore changed owned target: %+v %v", retained, err)
		}
	}
	result, err := svc.RestoreRevision(t.Context(), "missing-profile", first.Revision, "")
	requireImmutableConfig(t, result, err)
	if _, err = svc.ListRevisions(t.Context(), "missing-profile", 50, 0); svcerr.CodeFor(err) != svcerr.CodeNotFound {
		t.Fatalf("missing historical read=%v", err)
	}
}

func TestImmutableAgentRevisionKeepsLostPromptHistoryWithoutRestoringIt(t *testing.T) {
	svc, st, _ := newAgentConfigTestService(t)
	p := immutableConfigHistoricalFixture(t, st, store.AgentProfile{Name: "Recover", Slug: "lost-historical-prompt", SystemPrompt: "Historical instructions", Source: "user"})
	if _, err := st.DB.ExecContext(t.Context(), `UPDATE agent_profiles SET system_prompt='' WHERE id=?`, p.ID); err != nil {
		t.Fatal(err)
	}
	lost, err := st.GetHistoricalAgentProfile(t.Context(), p.ID)
	if err != nil || lost.SystemPrompt != "" || lost.Revision == p.Revision {
		t.Fatalf("lost-prompt history fixture=%+v %v", lost, err)
	}
	queries := []string{`SELECT * FROM agent_profiles ORDER BY id`, `SELECT * FROM agent_profile_revisions ORDER BY sequence`}
	before := make([][][]any, len(queries))
	for i, q := range queries {
		before[i] = immutableConfigSnapshot(t, st, q)
	}
	result, err := svc.RestoreRevision(t.Context(), p.ID, p.Revision, lost.Revision)
	requireImmutableConfig(t, result, err)
	for i, q := range queries {
		immutableConfigUnchanged(t, st, q, before[i])
	}
	original, err := st.GetAgentRevision(t.Context(), p.ID, p.Revision)
	if err != nil || original.Profile.SystemPrompt != "Historical instructions" {
		t.Fatalf("original prompt missing from historical export data: %+v %v", original, err)
	}
	retained, err := st.GetAgentRevision(t.Context(), p.ID, lost.Revision)
	if err != nil || retained.Profile.SystemPrompt != "" {
		t.Fatalf("lost-prompt event missing from historical data: %+v %v", retained, err)
	}
}
