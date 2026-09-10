package agentimport

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

	imp := &Importer{Store: st}
	res, err := imp.Import(context.Background(), Source{Path: path})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if created, _, _ := res.Counts(); created != 1 {
		t.Fatalf("counts: want 1 created, got %+v", res.Outcomes)
	}

	row, err := st.GetAgentBySlug(context.Background(), "imported-reviewer")
	if err != nil {
		t.Fatalf("GetAgentBySlug: %v", err)
	}
	if row.Source != SourceProvenance {
		t.Errorf("Source = %q, want %q", row.Source, SourceProvenance)
	}
	if row.SourceRef != path {
		t.Errorf("SourceRef = %q, want the originating path %q", row.SourceRef, path)
	}
	if row.ImportedAt == "" {
		t.Error("ImportedAt must record when the import happened")
	}
	// origin_system records the ecosystem the definition was authored in;
	// `source` records how the row came to exist. Two different questions.
	if row.OriginSystem != NativeOriginSystem {
		t.Errorf("OriginSystem = %q, want %q", row.OriginSystem, NativeOriginSystem)
	}
	if got := agent.NewClassification().Classify(row.Source); got != agent.ManageClassExternal {
		t.Errorf("stored row classifies as %q, want external", got)
	}
	// kind is migration 015's column, not ManageClass — an imported agent is
	// a DB-backed profile, not a messaging auto-registration.
	if row.Kind != "internal" {
		t.Errorf("Kind = %q, want %q (kind is not ManageClass)", row.Kind, "internal")
	}
}

// TestImport_ArrivesUntrusted — foreign content does not arrive trusted.
func TestImport_ArrivesUntrusted(t *testing.T) {
	st := newTestStore(t)
	imp := &Importer{Store: st}
	if _, err := imp.Import(context.Background(), Source{Path: writeDef(t, "reviewer.md", reviewerV1)}); err != nil {
		t.Fatalf("Import: %v", err)
	}

	var tier string
	if err := st.DB.QueryRow(
		`SELECT default_trust_tier FROM agent_profiles WHERE slug = ?`, "imported-reviewer",
	).Scan(&tier); err != nil {
		t.Fatalf("read trust tier: %v", err)
	}
	if tier != "untrusted" {
		t.Errorf("default_trust_tier = %q, want untrusted", tier)
	}
}

// TestImport_NeverWritesBackToSource is the one-way rule, tested rather than
// asserted: the bytes on disk must be identical before and after an import
// that mints a fresh database identity for the definition.
func TestImport_NeverWritesBackToSource(t *testing.T) {
	st := newTestStore(t)
	path := writeDef(t, "reviewer.md", reviewerV1)
	before, readErr := os.ReadFile(path) //nolint:gosec // reads a fixture this test just wrote into t.TempDir()
	if readErr != nil {
		t.Fatalf("read before: %v", readErr)
	}

	imp := &Importer{Store: st}
	if _, err := imp.Import(context.Background(), Source{Path: path}); err != nil {
		t.Fatalf("Import: %v", err)
	}

	after, readErr := os.ReadFile(path) //nolint:gosec // reads a fixture this test just wrote into t.TempDir()
	if readErr != nil {
		t.Fatalf("read after: %v", readErr)
	}
	if string(before) != string(after) {
		t.Errorf("source file changed during import:\nbefore:\n%s\nafter:\n%s", before, after)
	}

	row, _ := st.GetAgentBySlug(context.Background(), "imported-reviewer")
	if row.ID == "" {
		t.Fatal("expected a minted database identity")
	}
	if strings.Contains(string(after), row.ID) {
		t.Errorf("minted identity %q was written back into the source file", row.ID)
	}
}

