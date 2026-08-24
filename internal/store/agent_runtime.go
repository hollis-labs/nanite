package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
)

// AgentRuntimeRow is the persisted lifecycle row internal/runtime/agent.Boot
// writes for every spawned process. The runtime package treats
// internal/runtime/agent.RuntimeStore as the contract; this row is the
// production backing.
//
// State machine:
//
//	launching → running → done|failed|orphaned
//
// `orphaned` is set by orphansweep.RuntimeReaper.SweepOnce at daemon bootstrap when a persisted
// PID is no longer alive.
type AgentRuntimeRow struct {
	ID                string
	AgentProfile      string
	Provider          string
	RuntimeKind       string
	Mode              string
	Workdir           string
	State             string
	PID               int
	ParentSessionID   string
	ProviderSessionID string
	MetaJSON          string
	FailureReason     string
	StartedAt         time.Time
	UpdatedAt         time.Time
}

// AgentRuntimeCheckpoint is the resume payload ModeResume callers consult.
// Storage lands once the lib surfaces a Manager-level Checkpoint API; until
// then GetAgentRuntimeCheckpoint returns ErrAgentRuntimeCheckpointNotFound.
type AgentRuntimeCheckpoint struct {
	ID                string
	RuntimeID         string
	ProviderSessionID string
	CapturedAt        time.Time
}

// ErrAgentRuntimeCheckpointNotFound signals an absent or unknown
// checkpoint id. Callers should surface this as a clean "no checkpoint"
// rather than a corrupt-store error.
var ErrAgentRuntimeCheckpointNotFound = errors.New("agent_runtime_checkpoint not found")

const agentRuntimeColumns = `id, agent_profile, provider, runtime_kind, mode, workdir, state, pid,
    parent_session_id, provider_session_id, meta_json, failure_reason, started_at, updated_at`

// CreateAgentRuntimeRow persists a launching-state row for a Boot call.
// Idempotent on conflict: existing id triggers an update of the mutable
// fields rather than failing — Boot may be retried on the same id when
// transient setup errors get cleared upstream.
func (s *Store) CreateAgentRuntimeRow(ctx context.Context, row *AgentRuntimeRow) error {
	if row == nil {
		return errors.New("CreateAgentRuntimeRow: nil row")
	}
	if row.ID == "" {
		return errors.New("CreateAgentRuntimeRow: empty id")
	}
	if row.StartedAt.IsZero() {
		row.StartedAt = time.Now().UTC()
	}
	row.UpdatedAt = row.StartedAt
	if row.MetaJSON == "" {
		row.MetaJSON = "{}"
	}
	if row.RuntimeKind == "" {
		row.RuntimeKind = "unknown"
	}

	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO agent_runtime (`+agentRuntimeColumns+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		     agent_profile = excluded.agent_profile,
		     provider = excluded.provider,
		     runtime_kind = excluded.runtime_kind,
		     mode = excluded.mode,
		     workdir = excluded.workdir,
		     state = excluded.state,
		     pid = excluded.pid,
		     parent_session_id = excluded.parent_session_id,
		     provider_session_id = excluded.provider_session_id,
		     meta_json = excluded.meta_json,
		     failure_reason = excluded.failure_reason,
		     updated_at = excluded.updated_at`,
		row.ID, row.AgentProfile, row.Provider, row.RuntimeKind, row.Mode, row.Workdir,
		row.State, row.PID, row.ParentSessionID, row.ProviderSessionID,
		row.MetaJSON, row.FailureReason,
		row.StartedAt.UTC().Format(time.RFC3339Nano),
		row.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("create agent_runtime row %s: %w", row.ID, err)
	}
	return nil
}

// MarkAgentRuntimeFailed transitions row.id to state="failed" with reason.
// No-op on unknown id — prevents ghost rows from blocking happy-path Stop.
func (s *Store) MarkAgentRuntimeFailed(ctx context.Context, id, reason string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE agent_runtime SET state = 'failed', failure_reason = ?, updated_at = ? WHERE id = ?`,
		reason, time.Now().UTC().Format(time.RFC3339Nano), id,
	)
	if err != nil {
		return fmt.Errorf("mark agent_runtime failed %s: %w", id, err)
	}
	return nil
}

// SetAgentRuntimeProviderSessionID records the adapter-supplied session id
// (claude's session_id, codex's, etc.) as soon as the runtime reports it.
func (s *Store) SetAgentRuntimeProviderSessionID(ctx context.Context, id, providerSessionID string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE agent_runtime SET provider_session_id = ?, updated_at = ? WHERE id = ?`,
		providerSessionID, time.Now().UTC().Format(time.RFC3339Nano), id,
	)
	if err != nil {
		return fmt.Errorf("set agent_runtime provider_session_id %s: %w", id, err)
	}
	return nil
}

