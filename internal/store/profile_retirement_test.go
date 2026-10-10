package store_test

import (
	"bytes"
	"encoding/json"
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
	if err = storetest.HistoricalProfile(ctx, st, p); err != nil {
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
	if err = storetest.HistoricalProfile(ctx, st, p); err != nil {
		t.Fatal(err)
	}
	if _, err = st.ExportProfileRetirement(ctx, p.ID); !errors.Is(err, store.ErrProfileRetirementBound) {
		t.Fatal("oversized export accepted or truncated", err)
	}
	if _, err = st.GetHistoricalAgentProfile(ctx, p.ID); err != nil {
		t.Fatal("export refusal changed profile", err)
	}
}

func TestProfileRetirementRejectsChangedSQLiteCellType(t *testing.T) {
	ctx := t.Context()
	st, err := storetest.New(t, ctx, filepath.Join(t.TempDir(), "retire-types.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(ctx) })
	p := &store.AgentProfile{Name: "Test", Slug: "cell-type-test", SystemPrompt: "x", Source: "user"}
	if err = storetest.HistoricalProfile(ctx, st, p); err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB.ExecContext(ctx, `INSERT INTO agent_procedures(agent_id,name,body) VALUES(?,?,?)`, p.ID, "typed", []byte("foo")); err != nil {
		t.Fatal(err)
	}
	snapshot, err := st.ExportProfileRetirement(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := snapshot.Digest()
	if err != nil {
		t.Fatal(err)
	}
	// JSON encodes []byte("foo") as "Zm9v". A TEXT cell holding that spelling
	// is different SQLite data and must not satisfy the exported BLOB snapshot.
	if _, err = st.DB.ExecContext(ctx, `UPDATE agent_procedures SET body=? WHERE agent_id=?`, "Zm9v", p.ID); err != nil {
		t.Fatal(err)
	}
	err = st.RetireExportedProfile(ctx, p.ID, digest)
	_, remainingErr := st.GetHistoricalAgentProfile(ctx, p.ID)
	if !errors.Is(err, store.ErrProfileRetirementConflict) || remainingErr != nil {
		t.Fatalf("changed cell type accepted: outcome=%v; remaining profile error=%v", err, remainingErr)
	}
}

func TestProfileRetirementPreservesSQLiteCellClassesAndBytes(t *testing.T) {
	cases := []struct {
		name                      string
		original, changed         any
		originalType, changedType string
	}{
		{"blob-text", []byte("foo"), "Zm9v", "blob", "text"},
		{"integer-real", int64(1), float64(1), "integer", "real"},
		{"null-empty-text", nil, "", "null", "text"},
		{"null-empty-blob", nil, []byte{}, "null", "blob"},
		{"empty-text-blob", "", []byte{}, "text", "blob"},
		{"binary-bytes", []byte{0, 255, 128}, []byte{0, 255, 129}, "blob", "blob"},
		{"invalid-utf8-text", string([]byte{255}), string([]byte{254}), "text", "text"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			st, err := storetest.New(t, ctx, filepath.Join(t.TempDir(), "typed.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = st.Close(ctx) })
			p := &store.AgentProfile{Name: "Test", Slug: "typed", SystemPrompt: "x", Source: "user"}
			if err = storetest.HistoricalProfile(ctx, st, p); err != nil {
				t.Fatal(err)
			}
			// No column affinity: retain INTEGER and REAL storage classes.
			if _, err = st.DB.Exec(`CREATE TABLE retirement_typed(profile_id TEXT REFERENCES agent_profiles(id) ON DELETE CASCADE, value)`); err != nil {
				t.Fatal(err)
			}
			if _, err = st.DB.ExecContext(ctx, `INSERT INTO retirement_typed VALUES (?,?)`, p.ID, tc.original); err != nil {
				t.Fatal(err)
			}
			var kind string
			if err = st.DB.QueryRow(`SELECT typeof(value) FROM retirement_typed`).Scan(&kind); err != nil || kind != tc.originalType {
				t.Fatalf("original storage class: %s, %v", kind, err)
			}
			snapshot, err := st.ExportProfileRetirement(ctx, p.ID)
			if err != nil {
				t.Fatal(err)
			}
			var cells []struct {
				Type  string          `json:"type"`
				Value json.RawMessage `json:"value"`
			}
			if err = json.Unmarshal(snapshot.Tables["retirement_typed"].Rows[0], &cells); err != nil {
				t.Fatal(err)
			}
			cell := cells[1]
			if cell.Type != tc.originalType {
				t.Fatalf("exported storage class: %s", cell.Type)
			}
			if cell.Type == "text" || cell.Type == "blob" {
				var actual []byte
				if err = json.Unmarshal(cell.Value, &actual); err != nil {
					t.Fatal(err)
				}
				var want []byte
				if v, ok := tc.original.(string); ok {
					want = []byte(v)
				} else {
					want = tc.original.([]byte)
				}
				if !bytes.Equal(actual, want) {
					t.Fatal("export lost SQLite bytes")
				}
			}
			digest, err := snapshot.Digest()
			if err != nil {
				t.Fatal(err)
			}
			// An old-format prepared snapshot must require a fresh export.
			snapshot.SchemaVersion = 1
			legacy, err := snapshot.Digest()
			if err != nil {
				t.Fatal(err)
			}
			if err = st.RetireExportedProfile(ctx, p.ID, legacy); !errors.Is(err, store.ErrProfileRetirementConflict) {
				t.Fatal("legacy export admitted", err)
			}
			if _, err = st.DB.ExecContext(ctx, `UPDATE retirement_typed SET value=?`, tc.changed); err != nil {
				t.Fatal(err)
			}
			if err = st.DB.QueryRow(`SELECT typeof(value) FROM retirement_typed`).Scan(&kind); err != nil || kind != tc.changedType {
				t.Fatalf("changed storage class: %s, %v", kind, err)
			}
			if err = st.RetireExportedProfile(ctx, p.ID, digest); !errors.Is(err, store.ErrProfileRetirementConflict) {
				t.Fatal("changed typed cell admitted", err)
			}
			if _, err = st.GetHistoricalAgentProfile(ctx, p.ID); err != nil {
				t.Fatal("conflict deleted profile", err)
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
				t.Fatal("unchanged typed state refused", err)
			}
		})
	}
}

func TestProfileRetirementPreservesDeclaredDateAndBooleanStorage(t *testing.T) {
	ctx := t.Context()
	st, err := storetest.New(t, ctx, filepath.Join(t.TempDir(), "declared.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(ctx) })
	p := &store.AgentProfile{Name: "Test", Slug: "declared", SystemPrompt: "x", Source: "user"}
	if err = storetest.HistoricalProfile(ctx, st, p); err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB.Exec(`CREATE TABLE retirement_declared(profile_id TEXT REFERENCES agent_profiles(id) ON DELETE CASCADE, stamp DATETIME, flag BOOLEAN)`); err != nil {
		t.Fatal(err)
	}
	const original = "2026-10-09 10:00:00+00:00"
	if _, err = st.DB.ExecContext(ctx, `INSERT INTO retirement_declared VALUES (?, ?, 2)`, p.ID, original); err != nil {
		t.Fatal(err)
	}
	snapshot, err := st.ExportProfileRetirement(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	var cells []struct {
		Type  string          `json:"type"`
		Value json.RawMessage `json:"value"`
	}
	if err = json.Unmarshal(snapshot.Tables["retirement_declared"].Rows[0], &cells); err != nil {
		t.Fatal(err)
	}
	var stamp []byte
	if err = json.Unmarshal(cells[1].Value, &stamp); err != nil || string(stamp) != original || cells[1].Type != "text" {
		t.Fatal("timestamp representation lost", err)
	}
	if cells[2].Type != "integer" || string(cells[2].Value) != "2" {
		t.Fatal("boolean declaration lost raw integer")
	}
	digest, err := snapshot.Digest()
	if err != nil {
		t.Fatal(err)
	}
	// Same parsed instant, different stored bytes. Export must not normalize it.
	if _, err = st.DB.Exec(`UPDATE retirement_declared SET stamp='2026-10-09T10:00:00Z'`); err != nil {
		t.Fatal(err)
	}
	if err = st.RetireExportedProfile(ctx, p.ID, digest); !errors.Is(err, store.ErrProfileRetirementConflict) {
		t.Fatal("normalized date passed guard", err)
	}
	if _, err = st.DB.ExecContext(ctx, `UPDATE retirement_declared SET stamp=?, flag=1`, original); err != nil {
		t.Fatal(err)
	}
	if err = st.RetireExportedProfile(ctx, p.ID, digest); !errors.Is(err, store.ErrProfileRetirementConflict) {
		t.Fatal("normalized boolean passed guard", err)
	}
	if _, err = st.GetHistoricalAgentProfile(ctx, p.ID); err != nil {
		t.Fatal("conflict deleted profile", err)
	}
}

func TestAuditedRetirementSuppressesFreshHostAdmissionAfterReopen(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "shared-retirement.db")
	st, err := storetest.New(t, ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	historical := &store.AgentProfile{Name: "Private protected fixture", Slug: "protected-prior", Source: "internal", SystemPrompt: "Original historical body"}
	if operationErr := storetest.HistoricalProfile(ctx, st, historical); operationErr != nil {
		t.Fatal(operationErr)
	}
	snapshot, err := st.ExportProtectedProfileRetirement(ctx, historical.ID)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := snapshot.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if operationErr := st.RetireExportedProfileWithAudit(ctx, historical.ID, digest, store.RetireAgentProfileAudit{ExportID: "private-durable-export", Actor: "private-test-operator", Reason: "Explicit private retirement"}); operationErr != nil {
		t.Fatal(operationErr)
	}
	if operationErr := st.Close(ctx); operationErr != nil {
		t.Fatal(operationErr)
	}
	reopened, err := storetest.New(t, ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close(ctx) })
	tombstone, err := reopened.GetRetiredAgentProfileBySlug(ctx, historical.Slug)
	if err != nil || tombstone == nil || tombstone.Digest != digest || tombstone.ID != historical.ID {
		t.Fatalf("audited tombstone lost: %+v %v", tombstone, err)
	}
	data := []byte("---\nschema_version: \"2\"\ndefinition_id: def:private-replacement\nrevision: \"1\"\nname: replacement\ndescription: Private replacement artifact.\nbehavior:\n  purpose: Complete private work.\nrequirements: {}\nharness_profile:\n  context: {}\n  permissions:\n    profile: default\ncontinuity:\n  mode: ephemeral\n---\nFresh immutable body.\n")
	pin, err := reopened.InstallAgentDefinition(ctx, data, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = reopened.CreateAgentHostSettings(ctx, store.AgentHostSettings{Slug: historical.Slug, Title: "Replacement", Source: "explicit-private-test-authoring", DefinitionRef: pin, Settings: store.NativeHostSettings{Version: "1", Runtime: "api"}, Enabled: true})
	if !errors.Is(err, store.ErrProfileIngestionRetired) {
		t.Fatalf("public retirement ledger did not refuse fresh admission: %v", err)
	}
	rows, err := reopened.ListAgentHostSettings(ctx)
	if err != nil || len(rows) != 0 {
		t.Fatalf("refused admission wrote fresh host: %+v %v", rows, err)
	}
}
