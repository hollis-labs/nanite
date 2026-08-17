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

	"github.com/hollis-labs/go-sqlite/sqlitekit"
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
//
// The opener is sqlitekit.OpenSingle with WriterOptions: single-connection
// pool, DSN-level pragmas (WAL + foreign_keys + busy_timeout(5s) +
// synchronous(NORMAL) + temp_store(memory) + mmap_size(30 GB) +
// journal_size_limit(64 MiB) + _txlock=immediate) applied on every
// connection modernc opens.
func New(ctx context.Context, dbPath string) (*Store, error) {
	absPath, err := filepath.Abs(dbPath)
	if err != nil {
		return nil, fmt.Errorf("resolve db path: %w", err)
	}

	db, err := sqlitekit.OpenSingle(ctx, absPath, sqlitekit.OpenOptions{
		Options:         sqlitekit.WriterOptions(),
		CreateParentDir: true,
	})
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
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

	ctx := context.Background()
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration conn: %w", err)
	}
	defer conn.Close()

	for _, f := range files {
		data, err := migrationsFS.ReadFile(f)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", f, err)
		}
		text := string(data)

		// migrate:skip-if-column-exists lets a "recreate the table" migration
		// (SQLite can't ALTER a CHECK constraint, so widening one means
		// dropping/rebuilding the whole table — see migrations 019/065/067)
		// skip itself once its target table has already moved past it.
		// Without this, such a migration blindly rebuilds the table from its
		// OWN historical, narrower column/CHECK set on every single boot (no
		// schema_migrations table in this codebase — every file re-runs every
		// time), silently dropping any column a later migration already added
		// and resetting it to that later migration's default, and potentially
		// hard-failing outright if live data already carries a status value
		// only a later migration's CHECK permits (CW-20260817: subagent_runs
		// crash-looped on a real 'stalled' row against migration 019's
		// original 7-value CHECK once the reaper started using values 065
		// added). On a genuinely fresh database the marker column doesn't
		// exist yet, so this is a no-op and the migration runs exactly as
		// before.
		if table, column, ok := migrationSkipIfColumnDirective(text); ok {
			exists, err := columnExists(ctx, conn, table, column)
			if err != nil {
				return fmt.Errorf("check skip-if-column-exists for %s: %w", f, err)
			}
			if exists {
				continue
			}
		}

		statements := splitSQL(text)
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

// migrateSkipIfColumnDirective is the leading-comment marker a migration
// file can carry to opt into the skip-if-column-exists check in migrate().
// Must be the file's first line, exactly: "-- migrate:skip-if-column-exists
// <table> <column>".
const migrateSkipIfColumnDirective = "-- migrate:skip-if-column-exists "

// migrationSkipIfColumnDirective parses the directive described above from
// a migration file's contents. Returns ok=false if the file doesn't start
// with the marker.
func migrationSkipIfColumnDirective(text string) (table, column string, ok bool) {
	first, _, _ := strings.Cut(text, "\n")
	first = strings.TrimSpace(first)
	if !strings.HasPrefix(first, migrateSkipIfColumnDirective) {
		return "", "", false
	}
	fields := strings.Fields(strings.TrimPrefix(first, migrateSkipIfColumnDirective))
	if len(fields) != 2 {
		return "", "", false
	}
	return fields[0], fields[1], true
}

// columnExists reports whether table has a column named column, via
// PRAGMA table_info (SQLite has no parameterized form of PRAGMA, so table
// is interpolated directly — safe here because it only ever comes from this
// package's own embedded migration files, never external input).
func columnExists(ctx context.Context, conn *sql.Conn, table, column string) (bool, error) {
	rows, err := conn.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
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
