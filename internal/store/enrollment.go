package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Durable actor enrollment (CW-20261024-0017). ActorEnrollment is the ONE
// writer that creates agent_actor_bindings and actor_instances rows: it is the
// first production code that does. It does not authenticate anyone; the
// operator-authenticated caller supplies the credential id (principal) it
// already verified, and the writer records it in the binding receipt, the audit
// row and the enrollment record. A request field is never the principal.
//
// Every other path keeps refusing with ErrVerifiedActorRequired
// (CreateDurableAgentInstance is unchanged). Construct this writer only in
// internal/service/enrollment.go; a test pins that.

// Enrollment record states (agent_enrollments.status).
const (
	EnrollmentEnrolling         = "enrolling"
	EnrollmentEnrolled          = "enrolled"
	EnrollmentDeregisterPending = "deregister_pending"
	EnrollmentRetired           = "retired"
)

// EnrollmentAuthority is recorded in the binding receipt.
const EnrollmentAuthority = "nanite-enrollment"

var (
	ErrEnrollmentNotFound     = errors.New("agent enrollment not found")
	ErrEnrollmentPrincipal    = errors.New("enrollment requires the authenticated credential id")
	ErrEnrollmentHostDisabled = errors.New("host settings are disabled; enable them before enrolling")
	ErrEnrollmentRetired      = errors.New("this agent's actor is retired; a retired actor URN is never reused")
	ErrEnrollmentURN          = errors.New("enrollment URN is not a Tether agent address")
	ErrEnrollmentURNMismatch  = errors.New("agent is already enrolled with a different URN")
)

