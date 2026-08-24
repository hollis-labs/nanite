package store

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
)

const (
	legacyOfficialCatalogID  = "official"
	legacyOfficialCatalogURL = "https://raw.githubusercontent.com/hollis-labs/plugin-catalog/main/catalog.yaml"
)

func migration147Provider(t *testing.T, s *Store) *goose.Provider {
	t.Helper()
	migrationsDir, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("sub migrations fs: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, s.DB, migrationsDir, goose.WithVerbose(false))
	if err != nil {
		t.Fatalf("construct goose provider: %v", err)
	}
	return provider
}

func TestMigration147DownUpRemovesOnlyUntouchedLegacySeed(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	provider := migration147Provider(t, s)

	if _, err := provider.DownTo(ctx, 146); err != nil {
		t.Fatalf("goose DownTo 146: %v", err)
	}

	legacy, err := s.GetCatalogSource(ctx, legacyOfficialCatalogID)
	if err != nil {
		t.Fatalf("legacy source missing after Down: %v", err)
	}
	if legacy.Name != "Hollis Labs" || legacy.URL != legacyOfficialCatalogURL ||
		legacy.Type != "official" || !legacy.Enabled || legacy.Priority != 100 ||
		legacy.PublicKey != "" {
		t.Fatalf("Down recreated the wrong legacy source shape: %+v", legacy)
	}
	if !legacy.CreatedAt.Equal(legacy.UpdatedAt) {
		t.Fatalf("Down recreated a touched-looking source: created_at=%s updated_at=%s", legacy.CreatedAt, legacy.UpdatedAt)
	}

	unrelated, err := s.CreateCatalogSource(ctx, "Operator Catalog", "https://example.com/catalog.yaml", "custom", 25)
	if err != nil {
		t.Fatalf("create unrelated source: %v", err)
	}

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("goose Up: %v", err)
	}
	if _, err := s.GetCatalogSource(ctx, legacyOfficialCatalogID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("untouched legacy source survived Up: %v", err)
	}
	if got, err := s.GetCatalogSource(ctx, unrelated.ID); err != nil {
		t.Fatalf("unrelated source removed by Up: %v", err)
	} else if got.URL != unrelated.URL {
		t.Fatalf("unrelated source changed by Up: %+v", got)
	}
	assertGooseHasNothingPending(t, s)
}

func TestMigration147PreservesEveryChangedLegacySeedShape(t *testing.T) {
	tests := []struct {
		name       string
		updateSQL  string
		expectedID string
	}{
		{name: "different id", updateSQL: `UPDATE catalog_sources SET id = 'operator-official' WHERE id = 'official'`, expectedID: "operator-official"},
		{name: "different name", updateSQL: `UPDATE catalog_sources SET name = 'Operator Catalog' WHERE id = 'official'`, expectedID: legacyOfficialCatalogID},
		{name: "different url", updateSQL: `UPDATE catalog_sources SET url = 'https://example.com/operator-catalog.yaml' WHERE id = 'official'`, expectedID: legacyOfficialCatalogID},
		{name: "different type", updateSQL: `UPDATE catalog_sources SET type = 'custom' WHERE id = 'official'`, expectedID: legacyOfficialCatalogID},
		{name: "disabled", updateSQL: `UPDATE catalog_sources SET enabled = 0 WHERE id = 'official'`, expectedID: legacyOfficialCatalogID},
		{name: "reprioritized", updateSQL: `UPDATE catalog_sources SET priority = 50 WHERE id = 'official'`, expectedID: legacyOfficialCatalogID},
		{name: "keyed", updateSQL: `UPDATE catalog_sources SET public_key = 'abababababababababababababababababababababababababababababababab' WHERE id = 'official'`, expectedID: legacyOfficialCatalogID},
		{name: "updated", updateSQL: `UPDATE catalog_sources SET updated_at = '2030-01-01 00:00:00' WHERE id = 'official'`, expectedID: legacyOfficialCatalogID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestStore(t)
			ctx := context.Background()
			provider := migration147Provider(t, s)
			if _, err := provider.DownTo(ctx, 146); err != nil {
				t.Fatalf("goose DownTo 146: %v", err)
			}
			if _, err := s.DB.ExecContext(ctx, tt.updateSQL); err != nil {
				t.Fatalf("customize legacy source: %v", err)
			}

			if _, err := provider.Up(ctx); err != nil {
				t.Fatalf("goose Up: %v", err)
			}
			if _, err := s.GetCatalogSource(ctx, tt.expectedID); err != nil {
				t.Fatalf("changed legacy source was removed: %v", err)
			}
		})
	}
}

