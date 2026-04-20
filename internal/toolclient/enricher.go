package toolclient

import (
	"context"
	"errors"
	"fmt"

	"github.com/hollis-labs/go-toolbroker/broker"
	"github.com/hollis-labs/nanite/internal/store"
)

// storeEnricher is a SQLite-backed broker.Enricher that reads per-tool Hints
// from the tool_enrichments table. A nil store is permitted and makes the
// enricher a silent no-op (ok=false for every lookup) — this lets the
// toolclient be constructed in contexts that don't have a store wired yet
// (tests, CLI tools) without forcing every caller to branch on it.
type storeEnricher struct {
	s *store.Store
}

// NewStoreEnricher returns a broker.Enricher backed by the given store. If s
// is nil, the returned enricher answers ok=false for every lookup.
func NewStoreEnricher(s *store.Store) broker.Enricher {
	return &storeEnricher{s: s}
}

// LookupByToolName implements broker.Enricher. Missing records map to
// (Hints{}, false, nil) — the broker treats that as "no hints to surface"
// rather than an error. Real store / decode failures are wrapped so callers
// can errors.Is / errors.As through the lookup boundary.
func (e *storeEnricher) LookupByToolName(ctx context.Context, toolName string) (broker.Hints, bool, error) {
	if e.s == nil {
		return broker.Hints{}, false, nil
	}
	rec, err := e.s.GetToolEnrichment(toolName)
	if errors.Is(err, store.ErrToolEnrichmentNotFound) {
		return broker.Hints{}, false, nil
	}
	if err != nil {
		return broker.Hints{}, false, fmt.Errorf("lookup enrichment: %w", err)
	}
	h, err := broker.UnmarshalHints(rec.HintsJSON)
	if err != nil {
		return broker.Hints{}, false, fmt.Errorf("decode enrichment %q: %w", toolName, err)
	}
	return h, true, nil
}