// AgentEnrollment is one agent_enrollments row.
type AgentEnrollment struct {
	HostSettingsID string `json:"host_settings_id"`
	ExternalKey    string `json:"external_key"`
	Status         string `json:"status"`
	URN            string `json:"urn,omitempty"`
	Reason         string `json:"reason,omitempty"`
	Principal      string `json:"principal"`
	Attempts       int    `json:"attempts"`
	LastError      string `json:"last_error,omitempty"`
	InstanceID     string `json:"instance_id,omitempty"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
	EnrolledAt     string `json:"enrolled_at,omitempty"`
	RetiredAt      string `json:"retired_at,omitempty"`
}

// ActorEnrollment is the single writer of durable actor rows.
type ActorEnrollment struct{ s *Store }

// ActorEnrollment returns the writer for this store.
func (s *Store) ActorEnrollment() *ActorEnrollment { return &ActorEnrollment{s: s} }

const enrollmentColumns = `host_settings_id, external_key, status, urn, reason, principal, attempts, last_error, created_at, updated_at, enrolled_at, retired_at`

type enrollmentScanner interface{ Scan(...any) error }

func scanEnrollment(sc enrollmentScanner) (AgentEnrollment, error) {
	var e AgentEnrollment
	err := sc.Scan(&e.HostSettingsID, &e.ExternalKey, &e.Status, &e.URN, &e.Reason, &e.Principal, &e.Attempts, &e.LastError, &e.CreatedAt, &e.UpdatedAt, &e.EnrolledAt, &e.RetiredAt)
	return e, err
}

// Get returns the enrollment record for a host settings id, with the actor
// instance id when one exists. ErrEnrollmentNotFound when there is none.
func (w *ActorEnrollment) Get(ctx context.Context, hostSettingsID string) (AgentEnrollment, error) {
	e, err := scanEnrollment(w.s.DB.QueryRowContext(ctx,
		`SELECT `+enrollmentColumns+` FROM agent_enrollments WHERE host_settings_id = ?`, hostSettingsID))
	if errors.Is(err, sql.ErrNoRows) {
		return AgentEnrollment{}, ErrEnrollmentNotFound
	}
	if err != nil {
		return AgentEnrollment{}, fmt.Errorf("read agent enrollment: %w", err)
	}
	if e.URN != "" {
		_ = w.s.DB.QueryRowContext(ctx, `SELECT id FROM actor_instances WHERE urn = ?`, e.URN).Scan(&e.InstanceID)
	}
	return e, nil
}

// Begin commits the 'enrolling' record BEFORE Tether is called. It is
// idempotent: an existing record for the host (in any state) is returned
// unchanged, with the key it was first written with, so a retry registers the
// same key even if the install id was regenerated meanwhile. A new record needs
// existing, enabled host settings.
func (w *ActorEnrollment) Begin(ctx context.Context, principal, hostSettingsID, externalKey, reason string) (AgentEnrollment, error) {
	principal = strings.TrimSpace(principal)
	if principal == "" {
		return AgentEnrollment{}, ErrEnrollmentPrincipal
	}
	if existing, err := w.Get(ctx, hostSettingsID); err == nil {
		return existing, nil
	} else if !errors.Is(err, ErrEnrollmentNotFound) {
		return AgentEnrollment{}, err
	}
	host, err := w.s.GetAgentHostSettings(ctx, hostSettingsID)
	if err != nil {
		return AgentEnrollment{}, err // sql.ErrNoRows: no such host settings
	}
	if !host.Enabled {
		return AgentEnrollment{}, ErrEnrollmentHostDisabled
	}
	// INSERT OR IGNORE: concurrent callers converge on the one winner's row.
	if _, err = w.s.DB.ExecContext(ctx,
		`INSERT OR IGNORE INTO agent_enrollments (host_settings_id, external_key, status, reason, principal) VALUES (?,?,?,?,?)`,
		hostSettingsID, externalKey, EnrollmentEnrolling, strings.TrimSpace(reason), principal); err != nil {
		return AgentEnrollment{}, fmt.Errorf("record enrollment: %w", err)
	}
	return w.Get(ctx, hostSettingsID)
}

// NoteAttempt records a failed attempt (a short error code, never a response
// body or credential) on the record, for the operator's view.
func (w *ActorEnrollment) NoteAttempt(ctx context.Context, hostSettingsID, code string) {
	if len(code) > 200 {
		code = code[:200]
	}
	_, _ = w.s.DB.ExecContext(ctx,
		`UPDATE agent_enrollments SET attempts = attempts + 1, last_error = ?, updated_at = ? WHERE host_settings_id = ?`,
		code, nowStamp(), hostSettingsID)
}

func nowStamp() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000Z") }

// Complete is the one-transaction writer: it inserts the binding and the actor
// instance, writes the audit rows and marks the record 'enrolled', or does none
// of it. It is idempotent: completing an already-enrolled agent with the same
// URN returns the record unchanged; a different URN is refused.
func (w *ActorEnrollment) Complete(ctx context.Context, principal, hostSettingsID, urn string) (AgentEnrollment, error) {
	principal = strings.TrimSpace(principal)
	if principal == "" {
		return AgentEnrollment{}, ErrEnrollmentPrincipal
	}
	urn = strings.TrimSpace(urn)
	if !strings.HasPrefix(urn, "msg://agent/") || len(urn) <= len("msg://agent/") {
		return AgentEnrollment{}, ErrEnrollmentURN
	}
	tx, err := w.s.DB.BeginTx(ctx, nil)
	if err != nil {
		return AgentEnrollment{}, err
	}
	defer func() { _ = tx.Rollback() }()

	rec, err := scanEnrollment(tx.QueryRowContext(ctx, `SELECT `+enrollmentColumns+` FROM agent_enrollments WHERE host_settings_id = ?`, hostSettingsID))
	if errors.Is(err, sql.ErrNoRows) {
		return AgentEnrollment{}, ErrEnrollmentNotFound
	}
	if err != nil {
		return AgentEnrollment{}, err
	}
	switch rec.Status {
	case EnrollmentEnrolled:
		if rec.URN != urn {
			return AgentEnrollment{}, ErrEnrollmentURNMismatch
		}
		_ = tx.Rollback()
		return w.Get(ctx, hostSettingsID)
	case EnrollmentDeregisterPending, EnrollmentRetired:
		return AgentEnrollment{}, ErrEnrollmentRetired
	}

	host, err := getAgentHostSettingsWhere(ctx, tx, "id", hostSettingsID)
	if err != nil {
		return AgentEnrollment{}, err
	}
	if !host.Enabled {
		return AgentEnrollment{}, ErrEnrollmentHostDisabled
	}
	now := nowStamp()
	rc, err := json.Marshal(map[string]any{"authority": EnrollmentAuthority, "v": 1, "granted_by": principal,
		"external_key": rec.ExternalKey, "host_settings_revision": host.Revision, "reason": rec.Reason, "at": now})
	if err != nil {
		return AgentEnrollment{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO agent_actor_bindings(actor_uri, host_settings_id, binding_receipt, enabled) VALUES (?,?,?,1)`,
		urn, hostSettingsID, string(rc)); err != nil {
		return AgentEnrollment{}, fmt.Errorf("insert actor binding: %w", err)
	}
	instanceID := uuid.NewString()
	meta, _ := json.Marshal(map[string]string{"host_settings_id": hostSettingsID, "external_key": rec.ExternalKey, "enrolled_by": principal})
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	title := strings.TrimSpace(host.Title)
	if title == "" {
		title = host.Slug
	}
	if _, err = tx.ExecContext(ctx,
		`INSERT INTO actor_instances (id, name, slug, profile_id, lifecycle_class, provider, model, runtime_kind, launch_source_type, launch_source_id, work_root, status, current_session_id, metadata_json, urn, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		instanceID, title, host.Slug, urn, DurableAgentClassAdvisor, host.Settings.Provider, host.Settings.Model, host.Settings.Runtime,
		DurableAgentLaunchDurableAdvisor, hostSettingsID, "", DurableAgentStatusSleeping, "", string(meta), urn, stamp, stamp); err != nil {
		return AgentEnrollment{}, fmt.Errorf("insert actor instance: %w", err)
	}
	if _, err = tx.ExecContext(ctx,
		`INSERT INTO actor_instance_events (id, instance_id, event_type, status_before, status_after, source, message, metadata_json, created_at) VALUES (?,?,?,?,?,?,?,?,?)`,
		uuid.NewString(), instanceID, DurableAgentEventCreated, "", DurableAgentStatusSleeping, DurableAgentEventSourceAPI, "enrolled as a durable Tether actor", string(meta), stamp); err != nil {
		return AgentEnrollment{}, fmt.Errorf("insert actor instance event: %w", err)
	}
	if err = auditEnrollment(ctx, tx, "actor_enrolled", principal, hostSettingsID, urn, rec.ExternalKey, rec.Reason); err != nil {
		return AgentEnrollment{}, err
	}
	if _, err = tx.ExecContext(ctx,
		`UPDATE agent_enrollments SET status = ?, urn = ?, last_error = '', enrolled_at = ?, updated_at = ? WHERE host_settings_id = ?`,
		EnrollmentEnrolled, urn, now, now, hostSettingsID); err != nil {
		return AgentEnrollment{}, fmt.Errorf("mark enrollment done: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return AgentEnrollment{}, fmt.Errorf("commit enrollment: %w", err)
	}
	return w.Get(ctx, hostSettingsID)
}

func auditEnrollment(ctx context.Context, tx *sql.Tx, event, principal, hostSettingsID, urn, key, reason string) error {
	meta, _ := json.Marshal(map[string]string{"host_settings_id": hostSettingsID, "urn": urn, "external_key": key, "principal": principal, "reason": reason})
	detail := fmt.Sprintf("%s: host settings %s by %s", event, hostSettingsID, principal)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO event_log (session_id, event_type, category, detail, metadata) VALUES ('', ?, 'audit', ?, ?)`,
		event, detail, string(meta)); err != nil {
		return fmt.Errorf("audit %s: %w", event, err)
	}
	return nil
}

