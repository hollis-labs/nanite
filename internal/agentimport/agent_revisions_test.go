package agentimport

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestAgentRevisionsImportAndSync(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	path := writeDef(t, "revision.md", reviewerV1)
	imp := &Importer{Store: st}
	if _, err := imp.Import(ctx, Source{Path: path}); err != nil {
		t.Fatal(err)
	}
	first, err := st.GetAgentBySlug(ctx, "imported-reviewer")
	if err != nil {
		t.Fatal(err)
	}
	old, err := st.GetAgentRevision(ctx, first.ID, first.Revision)
	if err != nil || old.Profile.SystemPrompt != "imported prompt v1" {
		t.Fatalf("import snapshot = %#v, %v", old, err)
	}
	if err = os.WriteFile(path, []byte(strings.Replace(reviewerV1, "imported prompt v1", "imported prompt v2", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = imp.Import(ctx, Source{Path: path}); err != nil {
		t.Fatal(err)
	}
	second, err := st.GetAgentBySlug(ctx, first.Slug)
	if err != nil {
		t.Fatal(err)
	}
	latest, err := st.GetAgentRevision(ctx, second.ID, second.Revision)
	if err != nil || latest.Profile.SystemPrompt != "imported prompt v2" || second.Revision == first.Revision {
		t.Fatalf("sync snapshot = %#v, %v", latest, err)
	}
	retained, err := st.GetAgentRevision(ctx, first.ID, first.Revision)
	if err != nil || retained.Profile.SystemPrompt != "imported prompt v1" {
		t.Fatal("sync erased the previous snapshot")
	}
}
