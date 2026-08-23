package memory

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	conduit "github.com/hollis-labs/tesseract"
	conduitMemory "github.com/hollis-labs/tesseract/memory"
)

// newTestConduit creates a real embedded Conduit instance backed by a temp dir.
func newTestConduit(t *testing.T) (*conduit.Conduit, func()) {
	t.Helper()
	dir := t.TempDir()
	c, err := conduit.Open(context.Background(), conduit.Config{RootDir: dir})
	if err != nil {
		t.Fatalf("conduit.Open: %v", err)
	}
	var dbFile string
	rows, err := c.MemoryStore().DB().Query("PRAGMA database_list")
	if err != nil {
		_ = c.Close()
		t.Fatalf("PRAGMA database_list: %v", err)
	}
	for rows.Next() {
		var seq int
		var name, path string
		if err := rows.Scan(&seq, &name, &path); err != nil {
			_ = rows.Close()
			_ = c.Close()
			t.Fatalf("scan database_list: %v", err)
		}
		if name == "main" {
			dbFile = path
			break
		}
	}
	_ = rows.Close()
	if dbFile == "" || !memoryTestPathUnder(dir, dbFile) {
		canonicalDir, dirErr := filepath.EvalSymlinks(dir)
		canonicalDB, dbErr := filepath.EvalSymlinks(dbFile)
		if dirErr == nil && dbErr == nil && memoryTestPathUnder(canonicalDir, canonicalDB) {
			cleanup := func() { _ = c.Close() }
			return c, cleanup
		}
		_ = c.Close()
		t.Fatalf("test Tesseract DB %q escapes temp root %q (canonical root=%q err=%v; db=%q err=%v)", dbFile, dir, canonicalDir, dirErr, canonicalDB, dbErr)
	}
	cleanup := func() { _ = c.Close() }
	return c, cleanup
}

