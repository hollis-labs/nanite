package agentimport

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := storetest.New(t, context.Background(), filepath.Join(t.TempDir(), "agentimport.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return s
}

// writeDef writes a Nanite agent markdown file into a fresh temp dir and
// returns its path.
func writeDef(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

const reviewerV1 = `---
name: Reviewer
slug: imported-reviewer
description: An imported reviewer
---
imported prompt v1
`

// TestImportProvenanceIsExternal pins the mapping the whole ownership model
// rests on. Classify's `managed` case lists "cli", "nanite", "project",
// "user" and the empty string; if SourceProvenance ever drifts into that
// list, every imported agent silently becomes editable in place. This test
// asserts the mapping directly rather than trusting that the default branch
// keeps catching it.
func TestImportProvenanceIsExternal(t *testing.T) {
	class := agent.NewClassification().Classify(SourceProvenance)
	if class != agent.ManageClassExternal {
		t.Fatalf("Classify(%q) = %q, want %q — an imported agent must never be editable in place",
			SourceProvenance, class, agent.ManageClassExternal)
	}
	if class.Editable() {
		t.Error("imported provenance must not be Editable()")
	}
	if !class.CopyToManagedAllowed() {
		t.Error("imported provenance must offer CopyToManaged — otherwise the read-only boundary is a dead end")
	}
}

func TestImport_CreatesExternalRowWithProvenance(t *testing.T) {
	st := newTestStore(t)
	path := writeDef(t, "reviewer.md", reviewerV1)
	defs, err := (NativeParser{}).Parse(path)
	if err != nil || len(defs) != 1 {
		t.Fatalf("native parse: %+v, %v", defs, err)
	}
	if defs[0].Source != NativeOriginSystem || defs[0].SourceRef != path || defs[0].SystemPrompt != "imported prompt v1" {
		t.Fatalf("lost source provenance/body: %+v", defs[0])
	}
	before := importBoundarySnapshot(t, st)
	requireRetiredImport(t, &Importer{Store: st}, path)
	requireImportStateUnchanged(t, st, before)
}

// Foreign content cannot change retained trust or acquire fresh authority.
func TestImport_ArrivesUntrusted(t *testing.T) {
	st := newTestStore(t)
	retainedImportProfile(t, st, "imported-reviewer", SourceProvenance)
	before := importBoundarySnapshot(t, st)
	requireRetiredImport(t, &Importer{Store: st}, writeDef(t, "reviewer.md", reviewerV1))
	// Foreign content cannot enroll an actor, acquire grants, or change historical trust.
	requireImportStateUnchanged(t, st, before)
}

func TestImport_NeverWritesBackToSource(t *testing.T) {
	st := newTestStore(t)
	path := writeDef(t, "reviewer.md", reviewerV1)
	before, err := os.ReadFile(path) //nolint:gosec // private source artifact
	if err != nil {
		t.Fatal(err)
	}
	dbBefore := importBoundarySnapshot(t, st)
	requireRetiredImport(t, &Importer{Store: st}, path)
	after, err := os.ReadFile(path) //nolint:gosec // private source artifact
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("source changed during refused import: %q -> %q", before, after)
	}
	requireImportStateUnchanged(t, st, dbBefore)
}

func TestImport_ReimportIsSync(t *testing.T) {
	st := newTestStore(t)
	retained := retainedImportProfile(t, st, "imported-reviewer", SourceProvenance)
	path := writeDef(t, "reviewer.md", reviewerV1)
	before := importBoundarySnapshot(t, st)
	imp := &Importer{Store: st}
	requireRetiredImport(t, imp, path)
	if err := os.WriteFile(path, []byte(strings.Replace(reviewerV1, "imported prompt v1", "imported prompt v2", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	requireRetiredImport(t, imp, path)
	requireImportStateUnchanged(t, st, before)
	after, err := st.GetHistoricalAgentProfile(t.Context(), retained.ID)
	if err != nil || after.Revision != retained.Revision || after.SystemPrompt != retained.SystemPrompt || after.Protocol != "acp" || after.Transport != "stdio" {
		t.Fatalf("historical profile changed: %+v, %v", after, err)
	}
}

func TestImport_SyncPreservesDatabaseOnlyConfiguration(t *testing.T) {
	st := newTestStore(t)
	retained := retainedImportProfile(t, st, "imported-reviewer", SourceProvenance)
	path := writeDef(t, "reviewer.md", reviewerV1)
	before := importBoundarySnapshot(t, st)
	imp := &Importer{Store: st}
	requireRetiredImport(t, imp, path)
	if err := os.WriteFile(path, []byte(strings.Replace(reviewerV1, "imported prompt v1", "imported prompt v2", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	requireRetiredImport(t, imp, path)
	requireImportStateUnchanged(t, st, before)
	after, err := st.GetHistoricalAgentProfile(t.Context(), retained.ID)
	if err != nil || after.Revision != retained.Revision || after.SystemPrompt != retained.SystemPrompt || after.Protocol != "acp" || after.Transport != "stdio" {
		t.Fatalf("historical profile changed: %+v, %v", after, err)
	}
}

func TestImport_RefusesSlugItDoesNotOwn(t *testing.T) {
	for _, tc := range []struct {
		source string
		class  agent.ManageClass
	}{
		{"internal", agent.ManageClassInternal}, {"user", agent.ManageClassManaged}, {"plugin", agent.ManageClassPlugin},
	} {
		t.Run(tc.source, func(t *testing.T) {
			st := newTestStore(t)
			retained := retainedImportProfile(t, st, "imported-reviewer", tc.source)
			if got := agent.NewClassification().Classify(retained.Source); got != tc.class {
				t.Fatalf("class = %q, want %q", got, tc.class)
			}
			before := importBoundarySnapshot(t, st)
			requireRetiredImport(t, &Importer{Store: st}, writeDef(t, "reviewer.md", reviewerV1))
			requireImportStateUnchanged(t, st, before)
		})
	}
}

// TestImport_RejectsTraversalSlug — ValidateSlug is the canonical gate for
// any slug that reaches a filesystem path (GO-AGENT-001). Import runs it
// before writing anything.
func TestImport_RejectsTraversalSlug(t *testing.T) {
	st := newTestStore(t)
	body := `---
name: Bad
slug: ../../etc/passwd
---
body
`
	imp := &Importer{Store: st}
	if _, err := imp.Import(context.Background(), Source{Path: writeDef(t, "bad.md", body)}); err == nil {
		t.Fatal("expected a traversal slug to be rejected")
	}
	if imp.State() != StateFailed {
		t.Errorf("state = %q, want %q", imp.State(), StateFailed)
	}
}

// TestImport_EmptyPathAndUnrecognizedPath cover the two "nothing to do"
// shapes: a caller mistake, and a path no parser claims.
func TestImport_EmptyPathAndUnrecognizedPath(t *testing.T) {
	st := newTestStore(t)
	imp := &Importer{Store: st}

	if _, err := imp.Import(context.Background(), Source{Path: ""}); err == nil {
		t.Error("expected an error for an empty path")
	}

	// NativeParser reports a directory as "not mine" — the pipeline turns
	// that into ErrNoDefinitions rather than pretending it imported nothing
	// successfully. Directory expansion belongs to a format adapter.
	_, err := imp.Import(context.Background(), Source{Path: t.TempDir()})
	if !errors.Is(err, ErrNoDefinitions) {
		t.Errorf("err = %v, want ErrNoDefinitions for a directory NativeParser does not claim", err)
	}
	if err != nil && !strings.Contains(err.Error(), "is a directory") {
		t.Errorf("err = %v, want the decline reason carried through so the operator is not left guessing", err)
	}
}

func TestImport_SeedsChildren(t *testing.T) {
	st := newTestStore(t)
	body := "---\nname: Reviewer\nslug: imported-reviewer\nprocedures:\n  - name: triage\n    body: how to triage\n---\nprompt\n"
	path := writeDef(t, "reviewer.md", body)
	defs, err := (NativeParser{}).Parse(path)
	if err != nil || len(defs) != 1 || len(defs[0].Procedures) != 1 || defs[0].Procedures[0].Name != "triage" {
		t.Fatalf("procedure parsing: %+v, %v", defs, err)
	}
	calls := 0
	imp := &Importer{Store: st, SeedChildren: func(context.Context, string, *agent.Definition, bool) error { calls++; return nil }}
	before := importBoundarySnapshot(t, st)
	requireRetiredImport(t, imp, path)
	if calls != 0 {
		t.Fatalf("refused import seeded children %d times", calls)
	}
	requireImportStateUnchanged(t, st, before)
}

// stubParser lets a test drive the pipeline without a source format.
type stubParser struct {
	defs []*agent.Definition
	err  error
}

func (p stubParser) Parse(string) ([]*agent.Definition, error) { return p.defs, p.err }

func TestImport_MultiDefinitionPathReportsPerDefinition(t *testing.T) {
	st := newTestStore(t)
	retainedImportProfile(t, st, "blocked-one", "user")
	parser := stubParser{defs: []*agent.Definition{
		{Name: "First", Slug: "first-one", SystemPrompt: "a"},
		{Name: "Blocked", Slug: "blocked-one", SystemPrompt: "b"},
		{Name: "Third", Slug: "third-one", SystemPrompt: "c"},
	}}
	defs, err := parser.Parse("/some/dir")
	if err != nil || len(defs) != 3 || defs[0].Slug != "first-one" || defs[1].Slug != "blocked-one" || defs[2].Slug != "third-one" {
		t.Fatalf("directory expansion: %+v, %v", defs, err)
	}
	before := importBoundarySnapshot(t, st)
	calls := 0
	imp := &Importer{Store: st, Parse: parser, SeedChildren: func(context.Context, string, *agent.Definition, bool) error { calls++; return nil }}
	requireRetiredImport(t, imp, "/some/dir")
	if calls != 0 {
		t.Fatalf("partial seed effects: %d", calls)
	}
	requireImportStateUnchanged(t, st, before)
}

func TestImport_ChildSeederDistinguishesInstallFromSyncAndReturnsFailure(t *testing.T) {
	st := newTestStore(t)
	path := writeDef(t, "agent.md", "---\nname: Agent\nslug: agent\n---\nPrompt.\n")
	var modes []bool
	seedFailure := errors.New("grant persistence unavailable")
	imp := &Importer{Store: st, SeedChildren: func(_ context.Context, _ string, _ *agent.Definition, created bool) error {
		modes = append(modes, created)
		return seedFailure
	}}
	before := importBoundarySnapshot(t, st)
	requireRetiredImport(t, imp, path)
	requireRetiredImport(t, imp, path)
	if len(modes) != 0 {
		t.Fatalf("refused install/retry invoked child seeder: %v", modes)
	}
	requireImportStateUnchanged(t, st, before)
}

// importBoundarySnapshot captures retained history and both authority partitions in the private DB.
func importBoundarySnapshot(t *testing.T, st *store.Store) map[string][][]any {
	t.Helper()
	out := make(map[string][][]any)
	for _, query := range []struct{ table, sql string }{
		{"agent_profiles", "SELECT * FROM agent_profiles ORDER BY rowid"},
		{"agent_profile_revisions", "SELECT * FROM agent_profile_revisions ORDER BY rowid"},
		{"agent_procedures", "SELECT * FROM agent_procedures ORDER BY rowid"},
		{"agent_reflexes", "SELECT * FROM agent_reflexes ORDER BY rowid"},
		{"agent_tools", "SELECT * FROM agent_tools ORDER BY rowid"},
		{"agent_known_skills", "SELECT * FROM agent_known_skills ORDER BY rowid"},
		{"agent_definitions", "SELECT * FROM agent_definitions ORDER BY rowid"},
		{"agent_definition_resources", "SELECT * FROM agent_definition_resources ORDER BY rowid"},
		{"agent_definition_resource_refs", "SELECT * FROM agent_definition_resource_refs ORDER BY rowid"},
		{"agent_host_settings", "SELECT * FROM agent_host_settings ORDER BY rowid"},
		{"agent_actor_bindings", "SELECT * FROM agent_actor_bindings ORDER BY rowid"},
		{"actor_granted_tools", "SELECT * FROM actor_granted_tools ORDER BY rowid"},
		{"actor_known_skills", "SELECT * FROM actor_known_skills ORDER BY rowid"},
	} {
		rows, err := st.DB.QueryContext(t.Context(), query.sql)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		for rows.Next() {
			cells := make([]any, len(columns))
			refs := make([]any, len(columns))
			for i := range cells {
				refs[i] = &cells[i]
			}
			if err := rows.Scan(refs...); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			for i, cell := range cells {
				if raw, ok := cell.([]byte); ok {
					cells[i] = append([]byte(nil), raw...)
				}
			}
			out[query.table] = append(out[query.table], cells)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func retainedImportProfile(t *testing.T, st *store.Store, slug, source string) *store.AgentProfile {
	t.Helper()
	p := &store.AgentProfile{Name: "Historical reviewer", Slug: slug, SystemPrompt: "retained prompt", Source: source}
	// Raw retained data is not an import, enrollment, or runtime fixture.
	if err := storetest.HistoricalProfile(t.Context(), st, p); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.ExecContext(t.Context(), `UPDATE agent_profiles SET protocol='acp', transport='stdio', default_trust_tier='trusted' WHERE id=?`, p.ID); err != nil {
		t.Fatal(err)
	}

	// Retained grants and procedure content must stay historical and intact.
	if _, err := st.DB.ExecContext(t.Context(), `INSERT INTO known_tools(id,name,created_at,updated_at) VALUES(?,?,datetime('now'),datetime('now'))`, "retained-import-tool", "retained_import_tool"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.ExecContext(t.Context(), `INSERT INTO agent_tools(agent_id,tool_id,granted_via,created_at) VALUES(?,?,?,datetime('now'))`, p.ID, "retained-import-tool", "historical-operator"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.ExecContext(t.Context(), `INSERT INTO agent_known_skills(agent_id,skill_name,approved_content_hash,granted_by,capabilities_granted) VALUES(?,?,?,?,?)`, p.ID, "retained-import-skill", "historical-approved-content", "historical-operator", `{"run":true}`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.ExecContext(t.Context(), `INSERT INTO agent_procedures(agent_id,name,body) VALUES(?,?,?)`, p.ID, "retained-procedure", "Private retained procedure content."); err != nil {
		t.Fatal(err)
	}
	saved, err := st.GetHistoricalAgentProfile(t.Context(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

func requireRetiredImport(t *testing.T, imp *Importer, path string) {
	t.Helper()
	res, err := imp.Import(t.Context(), Source{Path: path})
	if !errors.Is(err, store.ErrImmutableAgentProfile) {
		t.Fatalf("Import = %+v, %v; want immutable profile refusal", res, err)
	}
	if len(res.Outcomes) != 0 || res.Skipped() {
		t.Fatalf("refused import reported effects: %+v", res)
	}
	if imp.State() != StateFailed {
		t.Fatalf("state = %q, want failed", imp.State())
	}
}

func requireImportStateUnchanged(t *testing.T, st *store.Store, before map[string][][]any) {
	t.Helper()
	if after := importBoundarySnapshot(t, st); !reflect.DeepEqual(before, after) {
		t.Fatalf("retired import changed history or authority: before=%#v after=%#v", before, after)
	}
}
