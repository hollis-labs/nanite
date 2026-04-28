package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrCompactionEventNotFound is returned when a compaction event cannot be located.
var ErrCompactionEventNotFound = errors.New("compaction event not found")

// CompactionEvent is a row in the compaction_events table. It records structured
// metadata emitted by the compaction pipeline after successful stage execution.
type CompactionEvent struct {
	ID                    string   `json:"id"`
	SessionID             string   `json:"session_id"`
	CoverageWindowStart   *string  `json:"coverage_window_start"`
	CoverageWindowEnd     *string  `json:"coverage_window_end"`
	EvictedCachePointers  []string `json:"evicted_cache_pointers"`
	PreservedSources      []string `json:"preserved_sources"`
	SummaryMode           string   `json:"summary_mode"`
	SummaryTokenCount     int      `json:"summary_token_count"`
	OriginalTokenCount    int      `json:"original_token_count"`
	HandoffStashID        *string  `json:"handoff_stash_id"`
	StagesApplied         []string `json:"stages_applied"`
	CreatedAt             string   `json:"created_at"`
}

// WriteCompactionEvent inserts a new compaction event record.
func (s *Store) WriteCompactionEvent(ctx context.Context, event CompactionEvent) error {
	if event.ID == "" {
		return fmt.Errorf("write compaction event: id is required")
	}
	if event.SessionID == "" {
		return fmt.Errorf("write compaction event: session_id is required")
	}
	if event.SummaryMode == "" {
		return fmt.Errorf("write compaction event: summary_mode is required")
	}

	// Marshal JSON arrays
	evictedJSON, err := json.Marshal(event.EvictedCachePointers)
	if err != nil {
		return fmt.Errorf("write compaction event: marshal evicted_cache_pointers: %w", err)
	}
	preservedJSON, err := json.Marshal(event.PreservedSources)
	if err != nil {
		return fmt.Errorf("write compaction event: marshal preserved_sources: %w", err)
	}
	stagesJSON, err := json.Marshal(event.StagesApplied)
	if err != nil {
		return fmt.Errorf("write compaction event: marshal stages_applied: %w", err)
	}

	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO compaction_events
			(id, session_id, coverage_window_start, coverage_window_end, evicted_cache_pointers,
			 preserved_sources, summary_mode, summary_token_count, original_token_count,
			 handoff_stash_id, stages_applied, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID, event.SessionID, event.CoverageWindowStart, event.CoverageWindowEnd,
		string(evictedJSON), string(preservedJSON), event.SummaryMode,
		event.SummaryTokenCount, event.OriginalTokenCount, event.HandoffStashID,
		string(stagesJSON), event.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("write compaction event: %w", err)
	}
	return nil
}

// ListCompactionEventsBySession returns the most recent compaction events for a session,
// ordered by created_at DESC. Returns up to limit events.
func (s *Store) ListCompactionEventsBySession(ctx context.Context, sessionID string, limit int) ([]CompactionEvent, error) {
	if limit <= 0 {
		limit = 50 // sensible default
	}

	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, session_id, coverage_window_start, coverage_window_end, evicted_cache_pointers,
		 preserved_sources, summary_mode, summary_token_count, original_token_count,
		 handoff_stash_id, stages_applied, created_at
		 FROM compaction_events WHERE session_id = ?
		 ORDER BY created_at DESC LIMIT ?`,
		sessionID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list compaction events: %w", err)
	}
	defer rows.Close()

	var events []CompactionEvent
	for rows.Next() {
		var e CompactionEvent
		var evictedRaw, preservedRaw, stagesRaw string

		err := rows.Scan(
			&e.ID, &e.SessionID, &e.CoverageWindowStart, &e.CoverageWindowEnd,
			&evictedRaw, &preservedRaw, &e.SummaryMode, &e.SummaryTokenCount,
			&e.OriginalTokenCount, &e.HandoffStashID, &stagesRaw, &e.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("list compaction events: scan: %w", err)
		}

		// Unmarshal JSON arrays
		if err := json.Unmarshal([]byte(evictedRaw), &e.EvictedCachePointers); err != nil {
			return nil, fmt.Errorf("list compaction events: unmarshal evicted_cache_pointers: %w", err)
		}
		if err := json.Unmarshal([]byte(preservedRaw), &e.PreservedSources); err != nil {
			return nil, fmt.Errorf("list compaction events: unmarshal preserved_sources: %w", err)
		}
		if err := json.Unmarshal([]byte(stagesRaw), &e.StagesApplied); err != nil {
			return nil, fmt.Errorf("list compaction events: unmarshal stages_applied: %w", err)
		}

		// Ensure nil slices are empty (not nil)
		if e.EvictedCachePointers == nil {
			e.EvictedCachePointers = []string{}
		}
		if e.PreservedSources == nil {
			e.PreservedSources = []string{}
		}
		if e.StagesApplied == nil {
			e.StagesApplied = []string{}
		}

		events = append(events, e)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("list compaction events: rows error: %w", err)
	}

	return events, nil
}

// GetLatestCompactionEvent returns the most recent compaction event for a session,
// or (nil, nil) if none exist.
func (s *Store) GetLatestCompactionEvent(ctx context.Context, sessionID string) (*CompactionEvent, error) {
	var e CompactionEvent
	var evictedRaw, preservedRaw, stagesRaw string

	err := s.DB.QueryRowContext(ctx,
		`SELECT id, session_id, coverage_window_start, coverage_window_end, evicted_cache_pointers,
		 preserved_sources, summary_mode, summary_token_count, original_token_count,
		 handoff_stash_id, stages_applied, created_at
		 FROM compaction_events WHERE session_id = ?
		 ORDER BY created_at DESC LIMIT 1`,
		sessionID,
	).Scan(
		&e.ID, &e.SessionID, &e.CoverageWindowStart, &e.CoverageWindowEnd,
		&evictedRaw, &preservedRaw, &e.SummaryMode, &e.SummaryTokenCount,
		&e.OriginalTokenCount, &e.HandoffStashID, &stagesRaw, &e.CreatedAt,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get latest compaction event: %w", err)
	}

	// Unmarshal JSON arrays
	if err := json.Unmarshal([]byte(evictedRaw), &e.EvictedCachePointers); err != nil {
		return nil, fmt.Errorf("get latest compaction event: unmarshal evicted_cache_pointers: %w", err)
	}
	if err := json.Unmarshal([]byte(preservedRaw), &e.PreservedSources); err != nil {
		return nil, fmt.Errorf("get latest compaction event: unmarshal preserved_sources: %w", err)
	}
	if err := json.Unmarshal([]byte(stagesRaw), &e.StagesApplied); err != nil {
		return nil, fmt.Errorf("get latest compaction event: unmarshal stages_applied: %w", err)
	}

	// Ensure nil slices are empty (not nil)
	if e.EvictedCachePointers == nil {
		e.EvictedCachePointers = []string{}
	}
	if e.PreservedSources == nil {
		e.PreservedSources = []string{}
	}
	if e.StagesApplied == nil {
		e.StagesApplied = []string{}
	}

	return &e, nil
}
