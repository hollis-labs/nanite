package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleListCatalogSourcesEmptyReturnsJSONArray(t *testing.T) {
	cs, _ := setupCatalogTestState(t)
	if err := cs.store.Seed(context.Background()); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/plugins/catalog/sources", nil)
	rec := httptest.NewRecorder()
	cs.handleListSources(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != "[]\n" {
		t.Fatalf("body = %q, want JSON empty array", got)
	}
}

func TestHandleBrowseCatalogEmptyReturnsJSONArray(t *testing.T) {
	cs, _ := setupCatalogTestState(t)
	if err := cs.store.Seed(context.Background()); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/plugins/catalog", nil)
	rec := httptest.NewRecorder()
	cs.handleBrowseCatalog(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != "[]\n" {
		t.Fatalf("body = %q, want JSON empty array", got)
	}
}

func TestHandleRefreshCatalogEmptyReturnsStatusObject(t *testing.T) {
	cs, _ := setupCatalogTestState(t)

	req := httptest.NewRequest(http.MethodPost, "/api/plugins/catalog/refresh", nil)
	rec := httptest.NewRecorder()
	cs.handleRefreshCatalog(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body) != 1 || body["status"] != "cache invalidated" {
		t.Fatalf("body = %#v, want status object", body)
	}
}
