package service

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	agentpkg "github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/agentimport"
	"github.com/hollis-labs/nanite/internal/store"
)

const boundaryDef = `---
name: Imported Reviewer
slug: imported-reviewer
roleTools:
  - dev_bash
procedures:
  - name: triage
    body: How to triage.
---
Imported system prompt.
`

func TestImmutableImportedAgentOwnershipPreservesHistoryAndSourceWithoutManagedCopy(t *testing.T) {
	svc, st, root := newAgentConfigTestService(t)
	path := filepath.Join(root, "imported-reviewer.md")
	if err := os.WriteFile(path, []byte(boundaryDef), 0600); err != nil {
		t.Fatal(err)
	}
	p := immutableConfigHistoricalFixture(t, st, store.AgentProfile{Name: "Imported Reviewer", Slug: "imported-reviewer", SystemPrompt: "Imported system prompt.", Source: agentimport.SourceProvenance})
	if _, err := st.DB.ExecContext(t.Context(), `UPDATE agent_profiles SET source_ref=?,imported_at='retained-import-time',origin_system='retained-ecosystem' WHERE id=?`, path, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.ExecContext(t.Context(), `INSERT INTO agent_procedures(agent_id,name,body) VALUES(?,'triage','Retained SOP')`, p.ID); err != nil {
		t.Fatal(err)
	}
	var err error
	p, err = st.GetHistoricalAgentProfile(t.Context(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if class := svc.Classify(p); class != agentpkg.ManageClassExternal {
		t.Fatalf("historical imported ownership=%v", class)
	}
	queries := []string{`SELECT * FROM agent_profiles ORDER BY id`, `SELECT * FROM agent_profile_revisions ORDER BY sequence`, `SELECT * FROM agent_procedures ORDER BY agent_id,name`, `SELECT * FROM agent_host_settings ORDER BY id`, `SELECT * FROM agent_actor_bindings ORDER BY actor_uri`}
	before := make([][][]any, len(queries))
	for i, q := range queries {
		before[i] = immutableConfigSnapshot(t, st, q)
	}
	updated := *p
	updated.SystemPrompt = "Refused edit"
	if _, err = svc.Update(p, &updated, nil, p.Revision); !errors.Is(err, ErrAgentNotManaged) {
		t.Fatalf("historical imported update=%v", err)
	}
	if err = svc.Delete(p); !errors.Is(err, ErrAgentNotManaged) {
		t.Fatalf("historical imported delete=%v", err)
	}
	for range 2 {
		for _, procedures := range [][]agentpkg.ProcedureDefinition{nil, {{Name: "triage", Body: "Never copied"}}} {
			result, copyErr := svc.CopyToManaged(p, procedures)
			requireImmutableConfig(t, result, copyErr)
		}
	}
	for i, q := range queries {
		immutableConfigUnchanged(t, st, q, before[i])
	}
	data, err := os.ReadFile(path) //nolint:gosec // private fixture created above
	if err != nil || string(data) != boundaryDef {
		t.Fatalf("ownership operations changed source bytes: %q %v", data, err)
	}
}

func TestImmutableImportedOwnershipCannotBeOverriddenBySourcePathOrPluginLabel(t *testing.T) {
	svc, st, _ := newAgentConfigTestService(t)
	for _, c := range []struct {
		source, plugin string
		class          agentpkg.ManageClass
		copyErr        error
	}{
		{"import", "", agentpkg.ManageClassExternal, store.ErrImmutableAgentProfile},
		{"plugin", "", agentpkg.ManageClassPlugin, store.ErrImmutableAgentProfile},
		{"adapter", "", agentpkg.ManageClassExternal, store.ErrImmutableAgentProfile},
		{"internal", "", agentpkg.ManageClassInternal, ErrAgentNotManaged},
		{"user", "private-plugin", agentpkg.ManageClassPlugin, store.ErrImmutableAgentProfile},
	} {
		p := immutableConfigHistoricalFixture(t, st, store.AgentProfile{Name: "Retained", Slug: "ownership-" + c.source, SystemPrompt: "Retained body", Source: c.source, PluginID: c.plugin})
		p.SourceRef = "/internal-looking/path/that-cannot-grant-authority.md"
		if class := svc.Classify(p); class != c.class {
			t.Fatalf("classification source=%s plugin=%s: %v", c.source, c.plugin, class)
		}
		const query = `SELECT * FROM agent_profiles ORDER BY id`
		before := immutableConfigSnapshot(t, st, query)
		updated := *p
		updated.Description = "Refused"
		if _, err := svc.Update(p, &updated, nil, ""); !errors.Is(err, ErrAgentNotManaged) {
			t.Fatalf("protected historical update=%v", err)
		}
		result, err := svc.CopyToManaged(p, nil)
		if result != nil || !errors.Is(err, c.copyErr) {
			t.Fatalf("source path/plugin label restored copy authority: %+v %v", result, err)
		}
		immutableConfigUnchanged(t, st, query, before)
	}
}
