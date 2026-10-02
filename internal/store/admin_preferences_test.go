package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/pressly/goose/v3"
)

func adminPreferences(t *testing.T, s *Store) *AdminPreferences {
	t.Helper()
	p, err := s.GetAdminPreferences(context.Background())
	if err != nil {
		t.Fatalf("GetAdminPreferences: %v", err)
	}
	return p
}

func execPreferences(t *testing.T, s *Store, query string, args ...any) {
	t.Helper()
	if _, err := s.DB.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("preference operation: %v", err)
	}
}

func TestAdminPreferencesTriggers(t *testing.T) {
	for _, recursive := range []int{0, 1} {
		t.Run(fmt.Sprintf("recursive=%d", recursive), func(t *testing.T) {
			s := newSeededStore(t)
			execPreferences(t, s, fmt.Sprintf("PRAGMA recursive_triggers=%d", recursive))
			before := adminPreferences(t, s)
			legacy, err := s.GetUserSettings(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			// Exercise the older API's explicit-column whole-row UPDATE unchanged.
			if checkErr := s.UpdateUserSettings(context.Background(), legacy); checkErr != nil {
				t.Fatal(checkErr)
			}
			if adminPreferences(t, s).Version != before.Version {
				t.Fatal("unchanged legacy update churned token")
			}
			execPreferences(t, s, `UPDATE user_settings SET default_model = 'unrelated' WHERE id=1`)
			if adminPreferences(t, s).Version != before.Version {
				t.Fatal("unrelated setting churned token")
			}

			legacy.ToolStreamBehavior = "hidden"
			if checkErr := s.UpdateUserSettings(context.Background(), legacy); checkErr != nil {
				t.Fatal(checkErr)
			}
			changed := adminPreferences(t, s)
			if changed.ToolStreamBehavior != "hidden" || changed.Version == before.Version {
				t.Fatal("legacy preference write failed to invalidate token")
			}
			execPreferences(t, s, `UPDATE user_settings SET tool_drawer_retention=30 WHERE id=1`)
			retained := adminPreferences(t, s)
			if retained.ToolDrawerRetention != 30 || retained.Version == changed.Version {
				t.Fatal("retention write failed to invalidate token")
			}
			execPreferences(t, s, `UPDATE user_settings SET tool_stream_behavior=tool_stream_behavior, tool_drawer_retention=tool_drawer_retention WHERE id=1`)
			if adminPreferences(t, s).Version != retained.Version {
				t.Fatal("same-value SQL churned token")
			}

			// No trigger recursion/churn: exactly one row update plus its token update.
			var totalBefore, totalAfter int
			if checkErr := s.DB.QueryRow(`SELECT total_changes()`).Scan(&totalBefore); checkErr != nil {
				t.Fatal(checkErr)
			}
			execPreferences(t, s, `UPDATE user_settings SET tool_drawer_retention=60 WHERE id=1`)
			if checkErr := s.DB.QueryRow(`SELECT total_changes()`).Scan(&totalAfter); checkErr != nil {
				t.Fatal(checkErr)
			}
			if totalAfter-totalBefore != 2 {
				t.Fatalf("preference update affected %d rows, want 2", totalAfter-totalBefore)
			}

			oldToken := adminPreferences(t, s).Version
			execPreferences(t, s, `DELETE FROM user_settings WHERE id=1`)
			execPreferences(t, s, `INSERT INTO user_settings (id) VALUES (1)`)
			if adminPreferences(t, s).Version == oldToken {
				t.Fatal("reinsert reused token")
			}
		})
	}
}

func TestAdminPreferencesRollbackAndReadOnly(t *testing.T) {
	s := newSeededStore(t)
	ctx := context.Background()
	before := adminPreferences(t, s)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackUnlessCommitted(tx)
	if _, checkErr := tx.ExecContext(ctx, `UPDATE user_settings SET tool_stream_behavior='persist', tool_drawer_retention=-1 WHERE id=1`); checkErr != nil {
		t.Fatal(checkErr)
	}
	var stagedToken string
	if checkErr := tx.QueryRowContext(ctx, `SELECT admin_preferences_version FROM user_settings WHERE id=1`).Scan(&stagedToken); checkErr != nil {
		t.Fatal(checkErr)
	}
	if stagedToken == before.Version {
		t.Fatal("trigger did not stage a new version inside transaction")
	}
	if checkErr := tx.Rollback(); checkErr != nil {
		t.Fatal(checkErr)
	}
	if *adminPreferences(t, s) != *before {
		t.Fatal("rollback failed to preserve preferences and token")
	}
	var changesBefore, changesAfter int
	if checkErr := s.DB.QueryRow(`SELECT total_changes()`).Scan(&changesBefore); checkErr != nil {
		t.Fatal(checkErr)
	}
	for range 5 {
		adminPreferences(t, s)
	}
	if checkErr := s.DB.QueryRow(`SELECT total_changes()`).Scan(&changesAfter); checkErr != nil {
		t.Fatal(checkErr)
	}
	if changesAfter != changesBefore {
		t.Fatal("reads persisted data")
	}

	execPreferences(t, s, `UPDATE user_settings SET admin_preferences_version='' WHERE id=1`)
	if _, err := s.GetAdminPreferences(ctx); err == nil {
		t.Fatal("missing token did not fail closed")
	}
	var version string
	if checkErr := s.DB.QueryRow(`SELECT admin_preferences_version FROM user_settings WHERE id=1`).Scan(&version); checkErr != nil {
		t.Fatal(checkErr)
	}
	if version != "" {
		t.Fatal("read initialized token")
	}
	for _, invalid := range []string{"not-a-token", "0123456789012345678901234567890xyz"} {
		execPreferences(t, s, `UPDATE user_settings SET admin_preferences_version=? WHERE id=1`, invalid)
		if _, err := s.GetAdminPreferences(ctx); err == nil {
			t.Fatal("invalid token did not fail closed")
		}
	}
	execPreferences(t, s, `DELETE FROM user_settings WHERE id=1`)
	if _, err := s.GetAdminPreferences(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing singleton: %v", err)
	}
}

func TestAdminPreferencesConcurrentConnections(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "concurrent.db")
	s, err := New(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(ctx) })
	execPreferences(t, s, `INSERT INTO user_settings (id) VALUES (1)`)
	other, err := New(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { other.Close(ctx) })
	initial := adminPreferences(t, s)
	type pair struct {
		stream    string
		retention int
	}
	seen := map[string]pair{initial.Version: {initial.ToolStreamBehavior, initial.ToolDrawerRetention}}
	var mu sync.Mutex
	record := func(p *AdminPreferences) error {
		mu.Lock()
		defer mu.Unlock()
		value := pair{p.ToolStreamBehavior, p.ToolDrawerRetention}
		if old, ok := seen[p.Version]; ok && old != value {
			return errors.New("changed snapshot reused an observed token")
		}
		// Each writer updates both fields atomically; no torn combinations allowed.
		if value != (pair{"streaming", 15}) && value != (pair{"persist", 30}) && value != (pair{"hidden", 60}) {
			return errors.New("torn preference read")
		}
		seen[p.Version] = value
		return nil
	}
	var wg sync.WaitGroup
	failures := make(chan error, 3)
	start := make(chan struct{})
	for i, writer := range []*Store{s, other} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := range 80 {
				stream, retention := "persist", 30
				if (i+j)%2 == 0 {
					stream, retention = "hidden", 60
				}
				if _, err := writer.DB.ExecContext(ctx, `UPDATE user_settings SET tool_stream_behavior=?, tool_drawer_retention=? WHERE id=1`, stream, retention); err != nil {
					failures <- err
					return
				}
				p, err := writer.GetAdminPreferences(ctx)
				if err != nil {
					failures <- err
					return
				}
				if err := record(p); err != nil {
					failures <- err
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for range 160 {
			p, err := s.GetAdminPreferences(ctx)
			if err != nil {
				failures <- err
				return
			}
			if err := record(p); err != nil {
				failures <- err
				return
			}
		}
	}()
	close(start)
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if adminPreferences(t, s).Version == initial.Version {
		t.Fatal("writers did not invalidate stale read version")
	}
}

