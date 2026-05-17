package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// EnvelopeInstance represents a server-side record of an envelope emitted into
// a chat session. Responses submitted via POST /api/envelopes/:id/respond
// update the Responded* fields. See plans/phase-3-s5-envelope-typed-responses.md §T2.
type EnvelopeInstance struct {
	ID             string     `json:"id"`
	SessionID      string     `json:"session_id"`
	EnvelopeType   string     `json:"envelope_type"`
	EnvelopeJSON   string     `json:"envelope_json"`
	EmittedAt      time.Time  `json:"emitted_at"`
	RespondedAt    *time.Time `json:"responded_at,omitempty"`
	ResponseStatus string     `json:"response_status,omitempty"`
	ResponseJSON   string     `json:"response_json,omitempty"`
}

// ErrEnvelopeAlreadyResponded is returned by RecordResponse when the envelope
// instance already carries a terminal response. Callers can treat this as a
// 409 and return the stored response to the client.
var ErrEnvelopeAlreadyResponded = errors.New("envelope already responded")

// CreateEnvelopeInstance persists a new envelope instance. If ID is empty, a
// new UUIDv4 is generated and assigned on the passed-in pointer. The caller
// should inject the resulting ID into the envelope JSON before streaming.
func (s *Store) CreateEnvelopeInstance(inst *EnvelopeInstance) error {
	if inst.ID == "" {
		inst.ID = uuid.NewString()
	}
	if inst.EmittedAt.IsZero() {
		inst.EmittedAt = time.Now().UTC()
	}
	_, err := s.DB.Exec(
		`INSERT INTO envelope_instances (id, session_id, envelope_type, envelope_json, emitted_at)
		 VALUES (?, ?, ?, ?, ?)`,
		inst.ID, inst.SessionID, inst.EnvelopeType, inst.EnvelopeJSON,
		inst.EmittedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("create envelope instance: %w", err)
	}
	return nil
}

// GetEnvelopeInstance returns the instance by ID, or sql.ErrNoRows if missing.
func (s *Store) GetEnvelopeInstance(id string) (*EnvelopeInstance, error) {
	var inst EnvelopeInstance
	var emittedAt string
	var respondedAt, responseStatus, responseJSON sql.NullString

	err := s.DB.QueryRow(
		`SELECT id, session_id, envelope_type, envelope_json, emitted_at,
		        responded_at, response_status, response_json
		 FROM envelope_instances WHERE id = ?`,
		id,
	).Scan(&inst.ID, &inst.SessionID, &inst.EnvelopeType, &inst.EnvelopeJSON,
		&emittedAt, &respondedAt, &responseStatus, &responseJSON)
	if err != nil {
		return nil, err
	}

	inst.EmittedAt, err = time.Parse(time.RFC3339Nano, emittedAt)
	if err != nil {
		return nil, fmt.Errorf("parse emitted_at for envelope %s: %w", id, err)
	}
	if respondedAt.Valid {
		t, perr := time.Parse(time.RFC3339Nano, respondedAt.String)
		if perr != nil {
			return nil, fmt.Errorf("parse responded_at for envelope %s: %w", id, perr)
		}
		inst.RespondedAt = &t
	}
	if responseStatus.Valid {
		inst.ResponseStatus = responseStatus.String
	}
	if responseJSON.Valid {
		inst.ResponseJSON = responseJSON.String
	}
	return &inst, nil
}

