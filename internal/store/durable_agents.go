package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/hollis-labs/nanite/internal/a2a"
)

const (
	DurableAgentClassAdvisor  = "advisor"
	DurableAgentClassProcess  = "process"
	DurableAgentClassTemplate = "template"
	DurableAgentClassHarness  = "harness"

	DurableAgentLaunchAPIChat         = "api_chat"
	DurableAgentLaunchCLIHarness      = "cli_harness"
	DurableAgentLaunchBootProfile     = "boot_profile"
	DurableAgentLaunchDurableAdvisor  = "durable_advisor"
	DurableAgentLaunchProcessTick     = "process_tick"
	DurableAgentLaunchTaskTemplateRun = "task_template_run"

	DurableAgentStatusSleeping        = "sleeping"
	DurableAgentStatusStarting        = "starting"
	DurableAgentStatusActive          = "active"
	DurableAgentStatusPaused          = "paused"
	DurableAgentStatusStopped         = "stopped"
	DurableAgentStatusStartRequested  = "start_requested"
	DurableAgentStatusStopRequested   = "stop_requested"
	DurableAgentStatusResumeRequested = "resume_requested"
	DurableAgentStatusFailed          = "failed"
	DurableAgentStatusArchived        = "archived"

	DurableAgentSessionRelationOwned    = "owned"
	DurableAgentSessionRelationAttached = "attached"
	DurableAgentSessionRelationSpawned  = "spawned"
	DurableAgentSessionRelationPrimary  = "primary"
	DurableAgentSessionRelationRun      = "run"
	DurableAgentSessionRelationWake     = "wake"
	DurableAgentSessionRelationHarness  = "harness"

	DurableAgentEventCreated              = "created"
	DurableAgentEventUpdated              = "updated"
	DurableAgentEventArchived             = "archived"
	DurableAgentEventSessionAttached      = "session_attached"
	DurableAgentEventStartRequested       = "start_requested"
	DurableAgentEventStartSucceeded       = "start_succeeded"
	DurableAgentEventStartFailed          = "start_failed"
	DurableAgentEventResumeRequested      = "resume_requested"
	DurableAgentEventResumeSucceeded      = "resume_succeeded"
	DurableAgentEventResumeFailed         = "resume_failed"
	DurableAgentEventPauseRequested       = "pause_requested"
	DurableAgentEventPauseSucceeded       = "pause_succeeded"
	DurableAgentEventStopRequested        = "stop_requested"
	DurableAgentEventRuntimeStopSucceeded = "runtime_stop_succeeded"
	DurableAgentEventStopSucceeded        = "stop_succeeded"
	DurableAgentEventStopFailed           = "stop_failed"
	DurableAgentEventWakeRequested        = "wake_requested"
	DurableAgentEventWakeStarted          = "wake_started"
	DurableAgentEventWakeSkipped          = "wake_skipped"
	DurableAgentEventWakeFailed           = "wake_failed"
	DurableAgentEventWakeCompleted        = "wake_completed"

	DurableAgentEventSourceAPI     = "api"
	DurableAgentEventSourceRuntime = "runtime"
)

var ErrDurableAgentInstanceNotFound = errors.New("durable agent instance not found")

