package tool

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

// DefaultSoftTruncBytes is the default byte threshold above which tool results
// are truncated for the LLM and the full body is cached.
const DefaultSoftTruncBytes = 64 * 1024 // 64 KiB

// DefaultHardCapBytes is the maximum body size stored in the cache. Results
// exceeding this are stored as metadata-only (body=NULL).
const DefaultHardCapBytes = 1024 * 1024 // 1 MiB

// DefaultCacheTTLSeconds is the default time-to-live for cached results.
const DefaultCacheTTLSeconds = 3600

// ResultCache stores and retrieves large tool results using the
// tool_result_cache table.
type ResultCache struct {
	db              *sql.DB
	softTruncBytes  int
	hardCapBytes    int
	cacheTTLSeconds int
}

// ResultCacheConfig configures the result cache thresholds. Zero values fall
// back to package defaults.
type ResultCacheConfig struct {
	SoftTruncBytes  int
	HardCapBytes    int
	CacheTTLSeconds int
}

// NewResultCache creates a ResultCache backed by the given DB connection.
func NewResultCache(db *sql.DB, cfg ResultCacheConfig) *ResultCache {
	soft := cfg.SoftTruncBytes
	if soft <= 0 {
		soft = DefaultSoftTruncBytes
	}
	hard := cfg.HardCapBytes
	if hard <= 0 {
		hard = DefaultHardCapBytes
	}
	ttl := cfg.CacheTTLSeconds
	if ttl <= 0 {
		ttl = DefaultCacheTTLSeconds
	}
	return &ResultCache{
		db:              db,
		softTruncBytes:  soft,
		hardCapBytes:    hard,
		cacheTTLSeconds: ttl,
	}
}

// StoreResult persists a tool result and returns the LLM-visible string.
// If the result is under the soft truncation threshold, the original body is
// returned unchanged (no cache entry). Otherwise, the result is cached and a
// truncated view with a pointer footer is returned.
func (c *ResultCache) StoreResult(sessionID, toolCallID, toolName, body string) (visible string, cached bool, err error) {
	bodyLen := len(body)

	// Under soft threshold — return as-is, no cache entry.
	if bodyLen <= c.softTruncBytes {
		return body, false, nil
	}

	id := newULID()
	now := time.Now().UTC()
	expiresAt := now.Add(time.Duration(c.cacheTTLSeconds) * time.Second)

	var storeBody sql.NullString
	wasTruncated := 1

	if bodyLen <= c.hardCapBytes {
		storeBody = sql.NullString{String: body, Valid: true}
	}
	// Over hard cap: body=NULL, metadata only.

	_, err = c.db.Exec(
		`INSERT INTO tool_result_cache (id, session_id, tool_name, tool_call_id, created_at, expires_at, byte_size, was_truncated, body)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, sessionID, toolName, toolCallID,
		now.Format(time.RFC3339), expiresAt.Format(time.RFC3339),
		bodyLen, wasTruncated, storeBody,
	)
	if err != nil {
		return body, false, fmt.Errorf("cache store: %w", err)
	}

	// Build truncated view + pointer.
	truncated := body[:c.softTruncBytes]
	footer := fmt.Sprintf(
		"\n\n[TRUNCATED — full result cached as tool_result://%s (total_size=%d bytes, expires_at=%s). "+
			"Use fetch_tool_result({\"id\": \"%s\"}) or search_tool_result({\"id\": \"%s\", \"pattern\": \"...\"}) to retrieve more.]",
		id, bodyLen, expiresAt.Format(time.RFC3339), id, id,
	)
	visible = truncated + footer

	slog.Info("tool-cache: result stored",
		"id", id, "tool", toolName, "byte_size", bodyLen,
		"soft_truncate", c.softTruncBytes, "hard_cap", c.hardCapBytes)
	return visible, true, nil
}

// Fetch retrieves a slice of the cached body.
func (c *ResultCache) Fetch(id string, offset, length int) (slice string, totalSize int, err error) {
	var body sql.NullString
	var byteSize int
	var expiresAt string

	err = c.db.QueryRow(
		`SELECT body, byte_size, expires_at FROM tool_result_cache WHERE id = ?`, id,
	).Scan(&body, &byteSize, &expiresAt)
	if err == sql.ErrNoRows {
		return "", 0, fmt.Errorf("cached result %q not found or expired", id)
	}
	if err != nil {
		return "", 0, fmt.Errorf("cache fetch: %w", err)
	}

	// Check expiry.
	expires, _ := time.Parse(time.RFC3339, expiresAt)
	if time.Now().UTC().After(expires) {
		return "", 0, fmt.Errorf("cached result %q has expired", id)
	}

	if !body.Valid {
		return "", byteSize, fmt.Errorf("cached result %q exceeded hard cap (%d bytes); body not stored", id, byteSize)
	}

	content := body.String
	if offset < 0 {
		offset = 0
	}
	if offset >= len(content) {
		return "", byteSize, nil
	}
	end := offset + length
	if end > len(content) || length <= 0 {
		end = len(content)
	}

	return content[offset:end], byteSize, nil
}

// Match describes a single regex match in a cached result.
type Match struct {
	LineStart int    `json:"line_start"`
	MatchText string `json:"match"`
	Context   string `json:"context"`
}

// Search performs a regex match over the cached body and returns matches with context.
func (c *ResultCache) Search(id, pattern string, maxMatches int) ([]Match, error) {
	if maxMatches <= 0 {
		maxMatches = 20
	}

	var body sql.NullString
	var expiresAt string
	err := c.db.QueryRow(
		`SELECT body, expires_at FROM tool_result_cache WHERE id = ?`, id,
	).Scan(&body, &expiresAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("cached result %q not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("cache search: %w", err)
	}

	expires, _ := time.Parse(time.RFC3339, expiresAt)
	if time.Now().UTC().After(expires) {
		return nil, fmt.Errorf("cached result %q has expired", id)
	}

	if !body.Valid {
		return nil, fmt.Errorf("cached result %q body not stored (exceeded hard cap)", id)
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid search pattern: %w", err)
	}

	content := body.String
	lines := strings.Split(content, "\n")

	var matches []Match
	for i, line := range lines {
		if re.MatchString(line) {
			// Build context: 2 lines before and after.
			start := i - 2
			if start < 0 {
				start = 0
			}
			end := i + 3
			if end > len(lines) {
				end = len(lines)
			}
			ctx := strings.Join(lines[start:end], "\n")
			matches = append(matches, Match{
				LineStart: i + 1,
				MatchText: re.FindString(line),
				Context:   ctx,
			})
			if len(matches) >= maxMatches {
				break
			}
		}
	}

	return matches, nil
}

// Purge deletes expired cache entries. Returns the count of deleted rows.
func (c *ResultCache) Purge() (int, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := c.db.Exec(`DELETE FROM tool_result_cache WHERE expires_at < ?`, now)
	if err != nil {
		return 0, fmt.Errorf("cache purge: %w", err)
	}
	n, _ := result.RowsAffected()
	if n > 0 {
		slog.Info("tool-cache: purged expired entries", "count", n)
	}
	return int(n), nil
}

func newULID() string {
	return ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}
