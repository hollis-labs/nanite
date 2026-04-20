package enrichment

import (
	"context"
	"errors"
	"fmt"

	"github.com/hollis-labs/nanite/internal/store"
)

// Enricher resolves per-tool Hints by tool name. Returns ok=false (with err=nil)
// when the tool has no enrichment record — this is expected for most tools and
// callers should treat it as "no hints to surface" rather than an error.
type Enricher interface {
	LookupByToolName(ctx context.Context, toolName string) (Hints, bool, error)
}

// storeEnricher is the SQLite-backed Enricher. A nil store is permitted and
// makes the enricher a silent no-op (ok=false for every lookup) — lets the
// broker be wired without requiring a store in every construction site.
type storeEnricher struct {
	s *store.Store
}

// NewStoreEnricher returns an Enricher backed by the given store. If s is nil,
// the returned enricher answers ok=false for every lookup.
func NewStoreEnricher(s *store.Store) Enricher {
	return &storeEnricher{s: s}
}

func (e *storeEnricher) LookupByToolName(ctx context.Context, toolName string) (Hints, bool, error) {
	if e.s == nil {
		return Hints{}, false, nil
	}
	rec, err := e.s.GetToolEnrichment(toolName)
	if errors.Is(err, store.ErrToolEnrichmentNotFound) {
		return Hints{}, false, nil
	}
	if err != nil {
		return Hints{}, false, fmt.Errorf("lookup enrichment: %w", err)
	}
	h, err := UnmarshalHints(rec.HintsJSON)
	if err != nil {
		return Hints{}, false, fmt.Errorf("decode enrichment %q: %w", toolName, err)
	}
	return h, true, nil
}
