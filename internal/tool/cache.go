package tool

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	toolresult "github.com/hollis-labs/go-toolresult"
	"github.com/hollis-labs/go-toolresult/sqlstore"
	"github.com/oklog/ulid/v2"
)

// ResultCache composes shared result recovery with Nanite's redacted argument audit.
// Results uses the same database and existing session-scoped cache table.
type ResultCache struct {
	Results         *toolresult.Cache
	db              *sql.DB
	cacheTTLSeconds int
	argBudgetBytes  int
	argRedactor     ArgumentRedactor
}

// ResultCacheConfig combines result budgets with argument-audit policy.
type ResultCacheConfig struct {
	SoftTruncBytes  int
	HardCapBytes    int
	CacheTTLSeconds int
	// ArgumentBudgetBytes bounds each persisted tool-call argument document.
	ArgumentBudgetBytes int
	// ArgumentRedactor overrides the default name-pattern redaction policy.
	ArgumentRedactor ArgumentRedactor
}

func NewResultCache(db *sql.DB, cfg ResultCacheConfig) *ResultCache {
	ttl := cfg.CacheTTLSeconds
	if ttl <= 0 {
		ttl = int(toolresult.DefaultTTL / time.Second)
	}
	argBudget := cfg.ArgumentBudgetBytes
	if argBudget <= 0 {
		argBudget = DefaultArgumentBudgetBytes
	}
	return &ResultCache{
		Results: toolresult.New(sqlstore.New(db, sqlstore.Table{}), toolresult.Config{
			DefaultBudget: cfg.SoftTruncBytes, HardCapBytes: cfg.HardCapBytes,
			TTL: time.Duration(ttl) * time.Second,
		}),
		db: db, cacheTTLSeconds: ttl, argBudgetBytes: argBudget, argRedactor: cfg.ArgumentRedactor,
	}
}

// Purge removes expired result and argument rows under the host's retention policy.
func (c *ResultCache) Purge() (int, error) { return PurgeExpired(c.db) }

// PurgeExpired deletes expired tool_result_cache and tool_call_arguments rows.
// Retention is each row's own expires_at, set at insert from the configured
// tool_result_cache_ttl_seconds, so a purge never removes a row the cache would
// still serve. It needs only the database, which lets the server's background
// worker run it without constructing a cache. Returns the rows deleted.
func PurgeExpired(db *sql.DB) (int, error) {
	if db == nil {
		return 0, errors.New("cache purge: no database")
	}
	now := time.Now().UTC()
	n, err := sqlstore.New(db, sqlstore.Table{}).DeleteExpired(context.Background(), now)
	if err != nil {
		return 0, fmt.Errorf("cache purge: %w", err)
	}
	args, err := db.Exec(`DELETE FROM tool_call_arguments WHERE expires_at < ?`, now.Format(time.RFC3339))
	if err != nil {
		return n, fmt.Errorf("cache purge arguments: %w", err)
	}
	m, _ := args.RowsAffected()
	n += int(m)
	if n > 0 {
		slog.Info("tool-cache: purged expired entries", "count", n)
	}
	return n, nil
}

// RunPurgeLoop purges expired rows once immediately and then every interval
// until ctx is canceled, at which point it returns. Blocking; the server runs
// it on its lifecycle manager. A failed purge is logged and the loop continues.
func RunPurgeLoop(ctx context.Context, db *sql.DB, interval time.Duration) {
	purge := func() {
		if _, err := PurgeExpired(db); err != nil {
			slog.Warn("tool-cache purge failed", "err", err)
		}
	}
	purge()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			purge()
		}
	}
}

// NewCallID returns a fresh, unique id for a tool call that has none of its own.
func NewCallID() string { return newULID() }

func newULID() string {
	return ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}
