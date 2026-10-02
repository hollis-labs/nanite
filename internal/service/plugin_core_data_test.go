package service

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/hollis-labs/nanite/internal/plugin/dataexport"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

func seedCoreBookmarkTransfer(t *testing.T, st *store.Store) (string, string) {
	t.Helper()
	ctx := context.Background()
	session := &store.Session{Title: "Core transfer"}
	if checkErr := st.CreateSession(ctx, session); checkErr != nil {
		t.Fatal(checkErr)
	}
	message := &store.Message{SessionID: session.ID, Role: "assistant", Content: "Core content stays core"}
	if checkErr := st.CreateMessage(ctx, message); checkErr != nil {
		t.Fatal(checkErr)
	}
	if _, checkErr := st.DB.Exec(`ALTER TABLE bookmarks ADD COLUMN future_metadata BLOB`); checkErr != nil {
		t.Fatal(checkErr)
	}
	if _, checkErr := st.DB.Exec(`INSERT INTO bookmarks(id,message_id,session_id,note,tags,created_at,future_metadata) VALUES (?,?,?,?,?,?,?)`, "kept-bookmark", message.ID, session.ID, nil, `["star"]`, "2026-01-02 03:04:05.678+02:00", []byte{0, 255, 1}); checkErr != nil {
		t.Fatal(checkErr)
	}
	return session.ID, message.ID
}
func TestPluginCoreDataBookmarksAtomicTransferAndMovedDatabase(t *testing.T) {
	ctx := context.Background()
	st := newConfigTestStore(t)
	session, message := seedCoreBookmarkTransfer(t, st)
	directory := t.TempDir()
	adopter := NewPluginCoreData(st)
	adopter.dataDir = func(string) (string, error) { return directory, nil }
	// A plugin cannot select an arbitrary core table or alias the allowed owner.
	if checkErr := adopter.AdoptPluginCoreData(ctx, "other.bookmarks"); checkErr != nil {
		t.Fatal(checkErr)
	}
	var count int
	if checkErr := st.DB.QueryRow(`SELECT count(*) FROM bookmarks`).Scan(&count); checkErr != nil || count != 1 {
		t.Fatal("foreign owner changed core table")
	}
	if checkErr := adopter.AdoptPluginCoreData(ctx, "nanite.bookmarks"); checkErr != nil {
		t.Fatal(checkErr)
	}
	var exists bool
	if checkErr := st.DB.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE name='bookmarks')`).Scan(&exists); checkErr != nil || exists {
		t.Fatal("core table not retired")
	}
	receipts, err := st.ReadPluginExportReceipts(ctx, "nanite.bookmarks", 100)
	if err != nil || len(receipts) != 1 {
		t.Fatalf("receipt not committed: %#v %v", receipts, err)
	}
	source, err := dataexport.SourceID(st.DBPath(ctx))
	if err != nil {
		t.Fatal(err)
	}
	receipt := receipts[0]
	if receipt.SourceID != source || receipt.RowCount != 1 {
		t.Fatal("workspace or row count changed")
	}
	dataRoot, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dataRoot.Close() }()
	file, err := dataRoot.Open(receipt.Path)
	if err != nil {
		t.Fatal(err)
	}
	exported, err := pluginapi.DecodeDataExport(file)
	if closeErr := file.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	verified := pluginapi.DataExportReceipt{PluginID: receipt.PluginID, Feature: receipt.Feature, SourceID: receipt.SourceID, Path: receipt.Path, SHA256: receipt.SHA256, RowCount: receipt.RowCount}
	if err = verified.Verify(exported); err != nil {
		t.Fatal(err)
	}
	values := exported.Snapshot.Rows[0]
	want := []pluginapi.DataCell{{Kind: "text", Text: "kept-bookmark"}, {Kind: "text", Text: message}, {Kind: "text", Text: session}, {Kind: "null"}, {Kind: "text", Text: `["star"]`}, {Kind: "text", Text: "2026-01-02 03:04:05.678+02:00"}, {Kind: "blob", Text: "AP8B"}}
	if !reflect.DeepEqual(values, want) {
		t.Fatalf("typed row changed: %#v", values)
	}
	if err = adopter.AdoptPluginCoreData(ctx, "nanite.bookmarks"); err != nil {
		t.Fatal(err)
	}
	// The database ledger, rather than a recomputed current path, remains the
	// import authority after a workspace is moved.
	moved := filepath.Join(t.TempDir(), "moved.db")
	if _, err = st.DB.ExecContext(ctx, `VACUUM INTO ?`, moved); err != nil {
		t.Fatal(err)
	}
	movedStore := newConfigTestStoreAt(t, moved)
	movedAdopter := NewPluginCoreData(movedStore)
	movedAdopter.dataDir = adopter.dataDir
	if err = movedAdopter.AdoptPluginCoreData(ctx, "nanite.bookmarks"); err != nil {
		t.Fatal(err)
	}
	movedReceipts, err := movedStore.ReadPluginExportReceipts(ctx, "nanite.bookmarks", 100)
	if err != nil || len(movedReceipts) != 1 || movedReceipts[0].SourceID != source {
		t.Fatal("moved DB created another source")
	}
}
func TestPluginCoreDataExportFailureAndUnrelatedLoadsPreserveRows(t *testing.T) {
	ctx := context.Background()
	st := newConfigTestStore(t)
	seedCoreBookmarkTransfer(t, st)
	directory := filepath.Join(t.TempDir(), "not-a-directory")
	if checkErr := os.WriteFile(directory, []byte("unchanged"), 0600); checkErr != nil {
		t.Fatal(checkErr)
	}
	adopter := NewPluginCoreData(st)
	adopter.dataDir = func(string) (string, error) { return directory, nil }
	if checkErr := adopter.AdoptPluginCoreData(ctx, "nanite.bookmarks"); checkErr == nil {
		t.Fatal("failed export acknowledged")
	}
	var count int
	if checkErr := st.DB.QueryRow(`SELECT count(*) FROM bookmarks`).Scan(&count); checkErr != nil || count != 1 {
		t.Fatal("failed export dropped core rows")
	}
	receipts, err := st.ReadPluginExportReceipts(ctx, "nanite.bookmarks", 100)
	if err != nil || len(receipts) != 0 {
		t.Fatal("failed export committed a receipt")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err = adopter.AdoptPluginCoreData(canceled, "nanite.bookmarks"); err == nil {
		t.Fatal("canceled adoption succeeded")
	}
	if err = adopter.AdoptPluginCoreData(ctx, "nanite.example"); err != nil {
		t.Fatal(err)
	}
}