// Build an actual pre-168 SQLite DB through goose, close it, then upgrade its
// copy. This fixture complements (and does not replace) the real backup check.
func TestAdminPreferencesUpgradeAndReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "pre.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, checkErr := provider.UpTo(ctx, 167); checkErr != nil {
		t.Fatal(checkErr)
	}
	if _, checkErr := db.Exec(`INSERT INTO user_settings (id,tool_stream_behavior,tool_drawer_retention) VALUES (1,'persist',30)`); checkErr != nil {
		t.Fatal(checkErr)
	}
	if checkErr := db.Close(); checkErr != nil {
		t.Fatal(checkErr)
	}
	upgraded := filepath.Join(t.TempDir(), "upgraded.db")
	if checkErr := copyFile(upgraded, path); checkErr != nil {
		t.Fatal(checkErr)
	}
	s, err := New(ctx, upgraded)
	if err != nil {
		t.Fatal(err)
	}
	before := adminPreferences(t, s)
	if before.ToolStreamBehavior != "persist" || before.ToolDrawerRetention != 30 {
		t.Fatal("upgrade changed preferences")
	}
	// Existing-row Seed fast path and repeated bootstrap must preserve the token.
	if checkErr := s.Seed(ctx); checkErr != nil {
		t.Fatal(checkErr)
	}
	if checkErr := s.initializeAdminPreferencesVersion(ctx); checkErr != nil {
		t.Fatal(checkErr)
	}
	if *adminPreferences(t, s) != *before {
		t.Fatal("bootstrap/seed churned token")
	}
	if checkErr := s.Close(ctx); checkErr != nil {
		t.Fatal(checkErr)
	}
	s, err = New(ctx, upgraded)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(ctx)
	if *adminPreferences(t, s) != *before {
		t.Fatal("reopen changed token or preferences")
	}
}