// DurableAgentInstance is a configured durable-agent runtime identity. The
// provider/model/runtime and launch-source fields are captured at creation and
// intentionally not updated by the metadata update path.
type DurableAgentInstance struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Slug             string `json:"slug"`
	ProfileID        string `json:"profile_id"`
	LifecycleClass   string `json:"lifecycle_class"`
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	RuntimeKind      string `json:"runtime_kind"`
	LaunchSourceType string `json:"launch_source_type"`
	LaunchSourceID   string `json:"launch_source_id"`
	WorkRoot         string `json:"work_root"`
	Status           string `json:"status"`
	CurrentSessionID string `json:"current_session_id"`
	// URN is this instance's durable actor address, minted once at creation
	// and never re-derived (migration 159, CW-20260912-0017). The actor is
	// the INSTANCE, not the profile: two instances of one profile are two
	// correspondents, which the architecture states as "sharing a definition
	// does not share identity". Persisting rather than recomputing is the
	// contract in Tether's messaging-integration.md §1 — an app that
	// re-derives on boot orphans every message addressed to the old value.
	// SyncDurableAgentInstanceConfig deliberately does not update it.
	URN           string     `json:"urn"`
	FailureReason string     `json:"failure_reason"`
	MetadataJSON  string     `json:"metadata_json"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	ArchivedAt    *time.Time `json:"archived_at,omitempty"`
}

type DurableAgentInstanceUpdate struct {
	Name         *string
	Slug         *string
	WorkRoot     *string
	MetadataJSON *string
}

type DurableAgentInstanceSession struct {
	InstanceID string     `json:"instance_id"`
	SessionID  string     `json:"session_id"`
	Relation   string     `json:"relation"`
	AttachedAt time.Time  `json:"attached_at"`
	DetachedAt *time.Time `json:"detached_at,omitempty"`
}

type DurableAgentInstanceSessionState struct {
	DurableAgentInstanceSession
	SessionStatus        string `json:"session_status"`
	Provider             string `json:"provider"`
	Model                string `json:"model"`
	RuntimeState         string `json:"runtime_state"`
	RuntimeFailureReason string `json:"runtime_failure_reason,omitempty"`
	HaltedAt             string `json:"halted_at,omitempty"`
	HaltedReason         string `json:"halted_reason,omitempty"`
}

type DurableAgentEvent struct {
	ID           string    `json:"id"`
	InstanceID   string    `json:"instance_id"`
	EventType    string    `json:"event_type"`
	StatusBefore string    `json:"status_before"`
	StatusAfter  string    `json:"status_after"`
	SessionID    string    `json:"session_id"`
	Source       string    `json:"source"`
	Message      string    `json:"message"`
	MetadataJSON string    `json:"metadata_json"`
	CreatedAt    time.Time `json:"created_at"`
}

const durableAgentInstanceColumns = `id, name, slug, profile_id, lifecycle_class, provider, model,
    runtime_kind, launch_source_type, launch_source_id, work_root, status, current_session_id,
    failure_reason, metadata_json, urn,
    created_at, updated_at, archived_at`

const durableAgentEventColumns = `id, instance_id, event_type, status_before, status_after,
    session_id, source, message, metadata_json, created_at`

func (s *Store) CreateDurableAgentInstance(ctx context.Context, inst *DurableAgentInstance) error {
	if inst == nil {
		return errors.New("CreateDurableAgentInstance: nil instance")
	}
	if inst.Name == "" {
		return errors.New("CreateDurableAgentInstance: name is required")
	}
	if inst.Slug == "" {
		return errors.New("CreateDurableAgentInstance: slug is required")
	}
	if inst.ProfileID == "" {
		return errors.New("CreateDurableAgentInstance: profile_id is required")
	}
	if _, err := s.GetAgent(ctx, inst.ProfileID); err != nil {
		return fmt.Errorf("CreateDurableAgentInstance: profile %s: %w", inst.ProfileID, err)
	}
	applyDurableAgentInstanceDefaults(inst)
	if err := validateDurableAgentInstance(inst); err != nil {
		return err
	}

	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO durable_agent_instances (`+durableAgentInstanceColumns+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		inst.ID, inst.Name, inst.Slug, inst.ProfileID, inst.LifecycleClass,
		inst.Provider, inst.Model, inst.RuntimeKind, inst.LaunchSourceType,
		inst.LaunchSourceID, inst.WorkRoot, inst.Status, inst.CurrentSessionID,
		inst.FailureReason, inst.MetadataJSON, inst.URN,
		inst.CreatedAt.UTC().Format(time.RFC3339Nano),
		inst.UpdatedAt.UTC().Format(time.RFC3339Nano),
		formatOptionalTime(inst.ArchivedAt),
	)
	if err != nil {
		return fmt.Errorf("create durable_agent_instances %s: %w", inst.ID, err)
	}
	return nil
}

