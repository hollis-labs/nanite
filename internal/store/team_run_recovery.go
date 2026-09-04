package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

var (
	ErrTeamRunLaunchNotFound        = errors.New("team run launch not found")
	ErrTeamRunLaunchConflict        = errors.New("team run launch idempotency conflict")
	ErrTeamSignalResolutionNotFound = errors.New("team signal resolution not found")
)

type TeamRunLaunch struct {
	IdempotencyKey    string
	TeamID            string
	RequestDigest     string
	PlanningJSON      string
	DefinitionJSON    string
	LaunchRequestJSON string
	MembersJSON       string
	WorkflowRunID     string
	RunStatus         string
	Status            string
	LastError         string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

const (
	TeamRunLaunchPlanning     = "planning"
	TeamRunLaunchPrepared     = "prepared"
	TeamRunLaunchLaunched     = "launched"
	TeamRunLaunchMembersReady = "members_ready"
	TeamRunLaunchRoutingReady = "routing_ready"
)

const teamRunLaunchColumns = `idempotency_key,team_id,request_digest,planning_json,definition_json,launch_request_json,members_json,workflow_run_id,run_status,status,last_error,created_at,updated_at`

// BeginTeamRunLaunch writes the exact, recoverable launch plan before member
// session provisioning. No member intent may precede this record.
func (s *Store) BeginTeamRunLaunch(ctx context.Context, launch TeamRunLaunch) (*TeamRunLaunch, error) {
	if launch.IdempotencyKey == "" || launch.TeamID == "" || launch.RequestDigest == "" || launch.PlanningJSON == "" {
		return nil, errors.New("begin team run launch: key, team, digest, and planning material are required")
	}
	now := time.Now().UTC()
	_, err := s.DB.ExecContext(ctx, `INSERT INTO team_run_launches (`+teamRunLaunchColumns+`) VALUES (?,?,?,?,'','','',NULL,'','planning','',?,?)`,
		launch.IdempotencyKey, launch.TeamID, launch.RequestDigest, launch.PlanningJSON,
		formatTimeRFC3339Nano(now), formatTimeRFC3339Nano(now))
	if err == nil {
		return s.GetTeamRunLaunch(ctx, launch.IdempotencyKey)
	}
	existing, loadErr := s.GetTeamRunLaunch(ctx, launch.IdempotencyKey)
	if loadErr != nil {
		return nil, fmt.Errorf("begin team run launch: %w", err)
	}
	if existing.TeamID != launch.TeamID || existing.RequestDigest != launch.RequestDigest || existing.PlanningJSON != launch.PlanningJSON {
		return nil, fmt.Errorf("%w: key %q belongs to a different request", ErrTeamRunLaunchConflict, launch.IdempotencyKey)
	}
	return existing, nil
}

func (s *Store) CreateTeamRunLaunch(ctx context.Context, launch TeamRunLaunch) (*TeamRunLaunch, error) {
	if launch.IdempotencyKey == "" || launch.TeamID == "" || launch.RequestDigest == "" || launch.DefinitionJSON == "" || launch.LaunchRequestJSON == "" || launch.MembersJSON == "" {
		return nil, errors.New("create team run launch: key, team, digest, definition, request, and members are required")
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE team_run_launches SET definition_json=?,launch_request_json=?,members_json=?,status='prepared',updated_at=? WHERE idempotency_key=? AND team_id=? AND request_digest=? AND status='planning'`,
		launch.DefinitionJSON, launch.LaunchRequestJSON, launch.MembersJSON,
		formatTimeRFC3339Nano(time.Now().UTC()), launch.IdempotencyKey, launch.TeamID, launch.RequestDigest)
	if err != nil {
		return nil, fmt.Errorf("create team run launch: finalize planning: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("create team run launch rows: %w", err)
	}
	existing, loadErr := s.GetTeamRunLaunch(ctx, launch.IdempotencyKey)
	if loadErr != nil {
		return nil, loadErr
	}
	if existing.TeamID != launch.TeamID || existing.RequestDigest != launch.RequestDigest {
		return nil, fmt.Errorf("%w: key %q belongs to a different request", ErrTeamRunLaunchConflict, launch.IdempotencyKey)
	}
	if count == 0 && (existing.DefinitionJSON != launch.DefinitionJSON || existing.LaunchRequestJSON != launch.LaunchRequestJSON || existing.MembersJSON != launch.MembersJSON) {
		return nil, fmt.Errorf("%w: prepared launch %q differs", ErrTeamRunLaunchConflict, launch.IdempotencyKey)
	}
	return existing, nil
}

func (s *Store) GetTeamRunLaunch(ctx context.Context, key string) (*TeamRunLaunch, error) {
	launch, err := scanTeamRunLaunch(s.DB.QueryRowContext(ctx, `SELECT `+teamRunLaunchColumns+` FROM team_run_launches WHERE idempotency_key=?`, key))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTeamRunLaunchNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get team run launch %q: %w", key, err)
	}
	return launch, nil
}

func (s *Store) ListPendingTeamRunLaunches(ctx context.Context, limit int) ([]TeamRunLaunch, error) {
	return s.ListPendingTeamRunLaunchesAfter(ctx, "", limit)
}

func (s *Store) ListPendingTeamRunLaunchesAfter(ctx context.Context, after string, limit int) ([]TeamRunLaunch, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+teamRunLaunchColumns+` FROM team_run_launches WHERE status IN ('planning','prepared','launched') ORDER BY CASE WHEN idempotency_key>? THEN 0 ELSE 1 END,idempotency_key LIMIT ?`, after, limit)
	if err != nil {
		return nil, fmt.Errorf("list pending team run launches: %w", err)
	}
	defer closeRows(rows)
	var launches []TeamRunLaunch
	for rows.Next() {
		launch, scanErr := scanTeamRunLaunch(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan pending team run launch: %w", scanErr)
		}
		launches = append(launches, *launch)
	}
	return launches, rows.Err()
}

func (s *Store) RecordTeamRunLaunchRun(ctx context.Context, key, runID, runStatus string, launchErr error) error {
	if key == "" || runID == "" {
		return errors.New("record team run launch: key and run id are required")
	}
	errorText := ""
	if launchErr != nil {
		errorText = launchErr.Error()
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE team_run_launches SET workflow_run_id=?,run_status=?,status=CASE WHEN status IN ('members_ready','routing_ready') THEN status ELSE 'launched' END,last_error=?,updated_at=? WHERE idempotency_key=? AND (workflow_run_id IS NULL OR workflow_run_id=?)`,
		runID, runStatus, errorText, formatTimeRFC3339Nano(time.Now().UTC()), key, runID)
	if err != nil {
		return fmt.Errorf("record team run launch run: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("record team run launch run rows: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("%w: launch %q is missing or bound to another run", ErrTeamRunLaunchConflict, key)
	}
	return nil
}

// CompleteTeamRunLaunch atomically materializes the exact prepared membership
// set and marks the launch ready. Existing exact member rows make replay safe;
// a conflicting row fails closed.
func (s *Store) CompleteTeamRunLaunch(ctx context.Context, key, runID, runStatus string, members []TeamRunMember) error {
	if key == "" || runID == "" || len(members) == 0 {
		return errors.New("complete team run launch: key, run id, and members are required")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("complete team run launch: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var boundRun sql.NullString
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT workflow_run_id,status FROM team_run_launches WHERE idempotency_key=?`, key).Scan(&boundRun, &status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrTeamRunLaunchNotFound
		}
		return fmt.Errorf("complete team run launch: load: %w", err)
	}
	if boundRun.Valid && boundRun.String != runID {
		return fmt.Errorf("%w: launch %q is bound to run %q, not %q", ErrTeamRunLaunchConflict, key, boundRun.String, runID)
	}
	now := formatTimeRFC3339Nano(time.Now().UTC())
	for _, member := range members {
		if member.ID == "" || member.SlotName == "" || member.AgentID == "" || member.SessionID == "" {
			return errors.New("complete team run launch: prepared member identity is incomplete")
		}
		resolvedAt := member.ResolvedAt
		if resolvedAt == "" {
			resolvedAt = now
		}
		memberStatus := member.Status
		if memberStatus == "" {
			memberStatus = TeamRunMemberStatusActive
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO team_run_members (id,workflow_run_id,slot_name,agent_id,session_id,resolved_at,status) VALUES (?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`,
			member.ID, runID, member.SlotName, member.AgentID, member.SessionID, resolvedAt, memberStatus); err != nil {
			return fmt.Errorf("complete team run launch: insert member %s: %w", member.ID, err)
		}
		var actual TeamRunMember
		if err := scanTeamRunMember(tx.QueryRowContext(ctx, `SELECT `+teamRunMemberColumns+` FROM team_run_members WHERE id=?`, member.ID), &actual); err != nil {
			return fmt.Errorf("complete team run launch: verify member %s: %w", member.ID, err)
		}
		if actual.WorkflowRunID != runID || actual.SlotName != member.SlotName || actual.AgentID != member.AgentID || actual.SessionID != member.SessionID {
			return fmt.Errorf("%w: prepared member %q differs from persisted row", ErrTeamRunLaunchConflict, member.ID)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE team_run_launches SET workflow_run_id=?,run_status=?,status=CASE WHEN status='routing_ready' THEN status ELSE 'members_ready' END,last_error='',updated_at=? WHERE idempotency_key=?`, runID, runStatus, now, key); err != nil {
		return fmt.Errorf("complete team run launch: finalize: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("complete team run launch: commit: %w", err)
	}
	return nil
}

func (s *Store) ListTeamRunLaunchesPendingRouting(ctx context.Context, limit int) ([]TeamRunLaunch, error) {
	return s.ListTeamRunLaunchesPendingRoutingAfter(ctx, "", limit)
}

func (s *Store) ListTeamRunLaunchesPendingRoutingAfter(ctx context.Context, after string, limit int) ([]TeamRunLaunch, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+teamRunLaunchColumns+` FROM team_run_launches WHERE status='members_ready' ORDER BY CASE WHEN idempotency_key>? THEN 0 ELSE 1 END,idempotency_key LIMIT ?`, after, limit)
	if err != nil {
		return nil, fmt.Errorf("list team launches pending routing: %w", err)
	}
	defer closeRows(rows)
	var launches []TeamRunLaunch
	for rows.Next() {
		launch, scanErr := scanTeamRunLaunch(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan team launch pending routing: %w", scanErr)
		}
		launches = append(launches, *launch)
	}
	return launches, rows.Err()
}

func (s *Store) MarkTeamRunLaunchRoutingReady(ctx context.Context, key, runID string) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE team_run_launches SET status='routing_ready',last_error='',updated_at=? WHERE idempotency_key=? AND workflow_run_id=? AND status IN ('members_ready','routing_ready')`,
		formatTimeRFC3339Nano(time.Now().UTC()), key, runID)
	if err != nil {
		return fmt.Errorf("mark team run routing ready: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("mark team run routing ready rows: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("%w: launch %q is not ready for routing on run %q", ErrTeamRunLaunchConflict, key, runID)
	}
	return nil
}

func scanTeamRunLaunch(scanner interface{ Scan(...any) error }) (*TeamRunLaunch, error) {
	var launch TeamRunLaunch
	var runID sql.NullString
	var createdAt, updatedAt string
	if err := scanner.Scan(&launch.IdempotencyKey, &launch.TeamID, &launch.RequestDigest, &launch.PlanningJSON, &launch.DefinitionJSON,
		&launch.LaunchRequestJSON, &launch.MembersJSON, &runID, &launch.RunStatus, &launch.Status,
		&launch.LastError, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	if runID.Valid {
		launch.WorkflowRunID = runID.String
	}
	launch.CreatedAt = parseTimeRFC3339Nano(createdAt)
	launch.UpdatedAt = parseTimeRFC3339Nano(updatedAt)
	return &launch, nil
}

type TeamRunMemberIntent struct {
	IdempotencyKey   string
	Ordinal          int
	MemberID         string
	TeamID           string
	SlotName         string
	AgentID          string
	SessionID        string
	ProvisioningKind string
	Status           string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

const (
	TeamRunMemberIntentPlanned     = "planned"
	TeamRunMemberIntentProvisioned = "provisioned"
)

func (s *Store) CreateTeamRunMemberIntent(ctx context.Context, intent TeamRunMemberIntent) (*TeamRunMemberIntent, error) {
	if intent.IdempotencyKey == "" || intent.Ordinal < 0 || intent.MemberID == "" || intent.TeamID == "" || intent.SlotName == "" || intent.AgentID == "" || intent.SessionID == "" {
		return nil, errors.New("create team member intent: complete stable identity is required")
	}
	if intent.ProvisioningKind != "fresh" && intent.ProvisioningKind != "durable" {
		return nil, errors.New("create team member intent: provisioning kind must be fresh or durable")
	}
	now := time.Now().UTC()
	intent.CreatedAt, intent.UpdatedAt, intent.Status = now, now, TeamRunMemberIntentPlanned
	_, err := s.DB.ExecContext(ctx, `INSERT INTO team_run_member_intents (idempotency_key,ordinal,member_id,team_id,slot_name,agent_id,session_id,provisioning_kind,status,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		intent.IdempotencyKey, intent.Ordinal, intent.MemberID, intent.TeamID, intent.SlotName, intent.AgentID, intent.SessionID, intent.ProvisioningKind, intent.Status,
		formatTimeRFC3339Nano(now), formatTimeRFC3339Nano(now))
	if err == nil {
		return &intent, nil
	}
	existing, loadErr := s.GetTeamRunMemberIntent(ctx, intent.IdempotencyKey, intent.Ordinal)
	if loadErr != nil {
		return nil, fmt.Errorf("create team member intent: %w", err)
	}
	if existing.MemberID != intent.MemberID || existing.TeamID != intent.TeamID || existing.SlotName != intent.SlotName || existing.AgentID != intent.AgentID || existing.ProvisioningKind != intent.ProvisioningKind {
		return nil, fmt.Errorf("%w: member intent %q/%d differs", ErrTeamRunLaunchConflict, intent.IdempotencyKey, intent.Ordinal)
	}
	return existing, nil
}

func (s *Store) GetTeamRunMemberIntent(ctx context.Context, key string, ordinal int) (*TeamRunMemberIntent, error) {
	intent, err := scanTeamRunMemberIntent(s.DB.QueryRowContext(ctx, `SELECT idempotency_key,ordinal,member_id,team_id,slot_name,agent_id,session_id,provisioning_kind,status,created_at,updated_at FROM team_run_member_intents WHERE idempotency_key=? AND ordinal=?`, key, ordinal))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTeamRunLaunchNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get team member intent: %w", err)
	}
	return intent, nil
}

func (s *Store) CompleteTeamRunMemberIntent(ctx context.Context, key string, ordinal int, sessionID string) (*TeamRunMemberIntent, error) {
	if sessionID == "" {
		return nil, errors.New("complete team member intent: session id is required")
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE team_run_member_intents SET session_id=?,status='provisioned',updated_at=? WHERE idempotency_key=? AND ordinal=?`, sessionID, formatTimeRFC3339Nano(time.Now().UTC()), key, ordinal)
	if err != nil {
		return nil, fmt.Errorf("complete team member intent: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("complete team member intent rows: %w", err)
	}
	if count == 0 {
		return nil, ErrTeamRunLaunchNotFound
	}
	return s.GetTeamRunMemberIntent(ctx, key, ordinal)
}

func scanTeamRunMemberIntent(scanner interface{ Scan(...any) error }) (*TeamRunMemberIntent, error) {
	var intent TeamRunMemberIntent
	var createdAt, updatedAt string
	if err := scanner.Scan(&intent.IdempotencyKey, &intent.Ordinal, &intent.MemberID, &intent.TeamID, &intent.SlotName,
		&intent.AgentID, &intent.SessionID, &intent.ProvisioningKind, &intent.Status, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	intent.CreatedAt, intent.UpdatedAt = parseTimeRFC3339Nano(createdAt), parseTimeRFC3339Nano(updatedAt)
	return &intent, nil
}

type TeamSignalResolution struct {
	WorkflowRunID      string
	StepID             string
	ResponderReference string
	Output             string
	MemberIDs          []string
	Status             string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

const (
	TeamSignalResolutionPrepared  = "prepared"
	TeamSignalResolutionCompleted = "completed"
)

func (s *Store) GetTeamSignalResolution(ctx context.Context, runID, stepID string) (*TeamSignalResolution, error) {
	resolution, err := scanTeamSignalResolution(s.DB.QueryRowContext(ctx, `SELECT workflow_run_id,step_id,responder_reference,output,member_ids_json,status,created_at,updated_at FROM team_signal_resolutions WHERE workflow_run_id=? AND step_id=?`, runID, stepID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTeamSignalResolutionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get team signal resolution: %w", err)
	}
	return resolution, nil
}

// PrepareTeamSignalResolution commits the winning trigger and member stand-down
// together. Replays return the immutable prepared outcome.
func (s *Store) PrepareTeamSignalResolution(ctx context.Context, resolution TeamSignalResolution) (*TeamSignalResolution, error) {
	if resolution.WorkflowRunID == "" || resolution.StepID == "" || resolution.ResponderReference == "" || len(resolution.MemberIDs) == 0 {
		return nil, errors.New("prepare team signal resolution: run, step, responder, and members are required")
	}
	sort.Strings(resolution.MemberIDs)
	memberJSON, err := json.Marshal(resolution.MemberIDs)
	if err != nil {
		return nil, fmt.Errorf("prepare team signal resolution: encode members: %w", err)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("prepare team signal resolution: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	existing, err := scanTeamSignalResolution(tx.QueryRowContext(ctx, `SELECT workflow_run_id,step_id,responder_reference,output,member_ids_json,status,created_at,updated_at FROM team_signal_resolutions WHERE workflow_run_id=? AND step_id=?`, resolution.WorkflowRunID, resolution.StepID))
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("prepare team signal resolution: load replay: %w", err)
	}
	for _, memberID := range resolution.MemberIDs {
		result, updateErr := tx.ExecContext(ctx, `UPDATE team_run_members SET status='stopped' WHERE id=? AND workflow_run_id=? AND status='active'`, memberID, resolution.WorkflowRunID)
		if updateErr != nil {
			return nil, fmt.Errorf("prepare team signal resolution: stop member %s: %w", memberID, updateErr)
		}
		count, countErr := result.RowsAffected()
		if countErr != nil {
			return nil, fmt.Errorf("prepare team signal resolution: stop member %s rows: %w", memberID, countErr)
		}
		if count != 1 {
			return nil, fmt.Errorf("prepare team signal resolution: member %s is not active", memberID)
		}
	}
	now := time.Now().UTC()
	if resolution.CreatedAt.IsZero() {
		resolution.CreatedAt = now
	}
	resolution.UpdatedAt = resolution.CreatedAt
	resolution.Status = TeamSignalResolutionPrepared
	if _, err := tx.ExecContext(ctx, `INSERT INTO team_signal_resolutions (workflow_run_id,step_id,responder_reference,output,member_ids_json,status,created_at,updated_at) VALUES (?,?,?,?,?,'prepared',?,?)`,
		resolution.WorkflowRunID, resolution.StepID, resolution.ResponderReference, resolution.Output, string(memberJSON),
		formatTimeRFC3339Nano(resolution.CreatedAt), formatTimeRFC3339Nano(resolution.UpdatedAt)); err != nil {
		return nil, fmt.Errorf("prepare team signal resolution: insert: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("prepare team signal resolution: commit: %w", err)
	}
	return &resolution, nil
}

func (s *Store) CompleteTeamSignalResolution(ctx context.Context, runID, stepID string) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE team_signal_resolutions SET status='completed',updated_at=? WHERE workflow_run_id=? AND step_id=?`, formatTimeRFC3339Nano(time.Now().UTC()), runID, stepID)
	if err != nil {
		return fmt.Errorf("complete team signal resolution: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("complete team signal resolution rows: %w", err)
	}
	if count == 0 {
		return ErrTeamSignalResolutionNotFound
	}
	return nil
}

// CompleteClosedTeamSignalResolutions repairs the only remaining crash window:
// member stand-down and the workflow wait both committed, but the service did
// not get to acknowledge the receipt. The canonical wait row is authoritative.
func (s *Store) CompleteClosedTeamSignalResolutions(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	result, err := s.DB.ExecContext(ctx, `
UPDATE team_signal_resolutions
SET status='completed',updated_at=?
WHERE rowid IN (
  SELECT r.rowid
  FROM team_signal_resolutions r
  JOIN workflow_waits w ON w.run_id=r.workflow_run_id AND w.node_id=r.step_id
  WHERE r.status='prepared' AND w.status<>'open'
  ORDER BY r.created_at,r.workflow_run_id,r.step_id
  LIMIT ?
)`, formatTimeRFC3339Nano(time.Now().UTC()), limit)
	if err != nil {
		return 0, fmt.Errorf("complete closed team signal resolutions: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("complete closed team signal resolution rows: %w", err)
	}
	return int(count), nil
}

func scanTeamSignalResolution(scanner interface{ Scan(...any) error }) (*TeamSignalResolution, error) {
	var resolution TeamSignalResolution
	var memberJSON, createdAt, updatedAt string
	if err := scanner.Scan(&resolution.WorkflowRunID, &resolution.StepID, &resolution.ResponderReference,
		&resolution.Output, &memberJSON, &resolution.Status, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(memberJSON), &resolution.MemberIDs); err != nil {
		return nil, fmt.Errorf("decode team signal member ids: %w", err)
	}
	resolution.CreatedAt = parseTimeRFC3339Nano(createdAt)
	resolution.UpdatedAt = parseTimeRFC3339Nano(updatedAt)
	return &resolution, nil
}

// ListOpenTeamSignalRunIDs returns durable Team waits that need trigger
// evaluation. It is used by the periodic reconciler and startup recovery.
func (s *Store) ListOpenTeamSignalRunIDs(ctx context.Context, limit int) ([]string, error) {
	return s.ListOpenTeamSignalRunIDsAfter(ctx, "", limit)
}

func (s *Store) ListOpenTeamSignalRunIDsAfter(ctx context.Context, after string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.DB.QueryContext(ctx, `
SELECT run_id FROM (
  SELECT DISTINCT w.run_id
  FROM workflow_waits w
  JOIN workflow_runs r ON r.id=w.run_id
  WHERE w.status='open'
    AND json_extract(w.record_json,'$.kind')='signal'
    AND json_extract(w.record_json,'$.authority.attributes.responder_kind')='team-host'
    AND r.runtime_status IN ('running','waiting')
)
ORDER BY CASE WHEN run_id>? THEN 0 ELSE 1 END,run_id
LIMIT ?`, after, limit)
	if err != nil {
		return nil, fmt.Errorf("list open team signal runs: %w", err)
	}
	defer closeRows(rows)
	var runIDs []string
	for rows.Next() {
		var runID string
		if err := rows.Scan(&runID); err != nil {
			return nil, fmt.Errorf("scan open team signal run: %w", err)
		}
		runIDs = append(runIDs, runID)
	}
	return runIDs, rows.Err()
}

// StopTeamRunMembers idempotently stands down every active member belonging to
// one terminal or explicitly canceled Team workflow run.
func (s *Store) StopTeamRunMembers(ctx context.Context, runID string) (int, error) {
	if runID == "" {
		return 0, errors.New("stop team run members: run id is required")
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE team_run_members SET status='stopped' WHERE workflow_run_id=? AND status='active'`, runID)
	if err != nil {
		return 0, fmt.Errorf("stop team run members: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("stop team run member rows: %w", err)
	}
	return int(count), nil
}

// StopTerminalTeamRunMembers repairs a crash after the workflow terminal
// transition committed but before its Team host acknowledgement. Successfully
// repaired rows leave this bounded query, so a permanently failing row cannot
// starve later runs.
func (s *Store) StopTerminalTeamRunMembers(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	result, err := s.DB.ExecContext(ctx, `
UPDATE team_run_members
SET status='stopped'
WHERE id IN (
  SELECT m.id
  FROM team_run_members m
  JOIN workflow_runs r ON r.id=m.workflow_run_id
  WHERE m.status='active'
    AND r.runtime_status IN ('succeeded','failed','canceled','timed_out','crashed')
  ORDER BY m.resolved_at,m.id
  LIMIT ?
)`, limit)
	if err != nil {
		return 0, fmt.Errorf("stop terminal team run members: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("stop terminal team run member rows: %w", err)
	}
	return int(count), nil
}
