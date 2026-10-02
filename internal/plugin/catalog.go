package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	catalogfetch "github.com/hollis-labs/nanite/internal/plugin/catalog"
	"github.com/hollis-labs/nanite/internal/safego"
	sharedcatalog "github.com/hollis-labs/plugins-catalog"
)

// CatalogEntry represents a single plugin in a remote catalog.
type CatalogEntry struct {
	ID             string                `json:"id"`
	Name           string                `json:"name"`
	Version        string                `json:"version"`
	Description    string                `json:"description"`
	Repo           string                `json:"repo,omitempty"`
	ArchiveURL     string                `json:"archive_url,omitempty"`
	Checksum       string                `json:"checksum,omitempty"`
	ArchiveSize    int64                 `json:"archive_size,omitempty"`
	ManifestSHA256 string                `json:"manifest_sha256"`
	Runtime        string                `json:"runtime"`
	Tags           []string              `json:"tags,omitempty"`
	Available      bool                  `json:"available"`
	Summary        sharedcatalog.Summary `json:"summary"`
}

// CatalogFile is the top-level structure of a catalog.yaml served by a source.
type CatalogFile struct {
	SchemaVersion  int            `json:"schema_version"`
	CatalogVersion string         `json:"catalog_version"`
	Plugins        []CatalogEntry `json:"plugins"`
}

// CatalogSource is a minimal view of a catalog source (URL and priority).
// Matches the DB model fields needed by the fetcher.
type CatalogSource struct {
	ID       string
	Name     string
	URL      string
	Priority int
	Enabled  bool
}

// MergedCatalogEntry is a CatalogEntry enriched with source metadata.
type MergedCatalogEntry struct {
	CatalogEntry
	SourceID   string `json:"source_id"`
	SourceName string `json:"source_name"`
}

// fetchResult holds the result of fetching a single catalog source.
type fetchResult struct {
	source  CatalogSource
	catalog *CatalogFile
	err     error
}

// CatalogFetcher fetches and merges plugin catalogs from multiple sources.
type CatalogFetcher struct {
	mu           sync.RWMutex
	cache        []MergedCatalogEntry
	cacheAt      time.Time
	cacheSources string
	cacheTTL     time.Duration
	cacheDir     string       // local disk cache for catalog files
	client       *http.Client // injectable for testing
}

// NewCatalogFetcher creates a new fetcher with the given cache TTL.
func NewCatalogFetcher(cacheTTL time.Duration, cacheDir string) *CatalogFetcher {
	return NewCatalogFetcherWithClient(cacheTTL, cacheDir, &http.Client{Timeout: 15 * time.Second})
}

// NewCatalogFetcherWithClient accepts host-controlled HTTPS roots and timeouts.
func NewCatalogFetcherWithClient(cacheTTL time.Duration, cacheDir string, client *http.Client) *CatalogFetcher {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &CatalogFetcher{cacheTTL: cacheTTL, cacheDir: cacheDir, client: client}
}

// Fetch retrieves and merges catalogs from all enabled sources.
// Returns cached results if within TTL. Sources are fetched in parallel;
// failures are logged but don't block other sources.
func (cf *CatalogFetcher) Fetch(ctx context.Context, sources []CatalogSource) ([]MergedCatalogEntry, error) {
	sourceBytes, err := json.Marshal(sources)
	if err != nil {
		return nil, err
	}
	fingerprint := string(sourceBytes)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cf.mu.RLock()
	if cf.cacheSources == fingerprint && cf.cache != nil && time.Since(cf.cacheAt) < cf.cacheTTL {
		result := cf.cache
		cf.mu.RUnlock()
		return result, nil
	}
	cf.mu.RUnlock()

	// Fetch all sources in parallel.
	results := make(chan fetchResult, len(sources))

	for _, src := range sources {
		if !src.Enabled {
			continue
		}
		s := src
		safego.Go(ctx, "plugin.catalog.fetchSource", func() {
			cat, err := cf.fetchSource(ctx, s)
			results <- fetchResult{source: s, catalog: cat, err: err}
		})
	}

	// Collect results.
	var catalogs []fetchResult
	enabledCount := 0
	for _, s := range sources {
		if s.Enabled {
			enabledCount++
		}
	}
	for i := 0; i < enabledCount; i++ {
		select {
		case result := <-results:
			catalogs = append(catalogs, result)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	var failures []error
	successes := 0
	for _, result := range catalogs {
		if result.err != nil {
			failures = append(failures, fmt.Errorf("catalog %q: %w", result.source.ID, result.err))
		} else {
			successes++
		}
	}
	if enabledCount > 0 && successes == 0 {
		return nil, errors.Join(failures...)
	}
	// Merge: higher priority sources win on ID conflicts.
	merged := cf.merge(catalogs)

	cf.mu.Lock()
	cf.cache = merged
	cf.cacheSources = fingerprint
	cf.cacheAt = time.Now()
	cf.mu.Unlock()

	return merged, nil
}

// Invalidate clears the cache so the next Fetch hits the network.
func (cf *CatalogFetcher) Invalidate() {
	cf.mu.Lock()
	cf.cache = nil
	cf.mu.Unlock()
}

// fetchSource downloads and parses a single catalog source.
// Falls back to a local disk cache if the network fetch fails.
func (cf *CatalogFetcher) fetchSource(ctx context.Context, src CatalogSource) (*CatalogFile, error) {
	fetcher := &catalogfetch.Fetcher{Client: cf.client, CacheDir: cf.cacheDir}
	fetched, err := fetcher.Fetch(ctx, src.URL)
	if err != nil {
		return nil, err
	}
	return DecodeCatalog(fetched.YAML)
}

// merge combines catalogs from multiple sources. Higher priority wins on name conflict.
func (cf *CatalogFetcher) merge(results []fetchResult) []MergedCatalogEntry {
	// Sort ties by source ID so network completion order cannot choose a winner.
	slices.SortFunc(results, func(a, b fetchResult) int { return strings.Compare(a.source.ID, b.source.ID) })
	// Map: canonical plugin ID → best entry (highest source priority).
	best := make(map[string]MergedCatalogEntry)
	bestPriority := make(map[string]int)

	for _, r := range results {
		if r.err != nil || r.catalog == nil {
			continue
		}
		for _, entry := range r.catalog.Plugins {
			existing, exists := bestPriority[entry.ID]
			if !exists || r.source.Priority > existing {
				best[entry.ID] = MergedCatalogEntry{
					CatalogEntry: entry,
					SourceID:     r.source.ID,
					SourceName:   r.source.Name,
				}
				bestPriority[entry.ID] = r.source.Priority
			}
		}
	}

	// Flatten to slice.
	merged := make([]MergedCatalogEntry, 0, len(best))
	for _, entry := range best {
		merged = append(merged, entry)
	}
	slices.SortFunc(merged, func(a, b MergedCatalogEntry) int { return strings.Compare(a.ID, b.ID) })
	return merged
}