func TestMigration147DownDoesNotOverwriteExistingOfficialRow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	provider := migration147Provider(t, s)

	_, insertErr := s.DB.ExecContext(ctx, `
		INSERT INTO catalog_sources
		    (id, name, url, type, enabled, priority, public_key, created_at, updated_at)
		VALUES
		    ('official', 'Operator Catalog', 'https://example.com/operator.yaml',
		     'custom', 0, 7, 'abababababababababababababababababababababababababababababababab',
		     '2026-01-01 00:00:00', '2026-02-01 00:00:00')
	`)
	if insertErr != nil {
		t.Fatalf("insert operator-owned official row: %v", insertErr)
	}

	if _, err := provider.DownTo(ctx, 146); err != nil {
		t.Fatalf("goose DownTo 146: %v", err)
	}
	got, err := s.GetCatalogSource(ctx, legacyOfficialCatalogID)
	if err != nil {
		t.Fatalf("operator row missing after Down: %v", err)
	}
	if got.Name != "Operator Catalog" || got.URL != "https://example.com/operator.yaml" ||
		got.Type != "custom" || got.Enabled || got.Priority != 7 || got.PublicKey == "" {
		t.Fatalf("Down overwrote operator-owned row: %+v", got)
	}

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("goose Up: %v", err)
	}
	if _, err := s.GetCatalogSource(ctx, legacyOfficialCatalogID); err != nil {
		t.Fatalf("operator row missing after re-Up: %v", err)
	}
}

func copyProductionBackupForMigration147(t *testing.T) string {
	t.Helper()
	home, homeErr := os.UserHomeDir()
	if homeErr != nil {
		t.Skipf("cannot resolve home directory: %v", homeErr)
	}
	backupSrc := filepath.Join(home, ".local", "share", "nanite", "workspaces", "default", "backups",
		"main.db.pre-execution-backup-20260818-132726")
	if _, statErr := os.Stat(backupSrc); statErr != nil {
		t.Skipf("real backup db not present at %s: %v", backupSrc, statErr)
	}

	dstPath := filepath.Join(t.TempDir(), "main.db")
	if copyErr := copyFile(dstPath, backupSrc); copyErr != nil {
		t.Fatalf("copy real backup db to scratch path: %v", copyErr)
	}
	if walInfo, walStatErr := os.Stat(backupSrc + "-wal"); walStatErr == nil && walInfo.Size() > 0 {
		if copyErr := copyFile(dstPath+"-wal", backupSrc+"-wal"); copyErr != nil {
			t.Fatalf("copy real backup WAL to scratch path: %v", copyErr)
		}
	}
	absPath, pathErr := filepath.Abs(dstPath)
	if pathErr != nil {
		t.Fatalf("resolve scratch db path: %v", pathErr)
	}
	return absPath
}

func inspectProductionBackupBeforeMigration147(t *testing.T, absPath string) int {
	t.Helper()
	preMigrationDB, openErr := sql.Open("sqlite", absPath)
	if openErr != nil {
		t.Fatalf("open scratch backup before migration: %v", openErr)
	}
	var exactLegacyCount, sessionCountBefore int
	queryErr := preMigrationDB.QueryRow(`
		SELECT COUNT(*) FROM catalog_sources
		WHERE id = 'official'
		  AND name = 'Hollis Labs'
		  AND url = ?
		  AND type = 'official'
		  AND enabled = 1
		  AND priority = 100
		  AND public_key = ''
		  AND created_at = updated_at
	`, legacyOfficialCatalogURL).Scan(&exactLegacyCount)
	if queryErr != nil {
		_ = preMigrationDB.Close()
		t.Fatalf("inspect legacy source in scratch backup: %v", queryErr)
	}
	if sessionsErr := preMigrationDB.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&sessionCountBefore); sessionsErr != nil {
		_ = preMigrationDB.Close()
		t.Fatalf("count scratch backup sessions before migration: %v", sessionsErr)
	}
	if closeErr := preMigrationDB.Close(); closeErr != nil {
		t.Fatalf("close pre-migration scratch backup: %v", closeErr)
	}
	if exactLegacyCount != 1 {
		t.Fatalf("scratch production backup has %d exact untouched legacy sources, want 1", exactLegacyCount)
	}
	if sessionCountBefore == 0 {
		t.Fatal("scratch production backup has no sessions; expected populated production data")
	}
	return sessionCountBefore
}

func TestRealBackupMigration147RemovesUntouchedOfficialSource(t *testing.T) {
	absPath := copyProductionBackupForMigration147(t)
	sessionCountBefore := inspectProductionBackupBeforeMigration147(t, absPath)

	rs, openErr := New(context.Background(), absPath)
	if openErr != nil {
		t.Fatalf("open+migrate scratch production backup: %v", openErr)
	}
	t.Cleanup(func() {
		if err := rs.Close(context.Background()); err != nil {
			t.Errorf("close migrated scratch production backup: %v", err)
		}
	})

	var exactAfter, sessionCountAfter int
	if err := rs.DB.QueryRow(`SELECT COUNT(*) FROM catalog_sources WHERE id = 'official'`).Scan(&exactAfter); err != nil {
		t.Fatalf("count official source after migration: %v", err)
	}
	if exactAfter != 0 {
		t.Fatalf("official source count after migration = %d, want 0", exactAfter)
	}
	if err := rs.DB.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&sessionCountAfter); err != nil {
		t.Fatalf("count sessions after migration: %v", err)
	}
	if sessionCountAfter != sessionCountBefore {
		t.Fatalf("session count changed across migration: before=%d after=%d", sessionCountBefore, sessionCountAfter)
	}
	assertGooseHasNothingPending(t, rs)
}
