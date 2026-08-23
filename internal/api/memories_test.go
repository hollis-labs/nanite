package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/memory"
)

func TestListMemories_FiltersBeforePaginationAndReturnsTrueTotal(t *testing.T) {
	a, mux := newTestAPI(t)
	ctx := context.Background()
	namespace := memory.UserNamespace("default")

	// These rank ahead of the reviewed records but do not match either
	// requested filter. The old limit+offset-then-filter path fetched only
	// these rows and under-returned an empty page.
	for i := 0; i < 5; i++ {
		if err := a.Services.Memory.Store(ctx, memory.Memory{
			Namespace: namespace, MemoryKey: fmt.Sprintf("canonical_%d", i),
			Summary: "unrelated high-ranked record", Origin: "user", Trigger: "manual",
			Confidence: 1, SessionID: "pagination-test", Status: "canonical",
		}); err != nil {
			t.Fatalf("store canonical %d: %v", i, err)
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
}
