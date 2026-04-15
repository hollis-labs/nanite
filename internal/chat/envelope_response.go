package chat

import (
	"encoding/json"
	"fmt"
)

// ResponseV1 is the typed envelope-response schema submitted by the frontend
// when a user acts on a rendered envelope. See plans/phase-3-s5-envelope-typed-responses.md §D1.
//
// Default shape: Data is a free-form map. Envelope types may register an override
// schema via RegisterResponseSchema; in that case the response endpoint validates
// Data against it before dispatching to the ResponseHandler.
//
// Convenience fields Answers/Decisions are populated when the envelope type is one
// of the Fast-Triage cores (collect_feedback, triage_items) — the response endpoint
// promotes well-known keys out of Data for ergonomics and back-compat with the
// Fast-Triage schema. Callers may populate either form.
type ResponseV1 struct {
	V         int            `json:"v"`
	Kind      string         `json:"kind"`
	ID        string         `json:"id"`
	Status    ResponseStatus `json:"status"`
	Data      map[string]any `json:"data,omitempty"`
	Answers   []Answer       `json:"answers,omitempty"`
	Decisions []Decision     `json:"decisions,omitempty"`
}

// ResponseStatus is the terminal state of an envelope response.
type ResponseStatus string

const (
	ResponseV1Version = 1

	StatusSubmitted ResponseStatus = "submitted"
	StatusCancelled ResponseStatus = "cancelled"
	StatusPartial   ResponseStatus = "partial"

	// RoleEnvelopeResponse is the message role used for transcript entries
	// produced by envelope-response submissions (T4/T5). Stored alongside
	// user/assistant/system/tool; context assembly maps it to "user" for
	// providers that only accept user/assistant roles.
	RoleEnvelopeResponse = "envelope_response"
)

// FormatEnvelopeResponseContent produces the self-describing string stored as
// message.content for envelope_response rows. The prefix `[envelope:{type}
// status:{status}]` lets the LLM and downstream tooling distinguish the
// payload from ordinary user text without metadata lookups.
func FormatEnvelopeResponseContent(envelopeType string, status ResponseStatus, payload string) string {
	if payload == "" {
		payload = "{}"
	}
	return "[envelope:" + envelopeType + " status:" + string(status) + "] " + payload
}

// Answer is the per-question response payload used by collect_feedback.
type Answer struct {
	QuestionID         string `json:"questionId"`
	Value              any    `json:"value"`
	AcceptedSuggestion *bool  `json:"acceptedSuggestion,omitempty"`
	Note               string `json:"note,omitempty"`
}

// Decision is the per-item triage payload used by triage_items.
type Decision struct {
	ItemID string         `json:"itemId"`
	Action string         `json:"action"`
	Note   string         `json:"note,omitempty"`
	Meta   map[string]any `json:"meta,omitempty"`
}

// Validate checks required fields and status enum on a ResponseV1. It does not
// enforce envelope-type-specific override schemas — that's the endpoint's job
// once it has looked the type up.
func (r ResponseV1) Validate() error {
	if r.V != ResponseV1Version {
		return fmt.Errorf("response_v1: unsupported schema version %d (want %d)", r.V, ResponseV1Version)
	}
	if r.Kind == "" {
		return fmt.Errorf("response_v1: kind is required")
	}
	if r.ID == "" {
		return fmt.Errorf("response_v1: id is required")
	}
	switch r.Status {
	case StatusSubmitted, StatusCancelled, StatusPartial:
	default:
		return fmt.Errorf("response_v1: invalid status %q (want submitted|cancelled|partial)", r.Status)
	}
	return nil
}

// UnmarshalResponseV1 parses + validates a ResponseV1 from JSON.
func UnmarshalResponseV1(b []byte) (ResponseV1, error) {
	var r ResponseV1
	if err := json.Unmarshal(b, &r); err != nil {
		return r, fmt.Errorf("response_v1: %w", err)
	}
	if err := r.Validate(); err != nil {
		return r, err
	}
	return r, nil
}