// AgentRuntimeProviderSessionID returns the provider_session_id captured on the
// session's runtime row (keyed by id == chat session id), or "" when there is
// no row or none captured. CW-20260525-0001 Slice 3 reads this to resume a CLI
// provider session after a host restart: the agent_runtime row persists (the
// reaper only marks it orphaned), so the captured provider session id survives.
// MUST be read BEFORE re-boot — CreateRuntimeRow upserts and clears the column.
func (s *Store) AgentRuntimeProviderSessionID(ctx context.Context, id string) (string, error) {
	var providerSessionID string
	err := s.DB.QueryRowContext(ctx,
		`SELECT COALESCE(provider_session_id, '') FROM agent_runtime WHERE id = ?`, id,
	).Scan(&providerSessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get agent_runtime provider_session_id %s: %w", id, err)
	}
	return providerSessionID, nil
}

// SetAgentRuntimeState transitions the row's state column. Used by the
// state-sink adapter wired into agentsessions.Manager — every lib-emitted
// state event (launching → running → done|failed) lands here.
func (s *Store) SetAgentRuntimeState(ctx context.Context, id, state string, pid int) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE agent_runtime SET state = ?, pid = ?, updated_at = ? WHERE id = ?`,
		state, pid, time.Now().UTC().Format(time.RFC3339Nano), id,
	)
	if err != nil {
		return fmt.Errorf("set agent_runtime state %s: %w", id, err)
	}
	return nil
}

// MarkAgentRuntimeOrphaned transitions the row to state="orphaned" with reason.
// orphansweep.RuntimeReaper.SweepOnce calls this at daemon bootstrap for rows whose persisted PID
// is no longer alive.
func (s *Store) MarkAgentRuntimeOrphaned(ctx context.Context, id, reason string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE agent_runtime SET state = 'orphaned', failure_reason = ?, updated_at = ? WHERE id = ?`,
		reason, time.Now().UTC().Format(time.RFC3339Nano), id,
	)
	if err != nil {
		return fmt.Errorf("mark agent_runtime orphaned %s: %w", id, err)
	}
	return nil
}

// ListRunningAgentRuntimeRows returns rows in launching/running state for
// orphansweep.RuntimeReaper.SweepOnce reconciliation at daemon bootstrap.
func (s *Store) ListRunningAgentRuntimeRows(ctx context.Context) ([]*AgentRuntimeRow, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+agentRuntimeColumns+` FROM agent_runtime
		 WHERE state IN ('launching','running') ORDER BY started_at`,
	)
	if err != nil {
		return nil, fmt.Errorf("list running agent_runtime: %w", err)
	}
	defer closeRows(rows)

	var out []*AgentRuntimeRow
	for rows.Next() {
		row, err := scanAgentRuntimeRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) ListAgentRuntimeRowsForSession(ctx context.Context, sessionID string) ([]*AgentRuntimeRow, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+agentRuntimeColumns+` FROM agent_runtime
		 WHERE parent_session_id = ? ORDER BY started_at DESC`, sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("list agent_runtime for session %s: %w", sessionID, err)
	}
	defer closeRows(rows)

	var out []*AgentRuntimeRow
	for rows.Next() {
		row, err := scanAgentRuntimeRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// SaveAgentRuntimeCheckpoint captures a checkpoint row for a runtime by
// snapshotting the provider_session_id currently sitting on the
// agent_runtime row. It returns the generated checkpoint id.
//
// SPIKE HACK — CW-20260519-0046 (DAR Track C, Task 4). The migration
// comment on agent_runtime_checkpoints says the table stays empty "until a
// Manager-level Checkpoint API lands in go-agent-sessions". The spike
// deliberately bypasses that: the durable context is already Claude's own
// `--resume <session-id>` conversation, and provider_session_id is captured
// live via the OnSessionID callback (SetAgentRuntimeProviderSessionID). A
// checkpoint therefore reduces to snapshotting that id into a row that
// ModeResume can later resolve via GetAgentRuntimeCheckpoint — no
// context (re)serialization, and no new lib API.
//
// A caller that checkpoints before the first turn has produced a session
// id will write an empty provider_session_id; that is a degenerate (not
// resumable) checkpoint, not an error — the caller is expected to drive at
// least one turn first (see spike-task-breakdown.md §5 "capture timing").
func (s *Store) SaveAgentRuntimeCheckpoint(ctx context.Context, runtimeID string) (string, error) {
	if runtimeID == "" {
		return "", errors.New("SaveAgentRuntimeCheckpoint: empty runtimeID")
	}
	var providerSessionID string
	err := s.DB.QueryRowContext(ctx,
		`SELECT provider_session_id FROM agent_runtime WHERE id = ?`, runtimeID,
	).Scan(&providerSessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("SaveAgentRuntimeCheckpoint: unknown runtime %s", runtimeID)
	}
	if err != nil {
		return "", fmt.Errorf("SaveAgentRuntimeCheckpoint: read runtime %s: %w", runtimeID, err)
	}
	checkpointID := ulid.Make().String()
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO agent_runtime_checkpoints (id, runtime_id, provider_session_id, captured_at)
		 VALUES (?, ?, ?, ?)`,
		checkpointID, runtimeID, providerSessionID,
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return "", fmt.Errorf("SaveAgentRuntimeCheckpoint: insert checkpoint for %s: %w", runtimeID, err)
	}
	return checkpointID, nil
}

// GetAgentRuntimeCheckpoint loads the resume payload for a checkpoint id.
// Returns ErrAgentRuntimeCheckpointNotFound when the id is unknown.
func (s *Store) GetAgentRuntimeCheckpoint(ctx context.Context, id string) (*AgentRuntimeCheckpoint, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, runtime_id, provider_session_id, captured_at
		 FROM agent_runtime_checkpoints WHERE id = ?`, id,
	)
	cp := &AgentRuntimeCheckpoint{}
	var capturedAt string
	err := row.Scan(&cp.ID, &cp.RuntimeID, &cp.ProviderSessionID, &capturedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAgentRuntimeCheckpointNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get agent_runtime_checkpoint %s: %w", id, err)
	}
	if t, perr := time.Parse(time.RFC3339Nano, capturedAt); perr == nil {
		cp.CapturedAt = t
	}
	return cp, nil
}

