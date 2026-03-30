package plugin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const testCatalogYAML = `version: 1
plugins:
  - name: hello-plugin
    version: "1.0.0"
    description: A hello world plugin
    author: Test Author
    archive_url: https://example.com/hello-plugin-1.0.0.tar.gz
    checksum: "sha256:abcdef1234567890"
    runtime: subprocess
  - name: goodbye-plugin
    version: "2.1.0"
    description: A goodbye plugin
    archive_url: https://example.com/goodbye-plugin-2.1.0.tar.gz
`

func TestCatalogFetcher_FetchAndMerge(t *testing.T) {
	// Source 1: serves two plugins.
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testCatalogYAML))
	}))
	defer srv1.Close()

	// Source 2: serves one plugin that overlaps with source 1.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`version: 1
plugins:
  - name: hello-plugin
    version: "1.1.0"
    description: Updated hello from custom source
    archive_url: https://custom.example.com/hello-1.1.0.tar.gz
  - name: custom-only
    version: "0.5.0"
    description: Only in custom source
    archive_url: https://custom.example.com/custom-only-0.5.0.tar.gz
`))
	}))
	defer srv2.Close()

	cacheDir := t.TempDir()
	fetcher := NewCatalogFetcher(1*time.Minute, cacheDir)

	sources := []CatalogSource{
		{ID: "official", Name: "Official", URL: srv1.URL, Priority: 100, Enabled: true},
		{ID: "custom", Name: "Custom", URL: srv2.URL, Priority: 50, Enabled: true},
	}

	entries, err := fetcher.Fetch(context.Background(), sources)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	// Should have 3 unique plugins: hello-plugin (from official, higher priority),
	// goodbye-plugin (from official), custom-only (from custom).
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	byName := make(map[string]MergedCatalogEntry)
	for _, e := range entries {
		byName[e.Name] = e
	}

	// hello-plugin should come from official (priority 100 > 50).
	hello, ok := byName["hello-plugin"]
	if !ok {
		t.Fatal("hello-plugin not found")
	}
	if hello.Version != "1.0.0" {
		t.Errorf("hello-plugin version: expected 1.0.0, got %s", hello.Version)
	}
	if hello.SourceName != "Official" {
		t.Errorf("hello-plugin source: expected Official, got %s", hello.SourceName)
	}

	// goodbye-plugin from official.
	goodbye, ok := byName["goodbye-plugin"]
	if !ok {
		t.Fatal("goodbye-plugin not found")
	}
	if goodbye.Version != "2.1.0" {
		t.Errorf("goodbye-plugin version: expected 2.1.0, got %s", goodbye.Version)
	}

	// custom-only from custom.
	custom, ok := byName["custom-only"]
	if !ok {
		t.Fatal("custom-only not found")
	}
	if custom.SourceName != "Custom" {
		t.Errorf("custom-only source: expected Custom, got %s", custom.SourceName)
	}
}

func TestCatalogFetcher_Cache(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Write([]byte(testCatalogYAML))
	}))
	defer srv.Close()

	fetcher := NewCatalogFetcher(5*time.Minute, t.TempDir())
	sources := []CatalogSource{
		{ID: "test", Name: "Test", URL: srv.URL, Priority: 100, Enabled: true},
	}

	// First fetch should hit the server.
	fetcher.Fetch(context.Background(), sources)
	if callCount != 1 {
		t.Errorf("expected 1 call, got %d", callCount)
	}

	// Second fetch should use cache.
	fetcher.Fetch(context.Background(), sources)
	if callCount != 1 {
		t.Errorf("expected still 1 call after cache hit, got %d", callCount)
	}

	// Invalidate and fetch again.
	fetcher.Invalidate()
	fetcher.Fetch(context.Background(), sources)
	if callCount != 2 {
		t.Errorf("expected 2 calls after invalidate, got %d", callCount)
	}
}

func TestCatalogFetcher_DisabledSource(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("disabled source should not be fetched")
	}))
	defer srv.Close()

	fetcher := NewCatalogFetcher(1*time.Minute, t.TempDir())
	sources := []CatalogSource{
		{ID: "disabled", Name: "Disabled", URL: srv.URL, Priority: 100, Enabled: false},
	}

	entries, err := fetcher.Fetch(context.Background(), sources)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries from disabled source, got %d", len(entries))
	}
}

func TestCatalogFetcher_DiskCacheFallback(t *testing.T) {
	cacheDir := t.TempDir()
	fetcher := NewCatalogFetcher(0, cacheDir) // TTL=0 so we always fetch

	// Pre-populate disk cache.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testCatalogYAML))
	}))
	sources := []CatalogSource{
		{ID: "fallback-test", Name: "Fallback", URL: srv.URL, Priority: 100, Enabled: true},
	}
	// Fetch once to populate disk cache.
	fetcher.Fetch(context.Background(), sources)
	srv.Close() // Stop the server.

	// Now fetch again — server is gone, should fall back to disk cache.
	fetcher.Invalidate()
	entries, err := fetcher.Fetch(context.Background(), sources)
	if err != nil {
		t.Fatalf("Fetch with fallback: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 entries from disk cache, got %d", len(entries))
	}
}

func TestVerifyChecksum(t *testing.T) {
	// Create a temp file with known content.
	f, _ := os.CreateTemp(t.TempDir(), "test-*")
	f.Write([]byte("hello world"))
	f.Close()

	// Compute expected checksum.
	// sha256("hello world") = b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9
	expected := "sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"

	if err := VerifyChecksum(f.Name(), expected); err != nil {
		t.Errorf("valid checksum failed: %v", err)
	}

	// Wrong checksum.
	if err := VerifyChecksum(f.Name(), "sha256:0000"); err == nil {
		t.Error("expected error for wrong checksum")
	}

	// Empty checksum (should pass — no verification).
	if err := VerifyChecksum(f.Name(), ""); err != nil {
		t.Errorf("empty checksum should pass: %v", err)
	}
}

func TestCatalogFetcher_PriorityOverride(t *testing.T) {
	// Custom source has HIGHER priority and should override official.
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`version: 1
plugins:
  - name: shared-plugin
    version: "1.0.0"
    description: From official
    archive_url: https://official.example.com/shared-1.0.0.tar.gz
