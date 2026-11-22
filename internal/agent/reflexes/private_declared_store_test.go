package reflexes

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
	"github.com/oklog/ulid/v2"
)

// This private declared catalog exists only in tests. It is deliberately stored
// outside the retained historical behavior tables and cannot be selected by any
// runtime API. It supplies authored candidate and persistence ports to pure
// resolver/filter/trace qualification; it is not a verifier or enrollment port.
type privateFixtureStore struct {
	*store.Store
	gate func(store.AgentReflex) bool
}

var privateFixtureMu sync.Mutex
var privateFixtureStores = map[*store.Store]*privateFixtureStore{}

func declaredFixture(st *store.Store) *privateFixtureStore {
	privateFixtureMu.Lock()
	defer privateFixtureMu.Unlock()
	if f := privateFixtureStores[st]; f != nil {
		return f
	}
	for _, q := range []string{
		"CREATE TABLE IF NOT EXISTS private_declared_reflexes AS SELECT * FROM agent_reflexes WHERE 0",
		"CREATE UNIQUE INDEX IF NOT EXISTS private_declared_id ON private_declared_reflexes(id)",
		"CREATE TABLE IF NOT EXISTS private_declared_opt_outs AS SELECT * FROM agent_reflex_opt_outs WHERE 0",
		"CREATE UNIQUE INDEX IF NOT EXISTS private_declared_opt_out_id ON private_declared_opt_outs(agent_id,reflex_id)",
	} {
		if _, err := st.DB.Exec(q); err != nil {
			panic(err)
		}
	}
	f := &privateFixtureStore{Store: st}
	privateFixtureStores[st] = f
	return f
}
func (s *privateFixtureStore) CreateAgent(ctx context.Context, p *store.AgentProfile) error {
	return storetest.HistoricalProfile(ctx, s.Store, p)
}
func (s *privateFixtureStore) SetPluginReflexGate(g func(store.AgentReflex) bool) { s.gate = g }
func (s *privateFixtureStore) pluginReflexAvailable(r store.AgentReflex) bool {
	return r.ProvenanceTier != "plugin" || s.gate != nil && s.gate(r)
}
func nullIfEmpty(v string) any {
	if v == "" {
		return nil
	}
	return v
}

const agentReflexColumns = `id, COALESCE(agent_id,''), COALESCE(class_tag,''), name, trigger_kind, trigger_spec, action_kind, action_spec, status, priority, fired_count, COALESCE(last_fired_at,''), created_at, created_by, opt_out_allowed, provenance_tier, recurrence_override_seconds, COALESCE(workflow_run_id,'')`

func privateFixtureCandidates(ctx context.Context, st *store.Store, actor, class, run, loop string) ([]store.AgentReflex, error) {
	f := declaredFixture(st)
	if loop != "" {
		return f.ListAgentReflexesForLoopRun(ctx, loop)
	}
	if run != "" {
		return f.ListAgentReflexesForWorkflowRun(ctx, run, actor, class)
	}
	return f.ListAgentReflexesForAgent(ctx, actor, class)
}

type privateFixtureTrace struct{ *store.Store }

func (s privateFixtureTrace) BumpAgentReflexFired(ctx context.Context, id string, now time.Time) error {
	return declaredFixture(s.Store).BumpAgentReflexFired(ctx, id, now)
}

func scanAgentReflex(scanner interface{ Scan(...any) error }, r *store.AgentReflex) error {
	var recurrenceOverride sql.NullInt64
	if err := scanner.Scan(
		&r.ID, &r.AgentID, &r.ClassTag, &r.Name,
		&r.TriggerKind, &r.TriggerSpec, &r.ActionKind, &r.ActionSpec,
		&r.Status, &r.Priority, &r.FiredCount, &r.LastFiredAt,
		&r.CreatedAt, &r.CreatedBy, &r.OptOutAllowed,
		&r.ProvenanceTier, &recurrenceOverride,
		&r.WorkflowRunID,
	); err != nil {
		return err
	}
	r.RecurrenceOverrideSeconds = nil
	if recurrenceOverride.Valid {
		v := recurrenceOverride.Int64
		r.RecurrenceOverrideSeconds = &v
	}
	return nil
}

func nullIfNilInt64(v *int64) interface{} {
	if v == nil {
		return nil
	}
	return *v
}

