package store

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// Historical migration fixtures inspect the retained graph directly. These
// test-only ports never turn a legacy row into a runtime selection or authority.
func migrationHistoricalAgentBySlug(ctx context.Context, s *Store, slug string) (*AgentProfile, error) {
	var id string
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM agent_profiles WHERE slug=?`, slug).Scan(&id); err != nil {
		return nil, err
	}
	return s.GetHistoricalAgentProfile(ctx, id)
}
func migrationHistoricalReflex(ctx context.Context, s *Store, id string) (*AgentReflex, error) {
	var row AgentReflex
	err := scanAgentReflex(s.DB.QueryRowContext(ctx, `SELECT `+agentReflexColumns+` FROM agent_reflexes WHERE id=?`, id), &row)
	return &row, err
}
func migrationInsertHistoricalReflex(ctx context.Context, s *Store, row AgentReflex) (string, error) {
	if row.ID == "" {
		row.ID = uuid.NewString()
	}
	if row.CreatedBy == "" {
		row.CreatedBy = "operator:private-test"
	}
	if row.ProvenanceTier == "" {
		row.ProvenanceTier = "operator"
		if row.CreatedBy == "system" {
			row.ProvenanceTier = "system"
		}
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO agent_reflexes(id,agent_id,class_tag,name,trigger_kind,trigger_spec,action_kind,action_spec,created_by,provenance_tier,opt_out_allowed) VALUES(?,NULLIF(?,''),NULLIF(?,''),?,?,?,?,?,?,?,?)`, row.ID, row.AgentID, row.ClassTag, row.Name, row.TriggerKind, row.TriggerSpec, row.ActionKind, row.ActionSpec, row.CreatedBy, row.ProvenanceTier, row.OptOutAllowed)
	return row.ID, err
}
func migrationHistoricalOptOut(ctx context.Context, s *Store, actor, id string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO agent_reflex_opt_outs(agent_id,reflex_id) VALUES(?,?)`, actor, id)
	return err
}
func migrationHistoricalOptOuts(ctx context.Context, s *Store, actor string) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT reflex_id FROM agent_reflex_opt_outs WHERE agent_id=? ORDER BY reflex_id`, actor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
func migrationHistoricalProfile(t *testing.T, s *Store, p *AgentProfile) {
	t.Helper()
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	if _, err := s.DB.ExecContext(t.Context(), `INSERT INTO agent_profiles(id,name,slug,system_prompt,source) VALUES(?,?,?,?,?)`, p.ID, p.Name, p.Slug, p.SystemPrompt, p.Source); err != nil {
		t.Fatal(err)
	}
}

func migrationInsertHistoricalSchedule(ctx context.Context, s *Store, row AgentSchedule) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO agent_schedules(id,agent_id,name,body,schedule_kind,schedule_spec,max_retries,on_fail,job_type,job_payload,next_run) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, row.ID, row.AgentID, row.Name, row.Body, row.ScheduleKind, row.ScheduleSpec, row.MaxRetries, row.OnFail, row.JobType, row.JobPayload, row.NextRun)
	return err
}
func migrationHistoricalSchedule(ctx context.Context, s *Store, id string) (*AgentSchedule, error) {
	var row AgentSchedule
	err := scanAgentSchedule(s.DB.QueryRowContext(ctx, `SELECT `+agentScheduleColumns+` FROM agent_schedules WHERE id=?`, id), &row)
	return &row, err
}
func migrationHistoricalFire(ctx context.Context, s *Store, id string) (*ScheduleFire, error) {
	var row ScheduleFire
	err := scanScheduleFire(s.DB.QueryRowContext(ctx, `SELECT `+scheduleFireColumns+` FROM schedule_runs WHERE run_id=?`, id), &row)
	return &row, err
}
