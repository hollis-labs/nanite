package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/tetherregistry"
)

// Durable actor enrollment (CW-20261024-0017, docs/architecture/tether-durable-actor.md).
// EnrollmentService is the only constructor of the store's ActorEnrollment
// writer (a test pins that). Enrollment is explicit and operator-authenticated:
// callers pass the credential id they already verified as principal.

var (
	// ErrEnrollmentReasonRequired: a reason is recorded with the audit row.
	ErrEnrollmentReasonRequired = errors.New("a reason of at most 500 characters is required")
	// ErrEnrollmentStaleRevision: the caller named a host settings revision that is no longer current.
	ErrEnrollmentStaleRevision = errors.New("host settings changed since this revision; read them again")
)

// EnrollmentService enrolls agents as durable Tether actors and retires them.
type EnrollmentService struct {
	Store     *store.Store
	Registrar tetherregistry.Registrar
}

// NewEnrollmentService builds the service with the environment-configured
// Tether registry client.
func NewEnrollmentService(st *store.Store) *EnrollmentService {
	return &EnrollmentService{Store: st, Registrar: &tetherregistry.Client{}}
}

func (s *EnrollmentService) writer() *store.ActorEnrollment { return s.Store.ActorEnrollment() }

// EnrollBinding is the actor's binding as the enrollment response shows it.
type EnrollBinding struct {
	ActorURI string `json:"actor_uri"`
	Enabled  bool   `json:"enabled"`
}

// EnrollResult is the enrollment response.
type EnrollResult struct {
	InstanceID string        `json:"instance_id"`
	URN        string        `json:"urn"`
	Binding    EnrollBinding `json:"binding"`
	Status     string        `json:"status"`
	// AlreadyEnrolled is true when this call changed nothing: the agent already
	// had its actor and the same URN is returned.
	AlreadyEnrolled bool   `json:"already_enrolled"`
	ExternalKey     string `json:"external_key"`
}

// Enroll gives the agent (host settings id) a durable Tether actor.
//
// Sequence (crash-safe): refuse early when Tether is not configured (nothing
// written); commit the 'enrolling' record; register the external key in Tether
// (idempotent: a repeat returns the same URN); then write binding, instance and
// audit row in one transaction. A crash anywhere leaves a record a retry
// resumes. revision, when non-empty, must match the host settings in force.
func (s *EnrollmentService) Enroll(ctx context.Context, principal, hostSettingsID, reason, revision string) (EnrollResult, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 500 {
		return EnrollResult{}, ErrEnrollmentReasonRequired
	}
	w := s.writer()
	if rec, err := w.Get(ctx, hostSettingsID); err == nil {
		switch rec.Status {
		case store.EnrollmentEnrolled:
			return s.result(rec, true), nil
		case store.EnrollmentDeregisterPending, store.EnrollmentRetired:
			return EnrollResult{}, store.ErrEnrollmentRetired
		}
	} else if !errors.Is(err, store.ErrEnrollmentNotFound) {
		return EnrollResult{}, err
	}
	host, err := s.Store.GetAgentHostSettings(ctx, hostSettingsID)
	if err != nil {
		return EnrollResult{}, err
	}
	if revision != "" && revision != host.Revision {
		return EnrollResult{}, ErrEnrollmentStaleRevision
	}
	if !host.Enabled {
		return EnrollResult{}, store.ErrEnrollmentHostDisabled
	}
	// Not configured, or no usable registry token: fail before anything is written.
	if err = s.Registrar.Ready(); err != nil {
		return EnrollResult{}, err
	}
	key, err := s.Store.EnrollmentKey(ctx, hostSettingsID)
	if err != nil {
		return EnrollResult{}, err
	}
	rec, err := w.Begin(ctx, principal, hostSettingsID, key, reason)
	if err != nil {
		return EnrollResult{}, err
	}
	if rec.Status == store.EnrollmentEnrolled {
		return s.result(rec, true), nil
	}
	if rec.Status != store.EnrollmentEnrolling {
		return EnrollResult{}, store.ErrEnrollmentRetired
	}
	name := strings.TrimSpace(host.Title)
	if name == "" {
		name = host.Slug
	}
	// The record's own key, not a fresh one: it is what a first attempt may already have registered.
	urn, _, err := s.Registrar.Register(ctx, rec.ExternalKey, name)
	if err != nil {
		w.NoteAttempt(ctx, hostSettingsID, enrollErrorCode(err))
		return EnrollResult{}, err
	}
	done, err := w.Complete(ctx, principal, hostSettingsID, urn)
	if err != nil {
		w.NoteAttempt(ctx, hostSettingsID, "local_write_failed")
		return EnrollResult{}, fmt.Errorf("tether registered the agent but the local write failed; repeat the enrollment to finish: %w", err)
	}
	return s.result(done, false), nil
}

func (s *EnrollmentService) result(rec store.AgentEnrollment, already bool) EnrollResult {
	out := EnrollResult{InstanceID: rec.InstanceID, URN: rec.URN, Binding: EnrollBinding{ActorURI: rec.URN, Enabled: rec.Status == store.EnrollmentEnrolled},
		Status: store.DurableAgentStatusSleeping, AlreadyEnrolled: already, ExternalKey: rec.ExternalKey}
	if inst, err := s.Store.GetDurableAgentInstance(context.Background(), rec.InstanceID); err == nil {
		out.Status = inst.Status
	}
	return out
}

