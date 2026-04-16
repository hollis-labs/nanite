package tool

import (
	"database/sql"
	"strings"
	"testing"

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
