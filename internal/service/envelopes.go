package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
)

// DefaultEnvelopeResponseHandlerTimeout caps how long a ResponseHandler can
// run before a response is abandoned as timed out. It keeps a slow handler
// (a ticket-API call, say) from stalling the request indefinitely.
const DefaultEnvelopeResponseHandlerTimeout = 5 * time.Second

// envelopeRecordTimeout bounds the writes that record a response's outcome
// once the handler has returned. They run detached from the request and the
// handler's deadline, so this is what stops a wedged database from holding
// the request forever.
const envelopeRecordTimeout = 5 * time.Second

// EnvelopeResponseStore is the store surface EnvelopeService uses.
type EnvelopeResponseStore interface {
	GetEnvelopeInstance(ctx context.Context, id string) (*store.EnvelopeInstance, error)
	ClaimEnvelopeForResponse(ctx context.Context, id string) error
	UpdateEnvelopeResponse(ctx context.Context, id, status, responseJSON string) error
	CreateMessage(ctx context.Context, msg *store.Message) error
}

// EnvelopeService records a user's response to an envelope: it checks the
// response against the envelope, claims the envelope atomically so its
// response handler runs once, runs the handler, stores the response, and adds
// the envelope_response transcript message.
type EnvelopeService struct {
	store   EnvelopeResponseStore
	timeout time.Duration
}

func NewEnvelopeService(st EnvelopeResponseStore) *EnvelopeService {
	return &EnvelopeService{store: st, timeout: DefaultEnvelopeResponseHandlerTimeout}
}

// SetHandlerTimeout replaces the response-handler timeout. Tests use it.
func (s *EnvelopeService) SetHandlerTimeout(d time.Duration) {
	s.timeout = d
}

// EnvelopeErrorKind classifies an EnvelopeError.
type EnvelopeErrorKind int

const (
	EnvelopeNotFound EnvelopeErrorKind = iota
	EnvelopeInvalid
	EnvelopeForbidden
	// EnvelopeConflict is an envelope that already has a response; the
	// error carries it.
	EnvelopeConflict
	EnvelopeTimeout
	EnvelopeInternal
)

// EnvelopeError reports why a response was refused or failed. Msg is meant
// for the caller. For EnvelopeConflict, PriorStatus and PriorResponse hold
// the envelope's existing response status and raw JSON.
type EnvelopeError struct {
	Kind          EnvelopeErrorKind
	Msg           string
	PriorStatus   string
	PriorResponse string
}

func (e *EnvelopeError) Error() string { return e.Msg }

func envelopeErr(kind EnvelopeErrorKind, msg string) error {
	return &EnvelopeError{Kind: kind, Msg: msg}
}

// envelopeConflict reports an envelope that already has a response. An
// envelope claimed but not yet given a response has no stored JSON; its
// PriorResponse is then "null", so the 409 body stays valid JSON.
func envelopeConflict(inst *store.EnvelopeInstance) error {
	prior := inst.ResponseJSON
	if prior == "" {
		prior = "null"
	}
	return &EnvelopeError{
		Kind:          EnvelopeConflict,
		Msg:           "envelope already responded",
		PriorStatus:   inst.ResponseStatus,
		PriorResponse: prior,
	}
}

// EnvelopeRespondResult is the outcome of a recorded response.
type EnvelopeRespondResult struct {
	EnvelopeType string
	SessionID    string
	// FollowUp is the handler's follow-up text, if any.
	FollowUp string
	// Silent handlers add no transcript message; MessageID is then empty.
	Silent    bool
	MessageID string
}

// RespondInput is a response to record.
type RespondInput struct {
	EnvelopeID string
	Response   chat.ResponseV1
	// BodySessionID, when BodySessionPresent, is the session the caller says
	// the envelope belongs to; a mismatch is refused. A non-string value is
	// passed as present with a value that cannot match.
	BodySessionID      string
	BodySessionPresent bool
}

