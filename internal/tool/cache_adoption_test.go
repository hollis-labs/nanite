package tool

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
	toolresult "github.com/hollis-labs/substrate/agent/toolresult"
	"github.com/hollis-labs/substrate/agent/toolresult/sqlstore"
	"github.com/hollis-labs/substrate/agent/toolresult/storetest"
)

func TestResultCache_SQLStoreConformance(t *testing.T) {
	st, err := store.New(context.Background(), filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	storetest.Run(t, func(t *testing.T) toolresult.Store {
		// The conformance cases run sequentially. Reset the actual migrated
		// table so each factory call gets an empty store without rerunning
		// Nanite's entire migration history for every case.
		if _, err := st.DB.ExecContext(context.Background(), `DELETE FROM tool_result_cache`); err != nil {
			t.Fatal(err)
		}
		return sqlstore.New(st.DB, sqlstore.Table{})
	})
}

func TestResultCache_ExistingRowsRemainRecoverable(t *testing.T) {
	cache, db := setupTestCache(t)
	defer db.Close()
	// These are the rows written by Nanite before adoption, including a
	// metadata-only result. No library DDL or migration is applied.
	body := `{"stdout":"first source\nlast source","a/b~c":9007199254740993}`
	created := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	expires := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	for _, row := range []struct {
		id   string
		body any
	}{
		{"existing", body},
		{"metadata-only", nil},
	} {
		if _, err := db.Exec(`INSERT INTO tool_result_cache
			(id, session_id, tool_name, tool_call_id, created_at, expires_at, byte_size, was_truncated, body)
			VALUES (?, 'owner', 'dev_bash', 'call', ?, ?, ?, 1, ?)`, row.id, created, expires, len(body), row.body); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	page, err := cache.Results.Read(ctx, "owner", "existing", "", 0, 0, 1000)
	if err != nil || page.Content != body || page.HasMore {
		t.Fatalf("existing original: %+v, %v", page, err)
	}
	page, err = cache.Results.Read(ctx, "owner", "existing", "/a~1b~0c", 0, 0, 1000)
	if err != nil || page.Content != "9007199254740993" {
		t.Fatalf("existing JSON pointer: %+v, %v", page, err)
	}
	text, isError := cache.Results.HandleFetch(ctx, "owner", map[string]any{"id": "existing", "json_pointer": "/stdout"}, 1000)
	if isError || !strings.HasSuffix(text, "first source\nlast source") {
		t.Fatalf("existing tool recovery: %q, isError=%t", text, isError)
	}
	if _, err := cache.Results.Read(ctx, "other-session", "existing", "", 0, 0, 1000); !errors.Is(err, toolresult.ErrNotFound) {
		t.Fatalf("cross-session result: %v", err)
	}
	if _, err := cache.Results.Read(ctx, "owner", "metadata-only", "", 0, 0, 1000); !errors.Is(err, toolresult.ErrBodyNotStored) {
		t.Fatalf("metadata-only result: %v", err)
	}
	if _, err := db.Exec(`UPDATE tool_result_cache SET expires_at = '2020-01-01T00:00:00Z' WHERE id = 'existing'`); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Results.Read(ctx, "owner", "existing", "", 0, 0, 1000); !errors.Is(err, toolresult.ErrExpired) {
		t.Fatalf("expired result: %v", err)
	}
}

func TestResultCache_CancelledStoreDoesNotInventPointer(t *testing.T) {
	cache, db := setupTestCache(t)
	defer db.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	body := strings.Repeat("source", 100)
	view, err := cache.Results.Present(ctx, "owner", toolresult.Meta{Tool: "dev_bash", CallID: "call"}, body, 100)
	if !errors.Is(err, context.Canceled) || view.Content != body || view.Cached || view.CacheID != "" {
		t.Fatalf("canceled store: %+v, %v", view, err)
	}
	var rows int
	if err := db.QueryRow(`SELECT count(*) FROM tool_result_cache`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("canceled write stored %d rows: %v", rows, err)
	}
}
