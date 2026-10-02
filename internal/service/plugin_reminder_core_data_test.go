package service

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

func seedCoreReminderTransfer(t *testing.T, st *store.Store) string {
	t.Helper()
	ctx := context.Background()
	session := &store.Session{Title: "Reminder transfer"}
	if checkErr := st.CreateSession(ctx, session); checkErr != nil {
		t.Fatal(checkErr)
	}
	if _, checkErr := st.DB.ExecContext(ctx, `ALTER TABLE reminders ADD COLUMN future_metadata BLOB`); checkErr != nil {
		t.Fatal(checkErr)
	}
	if _, checkErr := st.DB.ExecContext(ctx, `INSERT INTO reminders(id,session_id,scope,project_id,text,trigger_json,fired_at,created_at,updated_at,future_metadata) VALUES (?,?,?,?,?,?,?,?,?,?)`, "kept-reminder", session.ID, "session", nil, "Original text", `{"type":"turn_count","n":2}`, nil, "original created", "original updated", []byte{0, 255, 1}); checkErr != nil {
		t.Fatal(checkErr)
	}
	return session.ID
}
func TestPluginCoreDataRemindersTransferAndFailure(t *testing.T) {
	ctx := context.Background()
	st := newConfigTestStore(t)
	session := seedCoreReminderTransfer(t, st)
	directory := t.TempDir()
	adopter := NewPluginCoreData(st)
	adopter.dataDir = func(string) (string, error) { return directory, nil }
	if checkErr := adopter.AdoptPluginCoreData(ctx, "other.reminders"); checkErr != nil {
		t.Fatal(checkErr)
	}
	var count int
	if checkErr := st.DB.QueryRowContext(ctx, `SELECT count(*) FROM reminders`).Scan(&count); checkErr != nil || count != 1 {
		t.Fatal("foreign owner changed source", count, checkErr)
	}
	blocked := filepath.Join(directory, "blocked")
	if checkErr := os.WriteFile(blocked, []byte("sentinel"), 0600); checkErr != nil {
		t.Fatal(checkErr)
	}
	adopter.dataDir = func(string) (string, error) { return blocked, nil }
	if checkErr := adopter.AdoptPluginCoreData(ctx, "nanite.reminders"); checkErr == nil {
		t.Fatal("failed export accepted")
	}
	if checkErr := st.DB.QueryRowContext(ctx, `SELECT count(*) FROM reminders`).Scan(&count); checkErr != nil || count != 1 {
		t.Fatal("failed export lost rows", checkErr)
	}
	receipts, err := st.ReadPluginExportReceipts(ctx, "nanite.reminders", 100)
	if err != nil || len(receipts) != 0 {
		t.Fatal("failed export committed receipt", receipts, err)
	}
	adopter.dataDir = func(string) (string, error) { return directory, nil }
	if err = adopter.AdoptPluginCoreData(ctx, "nanite.reminders"); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err = st.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE name='reminders')`).Scan(&exists); err != nil || exists {
		t.Fatal("source table retained", exists, err)
	}
	receipts, err = st.ReadPluginExportReceipts(ctx, "nanite.reminders", 100)
	if err != nil || len(receipts) != 1 {
		t.Fatal("receipt missing", receipts, err)
	}
	receipt := receipts[0]
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	file, err := root.Open(receipt.Path)
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
	index := map[string]int{}
	for i, column := range exported.Snapshot.Columns {
		index[column] = i
	}
	row := exported.Snapshot.Rows[0]
	for key, want := range map[string]pluginapi.DataCell{"id": {Kind: "text", Text: "kept-reminder"}, "session_id": {Kind: "text", Text: session}, "text": {Kind: "text", Text: "Original text"}, "fired_at": {Kind: "null"}, "project_id": {Kind: "null"}, "created_at": {Kind: "text", Text: "original created"}, "updated_at": {Kind: "text", Text: "original updated"}, "future_metadata": {Kind: "blob", Text: "AP8B"}} {
		if !reflect.DeepEqual(row[index[key]], want) {
			t.Fatal("typed cell changed", key, row[index[key]], want)
		}
	}
	if err = adopter.AdoptPluginCoreData(ctx, "nanite.reminders"); err != nil {
		t.Fatal("reconnect", err)
	}
	// The independently owned bookmarks feature remains core until it activates.
	if err = st.DB.QueryRowContext(ctx, `SELECT count(*) FROM bookmarks`).Scan(&count); err != nil {
		t.Fatal("cross-feature table changed", err)
	}
}