func (s *privateFixtureStore) InsertAgentReflex(ctx context.Context, row store.AgentReflex) (string, error) {
	if row.Name == "" {
		return "", fmt.Errorf("insert private_declared_reflexes: name is required")
	}
	if row.TriggerKind == "" {
		return "", fmt.Errorf("insert private_declared_reflexes: trigger_kind is required")
	}
	if row.TriggerSpec == "" {
		return "", fmt.Errorf("insert private_declared_reflexes: trigger_spec is required")
	}
	if row.ActionKind == "" {
		return "", fmt.Errorf("insert private_declared_reflexes: action_kind is required")
	}
	if row.ActionSpec == "" {
		return "", fmt.Errorf("insert private_declared_reflexes: action_spec is required")
	}
	if row.Status == "" {
		row.Status = store.ReflexStatusActive
	}
	if row.CreatedBy == "" {
		row.CreatedBy = "operator"
	}
	if row.ID == "" {
		row.ID = "rfx-" + ulid.Make().String()
	}
	if row.ProvenanceTier == "" {

		if row.CreatedBy == "system" {
			row.ProvenanceTier = "system"
		} else {
			row.ProvenanceTier = "operator"
		}
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT OR REPLACE INTO private_declared_reflexes
		    (id, agent_id, class_tag, name, trigger_kind, trigger_spec,
		     action_kind, action_spec, status, priority, fired_count,
		     last_fired_at, created_at, created_by, opt_out_allowed,
		     provenance_tier, recurrence_override_seconds, workflow_run_id)
		 VALUES (?, ?, ?, ?, ?, ?,
		         ?, ?, ?, ?, ?,
		         ?,
		         COALESCE(NULLIF(?, ''), datetime('now')),
		         ?, ?,
		         ?, ?, ?)`,
		row.ID, nullIfEmpty(row.AgentID), nullIfEmpty(row.ClassTag),
		row.Name, row.TriggerKind, row.TriggerSpec,
		row.ActionKind, row.ActionSpec, row.Status, row.Priority, row.FiredCount,
		nullIfEmpty(row.LastFiredAt),
		row.CreatedAt,
		row.CreatedBy, row.OptOutAllowed,
		row.ProvenanceTier, nullIfNilInt64(row.RecurrenceOverrideSeconds),
		nullIfEmpty(row.WorkflowRunID),
	)
	if err != nil {
		return "", fmt.Errorf("insert private_declared_reflexes: %w", err)
	}
	return row.ID, nil
}

func (s *privateFixtureStore) GetAgentReflex(ctx context.Context, id string) (*store.AgentReflex, error) {
	var out store.AgentReflex
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+agentReflexColumns+` FROM private_declared_reflexes WHERE id = ?`, id,
	)
	if err := scanAgentReflex(row, &out); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrAgentReflexNotFound
		}
		return nil, fmt.Errorf("get private_declared_reflexes: %w", err)
	}
	return &out, nil
}

func (s *privateFixtureStore) ListAgentReflexesForAgent(ctx context.Context, agentID, classTag string) ([]store.AgentReflex, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+agentReflexColumns+`
		 FROM private_declared_reflexes
		 WHERE status = 'active'
		   AND (
		         (
		           agent_id IS NULL AND class_tag = ?
		           AND (
		                 opt_out_allowed = 0
		              OR NOT EXISTS (
		                   SELECT 1 FROM private_declared_opt_outs o
		                    WHERE o.agent_id = ? AND o.reflex_id = private_declared_reflexes.id
		                 )
		               )
		         )
		      OR (agent_id = ? AND (
		            provenance_tier != 'plugin' OR opt_out_allowed = 0 OR NOT EXISTS (
		              SELECT 1 FROM private_declared_opt_outs o
		               WHERE o.agent_id = ? AND o.reflex_id = private_declared_reflexes.id
		            )
		          ))
		       )
		 ORDER BY priority DESC, created_at ASC`,
		classTag, agentID, agentID, agentID,
	)
	if err != nil {
		return nil, fmt.Errorf("list private_declared_reflexes: %w", err)
	}
	defer rows.Close()
	out := make([]store.AgentReflex, 0)
	for rows.Next() {
		var r store.AgentReflex
		if err := scanAgentReflex(rows, &r); err != nil {
			return nil, fmt.Errorf("scan private_declared_reflexes: %w", err)
		}
		if s.pluginReflexAvailable(r) {
			out = append(out, r)
		}
	}
	return out, rows.Err()
}

func (s *privateFixtureStore) ListAgentReflexesForWorkflowRun(ctx context.Context, runID, agentID, classTag string) ([]store.AgentReflex, error) {
	if runID == "" {
		return nil, nil
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+agentReflexColumns+`
		 FROM private_declared_reflexes
		 WHERE status = 'active'
		   AND workflow_run_id = ?
		   AND (
		         (
		           agent_id IS NULL AND class_tag = ?
		           AND (
		                 opt_out_allowed = 0
		              OR NOT EXISTS (
		                   SELECT 1 FROM private_declared_opt_outs o
		                    WHERE o.agent_id = ? AND o.reflex_id = private_declared_reflexes.id
		                 )
		               )
		         )
		      OR (agent_id = ? AND (
		            provenance_tier != 'plugin' OR opt_out_allowed = 0 OR NOT EXISTS (
		              SELECT 1 FROM private_declared_opt_outs o
		               WHERE o.agent_id = ? AND o.reflex_id = private_declared_reflexes.id
		            )
		          ))
		       )
		 ORDER BY priority DESC, created_at ASC`,
		runID, classTag, agentID, agentID, agentID,
	)
	if err != nil {
		return nil, fmt.Errorf("list private_declared_reflexes for workflow run: %w", err)
	}
	defer rows.Close()
	out := make([]store.AgentReflex, 0)
	for rows.Next() {
		var r store.AgentReflex
		if err := scanAgentReflex(rows, &r); err != nil {
			return nil, fmt.Errorf("scan private_declared_reflexes: %w", err)
		}
		if s.pluginReflexAvailable(r) {
			out = append(out, r)
		}
	}
	return out, rows.Err()
}

func (s *privateFixtureStore) ListAgentReflexesForLoopRun(ctx context.Context, loopRunID string) ([]store.AgentReflex, error) {
	if loopRunID == "" {
		return nil, nil
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+agentReflexColumns+`
		 FROM private_declared_reflexes
		 WHERE status = 'active'
		   AND action_kind = ?
		   AND json_extract(action_spec, '$.loop_run_id') = ?
		 ORDER BY priority DESC, created_at ASC`,
		store.ReflexActionResumeLoopRun, loopRunID,
	)
	if err != nil {
		return nil, fmt.Errorf("list private_declared_reflexes for loop run: %w", err)
	}
	defer rows.Close()
	out := make([]store.AgentReflex, 0)
	for rows.Next() {
		var r store.AgentReflex
		if err := scanAgentReflex(rows, &r); err != nil {
			return nil, fmt.Errorf("scan private_declared_reflexes: %w", err)
		}
		if s.pluginReflexAvailable(r) {
			out = append(out, r)
		}
	}
	return out, rows.Err()
}

func (s *privateFixtureStore) ListAllAgentReflexes(ctx context.Context, agentID string) ([]store.AgentReflex, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+agentReflexColumns+`
		 FROM private_declared_reflexes
		 WHERE agent_id = ?
		 ORDER BY priority DESC, created_at ASC`,
		agentID,
	)
	if err != nil {
		return nil, fmt.Errorf("list all private_declared_reflexes: %w", err)
	}
	defer rows.Close()
	out := make([]store.AgentReflex, 0)
	for rows.Next() {
		var r store.AgentReflex
		if err := scanAgentReflex(rows, &r); err != nil {
			return nil, fmt.Errorf("scan private_declared_reflexes: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *privateFixtureStore) UpdateAgentReflex(ctx context.Context, row store.AgentReflex) error {
	if row.ID == "" {
		return fmt.Errorf("update private_declared_reflexes: id is required")
	}
	res, err := s.DB.ExecContext(ctx,
		`UPDATE private_declared_reflexes
		    SET name = ?,
		        trigger_kind = ?,
		        trigger_spec = ?,
		        action_kind = ?,
		        action_spec = ?,
		        status = ?,
		        priority = ?,
		        fired_count = ?,
		        last_fired_at = ?,
		        opt_out_allowed = ?,
		        recurrence_override_seconds = ?
		  WHERE id = ?`,
		row.Name, row.TriggerKind, row.TriggerSpec,
		row.ActionKind, row.ActionSpec,
		row.Status, row.Priority, row.FiredCount,
		nullIfEmpty(row.LastFiredAt), row.OptOutAllowed,
		nullIfNilInt64(row.RecurrenceOverrideSeconds), row.ID,
	)
	if err != nil {
		return fmt.Errorf("update private_declared_reflexes: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update private_declared_reflexes rows affected: %w", err)
	}
	if n == 0 {
		return store.ErrAgentReflexNotFound
	}
	return nil
}

func (s *privateFixtureStore) BumpAgentReflexFired(ctx context.Context, id string, now time.Time) error {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE private_declared_reflexes
		    SET fired_count = fired_count + 1,
		        last_fired_at = ?
		  WHERE id = ?`,
		now.UTC().Format(time.RFC3339), id,
	)
	if err != nil {
		return fmt.Errorf("bump private_declared_reflexes fired: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("bump private_declared_reflexes rows: %w", err)
	}
	if n == 0 {
		return store.ErrAgentReflexNotFound
	}
	return nil
}

func (s *privateFixtureStore) SetAgentReflexOptOut(ctx context.Context, agentID, reflexID string) error {
	if agentID == "" || reflexID == "" {
		return fmt.Errorf("set private_declared_opt_outs: agent_id and reflex_id are required")
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT OR IGNORE INTO private_declared_opt_outs (agent_id, reflex_id) VALUES (?, ?)`,
		agentID, reflexID,
	)
	if err != nil {
		return fmt.Errorf("set private_declared_opt_outs: %w", err)
	}
	return nil
}

func (s *privateFixtureStore) ClearAgentReflexOptOut(ctx context.Context, agentID, reflexID string) error {
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM private_declared_opt_outs WHERE agent_id = ? AND reflex_id = ?`,
		agentID, reflexID,
	)
	if err != nil {
		return fmt.Errorf("clear private_declared_opt_outs: %w", err)
	}
	return nil
}
