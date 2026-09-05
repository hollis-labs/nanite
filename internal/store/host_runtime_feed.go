package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// HostRuntimeFeedSchemaVersion is the public Nanite projection contract. It
// is intentionally not the source runtimeevents.SchemaVersion: changing the
// public allow-list or reducer contract is a separate compatibility decision.
const HostRuntimeFeedSchemaVersion = "host_runtime.v1"

// HostRuntimeFeedMaxEventBytes is enforced again at the persistence boundary,
// after the committed cursor has been assigned.
const HostRuntimeFeedMaxEventBytes = 8192

// HostRuntimeFeedIdentityRetention keeps compact dedupe hashes well beyond
// the 512-row public replay window without creating an unbounded ledger.
const HostRuntimeFeedIdentityRetention = 4096

// HostRuntimeEvent is the persisted and streamed public runtime projection.
// Cursor is allocated by Nanite per session and is never derived from the
// wrapper's SourceSequence, which can restart after recovery or relaunch.
type HostRuntimeEvent struct {
	SchemaVersion     string                 `json:"schema_version"`
	Cursor            int64                  `json:"cursor"`
	SessionID         string                 `json:"session_id"`
	RuntimeRunID      string                 `json:"runtime_run_id"`
	RuntimeGeneration int64                  `json:"runtime_generation"`
	SourceEventID     string                 `json:"source_event_id"`
	SourceSequence    uint64                 `json:"source_sequence"`
	Kind              string                 `json:"kind"`
	OccurredAt        string                 `json:"occurred_at"`
	TurnID            string                 `json:"turn_id,omitempty"`
	ParentID          string                 `json:"parent_id,omitempty"`
	Source            HostRuntimeEventSource `json:"source"`
	Process           HostRuntimeProcess     `json:"process,omitempty"`
	Payload           json.RawMessage        `json:"payload"`
	PayloadTruncated  bool                   `json:"payload_truncated,omitempty"`
	PayloadVisibility string                 `json:"payload_visibility"`
}

type HostRuntimeEventSource struct {
	Channel    string `json:"channel"`
	Confidence string `json:"confidence,omitempty"`
}

type HostRuntimeProcess struct {
	Provider          string `json:"provider,omitempty"`
	Runtime           string `json:"runtime,omitempty"`
	ProviderSessionID string `json:"provider_session_id,omitempty"`
}

// HostRuntimeGap is a host-generated control record. It is not a source
// runtime event and therefore has no source sequence or source event ID.
type HostRuntimeGap struct {
	SchemaVersion          string `json:"schema_version"`
	SessionID              string `json:"session_id"`
	Reason                 string `json:"reason"`
	RequestedCursor        int64  `json:"requested_cursor"`
	OldestAvailable        int64  `json:"oldest_available"`
	LatestCursor           int64  `json:"latest_cursor"`
	MissingCursorSpan      int64  `json:"missing_cursor_span"`
	RetentionDropped       int64  `json:"retention_dropped"`
	RuntimeGenerationFloor int64  `json:"runtime_generation_floor"`
	CurrentRuntimeRunID    string `json:"current_runtime_run_id,omitempty"`
}

// HostRuntimeHead is the authoritative runtime owner observed in the same
// transaction as a replay page. It is an SSE control frame, not a committed
// event, so LatestCursor is informational and never advances a client cursor.
type HostRuntimeHead struct {
	SchemaVersion          string `json:"schema_version"`
	SessionID              string `json:"session_id"`
	LatestCursor           int64  `json:"latest_cursor"`
	PrunedThroughCursor    int64  `json:"pruned_through_cursor"`
	RetentionDropped       int64  `json:"retention_dropped"`
	RuntimeGenerationFloor int64  `json:"runtime_generation_floor"`
	CurrentRuntimeRunID    string `json:"current_runtime_run_id,omitempty"`
}

type HostRuntimeReplay struct {
	Events                 []HostRuntimeEvent
	NextCursor             int64
	LatestCursor           int64
	PrunedThrough          int64
	RuntimeGenerationFloor int64
	CurrentRuntimeRunID    string
	Head                   HostRuntimeHead
	Gap                    *HostRuntimeGap
}

