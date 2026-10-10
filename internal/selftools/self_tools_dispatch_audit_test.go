package selftools

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/dispatch"
	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
	subagenthost "github.com/hollis-labs/nanite/internal/subagent"
	"github.com/hollis-labs/substrate/agent/subagent"
)

func TestCallExecuteTask_ReflexMatch_EmitsUnifiedTraceRecord(t *testing.T) {
	s := newTestStore(t)
	historical := retainedSelftoolsDispatchProfile(t, s, "retained-dispatch-audit")
	retainedSelftoolsDispatchRule(t, s, store.AgentReflex{Name: "retained-audit-route", AgentID: historical.ID, ActionSpec: `{"agent_slug":"retained-target","confidence":0.99,"reason":"historical matching route"}`, FiredCount: 11})
	if _, err := s.DB.ExecContext(t.Context(), `INSERT INTO event_log(session_id,event_type,category,detail,metadata) VALUES(?,'dispatch_to_agent','reflex','retained firing','{"private":"history"}')`, "sess-audit-1"); err != nil {
		t.Fatal(err)
	}
	emitter := &gatedApprovalEmitter{}
	svc := subagenthost.NewService(s.DB, &inertDispatchRunner{t: t}, nil, emitter, s)
	spawner := &heldDispatchSpawner{t: t, svc: svc}
	st := newTestSelfToolsTransport(s)
	st.Dispatch = spawner
	before := selftoolsDispatchSnapshot(t, s)
	res, err := st.callExecuteTask(mcp.WithCallerProfile(t.Context(), historical.ID), map[string]any{"session_id": "sess-audit-1", "message": "Implement the reflex matcher module"})
	if err != nil || res == nil || !res.IsError {
		t.Fatalf("task_execute = %+v, %v; want host issuer refusal", res, err)
	}
	raw, err := json.Marshal(res)
	if err != nil || !strings.Contains(string(raw), store.ErrVerifiedActorRequired.Error()) {
		t.Fatalf("wire result lost actual issuer refusal: %s, %v", raw, err)
	}
	if spawner.calls != 1 || !errors.Is(spawner.err, store.ErrVerifiedActorRequired) {
		t.Fatalf("actual host dispatch boundary: calls=%d err=%v", spawner.calls, spawner.err)
	}
	if spawner.request.Role == "retained-target" {
		t.Fatal("historical rule selected dispatch target")
	}
	if emitter.count != 0 {
		t.Fatalf("refused task emitted %d approvals", emitter.count)
	}
	requireSelftoolsDispatchUnchanged(t, s, before)
}

// This adapter reaches the real host refusal. It supplies no spawn authorization.
type heldDispatchSpawner struct {
	t       *testing.T
	svc     *subagent.Service
	calls   int
	request dispatch.SpawnRequest
	err     error
}

func (s *heldDispatchSpawner) Spawn(ctx context.Context, req dispatch.SpawnRequest) (*dispatch.SpawnResult, error) {
	s.calls++
	s.request = req
	id, err := s.svc.Spawn(ctx, subagent.SpawnRequest{ParentSessionID: req.ParentSessionID, ParentAgentID: req.ParentAgentID, AgentProfileID: req.AgentProfileID, Role: req.Role, Prompt: req.Prompt, Mode: req.Mode, Provider: req.Provider, TimeoutSeconds: req.TimeoutSeconds})
	s.err = err
	if err == nil {
		s.t.Fatalf("unavailable host issued run %q", id)
	}
	return nil, err
}

type inertDispatchRunner struct{ t *testing.T }

func (r *inertDispatchRunner) Run(context.Context, *subagent.Run) (*subagent.Result, error) {
	r.t.Fatal("retained dispatch cannot invoke a runner")
	return nil, nil
}

