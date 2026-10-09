package catalog

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/hollis-labs/substrate/harness/sandbox/atomicfile"
)

const DefaultCatalogFetchTimeout = 15 * time.Second
const MaxCatalogBytes = int64(4 * 1024 * 1024)

// Catalog contains fetched catalog bytes; schema validation belongs to the consumer.
type Catalog struct{ YAML []byte }

// Fetcher downloads a size-bounded catalog and caches it for offline browsing.
// Catalogs carry declarations and checksums, never publisher trust assertions.
type Fetcher struct {
	Client   *http.Client
	CacheDir string
	Timeout  time.Duration
}

func (f *Fetcher) Fetch(ctx context.Context, catalogURL string) (*Catalog, error) {
	timeout := f.Timeout
	if timeout <= 0 {
		timeout = DefaultCatalogFetchTimeout
	}
	fetchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	body, err := fetchBytes(fetchCtx, client, catalogURL, MaxCatalogBytes)
	if err == nil {
		if path := f.cachePath(catalogURL); path != "" {
			if mkdirErr := os.MkdirAll(f.CacheDir, 0700); mkdirErr == nil {
				_ = atomicfile.WriteFile(path, body, 0600)
			}
		}
		return &Catalog{YAML: body}, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	path := f.cachePath(catalogURL)
	if path == "" {
		return nil, fmt.Errorf("catalog fetch: %w", err)
	}
	cached, cacheErr := os.Open(path) // #nosec G304 -- filename is a URL hash confined to the configured cache directory.
	if cacheErr != nil {
		return nil, fmt.Errorf("catalog fetch failed (%w), no cache: %w", err, cacheErr)
	}
	defer func() { _ = cached.Close() }()
	body, cacheErr = io.ReadAll(io.LimitReader(cached, MaxCatalogBytes+1))
	if cacheErr != nil || int64(len(body)) > MaxCatalogBytes {
		return nil, errors.New("catalog cache unreadable or exceeds size cap")
	}
	return &Catalog{YAML: body}, nil
}

func (f *Fetcher) cachePath(url string) string {
	if f.CacheDir == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(url))
	return filepath.Join(f.CacheDir, fmt.Sprintf("catalog-%x.json", digest))
}

func fetchBytes(ctx context.Context, client *http.Client, url string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close() // Response-body close is best-effort cleanup after the request result is read.
	}()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("http %s", resp.Status)
	}
	if resp.ContentLength > max {
		return nil, fmt.Errorf("content-length %d > cap %d", resp.ContentLength, max)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > max {
		return nil, fmt.Errorf("body exceeds cap %d", max)
	}
	return body, nil
}