func (s *Store) GetDurableAgentInstance(ctx context.Context, id string) (*DurableAgentInstance, error) {
	var inst DurableAgentInstance
	err := scanDurableAgentInstance(s.DB.QueryRowContext(ctx,
		`SELECT `+durableAgentInstanceColumns+` FROM durable_agent_instances WHERE id = ?`, id,
	), &inst)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDurableAgentInstanceNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get durable_agent_instances %s: %w", id, err)
	}
	return &inst, nil
}

func (s *Store) GetDurableAgentInstanceBySlug(ctx context.Context, slug string) (*DurableAgentInstance, error) {
	var inst DurableAgentInstance
	err := scanDurableAgentInstance(s.DB.QueryRowContext(ctx,
		`SELECT `+durableAgentInstanceColumns+` FROM durable_agent_instances WHERE slug = ?`, slug,
	), &inst)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDurableAgentInstanceNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get durable_agent_instances by slug %s: %w", slug, err)
	}
	return &inst, nil
}

// GetDurableAgentInstanceByProfileID returns the durable_agent_instances
// row bound to profileID (profile_id, the NOT NULL FK to agent_profiles),
// or ErrDurableAgentInstanceNotFound if none exists yet. When more than one
// instance references the same profile (uncommon but not schema-forbidden
// -- profile_id has no UNIQUE constraint), the most recently updated row
// wins, matching ListDurableAgentInstances' own updated_at DESC ordering.
//
// TASKS/teams/08-team-run-launcher.md's `resolution: durable` Team Slot
// resolution is the first real caller: a Team Slot's AgentID (task 01's
// TeamSlotDefinition.AgentID, "the concrete agent_profiles.id to wake")
// names a profile, not an instance -- this is how that resolution finds
// (or learns it must first create) the durable_agent_instances row
// DurableAgentService.Start/Resume actually operate on. Deliberately not
// gated on agent_profiles.durable (see that task's own corrected-semantics
// finding: a Team Slot's launch-time "durable" resolution is independent
// of that unrelated migration-061-eject-survival flag) -- this is a plain
// profile_id lookup, no durable-candidate filtering of any kind.
func (s *Store) GetDurableAgentInstanceByProfileID(ctx context.Context, profileID string) (*DurableAgentInstance, error) {
	var inst DurableAgentInstance
	err := scanDurableAgentInstance(s.DB.QueryRowContext(ctx,
		`SELECT `+durableAgentInstanceColumns+` FROM durable_agent_instances
		  WHERE profile_id = ? ORDER BY updated_at DESC LIMIT 1`, profileID,
	), &inst)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDurableAgentInstanceNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get durable_agent_instances by profile_id %s: %w", profileID, err)
	}
	return &inst, nil
}

func (s *Store) ListDurableAgentInstances(ctx context.Context, includeArchived bool) ([]DurableAgentInstance, error) {
	where := "WHERE status != 'archived'"
	if includeArchived {
		where = ""
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+durableAgentInstanceColumns+` FROM durable_agent_instances `+where+` ORDER BY updated_at DESC, name`,
	)
	if err != nil {
		return nil, fmt.Errorf("list durable_agent_instances: %w", err)
	}
	defer closeRows(rows)
	var out []DurableAgentInstance
	for rows.Next() {
		var inst DurableAgentInstance
		if err := scanDurableAgentInstance(rows, &inst); err != nil {
			return nil, fmt.Errorf("scan durable_agent_instances: %w", err)
		}
		out = append(out, inst)
	}
	return out, rows.Err()
}

