package dataexport

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/hollis-labs/go-sqlite/sqlitekit"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
	_ "modernc.org/sqlite"
)

func exportFixture(t *testing.T) (*sql.DB, Spec, string) {
	t.Helper()
	databasePath := filepath.Join(t.TempDir(), "core.db")
	db, err := sqlitekit.OpenSingle(context.Background(), databasePath, sqlitekit.OpenOptions{Options: sqlitekit.WriterOptions(), CreateParentDir: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, checkErr := db.Exec(`CREATE TABLE bookmarks(id TEXT PRIMARY KEY, message_id TEXT, session_id TEXT, note TEXT, tags TEXT, created_at DATETIME, counter INTEGER, payload BLOB, score REAL)`); checkErr != nil {
		t.Fatal(checkErr)
	}
	if _, checkErr := db.Exec(`INSERT INTO bookmarks VALUES (?,?,?,?,?,?,?,?,?)`, "bookmark-one", "message-one", "session-one", nil, "[]", "2026-10-02T00:00:00Z", int64(9223372036854775807), []byte{0, 1, 255}, 1.2345678901234567); checkErr != nil {
		t.Fatal(checkErr)
	}
	if _, checkErr := db.Exec(`INSERT INTO bookmarks VALUES (?,?,?,?,?,?,?,?,?)`, "bookmark-two", "message-two", "session-one", string([]byte{255}), "[\"star\"]", "2026-10-02T00:00:01Z", int64(-9223372036854775807), []byte{}, 0.0); checkErr != nil {
		t.Fatal(checkErr)
	}
	source, err := SourceID(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	return db, Spec{PluginID: "hollis.bookmarks", Feature: "bookmarks", SourceID: source, Table: "bookmarks"}, t.TempDir()
}

func bookmarkCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var count int
	if checkErr := db.QueryRow(`SELECT count(*) FROM bookmarks`).Scan(&count); checkErr != nil {
		t.Fatal(checkErr)
	}
	return count
}

func TestExportAndDropVerifiedLosslessCommit(t *testing.T) {
	db, spec, dataDir := exportFixture(t)
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	receipt, err := ExportAndDrop(ctx, tx, spec, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.RowCount != 2 {
		t.Fatal("row count differs")
	}
	if checkErr := tx.Commit(); checkErr != nil {
		t.Fatal(checkErr)
	}
	var exists bool
	if checkErr := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE name='bookmarks')`).Scan(&exists); checkErr != nil || exists {
		t.Fatal("retired table remains")
	}
	file, err := os.Open(filepath.Join(dataDir, receipt.Path)) // #nosec G304 -- host-produced receipt beneath isolated test DataDir.
	if err != nil {
		t.Fatal(err)
	}
	export, err := pluginapi.DecodeDataExport(file)
	_ = file.Close()
	if err != nil || receipt.Verify(export) != nil {
		t.Fatalf("persisted snapshot: %v", err)
	}
	values := make([]any, len(export.Snapshot.Rows[0]))
	for i, cell := range export.Snapshot.Rows[0] {
		values[i], err = cell.Value()
		if err != nil {
			t.Fatal(err)
		}
	}
	want := []any{"bookmark-one", "message-one", "session-one", nil, "[]", "2026-10-02T00:00:00Z", int64(9223372036854775807), []byte{0, 1, 255}, 1.2345678901234567}
	if !reflect.DeepEqual(values, want) {
		t.Fatalf("SQLite values differ: %#v", values)
	}
	invalidText, err := export.Snapshot.Rows[1][3].Value()
	if err != nil || invalidText != string([]byte{255}) {
		t.Fatal("invalid UTF-8 text lost")
	}
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	again, err := ExportAndDrop(ctx, tx, spec, dataDir)
	if err != nil || !reflect.DeepEqual(again, receipt) {
		t.Fatalf("idempotent retry: %v", err)
	}
	if checkErr := tx.Commit(); checkErr != nil {
		t.Fatal(checkErr)
	}
}

func TestExportAndDropRollbackLeavesRowsAndNoReceipt(t *testing.T) {
	db, spec, dataDir := exportFixture(t)
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := ExportAndDrop(ctx, tx, spec, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if checkErr := tx.Rollback(); checkErr != nil {
		t.Fatal(checkErr)
	}
	if bookmarkCount(t, db) != 2 {
		t.Fatal("rollback lost rows")
	}
	var ledger bool
	if checkErr := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE name='plugin_core_exports')`).Scan(&ledger); checkErr != nil || ledger {
		t.Fatal("rollback published a receipt")
	}
	if _, checkErr := os.Stat(filepath.Join(dataDir, receipt.Path)); checkErr != nil {
		t.Fatal("durable orphan absent")
	}
	// Retry proves orphan reuse is safe and source changes produce a new file.
	if _, checkErr := db.Exec(`UPDATE bookmarks SET tags='["changed"]' WHERE id='bookmark-one'`); checkErr != nil {
		t.Fatal(checkErr)
	}
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	updated, err := ExportAndDrop(ctx, tx, spec, dataDir)
	if err != nil || updated.SHA256 == receipt.SHA256 {
		t.Fatal("changed core rows reused stale export")
	}
	if checkErr := tx.Commit(); checkErr != nil {
		t.Fatal(checkErr)
	}
	if _, checkErr := os.Stat(filepath.Join(dataDir, receipt.Path)); checkErr != nil {
		t.Fatal("retry deleted older snapshot")
	}
}

func TestExportFailuresDoNotDropSource(t *testing.T) {
	for _, failure := range []string{"write", "corrupt", "symlink", "invalid table"} {
		t.Run(failure, func(t *testing.T) {
			db, spec, dataDir := exportFixture(t)
			tx, err := db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			publisher := snapshotPublisher(publishSnapshot)
			switch failure {
			case "write":
				publisher = func(string, string, []byte) error { return io.ErrShortWrite }
			case "corrupt":
				publisher = func(dir, path string, raw []byte) error {
					if checkErr := publishSnapshot(dir, path, raw); checkErr != nil {
						return checkErr
					}
					return os.WriteFile(filepath.Join(dir, path), []byte("corrupt"), 0600)
				}
			case "symlink":
				outside := t.TempDir()
				if checkErr := os.Symlink(outside, filepath.Join(dataDir, "core-imports")); checkErr != nil {
					t.Fatal(checkErr)
				}
			case "invalid table":
				spec.Table = `bookmarks"; DROP TABLE sessions;--`
			}
			_, err = exportAndDrop(context.Background(), tx, spec, dataDir, publisher)
			if err == nil {
				t.Fatal("failure did not stop drop")
			}
			if failure == "write" && !errors.Is(err, io.ErrShortWrite) {
				t.Fatal(err)
			}
			if checkErr := tx.Rollback(); checkErr != nil {
				t.Fatal(checkErr)
			}
			if bookmarkCount(t, db) != 2 {
				t.Fatal("failed export lost source rows")
			}
		})
	}
}

func TestExportSourceIDSeparatesWorkspaces(t *testing.T) {
	one, err := SourceID(filepath.Join(t.TempDir(), "one.db"))
	if err != nil {
		t.Fatal(err)
	}
	two, err := SourceID(filepath.Join(t.TempDir(), "two.db"))
	if err != nil || one == two {
		t.Fatal("workspace identifiers collide")
	}
}

func TestFailedDropCannotCommitAnImportReceipt(t *testing.T) {
	db, spec, dataDir := exportFixture(t)
	if _, checkErr := db.Exec(`CREATE TABLE dependent(bookmark_id TEXT REFERENCES bookmarks(id) ON DELETE RESTRICT)`); checkErr != nil {
		t.Fatal(checkErr)
	}
	if _, checkErr := db.Exec(`INSERT INTO dependent VALUES ('bookmark-one')`); checkErr != nil {
		t.Fatal(checkErr)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, checkErr := ExportAndDrop(context.Background(), tx, spec, dataDir); checkErr == nil {
		t.Fatal("foreign-key-protected drop succeeded")
	}
	// Even a caller that commits after an error cannot publish an orphan receipt.
	if checkErr := tx.Commit(); checkErr != nil {
		t.Fatal(checkErr)
	}
	if bookmarkCount(t, db) != 2 {
		t.Fatal("failed drop lost source data")
	}
	var ledger bool
	if checkErr := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE name='plugin_core_exports')`).Scan(&ledger); checkErr != nil || ledger {
		t.Fatal("failed drop committed an import receipt")
	}
}

func TestCanceledExportCannotCommitAnImportReceipt(t *testing.T) {
	db, spec, dataDir := exportFixture(t)
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	publisher := func(dir, path string, raw []byte) error {
		if checkErr := publishSnapshot(dir, path, raw); checkErr != nil {
			return checkErr
		}
		cancel()
		return nil
	}
	if _, checkErr := exportAndDrop(ctx, tx, spec, dataDir, publisher); !errors.Is(checkErr, context.Canceled) {
		t.Fatalf("cancellation: %v", checkErr)
	}
	if checkErr := tx.Commit(); checkErr != nil {
		t.Fatal(checkErr)
	}
	if bookmarkCount(t, db) != 2 {
		t.Fatal("cancellation lost source rows")
	}
	var ledger bool
	if checkErr := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE name='plugin_core_exports')`).Scan(&ledger); checkErr != nil || ledger {
		t.Fatal("canceled export committed a receipt")
	}
}

func TestCoreIdentityAndMigrationTablesCannotBeExported(t *testing.T) {
	db, spec, dataDir := exportFixture(t)
	for _, table := range []string{"sessions", "messages", "agent_profiles", "agent_instances", "goose_db_version", "sqlite_master", "plugin_core_exports"} {
		tx, err := db.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		spec.Table = table
		if _, err := ExportAndDrop(context.Background(), tx, spec, dataDir); err == nil {
			t.Fatalf("core identity table %s accepted", table)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
	}
	if bookmarkCount(t, db) != 2 {
		t.Fatal("protected-table refusal touched feature data")
	}
}
