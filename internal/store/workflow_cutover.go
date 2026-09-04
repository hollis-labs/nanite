package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const AgentWorkflowsCutoverID = "agent_workflows"

// WorkflowEngineIdentity is the exact persisted engine/library contract for a
// durable workflow run or immutable definition revision. The pilot identity is
// retained solely so rows written before extraction remain recoverable; new
// definitions and runs use WorkflowEngineIdentityShared.
type WorkflowEngineIdentity struct {
	Kind            string
	ContractVersion string
}

var (
	WorkflowEngineIdentityLegacy = WorkflowEngineIdentity{
		Kind: "nanite_builtin_v1",
	}
	WorkflowEngineIdentityPilot = WorkflowEngineIdentity{
		Kind:            "hadron_v0.5.0-beta.2",
		ContractVersion: "v0.5.0-beta.2",
	}
	WorkflowEngineIdentityShared = WorkflowEngineIdentity{
		Kind:            "go_workflow_v0.1.0",
		ContractVersion: "v0.1.0",
	}
)

// SupportedEmbeddedWorkflowEngineIdentity accepts only the extracted release
// and the byte-compatible pilot identity that predates extraction. It does not
// accept the legacy Nanite sequencer or any floating/latest identity.
func SupportedEmbeddedWorkflowEngineIdentity(identity WorkflowEngineIdentity) bool {
	return identity == WorkflowEngineIdentityPilot || identity == WorkflowEngineIdentityShared
}

// EmbeddedWorkflowEngineIdentities returns a copy so callers can build exact
// dual-read queries without maintaining another identity list.
func EmbeddedWorkflowEngineIdentities() []WorkflowEngineIdentity {
	return []WorkflowEngineIdentity{WorkflowEngineIdentityPilot, WorkflowEngineIdentityShared}
}

type WorkflowCutoverPhase string

const (
	WorkflowCutoverLegacy     WorkflowCutoverPhase = "legacy"
	WorkflowCutoverQuiescing  WorkflowCutoverPhase = "quiescing"
	WorkflowCutoverSharedOnly WorkflowCutoverPhase = "shared_only"
)

var (
	ErrWorkflowCutoverNotFound           = errors.New("workflow engine cutover state not found")
	ErrWorkflowCutoverLeaseHeld          = errors.New("workflow engine cutover lease is held")
	ErrWorkflowCutoverGenerationStale    = errors.New("workflow engine cutover generation is stale")
	ErrWorkflowCutoverFenceStale         = errors.New("workflow engine cutover fence is stale")
	ErrWorkflowCutoverInvalidTransition  = errors.New("invalid workflow engine cutover phase transition")
	ErrWorkflowCutoverLegacyRunsPending  = errors.New("legacy workflow runs still require disposition")
	ErrWorkflowLegacyDispositionNotFound = errors.New("legacy workflow run is not in the cutover cohort")
	ErrWorkflowLegacyDispositionConflict = errors.New("legacy workflow run disposition conflicts with persisted audit")
)

type WorkflowCutoverState struct {
	CutoverID      string
	Phase          WorkflowCutoverPhase
	Generation     int64
	Fence          int64
	TargetEngine   WorkflowEngineIdentity
	LeaseOwner     string
	LeaseToken     string
	LeaseExpiresAt time.Time
	UpdatedAt      time.Time
}

// AllowsLegacyLaunch and AllowsSharedLaunch are the product/host routing seam.
// A quiescing deployment admits neither engine, preventing a moving run cohort.
func (state WorkflowCutoverState) AllowsLegacyLaunch() bool {
	return state.Phase == WorkflowCutoverLegacy
}

func (state WorkflowCutoverState) AllowsSharedLaunch() bool {
	return state.Phase == WorkflowCutoverSharedOnly
}

const workflowCutoverColumns = `cutover_id, phase, generation, fence,
       target_engine_kind, target_engine_contract_version,
       lease_owner, lease_token, lease_expires_at, updated_at`

