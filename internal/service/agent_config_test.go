package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	svcerr "github.com/hollis-labs/libs/util/svcerr"
	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

func newAgentConfigTestService(t *testing.T) (*AgentConfigService, *store.Store, string) {
	t.Helper()
	root := t.TempDir()
	st := newConfigTestStoreAt(t, filepath.Join(root, "agent-config.db"))
	return NewAgentConfigService(st, agent.NewClassification(), nil), st, root
}

func newConfigTestStore(t *testing.T) *store.Store {
	t.Helper()
	return newConfigTestStoreAt(t, filepath.Join(t.TempDir(), "config-test.db"))
}

func newConfigTestStoreAt(t *testing.T, path string) *store.Store {
	t.Helper()
	st, err := storetest.New(t, context.Background(), path)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	return st
}

// Raw private rows stand for retained history. They cannot author a fresh
// definition, enroll an actor or act as a production profile writer.
func immutableConfigHistoricalFixture(t *testing.T, st *store.Store, p store.AgentProfile) *store.AgentProfile {
	t.Helper()
	if err := storetest.HistoricalProfile(t.Context(), st, &p); err != nil {
		t.Fatal(err)
	}
	return &p
}

func immutableConfigSnapshot(t *testing.T, st *store.Store, query string) [][]any {
	t.Helper()
	rows, err := st.DB.QueryContext(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out [][]any
	for rows.Next() {
		cells := make([]any, len(cols))
		dest := make([]any, len(cols))
		for i := range cells {
			dest[i] = &cells[i]
		}
		if err = rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i, c := range cells {
			if b, ok := c.([]byte); ok {
				cells[i] = append([]byte(nil), b...)
			}
		}
		out = append(out, cells)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func immutableConfigUnchanged(t *testing.T, st *store.Store, query string, before [][]any) {
	t.Helper()
	if after := immutableConfigSnapshot(t, st, query); !reflect.DeepEqual(before, after) {
		t.Fatalf("retired operation changed %s: before=%v after=%v", query, before, after)
	}
}

func requireImmutableConfig(t *testing.T, result *AgentConfigResult, err error) {
	t.Helper()
	if !errors.Is(err, store.ErrImmutableAgentProfile) || result != nil {
		t.Fatalf("retired operation returned result=%+v err=%v, want immutable refusal", result, err)
	}
}

func TestImmutableAgentConfigRefusesValidCreateAndAssignmentSeeding(t *testing.T) {
	svc, st, root := newAgentConfigTestService(t)
	immutableConfigHistoricalFixture(t, st, store.AgentProfile{Name: "Retained", Slug: "retained-config-create", SystemPrompt: "Retained", Source: "user"})
	queries := []string{`SELECT * FROM agent_profiles ORDER BY id`, `SELECT * FROM agent_profile_revisions ORDER BY sequence`, `SELECT * FROM agent_procedures ORDER BY agent_id,name`, `SELECT * FROM agent_known_skills ORDER BY agent_id,skill_name`, `SELECT * FROM agent_tools ORDER BY agent_id,tool_id`, `SELECT * FROM actor_known_skills ORDER BY agent_id,skill_name`, `SELECT * FROM actor_granted_tools ORDER BY agent_id,tool_id`, `SELECT * FROM agent_host_settings ORDER BY id`, `SELECT * FROM agent_actor_bindings ORDER BY actor_uri`}
	before := make([][][]any, len(queries))
	for i, q := range queries {
		before[i] = immutableConfigSnapshot(t, st, q)
	}
	missingRole := "missing-role"
	for _, slug := range []string{"new-config-create", "retained-config-create"} {
		p := store.AgentProfile{Name: "Create refused", Slug: slug, SystemPrompt: "Never installed", RoleTools: `["*"]`, RoleSkills: `["kb-triage"]`}
		result, err := svc.Create(&p, []agent.ProcedureDefinition{{Name: "lint", Body: "Never seeded"}})
		requireImmutableConfig(t, result, err)
		result, err = svc.CreateWithAssignments(t.Context(), &p, []agent.ProcedureDefinition{{Name: "lint", Body: "Never seeded"}}, AgentAssignments{RoleID: &missingRole})
		requireImmutableConfig(t, result, err)
	}
	for i, q := range queries {
		immutableConfigUnchanged(t, st, q, before[i])
	}
	if _, err := os.Stat(filepath.Join(root, ".nanite", "agents")); !os.IsNotExist(err) {
		t.Fatalf("retired create wrote projection directory: %v", err)
	}
}

func TestImmutableAgentConfigUpdateDeleteAndCopyPreserveHistoricalDataWithoutNotification(t *testing.T) {
	_, st, root := newAgentConfigTestService(t)
	notifications := 0
	svc := NewAgentConfigService(st, agent.NewClassification(), func(string, string) { notifications++ })
	p := immutableConfigHistoricalFixture(t, st, store.AgentProfile{Name: "Retained", Slug: "retained-config-edit", SystemPrompt: "Private retained prompt", Source: "user"})
	sourcePath := filepath.Join(root, "historical.md")
	const sourceBody = "Private historical source bytes.\n"
	if err := os.WriteFile(sourcePath, []byte(sourceBody), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.ExecContext(t.Context(), `UPDATE agent_profiles SET source_ref=?,role_skills='["kb-triage"]',role_tools='["retained-tool"]',default_trust_tier='trusted' WHERE id=?`, sourcePath, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.ExecContext(t.Context(), `INSERT INTO agent_procedures(agent_id,name,body,scope,created_at,updated_at) VALUES(?,'lint','Retained SOP','shared','retained-created','retained-updated')`, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.ExecContext(t.Context(), `INSERT INTO agent_known_skills(agent_id,skill_name,pinned,approved_content_hash,granted_at,granted_by) VALUES(?,'kb-triage',1,'retained-hash','2026-09-16','historical-operator')`, p.ID); err != nil {
		t.Fatal(err)
	}
	var err error
	p, err = st.GetHistoricalAgentProfile(t.Context(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	plugin := immutableConfigHistoricalFixture(t, st, store.AgentProfile{Name: "Plugin", Slug: "retained-config-plugin", SystemPrompt: "Plugin retained prompt", Source: "plugin", PluginID: "private-fixture"})
	queries := []string{`SELECT * FROM agent_profiles ORDER BY id`, `SELECT * FROM agent_profile_revisions ORDER BY sequence`, `SELECT * FROM agent_procedures ORDER BY agent_id,name`, `SELECT * FROM agent_known_skills ORDER BY agent_id,skill_name`, `SELECT * FROM agent_host_settings ORDER BY id`, `SELECT * FROM agent_actor_bindings ORDER BY actor_uri`}
	before := make([][][]any, len(queries))
	for i, q := range queries {
		before[i] = immutableConfigSnapshot(t, st, q)
	}
	updated := *p
	updated.Slug = "renamed-refused"
	updated.SystemPrompt = "replacement"
	updated.SourceRef = "/must-not-be-written"
	updated.Description = "replacement"
	result, err := svc.Update(p, &updated, []agent.ProcedureDefinition{{Name: "replacement", Body: "Never seeded"}}, p.Revision)
	requireImmutableConfig(t, result, err)
	result, err = svc.UpdateWithAssignments(t.Context(), p, &updated, nil, p.Revision, AgentAssignments{})
	requireImmutableConfig(t, result, err)
	if err = svc.Delete(p); !errors.Is(err, store.ErrImmutableAgentProfile) {
		t.Fatalf("retired delete=%v", err)
	}
	for _, procedures := range [][]agent.ProcedureDefinition{nil, {{Name: "copied", Body: "Never copied"}}} {
		result, err = svc.CopyToManaged(plugin, procedures)
		requireImmutableConfig(t, result, err)
	}
	if notifications != 0 {
		t.Fatal("refused legacy operations emitted committed-write notifications", notifications)
	}
	for i, q := range queries {
		immutableConfigUnchanged(t, st, q, before[i])
	}
	data, err := os.ReadFile(sourcePath) //nolint:gosec // private fixture created above
	if err != nil || string(data) != sourceBody {
		t.Fatalf("retired operation changed source file: %q %v", data, err)
	}
}

func TestImmutableAgentConfigKeepsApplicableValidationAndOwnershipGuards(t *testing.T) {
	svc, st, _ := newAgentConfigTestService(t)
	const query = `SELECT * FROM agent_profiles ORDER BY id`
	before := immutableConfigSnapshot(t, st, query)
	for _, slug := range []string{"../evil", "a/b", "UPPER", "user"} {
		_, err := svc.Create(&store.AgentProfile{Name: "Invalid", Slug: slug, SystemPrompt: "x"}, nil)
		var typed *svcerr.Error
		if !errors.As(err, &typed) || typed.Code != svcerr.CodeInvalid || typed.Field != "slug" {
			t.Fatalf("invalid slug %q: %v", slug, err)
		}
	}
	for _, field := range []string{"class", "activation_mode", "default_state"} {
		p := &store.AgentProfile{Name: "Invalid", Slug: "invalid-behavior", SystemPrompt: "x"}
		switch field {
		case "class":
			p.Class = "bogus"
		case "activation_mode":
			p.ActivationMode = "bogus"
		case "default_state":
			p.DefaultState = "bogus"
		}
		_, err := svc.Create(p, nil)
		var typed *svcerr.Error
		if !errors.As(err, &typed) || typed.Code != svcerr.CodeInvalid || typed.Field != field {
			t.Fatalf("invalid %s: %v", field, err)
		}
	}
	immutableConfigUnchanged(t, st, query, before)
	p := immutableConfigHistoricalFixture(t, st, store.AgentProfile{Name: "Internal", Slug: "retained-config-internal", SystemPrompt: "Internal", Source: "internal"})
	before = immutableConfigSnapshot(t, st, query)
	updated := *p
	updated.Description = "refused"
	_, err := svc.Update(p, &updated, nil, "")
	if !errors.Is(err, ErrAgentNotManaged) || svcerr.CodeFor(err) != svcerr.CodePermission {
		t.Fatalf("internal update guard=%v", err)
	}
	if err = svc.Delete(p); !errors.Is(err, ErrAgentNotManaged) {
		t.Fatalf("internal deletion guard=%v", err)
	}
	if _, err = svc.CopyToManaged(p, nil); !errors.Is(err, ErrAgentNotManaged) {
		t.Fatalf("internal copy guard=%v", err)
	}
	immutableConfigUnchanged(t, st, query, before)
}