func (s *Store) UpdateDurableAgentInstance(ctx context.Context, id string, upd DurableAgentInstanceUpdate) (*DurableAgentInstance, error) {
	inst, err := s.GetDurableAgentInstance(ctx, id)
	if err != nil {
		return nil, err
	}
	if inst.Status == DurableAgentStatusArchived {
		return nil, errors.New("UpdateDurableAgentInstance: instance is archived")
	}
	if upd.Name != nil {
		inst.Name = *upd.Name
	}
	if upd.Slug != nil {
		inst.Slug = *upd.Slug
	}
	if upd.WorkRoot != nil {
		inst.WorkRoot = *upd.WorkRoot
	}
	if upd.MetadataJSON != nil {
		inst.MetadataJSON = *upd.MetadataJSON
	}
	if err := validateDurableAgentInstance(inst); err != nil {
		return nil, err
	}
	inst.UpdatedAt = time.Now().UTC()
	_, err = s.DB.ExecContext(ctx,
		`UPDATE durable_agent_instances
		    SET name = ?, slug = ?, work_root = ?, metadata_json = ?, updated_at = ?
		  WHERE id = ?`,
		inst.Name, inst.Slug, inst.WorkRoot, inst.MetadataJSON,
		inst.UpdatedAt.UTC().Format(time.RFC3339Nano), id,
	)
	if err != nil {
		return nil, fmt.Errorf("update durable_agent_instances %s: %w", id, err)
	}
	return s.GetDurableAgentInstance(ctx, id)
}

// SyncDurableAgentInstanceConfig atomically inserts or refreshes one managed
// config by slug. An omitted status preserves the runtime-owned lifecycle
// state; an explicit archive cannot be undone by a concurrent config refresh.
func (s *Store) SyncDurableAgentInstanceConfig(ctx context.Context, inst *DurableAgentInstance) (*DurableAgentInstance, error) {
	if inst == nil {
		return nil, errors.New("SyncDurableAgentInstanceConfig: nil instance")
	}
	preserveLifecycleState := inst.Status == ""
	if inst.Status == "" && inst.ArchivedAt != nil {
		inst.Status = DurableAgentStatusArchived
	}
	applyDurableAgentInstanceDefaults(inst)
	if err := validateDurableAgentInstance(inst); err != nil {
		return nil, err
	}
	if _, err := s.GetAgent(ctx, inst.ProfileID); err != nil {
		return nil, fmt.Errorf("SyncDurableAgentInstanceConfig: profile %s: %w", inst.ProfileID, err)
	}

	updatedAt := time.Now().UTC()
	archiveTime := formatOptionalTime(inst.ArchivedAt)
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO durable_agent_instances (`+durableAgentInstanceColumns+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(slug) DO UPDATE SET
		     name = excluded.name,
		     profile_id = excluded.profile_id,
		     lifecycle_class = excluded.lifecycle_class,
		     provider = excluded.provider,
		     model = excluded.model,
		     runtime_kind = excluded.runtime_kind,
		     launch_source_type = excluded.launch_source_type,
		     launch_source_id = excluded.launch_source_id,
		     work_root = excluded.work_root,
		     status = CASE WHEN ? THEN durable_agent_instances.status ELSE excluded.status END,
		     current_session_id = CASE
		         WHEN NOT ? AND excluded.status = 'archived' THEN excluded.current_session_id
		         ELSE durable_agent_instances.current_session_id
		     END,
		     failure_reason = CASE
		         WHEN NOT ? AND excluded.status = 'archived' THEN excluded.failure_reason
		         ELSE durable_agent_instances.failure_reason
		     END,
		     metadata_json = excluded.metadata_json,
		     updated_at = ?,
		     archived_at = CASE
		         WHEN NOT ? AND excluded.status = 'archived' THEN COALESCE(excluded.archived_at, ?)
		         ELSE durable_agent_instances.archived_at
		     END`,
		inst.ID, inst.Name, inst.Slug, inst.ProfileID, inst.LifecycleClass,
		inst.Provider, inst.Model, inst.RuntimeKind, inst.LaunchSourceType,
		inst.LaunchSourceID, inst.WorkRoot, inst.Status, inst.CurrentSessionID,
		inst.FailureReason, inst.MetadataJSON, inst.URN,
		inst.CreatedAt.UTC().Format(time.RFC3339Nano),
		inst.UpdatedAt.UTC().Format(time.RFC3339Nano),
		archiveTime,
		preserveLifecycleState, preserveLifecycleState, preserveLifecycleState,
		updatedAt.Format(time.RFC3339Nano), preserveLifecycleState, updatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, fmt.Errorf("sync durable_agent_instances %s: %w", inst.Slug, err)
	}
	saved, err := s.GetDurableAgentInstanceBySlug(ctx, inst.Slug)
	if err != nil {
		return nil, err
	}
	*inst = *saved
	return saved, nil
}

