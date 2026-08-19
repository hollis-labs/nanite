package context

import "context"

// CompactionEvent is the context-package mirror of store.CompactionEvent, used to
// decouple the compaction pipeline from the store. The service layer adapts between
// them via CompactionEventWriter.
type CompactionEvent struct {
	ID                   string
	SessionID            string
	CoverageWindowStart  *string
	CoverageWindowEnd    *string
	EvictedCachePointers []string
	PreservedSources     []string
	SummaryMode          string
	SummaryTokenCount    int
	OriginalTokenCount   int
	HandoffStashID       *string
	StagesApplied        []string
	CreatedAt            string
}

// CompactionEventWriter persists structured compaction metadata. Implemented by
// the service layer so the context package remains free of store imports.
type CompactionEventWriter interface {
	WriteCompactionEvent(ctx context.Context, event CompactionEvent) error
}

// CompactionEventReader fetches the most recent compaction event for a session.
// The chat assembly path uses this to detect a "fresh" compaction (one where
// no assistant turn has run since the event was emitted) and inject the
// CompactionContract disclosure prompt for that turn (CW-20260420-0025, P8A).
//
// Implemented by the service layer so the context package remains free of
// store imports. (nil, nil) is the canonical "no event yet" return — callers
// should treat that as "no disclosure to inject".
type CompactionEventReader interface {
	GetLatestCompactionEvent(ctx context.Context, sessionID string) (*CompactionEvent, error)
}
