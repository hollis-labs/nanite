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

	inst.EmittedAt, _ = time.Parse(time.RFC3339Nano, emittedAt)
	if respondedAt.Valid {
		t, _ := time.Parse(time.RFC3339Nano, respondedAt.String)
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

// RecordResponse writes the terminal response payload onto an envelope instance.
// Returns ErrEnvelopeAlreadyResponded if responded_at is already set — callers
// should fetch the existing response via GetEnvelopeInstance and return it with
// a 409 to keep submissions idempotent.
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