// GetCheckpointBootDir resolves the boot dir (= the originating
// agent_runtime.workdir) of the run a checkpoint was captured from.
//
// SPIKE HACK — CW-20260519-0047 (DAR Track C, Task 5). `claude --resume`
// locates a saved conversation by the CLI's project dir, which is its
// spawn cwd. For a claude launch, agent_runtime.workdir IS the boot dir
// (claudeLayout.SpawnWorkdir returns it). A ModeResume launch must
// therefore re-pin its boot dir to this path or `claude --resume` fails
// with "No conversation found". Returns the workdir for the checkpoint's
// originating runtime; ErrAgentRuntimeCheckpointNotFound when the
// checkpoint id is unknown.
func (s *Store) GetCheckpointBootDir(ctx context.Context, checkpointID string) (string, error) {
	if checkpointID == "" {
		return "", errors.New("GetCheckpointBootDir: empty checkpointID")
	}
	var workdir string
	err := s.DB.QueryRowContext(ctx,
		`SELECT r.workdir
		   FROM agent_runtime_checkpoints c
		   JOIN agent_runtime r ON r.id = c.runtime_id
		  WHERE c.id = ?`, checkpointID,
	).Scan(&workdir)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrAgentRuntimeCheckpointNotFound
	}
	if err != nil {
		return "", fmt.Errorf("GetCheckpointBootDir %s: %w", checkpointID, err)
	}
	return workdir, nil
}

// scanAgentRuntimeRow projects a row into AgentRuntimeRow. Times are parsed
// as RFC3339Nano; malformed timestamps fall back to zero rather than
// failing the whole sweep.
func scanAgentRuntimeRow(scanner interface{ Scan(...any) error }) (*AgentRuntimeRow, error) {
	r := &AgentRuntimeRow{}
	var startedAt, updatedAt string
	err := scanner.Scan(
		&r.ID, &r.AgentProfile, &r.Provider, &r.RuntimeKind, &r.Mode, &r.Workdir, &r.State, &r.PID,
		&r.ParentSessionID, &r.ProviderSessionID, &r.MetaJSON, &r.FailureReason,
		&startedAt, &updatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan agent_runtime row: %w", err)
	}
	if t, perr := time.Parse(time.RFC3339Nano, startedAt); perr == nil {
		r.StartedAt = t
	}
	if t, perr := time.Parse(time.RFC3339Nano, updatedAt); perr == nil {
		r.UpdatedAt = t
	}
	return r, nil
}

// MetaMap projects MetaJSON into a map. Returns an empty map when the
// JSON is empty or malformed; never errors. Convenience for callers that
// want structured access without rolling their own decoder.
func (r *AgentRuntimeRow) MetaMap() map[string]any {
	out := map[string]any{}
	if r.MetaJSON == "" {
		return out
	}
	_ = json.Unmarshal([]byte(r.MetaJSON), &out)
	return out
}