func memoryTestPathUnder(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func TestMemoryStore(t *testing.T) {
	c, cleanup := newTestConduit(t)
	defer cleanup()
	svc := NewService(c.MemoryStore())

	err := svc.Store(context.Background(), Memory{
		Namespace:  "user/default/session/test-123/memory",
		MemoryKey:  "prefers_terse_output",
		Summary:    "User prefers terse output",
		Body:       "When asked, user said they prefer concise responses.",
		Origin:     "user",
		Trigger:    "per_turn",
		Confidence: 0.9,
		Tags:       []string{"preferences", "output_style"},
		SessionID:  "test-123",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify we can recall it.
	memories, err := svc.Recall(context.Background(), RecallOpts{
		Namespaces: []string{"user/default/session/test-123/memory"},
		Ranking:    "activation",
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("recall error: %v", err)
	}
	if len(memories) != 1 {
		t.Fatalf("expected 1 memory, got %d", len(memories))
	}
	if memories[0].Summary != "User prefers terse output" {
		t.Errorf("unexpected summary: %s", memories[0].Summary)
	}
	if memories[0].Origin != "user" {
		t.Errorf("expected origin user, got %s", memories[0].Origin)
	}
}

func TestMemoryStore_NilStore(t *testing.T) {
	svc := NewService(nil)
	err := svc.Store(context.Background(), Memory{Summary: "test"})
	if err == nil {
		t.Error("expected error for nil store")
	}
}

func TestMemoryRecall(t *testing.T) {
	c, cleanup := newTestConduit(t)
	defer cleanup()
	svc := NewService(c.MemoryStore())

	// Store two memories in different namespaces.
	err := svc.Store(context.Background(), Memory{
		Namespace:  "user/chrispian/memory",
		MemoryKey:  "prefers_terse_output",
		Summary:    "User prefers terse output",
		Origin:     "user",
		Trigger:    "explicit",
		Confidence: 0.9,
		SessionID:  "test-1",
	})
	if err != nil {
		t.Fatalf("store 1: %v", err)
	}

	err = svc.Store(context.Background(), Memory{
		Namespace:  "user/chrispian/project/nanite/memory",
		MemoryKey:  "uses_sqlite",
		Summary:    "Project uses SQLite for persistence",
		Origin:     "project",
		Trigger:    "explicit",
		Confidence: 0.95,
		SessionID:  "test-2",
	})
	if err != nil {
		t.Fatalf("store 2: %v", err)
	}

	// Recall from both namespaces.
	results, err := svc.Recall(context.Background(), RecallOpts{
		Namespaces:    []string{"user/chrispian/memory", "user/chrispian/project/nanite/memory"},
		Ranking:       "activation",
		Limit:         10,
		MinConfidence: 0.5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 memories, got %d", len(results))
	}
}

func TestMemoryRecall_DefaultValues(t *testing.T) {
	c, cleanup := newTestConduit(t)
	defer cleanup()
	svc := NewService(c.MemoryStore())

	results, err := svc.Recall(context.Background(), RecallOpts{
		Namespaces: []string{"user/default/memory"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 memories, got %d", len(results))
	}
}

func TestMemoryRecallPage_PreservesActivationRankingAndTotal(t *testing.T) {
	c, cleanup := newTestConduit(t)
	defer cleanup()
	svc := NewService(c.MemoryStore())
	ctx := context.Background()
	namespace := "user/ranked-page/memory"
	for _, tc := range []struct {
		key        string
		confidence float64
	}{
		{key: "low", confidence: 0.2},
		{key: "high", confidence: 0.9},
		{key: "middle", confidence: 0.5},
	} {
		if err := svc.Store(ctx, Memory{
			Namespace: namespace, MemoryKey: tc.key, Summary: "Unicode CAFÉ match",
			Origin: "user", Trigger: "manual", Confidence: tc.confidence,
			SessionID: "ranked-page", Status: "reviewed",
		}); err != nil {
			t.Fatalf("store %s: %v", tc.key, err)
		}
	}
	// A second revision of the low-ranked logical memory proves both paths
	// join through memory_state.current_revision rather than returning history.
	if err := svc.Store(ctx, Memory{
		Namespace: namespace, MemoryKey: "low", Summary: "Unicode CAFÉ match updated",
		Origin: "user", Trigger: "manual", Confidence: 0.3,
		SessionID: "ranked-page", Status: "reviewed",
	}); err != nil {
		t.Fatalf("store updated low revision: %v", err)
	}

	tesseractOrder, err := svc.Recall(ctx, RecallOpts{
		Namespaces: []string{namespace}, Ranking: "activation",
		Statuses: []string{"reviewed"}, Limit: 3,
	})
	if err != nil {
		t.Fatalf("Tesseract Recall: %v", err)
	}

	page, err := svc.RecallPage(ctx, RecallOpts{
		Namespaces: []string{namespace}, Ranking: "activation",
		Statuses: []string{"reviewed"}, Search: "café", Limit: 2,
	})
	if err != nil {
		t.Fatalf("RecallPage: %v", err)
	}
	if page.Total != 3 || len(page.Memories) != 2 {
		t.Fatalf("page total=%d len=%d, want total=3 len=2", page.Total, len(page.Memories))
	}
	if page.Memories[0].MemoryKey != "high" || page.Memories[1].MemoryKey != "middle" {
		t.Fatalf("activation order = [%s %s], want [high middle]", page.Memories[0].MemoryKey, page.Memories[1].MemoryKey)
	}
	if len(tesseractOrder) != 3 || page.Memories[0].MemoryKey != tesseractOrder[0].MemoryKey || page.Memories[1].MemoryKey != tesseractOrder[1].MemoryKey {
		t.Fatalf("list order [%s %s] differs from pinned Tesseract order %+v", page.Memories[0].MemoryKey, page.Memories[1].MemoryKey, tesseractOrder)
	}
	if tesseractOrder[2].MemoryKey != "low" || !strings.Contains(tesseractOrder[2].Summary, "updated") {
		t.Fatalf("current-revision result = %+v, want updated low revision", tesseractOrder[2])
	}
}

func TestMemoryRecallPage_LegacyTimestampParsingAndOrdering(t *testing.T) {
	c, cleanup := newTestConduit(t)
	defer cleanup()
	svc := NewService(c.MemoryStore())
	ctx := context.Background()
	namespace := "user/legacy-time/memory"
	for _, key := range []string{"legacy_old", "legacy_new"} {
		if err := svc.Store(ctx, Memory{
			Namespace: namespace, MemoryKey: key, Summary: key,
			Origin: "user", Trigger: "manual", Confidence: 0.8,
			SessionID: "legacy-time", Status: "reviewed",
		}); err != nil {
			t.Fatalf("store %s: %v", key, err)
		}
	}
	db := c.MemoryStore().DB()
	legacyRows := []struct {
		key          string
		createdAt    string
		lastAccessed string
	}{
		{key: "legacy_old", createdAt: "2024-01-02 03:04:05", lastAccessed: time.Now().UTC().Add(-40 * 24 * time.Hour).Format(time.DateTime)},
		{key: "legacy_new", createdAt: "2025-02-03 04:05:06", lastAccessed: time.Now().UTC().Add(-time.Hour).Format(time.DateTime)},
	}
	for _, row := range legacyRows {
		if _, err := db.ExecContext(ctx, `UPDATE memory_revisions SET created_at = ? WHERE namespace = ? AND memory_key = ?`, row.createdAt, namespace, row.key); err != nil {
			t.Fatalf("update legacy created_at %s: %v", row.key, err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE memory_state SET last_accessed_at = ? WHERE namespace = ? AND memory_key = ?`, row.lastAccessed, namespace, row.key); err != nil {
			t.Fatalf("update legacy last_accessed_at %s: %v", row.key, err)
		}
	}

	activationPage, err := svc.RecallPage(ctx, RecallOpts{
		Namespaces: []string{namespace}, Ranking: "activation", Limit: 1,
	})
	if err != nil {
		t.Fatalf("activation RecallPage: %v", err)
	}
	if activationPage.Total != 2 || len(activationPage.Memories) != 1 || activationPage.Memories[0].MemoryKey != "legacy_new" {
		t.Fatalf("legacy activation page = %+v total=%d, want legacy_new first and total 2", activationPage.Memories, activationPage.Total)
	}

	chronologicalPage, err := svc.RecallPage(ctx, RecallOpts{
		Namespaces: []string{namespace}, Ranking: "chronological", Limit: 2,
	})
	if err != nil {
		t.Fatalf("chronological RecallPage: %v", err)
	}
	if len(chronologicalPage.Memories) != 2 || chronologicalPage.Memories[0].MemoryKey != "legacy_new" || chronologicalPage.Memories[1].MemoryKey != "legacy_old" {
		t.Fatalf("legacy chronological order = %+v, want [legacy_new legacy_old]", chronologicalPage.Memories)
	}
}

func TestMemoryRecallPage_ReinforcesOnlyReturnedPage(t *testing.T) {
	c, cleanup := newTestConduit(t)
	defer cleanup()
	svc := NewService(c.MemoryStore())
	ctx := context.Background()
	namespace := "user/page-reinforcement/memory"
	for _, tc := range []struct {
		key        string
		summary    string
		confidence float64
	}{
		{key: "filtered_out", summary: "unrelated", confidence: 1.0},
		{key: "offset_skipped", summary: "needle", confidence: 0.9},
		{key: "page_a", summary: "needle", confidence: 0.8},
		{key: "page_b", summary: "needle", confidence: 0.7},
		{key: "after_page", summary: "needle", confidence: 0.6},
	} {
		if err := svc.Store(ctx, Memory{
			Namespace: namespace, MemoryKey: tc.key, Summary: tc.summary,
			Origin: "user", Trigger: "manual", Confidence: tc.confidence,
			SessionID: "page-reinforcement", Status: "reviewed",
		}); err != nil {
			t.Fatalf("store %s: %v", tc.key, err)
		}
	}

	page, err := svc.RecallPage(ctx, RecallOpts{
		Namespaces: []string{namespace}, Ranking: "activation",
		Statuses: []string{"reviewed"}, Search: "needle", Limit: 2, Offset: 1,
	})
	if err != nil {
		t.Fatalf("RecallPage: %v", err)
	}
	if page.Total != 4 || len(page.Memories) != 2 || page.Memories[0].MemoryKey != "page_a" || page.Memories[1].MemoryKey != "page_b" {
		t.Fatalf("page = %+v total=%d, want [page_a page_b] total=4", page.Memories, page.Total)
	}

	rows, err := c.MemoryStore().DB().QueryContext(ctx, `
		SELECT s.memory_key, s.activation, s.access_count, s.last_accessed_at
		FROM memory_state s WHERE s.namespace = ?`, namespace)
	if err != nil {
		t.Fatalf("query memory_state: %v", err)
	}
	defer func() { _ = rows.Close() }()
	seen := map[string]bool{}
	for rows.Next() {
		var key string
		var activation float64
		var accessCount int64
		var lastAccessed sql.NullString
		if err := rows.Scan(&key, &activation, &accessCount, &lastAccessed); err != nil {
			t.Fatalf("scan memory_state: %v", err)
		}
		returned := key == "page_a" || key == "page_b"
		seen[key] = true
		if returned {
			if accessCount != 1 || !lastAccessed.Valid || activation < 1.099 || activation > 1.101 {
				t.Errorf("returned %s state: activation=%f access_count=%d last=%v", key, activation, accessCount, lastAccessed)
			}
			if _, err := parseMemoryTimestamp(lastAccessed.String); err != nil {
				t.Errorf("returned %s last_accessed_at %q: %v", key, lastAccessed.String, err)
			}
		} else if accessCount != 0 || lastAccessed.Valid || activation < 0.999 || activation > 1.001 {
			t.Errorf("non-returned %s was reinforced: activation=%f access_count=%d last=%v", key, activation, accessCount, lastAccessed)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("memory_state rows: %v", err)
	}
	for _, key := range []string{"filtered_out", "offset_skipped", "page_a", "page_b", "after_page"} {
		if !seen[key] {
			t.Errorf("missing memory_state row for %s", key)
		}
	}
}

// TestMemoryRecall_RankingRelevance verifies the hybrid-relevance ranking
// (Vanta v0.4.0+) is accepted via its string name and passes through to
// Conduit without error. Doesn't assert ranking quality — that's covered
// by Vanta's own regression gate.
func TestMemoryRecall_RankingRelevance(t *testing.T) {
	c, cleanup := newTestConduit(t)
	defer cleanup()
	svc := NewService(c.MemoryStore())

	if err := svc.Store(context.Background(), Memory{
		Namespace: "user/chrispian/memory",
		MemoryKey: "user_prefers_go",
		Summary:   "User prefers Go for backend work",
		Origin:    "user",
		Trigger:   "explicit",
		SessionID: "test-relevance",
	}); err != nil {
		t.Fatalf("store: %v", err)
	}

	// Sanity: activation ranking sees the memory.
	baseline, err := svc.Recall(context.Background(), RecallOpts{
		Namespaces: []string{"user/chrispian/memory"},
		Ranking:    "activation",
		Limit:      5,
	})
	if err != nil {
		t.Fatalf("activation baseline: %v", err)
	}
	if len(baseline) != 1 {
		t.Fatalf("activation baseline: expected 1, got %d", len(baseline))
	}

	// Relevance ranking with a query that overlaps the stored summary.
	results, err := svc.Recall(context.Background(), RecallOpts{
		Namespaces: []string{"user/chrispian/memory"},
		Ranking:    "relevance",
		Query:      "prefers backend",
		Limit:      5,
	})
	if err != nil {
		t.Fatalf("relevance recall: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 memory, got %d — relevance arm may need closer term overlap", len(results))
	}
}

// TestMemoryRecall_EmptyRankingSmartDefault verifies that empty Ranking is
// passed through to Conduit (not normalized to activation), so Conduit's
// smart default can pick relevance-when-query / activation-when-no-query.
func TestMemoryRecall_EmptyRankingSmartDefault(t *testing.T) {
	c, cleanup := newTestConduit(t)
	defer cleanup()
	svc := NewService(c.MemoryStore())

	if err := svc.Store(context.Background(), Memory{
		Namespace: "user/chrispian/memory",
		MemoryKey: "deploy_on_friday",
		Summary:   "Never deploy on Friday",
		Origin:    "feedback",
		Trigger:   "explicit",
		SessionID: "test-smart-default",
	}); err != nil {
		t.Fatalf("store: %v", err)
	}

	// Empty ranking + query — Conduit resolves to relevance.
	withQuery, err := svc.Recall(context.Background(), RecallOpts{
		Namespaces: []string{"user/chrispian/memory"},
		Query:      "deploy Friday",
		Limit:      5,
	})
	if err != nil {
		t.Fatalf("smart-default recall with query: %v", err)
	}
	if len(withQuery) != 1 {
		t.Fatalf("expected 1 memory with query, got %d", len(withQuery))
	}

	// Empty ranking + no query — Conduit resolves to activation.
	noQuery, err := svc.Recall(context.Background(), RecallOpts{
		Namespaces: []string{"user/chrispian/memory"},
		Limit:      5,
	})
	if err != nil {
		t.Fatalf("smart-default recall no query: %v", err)
	}
	if len(noQuery) != 1 {
		t.Fatalf("expected 1 memory with no query, got %d", len(noQuery))
	}
}

func TestMemoryGet(t *testing.T) {
	c, cleanup := newTestConduit(t)
	defer cleanup()
	svc := NewService(c.MemoryStore())

	err := svc.Store(context.Background(), Memory{
		Namespace:  "user/default/memory",
		MemoryKey:  "test_key",
		Summary:    "A test memory",
		Origin:     "observation",
		Trigger:    "explicit",
		Confidence: 0.8,
		SessionID:  "test-get",
	})
	if err != nil {
		t.Fatalf("store: %v", err)
	}

	m, err := svc.Get(context.Background(), "user/default/memory", "test_key")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if m.Summary != "A test memory" {
		t.Errorf("unexpected summary: %s", m.Summary)
	}
}

func TestMemoryDeprecate(t *testing.T) {
	c, cleanup := newTestConduit(t)
	defer cleanup()
	svc := NewService(c.MemoryStore())

	err := svc.Store(context.Background(), Memory{
		Namespace:  "user/default/memory",
		MemoryKey:  "to_deprecate",
		Summary:    "Will be deprecated",
		Origin:     "observation",
		Trigger:    "explicit",
		Confidence: 0.8,
		SessionID:  "test-dep",
	})
	if err != nil {
		t.Fatalf("store: %v", err)
	}

	// Get the revision ID.
	m, err := svc.Get(context.Background(), "user/default/memory", "to_deprecate")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	// Deprecate it.
	err = svc.Deprecate(context.Background(), m.RevisionID)
	if err != nil {
		t.Fatalf("deprecate: %v", err)
	}

	// Recall should return nothing (deprecated memories are filtered).
	results, err := svc.Recall(context.Background(), RecallOpts{
		Namespaces: []string{"user/default/memory"},
	})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 memories after deprecation, got %d", len(results))
	}
}

func TestPerTurnExtraction_WithSignal(t *testing.T) {
	if !HasMemorySignal("Please remember this: I always prefer tabs over spaces") {
		t.Error("expected HasMemorySignal to detect 'remember' and 'always'")
	}
	if !HasMemorySignal("From now on, use Go 1.22 features") {
		t.Error("expected HasMemorySignal to detect 'from now on'")
	}
	if !HasMemorySignal("I prefer using SQLite for small projects") {
		t.Error("expected HasMemorySignal to detect 'I prefer'")
	}
	if !HasMemorySignal("No, don't do that. Use the other approach.") {
		t.Error("expected HasMemorySignal to detect correction pattern")
	}
	if !HasMemorySignal("Never use global variables in this project") {
		t.Error("expected HasMemorySignal to detect 'never'")
	}
}

func TestPerTurnExtraction_NoSignal(t *testing.T) {
	if HasMemorySignal("Can you help me write a function to parse JSON?") {
		t.Error("ordinary request should not trigger memory signal")
	}
	if HasMemorySignal("What is the capital of France?") {
		t.Error("simple question should not trigger memory signal")
	}
	if HasMemorySignal("Please review this code for bugs") {
		t.Error("code review request should not trigger memory signal")
	}
}

func TestPostCompactionExtraction(t *testing.T) {
	c, cleanup := newTestConduit(t)
	defer cleanup()
	svc := NewService(c.MemoryStore())

	called := false
	utilityCall := func(_ context.Context, prompt string) (string, error) {
		called = true
		return `[{"memory_key": "test_fact", "summary": "A test memory", "origin": "project", "confidence": 0.9, "tags": ["test"]}]`, nil
	}

	extractor := NewExtractor(svc, utilityCall)
	extractor.extractPostCompact("session-123", 5000)

	if !called {
		t.Error("expected utility call to be made during post-compact extraction")
	}

	// Verify the memory was stored.
	results, err := svc.Recall(context.Background(), RecallOpts{
		Namespaces: []string{SessionNamespace("session-123")},
	})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 stored memory, got %d", len(results))
	}
	if results[0].Summary != "A test memory" {
		t.Errorf("unexpected summary: %s", results[0].Summary)
	}
}

func TestPerTurnExtraction_Full(t *testing.T) {
	c, cleanup := newTestConduit(t)
	defer cleanup()
	svc := NewService(c.MemoryStore())

	utilityCall := func(_ context.Context, prompt string) (string, error) {
		return `{"memory_key": "prefers_terse", "summary": "User prefers terse output", "origin": "user", "confidence": 0.85, "tags": ["preferences"]}`, nil
	}

	extractor := NewExtractor(svc, utilityCall)
	extractor.extractPerTurn("session-456", "I always prefer terse, concise responses.")

	// Verify the memory was stored.
	results, err := svc.Recall(context.Background(), RecallOpts{
		Namespaces: []string{SessionNamespace("session-456")},
	})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 stored memory, got %d", len(results))
	}
	if results[0].MemoryKey != "prefers_terse" {
		t.Errorf("expected memory_key prefers_terse, got %s", results[0].MemoryKey)
	}
}

func TestPerTurnExtraction_LowConfidence(t *testing.T) {
	c, cleanup := newTestConduit(t)
	defer cleanup()
	svc := NewService(c.MemoryStore())

	utilityCall := func(_ context.Context, prompt string) (string, error) {
		return `{"memory_key": "maybe", "summary": "Maybe important", "origin": "user", "confidence": 0.3, "tags": []}`, nil
	}

	extractor := NewExtractor(svc, utilityCall)
	extractor.extractPerTurn("session-789", "Remember this might be useful")

	// Low confidence should not be stored.
	results, err := svc.Recall(context.Background(), RecallOpts{
		Namespaces: []string{SessionNamespace("session-789")},
	})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(results) != 0 {
		t.Error("expected no stored memories for low-confidence extraction")
	}
}

func TestNamespaceHelpers(t *testing.T) {
	if ns := SessionNamespace("abc-123"); ns != "user/default/session/abc-123/memory" {
		t.Errorf("unexpected session namespace: %s", ns)
	}
	if ns := ProjectNamespace("nanite"); ns != "user/default/project/nanite/memory" {
		t.Errorf("unexpected project namespace: %s", ns)
	}
	if ns := UserNamespace("chrispian"); ns != "user/chrispian/memory" {
		t.Errorf("unexpected user namespace: %s", ns)
	}
}

func TestCleanJSONResponse(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`{"key": "value"}`, `{"key": "value"}`},
		{"```json\n{\"key\": \"value\"}\n```", `{"key": "value"}`},
		{"  \n```\n{\"key\": \"value\"}\n```\n  ", `{"key": "value"}`},
		{`  {"key": "value"}  `, `{"key": "value"}`},
	}

	for _, tt := range tests {
		got := cleanJSONResponse(tt.input)
		if got != tt.want {
			t.Errorf("cleanJSONResponse(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestStoreError_NilStore(t *testing.T) {
	svc := NewService(nil)

	_, err := svc.Recall(context.Background(), RecallOpts{})
	if err == nil {
		t.Error("expected error for nil store on recall")
	}

	_, err = svc.Get(context.Background(), "ns", "key")
	if err == nil {
		t.Error("expected error for nil store on get")
	}

	err = svc.Promote(context.Background(), "rev", "ns")
	if err == nil {
		t.Error("expected error for nil store on promote")
	}

	err = svc.Deprecate(context.Background(), "rev")
	if err == nil {
		t.Error("expected error for nil store on deprecate")
	}
}

// TestMapOrigin verifies origin string mapping.
func TestMapOrigin(t *testing.T) {
	tests := []struct {
		input string
		want  conduitMemory.Origin
	}{
		{"user", conduitMemory.OriginUser},
		{"feedback", conduitMemory.OriginFeedback},
		{"project", conduitMemory.OriginProject},
		{"reference", conduitMemory.OriginReference},
		{"observation", conduitMemory.OriginObservation},
		{"", conduitMemory.OriginObservation},
	}
	for _, tt := range tests {
		got := mapOrigin(tt.input)
		if got != tt.want {
			t.Errorf("mapOrigin(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestMapTrigger verifies trigger string mapping.
func TestMapTrigger(t *testing.T) {
	tests := []struct {
		input string
		want  conduitMemory.Trigger
	}{
		{"explicit", conduitMemory.TriggerExplicit},
		{"post_compact", conduitMemory.TriggerPostCompact},
		{"per_turn", conduitMemory.TriggerPerTurn},
		{"promotion", conduitMemory.TriggerPromotion},
		{"manual", conduitMemory.TriggerManual},
		{"", conduitMemory.TriggerManual},
	}
	for _, tt := range tests {
		got := mapTrigger(tt.input)
		if got != tt.want {
			t.Errorf("mapTrigger(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// Ensure fmt is used (for error formatting in tests).
var _ = fmt.Sprintf
