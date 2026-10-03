package store

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
)

func TestMigration172PreservesCatalogsAndSettings(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	provider := migration147Provider(t, s)
	if _, err := provider.DownTo(ctx, 171); err != nil {
		t.Fatal(err)
	}
	source, createErr := s.CreateCatalogSource(ctx, "Operator Catalog", "https://example.com/plugins.json", "custom", 73)
	if createErr != nil {
		t.Fatal(createErr)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE catalog_sources SET public_key = 'retired-key', enabled = 0 WHERE id = ?`, source.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO user_settings (id) VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE user_settings SET allow_unsigned_plugins = 1, default_model = 'operator-model', settings = '{"plugin_config":{"example":{"enabled":true}}}' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	beforeSources := signingMigrationRows(t, s.DB, "catalog_sources", "public_key")
	beforeSettings := signingMigrationRows(t, s.DB, "user_settings", "allow_unsigned_plugins")
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`SELECT public_key FROM catalog_sources`, `SELECT allow_unsigned_plugins FROM user_settings`} {
		rows, err := s.DB.QueryContext(ctx, query)
		if err == nil {
			rows.Close()
			t.Fatalf("retired column survived: %s", query)
		}
	}
	if got := signingMigrationRows(t, s.DB, "catalog_sources", "public_key"); !reflect.DeepEqual(beforeSources, got) {
		t.Fatalf("catalog data changed: before=%v after=%v", beforeSources, got)
	}
	if got := signingMigrationRows(t, s.DB, "user_settings", "allow_unsigned_plugins"); !reflect.DeepEqual(beforeSettings, got) {
		t.Fatalf("settings data changed: before=%v after=%v", beforeSettings, got)
	}
	if _, err := s.GetCatalogSource(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	settings, settingsErr := s.GetUserSettings(ctx)
	if settingsErr != nil {
		t.Fatal(settingsErr)
	}
	if settings.DefaultModel != "operator-model" {
		t.Fatalf("model = %q", settings.DefaultModel)
	}
	if err := s.UpdateUserSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(ctx, 171); err != nil {
		t.Fatal(err)
	}
	var key string
	if err := s.DB.QueryRowContext(ctx, `SELECT public_key FROM catalog_sources WHERE id = ?`, source.ID).Scan(&key); err != nil {
		t.Fatal(err)
	}
	var bypass int
	if err := s.DB.QueryRowContext(ctx, `SELECT allow_unsigned_plugins FROM user_settings WHERE id = 1`).Scan(&bypass); err != nil {
		t.Fatal(err)
	}
	if key != "" || bypass != 0 {
		t.Fatalf("rollback revived retired signing state: %q, %d", key, bypass)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	assertGooseHasNothingPending(t, s)
	var integrity string
	if err := s.DB.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity = %q: %v", integrity, err)
	}
}

// Compare every surviving field rather than only the model projection.
func signingMigrationRows(t *testing.T, db *sql.DB, table, retired string) []map[string]any {
	t.Helper()
	var query string
	switch table {
	case "catalog_sources":
		query = `SELECT * FROM catalog_sources ORDER BY id`
	case "user_settings":
		query = `SELECT * FROM user_settings ORDER BY id`
	default:
		t.Fatalf("unsupported snapshot table %q", table)
	}
	rows, err := db.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var result []map[string]any
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		record := map[string]any{}
		for i, column := range columns {
			if column != retired {
				record[column] = values[i]
			}
		}
		result = append(result, record)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}