func (s *Store) SetDurableAgentInstanceStatus(ctx context.Context, id, status string) (*DurableAgentInstance, error) {
	if !validDurableAgentStatus(status) || status == DurableAgentStatusArchived {
		return nil, fmt.Errorf("SetDurableAgentInstanceStatus: invalid status %q", status)
	}
	now := time.Now().UTC()
	res, err := s.DB.ExecContext(ctx,
		`UPDATE durable_agent_instances SET status = ?, updated_at = ? WHERE id = ? AND status != 'archived'`,
		status, now.Format(time.RFC3339Nano), id,
	)
	if err != nil {
		return nil, fmt.Errorf("set durable_agent_instances status %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("set durable_agent_instances status rows affected: %w", err)
	}
	if n == 0 {
		return nil, ErrDurableAgentInstanceNotFound
	}
	return s.GetDurableAgentInstance(ctx, id)
}

func (s *Store) SetDurableAgentInstanceLaunchState(ctx context.Context, id, status, sessionID, failureReason string) (*DurableAgentInstance, error) {
	if !validDurableAgentStatus(status) || status == DurableAgentStatusArchived {
		return nil, fmt.Errorf("SetDurableAgentInstanceLaunchState: invalid status %q", status)
	}
	now := time.Now().UTC()
	res, err := s.DB.ExecContext(ctx,
		`UPDATE durable_agent_instances
		    SET status = ?, current_session_id = ?, failure_reason = ?, updated_at = ?
		  WHERE id = ? AND status != 'archived'`,
		status, sessionID, failureReason, now.Format(time.RFC3339Nano), id,
	)
	if err != nil {
		return nil, fmt.Errorf("set durable_agent_instances launch state %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("set durable_agent_instances launch state rows affected: %w", err)
	}
	if n == 0 {
		return nil, ErrDurableAgentInstanceNotFound
	}
	return s.GetDurableAgentInstance(ctx, id)
}

func (s *Store) ArchiveDurableAgentInstance(ctx context.Context, id string) (*DurableAgentInstance, error) {
	now := time.Now().UTC()
	res, err := s.DB.ExecContext(ctx,
		`UPDATE durable_agent_instances
		    SET status = 'archived', archived_at = ?, updated_at = ?
		  WHERE id = ? AND status != 'archived'`,
		now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), id,
	)
	if err != nil {
		return nil, fmt.Errorf("archive durable_agent_instances %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("archive durable_agent_instances rows affected: %w", err)
	}
	if n == 0 {
		return nil, ErrDurableAgentInstanceNotFound
	}
	return s.GetDurableAgentInstance(ctx, id)
}

func (s *Store) AttachDurableAgentInstanceSession(ctx context.Context, instanceID, sessionID, relation string) error {
	if relation == "" {
		relation = DurableAgentSessionRelationOwned
	}
	if !validDurableAgentSessionRelation(relation) {
		return fmt.Errorf("AttachDurableAgentInstanceSession: invalid relation %q", relation)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO durable_agent_instance_sessions (instance_id, session_id, relation, attached_at, detached_at)
		 VALUES (?, ?, ?, ?, NULL)
		 ON CONFLICT(instance_id, session_id) DO UPDATE SET
		     relation = excluded.relation,
		     detached_at = NULL`,
		instanceID, sessionID, relation, now,
	)
	if err != nil {
		return fmt.Errorf("attach durable agent instance session: %w", err)
	}
	return nil
}

func (s *Store) ListDurableAgentInstanceSessions(ctx context.Context, instanceID string) ([]DurableAgentInstanceSession, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT instance_id, session_id, relation, attached_at, detached_at
		   FROM durable_agent_instance_sessions
		  WHERE instance_id = ?
		  ORDER BY attached_at DESC`, instanceID,
	)
	if err != nil {
		return nil, fmt.Errorf("list durable agent instance sessions: %w", err)
	}
	defer closeRows(rows)
	var out []DurableAgentInstanceSession
	for rows.Next() {
		rel, err := scanDurableAgentInstanceSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rel)
	}
	return out, rows.Err()
}

func (s *Store) ListDurableAgentInstanceSessionStates(ctx context.Context, instanceID string) ([]DurableAgentInstanceSessionState, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT rel.instance_id, rel.session_id, rel.relation, rel.attached_at, rel.detached_at,
		        COALESCE(sess.status, ''), COALESCE(sess.provider, ''), COALESCE(sess.model, ''),
		        COALESCE((
		            SELECT ar.state
		              FROM agent_runtime ar
		             WHERE ar.parent_session_id = rel.session_id
		             ORDER BY ar.started_at DESC
		             LIMIT 1
		        ), ''),
		        COALESCE((
		            SELECT ar.failure_reason
		              FROM agent_runtime ar
		             WHERE ar.parent_session_id = rel.session_id
		             ORDER BY ar.started_at DESC
		             LIMIT 1
		        ), ''),
		        COALESCE(sess.halted_at, ''),
		        COALESCE(sess.halted_reason, '')
		   FROM durable_agent_instance_sessions rel
		   LEFT JOIN sessions sess ON sess.id = rel.session_id
		  WHERE rel.instance_id = ?
		  ORDER BY rel.attached_at DESC`, instanceID,
	)
	if err != nil {
		return nil, fmt.Errorf("list durable agent instance session states: %w", err)
	}
	defer closeRows(rows)
	var out []DurableAgentInstanceSessionState
	for rows.Next() {
		var row DurableAgentInstanceSessionState
		var attached string
		var detached sql.NullString
		if err := rows.Scan(&row.InstanceID, &row.SessionID, &row.Relation, &attached, &detached,
			&row.SessionStatus, &row.Provider, &row.Model, &row.RuntimeState, &row.RuntimeFailureReason,
			&row.HaltedAt, &row.HaltedReason); err != nil {
			return nil, fmt.Errorf("scan durable agent instance session state: %w", err)
		}
		row.AttachedAt = parseStoreTime(attached)
		if detached.Valid && detached.String != "" {
			t := parseStoreTime(detached.String)
			row.DetachedAt = &t
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) CreateDurableAgentEvent(ctx context.Context, event *DurableAgentEvent) error {
	if event == nil {
		return errors.New("CreateDurableAgentEvent: nil event")
	}
	if event.InstanceID == "" {
		return errors.New("CreateDurableAgentEvent: instance_id is required")
	}
	if event.EventType == "" {
		return errors.New("CreateDurableAgentEvent: event_type is required")
	}
	applyDurableAgentEventDefaults(event)
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO durable_agent_events (`+durableAgentEventColumns+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID, event.InstanceID, event.EventType, event.StatusBefore, event.StatusAfter,
		event.SessionID, event.Source, event.Message, event.MetadataJSON,
		event.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("create durable_agent_events %s: %w", event.ID, err)
	}
	return nil
}