func scanWorkflowCutoverState(scanner interface{ Scan(...any) error }) (WorkflowCutoverState, error) {
	var state WorkflowCutoverState
	var leaseExpiresAt, updatedAt string
	if err := scanner.Scan(
		&state.CutoverID, &state.Phase, &state.Generation, &state.Fence,
		&state.TargetEngine.Kind, &state.TargetEngine.ContractVersion,
		&state.LeaseOwner, &state.LeaseToken, &leaseExpiresAt, &updatedAt,
	); err != nil {
		return WorkflowCutoverState{}, err
	}
	parsed, err := time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return WorkflowCutoverState{}, fmt.Errorf("parse workflow cutover updated_at: %w", err)
	}
	state.UpdatedAt = parsed
	if leaseExpiresAt != "" {
		parsed, err = time.Parse(time.RFC3339Nano, leaseExpiresAt)
		if err != nil {
			return WorkflowCutoverState{}, fmt.Errorf("parse workflow cutover lease_expires_at: %w", err)
		}
		state.LeaseExpiresAt = parsed
	}
	return state, nil
}

// EnsureWorkflowCutoverState creates the schema's singleton control row if it
// does not exist, then returns the persisted state. Repetition is idempotent.
func (s *Store) EnsureWorkflowCutoverState(ctx context.Context, at time.Time) (WorkflowCutoverState, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	_, err := s.DB.ExecContext(ctx, `
INSERT INTO workflow_engine_cutovers(
    cutover_id, phase, generation, fence, target_engine_kind,
    target_engine_contract_version, updated_at
)
SELECT ?, ?, 0, 0, ?, ?, ?
WHERE NOT EXISTS (
    SELECT 1 FROM workflow_engine_cutovers WHERE cutover_id = ?
)
ON CONFLICT(cutover_id) DO NOTHING`, AgentWorkflowsCutoverID, WorkflowCutoverLegacy,
		WorkflowEngineIdentityShared.Kind, WorkflowEngineIdentityShared.ContractVersion,
		workflowCutoverTime(at), AgentWorkflowsCutoverID)
	if err != nil {
		return WorkflowCutoverState{}, fmt.Errorf("ensure workflow cutover state: %w", err)
	}
	return s.LoadWorkflowCutoverState(ctx)
}

