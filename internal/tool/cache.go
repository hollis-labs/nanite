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

// DefaultSoftTruncBytes is the fallback for callers without a model budget.
// Chat supplies the shared model-aware budget through PresentResult.
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

// ResultView records the reading view and its relationship to the original.
type ResultView struct {
	Content       string
	CacheID       string
	Format        string
	OriginalBytes int
	BudgetBytes   int
	Cached        bool
}

// StoreResult uses the fallback budget for callers without a model budget.
// A result that fits is returned unchanged, without creating a cache entry.
func (c *ResultCache) StoreResult(sessionID, toolCallID, toolName, body string) (string, bool, error) {
	view, err := c.PresentResult(sessionID, toolCallID, toolName, body, c.softTruncBytes)
	return view.Content, view.Cached, err
}

// PresentResult stores the original output before constructing a reading view.
// Budget applies to preview content; the small recovery notice is additional.
func (c *ResultCache) PresentResult(sessionID, toolCallID, toolName, body string, budget int) (ResultView, error) {
	if budget <= 0 {
		budget = c.softTruncBytes
	}
	budget = min(budget, c.hardCapBytes)
	view := ResultView{Content: body, Format: "complete", OriginalBytes: len(body), BudgetBytes: budget}
	if len(body) <= budget {
		return view, nil
	}

	id := newULID()
	now := time.Now().UTC()
	expiresAt := now.Add(time.Duration(c.cacheTTLSeconds) * time.Second)
	var stored sql.NullString
	if len(body) <= c.hardCapBytes {
		stored = sql.NullString{String: body, Valid: true}
	}
	_, err := c.db.Exec(
		`INSERT INTO tool_result_cache (id, session_id, tool_name, tool_call_id, created_at, expires_at, byte_size, was_truncated, body)
   VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?)`,
		id, sessionID, toolName, toolCallID, now.Format(time.RFC3339), expiresAt.Format(time.RFC3339), len(body), stored)
	if err != nil {
		return view, fmt.Errorf("cache store: %w", err)
	}

	preview, format := previewResult(body, budget)
	view.CacheID, view.Cached, view.Format = id, true, format
	view.Content = "[PARTIAL PREVIEW — not a complete read. Omitted content must be retrieved before making claims about it.]\n" + preview
	if stored.Valid {
		view.Content += fmt.Sprintf("\n\n[TRUNCATED — full result cached as tool_result://%s (total_size=%d bytes, expires_at=%s). "+
			"Use fetch_tool_result({\"id\":\"%s\"}) for pages, optionally with json_pointer to select a field (for example /stdout). "+
			"Use search_tool_result({\"id\":\"%s\",\"pattern\":\"...\"}) for matching regions. Preview labels are JSON pointers; fetch offsets address the selected text.]",
			id, len(body), expiresAt.Format(time.RFC3339), id, id)
	} else {
		view.Content += fmt.Sprintf("\n\n[TRUNCATED — result exceeded the %d-byte storage cap. Only metadata was cached as tool_result://%s; full content is unavailable. Narrow the source query.]", c.hardCapBytes, id)
	}
	slog.Info("tool-cache: result stored", "id", id, "tool", toolName, "byte_size", len(body), "preview_budget", budget, "format", format)
	return view, nil
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
	end := len(content)
	if length > 0 && length < len(content)-offset {
		end = offset + length
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
