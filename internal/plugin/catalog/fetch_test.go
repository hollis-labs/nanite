package catalog

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestFetcherPlainCatalogAndOfflineCache(t *testing.T) {
	body := []byte(`{"schema_version":2,"plugins":[]}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/catalog.yaml" {
			t.Errorf("unexpected signature fetch: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	f := Fetcher{CacheDir: t.TempDir()}
	url := srv.URL + "/catalog.yaml"
	got, err := f.Fetch(context.Background(), url)
	if err != nil || string(got.YAML) != string(body) {
		t.Fatalf("fetch: %+v %v", got, err)
	}
	srv.Close()
	cached, cacheErr := f.Fetch(context.Background(), url)
	if cacheErr != nil || string(cached.YAML) != string(body) {
		t.Fatalf("offline cache: %+v %v", cached, cacheErr)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, cancelErr := f.Fetch(ctx, url); !errors.Is(cancelErr, context.Canceled) {
		t.Fatalf("cancellation returned cached success: %v", cancelErr)
	}
	if _, isolationErr := f.Fetch(context.Background(), url+"/different"); isolationErr == nil {
		t.Fatal("cache reused across catalog URLs")
	}
}

func TestFetcherRejectsOversizedNetworkAndCacheBodies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", int(MaxCatalogBytes)+1)))
	}))
	defer srv.Close()
	f := Fetcher{CacheDir: t.TempDir()}
	if _, err := f.Fetch(context.Background(), srv.URL); err == nil {
		t.Fatal("accepted oversized network body")
	}
	path := f.cachePath(srv.URL)
	if err := os.WriteFile(path, []byte(strings.Repeat("x", int(MaxCatalogBytes)+1)), 0600); err != nil {
		t.Fatal(err)
	} // #nosec G703 -- cache path is a fixed URL hash within t.TempDir.
	srv.Close()
	if _, err := f.Fetch(context.Background(), srv.URL); err == nil {
		t.Fatal("accepted oversized cache")
	}
}
