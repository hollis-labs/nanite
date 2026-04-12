package plugin

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/hollis-labs/nanite/internal/safego"
)

// CatalogEntry represents a single plugin in a remote catalog.
type CatalogEntry struct {
	Name        string `yaml:"name"        json:"name"`
	Version     string `yaml:"version"     json:"version"`
	Description string `yaml:"description" json:"description"`
	Author      string `yaml:"author"      json:"author,omitempty"`
	Repo        string `yaml:"repo"        json:"repo,omitempty"`        // e.g. "hollis-labs/nanite-plugin-git"
	ArchiveURL  string `yaml:"archive_url" json:"archive_url"`           // download URL for .tar.gz
	Checksum    string `yaml:"checksum"    json:"checksum,omitempty"`    // "sha256:hex..."
	Signature   string `yaml:"signature"   json:"signature,omitempty"`  // hex-encoded Ed25519 signature over the archive
	Compat      string `yaml:"compat"      json:"compat,omitempty"`     // semver range, e.g. ">=0.2.0"
	Runtime     string `yaml:"runtime"     json:"runtime,omitempty"`    // "builtin" or "subprocess"
	Tags        []string `yaml:"tags"      json:"tags,omitempty"`
}

// CatalogFile is the top-level structure of a catalog.yaml served by a source.
type CatalogFile struct {
	Version int            `yaml:"version"` // catalog format version (1)
	Plugins []CatalogEntry `yaml:"plugins"`
}

// CatalogSource is a minimal view of a catalog source (URL + priority + trust key).
// Matches the DB model fields needed by the fetcher.
type CatalogSource struct {
	ID        string
	Name      string
	URL       string
	Priority  int
	Enabled   bool
	PublicKey string // hex-encoded Ed25519 public key for signature verification
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
	mu       sync.RWMutex
	cache    []MergedCatalogEntry
	cacheAt  time.Time
	cacheTTL time.Duration
	cacheDir string       // local disk cache for catalog files
	client   *http.Client // injectable for testing
}

// NewCatalogFetcher creates a new fetcher with the given cache TTL.
func NewCatalogFetcher(cacheTTL time.Duration, cacheDir string) *CatalogFetcher {
	return &CatalogFetcher{
		cacheTTL: cacheTTL,
		cacheDir: cacheDir,
		client:   &http.Client{Timeout: 15 * time.Second},
	}
}

// Fetch retrieves and merges catalogs from all enabled sources.
// Returns cached results if within TTL. Sources are fetched in parallel;
// failures are logged but don't block other sources.
func (cf *CatalogFetcher) Fetch(ctx context.Context, sources []CatalogSource) ([]MergedCatalogEntry, error) {
	cf.mu.RLock()
	if cf.cache != nil && time.Since(cf.cacheAt) < cf.cacheTTL {
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
		catalogs = append(catalogs, <-results)
	}

	// Merge: higher priority sources win on name conflicts.
	merged := cf.merge(catalogs)

	cf.mu.Lock()
	cf.cache = merged
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
	req, err := http.NewRequestWithContext(ctx, "GET", src.URL, nil)
	if err != nil {
		return cf.loadDiskCache(src.ID)
	}

	resp, err := cf.client.Do(req)
	if err != nil {
		log.Printf("catalog: fetch %s failed (using cache): %v", src.Name, err)
		return cf.loadDiskCache(src.ID)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("catalog: fetch %s returned %d (using cache)", src.Name, resp.StatusCode)
		return cf.loadDiskCache(src.ID)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return cf.loadDiskCache(src.ID)
	}

	var catalog CatalogFile
	if err := yaml.Unmarshal(body, &catalog); err != nil {
		return nil, fmt.Errorf("parse catalog from %s: %w", src.Name, err)
	}

	// Save to disk cache for offline fallback.
	cf.saveDiskCache(src.ID, body)

	return &catalog, nil
}

// merge combines catalogs from multiple sources. Higher priority wins on name conflict.
func (cf *CatalogFetcher) merge(results []fetchResult) []MergedCatalogEntry {
	// Map: plugin name → best entry (highest source priority).
	best := make(map[string]MergedCatalogEntry)
	bestPriority := make(map[string]int)

	for _, r := range results {
		if r.err != nil || r.catalog == nil {
			continue
		}
		for _, entry := range r.catalog.Plugins {
			existing, exists := bestPriority[entry.Name]
			if !exists || r.source.Priority > existing {
				best[entry.Name] = MergedCatalogEntry{
					CatalogEntry: entry,
					SourceID:     r.source.ID,
					SourceName:   r.source.Name,
				}
				bestPriority[entry.Name] = r.source.Priority
			}
		}
	}

	// Flatten to slice.
	merged := make([]MergedCatalogEntry, 0, len(best))
	for _, entry := range best {
		merged = append(merged, entry)
	}
	return merged
}

// --- Disk cache helpers ---

func (cf *CatalogFetcher) diskCachePath(sourceID string) string {
	if cf.cacheDir == "" {
		return ""
	}
	// Hash the source ID for a safe filename.
	h := sha256.Sum256([]byte(sourceID))
	return filepath.Join(cf.cacheDir, fmt.Sprintf("catalog-%x.yaml", h[:8]))
}

func (cf *CatalogFetcher) saveDiskCache(sourceID string, data []byte) {
	path := cf.diskCachePath(sourceID)
	if path == "" {
		return
	}
	os.MkdirAll(cf.cacheDir, 0755)
	_ = os.WriteFile(path, data, 0644)
}

func (cf *CatalogFetcher) loadDiskCache(sourceID string) (*CatalogFile, error) {
	path := cf.diskCachePath(sourceID)
	if path == "" {
		return nil, fmt.Errorf("no disk cache configured")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("no cached catalog for source %s", sourceID)
	}
	var catalog CatalogFile
	if err := yaml.Unmarshal(data, &catalog); err != nil {
		return nil, fmt.Errorf("parse cached catalog: %w", err)
	}
	return &catalog, nil
}

// VerifyChecksum checks a downloaded file against the catalog entry's checksum.
// Returns nil if no checksum is specified (user-uploaded trust model).
func VerifyChecksum(filePath, checksum string) error {
	if checksum == "" {
		return nil
	}

	// Parse "sha256:hex..." format.
	if len(checksum) < 8 || checksum[:7] != "sha256:" {
		return fmt.Errorf("unsupported checksum format: %s (expected sha256:hex)", checksum)
	}
	expected := checksum[7:]

	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open file for checksum: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hash file: %w", err)
	}

	actual := fmt.Sprintf("%x", h.Sum(nil))
	if actual != expected {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expected, actual)
	}
	return nil
}