func (s *Store) LoadWorkflowCutoverState(ctx context.Context) (WorkflowCutoverState, error) {
	state, err := scanWorkflowCutoverState(s.DB.QueryRowContext(ctx,
		`SELECT `+workflowCutoverColumns+` FROM workflow_engine_cutovers WHERE cutover_id = ?`,
		AgentWorkflowsCutoverID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return WorkflowCutoverState{}, ErrWorkflowCutoverNotFound
	}
	if err != nil {
		return WorkflowCutoverState{}, fmt.Errorf("load workflow cutover state: %w", err)
	}
	return state, nil
}

type AcquireWorkflowCutoverLeaseRequest struct {
	Owner              string
	Token              string
	ExpectedGeneration int64
	Now                time.Time
	Duration           time.Duration
}

// AcquireWorkflowCutoverLease starts a new fencing epoch. A caller must first
// load the state and present its generation; a live lease is never stolen.
func (s *Store) AcquireWorkflowCutoverLease(ctx context.Context, request AcquireWorkflowCutoverLeaseRequest) (WorkflowCutoverState, error) {
	if strings.TrimSpace(request.Owner) == "" || strings.TrimSpace(request.Token) == "" || request.Duration <= 0 {
		return WorkflowCutoverState{}, fmt.Errorf("acquire workflow cutover lease: owner, token, and positive duration are required")
	}
	if request.Now.IsZero() {
		request.Now = time.Now().UTC()
	}
	if _, err := s.EnsureWorkflowCutoverState(ctx, request.Now); err != nil {
		return WorkflowCutoverState{}, err
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return WorkflowCutoverState{}, fmt.Errorf("acquire workflow cutover lease: begin: %w", err)
	}
	defer rollbackUnlessCommitted(tx)

	state, err := loadWorkflowCutoverStateTx(ctx, tx)
	if err != nil {
		return WorkflowCutoverState{}, err
	}
	if state.Generation != request.ExpectedGeneration {
		return WorkflowCutoverState{}, fmt.Errorf("%w: have %d want %d", ErrWorkflowCutoverGenerationStale, state.Generation, request.ExpectedGeneration)
	}
	if state.LeaseOwner != "" && state.LeaseExpiresAt.After(request.Now) {
		if state.LeaseOwner == request.Owner && state.LeaseToken == request.Token {
			if commitErr := tx.Commit(); commitErr != nil {
				return WorkflowCutoverState{}, fmt.Errorf("acquire workflow cutover lease: commit replay: %w", commitErr)
			}
			return state, nil
		}
		return WorkflowCutoverState{}, fmt.Errorf("%w: owner=%q expires_at=%s", ErrWorkflowCutoverLeaseHeld, state.LeaseOwner, state.LeaseExpiresAt.Format(time.RFC3339Nano))
	}

	result, err := tx.ExecContext(ctx, `
UPDATE workflow_engine_cutovers
SET fence = fence + 1, lease_owner = ?, lease_token = ?, lease_expires_at = ?, updated_at = ?
WHERE cutover_id = ? AND generation = ? AND fence = ?`,
		request.Owner, request.Token, workflowCutoverTime(request.Now.Add(request.Duration)),
		workflowCutoverTime(request.Now), AgentWorkflowsCutoverID, state.Generation, state.Fence)
	if err != nil {
		return WorkflowCutoverState{}, fmt.Errorf("acquire workflow cutover lease: update: %w", err)
	}
	if rowErr := requireOneWorkflowCutoverRow(result); rowErr != nil {
		return WorkflowCutoverState{}, rowErr
	}
	state, err = loadWorkflowCutoverStateTx(ctx, tx)
	if err != nil {
		return WorkflowCutoverState{}, err
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return WorkflowCutoverState{}, fmt.Errorf("acquire workflow cutover lease: commit: %w", commitErr)
	}
	return state, nil
}

type WorkflowCutoverLeaseMutation struct {
	Owner              string
	Token              string
	ExpectedGeneration int64
	Fence              int64
	Now                time.Time
}

func (s *Store) RenewWorkflowCutoverLease(ctx context.Context, lease WorkflowCutoverLeaseMutation, duration time.Duration) (WorkflowCutoverState, error) {
	if duration <= 0 {
		return WorkflowCutoverState{}, fmt.Errorf("renew workflow cutover lease: positive duration is required")
	}
	if lease.Now.IsZero() {
		lease.Now = time.Now().UTC()
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return WorkflowCutoverState{}, fmt.Errorf("renew workflow cutover lease: begin: %w", err)
	}
	defer rollbackUnlessCommitted(tx)
	state, err := loadWorkflowCutoverStateTx(ctx, tx)
	if err != nil {
		return WorkflowCutoverState{}, err
	}
	if validationErr := validateWorkflowCutoverLease(state, lease); validationErr != nil {
		return WorkflowCutoverState{}, validationErr
	}
	if _, updateErr := tx.ExecContext(ctx, `
UPDATE workflow_engine_cutovers
SET lease_expires_at = ?, updated_at = ?
WHERE cutover_id = ?`, workflowCutoverTime(lease.Now.Add(duration)), workflowCutoverTime(lease.Now), AgentWorkflowsCutoverID); updateErr != nil {
		return WorkflowCutoverState{}, fmt.Errorf("renew workflow cutover lease: update: %w", updateErr)
	}
	state, err = loadWorkflowCutoverStateTx(ctx, tx)
	if err != nil {
		return WorkflowCutoverState{}, err
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return WorkflowCutoverState{}, fmt.Errorf("renew workflow cutover lease: commit: %w", commitErr)
	}
	return state, nil
}

func (s *Store) ReleaseWorkflowCutoverLease(ctx context.Context, lease WorkflowCutoverLeaseMutation) (WorkflowCutoverState, error) {
	if lease.Now.IsZero() {
		lease.Now = time.Now().UTC()
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return WorkflowCutoverState{}, fmt.Errorf("release workflow cutover lease: begin: %w", err)
	}
	defer rollbackUnlessCommitted(tx)
	state, err := loadWorkflowCutoverStateTx(ctx, tx)
	if err != nil {
		return WorkflowCutoverState{}, err
	}
	if validationErr := validateWorkflowCutoverLease(state, lease); validationErr != nil {
		return WorkflowCutoverState{}, validationErr
	}
	if _, updateErr := tx.ExecContext(ctx, `
UPDATE workflow_engine_cutovers
SET lease_owner = '', lease_token = '', lease_expires_at = '', updated_at = ?
WHERE cutover_id = ?`, workflowCutoverTime(lease.Now), AgentWorkflowsCutoverID); updateErr != nil {
		return WorkflowCutoverState{}, fmt.Errorf("release workflow cutover lease: update: %w", updateErr)
	}
	state, err = loadWorkflowCutoverStateTx(ctx, tx)
	if err != nil {
		return WorkflowCutoverState{}, err
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return WorkflowCutoverState{}, fmt.Errorf("release workflow cutover lease: commit: %w", commitErr)
	}
	return state, nil
}

type TransitionWorkflowCutoverRequest struct {
	Lease WorkflowCutoverLeaseMutation
	To    WorkflowCutoverPhase
}

// TransitionWorkflowCutover changes the launch mode under the live lease.
// Entering quiescing snapshots every active legacy run into a generation-bound
// audit cohort. The shared-only transition re-captures any late legacy row and
// refuses to proceed until all cohort entries are explicitly disposed and no
// legacy run remains active.
func (s *Store) TransitionWorkflowCutover(ctx context.Context, request TransitionWorkflowCutoverRequest) (WorkflowCutoverState, error) {
	if request.Lease.Now.IsZero() {
		request.Lease.Now = time.Now().UTC()
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return WorkflowCutoverState{}, fmt.Errorf("transition workflow cutover: begin: %w", err)
	}
	defer rollbackUnlessCommitted(tx)

	state, err := loadWorkflowCutoverStateTx(ctx, tx)
	if err != nil {
		return WorkflowCutoverState{}, err
	}
	if validationErr := validateWorkflowCutoverLease(state, request.Lease); validationErr != nil {
		return WorkflowCutoverState{}, validationErr
	}
	if state.Phase == request.To {
		if commitErr := tx.Commit(); commitErr != nil {
			return WorkflowCutoverState{}, fmt.Errorf("transition workflow cutover: commit idempotent read: %w", commitErr)
		}
		return state, nil
	}
	if !validWorkflowCutoverTransition(state.Phase, request.To) {
		return WorkflowCutoverState{}, fmt.Errorf("%w: %s -> %s", ErrWorkflowCutoverInvalidTransition, state.Phase, request.To)
	}

	nextGeneration := state.Generation + 1
	if request.To == WorkflowCutoverQuiescing {
		if captureErr := captureActiveLegacyWorkflowRuns(ctx, tx, nextGeneration, request.Lease.Now); captureErr != nil {
			return WorkflowCutoverState{}, captureErr
		}
	}
	if request.To == WorkflowCutoverSharedOnly {
		if captureErr := captureActiveLegacyWorkflowRuns(ctx, tx, state.Generation, request.Lease.Now); captureErr != nil {
			return WorkflowCutoverState{}, captureErr
		}
		var active, pending int
		if queryErr := tx.QueryRowContext(ctx, `
SELECT COUNT(1) FROM workflow_runs
WHERE engine_kind = ? AND status IN ('running','waiting_on_gate','waiting_on_flex','waiting_on_loop')`,
			WorkflowEngineIdentityLegacy.Kind).Scan(&active); queryErr != nil {
			return WorkflowCutoverState{}, fmt.Errorf("transition workflow cutover: count active legacy runs: %w", queryErr)
		}
		if queryErr := tx.QueryRowContext(ctx, `
SELECT COUNT(1) FROM workflow_legacy_run_dispositions
WHERE cutover_id = ? AND cutover_generation = ? AND disposition = 'pending'`,
			AgentWorkflowsCutoverID, state.Generation).Scan(&pending); queryErr != nil {
			return WorkflowCutoverState{}, fmt.Errorf("transition workflow cutover: count pending legacy dispositions: %w", queryErr)
		}
		if active != 0 || pending != 0 {
			// Persist the newly observed cohort even though the phase transition is
			// rejected. Operators can then disposition every late legacy run under
			// this same generation and fence before retrying shared-only.
			if commitErr := tx.Commit(); commitErr != nil {
				return WorkflowCutoverState{}, fmt.Errorf("transition workflow cutover: commit blocked cohort: %w", commitErr)
			}
			return WorkflowCutoverState{}, fmt.Errorf("%w: active=%d pending=%d generation=%d", ErrWorkflowCutoverLegacyRunsPending, active, pending, state.Generation)
		}
	}

	result, err := tx.ExecContext(ctx, `
UPDATE workflow_engine_cutovers
SET phase = ?, generation = ?, updated_at = ?
WHERE cutover_id = ? AND generation = ? AND fence = ?`,
		request.To, nextGeneration, workflowCutoverTime(request.Lease.Now),
		AgentWorkflowsCutoverID, state.Generation, state.Fence)
	if err != nil {
		return WorkflowCutoverState{}, fmt.Errorf("transition workflow cutover: update: %w", err)
	}
	if rowErr := requireOneWorkflowCutoverRow(result); rowErr != nil {
		return WorkflowCutoverState{}, rowErr
	}
	state, err = loadWorkflowCutoverStateTx(ctx, tx)
	if err != nil {
		return WorkflowCutoverState{}, err
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return WorkflowCutoverState{}, fmt.Errorf("transition workflow cutover: commit: %w", commitErr)
	}
	return state, nil
}

func validWorkflowCutoverTransition(from, to WorkflowCutoverPhase) bool {
	switch from {
	case WorkflowCutoverLegacy:
		return to == WorkflowCutoverQuiescing
	case WorkflowCutoverQuiescing:
		return to == WorkflowCutoverLegacy || to == WorkflowCutoverSharedOnly
	default:
		return false
	}
}

func validateWorkflowCutoverLease(state WorkflowCutoverState, lease WorkflowCutoverLeaseMutation) error {
	if state.Generation != lease.ExpectedGeneration {
		return fmt.Errorf("%w: have %d want %d", ErrWorkflowCutoverGenerationStale, state.Generation, lease.ExpectedGeneration)
	}
	if lease.Fence <= 0 || state.Fence != lease.Fence || state.LeaseOwner != lease.Owner || state.LeaseToken != lease.Token ||
		state.LeaseOwner == "" || !state.LeaseExpiresAt.After(lease.Now) {
		return fmt.Errorf("%w: generation=%d fence=%d", ErrWorkflowCutoverFenceStale, state.Generation, state.Fence)
	}
	return nil
}

func loadWorkflowCutoverStateTx(ctx context.Context, tx *sql.Tx) (WorkflowCutoverState, error) {
	state, err := scanWorkflowCutoverState(tx.QueryRowContext(ctx,
		`SELECT `+workflowCutoverColumns+` FROM workflow_engine_cutovers WHERE cutover_id = ?`,
		AgentWorkflowsCutoverID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return WorkflowCutoverState{}, ErrWorkflowCutoverNotFound
	}
	if err != nil {
		return WorkflowCutoverState{}, fmt.Errorf("load workflow cutover state: %w", err)
	}
	return state, nil
}

func requireOneWorkflowCutoverRow(result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("workflow cutover rows affected: %w", err)
	}
	if count != 1 {
		return ErrWorkflowCutoverFenceStale
	}
	return nil
}

func captureActiveLegacyWorkflowRuns(ctx context.Context, tx *sql.Tx, generation int64, at time.Time) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO workflow_legacy_run_dispositions(
    cutover_id, cutover_generation, run_id, observed_status,
    disposition, observed_at
)
SELECT ?, ?, id, status, 'pending', ?
FROM workflow_runs
WHERE engine_kind = ?
  AND status IN ('running','waiting_on_gate','waiting_on_flex','waiting_on_loop')
ON CONFLICT(cutover_generation, run_id) DO NOTHING`,
		AgentWorkflowsCutoverID, generation, workflowCutoverTime(at), WorkflowEngineIdentityLegacy.Kind)
	if err != nil {
		return fmt.Errorf("capture active legacy workflow runs: %w", err)
	}
	return nil
}

// ListActiveLegacyWorkflowRuns is the deployment preflight surface. Terminal
// legacy rows are deliberately absent from this result but remain readable via
// GetWorkflowRun and the existing product API.
func (s *Store) ListActiveLegacyWorkflowRuns(ctx context.Context) ([]WorkflowRunRow, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT `+workflowRunColumns+`
FROM workflow_runs
WHERE engine_kind = ?
  AND status IN ('running','waiting_on_gate','waiting_on_flex','waiting_on_loop')
ORDER BY started_at, id`, WorkflowEngineIdentityLegacy.Kind)
	if err != nil {
		return nil, fmt.Errorf("list active legacy workflow runs: %w", err)
	}
	defer closeRows(rows)
	result := make([]WorkflowRunRow, 0)
	for rows.Next() {
		row, scanErr := scanWorkflowRunRow(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("list active legacy workflow runs: scan: %w", scanErr)
		}
		result = append(result, *row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list active legacy workflow runs: iterate: %w", err)
	}
	return result, nil
}