// Optional operator artifact, never opened/migrated in place. Set an explicit
// immutable backup path; copy it and any WAL into the private test directory.
// The release verification must run this test without a skip.
func TestAdminPreferencesRealBackupUpgrade(t *testing.T) {
	source := os.Getenv("NANITE_ADMIN_TEST_BACKUP")
	if source == "" {
		t.Skip("NANITE_ADMIN_TEST_BACKUP not set; real upgrade not verified")
	}
	ctx := context.Background()
	copyPath := filepath.Join(t.TempDir(), "backup-copy.db")
	if checkErr := copyFile(copyPath, source); checkErr != nil {
		t.Fatal(checkErr)
	}
	if checkErr := os.Chmod(copyPath, 0600); checkErr != nil {
		t.Fatal(checkErr)
	}
	if info, statErr := os.Stat(source + "-wal"); statErr == nil && info.Size() > 0 { // #nosec G703 -- Explicit operator-selected immutable backup, read only; never request input.
		if checkErr := copyFile(copyPath+"-wal", source+"-wal"); checkErr != nil {
			t.Fatal(checkErr)
		}
		if checkErr := os.Chmod(copyPath+"-wal", 0600); checkErr != nil {
			t.Fatal(checkErr)
		}
	}
	db, err := sql.Open("sqlite", copyPath)
	if err != nil {
		t.Fatal(err)
	}
	// SELECT * comparison is test-only, keeping all pre-existing singleton
	// columns private and checking they survive unchanged. Never log their values.
	before := privateSettingsRow(t, db)
	if _, exists := before["admin_preferences_version"]; exists {
		t.Fatal("backup is already migrated")
	}
	if checkErr := db.Close(); checkErr != nil {
		t.Fatal(checkErr)
	}
	s, err := New(ctx, copyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(ctx)
	after := privateSettingsRow(t, s.DB)
	delete(after, "admin_preferences_version")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("upgrade changed existing singleton columns (values omitted)")
	}
	token := adminPreferences(t, s).Version
	if checkErr := s.initializeAdminPreferencesVersion(ctx); checkErr != nil {
		t.Fatal(checkErr)
	}
	if adminPreferences(t, s).Version != token {
		t.Fatal("real upgrade initialization was not idempotent")
	}
	// Optional independently compiled pre-migration source, exercising its
	// actual Store.New/Get/Update against this migrated copy (no server).
	if binary := os.Getenv("NANITE_ADMIN_TEST_OLD_BINARY"); binary != "" {
		command := exec.CommandContext(ctx, binary, "-test.run=^TestAdminOldBinaryCompatibility$", "-test.v") // #nosec G204 G702 -- Explicit operator-selected compatibility test executable; no shell or request input.
		command.Env = append(os.Environ(), "NANITE_ADMIN_OLD_BINARY_DB="+copyPath)
		output, runErr := command.CombinedOutput()
		if runErr != nil {
			t.Fatalf("pre-migration binary compatibility: %v\n%s", runErr, output)
		}
		t.Logf("%s", output)
		if adminPreferences(t, s).Version == token {
			t.Fatal("old binary changed preferences without invalidation")
		}
		token = adminPreferences(t, s).Version
	}
	legacy, err := s.GetUserSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.ToolDrawerRetention == 30 {
		legacy.ToolDrawerRetention = 60
	} else {
		legacy.ToolDrawerRetention = 30
	}
	if checkErr := s.UpdateUserSettings(ctx, legacy); checkErr != nil {
		t.Fatal(checkErr)
	}
	if adminPreferences(t, s).Version == token {
		t.Fatal("legacy explicit-column write failed to invalidate upgraded token")
	}
	t.Log("real populated pre-migration backup copy upgraded; existing singleton columns preserved; legacy read/write succeeded")
}

