package workflowhost

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
)

func collectWorkflowPreflight(t *testing.T, f sqlFacadeFixture) (*workflowPreflight, error) {
	t.Helper()
	state := newWorkflowPreflight(true)
	err := f.host.SQLWorkflowStateStore.write(t.Context(), "collect qualification", func(tx workflowSQL) error {
		return preflightWorkflowSnapshot(context.WithValue(t.Context(), workflowPreflightKey{}, state), tx)
	})
	return state, err
}

func TestSQLPreflightTerminalCompatibilityWarnings(t *testing.T) {
	for _, terminal := range []bool{true, false} {
		t.Run(fmt.Sprint(terminal), func(t *testing.T) {
			f := newSQLFacadeFixture(t, false, true)
			if _, _, err := f.state.CreateRun(t.Context(), f.createRequest()); err != nil {
				t.Fatal(err)
			}
			status := "pending"
			if terminal {
				status = "succeeded"
			}
			if _, err := f.product.DB.ExecContext(t.Context(), `UPDATE workflow_runs SET runtime_status=?,definition_revision_id=NULL WHERE id='host-run'`, status); err != nil {
				t.Fatal(err)
			}
			state, err := collectWorkflowPreflight(t, f)
			if terminal {
				if err != nil || state.warnings == 0 || state.fatals != 0 {
					t.Fatalf("terminal warnings=%d fatals=%d err=%v", state.warnings, state.fatals, err)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), "definition_revision_id=NULL") || !strings.Contains(err.Error(), "host-run") {
					t.Fatalf("resumable refusal=%v", err)
				}
			}
			if startErr := PreflightWorkflowStorage(t.Context(), f.product); (startErr == nil) != terminal {
				t.Fatalf("startup terminal=%v error=%v", terminal, startErr)
			}
		})
	}
}