type WorkflowLegacyDisposition string

const (
	WorkflowLegacyDispositionPending  WorkflowLegacyDisposition = "pending"
	WorkflowLegacyDispositionDrained  WorkflowLegacyDisposition = "drained"
	WorkflowLegacyDispositionCanceled WorkflowLegacyDisposition = "canceled"
	WorkflowLegacyDispositionFailed   WorkflowLegacyDisposition = "failed"
)

type WorkflowLegacyRunDisposition struct {
	CutoverID         string
	CutoverGeneration int64
	RunID             string
	ObservedStatus    string
	Disposition       WorkflowLegacyDisposition
	FinalStatus       string
	Actor             string
	Reason            string
	ObservedAt        time.Time
	DisposedAt        time.Time
}

const workflowLegacyDispositionColumns = `cutover_id, cutover_generation, run_id,
       observed_status, disposition, final_status, actor, reason,
       observed_at, disposed_at`

func scanWorkflowLegacyRunDisposition(scanner interface{ Scan(...any) error }) (WorkflowLegacyRunDisposition, error) {
	var result WorkflowLegacyRunDisposition
	var observedAt, disposedAt string
	if err := scanner.Scan(
		&result.CutoverID, &result.CutoverGeneration, &result.RunID,
		&result.ObservedStatus, &result.Disposition, &result.FinalStatus,
		&result.Actor, &result.Reason, &observedAt, &disposedAt,
	); err != nil {
		return WorkflowLegacyRunDisposition{}, err
	}
	parsed, err := time.Parse(time.RFC3339Nano, observedAt)
	if err != nil {
		return WorkflowLegacyRunDisposition{}, fmt.Errorf("parse legacy workflow disposition observed_at: %w", err)
	}
	result.ObservedAt = parsed
	if disposedAt != "" {
		parsed, err = time.Parse(time.RFC3339Nano, disposedAt)
		if err != nil {
			return WorkflowLegacyRunDisposition{}, fmt.Errorf("parse legacy workflow disposition disposed_at: %w", err)
		}
		result.DisposedAt = parsed
	}
	return result, nil
}

