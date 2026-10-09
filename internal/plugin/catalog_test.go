package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sharedcatalog "github.com/hollis-labs/plugins-catalog"
)

func catalogFixture(t *testing.T, ids ...string) []byte {
	t.Helper()
	doc := sharedcatalog.Document{SchemaVersion: 2, CatalogVersion: "0.1.0", GeneratedAt: "2026-10-01T00:00:00Z", Plugins: []sharedcatalog.Plugin{}}
	for _, id := range ids {
		doc.Plugins = append(doc.Plugins, sharedcatalog.Plugin{
			ID: id, Name: "Display " + id, Version: "1.0.0",
			Source:         sharedcatalog.Source{Type: "git", Repo: "https://github.com/example/plugins", Tag: "v1.0.0"},
			Archives:       []sharedcatalog.Archive{{Platform: runtime.GOOS + "-" + runtime.GOARCH, URL: "https://example.com/plugin.tar.gz", SHA256: strings.Repeat("a", 64), Size: 123}},
			ManifestSHA256: strings.Repeat("b", 64), Directory: sharedcatalog.Directory{Status: "active"},
		})
		if err := json.Unmarshal([]byte(`{"nanite":{"min":"0.1.0"}}`), &doc.Plugins[len(doc.Plugins)-1].Hosts); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sharedcatalog.Decode(raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestCatalogFetcher_FetchAndMerge(t *testing.T) {
	raw := catalogFixture(t, "hello.plugin")
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.Write(raw) }))
	defer server.Close()
	fetcher := NewCatalogFetcher(time.Hour, t.TempDir())
	sources := []CatalogSource{
		{ID: "z", Name: "lower", URL: server.URL, Priority: 10, Enabled: true},
		{ID: "a", Name: "higher", URL: server.URL, Priority: 20, Enabled: true},
	}
	entries, err := fetcher.Fetch(context.Background(), sources)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].SourceID != "a" || entries[0].ID != "hello.plugin" || entries[0].Name != "Display hello.plugin" || !entries[0].Available {
		t.Fatalf("entries: %+v", entries)
	}
	if _, cachedErr := fetcher.Fetch(context.Background(), sources); cachedErr != nil {
		t.Fatal(cachedErr)
	}
	if calls.Load() != 2 {
		t.Fatalf("cache missed: %d", calls.Load())
	}
	// Changing priority must invalidate the view even inside its TTL.
	sources[0].Priority = 30
	entries, err = fetcher.Fetch(context.Background(), sources)
	if err != nil || len(entries) != 1 || entries[0].SourceID != "z" {
		t.Fatalf("changed sources: %+v, %v", entries, err)
	}
	// Disabling every source must never return stale enabled entries.
	sources[0].Enabled, sources[1].Enabled = false, false
	entries, err = fetcher.Fetch(context.Background(), sources)
	if err != nil || len(entries) != 0 {
		t.Fatalf("disabled sources: %+v, %v", entries, err)
	}
}

func TestCatalogFetcher_TiesAreDeterministic(t *testing.T) {
	fetcher := NewCatalogFetcher(0, "")
	entry := CatalogEntry{ID: "example", Name: "Display"}
	a := fetchResult{source: CatalogSource{ID: "a", Priority: 1}, catalog: &CatalogFile{Plugins: []CatalogEntry{entry}}}
	z := fetchResult{source: CatalogSource{ID: "z", Priority: 1}, catalog: a.catalog}
	for _, results := range [][]fetchResult{{a, z}, {z, a}} {
		got := fetcher.merge(results)
		if len(got) != 1 || got[0].SourceID != "a" {
			t.Fatalf("unstable winner: %+v", got)
		}
	}
}

func TestCatalogFetcher_DiskCacheFallback(t *testing.T) {
	raw := catalogFixture(t, "cached.plugin")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(raw) }))
	fetcher := NewCatalogFetcher(0, t.TempDir())
	sources := []CatalogSource{{ID: "cache", URL: server.URL, Enabled: true}}
	if _, cachedErr := fetcher.Fetch(context.Background(), sources); cachedErr != nil {
		t.Fatal(cachedErr)
	}
	server.Close()
	entries, err := fetcher.Fetch(context.Background(), sources)
	if err != nil || len(entries) != 1 {
		t.Fatalf("fallback: %+v %v", entries, err)
	}
	// A new URL with the same source ID must not reuse the previous URL's cache.
	sources[0].URL = server.URL + "/other"
	if _, err := fetcher.Fetch(context.Background(), sources); err == nil {
		t.Fatal("reused cache for changed URL")
	}
}

func TestCatalogFetcher_RejectsLegacyAndCancels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("version: 1\nplugins: []\n")) }))
	defer server.Close()
	fetcher := NewCatalogFetcher(time.Hour, t.TempDir())
	sources := []CatalogSource{{ID: "legacy", URL: server.URL, Enabled: true}}
	if _, err := fetcher.Fetch(context.Background(), sources); err == nil {
		t.Fatal("accepted legacy catalog")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fetcher.Fetch(ctx, sources); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestDecodeCatalog_PlatformAndHostSelection(t *testing.T) {
	raw := catalogFixture(t, "example")
	var doc sharedcatalog.Document
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc.Plugins[0].Archives[0].Platform = "windows-arm64"
	if runtime.GOOS == "windows" && runtime.GOARCH == "arm64" {
		doc.Plugins[0].Archives[0].Platform = "linux-amd64"
	}
	raw, _ = json.Marshal(doc)
	decoded, err := DecodeCatalog(raw)
	if err != nil || len(decoded.Plugins) != 1 || decoded.Plugins[0].Available || decoded.Plugins[0].ArchiveURL != "" {
		t.Fatalf("unsupported platform: %+v %v", decoded, err)
	}
	if decodeErr := json.Unmarshal([]byte(`{"cerberus":{"min":"0.1.0"}}`), &doc.Plugins[0].Hosts); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	delete(doc.Plugins[0].Hosts, "nanite")
	raw, _ = json.Marshal(doc)
	decoded, err = DecodeCatalog(raw)
	if err != nil || len(decoded.Plugins) != 0 {
		t.Fatalf("other host: %+v %v", decoded, err)
	}
}
