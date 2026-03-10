package store

import (
	"database/sql"
	"embed"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store wraps the SQLite database connection.
type Store struct {
	DB *sql.DB
}

// New opens a SQLite database at dbPath and runs all embedded migrations.
func New(dbPath string) (*Store, error) {
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

	s := &Store{DB: db}
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
