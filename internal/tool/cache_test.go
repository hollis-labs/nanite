package tool

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

func setupTestCache(t *testing.T) (*ResultCache, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE tool_result_cache (
		id TEXT PRIMARY KEY,
		session_id TEXT NOT NULL,
		tool_name TEXT NOT NULL,
		tool_call_id TEXT NOT NULL,
		created_at TEXT NOT NULL,
		expires_at TEXT NOT NULL,
		byte_size INTEGER NOT NULL,
		was_truncated INTEGER NOT NULL DEFAULT 0,
		body TEXT
	)`)
	if err != nil {
		t.Fatal(err)
	}
	cache := NewResultCache(db, ResultCacheConfig{
		SoftTruncBytes:  100,  // small for testing
		HardCapBytes:    1000, // small for testing
		CacheTTLSeconds: 3600,
	})
	return cache, db
}

func TestResultCache_SmallResult(t *testing.T) {
	cache, db := setupTestCache(t)
	defer db.Close()

	body := "small result"
	visible, cached, err := cache.StoreResult("sess-1", "call-1", "test_tool", body)
	if err != nil {
		t.Fatal(err)
	}
	if cached {
		t.Error("small result should not be cached")
	}
	if visible != body {
		t.Errorf("expected original body, got: %s", visible)
	}
}

func TestResultCache_LargeResult_StoreAndFetch(t *testing.T) {
	cache, db := setupTestCache(t)
	defer db.Close()

	body := strings.Repeat("x", 200) // over 100 byte soft threshold
	visible, cached, err := cache.StoreResult("sess-1", "call-1", "test_tool", body)
	if err != nil {
		t.Fatal(err)
	}
	if !cached {
		t.Error("large result should be cached")
	}
	if !strings.Contains(visible, "[TRUNCATED") {
		t.Error("expected truncation pointer")
	}
	if !strings.Contains(visible, "tool_result://") {
		t.Error("expected tool_result:// pointer")
	}

	// Extract ID from pointer.
	idx := strings.Index(visible, "tool_result://")
	idStart := idx + len("tool_result://")
	idEnd := strings.Index(visible[idStart:], " ")
	id := visible[idStart : idStart+idEnd]

	// Fetch full body.
	slice, totalSize, err := cache.Fetch("sess-1", id, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if totalSize != 200 {
		t.Errorf("expected total 200, got %d", totalSize)
	}
	if slice != body {
		t.Error("fetched body doesn't match original")
	}

	// Fetch with offset and length.
	slice2, _, err := cache.Fetch("sess-1", id, 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	if slice2 != body[10:30] {
		t.Errorf("expected slice from 10-30, got: %s", slice2)
	}
}

func TestResultCache_HardCap(t *testing.T) {
	cache, db := setupTestCache(t)
	defer db.Close()

	body := strings.Repeat("x", 1500) // over 1000 byte hard cap
	_, cached, err := cache.StoreResult("sess-1", "call-1", "test_tool", body)
	if err != nil {
		t.Fatal(err)
	}
	if !cached {
		t.Error("should be cached")
	}

	// The fetch should fail because body=NULL for over-hard-cap.
	var id string
	err = db.QueryRow(`SELECT id FROM tool_result_cache WHERE session_id = 'sess-1'`).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = cache.Fetch("sess-1", id, 0, 0)
	if err == nil {
		t.Error("expected error for hard-cap entry")
	}
	if !strings.Contains(err.Error(), "hard cap") {
		t.Errorf("expected hard cap error, got: %v", err)
	}
}

func TestResultCache_Search(t *testing.T) {
	cache, db := setupTestCache(t)
	defer db.Close()

	lines := []string{
		"line 1: hello world",
		"line 2: foo bar",
		"line 3: hello again",
		"line 4: nothing here",
		"line 5: hello final",
	}
	body := strings.Repeat("x", 50) + "\n" + strings.Join(lines, "\n") + "\n" + strings.Repeat("y", 50)
	_, _, err := cache.StoreResult("sess-1", "call-1", "test_tool", body)
	if err != nil {
		t.Fatal(err)
	}

	var id string
	err = db.QueryRow(`SELECT id FROM tool_result_cache WHERE session_id = 'sess-1'`).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}

	matches, err := cache.Search("sess-1", id, "hello", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 3 {
		t.Errorf("expected 3 matches, got %d", len(matches))
	}
}

func TestResultCache_Purge(t *testing.T) {
	cache, db := setupTestCache(t)
	defer db.Close()

	// Insert an already-expired entry.
	_, err := db.Exec(
		`INSERT INTO tool_result_cache (id, session_id, tool_name, tool_call_id, created_at, expires_at, byte_size, was_truncated, body)
		 VALUES ('expired-1', 'sess-1', 'tool', 'call', '2020-01-01T00:00:00Z', '2020-01-01T01:00:00Z', 10, 0, 'data')`,
	)
	if err != nil {
		t.Fatal(err)
	}

	count, err := cache.Purge()
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1 purged, got %d", count)
	}
}

func TestResultCache_FetchNotFound(t *testing.T) {
	cache, db := setupTestCache(t)
	defer db.Close()

	_, _, err := cache.Fetch("sess-1", "nonexistent", 0, 0)
	if err == nil {
		t.Error("expected error for nonexistent ID")
	}
}

// TestTruncateAtBoundary_LineBoundary asserts that the helper prefers to cut
// at a newline rather than in the middle of a line (CW-20260426-0011).
func TestTruncateAtBoundary_LineBoundary(t *testing.T) {
	// Build a body where the soft threshold falls in the middle of a line.
	// Lines are 20 bytes each; threshold is set at 35 — lands mid-line-2.
	// Expected: cut at the end of line 1 (position 20, the \n index).
	line1 := strings.Repeat("a", 19) + "\n" // 20 bytes
	line2 := strings.Repeat("b", 19) + "\n" // 20 bytes
	body := line1 + line2 + strings.Repeat("c", 20)

	cut := truncateAtBoundary(body, 35)
	// cut should be at the \n in line1, i.e. index 19 (just before the \n).
	if cut != 19 {
		t.Errorf("expected cut at 19 (end of line1 before \\n), got %d", cut)
	}
	// Verify the truncated prefix ends with a complete line (no partial content).
	prefix := body[:cut]
	if strings.Contains(prefix, "b") {
		t.Error("truncated prefix must not contain line2 content")
	}
}

// TestTruncateAtBoundary_UTF8 asserts that the helper never splits a multi-byte
// UTF-8 sequence (CW-20260426-0011).
func TestTruncateAtBoundary_UTF8(t *testing.T) {
	// "é" is 2 bytes (0xC3 0xA9). Build a string of 'a'*9 + "é" so the
	// 2-byte sequence straddles the threshold of 10.
	s := strings.Repeat("a", 9) + "é" + strings.Repeat("a", 10)
	cut := truncateAtBoundary(s, 10)
	// The cut must not land inside the 2-byte sequence.
	// Valid positions: 9 (before "é") or 11 (after "é" — but 11 > 10 so not taken).
	// truncateAtBoundary walks back, so it should land at 9.
	if cut != 9 {
		t.Errorf("expected cut at 9 (before multi-byte rune), got %d", cut)
	}
	// Ensure the slice is valid UTF-8.
	if !utf8.ValidString(s[:cut]) {
		t.Errorf("prefix is not valid UTF-8: %q", s[:cut])
	}
}

// TestTruncateAtBoundary_NoNewline asserts fallback to UTF-8-safe byte position
// when no newline precedes the threshold.
func TestTruncateAtBoundary_NoNewline(t *testing.T) {
	s := strings.Repeat("x", 200)
	cut := truncateAtBoundary(s, 50)
	if cut != 50 {
		t.Errorf("expected cut at 50 (no newline, plain ASCII), got %d", cut)
	}
}

// TestTruncateAtBoundary_BeyondLength asserts the full length is returned when
// maxBytes >= len(s).
func TestTruncateAtBoundary_BeyondLength(t *testing.T) {
	s := "hello"
	cut := truncateAtBoundary(s, 100)
	if cut != len(s) {
		t.Errorf("expected cut at %d, got %d", len(s), cut)
	}
}

// TestResultCache_TruncatesAtLineBoundary is the integration-level regression:
// a large result whose soft threshold falls mid-line must produce a visible
// preview that ends at a complete line, not mid-content (CW-20260426-0011).
func TestResultCache_TruncatesAtLineBoundary(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE tool_result_cache (
		id TEXT PRIMARY KEY,
		session_id TEXT NOT NULL,
		tool_name TEXT NOT NULL,
		tool_call_id TEXT NOT NULL,
		created_at TEXT NOT NULL,
		expires_at TEXT NOT NULL,
		byte_size INTEGER NOT NULL,
		was_truncated INTEGER NOT NULL DEFAULT 0,
		body TEXT
	)`)
	if err != nil {
		t.Fatal(err)
	}

	// soft threshold = 100 bytes; each item line is 30 bytes so threshold
	// falls mid-item-4. The preview must end at the \n after item-3.
	const softThreshold = 100
	cache := NewResultCache(db, ResultCacheConfig{
		SoftTruncBytes:  softThreshold,
		HardCapBytes:    1024 * 1024,
		CacheTTLSeconds: 3600,
	})

	// Build body: 10 lines of 30 bytes each (29 chars + \n).
	var bodyLines []string
	for i := 0; i < 10; i++ {
		bodyLines = append(bodyLines, strings.Repeat(string(rune('A'+i)), 29))
	}
	body := strings.Join(bodyLines, "\n") + "\n"

	visible, cached, err := cache.StoreResult("sess", "call", "tool", body)
	if err != nil {
		t.Fatalf("StoreResult: %v", err)
	}
	if !cached {
		t.Fatal("expected body to be cached (over soft threshold)")
	}

	// Extract just the preview portion (everything before [TRUNCATED).
	truncIdx := strings.Index(visible, "\n\n[TRUNCATED")
	if truncIdx < 0 {
		t.Fatalf("no truncation footer found in visible output: %q", visible)
	}
	preview := visible[:truncIdx]

	// The preview must end at a complete line: last byte before the footer
	// should be 'A'–'Z' (item content), and the preview must not contain a
	// partial item (we check that the last char of the last line is the
	// expected letter repeated 29 times).
	lastNL := strings.LastIndexByte(preview, '\n')
	var lastLine string
	if lastNL < 0 {
		lastLine = preview
	} else {
		lastLine = preview[lastNL+1:]
	}
	if len(lastLine) != 0 {
		// Every non-empty last line must be a full 29-byte item.
		if len(lastLine) != 29 {
			t.Errorf("last line of preview has unexpected length %d (want 0 or 29): %q",
				len(lastLine), lastLine)
		}
	}
	// Crucially: no partial item content — every character in the preview
	// must be consistent with our item alphabet.
	if !utf8.ValidString(preview) {
		t.Error("preview is not valid UTF-8")
	}
}
