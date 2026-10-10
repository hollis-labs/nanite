package agentimport

import (
	"os"
	"strings"
	"testing"
)

func TestAgentRevisionsImportAndSync(t *testing.T) {
	st := newTestStore(t)
	retained := retainedImportProfile(t, st, "imported-reviewer", SourceProvenance)
	path := writeDef(t, "revision.md", reviewerV1)
	before := importBoundarySnapshot(t, st)
	imp := &Importer{Store: st}
	requireRetiredImport(t, imp, path)
	if err := os.WriteFile(path, []byte(strings.Replace(reviewerV1, "imported prompt v1", "imported prompt v2", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	requireRetiredImport(t, imp, path)
	requireImportStateUnchanged(t, st, before)
	revision, err := st.GetAgentRevision(t.Context(), retained.ID, retained.Revision)
	if err != nil || revision.Profile.SystemPrompt != "retained prompt" || revision.ID != retained.Revision {
		t.Fatalf("retained revision = %+v, %v", revision, err)
	}
}