// ListEnvelopeInstancesBySession returns every envelope instance for a session,
// ordered by emitted_at ASC.
func (s *Store) ListEnvelopeInstancesBySession(sessionID string) ([]EnvelopeInstance, error) {
	rows, err := s.DB.Query(
		`SELECT id, session_id, envelope_type, envelope_json, emitted_at,
		        responded_at, response_status, response_json
		   FROM envelope_instances
		  WHERE session_id = ?
		  ORDER BY emitted_at ASC`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("list envelope instances by session: %w", err)
	}
	defer rows.Close()

	var out []EnvelopeInstance
	for rows.Next() {
		var inst EnvelopeInstance
		var emittedAt string
		var respondedAt, responseStatus, responseJSON sql.NullString
		if err := rows.Scan(
			&inst.ID, &inst.SessionID, &inst.EnvelopeType, &inst.EnvelopeJSON,
			&emittedAt, &respondedAt, &responseStatus, &responseJSON,
		); err != nil {
			return nil, fmt.Errorf("scan envelope instance: %w", err)
		}
		inst.EmittedAt, err = time.Parse(time.RFC3339Nano, emittedAt)
		if err != nil {
			return nil, fmt.Errorf("parse emitted_at for envelope %s: %w", inst.ID, err)
		}
		if respondedAt.Valid {
			t, perr := time.Parse(time.RFC3339Nano, respondedAt.String)
			if perr != nil {
				return nil, fmt.Errorf("parse responded_at for envelope %s: %w", inst.ID, perr)
			}
			inst.RespondedAt = &t
		}
		if responseStatus.Valid {
			inst.ResponseStatus = responseStatus.String
		}
		if responseJSON.Valid {
			inst.ResponseJSON = responseJSON.String
		}
		out = append(out, inst)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate envelope instances: %w", err)
	}
	return out, nil
}

// ClaimEnvelopeForResponse atomically reserves an envelope instance for
// response handling by setting responded_at to now and response_status to
// "handling" iff the row is not already claimed. Returns
// ErrEnvelopeAlreadyResponded when another submission raced and won.
//
// The endpoint uses this to prevent concurrent submissions from firing the
// ResponseHandler multiple times (which would duplicate side effects).
// The final response_status + response_json are written via
// UpdateEnvelopeResponse once the handler finishes.
func (s *Store) ClaimEnvelopeForResponse(id string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.DB.Exec(
		`UPDATE envelope_instances
		   SET responded_at = ?, response_status = 'handling'
		 WHERE id = ? AND responded_at IS NULL`,
		now, id,
	)
	if err != nil {
		return fmt.Errorf("claim envelope for response: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("claim envelope for response: rows affected: %w", err)
	}
	if n == 0 {
		existing, qerr := s.GetEnvelopeInstance(id)
		if qerr != nil {
			return qerr
		}
		if existing.RespondedAt != nil {
			return ErrEnvelopeAlreadyResponded
		}
		return fmt.Errorf("claim envelope for response: no row updated for id %s", id)
	}
	return nil
}

// UpdateEnvelopeResponse writes the final status + response JSON onto an
// envelope instance that has already been claimed via ClaimEnvelopeForResponse.
// Safe to call unconditionally once the claim succeeds; the row is already
// held by this caller.
func (s *Store) UpdateEnvelopeResponse(id, status, responseJSON string) error {
	_, err := s.DB.Exec(
		`UPDATE envelope_instances
		   SET response_status = ?, response_json = ?
		 WHERE id = ?`,
		status, responseJSON, id,
	)
	if err != nil {
		return fmt.Errorf("update envelope response: %w", err)
	}
	return nil
}

// RecordResponse writes the terminal response payload onto an envelope instance.
// Returns ErrEnvelopeAlreadyResponded if responded_at is already set — callers
// should fetch the existing response via GetEnvelopeInstance and return it with
// a 409 to keep submissions idempotent.
//
// This is a non-atomic convenience for bulk/test paths that don't need to run
// a handler between the claim and the final write. Request-handling code
// should use ClaimEnvelopeForResponse + UpdateEnvelopeResponse to prevent
// duplicate handler invocations under concurrent submissions.
func (s *Store) RecordResponse(id, status, responseJSON string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.DB.Exec(
		`UPDATE envelope_instances
		   SET responded_at = ?, response_status = ?, response_json = ?
		 WHERE id = ? AND responded_at IS NULL`,
		now, status, responseJSON, id,
	)
	if err != nil {
		return fmt.Errorf("record envelope response: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("record envelope response: rows affected: %w", err)
	}
	if n == 0 {
		// Either row is missing or already responded. Distinguish by re-reading.
		existing, qerr := s.GetEnvelopeInstance(id)
		if qerr != nil {
			return qerr
		}
		if existing.RespondedAt != nil {
			return ErrEnvelopeAlreadyResponded
		}
		// Row exists but wasn't updated — unexpected.
		return fmt.Errorf("record envelope response: no row updated for id %s", id)
	}
	return nil
}
