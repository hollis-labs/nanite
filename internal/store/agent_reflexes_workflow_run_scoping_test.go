package store

// Regression coverage for TASKS/teams/05-agent-reflexes-run-scoping.md's
// schema piece: migration 131_agent_reflexes_workflow_run_scoping.sql's
// nullable agent_reflexes.workflow_run_id column, and this task's own
// documented provenance_tier judgment call for a Team-authored run-scoped
// dispatch_to_agent row.

import (
	"context"
	"testing"
)

// TestAgentReflex_WorkflowRunID_RoundTrips confirms the new column reads
// back through InsertAgentReflex/GetAgentReflex/ListAgentReflexesForAgent/
// ListAgentReflexesForWorkflowRun exactly as inserted, and that the
// pre-existing "global" shape (WorkflowRunID left empty) is unchanged —
// InsertAgentReflex's nullIfEmpty(row.WorkflowRunID) persists NULL, and
// scanAgentReflex's COALESCE(workflow_run_id,”) reads it back as "".
func TestAgentReflex_WorkflowRunID_RoundTrips(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if err := s.CreateAgent(context.Background(), &AgentProfile{
		ID:           "agent-wfr-roundtrip",
		Name:         "Agent WFR Roundtrip",
		Slug:         "agent-wfr-roundtrip",
		Class:        "advisor",
		SystemPrompt: "test",
		Source:       "test",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO workflow_runs (id, started_at) VALUES (?, datetime('now'))`,
		"run-wfr-roundtrip",
	); err != nil {
		t.Fatalf("insert workflow_runs row: %v", err)
	}

	globalID, err := s.InsertAgentReflex(ctx, AgentReflex{
		ClassTag:    "advisor",
		Name:        "wfr_roundtrip_global",
		TriggerKind: ReflexTriggerEvent,
		TriggerSpec: `{"name":"probe"}`,
		ActionKind:  ReflexActionDispatchToAgent,
		ActionSpec:  `{"agent_slug":"planner","confidence":0.5,"reason":"test"}`,
		// WorkflowRunID left empty — global.
	})
	if err != nil {
		t.Fatalf("InsertAgentReflex (global): %v", err)
	}

	// This task's own documented provenance_tier judgment call (see this
	// task file's Work Log): a Team's routing rules are conceptually
	// "configured by whoever created the Team" — the same trust level the
	// operator's own reflexes already carry (created_by="operator" ->
	// provenance_tier="operator", migration 124's own backfill rule) — not
	// system (reserved for the base reflex seeder, seeds.go/
	// loom_pilot_seeds.go) and not plugin (migration 125 explicitly denies
	// plugin-tier dispatch_to_agent rows — see
	// TestActionKindAllowsProvenanceTier_DispatchToAgent_OperatorTier
	// below for the direct confirmation this passes the allow-list).
	runScopedID, err := s.InsertAgentReflex(ctx, AgentReflex{
		ClassTag:       "advisor",
		Name:           "wfr_roundtrip_run_scoped",
		TriggerKind:    ReflexTriggerEvent,
		TriggerSpec:    `{"name":"probe"}`,
		ActionKind:     ReflexActionDispatchToAgent,
		ActionSpec:     `{"agent_slug":"architect","confidence":0.9,"reason":"team routing rule"}`,
		ProvenanceTier: "operator",
		WorkflowRunID:  "run-wfr-roundtrip",
	})
	if err != nil {
		t.Fatalf("InsertAgentReflex (run-scoped): %v", err)
	}

	global, err := s.GetAgentReflex(ctx, globalID)
	if err != nil {
		t.Fatalf("GetAgentReflex (global): %v", err)
	}
	if global.WorkflowRunID != "" {
		t.Errorf("global row WorkflowRunID = %q, want empty", global.WorkflowRunID)
	}

	runScoped, err := s.GetAgentReflex(ctx, runScopedID)
	if err != nil {
		t.Fatalf("GetAgentReflex (run-scoped): %v", err)
	}
	if runScoped.WorkflowRunID != "run-wfr-roundtrip" {
		t.Errorf("run-scoped row WorkflowRunID = %q, want run-wfr-roundtrip", runScoped.WorkflowRunID)
	}
	if runScoped.ProvenanceTier != "operator" {
		t.Errorf("run-scoped row ProvenanceTier = %q, want operator", runScoped.ProvenanceTier)
	}

	// ListAgentReflexesForAgent (the shared global-candidate query every
	// caller, including Engine.EvaluateState, still uses unmodified) is
	// NOT filtered by workflow_run_id at the SQL level — TASKS/teams/
	// 05-agent-reflexes-run-scoping.md's own documented design call: it
	// returns both rows here, exactly as before this column existed. It
	// is each dispatch_to_agent call site's own job (internal/service/
	// chat_reflex_dispatch.go, internal/selftools/self_tools_dispatch.go)
	// to additionally require WorkflowRunID=="" from this list — proven
	// directly at those call sites' own tests, not re-proven here.
	all, err := s.ListAgentReflexesForAgent(ctx, "", "advisor")
	if err != nil {
		t.Fatalf("ListAgentReflexesForAgent: %v", err)
	}
	foundGlobal, foundRunScoped := false, false
	for _, r := range all {
		switch r.ID {
		case globalID:
			foundGlobal = true
		case runScopedID:
			foundRunScoped = true
		}
	}
	if !foundGlobal || !foundRunScoped {
		t.Fatalf("ListAgentReflexesForAgent: foundGlobal=%v foundRunScoped=%v, want both true (this query is scope-agnostic by design)", foundGlobal, foundRunScoped)
	}

	// ListAgentReflexesForWorkflowRun (new, this task) returns only the
	// run-scoped row for its own run, and nothing for an unrelated run.
	scoped, err := s.ListAgentReflexesForWorkflowRun(ctx, "run-wfr-roundtrip", "", "advisor")
	if err != nil {
		t.Fatalf("ListAgentReflexesForWorkflowRun: %v", err)
	}
	if len(scoped) != 1 || scoped[0].ID != runScopedID {
		t.Fatalf("ListAgentReflexesForWorkflowRun(run-wfr-roundtrip) = %+v, want exactly [%s]", scoped, runScopedID)
	}

	empty, err := s.ListAgentReflexesForWorkflowRun(ctx, "some-other-run", "", "advisor")
	if err != nil {
		t.Fatalf("ListAgentReflexesForWorkflowRun (unrelated run): %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("ListAgentReflexesForWorkflowRun(some-other-run) = %+v, want empty", empty)
	}

	noRun, err := s.ListAgentReflexesForWorkflowRun(ctx, "", "", "advisor")
	if err != nil {
		t.Fatalf("ListAgentReflexesForWorkflowRun (empty runID): %v", err)
	}
	if len(noRun) != 0 {
		t.Fatalf("ListAgentReflexesForWorkflowRun(\"\") = %+v, want nil/empty (no run context to scope against)", noRun)
	}
}

// TestActionKindAllowsProvenanceTier_DispatchToAgent_OperatorTier is the
// direct confirmation TASKS/teams/05-agent-reflexes-run-scoping.md's own
// Done-means requires for its provenance_tier judgment call: "operator"
// (the tier this task lands on for a Team-authored run-scoped
// dispatch_to_agent row — see TestAgentReflex_WorkflowRunID_RoundTrips's
// comment above for the full reasoning) passes migration
// 125_reflex_action_kind_provenance_allow.sql's per-kind allow-list gate
// for dispatch_to_agent, while "plugin" — deliberately denied by that
// migration for this exact action kind — does not. Overlaps with (does
// not replace) migration_125_reflex_action_kind_provenance_allow_test.go's
// TestMigrate125SeedsProvenanceAllowList, which already covers this same
// (kind, tier) pair as part of its full 18-combination round trip — this
// test exists as this task's own direct, self-contained confirmation of
// its own judgment call, not a re-derivation of that task's coverage.
func TestActionKindAllowsProvenanceTier_DispatchToAgent_OperatorTier(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	allowed, err := s.ActionKindAllowsProvenanceTier(ctx, ReflexActionDispatchToAgent, "operator")
	if err != nil {
		t.Fatalf("ActionKindAllowsProvenanceTier(dispatch_to_agent, operator): %v", err)
	}
	if !allowed {
		t.Fatalf("ActionKindAllowsProvenanceTier(dispatch_to_agent, operator) = false, want true — this task's landed provenance_tier choice for Team-authored run-scoped rows must pass the allow-list")
	}

	deniedPlugin, err := s.ActionKindAllowsProvenanceTier(ctx, ReflexActionDispatchToAgent, "plugin")
	if err != nil {
		t.Fatalf("ActionKindAllowsProvenanceTier(dispatch_to_agent, plugin): %v", err)
	}
	if deniedPlugin {
		t.Fatalf("ActionKindAllowsProvenanceTier(dispatch_to_agent, plugin) = true, want false — sanity check that this test isn't vacuously true (plugin-tier dispatch_to_agent is deliberately denied by migration 125)")
	}
}
