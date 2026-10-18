package workflowhost

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	workflowruntime "github.com/hollis-labs/libs/workflow/engine/runtime"

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
			if _, _, checkErr := f.state.CreateRun(t.Context(), f.createRequest()); checkErr != nil {
				t.Fatal(checkErr)
			}
			status := "pending"
			if terminal {
				status = "succeeded"
			}
			if _, checkErr := f.product.DB.ExecContext(t.Context(), `UPDATE workflow_runs SET runtime_status=?,definition_revision_id=NULL WHERE id='host-run'`, status); checkErr != nil {
				t.Fatal(checkErr)
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
	if checkErr := f.host.RecordPlanMaterial(t.Context(), compiled.material); checkErr != nil {
		t.Fatal(checkErr)
	}
	// Persist the migration-150 pilot identity from the first insert, with the
	// pre-154 NULL revision. No current-engine launch or relabeling is involved.
	if _, checkErr := f.product.DB.ExecContext(t.Context(), `INSERT INTO workflow_runs(id,definition_name,status,started_at,updated_at,engine_kind,engine_contract_version,plan_digest,runtime_status,runtime_generation) VALUES('pilot-history',?,'completed',?,?,?,?,?,'succeeded',3)`, compiled.material.ProductDefinitionName, workflowTime(workflowTestTime()), workflowTime(workflowTestTime()), EngineKindPilotHadron, PilotContractVersion, compiled.material.Plan.Digest); checkErr != nil {
		t.Fatal(checkErr)
	}
	state, err := collectWorkflowPreflight(t, f)
	if err != nil || state.warnings == 0 {
		t.Fatalf("pilot terminal warnings=%d err=%v", state.warnings, err)
	}
	if checkErr := PreflightWorkflowStorage(t.Context(), f.product); checkErr != nil {
		t.Fatal(checkErr)
	}
}

func TestSQLPreflightProductOrphanIsWarning(t *testing.T) {
	f := newSQLFacadeFixture(t, false, true)
	if _, _, checkErr := f.state.CreateRun(t.Context(), f.createRequest()); checkErr != nil {
		t.Fatal(checkErr)
	}
	if _, checkErr := f.product.DB.ExecContext(t.Context(), `INSERT INTO workflow_run_steps(id,workflow_run_id,step_id,kind) VALUES('orphan','missing','work','tool')`); checkErr != nil {
		t.Fatal(checkErr)
	}
	state, err := collectWorkflowPreflight(t, f)
	if err != nil || state.warnings != 1 {
		t.Fatalf("warnings=%d err=%v", state.warnings, err)
	}
	finding := state.findings[0]
	if finding.Severity != "warning" || finding.Table != "workflow_run_steps" || !strings.Contains(finding.Row, "orphan") || !strings.Contains(finding.Row, "missing") || finding.Expected == "" || finding.Actual == "" {
		t.Fatalf("orphan diagnostic=%+v", finding)
	}
	if checkErr := PreflightWorkflowStorage(t.Context(), f.product); checkErr != nil {
		t.Fatal(checkErr)
	}
}