// Retire ends the actor locally in one transaction: the instance is archived
// and detached from any session, the binding is disabled (so nothing routes to
// it), and the record becomes 'deregister_pending' until Tether confirms
// (MarkDeregistered). It must run BEFORE the host settings are disabled: the
// schema refuses instance updates once the host is disabled. Repeating it on a
// retired record changes nothing. ErrEnrollmentNotFound means the agent was
// never enrolled.
func (w *ActorEnrollment) Retire(ctx context.Context, principal, hostSettingsID, reason string) (AgentEnrollment, error) {
	principal = strings.TrimSpace(principal)
	if principal == "" {
		return AgentEnrollment{}, ErrEnrollmentPrincipal
	}
	tx, err := w.s.DB.BeginTx(ctx, nil)
	if err != nil {
		return AgentEnrollment{}, err
	}
	defer func() { _ = tx.Rollback() }()
	rec, err := scanEnrollment(tx.QueryRowContext(ctx, `SELECT `+enrollmentColumns+` FROM agent_enrollments WHERE host_settings_id = ?`, hostSettingsID))
	if errors.Is(err, sql.ErrNoRows) {
		return AgentEnrollment{}, ErrEnrollmentNotFound
	}
	if err != nil {
		return AgentEnrollment{}, err
	}
	if rec.Status == EnrollmentDeregisterPending || rec.Status == EnrollmentRetired {
		_ = tx.Rollback()
		return w.Get(ctx, hostSettingsID)
	}
	now := nowStamp()
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	if rec.Status == EnrollmentEnrolled {
		var instanceID, before string
		if err = tx.QueryRowContext(ctx, `SELECT id, status FROM actor_instances WHERE urn = ?`, rec.URN).Scan(&instanceID, &before); err != nil {
			return AgentEnrollment{}, fmt.Errorf("find actor instance: %w", err)
		}
		if _, err = tx.ExecContext(ctx,
			`UPDATE actor_instances SET status = 'archived', current_session_id = '', archived_at = ?, updated_at = ? WHERE id = ?`,
			stamp, stamp, instanceID); err != nil {
			return AgentEnrollment{}, fmt.Errorf("archive actor instance: %w", err)
		}
		if _, err = tx.ExecContext(ctx, `UPDATE agent_actor_bindings SET enabled = 0 WHERE actor_uri = ?`, rec.URN); err != nil {
			return AgentEnrollment{}, fmt.Errorf("disable actor binding: %w", err)
		}
		if _, err = tx.ExecContext(ctx,
			`INSERT INTO actor_instance_events (id, instance_id, event_type, status_before, status_after, source, message, created_at) VALUES (?,?,?,?,?,?,?,?)`,
			uuid.NewString(), instanceID, DurableAgentEventArchived, before, DurableAgentStatusArchived, DurableAgentEventSourceAPI, "retired; deregistration pending in Tether", stamp); err != nil {
			return AgentEnrollment{}, fmt.Errorf("insert actor instance event: %w", err)
		}
	}
	if err = auditEnrollment(ctx, tx, "actor_retired", principal, hostSettingsID, rec.URN, rec.ExternalKey, strings.TrimSpace(reason)); err != nil {
		return AgentEnrollment{}, err
	}
	if _, err = tx.ExecContext(ctx,
		`UPDATE agent_enrollments SET status = ?, retired_at = ?, updated_at = ? WHERE host_settings_id = ?`,
		EnrollmentDeregisterPending, now, now, hostSettingsID); err != nil {
		return AgentEnrollment{}, fmt.Errorf("mark enrollment retiring: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return AgentEnrollment{}, err
	}
	return w.Get(ctx, hostSettingsID)
}

// SetURN records a URN learned while deregistering a record that never reached
// 'enrolled' (the crash window). It only fills an empty URN on a pending record.
func (w *ActorEnrollment) SetURN(ctx context.Context, hostSettingsID, urn string) error {
	_, err := w.s.DB.ExecContext(ctx,
		`UPDATE agent_enrollments SET urn = ?, updated_at = ? WHERE host_settings_id = ? AND urn = '' AND status = ?`,
		urn, nowStamp(), hostSettingsID, EnrollmentDeregisterPending)
	return err
}

// MarkDeregistered records that Tether confirmed the deregistration.
func (w *ActorEnrollment) MarkDeregistered(ctx context.Context, hostSettingsID string) error {
	_, err := w.s.DB.ExecContext(ctx,
		`UPDATE agent_enrollments SET status = ?, last_error = '', updated_at = ? WHERE host_settings_id = ? AND status = ?`,
		EnrollmentRetired, nowStamp(), hostSettingsID, EnrollmentDeregisterPending)
	return err
}

// PendingDeregistrations lists the records whose Tether deregistration is
// still owed, oldest first.
func (w *ActorEnrollment) PendingDeregistrations(ctx context.Context) ([]AgentEnrollment, error) {
	rows, err := w.s.DB.QueryContext(ctx,
		`SELECT `+enrollmentColumns+` FROM agent_enrollments WHERE status = ? ORDER BY retired_at, host_settings_id`, EnrollmentDeregisterPending)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)
	var out []AgentEnrollment
	for rows.Next() {
		e, err := scanEnrollment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// AttachActorSession points the live, non-archived actor of a host settings id
// at a session and marks it active; the Tether bridge then serves it. It is a
// no-op (false) when the agent has no enrolled actor, so ordinary sessions are
// unaffected. A second attach replaces the first: one current session per actor.
func (s *Store) AttachActorSession(ctx context.Context, hostSettingsID, sessionID string) (bool, error) {
	if strings.TrimSpace(hostSettingsID) == "" || strings.TrimSpace(sessionID) == "" {
		return false, nil
	}
	res, err := s.DB.ExecContext(ctx,
		`UPDATE actor_instances SET current_session_id = ?, status = 'active', updated_at = ?
		  WHERE status != 'archived' AND urn != '' AND profile_id IN
		        (SELECT actor_uri FROM agent_actor_bindings WHERE host_settings_id = ? AND enabled = 1)`,
		sessionID, time.Now().UTC().Format(time.RFC3339Nano), hostSettingsID)
	if err != nil {
		return false, fmt.Errorf("attach actor session: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// DetachActorSession clears current_session_id on every actor attached to the
// session and returns it to 'sleeping'. Mail then waits in Tether until the
// next session attaches.
func (s *Store) DetachActorSession(ctx context.Context, sessionID string) (bool, error) {
	if strings.TrimSpace(sessionID) == "" {
		return false, nil
	}
	res, err := s.DB.ExecContext(ctx,
		`UPDATE actor_instances SET current_session_id = '', status = 'sleeping', updated_at = ?
		  WHERE current_session_id = ? AND status != 'archived'`,
		time.Now().UTC().Format(time.RFC3339Nano), sessionID)
	if err != nil {
		return false, fmt.Errorf("detach actor session: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