func (s *Store) ListWorkflowLegacyRunDispositions(ctx context.Context, generation int64) ([]WorkflowLegacyRunDisposition, error) {
	if generation <= 0 {
		return nil, fmt.Errorf("list legacy workflow run dispositions: positive cutover generation is required")
	}
	rows, err := s.DB.QueryContext(ctx, `
SELECT `+workflowLegacyDispositionColumns+`
FROM workflow_legacy_run_dispositions
WHERE cutover_id = ? AND cutover_generation = ?
ORDER BY run_id`, AgentWorkflowsCutoverID, generation)
	if err != nil {
		return nil, fmt.Errorf("list legacy workflow run dispositions: %w", err)
	}
	defer closeRows(rows)
	result := make([]WorkflowLegacyRunDisposition, 0)
	for rows.Next() {
		disposition, scanErr := scanWorkflowLegacyRunDisposition(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("list legacy workflow run dispositions: scan: %w", scanErr)
		}
		result = append(result, disposition)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list legacy workflow run dispositions: iterate: %w", err)
	}
	return result, nil
}

type RecordWorkflowLegacyDispositionRequest struct {
	Lease       WorkflowCutoverLeaseMutation
	RunID       string
	Disposition WorkflowLegacyDisposition
	Actor       string
	Reason      string
}

