package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/hollis-labs/go-sqlite/sqlitekit"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"
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

// legacyMigrationCutoverVersion is the highest goose migration version that
// corresponds to a file converted from Nanite's pre-goose migration
// mechanism (see docs/engineering/architecture/05-storage-and-migrations.md,
// "Migrations: adopting a real ledger"). It is a fixed historical boundary,
// not "however many files are embedded right now" — every migration file
// numbered 1..legacyMigrationCutoverVersion existed, unchanged in effect, at
// the moment this codebase adopted goose, so every database that ran any
// pre-goose build of this codebase has already applied all of them via the
// old swallow-errors mechanism (every migration file re-ran, unconditionally,
// on every boot). seedLegacyLedger uses this constant to seed goose's ledger
// for such a database without re-executing their SQL. It must never be
// changed after the fact — new migrations just get numbered above it and
// goose applies them for real, the normal way.
const legacyMigrationCutoverVersion = 94

// migrate runs every pending migration via goose (github.com/pressly/goose/v3)
// against the embedded migrations/ directory.
//
// This replaces a custom runner that had no schema_migrations-equivalent
// ledger: every migration file re-executed in full on every single process
// boot, with idempotency achieved by swallowing specific SQL errors
// ("duplicate column", "no such column"/"no such table" on DDL statements,
// ALTER TABLE ... RENAME TO failures) plus an opt-in
// "migrate:skip-if-column-exists" directive for migrations that recreate a
// table to widen a CHECK constraint (SQLite has no ALTER-CHECK). That
// mechanism caused a real production crash-loop (CW-20260817): three
// migrations (019/065/067) each rebuilt subagent_runs from their own,
// mutually-unaware historical schema, and the fix at the time only guarded
// those three, leaving the identical rename-recreate-copy pattern in
// 043/089 unguarded. See docs/engineering/architecture/05-storage-and-migrations.md.
//
// Cutover for pre-existing databases: goose has no first-class documented
// mechanism for adopting it against a database that already has its schema
// applied outside goose's own ledger (checked goose's own docs site and
// bundled README — there is no baseline/adopt command). The approach used
// here — and the standard community workaround for this scenario — is to
// seed goose's own version table directly via its public database.Store
// API, marking every migration up to legacyMigrationCutoverVersion as
// already applied without executing their SQL, so goose does not attempt to
// redo work a pre-existing database already has. A genuinely fresh database
// has neither a goose ledger nor any of Nanite's application tables, so it
// skips seeding entirely and runs every migration for real, starting from
// version 1.
func (s *Store) migrate() error {
	ctx := context.Background()

	migrationsDir, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("sub migrations fs: %w", err)
	}

	preExisting, err := s.isPreGooseDatabase(ctx)
	if err != nil {
		return fmt.Errorf("detect pre-goose database: %w", err)
	}
	if preExisting {
		if err := s.seedLegacyLedger(ctx); err != nil {
			return fmt.Errorf("seed legacy migration ledger: %w", err)
		}
	}

	provider, err := goose.NewProvider(goose.DialectSQLite3, s.DB, migrationsDir, goose.WithVerbose(false))
	if err != nil {
		return fmt.Errorf("create goose provider: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}

// isPreGooseDatabase reports whether this database already has Nanite's
// application schema (created by the old, pre-goose migration runner)
// without yet having a goose ledger of its own — i.e. this is an existing
// deployment being cut over to goose for the first time, not a genuinely
// fresh database and not a database that was already cut over on a prior
// boot.
func (s *Store) isPreGooseDatabase(ctx context.Context) (bool, error) {
	gooseTable, err := s.tableExists(ctx, goose.DefaultTablename)
	if err != nil {
		return false, err
	}
	if gooseTable {
		// Already has a goose ledger (either cut over on a prior boot, or
		// created fresh under goose from the start) — nothing to seed.
		return false, nil
	}
	// sessions is created by migration 001 and has never been dropped or
	// renamed by any later migration — a reliable signal that this database
	// already ran the old migration mechanism to completion, as opposed to
	// a genuinely fresh, empty database.
	return s.tableExists(ctx, "sessions")
}

func (s *Store) tableExists(ctx context.Context, name string) (bool, error) {
	var count int
	err := s.DB.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, name,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check table %q: %w", name, err)
	}
	return count > 0, nil
}

// seedLegacyLedger marks every migration from 1 through
// legacyMigrationCutoverVersion as already applied, without executing their
// SQL, using goose's own database.Store implementation so the seeded ledger
// has exactly the shape goose itself would create (same table, same
// bootstrap version-0 row that goose's lazy table-creation path would
// otherwise insert).
func (s *Store) seedLegacyLedger(ctx context.Context) error {
	verStore, err := database.NewStore(database.DialectSQLite3, goose.DefaultTablename)
	if err != nil {
		return fmt.Errorf("create goose version store: %w", err)
	}
	if err := verStore.CreateVersionTable(ctx, s.DB); err != nil {
		return fmt.Errorf("create goose version table: %w", err)
	}
	if err := verStore.Insert(ctx, s.DB, database.InsertRequest{Version: 0}); err != nil {
		return fmt.Errorf("insert goose bootstrap version: %w", err)
	}
	for v := int64(1); v <= legacyMigrationCutoverVersion; v++ {
		if err := verStore.Insert(ctx, s.DB, database.InsertRequest{Version: v}); err != nil {
			return fmt.Errorf("seed legacy migration version %d as applied: %w", v, err)
		}
	}
	return nil
}