func privateSettingsRow(t *testing.T, db *sql.DB) map[string]any {
	t.Helper()
	rows, err := db.Query(`SELECT * FROM user_settings WHERE id=1`)
	if err != nil {
		t.Fatal(err)
	}
	defer closeRows(rows)
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatal("backup has no singleton row")
	}
	values := make([]any, len(columns))
	pointers := make([]any, len(columns))
	for i := range values {
		pointers[i] = &values[i]
	}
	if checkErr := rows.Scan(pointers...); checkErr != nil {
		t.Fatal(checkErr)
	}
	result := make(map[string]any, len(columns))
	for i, name := range columns {
		result[name] = values[i]
	}
	if rows.Next() {
		t.Fatal("unexpected extra singleton")
	}
	if checkErr := rows.Err(); checkErr != nil {
		t.Fatal(checkErr)
	}
	return result
}

func TestAdminPreferencesMigrationRoundTrip(t *testing.T) {
	s := newSeededStore(t)
	ctx := context.Background()
	execPreferences(t, s, `UPDATE user_settings SET tool_stream_behavior='hidden', tool_drawer_retention=60 WHERE id=1`)
	before := adminPreferences(t, s)
	shape := adminPreferencesSchemaShape(t, s.DB)
	dir, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, s.DB, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, checkErr := provider.DownTo(ctx, 167); checkErr != nil {
		t.Fatal(checkErr)
	}
	var stream string
	var retention int
	if checkErr := s.DB.QueryRow(`SELECT tool_stream_behavior, tool_drawer_retention FROM user_settings WHERE id=1`).Scan(&stream, &retention); checkErr != nil {
		t.Fatal(checkErr)
	}
	if stream != before.ToolStreamBehavior || retention != before.ToolDrawerRetention {
		t.Fatal("Down changed preferences")
	}
	if _, checkErr := provider.Up(ctx); checkErr != nil {
		t.Fatal(checkErr)
	}
	if !reflect.DeepEqual(shape, adminPreferencesSchemaShape(t, s.DB)) {
		t.Fatal("Up/Down/Up changed schema shape")
	}
	if checkErr := s.initializeAdminPreferencesVersion(ctx); checkErr != nil {
		t.Fatal(checkErr)
	}
	after := adminPreferences(t, s)
	if after.ToolStreamBehavior != stream || after.ToolDrawerRetention != retention {
		t.Fatal("re-Up changed preferences")
	}
	execPreferences(t, s, `UPDATE user_settings SET tool_stream_behavior='persist' WHERE id=1`)
	if adminPreferences(t, s).Version == after.Version {
		t.Fatal("re-Up did not restore version trigger")
	}
}

func adminPreferencesSchemaShape(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT name, type, "notnull", coalesce(dflt_value,''), pk FROM pragma_table_info('user_settings') ORDER BY cid`)
	if err != nil {
		t.Fatal(err)
	}
	var shape []string
	for rows.Next() {
		var name, kind, defaultValue string
		var notNull, primaryKey int
		if checkErr := rows.Scan(&name, &kind, &notNull, &defaultValue, &primaryKey); checkErr != nil {
			t.Fatal(checkErr)
		}
		shape = append(shape, fmt.Sprintf("%s/%s/%d/%s/%d", name, kind, notNull, defaultValue, primaryKey))
	}
	if checkErr := rows.Err(); checkErr != nil {
		t.Fatal(checkErr)
	}
	closeRows(rows)
	rows, err = db.Query(`SELECT name, sql FROM sqlite_master WHERE type='trigger' AND tbl_name='user_settings' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer closeRows(rows)
	for rows.Next() {
		var name, definition string
		if checkErr := rows.Scan(&name, &definition); checkErr != nil {
			t.Fatal(checkErr)
		}
		shape = append(shape, name+":"+definition)
	}
	if checkErr := rows.Err(); checkErr != nil {
		t.Fatal(checkErr)
	}
	return shape
}
