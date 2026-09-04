package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/memory"
)

func TestMemoryCreateListAndEditPreserveFullBody(t *testing.T) {
	a, mux := newTestAPI(t)

	create := httptest.NewRequest(http.MethodPost, "/api/memories", bytes.NewBufferString(`{
		"summary":"Review summary",
		"body":"PRESERVE-ME",
		"origin":"user",
		"confidence":0.8,
		"scope":"user",
		"tags":[]
	}`))
	create.Header.Set("Content-Type", "application/json")
	created := httptest.NewRecorder()
	mux.ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("POST /api/memories = %d, want 201; body=%s", created.Code, created.Body.String())
	}

	list := httptest.NewRequest(http.MethodGet, "/api/memories?scope=user", nil)
	listed := httptest.NewRecorder()
	mux.ServeHTTP(listed, list)
	if listed.Code != http.StatusOK {
		t.Fatalf("GET /api/memories = %d, want 200; body=%s", listed.Code, listed.Body.String())
	}
	var response struct {
		Memories []struct {
			memory.Memory
			Key string `json:"key"`
		} `json:"memories"`
	}
	if err := json.NewDecoder(listed.Body).Decode(&response); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(response.Memories) != 1 {
		t.Fatalf("listed memories = %d, want 1", len(response.Memories))
	}
	got := response.Memories[0]
	if got.MemoryKey != "review_summary" {
		t.Fatalf("derived memory key = %q, want v0.9-valid review_summary", got.MemoryKey)
	}
	if got.Body != "PRESERVE-ME" {
		t.Fatalf("listed body = %q, want full editable body", got.Body)
	}
	if got.Key == "" {
		t.Fatal("list response omitted path-safe memory key")
	}

	// An older/projected client may omit body. Omission means "unchanged",
	// never "replace the stored body with empty".
	update := httptest.NewRequest(http.MethodPut, "/api/memories/"+got.Key,
		bytes.NewBufferString(`{"summary":"Edited summary","origin":"user","confidence":0.8,"tags":[]}`))
	update.Header.Set("Content-Type", "application/json")
	updated := httptest.NewRecorder()
	mux.ServeHTTP(updated, update)
	if updated.Code != http.StatusOK {
		t.Fatalf("PUT /api/memories/{key} = %d, want 200; body=%s", updated.Code, updated.Body.String())
	}
	stored, err := a.Services.Memory.Get(t.Context(), memory.UserNamespace("default"), "review_summary")
	if err != nil {
		t.Fatalf("get updated memory: %v", err)
	}
	if stored.Summary != "Edited summary" || stored.Body != "PRESERVE-ME" {
		t.Fatalf("updated memory = %+v, want edited summary and preserved body", stored)
	}
}

func TestListMemoriesUnavailableWithoutEmbeddedFacade(t *testing.T) {
	a, mux := newTestAPI(t)
	a.Services.Memory = nil

	req := httptest.NewRequest(http.MethodGet, "/api/memories", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /api/memories without local facade = %d, want 503; body=%s", w.Code, w.Body.String())
	}
}

func TestListMemories_FiltersBeforePaginationAndReturnsTrueTotal(t *testing.T) {
	a, mux := newTestAPI(t)
	ctx := context.Background()
	namespace := memory.UserNamespace("default")

	// These share the requested status and rank ahead of the matching rows,
	// but do not match q. More than 500 proves the list path cannot rely on
	// Tesseract Recall's finite candidate cap before applying text search.
	for i := 0; i < 505; i++ {
		if err := a.Services.Memory.Store(ctx, memory.Memory{
			Namespace: namespace, MemoryKey: fmt.Sprintf("reviewed_nonmatch_%d", i),
			Summary: "unrelated high-ranked record", Origin: "user", Trigger: "manual",
			Confidence: 1, SessionID: "pagination-test", Status: "reviewed",
		}); err != nil {
			t.Fatalf("store nonmatch %d: %v", i, err)
		}
	}
	for i := 0; i < 4; i++ {
		if err := a.Services.Memory.Store(ctx, memory.Memory{
			Namespace: namespace, MemoryKey: fmt.Sprintf("reviewed_%d", i),
			Summary: fmt.Sprintf("needle reviewed record %d", i), Origin: "observation", Trigger: "manual",
			Confidence: 0.2, SessionID: "pagination-test", Status: "reviewed",
		}); err != nil {
			t.Fatalf("store reviewed %d: %v", i, err)
		}
	}
	if err := a.Services.Memory.Store(ctx, memory.Memory{
		Namespace: namespace, MemoryKey: "unicode_case_match",
		Summary: "CAFÉ migration", Origin: "observation", Trigger: "manual",
		Confidence: 0.2, SessionID: "pagination-test", Status: "reviewed",
	}); err != nil {
		t.Fatalf("store Unicode match: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/memories?scope=user&status=reviewed&q=needle&limit=2&offset=1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/memories = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var response struct {
		Memories []memory.Memory `json:"memories"`
		Total    int             `json:"total"`
	}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Total != 4 {
		t.Fatalf("total = %d, want 4 matches independent of the page length", response.Total)
	}
	if len(response.Memories) != 2 {
		t.Fatalf("page length = %d, want requested limit 2", len(response.Memories))
	}
	for _, recalled := range response.Memories {
		if recalled.Status != "reviewed" || !strings.Contains(strings.ToLower(recalled.Summary+recalled.Body), "needle") {
			t.Errorf("returned non-matching memory: %+v", recalled)
		}
	}

	// Status-only paging beyond Tesseract's 500-result recall cap must still
	// return a full page from the 510 matching current revisions.
	req = httptest.NewRequest(http.MethodGet, "/api/memories?scope=user&status=reviewed&limit=3&offset=500", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("large-offset GET = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	response = struct {
		Memories []memory.Memory `json:"memories"`
		Total    int             `json:"total"`
	}{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode large-offset response: %v", err)
	}
	if response.Total != 510 || len(response.Memories) != 3 {
		t.Fatalf("large-offset page: total=%d len=%d, want total=510 len=3", response.Total, len(response.Memories))
	}

	// Page and total share the same Go Unicode case-folding predicate. SQLite
	// LOWER is ASCII-only and previously counted this uppercase É differently.
	req = httptest.NewRequest(http.MethodGet, "/api/memories?scope=user&status=reviewed&q=caf%C3%A9&limit=10", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Unicode search GET = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	response = struct {
		Memories []memory.Memory `json:"memories"`
		Total    int             `json:"total"`
	}{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode Unicode response: %v", err)
	}
	if response.Total != 1 || len(response.Memories) != 1 || response.Memories[0].MemoryKey != "unicode_case_match" {
		t.Fatalf("Unicode search page: total=%d memories=%+v", response.Total, response.Memories)
	}
}
