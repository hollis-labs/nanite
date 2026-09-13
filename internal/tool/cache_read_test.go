package tool

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestResultPreview_TaskFieldsAndCommentEnds(t *testing.T) {
	cache, db := setupTestCache(t)
	defer db.Close()
	cache.hardCapBytes = DefaultHardCapBytes
	comments := []any{}
	for i := 0; i < 8; i++ {
		comments = append(comments, map[string]any{"content": strings.Repeat("historical investigation ", 200)})
	}
	comments[7] = map[string]any{"content": "DEPLOYMENT VERIFIED; current code fixes discovery"}
	data := map[string]any{"ok": true, "data": map[string]any{
		"id": "task-example", "title": "Repair discovery", "status": "review", "comments": comments,
		"description": strings.Repeat("Background detail. ", 700),
	}}
	body, _ := json.MarshalIndent(data, "", "  ")
	view, err := cache.PresentResult("session", "call", "task_get", string(body), 4000)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Repair discovery", "review", "DEPLOYMENT VERIFIED", "OMITTED items", "tool_result://"} {
		if !strings.Contains(view.Content, want) {
			t.Errorf("preview lost %q: %s", want, view.Content)
		}
	}
	page, err := cache.ReadPage("session", view.CacheID, "/data/comments/7/content", 0, 0, 4000)
	if err != nil || page.Content != "DEPLOYMENT VERIFIED; current code fixes discovery" || page.HasMore {
		t.Fatalf("JSON field recovery: %+v, %v", page, err)
	}
	full, _, err := cache.Fetch("session", view.CacheID, 0, 0)
	if err != nil || full != string(body) {
		t.Fatal("presentation modified the stored original")
	}
}

func TestResultPreview_LongTextAndPython(t *testing.T) {
	text := "FIRST SOURCE\n" + strings.Repeat("é", 40000) + "\nLAST SOURCE"
	view := previewText(text, 4000)
	if !strings.Contains(view, "FIRST SOURCE") || !strings.Contains(view, "LAST SOURCE") || !strings.Contains(view, "OMITTED bytes") || !utf8.ValidString(view) || len(view) > 4000 {
		t.Fatalf("invalid text preview: %s", view)
	}
	body, _ := json.Marshal(map[string]any{"stdout": text, "stderr": "diagnostic", "result": nil, "error": "scan stopped"})
	view, format := previewResult(string(body), 4000)
	for _, want := range []string{"FIRST SOURCE\n", "LAST SOURCE", "scan stopped", "diagnostic", "/stdout"} {
		if !strings.Contains(view, want) {
			t.Errorf("Python preview lost %q: %s", want, view)
		}
	}
	if format != "json" || len(view) > 4000 {
		t.Fatalf("unexpected preview format or budget: %s / %d", format, len(view))
	}
}

func TestResultCache_ReadPagesPreserveTextAndScope(t *testing.T) {
	cache, db := setupTestCache(t)
	defer db.Close()
	cache.hardCapBytes = DefaultHardCapBytes
	text := strings.Repeat("é🙂 source line\n", 700)
	body, _ := json.Marshal(map[string]any{"stdout": text, "a/b~c": []any{json.Number("9007199254740993")}})
	view, err := cache.PresentResult("owner", "call", "python_run", string(body), 4000)
	if err != nil {
		t.Fatal(err)
	}
	var read strings.Builder
	for offset := 0; ; {
		page, pageErr := cache.ReadPage("owner", view.CacheID, "/stdout", offset, 1<<29, 257)
		if pageErr != nil || !utf8.ValidString(page.Content) || len(page.Content) > 257 {
			t.Fatalf("page: %+v, %v", page, pageErr)
		}
		read.WriteString(page.Content)
		if !page.HasMore {
			break
		}
		if page.End <= offset {
			t.Fatal("page made no progress")
		}
		offset = page.End
	}
	if read.String() != text {
		t.Fatal("paging lost, duplicated, or corrupted decoded stdout")
	}
	page, err := cache.ReadPage("owner", view.CacheID, "/a~1b~0c/0", 0, 0, 4000)
	if err != nil || page.Content != "9007199254740993" {
		t.Fatalf("pointer escaping/number precision: %+v %v", page, err)
	}
	if _, err := cache.ReadPage("other-session", view.CacheID, "/stdout", 0, 0, 4000); err == nil {
		t.Fatal("cross-session retrieval succeeded")
	}
	if _, err := cache.SearchPage("other-session", view.CacheID, "", "source", 0, 20, 4000); err == nil {
		t.Fatal("cross-session search succeeded")
	}
	for _, pointer := range []string{"stdout", "/missing", "/stdout/0", "/a~2b", "/a~1b~0c/01"} {
		if _, err := cache.ReadPage("owner", view.CacheID, pointer, 0, 0, 4000); err == nil {
			t.Errorf("invalid pointer %q succeeded", pointer)
		}
	}
	if _, err := db.Exec("UPDATE tool_result_cache SET expires_at = '2020-01-01T00:00:00Z'"); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.ReadPage("owner", view.CacheID, "", 0, 0, 4000); err == nil {
		t.Fatal("expired retrieval succeeded")
	}
}

func TestResultCache_SearchContinuationAndFetchCoordinates(t *testing.T) {
	cache, db := setupTestCache(t)
	defer db.Close()
	cache.hardCapBytes = DefaultHardCapBytes
	text := strings.Repeat("prefix ", 1000) + "NEEDLE one\nother\nNEEDLE two\nNEEDLE three\n"
	body, _ := json.Marshal(map[string]any{"stdout": text})
	view, err := cache.PresentResult("s", "call", "python_run", string(body), 4000)
	if err != nil {
		t.Fatal(err)
	}
	page, err := cache.SearchPage("s", view.CacheID, "/stdout", "NEEDLE", 0, 1, 4000)
	if err != nil || !page.HasMore || len(page.Matches) != 1 {
		t.Fatalf("first search: %+v %v", page, err)
	}
	match := page.Matches[0]
	if !match.ContextTruncated || !strings.Contains(match.Context, "NEEDLE one") {
		t.Fatalf("long-line context: %+v", match)
	}
	read, err := cache.ReadPage("s", view.CacheID, "/stdout", match.MatchOffset, match.MatchEnd-match.MatchOffset, 4000)
	if err != nil || read.Content != "NEEDLE" {
		t.Fatalf("search coordinates did not fetch the match: %+v %v", read, err)
	}
	next, err := cache.SearchPage("s", view.CacheID, "/stdout", "NEEDLE", page.NextOffset, 20, 4000)
	if err != nil || next.HasMore || len(next.Matches) != 2 {
		t.Fatalf("continued search: %+v %v", next, err)
	}
	if next.Matches[0].MatchOffset <= match.MatchOffset {
		t.Fatal("search repeated a previous match")
	}
}