// enrollErrorCode is the short, secret-free text stored on the record.
func enrollErrorCode(err error) string {
	var ref *tetherregistry.RefusedError
	switch {
	case errors.As(err, &ref):
		return fmt.Sprintf("tether_refused_%d_%s", ref.Status, ref.Code)
	case errors.Is(err, tetherregistry.ErrUnreachable):
		return "tether_unreachable"
	case errors.Is(err, tetherregistry.ErrBadResponse):
		return "tether_bad_response"
	}
	return "tether_error"
}

// RetireResult is the outcome of retiring an agent's actor.
type RetireResult struct {
	// Enrolled is false when the agent never had an actor (nothing to retire).
	Enrolled bool   `json:"enrolled"`
	URN      string `json:"urn,omitempty"`
	// Status is the record state: deregister_pending or retired.
	Status string `json:"status,omitempty"`
	// DeregisterPending is true while Tether has not yet confirmed. The actor is
	// already unroutable locally; repeat the retire to retry.
	DeregisterPending bool   `json:"deregister_pending"`
	DeregisterError   string `json:"deregister_error,omitempty"`
}

// Retire ends the agent's actor: archive the instance, disable the binding,
// then deregister in Tether. Local retirement always succeeds first, so the
// actor is never routable locally; if Tether is down or not configured the
// record stays 'deregister_pending' and a later Retire (for this or any agent)
// retries it. Retire must run before the host settings are disabled.
func (s *EnrollmentService) Retire(ctx context.Context, principal, hostSettingsID, reason string) (RetireResult, error) {
	w := s.writer()
	rec, err := w.Retire(ctx, principal, hostSettingsID, reason)
	if errors.Is(err, store.ErrEnrollmentNotFound) {
		return RetireResult{}, nil
	}
	if err != nil {
		return RetireResult{}, err
	}
	s.RetryPendingDeregistrations(ctx)
	rec, err = w.Get(ctx, hostSettingsID)
	if err != nil {
		return RetireResult{}, err
	}
	out := RetireResult{Enrolled: true, URN: rec.URN, Status: rec.Status, DeregisterPending: rec.Status == store.EnrollmentDeregisterPending}
	if out.DeregisterPending {
		out.DeregisterError = rec.LastError
	}
	return out, nil
}

// RetryPendingDeregistrations attempts every owed Tether deregistration and
// returns how many completed and how many are still pending. Failures are kept
// on the record, never raised: retirement already succeeded locally.
func (s *EnrollmentService) RetryPendingDeregistrations(ctx context.Context) (done, pending int) {
	w := s.writer()
	recs, err := w.PendingDeregistrations(ctx)
	if err != nil {
		slog.Error("enrollment: list pending deregistrations", "error", err)
		return 0, 0
	}
	for _, rec := range recs {
		if err := s.deregister(ctx, rec); err != nil {
			w.NoteAttempt(ctx, rec.HostSettingsID, enrollErrorCode2(err))
			pending++
			continue
		}
		if err := w.MarkDeregistered(ctx, rec.HostSettingsID); err != nil {
			slog.Error("enrollment: mark deregistered", "host_settings_id", rec.HostSettingsID, "error", err)
			pending++
			continue
		}
		done++
	}
	return done, pending
}

func enrollErrorCode2(err error) string {
	switch {
	case errors.Is(err, tetherregistry.ErrNotConfigured):
		return "tether_not_configured"
	case errors.Is(err, tetherregistry.ErrTokenMissing):
		return "registry_token_missing"
	case errors.Is(err, tetherregistry.ErrTokenInsecure):
		return "registry_token_insecure"
	}
	return enrollErrorCode(err)
}

// deregister asks Tether to deprecate the actor. A record that never reached
// 'enrolled' may have no URN (the crash window); the key is looked up instead,
// and nothing found means nothing to deregister.
func (s *EnrollmentService) deregister(ctx context.Context, rec store.AgentEnrollment) error {
	if err := s.Registrar.Ready(); err != nil {
		return err
	}
	urn := rec.URN
	if urn == "" {
		found, ok, err := s.Registrar.Lookup(ctx, rec.ExternalKey)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		urn = found
		if err = s.writer().SetURN(ctx, rec.HostSettingsID, urn); err != nil {
			return err
		}
	}
	return s.Registrar.Deregister(ctx, urn)
}

// AttachSession points the agent's actor at a started session so the Tether
// bridge serves it. Agents without an enrolled actor are unaffected.
func (s *EnrollmentService) AttachSession(ctx context.Context, hostSettingsID, sessionID string) {
	if _, err := s.Store.AttachActorSession(ctx, hostSettingsID, sessionID); err != nil {
		slog.Error("enrollment: attach actor session", "session_id", sessionID, "error", err)
	}
}

// DetachSession clears the actor's current session when that session ends.
func (s *EnrollmentService) DetachSession(ctx context.Context, sessionID string) {
	if _, err := s.Store.DetachActorSession(ctx, sessionID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		slog.Error("enrollment: detach actor session", "session_id", sessionID, "error", err)
	}
}