// ReserveHostRuntimeRun atomically allocates the durable generation and binds
// the host-owned run ID used by reconnect snapshots. It deliberately happens
// before any runtime event: observation order cannot distinguish a delayed
// predecessor event from a newly-created successor wrapper.
func (s *Store) ReserveHostRuntimeRun(ctx context.Context, sessionID, runID string) (int64, error) {
	if runID == "" {
		return 0, errors.New("host runtime run id is required")
	}
	if sessionID == "" {
		return 0, errors.New("host runtime session id is required")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var generation int64
	err := s.DB.QueryRowContext(ctx, `
		INSERT INTO host_runtime_feed_heads (
			session_id, last_cursor, last_runtime_generation,
			current_runtime_run_id, pruned_through_cursor, retention_dropped, updated_at
		) VALUES (?, 0, 1, ?, 0, 0, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			last_runtime_generation = host_runtime_feed_heads.last_runtime_generation + 1,
			current_runtime_run_id = excluded.current_runtime_run_id,
			updated_at = excluded.updated_at
		RETURNING last_runtime_generation`, sessionID, runID, now).Scan(&generation)
	if err != nil {
		return 0, fmt.Errorf("reserve host runtime generation: %w", err)
	}
	return generation, nil
}

// AppendHostRuntimeEvent assigns a committed per-session cursor, persists the
// already-public projection, and prunes old history in the same transaction.
// A repeated source event in the same runtime run is idempotent. Reusing its
// identity with different contents is rejected as an integrity failure.
func (s *Store) AppendHostRuntimeEvent(ctx context.Context, event HostRuntimeEvent, retain int) (HostRuntimeEvent, bool, error) {
	if event.SessionID == "" || event.RuntimeRunID == "" || event.RuntimeGeneration < 1 || event.SourceEventID == "" || event.Kind == "" {
		return HostRuntimeEvent{}, false, errors.New("host runtime event identity is incomplete")
	}
	if retain < 1 {
		return HostRuntimeEvent{}, false, errors.New("host runtime retention must be positive")
	}
	if retain > HostRuntimeFeedIdentityRetention {
		return HostRuntimeEvent{}, false, errors.New("host runtime event retention exceeds identity horizon")
	}
	if len(event.Payload) > HostRuntimeFeedMaxEventBytes {
		return HostRuntimeEvent{}, false, fmt.Errorf("host runtime event exceeds %d-byte public bound", HostRuntimeFeedMaxEventBytes)
	}
	event.SchemaVersion = HostRuntimeFeedSchemaVersion
	event.Cursor = 0
	identityHash, err := hostRuntimeIdentityHash(event)
	if err != nil {
		return HostRuntimeEvent{}, false, err
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return HostRuntimeEvent{}, false, fmt.Errorf("begin host runtime append: %w", err)
	}
	defer rollbackUnlessCommitted(tx)
	var generationFloor int64
	if err := tx.QueryRowContext(ctx, `
		SELECT last_runtime_generation
		FROM host_runtime_feed_heads WHERE session_id = ?`, event.SessionID).Scan(&generationFloor); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return HostRuntimeEvent{}, false, errors.New("host runtime generation was not reserved")
		}
		return HostRuntimeEvent{}, false, fmt.Errorf("load host runtime generation floor: %w", err)
	}
	if event.RuntimeGeneration > generationFloor {
		return HostRuntimeEvent{}, false, errors.New("host runtime event generation exceeds reserved floor")
	}

	var existingHash string
	var existingCursor int64
	err = tx.QueryRowContext(ctx, `
		SELECT event_hash, first_cursor
		FROM host_runtime_feed_identities
		WHERE session_id = ? AND runtime_run_id = ? AND source_event_id = ?`,
		event.SessionID, event.RuntimeRunID, event.SourceEventID,
	).Scan(&existingHash, &existingCursor)
	if err == nil {
		if existingHash != identityHash {
			return HostRuntimeEvent{}, false, errors.New("host runtime source event identity reused with different contents")
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return HostRuntimeEvent{}, false, fmt.Errorf("commit duplicate host runtime lookup: %w", commitErr)
		}
		event.Cursor = existingCursor
		return event, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return HostRuntimeEvent{}, false, fmt.Errorf("lookup host runtime event: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := tx.QueryRowContext(ctx, `
		UPDATE host_runtime_feed_heads
		SET last_cursor = last_cursor + 1, updated_at = ?
		WHERE session_id = ?
		RETURNING last_cursor`, now, event.SessionID).Scan(&event.Cursor); err != nil {
		return HostRuntimeEvent{}, false, fmt.Errorf("allocate host runtime cursor: %w", err)
	}
	eventJSON, err := json.Marshal(event)
	if err != nil {
		return HostRuntimeEvent{}, false, fmt.Errorf("encode host runtime event: %w", err)
	}
	if len(eventJSON) > HostRuntimeFeedMaxEventBytes {
		return HostRuntimeEvent{}, false, fmt.Errorf("host runtime event exceeds %d-byte public bound", HostRuntimeFeedMaxEventBytes)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO host_runtime_feed_events (
			session_id, cursor, runtime_run_id, runtime_generation, source_event_id,
			source_sequence, kind, event_json, occurred_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.SessionID, event.Cursor, event.RuntimeRunID, event.RuntimeGeneration, event.SourceEventID,
		event.SourceSequence, event.Kind, string(eventJSON), event.OccurredAt, now,
	); err != nil {
		return HostRuntimeEvent{}, false, fmt.Errorf("insert host runtime event: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO host_runtime_feed_identities (
			session_id, runtime_run_id, source_event_id, event_hash, first_cursor, created_at
		) VALUES (?, ?, ?, ?, ?, ?)`,
		event.SessionID, event.RuntimeRunID, event.SourceEventID, identityHash, event.Cursor, now,
	); err != nil {
		return HostRuntimeEvent{}, false, fmt.Errorf("insert host runtime event identity: %w", err)
	}

	pruneThrough := event.Cursor - int64(retain)
	if pruneThrough > 0 {
		result, deleteErr := tx.ExecContext(ctx, `
			DELETE FROM host_runtime_feed_events
			WHERE session_id = ? AND cursor <= ?`, event.SessionID, pruneThrough)
		if deleteErr != nil {
			return HostRuntimeEvent{}, false, fmt.Errorf("prune host runtime events: %w", deleteErr)
		}
		dropped, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return HostRuntimeEvent{}, false, fmt.Errorf("count pruned host runtime events: %w", rowsErr)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE host_runtime_feed_heads
			SET pruned_through_cursor = MAX(pruned_through_cursor, ?),
				retention_dropped = retention_dropped + ?,
				updated_at = ?
			WHERE session_id = ?`, pruneThrough, dropped, now, event.SessionID); err != nil {
			return HostRuntimeEvent{}, false, fmt.Errorf("update host runtime retention head: %w", err)
		}
	}
	identityPruneThrough := event.Cursor - HostRuntimeFeedIdentityRetention
	if identityPruneThrough > 0 {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM host_runtime_feed_identities
			WHERE session_id = ? AND first_cursor <= ?`, event.SessionID, identityPruneThrough); err != nil {
			return HostRuntimeEvent{}, false, fmt.Errorf("prune host runtime event identities: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return HostRuntimeEvent{}, false, fmt.Errorf("commit host runtime event: %w", err)
	}
	return event, true, nil
}

func hostRuntimeIdentityHash(event HostRuntimeEvent) (string, error) {
	event.Cursor = 0
	raw, err := json.Marshal(event)
	if err != nil {
		return "", fmt.Errorf("encode host runtime event identity: %w", err)
	}
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("sha256:%x", sum[:]), nil
}

// HostRuntimeEventsAfter returns a stable page after a client cursor and an
// explicit gap when that cursor cannot be continued from retained history.
func (s *Store) HostRuntimeEventsAfter(ctx context.Context, sessionID string, after int64, limit int) (HostRuntimeReplay, error) {
	if sessionID == "" {
		return HostRuntimeReplay{}, errors.New("host runtime session id is required")
	}
	if after < 0 {
		return HostRuntimeReplay{}, errors.New("host runtime cursor must be non-negative")
	}
	if limit < 1 || limit > 512 {
		return HostRuntimeReplay{}, errors.New("host runtime replay limit must be between 1 and 512")
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return HostRuntimeReplay{}, fmt.Errorf("begin host runtime replay: %w", err)
	}
	defer rollbackUnlessCommitted(tx)

	var latest, prunedThrough, retentionDropped, generationFloor int64
	var currentRunID string
	err = tx.QueryRowContext(ctx, `
		SELECT last_cursor, pruned_through_cursor, retention_dropped,
		       last_runtime_generation, current_runtime_run_id
		FROM host_runtime_feed_heads WHERE session_id = ?`, sessionID,
	).Scan(&latest, &prunedThrough, &retentionDropped, &generationFloor, &currentRunID)
	if errors.Is(err, sql.ErrNoRows) {
		replay := HostRuntimeReplay{
			Events: []HostRuntimeEvent{},
			Head: HostRuntimeHead{
				SchemaVersion: "host_runtime.head.v1",
				SessionID:     sessionID,
			},
		}
		if after > 0 {
			replay.Gap = &HostRuntimeGap{
				SchemaVersion:          "host_runtime.gap.v1",
				SessionID:              sessionID,
				Reason:                 "cursor_ahead",
				RequestedCursor:        after,
				OldestAvailable:        1,
				MissingCursorSpan:      after,
				RetentionDropped:       0,
				CurrentRuntimeRunID:    "",
				RuntimeGenerationFloor: 0,
			}
		}
		if err := tx.Commit(); err != nil {
			return HostRuntimeReplay{}, fmt.Errorf("commit empty host runtime replay: %w", err)
		}
		return replay, nil
	}
	if err != nil {
		return HostRuntimeReplay{}, fmt.Errorf("load host runtime replay head: %w", err)
	}

	replayAfter := after
	var gap *HostRuntimeGap
	if after < prunedThrough || after > latest {
		reason := "retention"
		if after > latest {
			reason = "cursor_ahead"
		}
		missing := prunedThrough - after
		if missing < 0 {
			missing = after - latest
		}
		gap = &HostRuntimeGap{
			SchemaVersion:          "host_runtime.gap.v1",
			SessionID:              sessionID,
			Reason:                 reason,
			RequestedCursor:        after,
			OldestAvailable:        prunedThrough + 1,
			LatestCursor:           latest,
			MissingCursorSpan:      missing,
			RetentionDropped:       retentionDropped,
			RuntimeGenerationFloor: generationFloor,
			CurrentRuntimeRunID:    currentRunID,
		}
		replayAfter = prunedThrough
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT event_json
		FROM host_runtime_feed_events
		WHERE session_id = ? AND cursor > ?
		ORDER BY cursor ASC
		LIMIT ?`, sessionID, replayAfter, limit)
	if err != nil {
		return HostRuntimeReplay{}, fmt.Errorf("query host runtime replay: %w", err)
	}
	defer closeRows(rows)
	events := make([]HostRuntimeEvent, 0, limit)
	next := replayAfter
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return HostRuntimeReplay{}, fmt.Errorf("scan host runtime replay: %w", err)
		}
		var event HostRuntimeEvent
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			return HostRuntimeReplay{}, fmt.Errorf("decode host runtime replay: %w", err)
		}
		events = append(events, event)
		next = event.Cursor
	}
	if err := rows.Err(); err != nil {
		return HostRuntimeReplay{}, fmt.Errorf("iterate host runtime replay: %w", err)
	}
	if err := rows.Close(); err != nil {
		return HostRuntimeReplay{}, fmt.Errorf("close host runtime replay rows: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return HostRuntimeReplay{}, fmt.Errorf("commit host runtime replay: %w", err)
	}
	if gap != nil && len(events) == 0 {
		next = latest
	}
	return HostRuntimeReplay{
		Events:                 events,
		NextCursor:             next,
		LatestCursor:           latest,
		PrunedThrough:          prunedThrough,
		RuntimeGenerationFloor: generationFloor,
		CurrentRuntimeRunID:    currentRunID,
		Head: HostRuntimeHead{
			SchemaVersion:          "host_runtime.head.v1",
			SessionID:              sessionID,
			LatestCursor:           latest,
			PrunedThroughCursor:    prunedThrough,
			RetentionDropped:       retentionDropped,
			RuntimeGenerationFloor: generationFloor,
			CurrentRuntimeRunID:    currentRunID,
		},
		Gap: gap,
	}, nil
}