// Respond records a response to an envelope. In order, it refuses: a
// missing envelope (EnvelopeNotFound), a response kind other than the
// envelope's type (EnvelopeInvalid), a session other than the envelope's
// (EnvelopeForbidden), and an envelope already responded to
// (EnvelopeConflict, also when a concurrent response claims it first). It
// then runs the envelope type's response handler, under the handler timeout
// (EnvelopeTimeout when exceeded), stores the response and, unless the
// handler is silent, adds the transcript message. Other failures are
// EnvelopeInternal with the message the API has always returned.
//
// Once the handler has returned, its outcome is always recorded: the failure
// status after a handler error or timeout, the stored response and the
// transcript message are written under a context detached from the request
// and from the handler's deadline (bounded by envelopeRecordTimeout). A
// claimed envelope therefore never stays without a response because the
// handler used up its time or the caller went away (CW-20260930-0241).
func (s *EnvelopeService) Respond(ctx context.Context, in RespondInput) (*EnvelopeRespondResult, error) {
	envelopeID := in.EnvelopeID
	resp := in.Response

	inst, err := s.store.GetEnvelopeInstance(ctx, envelopeID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, envelopeErr(EnvelopeNotFound, "envelope not found")
	}
	if err != nil {
		return nil, envelopeErr(EnvelopeInternal, err.Error())
	}

	// Kind must match the stored envelope type so response_json and
	// envelope_type stay consistent on the instance row.
	if resp.Kind != inst.EnvelopeType {
		return nil, envelopeErr(EnvelopeInvalid, "response kind does not match envelope type")
	}

	// Session-scoped defense in depth: a caller-supplied session must match.
	if in.BodySessionPresent && in.BodySessionID != inst.SessionID {
		return nil, envelopeErr(EnvelopeForbidden, "session mismatch")
	}

	// Fast-path the already-responded case. The atomic claim below is the
	// race-safe barrier; this read is a cooperative early return.
	if inst.RespondedAt != nil {
		return nil, envelopeConflict(inst)
	}

	// Claim the envelope before invoking the handler, so concurrent
	// submissions cannot each run the handler (duplicating its side effects)
	// before one of them records a response.
	if err = s.store.ClaimEnvelopeForResponse(ctx, envelopeID); err != nil {
		if errors.Is(err, store.ErrEnvelopeAlreadyResponded) {
			again, rerr := s.store.GetEnvelopeInstance(ctx, envelopeID)
			if rerr != nil {
				return nil, envelopeErr(EnvelopeInternal, rerr.Error())
			}
			return nil, envelopeConflict(again)
		}
		return nil, envelopeErr(EnvelopeInternal, err.Error())
	}

	handler := chat.LookupResponseHandler(inst.EnvelopeType)

	hctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	result, err := handler.HandleResponse(hctx, *inst, resp)

	// The envelope is claimed and the handler has run; recording the outcome
	// must not inherit the handler's deadline (already passed after a
	// timeout) or the request's cancellation, or the envelope is left claimed
	// with no response for good.
	wctx, wcancel := context.WithTimeout(context.WithoutCancel(ctx), envelopeRecordTimeout)
	defer wcancel()

	if err != nil {
		if errors.Is(hctx.Err(), context.DeadlineExceeded) {
			s.recordFailure(wctx, envelopeID, `{"reason":"handler_timeout"}`)
			return nil, envelopeErr(EnvelopeTimeout, "response handler timed out")
		}
		s.recordFailure(wctx, envelopeID, `{"reason":"handler_error"}`)
		return nil, envelopeErr(EnvelopeInternal, "handler: "+err.Error())
	}

	respJSON, err := json.Marshal(resp)
	if err != nil {
		return nil, envelopeErr(EnvelopeInternal, "marshal response: "+err.Error())
	}
	if err := s.store.UpdateEnvelopeResponse(wctx, envelopeID, string(resp.Status), string(respJSON)); err != nil {
		return nil, envelopeErr(EnvelopeInternal, err.Error())
	}

	out := &EnvelopeRespondResult{
		EnvelopeType: inst.EnvelopeType,
		SessionID:    inst.SessionID,
		FollowUp:     result.FollowUp,
		Silent:       result.Silent,
	}
	if !result.Silent {
		payload := result.TranscriptData
		if payload == nil {
			payload = map[string]any{}
		}
		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			return nil, envelopeErr(EnvelopeInternal, "marshal payload: "+err.Error())
		}
		msg := &store.Message{
			SessionID: inst.SessionID,
			Role:      chat.RoleEnvelopeResponse,
			Content:   chat.FormatEnvelopeResponseContent(inst.EnvelopeType, resp.Status, string(payloadJSON)),
		}
		if err := s.store.CreateMessage(wctx, msg); err != nil {
			return nil, envelopeErr(EnvelopeInternal, "persist message: "+err.Error())
		}
		out.MessageID = msg.ID
	}
	return out, nil
}

// recordFailure stores a failed outcome for a claimed envelope. The caller
// is already answering with the handler's failure, so a write error is only
// logged.
func (s *EnvelopeService) recordFailure(ctx context.Context, envelopeID, reasonJSON string) {
	if err := s.store.UpdateEnvelopeResponse(ctx, envelopeID, "failed", reasonJSON); err != nil {
		slog.Warn("envelope: recording failed response", "envelope_id", envelopeID, "err", err)
	}
}