func TestSQLPreflightMemoizesDistinctPlansForThousandsOfRuns(t *testing.T) {
	f := newSQLFacadeFixture(t, false, true)
	registry, registryErr := newFrozenRegistry(&recordingStepExecutor{})
	if registryErr != nil {
		t.Fatal(registryErr)
	}
	materials := []PlanMaterial{f.material}
	for i := 1; i < 3; i++ {
		material, err := CompileSource(t.Context(), f.material.SourceLocator, append([]byte(fmt.Sprintf("# distinct %d\n", i)), f.material.SourceContent...), registry)
		if err != nil {
			t.Fatal(err)
		}
		material.CreatedAt = workflowTestTime()
		if checkErr := f.host.RecordPlanMaterial(t.Context(), material); checkErr != nil {
			t.Fatal(checkErr)
		}
		if _, checkErr := f.host.publishDefinition(t.Context(), material); checkErr != nil {
			t.Fatal(checkErr)
		}
		materials = append(materials, material)
	}
	for index, material := range materials {
		if _, checkErr := f.product.DB.ExecContext(t.Context(), `WITH RECURSIVE ids(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM ids WHERE n<1200) INSERT INTO workflow_runs(id,definition_name,status,started_at,updated_at,engine_kind,engine_contract_version,plan_digest,runtime_status,runtime_generation,definition_revision_id) SELECT ?||n,?,'running',?,?,?, ?,?,'pending',1,(SELECT revision_id FROM workflow_definition_revisions WHERE compiled_plan_digest=?) FROM ids`, fmt.Sprintf("batch-%d-", index), material.ProductDefinitionName, workflowTime(workflowTestTime()), workflowTime(workflowTestTime()), EngineKindGoWorkflow, EngineContractVersion, material.Plan.Digest, material.Plan.Digest); checkErr != nil {
			t.Fatal(checkErr)
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
	if _, _, checkErr := f.state.CreateRun(t.Context(), f.createRequest()); checkErr != nil {
		t.Fatal(checkErr)
	}
	for _, statement := range []string{`UPDATE workflow_runs SET definition_revision_id=NULL,engine_contract_version='old-contract' WHERE id='host-run'`, `INSERT INTO workflow_event_sequences(run_id,last_sequence) VALUES('host-run',17)`, `INSERT INTO workflow_run_steps(id,workflow_run_id,step_id,kind) VALUES('orphan','missing','work','tool')`} {
		if _, checkErr := f.product.DB.ExecContext(t.Context(), statement); checkErr != nil {
			t.Fatal(checkErr)
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
		if _, checkErr := f.product.DB.ExecContext(t.Context(), statement); checkErr != nil {
			t.Fatal(checkErr)
		}
	}
	if checkErr := PreflightWorkflowStorage(t.Context(), f.product); checkErr != nil {
		t.Fatalf("unowned table blocked workflow storage: %v", checkErr)
	}
}

func TestSQLPreflightRetrySnapshotCorruption(t *testing.T) {
	f := newSQLFacadeFixture(t, false, true)
	_, started, proof := sqlFacadeStartAttempt(t, f)
	at := workflowTestTime().Add(3 * time.Second)
	_, err := f.state.ScheduleNodeRetry(t.Context(), workflowruntime.ScheduleNodeRetryRequest{Activation: workflowruntime.RetryActivationSnapshot{ID: "retry", Attempt: started.Attempt.ID, Status: workflowruntime.RetryScheduled, FireAt: at.Add(time.Minute), Failure: workflowruntime.Failure{Code: "transient", Message: "retry failure", Retryable: true}}, ExpectedNodeGeneration: started.Node.Generation, ExpectedAttemptGeneration: started.Attempt.Generation, Claim: proof, AttemptStatus: workflowruntime.NodeFailed, At: at})
	if err != nil {
		t.Fatal(err)
	}
	if err = PreflightWorkflowStorage(t.Context(), f.product); err != nil {
		t.Fatal(err)
	}
	if _, err = f.product.DB.ExecContext(t.Context(), `UPDATE workflow_retry_activations SET snapshot_json='{}' WHERE activation_id='retry'`); err != nil {
		t.Fatal(err)
	}
	if _, err = f.host.SQLWorkflowStateStore.LoadRetryActivation(t.Context(), "retry"); !errors.Is(err, workflowruntime.ErrInvalidRecord) {
		t.Fatalf("released reader: %v", err)
	}
	err = PreflightWorkflowStorage(t.Context(), f.product)
	if err == nil || !strings.Contains(err.Error(), "retry") || !strings.Contains(err.Error(), "host-run") {
		t.Fatalf("startup admitted unreadable retry or omitted its identity: %v", err)
	}
	report, err := collectWorkflowPreflight(t, f)
	if err == nil || report.counts["records:retry-activations"] != 1 {
		t.Fatalf("collect err=%v counts=%v", err, report.counts)
	}
}
