package store

import (
	"context"
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

	// Run every migration statement on a single dedicated connection.
	// SQLite PRAGMAs (notably foreign_keys) are per-connection, so
	// dispersing statements across pool connections breaks any migration
	// that toggles PRAGMA state around a DDL block (e.g. 008, 019).
	ctx := context.Background()
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration conn: %w", err)
	}
	defer conn.Close()

	// The pool-level PRAGMAs set in New() ran on an arbitrary pool conn,
	// not this one. Re-apply the required defaults here so migrations run
	// with the same invariants the app expects (and leave this conn in a
	// known state before it returns to the pool).
	for _, pragma := range []string{
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := conn.ExecContext(ctx, pragma); err != nil {
			return fmt.Errorf("exec %s on migration conn: %w", pragma, err)
		}
	}

	for _, f := range files {
		data, err := migrationsFS.ReadFile(f)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", f, err)
		}

		statements := splitSQL(string(data))
		for _, stmt := range statements {
			stmt = strings.TrimSpace(stmt)
			if stmt == "" {
				continue
			}
			if _, err := conn.ExecContext(ctx, stmt); err != nil {
				// SQLite ALTER TABLE ADD COLUMN fails with "duplicate column"
				// if the column already exists; treat as idempotent.
				if strings.Contains(err.Error(), "duplicate column") {
					continue
				}
				// SQLite ALTER TABLE DROP COLUMN fails with "no such column"
				// if the column was already dropped; treat as idempotent.
				// CREATE INDEX can also reference a column that a later migration
				// dropped (e.g. migration 001 creates idx_a2a_inbox on to_agent,
				// which migration 005 drops). Both are DDL-level idempotency cases.
				// Gate this on DDL-only statements so DML typos (SELECT, UPDATE,
				// INSERT, DELETE) are never silently swallowed.
				upper := strings.ToUpper(stmt)
				isDDLIdempotent := strings.Contains(upper, "DROP COLUMN") ||
					strings.Contains(upper, "CREATE INDEX")
				if isDDLIdempotent && strings.Contains(err.Error(), "no such column") {
					continue
				}
				// ALTER TABLE ... RENAME TO is idempotent if the rename already
				// happened. Two error shapes indicate this:
				//   - "no such table: <src>" — source already renamed away.
				//   - "already another table ... <dst>" — destination exists.
				// Swallow both so migrations can re-run on every boot cleanly
				// (no schema_migrations table in this codebase).
				if strings.Contains(upper, "ALTER TABLE") && strings.Contains(upper, "RENAME TO") {
					msg := err.Error()
					if strings.Contains(msg, "no such table") ||
						strings.Contains(msg, "already another table") {
						continue
					}
				}
				return fmt.Errorf("exec migration statement: %w\nSQL: %s", err, stmt)
			}
		}
	}
	return nil
}

// splitSQL splits a SQL script on semicolons while keeping BEGIN...END blocks
// (used by triggers) intact as single statements.
func splitSQL(sql string) []string {
	var stmts []string
	var buf strings.Builder
	depth := 0
	for _, raw := range strings.Split(sql, ";") {
		upper := strings.ToUpper(strings.TrimSpace(raw))
		// Track BEGIN/END nesting so semicolons inside triggers don't split.
		depth += strings.Count(upper, "BEGIN") - strings.Count(upper, "END")
		if buf.Len() > 0 {
			buf.WriteByte(';')
		}
		buf.WriteString(raw)
		if depth <= 0 {
			stmts = append(stmts, buf.String())
			buf.Reset()
			depth = 0
		}
	}
	if buf.Len() > 0 {
		stmts = append(stmts, buf.String())
	}
	return stmts
}