func retainedSelftoolsDispatchProfile(t *testing.T, s *store.Store, slug string) *store.AgentProfile {
	t.Helper()
	p := &store.AgentProfile{Name: "Retained dispatch profile", Slug: slug, SystemPrompt: "Private retained body", Source: "internal"}
	// Historical rows are private audit fixtures, never enrollment or runtime authority.
	if err := storetest.HistoricalProfile(t.Context(), s, p); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(t.Context(), `UPDATE agent_profiles SET class='advisor',default_trust_tier='trusted' WHERE id=?`, p.ID); err != nil {
		t.Fatal(err)
	}
	return p
}

func retainedSelftoolsDispatchRule(t *testing.T, s *store.Store, row store.AgentReflex) {
	t.Helper()
	if row.ID == "" {
		row.ID = "retained-" + row.Name
	}
	if row.ClassTag == "" {
		row.ClassTag = "advisor"
	}
	if row.TriggerSpec == "" {
		row.TriggerSpec = `{"kind":"user_regex_window","window":1,"pattern":".*"}`
	}
	if row.ActionSpec == "" {
		row.ActionSpec = `{"agent_slug":"retained-target","confidence":0.99,"reason":"Private retained rule"}`
	}
	var agentID, runID any
	if row.AgentID != "" {
		agentID = row.AgentID
	}
	if row.WorkflowRunID != "" {
		runID = row.WorkflowRunID
	}
	_, err := s.DB.ExecContext(t.Context(), `INSERT INTO agent_reflexes(id,agent_id,class_tag,name,trigger_kind,trigger_spec,action_kind,action_spec,status,priority,fired_count,last_fired_at,created_at,created_by,opt_out_allowed,provenance_tier,recurrence_override_seconds,workflow_run_id) VALUES(?,?,?,?,'predicate',?,'dispatch_to_agent',?,'active',?,?,?,'2026-09-01','historical-operator',1,'operator',?,?)`, row.ID, agentID, row.ClassTag, row.Name, row.TriggerSpec, row.ActionSpec, row.Priority, row.FiredCount, row.LastFiredAt, row.RecurrenceOverrideSeconds, runID)
	if err != nil {
		t.Fatal(err)
	}
}

func selftoolsDispatchSnapshot(t *testing.T, s *store.Store) map[string][][]any {
	t.Helper()
	out := make(map[string][][]any)
	for _, query := range []string{
		`SELECT * FROM agent_profiles ORDER BY id`, `SELECT * FROM agent_profile_revisions ORDER BY sequence`,
		`SELECT * FROM agent_reflexes ORDER BY id`, `SELECT * FROM agent_reflex_opt_outs ORDER BY agent_id,reflex_id`,
		`SELECT * FROM pending_reflexes ORDER BY id`, `SELECT * FROM team_run_members ORDER BY id`, `SELECT * FROM actor_team_run_members ORDER BY id`,
		`SELECT * FROM agent_definitions ORDER BY definition_id,revision`, `SELECT * FROM agent_host_settings ORDER BY id`,
		`SELECT * FROM agent_actor_bindings ORDER BY actor_uri`, `SELECT * FROM actor_reflex_state ORDER BY actor_uri,bundle_digest,rule_id`,
		`SELECT * FROM subagent_runs ORDER BY id`, `SELECT * FROM agent_messages ORDER BY id`, `SELECT * FROM event_log ORDER BY id`,
	} {
		rows, err := s.DB.QueryContext(t.Context(), query)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		for rows.Next() {
			cells := make([]any, len(columns))
			refs := make([]any, len(columns))
			for i := range cells {
				refs[i] = &cells[i]
			}
			if err := rows.Scan(refs...); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			for i, cell := range cells {
				if raw, ok := cell.([]byte); ok {
					cells[i] = append([]byte(nil), raw...)
				}
			}
			out[query] = append(out[query], cells)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return out
}
func requireSelftoolsDispatchUnchanged(t *testing.T, s *store.Store, before map[string][][]any) {
	t.Helper()
	if after := selftoolsDispatchSnapshot(t, s); !reflect.DeepEqual(before, after) {
		t.Fatalf("retired dispatch changed history or effects: %#v -> %#v", before, after)
	}
}
