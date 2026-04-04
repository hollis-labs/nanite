package store

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
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
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	// Sort alphabetically so numbered prefixes determine order.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	var files []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		files = append(files, "migrations/"+e.Name())
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