func TestSQLPreflightPilotEraTerminalRow(t *testing.T) {
	f := newSQLFacadeFixture(t, false, true)
	registry, err := newPilotRegistry(&recordingStepExecutor{})
	if err != nil {
		t.Fatal(err)
	}
	definition := agentworkflow.WorkflowDefinition{Name: "pilot-era", Engine: agentworkflow.EngineHadron, Steps: []agentworkflow.StepDefinition{{ID: "approval", Kind: agentworkflow.StepKindGate, Config: map[string]any{"authority_ref": "operator"}}}}
	compiled, err := compileWorkflowDefinition(t.Context(), definition, registry)
	if err != nil {
		t.Fatal(err)
	}
	compiled.material.HostContract, compiled.material.HostContractDigest, err = pilotHostContract()
	if err != nil {
		t.Fatal(err)
	}
	if err := f.host.RecordPlanMaterial(t.Context(), compiled.material); err != nil {
		t.Fatal(err)
	}
	// Persist the migration-150 pilot identity from the first insert, with the
	// pre-154 NULL revision. No current-engine launch or relabeling is involved.
	if _, err := f.product.DB.ExecContext(t.Context(), `INSERT INTO workflow_runs(id,definition_name,status,started_at,updated_at,engine_kind,engine_contract_version,plan_digest,runtime_status,runtime_generation) VALUES('pilot-history',?,'completed',?,?,?,?,?,'succeeded',3)`, compiled.material.ProductDefinitionName, workflowTime(workflowTestTime()), workflowTime(workflowTestTime()), EngineKindPilotHadron, PilotContractVersion, compiled.material.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	state, err := collectWorkflowPreflight(t, f)
	if err != nil || state.warnings == 0 {
		t.Fatalf("pilot terminal warnings=%d err=%v", state.warnings, err)
	}
	if err := PreflightWorkflowStorage(t.Context(), f.product); err != nil {
		t.Fatal(err)
	}
}

func TestSQLPreflightProductOrphanIsWarning(t *testing.T) {
	f := newSQLFacadeFixture(t, false, true)
	if _, _, err := f.state.CreateRun(t.Context(), f.createRequest()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.product.DB.ExecContext(t.Context(), `INSERT INTO workflow_run_steps(id,workflow_run_id,step_id,kind) VALUES('orphan','missing','work','tool')`); err != nil {
		t.Fatal(err)
	}
	state, err := collectWorkflowPreflight(t, f)
	if err != nil || state.warnings != 1 {
		t.Fatalf("warnings=%d err=%v", state.warnings, err)
	}
	finding := state.findings[0]
	if finding.Severity != "warning" || finding.Table != "workflow_run_steps" || !strings.Contains(finding.Row, "orphan") || !strings.Contains(finding.Row, "missing") || finding.Expected == "" || finding.Actual == "" {
		t.Fatalf("orphan diagnostic=%+v", finding)
	}
	if err := PreflightWorkflowStorage(t.Context(), f.product); err != nil {
		t.Fatal(err)
	}
}

func TestSQLPreflightMemoizesDistinctPlansForThousandsOfRuns(t *testing.T) {
	f := newSQLFacadeFixture(t, false, true)
	registry, err := newFrozenRegistry(&recordingStepExecutor{})
	if err != nil {
		t.Fatal(err)
	}
	materials := []PlanMaterial{f.material}
	for i := 1; i < 3; i++ {
		material, err := CompileSource(t.Context(), f.material.SourceLocator, append([]byte(fmt.Sprintf("# distinct %d\n", i)), f.material.SourceContent...), registry)
		if err != nil {
			t.Fatal(err)
		}
		material.CreatedAt = workflowTestTime()
		if err := f.host.RecordPlanMaterial(t.Context(), material); err != nil {
			t.Fatal(err)
		}
		if _, err := f.host.publishDefinition(t.Context(), material); err != nil {
			t.Fatal(err)
		}
		materials = append(materials, material)
	}
	for index, material := range materials {
		if _, err := f.product.DB.ExecContext(t.Context(), `WITH RECURSIVE ids(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM ids WHERE n<1200) INSERT INTO workflow_runs(id,definition_name,status,started_at,updated_at,engine_kind,engine_contract_version,plan_digest,runtime_status,runtime_generation,definition_revision_id) SELECT ?||n,?,'running',?,?,?, ?,?,'pending',1,(SELECT revision_id FROM workflow_definition_revisions WHERE compiled_plan_digest=?) FROM ids`, fmt.Sprintf("batch-%d-", index), material.ProductDefinitionName, workflowTime(workflowTestTime()), workflowTime(workflowTestTime()), EngineKindGoWorkflow, EngineContractVersion, material.Plan.Digest, material.Plan.Digest); err != nil {
			t.Fatal(err)
		}
	}
	state, err := collectWorkflowPreflight(t, f)
	if err != nil {
		t.Fatal(err)
	}
	if state.counts["run-bindings"] != 3600 || state.materialChecks != 3 || state.identityChecks != 3 || state.planChecks != 3 {
		t.Fatalf("runs=%d material=%d identity=%d ValidatePlan=%d", state.counts["run-bindings"], state.materialChecks, state.identityChecks, state.planChecks)
	}
}

func TestSQLPreflightCollectsAllNamedFindings(t *testing.T) {
	f := newSQLFacadeFixture(t, false, true)
	if _, _, err := f.state.CreateRun(t.Context(), f.createRequest()); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{`UPDATE workflow_runs SET definition_revision_id=NULL,engine_contract_version='old-contract' WHERE id='host-run'`, `INSERT INTO workflow_event_sequences(run_id,last_sequence) VALUES('host-run',17)`, `INSERT INTO workflow_run_steps(id,workflow_run_id,step_id,kind) VALUES('orphan','missing','work','tool')`} {
		if _, err := f.product.DB.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	state, err := collectWorkflowPreflight(t, f)
	if err == nil || state.fatals < 3 || state.warnings != 1 {
		t.Fatalf("fatals=%d warnings=%d err=%v findings=%+v", state.fatals, state.warnings, err, state.findings)
	}
	for _, finding := range state.findings {
		if finding.Table == "" || finding.Row == "" || finding.Expected == "" || finding.Actual == "" {
			t.Fatalf("unnamed finding=%+v", finding)
		}
	}
	if !strings.Contains(err.Error(), "roll back to the previous binary") || strings.Contains(err.Error(), "audited disposition") {
		t.Fatalf("operator disposition=%v", err)
	}
}

func TestSQLPreflightScopesSchemaAndForeignKeys(t *testing.T) {
	f := newSQLFacadeFixture(t, false, true)
	for _, statement := range []string{`CREATE TABLE workflowX_unrelated(id TEXT PRIMARY KEY,run_id TEXT REFERENCES workflow_runs(id))`, `PRAGMA foreign_keys=OFF`, `INSERT INTO workflowX_unrelated VALUES('unrelated','absent')`, `PRAGMA foreign_keys=ON`} {
		if _, err := f.product.DB.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := PreflightWorkflowStorage(t.Context(), f.product); err != nil {
		t.Fatalf("unowned table blocked workflow storage: %v", err)
	}
}
