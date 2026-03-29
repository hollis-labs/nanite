package store

import (
	"database/sql"
	"embed"
	"fmt"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store wraps the SQLite database connection.
type Store struct {
	DB     *sql.DB
	dbPath string
}

// DBPath returns the path to the SQLite database file.
func (s *Store) DBPath() string {
	return s.dbPath
}

// New opens a SQLite database at dbPath and runs all embedded migrations.
// The dbPath is resolved to an absolute path so that DBPath() is safe
// to use from subprocesses running in different working directories.
func New(dbPath string) (*Store, error) {
	absPath, err := filepath.Abs(dbPath)
	if err != nil {
		return nil, fmt.Errorf("resolve db path: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	// Enable WAL mode and foreign keys.
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("exec %s: %w", pragma, err)
		}
	}

	s := &Store{DB: db, dbPath: absPath}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.DB.Close()
}

func (s *Store) migrate() error {
	files := []string{
		"migrations/001_initial.sql",
		"migrations/002_add_session_tags.sql",
		"migrations/003_add_templates.sql",
		"migrations/004_add_skills.sql",
		"migrations/005_add_prompt_templates.sql",
		"migrations/006_add_modes.sql",
		"migrations/007_add_mcp_servers.sql",
		"migrations/008_add_cache_tokens.sql",
		"migrations/009_add_a2a_messages.sql",
		"migrations/010_add_pty_provider.sql",
		"migrations/011_add_codex_gemini_providers.sql",
		"migrations/012_add_user_settings.sql",
		"migrations/013_add_execution_metrics.sql",
		"migrations/014_add_plugin_settings.sql",
		"migrations/015_extend_artifacts.sql",
		"migrations/016_add_settings_mode_flags.sql",
		"migrations/017_agent_schema_v2.sql",
		"migrations/018_frontend_unblock.sql",
		"migrations/019_connector_triggers.sql",
		"migrations/020_custom_actions.sql",
	}

	for _, f := range files {
		data, err := migrationsFS.ReadFile(f)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", f, err)
		}

		statements := strings.Split(string(data), ";")
		for _, stmt := range statements {
			stmt = strings.TrimSpace(stmt)
			if stmt == "" {
				continue
			}
			if _, err := s.DB.Exec(stmt); err != nil {
				// SQLite ALTER TABLE ADD COLUMN fails with "duplicate column"
				// if the column already exists; treat as idempotent.
				if strings.Contains(err.Error(), "duplicate column") {
					continue
				}
				return fmt.Errorf("exec migration statement: %w\nSQL: %s", err, stmt)
			}
		}
	}
	return nil
}
