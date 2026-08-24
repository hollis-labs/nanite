package tool

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/oklog/ulid/v2"
)

// DefaultSoftTruncBytes is the default byte threshold above which tool results
// are truncated for the LLM and the full body is cached. The LLM-visible slice
// is also capped at this size; anything longer gets replaced with the slice
// plus a `tool_result://<id>` pointer footer the LLM can fetch via
// `fetch_tool_result` / `search_tool_result` when it actually needs more.
//
// CW-20260419-0004 Part 1: lowered from 64 KiB → 2 KiB. At 64 KiB nothing in
// practical use ever hit the cache path — every tool result fell through to
// `truncate.Output`'s 4 KiB fallback and accumulated in the conversation
// slot. The c9 UAT died at ~45 K tokens with 13 tool calls × ~4 KiB each.
// At 2 KiB, most tool results (clockwork_task_list, dev_read, etc.) become
// pointers and the conversation slot stays tiny; tiny results (health
// checks, small lookups) still pass through untouched. Once
// CW-20260419-0001 ships a settings UI, this becomes a user-tunable knob
// with this value as the safe default.
const DefaultSoftTruncBytes = 2 * 1024 // 2 KiB

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
	// Clamp: soft must not exceed hard.
	if soft > hard {
		soft = hard
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
	// truncateAtBoundary walks back from the soft threshold to find the
	// nearest line boundary (\n), then further back to a valid UTF-8 rune
	// start so the LLM-visible preview is never mid-line or mid-character.
	cutAt := truncateAtBoundary(body, c.softTruncBytes)
	truncated := body[:cutAt]
	var footer string
	if storeBody.Valid {
		footer = fmt.Sprintf(
			"\n\n[TRUNCATED — full result cached as tool_result://%s (total_size=%d bytes, expires_at=%s). "+
				"Use fetch_tool_result({\"id\": \"%s\"}) or search_tool_result({\"id\": \"%s\", \"pattern\": \"...\"}) to retrieve more.]",
			id, bodyLen, expiresAt.Format(time.RFC3339), id, id,
		)
	} else {
		footer = fmt.Sprintf(
			"\n\n[TRUNCATED — result too large (%d bytes, exceeds hard cap %d). "+
				"Only metadata was cached (tool_result://%s). The full body is not available for retrieval.]",
			bodyLen, c.hardCapBytes, id,
		)
	}
	visible = truncated + footer

	slog.Info("tool-cache: result stored",
		"id", id, "tool", toolName, "byte_size", bodyLen,
		"soft_truncate", c.softTruncBytes, "hard_cap", c.hardCapBytes)
	return visible, true, nil
}

// Fetch retrieves a slice of the cached body. sessionID scopes the lookup
// to prevent cross-session reads.
func (c *ResultCache) Fetch(sessionID, id string, offset, length int) (slice string, totalSize int, err error) {
	var body sql.NullString
	var byteSize int
	var expiresAt string

	err = c.db.QueryRow(
		`SELECT body, byte_size, expires_at FROM tool_result_cache WHERE id = ? AND session_id = ?`, id, sessionID,
	).Scan(&body, &byteSize, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, fmt.Errorf("cached result %q not found or expired", id)
	}
	if err != nil {
		return "", 0, fmt.Errorf("cache fetch: %w", err)
	}

	// Check expiry.
	expires, parseErr := time.Parse(time.RFC3339, expiresAt)
	if parseErr != nil {
		return "", 0, fmt.Errorf("cache fetch: malformed expires_at for %q: %w", id, parseErr)
	}
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
// sessionID scopes the lookup to prevent cross-session reads.
func (c *ResultCache) Search(sessionID, id, pattern string, maxMatches int) ([]Match, error) {
	if maxMatches <= 0 {
		maxMatches = 20
	}

	var body sql.NullString
	var expiresAt string
	err := c.db.QueryRow(
		`SELECT body, expires_at FROM tool_result_cache WHERE id = ? AND session_id = ?`, id, sessionID,
	).Scan(&body, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("cached result %q not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("cache search: %w", err)
	}

	expires, parseErr := time.Parse(time.RFC3339, expiresAt)
	if parseErr != nil {
		return nil, fmt.Errorf("cache search: malformed expires_at for %q: %w", id, parseErr)
	}
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

// truncateAtBoundary returns the largest cut point ≤ maxBytes that lands on
// a line boundary (\n) and a valid UTF-8 rune start. This prevents the
// LLM-visible truncation preview from ending mid-line or mid-character
// (CW-20260426-0011). Preference order:
//  1. Line boundary — walk back from maxBytes to the nearest preceding \n.
//  2. UTF-8 boundary — walk back further if the \n position splits a
//     multi-byte sequence (shouldn't happen in practice but guarded anyway).
//
// If no \n exists before maxBytes the cut falls back to the UTF-8-safe byte
// position at maxBytes (no line boundary available, still safe for the codec).
// If maxBytes ≥ len(s) the full string length is returned unchanged.
func truncateAtBoundary(s string, maxBytes int) int {
	if maxBytes >= len(s) {
		return len(s)
	}
	if maxBytes <= 0 {
		return 0
	}

	// Walk back to the nearest preceding newline.
	cut := maxBytes
	if nl := strings.LastIndexByte(s[:cut], '\n'); nl >= 0 {
		cut = nl // cut just before the \n so the last visible line is complete
	}

	// Ensure we're at a valid UTF-8 rune start (no mid-sequence split).
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return cut
}

func newULID() string {
	return ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}