// TestImport_ReimportIsSync — the same path imported twice updates the row it
// already owns rather than creating a second one or refusing.
func TestImport_ReimportIsSync(t *testing.T) {
	st := newTestStore(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "reviewer.md")
	if err := os.WriteFile(path, []byte(reviewerV1), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	imp := &Importer{Store: st}
	if _, err := imp.Import(context.Background(), Source{Path: path}); err != nil {
		t.Fatalf("first Import: %v", err)
	}
	first, _ := st.GetAgentBySlug(context.Background(), "imported-reviewer")

	edited := strings.Replace(reviewerV1, "imported prompt v1", "imported prompt v2", 1)
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	res, err := imp.Import(context.Background(), Source{Path: path})
	if err != nil {
		t.Fatalf("second Import: %v", err)
	}
	if _, synced, _ := res.Counts(); synced != 1 {
		t.Fatalf("want 1 synced, got %+v", res.Outcomes)
	}

	second, _ := st.GetAgentBySlug(context.Background(), "imported-reviewer")
	if second.ID != first.ID {
		t.Errorf("sync minted a new identity: %q -> %q", first.ID, second.ID)
	}
	if second.SystemPrompt != "imported prompt v2" {
		t.Errorf("SystemPrompt = %q, want the re-read content", second.SystemPrompt)
	}
}

// TestImport_SyncPreservesDatabaseOnlyConfiguration: composition and ACP
// columns have zero representation in the source format, so a sync must not
// wipe values set through their own direct-DB write paths — the same
// preservation upsertAgentDef performs, for the same reason.
func TestImport_SyncPreservesDatabaseOnlyConfiguration(t *testing.T) {
	st := newTestStore(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "reviewer.md")
	if err := os.WriteFile(path, []byte(reviewerV1), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	imp := &Importer{Store: st}
	if _, err := imp.Import(context.Background(), Source{Path: path}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	row, _ := st.GetAgentBySlug(context.Background(), "imported-reviewer")
	if _, err := st.DB.Exec(
		`UPDATE agent_profiles SET protocol = 'acp', transport = 'stdio' WHERE id = ?`, row.ID,
	); err != nil {
		t.Fatalf("set ACP config: %v", err)
	}

	edited := strings.Replace(reviewerV1, "imported prompt v1", "imported prompt v2", 1)
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if _, err := imp.Import(context.Background(), Source{Path: path}); err != nil {
		t.Fatalf("sync: %v", err)
	}

	after, _ := st.GetAgentBySlug(context.Background(), "imported-reviewer")
	if after.Protocol != "acp" || after.Transport != "stdio" {
		t.Errorf("sync wiped ACP config: protocol=%q transport=%q", after.Protocol, after.Transport)
	}
	if after.SystemPrompt != "imported prompt v2" {
		t.Errorf("SystemPrompt = %q, want the re-read content", after.SystemPrompt)
	}
}

// TestImport_RefusesSlugItDoesNotOwn is the inbound half of the ownership
// boundary. Table-driven across every non-external class so a new
// ManageClass cannot be added without a decision about this path.
func TestImport_RefusesSlugItDoesNotOwn(t *testing.T) {
	for _, tc := range []struct {
		name      string
		source    string
		wantClass agent.ManageClass
	}{
		{"internal harness primitive", "internal", agent.ManageClassInternal},
		{"operator-managed profile", "user", agent.ManageClassManaged},
		{"plugin-provided profile", "plugin", agent.ManageClassPlugin},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			existing := &store.AgentProfile{
				Name:         "Incumbent",
				Slug:         "imported-reviewer",
				SystemPrompt: "incumbent prompt",
				Source:       tc.source,
			}
			if err := st.CreateAgent(context.Background(), existing); err != nil {
				t.Fatalf("seed incumbent: %v", err)
			}

			imp := &Importer{Store: st}
			res, err := imp.Import(context.Background(), Source{Path: writeDef(t, "reviewer.md", reviewerV1)})
			if err != nil {
				t.Fatalf("Import returned a hard error; a refusal is a reported outcome: %v", err)
			}
			if _, _, skipped := res.Counts(); skipped != 1 {
				t.Fatalf("want 1 skipped, got %+v", res.Outcomes)
			}
			out := res.Outcomes[0]
			if out.BlockedBy != tc.wantClass {
				t.Errorf("BlockedBy = %q, want %q", out.BlockedBy, tc.wantClass)
			}
			if !errors.Is(out.Err, ErrSlugNotImportable) {
				t.Errorf("Err = %v, want it to wrap ErrSlugNotImportable so a caller can classify it", out.Err)
			}
			if !strings.Contains(out.Reason, tc.wantClass.Describe()) {
				t.Errorf("reason %q does not say what is in the way", out.Reason)
			}

			after, _ := st.GetAgentBySlug(context.Background(), "imported-reviewer")
			if after.SystemPrompt != "incumbent prompt" {
				t.Errorf("incumbent was overwritten: %q", after.SystemPrompt)
			}
			if after.Source != tc.source {
				t.Errorf("incumbent provenance changed: %q -> %q", tc.source, after.Source)
			}
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

// TestImport_SeedsChildrenOnCreateAndSync — an imported definition's declared
// procedures must reach agent_procedures, or the imported agent is missing
// content it declared.
func TestImport_SeedsChildren(t *testing.T) {
	st := newTestStore(t)
	body := `---
name: Reviewer
slug: imported-reviewer
procedures:
  - name: triage
    body: how to triage
---
prompt
`
	var seededAgentID string
	imp := &Importer{
		Store: st,
		SeedChildren: func(_ context.Context, agentID string, def *agent.Definition) {
			seededAgentID = agentID
			if len(def.Procedures) != 1 || def.Procedures[0].Name != "triage" {
				t.Errorf("seeder received unexpected procedures: %+v", def.Procedures)
			}
		},
	}
	if _, err := imp.Import(context.Background(), Source{Path: writeDef(t, "reviewer.md", body)}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	row, _ := st.GetAgentBySlug(context.Background(), "imported-reviewer")
	if seededAgentID != row.ID {
		t.Errorf("seeder got agent id %q, want %q", seededAgentID, row.ID)
	}
}

// stubParser lets a test drive the pipeline without a source format.
type stubParser struct {
	defs []*agent.Definition
	err  error
}

func (p stubParser) Parse(string) ([]*agent.Definition, error) { return p.defs, p.err }

// TestImport_MultiDefinitionPathReportsPerDefinition — the "directory
// argument is sugar" contract: a path that expands to N definitions reports
// each one, and one refusal does not abandon the rest.
func TestImport_MultiDefinitionPathReportsPerDefinition(t *testing.T) {
	st := newTestStore(t)
	if err := st.CreateAgent(context.Background(), &store.AgentProfile{
		Name: "Incumbent", Slug: "blocked-one", SystemPrompt: "incumbent", Source: "user",
	}); err != nil {
		t.Fatalf("seed incumbent: %v", err)
	}

	imp := &Importer{
		Store: st,
		Parse: stubParser{defs: []*agent.Definition{
			{Name: "First", Slug: "first-one", SystemPrompt: "a"},
			{Name: "Blocked", Slug: "blocked-one", SystemPrompt: "b"},
			{Name: "Third", Slug: "third-one", SystemPrompt: "c"},
		}},
	}
	res, err := imp.Import(context.Background(), Source{Path: "/some/dir"})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	created, _, skipped := res.Counts()
	if created != 2 || skipped != 1 {
		t.Fatalf("want 2 created / 1 skipped, got %d / %d: %+v", created, skipped, res.Outcomes)
	}
	if len(res.Outcomes) != 3 {
		t.Fatalf("want an outcome per definition, got %d", len(res.Outcomes))
	}
	if res.Outcomes[2].Action != ActionCreated {
		t.Error("a refusal in the middle must not abandon the definitions after it")
	}
	if !res.Skipped() {
		t.Error("Skipped() must report the refusal so the CLI can exit non-zero")
	}
}
