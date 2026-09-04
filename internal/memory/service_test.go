package memory

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract"
	tesseractMemory "github.com/hollis-labs/tesseract/memory"
)

func newTestTesseract(t *testing.T) (*tesseract.Tesseract, func()) {
	t.Helper()
	dir := t.TempDir()
	instance, err := tesseract.Open(context.Background(), tesseract.Config{RootDir: dir})
	if err != nil {
		t.Fatalf("tesseract.Open: %v", err)
	}
	var dbFile string
	rows, err := instance.MemoryStore().DB().Query("PRAGMA database_list")
	if err != nil {
		_ = instance.Close()
		t.Fatalf("PRAGMA database_list: %v", err)
	}
	for rows.Next() {
		var seq int
		var name, path string
		if err := rows.Scan(&seq, &name, &path); err != nil {
			_ = rows.Close()
			_ = instance.Close()
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
			return instance, func() { _ = instance.Close() }
		}
		_ = instance.Close()
		t.Fatalf("test Tesseract DB %q escapes temp root %q (canonical root=%q err=%v; db=%q err=%v)",
			dbFile, dir, canonicalDir, dirErr, canonicalDB, dbErr)
	}
	return instance, func() { _ = instance.Close() }
}

func memoryTestPathUnder(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func storeTestMemory(t *testing.T, svc *Service, namespace, key, summary, body string, confidence float64) {
	t.Helper()
	if err := svc.Store(context.Background(), Memory{
		Namespace: namespace, MemoryKey: key, Summary: summary, Body: body,
		Origin: "user", Trigger: "explicit", Confidence: confidence,
		SessionID: "test-session", Status: "reviewed",
	}); err != nil {
		t.Fatalf("store %s: %v", key, err)
	}
}

func TestMemoryStoreAndDefaultSummaryProjection(t *testing.T) {
	instance, cleanup := newTestTesseract(t)
	defer cleanup()
	svc := NewService(instance.MemoryStore())
	namespace := SessionNamespace("test-123")
	storeTestMemory(t, svc, namespace, "prefers_terse", "User prefers terse output", "full body", 0.9)

	memories, err := svc.Recall(context.Background(), RecallOpts{
		Namespaces: []string{namespace}, Ranking: RankingActivation, Limit: 10,
	})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(memories) != 1 {
		t.Fatalf("got %d memories, want 1", len(memories))
	}
	if memories[0].Summary != "User prefers terse output" || memories[0].Body != "" {
		t.Fatalf("summary projection = %+v", memories[0])
	}
	if memories[0].PayloadMode != "summary" {
		t.Fatalf("payload mode = %q, want summary", memories[0].PayloadMode)
	}
	if memories[0].Score == nil {
		t.Fatal("activation score is nil; want numeric score")
	}
}

func TestMemoryRecallPagedProjectedAndNullableScores(t *testing.T) {
	instance, cleanup := newTestTesseract(t)
	defer cleanup()
	svc := NewService(instance.MemoryStore())
	namespace := UserNamespace("paged")
	for _, key := range []string{"one", "two", "three"} {
		storeTestMemory(t, svc, namespace, key, "summary "+key, "body "+key, 0.8)
	}

	first, err := svc.RecallPage(context.Background(), RecallOpts{
		Namespaces: []string{namespace}, Ranking: RankingChronological, Limit: 2,
	})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if first.Manifest == nil {
		t.Fatal("manifest is nil")
	}
	if first.Total != 3 || first.Manifest.ResultsTotal != 3 || first.Manifest.ResultsReturned != 2 ||
		!first.Manifest.Truncated || first.Manifest.NextCursor == nil {
		t.Fatalf("first manifest = %+v total=%d", *first.Manifest, first.Total)
	}
	for _, memory := range first.Memories {
		if memory.Score != nil {
			t.Errorf("chronological score = %v, want nil", *memory.Score)
		}
		if memory.PayloadMode != "summary" || memory.Body != "" || memory.Summary == "" {
			t.Errorf("summary projection = %+v", memory)
		}
	}

	second, err := svc.RecallPage(context.Background(), RecallOpts{
		Namespaces: []string{namespace}, Ranking: RankingChronological, Limit: 2,
		Cursor: *first.Manifest.NextCursor, PayloadMode: PayloadModeKeys,
	})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(second.Memories) != 1 || second.Manifest == nil || second.Manifest.NextCursor != nil {
		t.Fatalf("second page = %+v", second)
	}
	if got := second.Memories[0]; got.PayloadMode != "keys" || got.Summary != "" || got.Body != "" {
		t.Fatalf("keys projection = %+v", got)
	}

	full, err := svc.RecallPage(context.Background(), RecallOpts{
		Namespaces: []string{namespace}, Ranking: RankingChronological, Limit: 1, PayloadMode: PayloadModeFull,
	})
	if err != nil {
		t.Fatalf("full page: %v", err)
	}
	if len(full.Memories) != 1 || full.Memories[0].Body == "" || full.Memories[0].PayloadMode != "" {
		t.Fatalf("full projection = %+v", full.Memories)
	}
}

func TestMemoryRecallLexicalScoreIsNullable(t *testing.T) {
	instance, cleanup := newTestTesseract(t)
	defer cleanup()
	svc := NewService(instance.MemoryStore())
	namespace := UserNamespace("lexical", "decisions")
	storeTestMemory(t, svc, namespace, "ticket", "Fix CW-20260904-0058 migration", "details", 0.9)

	results, err := svc.Recall(context.Background(), RecallOpts{
		Namespaces: []string{namespace}, Ranking: RankingRelevance, SearchMode: SearchModeLexical,
		Query: "CW-20260904-0058", Limit: 5,
	})
	if err != nil {
		t.Fatalf("lexical recall: %v", err)
	}
	if len(results) != 1 || results[0].Score != nil {
		t.Fatalf("lexical results = %+v; want one result with nil score", results)
	}
}

func TestMemoryEstimateOnlyMatchesManifestAndWithholdsRows(t *testing.T) {
	instance, cleanup := newTestTesseract(t)
	defer cleanup()
	svc := NewService(instance.MemoryStore())
	namespace := UserNamespace("estimate")
	storeTestMemory(t, svc, namespace, "one", "estimate one", "body", 0.8)

	page, err := svc.RecallPage(context.Background(), RecallOpts{
		Namespaces: []string{namespace}, Limit: 10, EstimateOnly: true,
	})
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	if len(page.Memories) != 0 || page.Manifest == nil || page.Manifest.ResultsReturned != 1 || page.Manifest.BytesReturned == 0 {
		t.Fatalf("estimate page = %+v", page)
	}
}

func TestMemoryHydrateAndTouchLifecycle(t *testing.T) {
	instance, cleanup := newTestTesseract(t)
	defer cleanup()
	svc := NewService(instance.MemoryStore())
	ctx := context.Background()
	namespace := UserNamespace("lifecycle")
	storeTestMemory(t, svc, namespace, "selected", "selected summary", "selected body", 0.8)
	storeTestMemory(t, svc, namespace, "ignored", "ignored summary", "ignored body", 0.7)

	page, err := svc.RecallPage(ctx, RecallOpts{Namespaces: []string{namespace}, Limit: 10})
	if err != nil || len(page.Memories) != 2 {
		t.Fatalf("recall page = %+v err=%v", page, err)
	}
	selected := page.Memories[0]
	if touchErr := svc.Touch(ctx, []string{selected.RevisionID, selected.RevisionID}); touchErr != nil {
		t.Fatalf("touch: %v", touchErr)
	}
	var selectedAccess, ignoredAccess int
	if stateErr := instance.MemoryStore().DB().QueryRowContext(ctx,
		`SELECT access_count FROM memory_state WHERE namespace = ? AND memory_key = ?`, namespace, selected.MemoryKey).Scan(&selectedAccess); stateErr != nil {
		t.Fatalf("selected state: %v", stateErr)
	}
	ignoredKey := "ignored"
	if selected.MemoryKey == ignoredKey {
		ignoredKey = "selected"
	}
	if stateErr := instance.MemoryStore().DB().QueryRowContext(ctx,
		`SELECT access_count FROM memory_state WHERE namespace = ? AND memory_key = ?`, namespace, ignoredKey).Scan(&ignoredAccess); stateErr != nil {
		t.Fatalf("ignored state: %v", stateErr)
	}
	if selectedAccess != 1 || ignoredAccess != 0 {
		t.Fatalf("access counts selected=%d ignored=%d, want 1/0", selectedAccess, ignoredAccess)
	}

	hydrated, err := svc.GetRevision(ctx, selected.RevisionID)
	if err != nil {
		t.Fatalf("hydrate: %v", err)
	}
	if hydrated.Body == "" {
		t.Fatal("hydrated body is empty")
	}
}

func TestMemoryRecallOffsetUsesPublicPageAndTotal(t *testing.T) {
	instance, cleanup := newTestTesseract(t)
	defer cleanup()
	svc := NewService(instance.MemoryStore())
	namespace := UserNamespace("offset")
	for _, key := range []string{"one", "two", "three"} {
		storeTestMemory(t, svc, namespace, key, "needle "+key, "body", 0.8)
	}
	page, err := svc.RecallPage(context.Background(), RecallOpts{
		Namespaces: []string{namespace}, Search: "needle", Limit: 1, Offset: 1,
	})
	if err != nil {
		t.Fatalf("offset page: %v", err)
	}
	if page.Total != 3 || len(page.Memories) != 1 || page.Manifest != nil {
		t.Fatalf("offset page = %+v", page)
	}
}

func TestMemoryReadPrefixSpansTypedNamespaces(t *testing.T) {
	instance, cleanup := newTestTesseract(t)
	defer cleanup()
	svc := NewService(instance.MemoryStore())
	storeTestMemory(t, svc, UserNamespace("prefix", "notes"), "note", "shared", "body", 0.8)
	storeTestMemory(t, svc, UserNamespace("prefix", "decisions"), "decision", "shared", "body", 0.8)

	page, err := svc.RecallPage(context.Background(), RecallOpts{
		Namespaces: []string{UserMemoryPrefix("prefix")}, Ranking: RankingChronological, Limit: 10,
	})
	if err != nil {
		t.Fatalf("prefix recall: %v", err)
	}
	if page.Total != 2 || len(page.Memories) != 2 {
		t.Fatalf("prefix page = %+v", page)
	}
}

func TestMemoryGetDeprecateAndExtraction(t *testing.T) {
	instance, cleanup := newTestTesseract(t)
	defer cleanup()
	svc := NewService(instance.MemoryStore())
	namespace := UserNamespace("get")
	storeTestMemory(t, svc, namespace, "to_deprecate", "A test memory", "body", 0.8)
	memory, err := svc.Get(context.Background(), namespace, "to_deprecate")
	if err != nil || memory.Summary != "A test memory" {
		t.Fatalf("get = %+v err=%v", memory, err)
	}
	if deprecateErr := svc.Deprecate(context.Background(), memory.RevisionID); deprecateErr != nil {
		t.Fatalf("deprecate: %v", deprecateErr)
	}
	results, err := svc.Recall(context.Background(), RecallOpts{Namespaces: []string{namespace}})
	if err != nil || len(results) != 0 {
		t.Fatalf("post-deprecate recall = %+v err=%v", results, err)
	}

	utilityCall := func(_ context.Context, _ string) (string, error) {
		return `[{"memory_key":"extracted","summary":"A learned fact","origin":"project","confidence":0.9}]`, nil
	}
	extractor := NewExtractor(svc, utilityCall)
	extractor.extractPostCompact("session-123", 5000)
	extracted, err := svc.Recall(context.Background(), RecallOpts{Namespaces: []string{SessionNamespace("session-123")}})
	if err != nil || len(extracted) != 1 || extracted[0].MemoryKey != "extracted" {
		t.Fatalf("extracted recall = %+v err=%v", extracted, err)
	}
}

func TestMemorySignalsAndNamespaces(t *testing.T) {
	for _, input := range []string{
		"Please remember this: I always prefer tabs over spaces",
		"From now on, use Go 1.22 features",
		"No, don't do that. Use the other approach.",
	} {
		if !HasMemorySignal(input) {
			t.Errorf("expected signal in %q", input)
		}
	}
	if HasMemorySignal("Can you help me parse JSON?") {
		t.Error("ordinary request triggered a memory signal")
	}
	if got := SessionNamespace("abc"); got != "user/default/session/abc/memory/notes" {
		t.Errorf("session namespace = %q", got)
	}
	if got := ProjectNamespace("nanite", "decisions"); got != "user/default/project/nanite/memory/decisions" {
		t.Errorf("project namespace = %q", got)
	}
	if got := UserNamespace("chrispian"); got != "user/chrispian/memory/notes" {
		t.Errorf("user namespace = %q", got)
	}
	if got := SessionMemoryPrefix("abc"); got != "user/default/session/abc/memory" {
		t.Errorf("session prefix = %q", got)
	}
	if got := ProjectMemoryPrefix("nanite"); got != "user/default/project/nanite/memory" {
		t.Errorf("project prefix = %q", got)
	}
	if got := UserMemoryPrefix("chrispian"); got != "user/chrispian/memory" {
		t.Errorf("user prefix = %q", got)
	}
	if got := AllNaniteNamespaces(); len(got) != 1 || got[0] != "user/default/memory" {
		t.Errorf("read prefixes = %v", got)
	}
}

func TestMemoryServiceNilStoreAndValidation(t *testing.T) {
	svc := NewService(nil)
	if err := svc.Store(context.Background(), Memory{}); err == nil {
		t.Error("nil store write succeeded")
	}
	if _, err := svc.Recall(context.Background(), RecallOpts{}); err == nil {
		t.Error("nil store recall succeeded")
	}
	if _, err := svc.GetRevision(context.Background(), "rev"); err == nil {
		t.Error("nil store hydrate succeeded")
	}
	if err := svc.Touch(context.Background(), []string{"rev"}); err == nil {
		t.Error("nil store touch succeeded")
	}
}

func TestMapOriginAndTrigger(t *testing.T) {
	if got := mapOrigin(""); got != tesseractMemory.OriginObservation {
		t.Errorf("default origin = %q", got)
	}
	if got := mapOrigin("feedback"); got != tesseractMemory.OriginFeedback {
		t.Errorf("feedback origin = %q", got)
	}
	if got := mapTrigger(""); got != tesseractMemory.TriggerManual {
		t.Errorf("default trigger = %q", got)
	}
	if got := mapTrigger("post_compact"); got != tesseractMemory.TriggerPostCompact {
		t.Errorf("post-compact trigger = %q", got)
	}
}