func (s *Store) ListDurableAgentEvents(ctx context.Context, instanceID string, limit int) ([]DurableAgentEvent, error) {
	if instanceID == "" {
		return nil, errors.New("ListDurableAgentEvents: instance_id is required")
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+durableAgentEventColumns+`
		   FROM durable_agent_events
		  WHERE instance_id = ?
		  ORDER BY created_at DESC, id DESC
		  LIMIT ?`, instanceID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list durable_agent_events: %w", err)
	}
	defer closeRows(rows)
	var out []DurableAgentEvent
	for rows.Next() {
		var event DurableAgentEvent
		if err := scanDurableAgentEvent(rows, &event); err != nil {
			return nil, fmt.Errorf("scan durable_agent_events: %w", err)
		}
		out = append(out, event)
	}
	return out, rows.Err()
}

func (s *Store) ListDurableAgentSessionStatesForSession(ctx context.Context, sessionID string) ([]DurableAgentInstanceSessionState, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT rel.instance_id, rel.session_id, rel.relation, rel.attached_at, rel.detached_at,
		        COALESCE(sess.status, ''), COALESCE(sess.provider, ''), COALESCE(sess.model, ''),
		        COALESCE((
		            SELECT ar.state
		              FROM agent_runtime ar
		             WHERE ar.parent_session_id = rel.session_id
		             ORDER BY ar.started_at DESC
		             LIMIT 1
		        ), ''),
		        COALESCE((
		            SELECT ar.failure_reason
		              FROM agent_runtime ar
		             WHERE ar.parent_session_id = rel.session_id
		             ORDER BY ar.started_at DESC
		             LIMIT 1
		        ), ''),
		        COALESCE(sess.halted_at, ''),
		        COALESCE(sess.halted_reason, '')
		   FROM durable_agent_instance_sessions rel
		   LEFT JOIN sessions sess ON sess.id = rel.session_id
		  WHERE rel.session_id = ?
		  ORDER BY rel.attached_at DESC`, sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("list durable agent session states for session: %w", err)
	}
	defer closeRows(rows)
	var out []DurableAgentInstanceSessionState
	for rows.Next() {
		var row DurableAgentInstanceSessionState
		var attached string
		var detached sql.NullString
		if err := rows.Scan(&row.InstanceID, &row.SessionID, &row.Relation, &attached, &detached,
			&row.SessionStatus, &row.Provider, &row.Model, &row.RuntimeState, &row.RuntimeFailureReason,
			&row.HaltedAt, &row.HaltedReason); err != nil {
			return nil, fmt.Errorf("scan durable agent session state for session: %w", err)
		}
		row.AttachedAt = parseStoreTime(attached)
		if detached.Valid && detached.String != "" {
			t := parseStoreTime(detached.String)
			row.DetachedAt = &t
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func applyDurableAgentEventDefaults(event *DurableAgentEvent) {
	if event.ID == "" {
		event.ID = uuid.New().String()
	}
	if event.Source == "" {
		event.Source = DurableAgentEventSourceAPI
	}
	if event.MetadataJSON == "" {
		event.MetadataJSON = "{}"
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
}

func applyDurableAgentInstanceDefaults(inst *DurableAgentInstance) {
	if inst.ID == "" {
		inst.ID = uuid.New().String()
	}
	// Mint the actor URN once. a2a.GenerateAgentURN is the canonical
	// generator and puts it under the `nanite` authority; the retired
	// store-side mirror wrote Tether's `agent-mux` instead
	// (CW-20260912-0017). A caller that supplies a URN keeps it, so a
	// restore or a deliberate re-home is not overwritten here.
	if inst.URN == "" {
		inst.URN = a2a.GenerateAgentURN()
	}
	if inst.LifecycleClass == "" {
		inst.LifecycleClass = DurableAgentClassAdvisor
	}
	if inst.RuntimeKind == "" {
		inst.RuntimeKind = "api"
	}
	if inst.LaunchSourceType == "" {
		inst.LaunchSourceType = DurableAgentLaunchDurableAdvisor
	}
	if inst.Status == "" {
		inst.Status = DurableAgentStatusSleeping
	}
	if inst.MetadataJSON == "" {
		inst.MetadataJSON = "{}"
	}
	now := time.Now().UTC()
	if inst.CreatedAt.IsZero() {
		inst.CreatedAt = now
	}
	if inst.UpdatedAt.IsZero() {
		inst.UpdatedAt = inst.CreatedAt
	}
}

func validateDurableAgentInstance(inst *DurableAgentInstance) error {
	if inst.Name == "" || inst.Slug == "" || inst.ProfileID == "" {
		return errors.New("durable agent instance requires name, slug, and profile_id")
	}
	if !validDurableAgentLifecycleClass(inst.LifecycleClass) {
		return fmt.Errorf("durable agent lifecycle_class %q invalid", inst.LifecycleClass)
	}
	if !validDurableAgentLaunchSource(inst.LaunchSourceType) {
		return fmt.Errorf("durable agent launch_source_type %q invalid", inst.LaunchSourceType)
	}
	if !validDurableAgentStatus(inst.Status) {
		return fmt.Errorf("durable agent status %q invalid", inst.Status)
	}
	return nil
}

func validDurableAgentLifecycleClass(v string) bool {
	switch v {
	case DurableAgentClassAdvisor, DurableAgentClassProcess, DurableAgentClassTemplate, DurableAgentClassHarness:
		return true
	default:
		return false
	}
}

func validDurableAgentLaunchSource(v string) bool {
	switch v {
	case DurableAgentLaunchAPIChat, DurableAgentLaunchCLIHarness, DurableAgentLaunchBootProfile,
		DurableAgentLaunchDurableAdvisor, DurableAgentLaunchProcessTick, DurableAgentLaunchTaskTemplateRun:
		return true
	default:
		return false
	}
}

func validDurableAgentStatus(v string) bool {
	switch v {
	case DurableAgentStatusSleeping, DurableAgentStatusStarting, DurableAgentStatusActive, DurableAgentStatusPaused,
		DurableAgentStatusStopped, DurableAgentStatusStartRequested, DurableAgentStatusStopRequested,
		DurableAgentStatusResumeRequested, DurableAgentStatusFailed, DurableAgentStatusArchived:
		return true
	default:
		return false
	}
}

func validDurableAgentSessionRelation(v string) bool {
	switch v {
	case DurableAgentSessionRelationOwned, DurableAgentSessionRelationAttached, DurableAgentSessionRelationSpawned,
		DurableAgentSessionRelationPrimary, DurableAgentSessionRelationRun, DurableAgentSessionRelationWake,
		DurableAgentSessionRelationHarness:
		return true
	default:
		return false
	}
}

func scanDurableAgentEvent(scanner interface{ Scan(...any) error }, event *DurableAgentEvent) error {
	var created string
	if err := scanner.Scan(
		&event.ID, &event.InstanceID, &event.EventType, &event.StatusBefore, &event.StatusAfter,
		&event.SessionID, &event.Source, &event.Message, &event.MetadataJSON, &created,
	); err != nil {
		return err
	}
	event.CreatedAt = parseStoreTime(created)
	return nil
}

func scanDurableAgentInstance(scanner interface{ Scan(...any) error }, inst *DurableAgentInstance) error {
	var created, updated string
	var archived sql.NullString
	if err := scanner.Scan(
		&inst.ID, &inst.Name, &inst.Slug, &inst.ProfileID, &inst.LifecycleClass,
		&inst.Provider, &inst.Model, &inst.RuntimeKind, &inst.LaunchSourceType,
		&inst.LaunchSourceID, &inst.WorkRoot, &inst.Status, &inst.CurrentSessionID,
		&inst.FailureReason, &inst.MetadataJSON, &inst.URN,
		&created, &updated, &archived,
	); err != nil {
		return err
	}
	inst.CreatedAt = parseStoreTime(created)
	inst.UpdatedAt = parseStoreTime(updated)
	if archived.Valid && archived.String != "" {
		t := parseStoreTime(archived.String)
		inst.ArchivedAt = &t
	} else {
		inst.ArchivedAt = nil
	}
	return nil
}

func scanDurableAgentInstanceSession(scanner interface{ Scan(...any) error }) (DurableAgentInstanceSession, error) {
	var rel DurableAgentInstanceSession
	var attached string
	var detached sql.NullString
	if err := scanner.Scan(&rel.InstanceID, &rel.SessionID, &rel.Relation, &attached, &detached); err != nil {
		return rel, fmt.Errorf("scan durable agent instance session: %w", err)
	}
	rel.AttachedAt = parseStoreTime(attached)
	if detached.Valid && detached.String != "" {
		t := parseStoreTime(detached.String)
		rel.DetachedAt = &t
	}
	return rel, nil
}

func parseStoreTime(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t
		}
	}
	return time.Time{}
}

func formatOptionalTime(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}
