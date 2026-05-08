package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
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
// `orphaned` is set by SweepOrphans at daemon bootstrap when a persisted
// PID is no longer alive.
type AgentRuntimeRow struct {
	ID                string
	AgentProfile      string
	Provider          string
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

const agentRuntimeColumns = `id, agent_profile, provider, mode, workdir, state, pid,
    parent_session_id, provider_session_id, meta_json, failure_reason, started_at, updated_at`

// CreateAgentRuntimeRow persists a launching-state row for a Boot call.
// Idempotent on conflict: existing id triggers an update of the mutable
// fields rather than failing — Boot may be retried on the same id when
// transient setup errors get cleared upstream.
func (s *Store) CreateAgentRuntimeRow(row *AgentRuntimeRow) error {
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

	_, err := s.DB.Exec(
		`INSERT INTO agent_runtime (`+agentRuntimeColumns+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		     agent_profile = excluded.agent_profile,
		     provider = excluded.provider,
		     mode = excluded.mode,
		     workdir = excluded.workdir,
		     state = excluded.state,
		     pid = excluded.pid,
		     parent_session_id = excluded.parent_session_id,
		     provider_session_id = excluded.provider_session_id,
		     meta_json = excluded.meta_json,
		     failure_reason = excluded.failure_reason,
		     updated_at = excluded.updated_at`,
		row.ID, row.AgentProfile, row.Provider, row.Mode, row.Workdir,
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
func (s *Store) MarkAgentRuntimeFailed(id, reason string) error {
	_, err := s.DB.Exec(
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
func (s *Store) SetAgentRuntimeProviderSessionID(id, providerSessionID string) error {
	_, err := s.DB.Exec(
		`UPDATE agent_runtime SET provider_session_id = ?, updated_at = ? WHERE id = ?`,
		providerSessionID, time.Now().UTC().Format(time.RFC3339Nano), id,
	)
	if err != nil {
		return fmt.Errorf("set agent_runtime provider_session_id %s: %w", id, err)
	}
	return nil
}

// SetAgentRuntimeState transitions the row's state column. Used by the
// state-sink adapter wired into agentsessions.Manager — every lib-emitted
// state event (launching → running → done|failed) lands here.
func (s *Store) SetAgentRuntimeState(id, state string, pid int) error {
	_, err := s.DB.Exec(
		`UPDATE agent_runtime SET state = ?, pid = ?, updated_at = ? WHERE id = ?`,
		state, pid, time.Now().UTC().Format(time.RFC3339Nano), id,
	)
	if err != nil {
		return fmt.Errorf("set agent_runtime state %s: %w", id, err)
	}
	return nil
}

// MarkAgentRuntimeOrphaned transitions the row to state="orphaned" with reason.
// SweepOrphans calls this at daemon bootstrap for rows whose persisted PID
// is no longer alive.
func (s *Store) MarkAgentRuntimeOrphaned(id, reason string) error {
	_, err := s.DB.Exec(
		`UPDATE agent_runtime SET state = 'orphaned', failure_reason = ?, updated_at = ? WHERE id = ?`,
		reason, time.Now().UTC().Format(time.RFC3339Nano), id,
	)
	if err != nil {
		return fmt.Errorf("mark agent_runtime orphaned %s: %w", id, err)
	}
	return nil
}

// ListRunningAgentRuntimeRows returns rows in launching/running state for
// SweepOrphans reconciliation at daemon bootstrap.
func (s *Store) ListRunningAgentRuntimeRows() ([]*AgentRuntimeRow, error) {
	rows, err := s.DB.Query(
		`SELECT ` + agentRuntimeColumns + ` FROM agent_runtime
		 WHERE state IN ('launching','running') ORDER BY started_at`,
	)
	if err != nil {
		return nil, fmt.Errorf("list running agent_runtime: %w", err)
	}
	defer rows.Close()

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

// GetAgentRuntimeCheckpoint loads the resume payload for a checkpoint id.
// Returns ErrAgentRuntimeCheckpointNotFound when the id is unknown.
func (s *Store) GetAgentRuntimeCheckpoint(id string) (*AgentRuntimeCheckpoint, error) {
	row := s.DB.QueryRow(
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

// scanAgentRuntimeRow projects a row into AgentRuntimeRow. Times are parsed
// as RFC3339Nano; malformed timestamps fall back to zero rather than
// failing the whole sweep.
func scanAgentRuntimeRow(scanner interface{ Scan(...any) error }) (*AgentRuntimeRow, error) {
	r := &AgentRuntimeRow{}
	var startedAt, updatedAt string
	err := scanner.Scan(
		&r.ID, &r.AgentProfile, &r.Provider, &r.Mode, &r.Workdir, &r.State, &r.PID,
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