// RecordWorkflowLegacyRunDisposition resolves one member of the active cohort
// under the same cutover fence. Canceled/failed dispositions atomically make
// the product run terminal and close unfinished product steps; drained requires
// the legacy engine to have already reached a terminal state. No runtime plan,
// input reference, or shared event is invented.
func (s *Store) RecordWorkflowLegacyRunDisposition(ctx context.Context, request RecordWorkflowLegacyDispositionRequest) (WorkflowLegacyRunDisposition, error) {
	if strings.TrimSpace(request.RunID) == "" || strings.TrimSpace(request.Actor) == "" || strings.TrimSpace(request.Reason) == "" {
		return WorkflowLegacyRunDisposition{}, fmt.Errorf("record legacy workflow disposition: run_id, actor, and reason are required")
	}
	if request.Disposition != WorkflowLegacyDispositionDrained && request.Disposition != WorkflowLegacyDispositionCanceled && request.Disposition != WorkflowLegacyDispositionFailed {
		return WorkflowLegacyRunDisposition{}, fmt.Errorf("record legacy workflow disposition: invalid disposition %q", request.Disposition)
	}
	if request.Lease.Now.IsZero() {
		request.Lease.Now = time.Now().UTC()
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return WorkflowLegacyRunDisposition{}, fmt.Errorf("record legacy workflow disposition: begin: %w", err)
	}
	defer rollbackUnlessCommitted(tx)

	state, err := loadWorkflowCutoverStateTx(ctx, tx)
	if err != nil {
		return WorkflowLegacyRunDisposition{}, err
	}
	if validationErr := validateWorkflowCutoverLease(state, request.Lease); validationErr != nil {
		return WorkflowLegacyRunDisposition{}, validationErr
	}
	if state.Phase != WorkflowCutoverQuiescing {
		return WorkflowLegacyRunDisposition{}, fmt.Errorf("%w: disposition requires quiescing phase", ErrWorkflowCutoverInvalidTransition)
	}

	disposition, err := scanWorkflowLegacyRunDisposition(tx.QueryRowContext(ctx, `
SELECT `+workflowLegacyDispositionColumns+`
FROM workflow_legacy_run_dispositions
WHERE cutover_id = ? AND cutover_generation = ? AND run_id = ?`,
		AgentWorkflowsCutoverID, state.Generation, request.RunID))
	if errors.Is(err, sql.ErrNoRows) {
		return WorkflowLegacyRunDisposition{}, ErrWorkflowLegacyDispositionNotFound
	}
	if err != nil {
		return WorkflowLegacyRunDisposition{}, fmt.Errorf("record legacy workflow disposition: load audit row: %w", err)
	}
	if disposition.Disposition != WorkflowLegacyDispositionPending {
		if disposition.Disposition == request.Disposition && disposition.Actor == request.Actor && disposition.Reason == request.Reason {
			if commitErr := tx.Commit(); commitErr != nil {
				return WorkflowLegacyRunDisposition{}, fmt.Errorf("record legacy workflow disposition: commit replay: %w", commitErr)
			}
			return disposition, nil
		}
		return WorkflowLegacyRunDisposition{}, ErrWorkflowLegacyDispositionConflict
	}

	var status, engineKind string
	if queryErr := tx.QueryRowContext(ctx, `SELECT status, engine_kind FROM workflow_runs WHERE id = ?`, request.RunID).Scan(&status, &engineKind); queryErr != nil {
		return WorkflowLegacyRunDisposition{}, fmt.Errorf("record legacy workflow disposition: load run: %w", queryErr)
	}
	if engineKind != WorkflowEngineIdentityLegacy.Kind {
		return WorkflowLegacyRunDisposition{}, fmt.Errorf("%w: run engine kind is %q", ErrWorkflowLegacyDispositionConflict, engineKind)
	}
	finalStatus := status
	switch request.Disposition {
	case WorkflowLegacyDispositionPending:
		return WorkflowLegacyRunDisposition{}, fmt.Errorf("record legacy workflow disposition: pending is not a final disposition")
	case WorkflowLegacyDispositionDrained:
		if !terminalLegacyWorkflowStatus(status) {
			return WorkflowLegacyRunDisposition{}, fmt.Errorf("%w: drained run remains %q", ErrWorkflowLegacyDispositionConflict, status)
		}
	case WorkflowLegacyDispositionCanceled:
		if activeLegacyWorkflowStatus(status) {
			finalStatus = "canceled"
			if terminateErr := terminateLegacyWorkflowRun(ctx, tx, request.RunID, finalStatus, request.Reason, request.Lease.Now); terminateErr != nil {
				return WorkflowLegacyRunDisposition{}, terminateErr
			}
		} else if status != "canceled" {
			return WorkflowLegacyRunDisposition{}, fmt.Errorf("%w: cannot cancel terminal run in status %q", ErrWorkflowLegacyDispositionConflict, status)
		}
	case WorkflowLegacyDispositionFailed:
		if activeLegacyWorkflowStatus(status) {
			finalStatus = "failed"
			if terminateErr := terminateLegacyWorkflowRun(ctx, tx, request.RunID, finalStatus, request.Reason, request.Lease.Now); terminateErr != nil {
				return WorkflowLegacyRunDisposition{}, terminateErr
			}
		} else if status != "failed" {
			return WorkflowLegacyRunDisposition{}, fmt.Errorf("%w: cannot fail terminal run in status %q", ErrWorkflowLegacyDispositionConflict, status)
		}
	}

	result, err := tx.ExecContext(ctx, `
UPDATE workflow_legacy_run_dispositions
SET disposition = ?, final_status = ?, actor = ?, reason = ?, disposed_at = ?
WHERE cutover_id = ? AND cutover_generation = ? AND run_id = ? AND disposition = 'pending'`,
		request.Disposition, finalStatus, request.Actor, request.Reason,
		workflowCutoverTime(request.Lease.Now), AgentWorkflowsCutoverID,
		state.Generation, request.RunID)
	if err != nil {
		return WorkflowLegacyRunDisposition{}, fmt.Errorf("record legacy workflow disposition: update audit: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return WorkflowLegacyRunDisposition{}, fmt.Errorf("record legacy workflow disposition: rows affected: %w", err)
	}
	if count != 1 {
		return WorkflowLegacyRunDisposition{}, ErrWorkflowLegacyDispositionConflict
	}
	disposition, err = scanWorkflowLegacyRunDisposition(tx.QueryRowContext(ctx, `
SELECT `+workflowLegacyDispositionColumns+`
FROM workflow_legacy_run_dispositions
WHERE cutover_id = ? AND cutover_generation = ? AND run_id = ?`,
		AgentWorkflowsCutoverID, state.Generation, request.RunID))
	if err != nil {
		return WorkflowLegacyRunDisposition{}, fmt.Errorf("record legacy workflow disposition: reload audit: %w", err)
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return WorkflowLegacyRunDisposition{}, fmt.Errorf("record legacy workflow disposition: commit: %w", commitErr)
	}
	return disposition, nil
}

func terminateLegacyWorkflowRun(ctx context.Context, tx *sql.Tx, runID, finalStatus, reason string, at time.Time) error {
	stepStatus := "failed"
	isError := 1
	if finalStatus == "canceled" {
		stepStatus = "skipped"
		isError = 0
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE workflow_run_steps
SET status = ?, is_error = ?, error = ?, completed_at = ?, updated_at = ?
WHERE workflow_run_id = ?
  AND status IN ('pending','running','waiting_on_gate','waiting_on_flex','waiting_on_loop')`,
		stepStatus, isError, reason, workflowCutoverTime(at), workflowCutoverTime(at), runID); err != nil {
		return fmt.Errorf("record legacy workflow disposition: close unfinished steps: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE workflow_runs
SET status = ?, error = ?, completed_at = ?, updated_at = ?
WHERE id = ?`, finalStatus, reason, workflowCutoverTime(at), workflowCutoverTime(at), runID); err != nil {
		return fmt.Errorf("record legacy workflow disposition: terminate run: %w", err)
	}
	return nil
}

func activeLegacyWorkflowStatus(status string) bool {
	switch status {
	case "running", "waiting_on_gate", "waiting_on_flex", "waiting_on_loop":
		return true
	default:
		return false
	}
}

func terminalLegacyWorkflowStatus(status string) bool {
	switch status {
	case "completed", "failed", "canceled":
		return true
	default:
		return false
	}
}

func workflowCutoverTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
