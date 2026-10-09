package store_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

func TestProfileRetirementCapturesCascadeChildrenAndRejectsChangedState(t *testing.T) {
	ctx := t.Context()
	st, err := storetest.New(t, ctx, filepath.Join(t.TempDir(), "retire.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(ctx) })
	p := &store.AgentProfile{Name: "Test", Slug: "cascade-test", SystemPrompt: "x", Source: "user"}
	if err = st.CreateAgent(ctx, p); err != nil {
		t.Fatal(err)
	}
	// Exercise the schema-discovered cascade path with binary data, including a
	// second-level child that the legacy explicit cleanup list cannot enumerate.
	if _, err = st.DB.Exec(`CREATE TABLE retirement_parent(id TEXT PRIMARY KEY, profile_id TEXT REFERENCES agent_profiles(id) ON DELETE CASCADE);
CREATE TABLE retirement_child(id TEXT PRIMARY KEY, parent_id TEXT REFERENCES retirement_parent(id) ON DELETE CASCADE, body BLOB);
`); err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB.ExecContext(ctx, "INSERT INTO retirement_parent VALUES('owned', ?)", p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB.ExecContext(ctx, "INSERT INTO retirement_child VALUES('child','owned',x'0001ff')"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := st.ExportProfileRetirement(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Tables["retirement_child"].Rows) != 1 {
		t.Fatal("cascade child not exported")
	}
	digest, err := snapshot.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB.Exec(`UPDATE retirement_child SET body=x'ff0200' WHERE id='child'`); err != nil {
		t.Fatal(err)
	}
	if err = st.RetireExportedProfile(ctx, p.ID, digest); !errors.Is(err, store.ErrProfileRetirementConflict) {
		t.Fatal("changed nested child accepted", err)
	}
	fresh, err := st.ExportProfileRetirement(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	digest, err = fresh.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if err = st.RetireExportedProfile(ctx, p.ID, digest); err != nil {
		t.Fatal(err)
	}
	var children int
	if err = st.DB.QueryRow(`SELECT count(*) FROM retirement_child`).Scan(&children); err != nil || children != 0 {
		t.Fatal("cascade effect missing", err)
	}
}

func TestProfileRetirementOversizedExportRefusesWithoutDeletion(t *testing.T) {
	ctx := t.Context()
	st, err := storetest.New(t, ctx, filepath.Join(t.TempDir(), "retire-bound.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(ctx) })
	p := &store.AgentProfile{Name: "Large", Slug: "large-export", SystemPrompt: strings.Repeat("x", store.ProfileExportMaxBytes+1), Source: "user"}
	if err = st.CreateAgent(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err = st.ExportProfileRetirement(ctx, p.ID); !errors.Is(err, store.ErrProfileRetirementBound) {
		t.Fatal("oversized export accepted or truncated", err)
	}
	if _, err = st.GetAgent(ctx, p.ID); err != nil {
		t.Fatal("export refusal changed profile", err)
	}
}