`))
	}))
	defer srv1.Close()

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`version: 1
plugins:
  - name: shared-plugin
    version: "1.5.0"
    description: From custom fork
    archive_url: https://custom.example.com/shared-1.5.0.tar.gz
`))
	}))
	defer srv2.Close()

	fetcher := NewCatalogFetcher(1*time.Minute, t.TempDir())
	sources := []CatalogSource{
		{ID: "official", Name: "Official", URL: srv1.URL, Priority: 50, Enabled: true},
		{ID: "custom", Name: "Custom Fork", URL: srv2.URL, Priority: 200, Enabled: true},
	}

	entries, err := fetcher.Fetch(context.Background(), sources)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Version != "1.5.0" {
		t.Errorf("expected version 1.5.0 from higher-priority source, got %s", entries[0].Version)
	}
	if entries[0].SourceName != "Custom Fork" {
		t.Errorf("expected source 'Custom Fork', got %s", entries[0].SourceName)
	}
}

func TestVerifyChecksum_BadFormat(t *testing.T) {
	f, _ := os.CreateTemp(t.TempDir(), "test-*")
	f.Close()
	err := VerifyChecksum(f.Name(), "md5:abc123")
	if err == nil {
		t.Error("expected error for unsupported format")
	}
}

func TestCatalogDiskCachePath(t *testing.T) {
	fetcher := NewCatalogFetcher(1*time.Minute, "/tmp/test-cache")
	path := fetcher.diskCachePath("my-source-id")
	if !filepath.IsAbs(path) {
		t.Errorf("expected absolute path, got %q", path)
	}

	// Empty cache dir returns empty path.
	fetcher2 := NewCatalogFetcher(1*time.Minute, "")
	if fetcher2.diskCachePath("x") != "" {
		t.Error("expected empty path for empty cacheDir")
	}
}
