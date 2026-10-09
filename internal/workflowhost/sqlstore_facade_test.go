package workflowhost

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	workflowruntime "github.com/hollis-labs/libs/workflow/runtime"
	"github.com/hollis-labs/libs/workflow/values"
	nanitestore "github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

type sqlFacadeFixture struct {
	product  *nanitestore.Store
	host     *WorkflowStateStore
	state    workflowruntime.StateStore
	material PlanMaterial
	revision string
}

func newSQLFacadeFixture(t *testing.T, shared bool, publish bool) sqlFacadeFixture {
	t.Helper()
	product, err := storetest.New(t, t.Context(), filepath.Join(t.TempDir(), "host.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = product.Close(context.Background()) })
	// Every hook read and write must fit on the transaction's only connection.
	product.DB.SetMaxOpenConns(1)
	host, err := NewWorkflowStateStore(product)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := newFrozenRegistry(&recordingStepExecutor{})
	if err != nil {
		t.Fatal(err)
	}
	source := []byte("# preserve exact source bytes\nworkflow: {id: sql-facade, version: v1}\nsteps:\n  - {id: work, kind: nanite-tool, kind_version: v1, config: {tool: noop}}\n")
	material, err := CompileSource(t.Context(), "sql-facade.workflow.yaml", source, registry)
	if err != nil {
		t.Fatal(err)
	}
	material.CreatedAt = workflowTestTime()
	if err := host.RecordPlanMaterial(t.Context(), material); err != nil {
		t.Fatal(err)
	}
	// Use a distinct product identity to catch node-ID/step-ID conflation.
	if err := host.RecordPlanNodeProjections(t.Context(), planRef(material.Plan), []PlanNodeProjection{
		{NodeID: "work", ProductStepID: "product-work", ProductKind: "tool"},
	}); err != nil {
		t.Fatal(err)
	}
	f := sqlFacadeFixture{product: product, host: host, state: host, material: material}
	if publish {
		revision, err := host.publishDefinition(t.Context(), material)
		if err != nil {
			t.Fatal(err)
		}
		f.revision = revision.RevisionID
	}
	if shared {
		state, err := NewSQLWorkflowStateStore(product)
		if err != nil {
			t.Fatal(err)
		}
		f.state = state
	}
	return f
}

func (f sqlFacadeFixture) createRequest() workflowruntime.CreateRunRequest {
	return workflowruntime.CreateRunRequest{ID: "host-run", Plan: planRef(f.material.Plan), Status: workflowruntime.RunPending, StartIdempotencyKey: "host-start", CreatedAt: workflowTestTime()}
}

// Rows are read as SQLite values, keeping JSON/source/time bytes intact. This
// compares behavior on real product data, not counts or mutable source files.
func sqlFacadeRows(t *testing.T, db *sql.DB, query string) [][]any {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer closeRows(rows)
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var result [][]any
	for rows.Next() {
		row := make([]any, len(columns))
		targets := make([]any, len(columns))
		for i := range row {
			targets[i] = &row[i]
		}
		if err := rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		for i, value := range row {
			if b, ok := value.([]byte); ok {
				row[i] = string(b)
			}
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func sqlFacadeSnapshot(t *testing.T, f sqlFacadeFixture) map[string][][]any {
	t.Helper()
	queries := map[string]string{
		"runs":          "SELECT * FROM workflow_runs ORDER BY id",
		"steps":         "SELECT * FROM workflow_run_steps ORDER BY id",
		"nodes":         "SELECT * FROM workflow_node_invocations ORDER BY run_id,node_id,iteration",
		"leases":        "SELECT * FROM workflow_node_leases ORDER BY run_id,node_id,iteration",
		"attempts":      "SELECT * FROM workflow_attempts ORDER BY run_id,node_id,iteration,attempt_number",
		"events":        "SELECT * FROM workflow_events ORDER BY run_id,sequence",
		"sequences":     "SELECT * FROM workflow_event_sequences ORDER BY run_id",
		"starts":        "SELECT * FROM workflow_run_start_idempotency ORDER BY idempotency_key",
		"claims":        "SELECT * FROM workflow_claim_idempotency ORDER BY idempotency_key",
		"waits":         "SELECT * FROM workflow_waits ORDER BY wait_id",
		"wait-bindings": "SELECT * FROM workflow_wait_attempt_bindings ORDER BY wait_id",
	}
	result := make(map[string][][]any, len(queries))
	for name, query := range queries {
		result[name] = sqlFacadeRows(t, f.product.DB, query)
	}
	return result
}

func assertSQLFacadeEqual(t *testing.T, before, after map[string][][]any) {
	t.Helper()
	for name, rows := range before {
		if !reflect.DeepEqual(rows, after[name]) {
			t.Fatalf("%s changed: before=%#v after=%#v", name, rows, after[name])
		}
	}
}

func TestSQLFacadeFrozenRevisionAndProductParity(t *testing.T) {
	reference := newSQLFacadeFixture(t, false, true)
	shared := newSQLFacadeFixture(t, true, true)
	fixtures := []sqlFacadeFixture{reference, shared}
	runs := make([]workflowruntime.RunSnapshot, 2)
	nodes := make([]workflowruntime.NodeInvocationSnapshot, 2)
	proofs := make([]workflowruntime.ClaimProof, 2)
	attempts := make([]workflowruntime.AttemptSnapshot, 2)
	compare := func() {
		t.Helper()
		assertSQLFacadeEqual(t, sqlFacadeSnapshot(t, reference), sqlFacadeSnapshot(t, shared))
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	at := workflowTestTime()
	// Publish another revision and advance the mutable head. The first run must
	// bind its frozen digest, not whatever head is latest at insert time.
	for _, f := range fixtures {
		registry, err := newFrozenRegistry(&recordingStepExecutor{})
		if err != nil {
			t.Fatal(err)
		}
		next, err := CompileSource(ctx, f.material.SourceLocator, append([]byte("# newer revision\n"), f.material.SourceContent...), registry)
		if err != nil {
			t.Fatal(err)
		}
		next.CreatedAt = at
		if err := f.host.RecordPlanMaterial(ctx, next); err != nil {
			t.Fatal(err)
		}
		if _, err := f.host.publishDefinition(ctx, next); err != nil {
			t.Fatal(err)
		}
	}
	for i, f := range fixtures {
		run, outcome, err := f.state.CreateRun(ctx, f.createRequest())
		if err != nil || outcome != workflowruntime.IdempotencyApplied {
			t.Fatalf("create: %s %v", outcome, err)
		}
		runs[i] = run
		var revision, name, engine, contract string
		if bindingErr := f.product.DB.QueryRowContext(ctx, `SELECT definition_revision_id,definition_name,engine_kind,engine_contract_version FROM workflow_runs WHERE id=?`, run.ID).Scan(&revision, &name, &engine, &contract); bindingErr != nil {
			t.Fatal(bindingErr)
		}
		if revision != f.revision || name != f.material.ProductDefinitionName || engine != EngineKindGoWorkflow || contract != EngineContractVersion {
			t.Fatalf("incorrect frozen binding: %q %q %q %q", revision, name, engine, contract)
		}
		loaded, err := f.host.LoadPlanMaterial(ctx, run.Plan.Digest)
		if err != nil || !equalPlanMaterial(loaded, f.material) {
			t.Fatalf("frozen material changed: %v", err)
		}
	}
	compare()
	for i, f := range fixtures {
		running, err := f.state.TransitionRun(ctx, workflowruntime.RunTransitionRequest{RunID: runs[i].ID, ExpectedGeneration: runs[i].Generation, To: workflowruntime.RunRunning, At: at.Add(time.Second)})
		if err != nil {
			t.Fatal(err)
		}
		runs[i] = running.Snapshot
		node, err := f.state.CreateNodeInvocation(ctx, workflowruntime.CreateNodeInvocationRequest{Snapshot: workflowruntime.NodeInvocationSnapshot{ID: workflowruntime.NodeInvocationID{RunID: runs[i].ID, NodeID: "work"}, Status: workflowruntime.NodePending, CreatedAt: at, UpdatedAt: at}})
		if err != nil {
			t.Fatal(err)
		}
		nodes[i] = node
	}
	compare()
	for i, f := range fixtures {
		ready, err := f.state.TransitionNode(ctx, workflowruntime.NodeTransitionRequest{InvocationID: nodes[i].ID, ExpectedGeneration: nodes[i].Generation, To: workflowruntime.NodeReady, At: at.Add(time.Second)})
		if err != nil {
			t.Fatal(err)
		}
		nodes[i] = ready.Snapshot
	}
	compare()
	for i, f := range fixtures {
		claim, err := f.state.ClaimNode(ctx, workflowruntime.ClaimNodeRequest{InvocationID: nodes[i].ID, ExpectedClaimGeneration: nodes[i].ClaimGeneration, Owner: "host-worker", Token: "host-token", IdempotencyKey: "host-claim", Now: at.Add(2 * time.Second), LeaseUntil: at.Add(time.Hour)})
		if err != nil || !claim.Acquired || claim.Lease == nil {
			t.Fatalf("claim: %+v %v", claim, err)
		}
		proofs[i] = workflowruntime.ClaimProof{Owner: claim.Lease.Owner, Token: claim.Lease.Token, Generation: claim.Lease.Generation}
		node, err := f.state.LoadNodeInvocation(ctx, nodes[i].ID)
		if err != nil {
			t.Fatal(err)
		}
		nodes[i] = node
	}
	compare()
	for i, f := range fixtures {
		started, err := f.state.StartNodeAttempt(ctx, workflowruntime.StartNodeAttemptRequest{InvocationID: nodes[i].ID, ExpectedNodeGeneration: nodes[i].Generation, Claim: proofs[i], Executor: workflowruntime.ExecutorMetadata{Kind: "host-test", Version: "v1"}, At: at.Add(3 * time.Second)})
		if err != nil {
			t.Fatal(err)
		}
		nodes[i], attempts[i] = started.Node, started.Attempt
	}
	compare()
	for i, f := range fixtures {
		value, err := values.NewInline(map[string]any{"output": "exact output ☃", "tool_calls": []any{map[string]any{"name": "noop"}}, "verify": map[string]any{"passed": true}}, values.Metadata{Producer: values.Producer{Kind: "test", Reference: "work"}, MediaType: "application/json", Redaction: values.RedactionPrivate, Retention: values.RetentionRun})
		if err != nil {
			t.Fatal(err)
		}
		outputs, err := f.state.SaveValues(ctx, workflowruntime.SaveValuesRequest{Owner: workflowruntime.ValueOwner{Kind: "test", RunID: runs[i].ID}, Values: values.ValueSet{"result": value}})
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.state.FinishNodeAttempt(ctx, workflowruntime.FinishNodeAttemptRequest{InvocationID: nodes[i].ID, AttemptNumber: attempts[i].ID.Number, ExpectedNodeGeneration: nodes[i].Generation, ExpectedAttemptGeneration: attempts[i].Generation, Claim: proofs[i], AttemptStatus: workflowruntime.NodeSucceeded, NextNodeStatus: workflowruntime.NodeSucceeded, Outputs: &outputs, At: at.Add(4 * time.Second)})
		if err != nil {
			t.Fatal(err)
		}
	}
	compare()
	for i, f := range fixtures {
		_, err := f.state.TransitionRun(ctx, workflowruntime.RunTransitionRequest{RunID: runs[i].ID, ExpectedGeneration: runs[i].Generation, To: workflowruntime.RunSucceeded, At: at.Add(5 * time.Second)})
		if err != nil {
			t.Fatal(err)
		}
	}
	compare()
}

func TestSQLFacadePreservesExistingEngineIdentity(t *testing.T) {
	f := newSQLFacadeFixture(t, true, true)
	ctx := t.Context()
	run, _, err := f.state.CreateRun(ctx, f.createRequest())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.product.DB.ExecContext(ctx, `UPDATE workflow_runs SET engine_kind=?,engine_contract_version=? WHERE id=?`, EngineKindPilotHadron, PilotContractVersion, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.state.TransitionRun(ctx, workflowruntime.RunTransitionRequest{RunID: run.ID, ExpectedGeneration: run.Generation, To: workflowruntime.RunRunning, At: workflowTestTime().Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	var kind, contract, revision string
	if err := f.product.DB.QueryRowContext(ctx, `SELECT engine_kind,engine_contract_version,definition_revision_id FROM workflow_runs WHERE id=?`, run.ID).Scan(&kind, &contract, &revision); err != nil {
		t.Fatal(err)
	}
	if kind != EngineKindPilotHadron || contract != PilotContractVersion || revision != f.revision {
		t.Fatalf("reclassified existing row: %q %q %q", kind, contract, revision)
	}
}

func TestSQLFacadeMissingRevisionParityAndRollback(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(map[bool]string{false: "reference", true: "released"}[shared], func(t *testing.T) {
			f := newSQLFacadeFixture(t, shared, false)
			before := sqlFacadeSnapshot(t, f)
			_, _, err := f.state.CreateRun(t.Context(), f.createRequest())
			if !errors.Is(err, workflowruntime.ErrInvalidRecord) || errors.Is(err, workflowruntime.ErrNotFound) {
				t.Fatalf("missing revision category: %v", err)
			}
			assertSQLFacadeEqual(t, before, sqlFacadeSnapshot(t, f))
			// Publication followed by the same request succeeds: no start replay or
			// half-inserted run survived the failed identity hook.
			if _, err := f.host.publishDefinition(t.Context(), f.material); err != nil {
				t.Fatal(err)
			}
			if _, outcome, err := f.state.CreateRun(t.Context(), f.createRequest()); err != nil || outcome != workflowruntime.IdempotencyApplied {
				t.Fatalf("retry create: %s %v", outcome, err)
			}
		})
	}
}

func TestSQLFacadeHookFailureRollback(t *testing.T) {
	for _, operation := range []string{"run", "node", "claim", "finish"} {
		t.Run(operation, func(t *testing.T) {
			f := newSQLFacadeFixture(t, true, true)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			at := workflowTestTime()
			run, _, err := f.state.CreateRun(ctx, f.createRequest())
			if err != nil {
				t.Fatal(err)
			}
			var node workflowruntime.NodeInvocationSnapshot
			var proof workflowruntime.ClaimProof
			var attempt workflowruntime.AttemptSnapshot
			if operation == "claim" || operation == "finish" {
				node, err = f.state.CreateNodeInvocation(ctx, workflowruntime.CreateNodeInvocationRequest{Snapshot: workflowruntime.NodeInvocationSnapshot{ID: workflowruntime.NodeInvocationID{RunID: run.ID, NodeID: "work"}, Status: workflowruntime.NodePending, CreatedAt: at, UpdatedAt: at}})
				if err != nil {
					t.Fatal(err)
				}
				ready, err := f.state.TransitionNode(ctx, workflowruntime.NodeTransitionRequest{InvocationID: node.ID, ExpectedGeneration: node.Generation, To: workflowruntime.NodeReady, At: at})
				if err != nil {
					t.Fatal(err)
				}
				node = ready.Snapshot
			}
			if operation == "finish" {
				claim, err := f.state.ClaimNode(ctx, workflowruntime.ClaimNodeRequest{InvocationID: node.ID, ExpectedClaimGeneration: node.ClaimGeneration, Owner: "worker", Token: "token", IdempotencyKey: "claim", Now: at, LeaseUntil: at.Add(time.Hour)})
				if err != nil || claim.Lease == nil {
					t.Fatalf("claim: %+v %v", claim, err)
				}
				proof = workflowruntime.ClaimProof{Owner: claim.Lease.Owner, Token: claim.Lease.Token, Generation: claim.Lease.Generation}
				node, err = f.state.LoadNodeInvocation(ctx, node.ID)
				if err != nil {
					t.Fatal(err)
				}
				started, err := f.state.StartNodeAttempt(ctx, workflowruntime.StartNodeAttemptRequest{InvocationID: node.ID, ExpectedNodeGeneration: node.Generation, Claim: proof, Executor: workflowruntime.ExecutorMetadata{Kind: "test", Version: "v1"}, At: at})
				if err != nil {
					t.Fatal(err)
				}
				node, attempt = started.Node, started.Attempt
			}
			trigger := `CREATE TRIGGER reject_projection BEFORE INSERT ON workflow_run_steps BEGIN SELECT RAISE(ABORT,'projection rejected'); END`
			if operation == "run" {
				trigger = `CREATE TRIGGER reject_projection BEFORE UPDATE OF status ON workflow_runs BEGIN SELECT RAISE(ABORT,'projection rejected'); END`
			}
			if _, err := f.product.DB.ExecContext(ctx, trigger); err != nil {
				t.Fatal(err)
			}
			before := sqlFacadeSnapshot(t, f)
			apply := func() error {
				switch operation {
				case "run":
					_, err := f.state.TransitionRun(ctx, workflowruntime.RunTransitionRequest{RunID: run.ID, ExpectedGeneration: run.Generation, To: workflowruntime.RunRunning, At: at.Add(time.Second)})
					return err
				case "node":
					_, err := f.state.CreateNodeInvocation(ctx, workflowruntime.CreateNodeInvocationRequest{Snapshot: workflowruntime.NodeInvocationSnapshot{ID: workflowruntime.NodeInvocationID{RunID: run.ID, NodeID: "work"}, Status: workflowruntime.NodePending, CreatedAt: at, UpdatedAt: at}})
					return err
				case "claim":
					_, err := f.state.ClaimNode(ctx, workflowruntime.ClaimNodeRequest{InvocationID: node.ID, ExpectedClaimGeneration: node.ClaimGeneration, Owner: "worker", Token: "token", IdempotencyKey: "claim", Now: at, LeaseUntil: at.Add(time.Hour)})
					return err
				default:
					_, err := f.state.FinishNodeAttempt(ctx, workflowruntime.FinishNodeAttemptRequest{InvocationID: node.ID, AttemptNumber: attempt.ID.Number, ExpectedNodeGeneration: node.Generation, ExpectedAttemptGeneration: attempt.Generation, Claim: proof, AttemptStatus: workflowruntime.NodeSucceeded, NextNodeStatus: workflowruntime.NodeSucceeded, At: at.Add(time.Second)})
					return err
				}
			}
			if err := apply(); err == nil {
				t.Fatal("projection failure committed")
			}
			assertSQLFacadeEqual(t, before, sqlFacadeSnapshot(t, f))
			if _, err := f.product.DB.ExecContext(ctx, `DROP TRIGGER reject_projection`); err != nil {
				t.Fatal(err)
			}
			if err := apply(); err != nil {
				t.Fatalf("retry after rollback: %v", err)
			}
		})
	}
}

func TestSQLFacadeNeutralErrorParity(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(map[bool]string{false: "reference", true: "released"}[shared], func(t *testing.T) {
			f := newSQLFacadeFixture(t, shared, true)
			ctx := t.Context()
			if _, err := f.state.LoadRun(ctx, "absent"); !errors.Is(err, workflowruntime.ErrNotFound) {
				t.Fatalf("absence: %v", err)
			}
			invalid := f.createRequest()
			invalid.Status = workflowruntime.RunRunning
			if _, _, err := f.state.CreateRun(ctx, invalid); !errors.Is(err, workflowruntime.ErrInvalidRecord) {
				t.Fatalf("invalid: %v", err)
			}
			request := f.createRequest()
			run, _, err := f.state.CreateRun(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			if _, outcome, err := f.state.CreateRun(ctx, request); err != nil || outcome != workflowruntime.IdempotencyReplayed {
				t.Fatalf("replay: %s %v", outcome, err)
			}
			request.ID = "other"
			if _, _, err := f.state.CreateRun(ctx, request); !errors.Is(err, workflowruntime.ErrIdempotencyConflict) {
				t.Fatalf("key conflict: %v", err)
			}
			request.ID = run.ID
			request.StartIdempotencyKey = "other-key"
			if _, _, err := f.state.CreateRun(ctx, request); !errors.Is(err, workflowruntime.ErrAlreadyExists) {
				t.Fatalf("identity conflict: %v", err)
			}
			if _, err := f.state.TransitionRun(ctx, workflowruntime.RunTransitionRequest{RunID: run.ID, ExpectedGeneration: 0, To: workflowruntime.RunRunning, At: workflowTestTime()}); !errors.Is(err, workflowruntime.ErrCASMismatch) {
				t.Fatalf("stale generation: %v", err)
			}
		})
	}
}

func TestSQLFacadeWaitProjectionParity(t *testing.T) {
	reference := newSQLFacadeFixture(t, false, true)
	shared := newSQLFacadeFixture(t, true, true)
	// Reuse the host's existing durable callback fixture; preparations deliberately
	// use the reference writer, then suspension uses the selected implementation.
	first := prepareWorkflowSQLiteWait(t, reference.host, "facade", workflowTestTime(), time.Hour)
	second := prepareWorkflowSQLiteWait(t, shared.host, "facade", workflowTestTime(), time.Hour)
	assertSQLFacadeEqual(t, sqlFacadeSnapshot(t, reference), sqlFacadeSnapshot(t, shared))
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for i, f := range []sqlFacadeFixture{reference, shared} {
		fixture := []workflowSQLiteWaitFixture{first, second}[i]
		waits := f.state.(workflowruntime.WaitStore)
		if _, err := waits.SuspendNodeWait(ctx, fixture.request); err != nil {
			t.Fatal(err)
		}
		run, err := f.state.LoadRun(ctx, fixture.invocation.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.state.TransitionRun(ctx, workflowruntime.RunTransitionRequest{RunID: run.ID, ExpectedGeneration: run.Generation, To: workflowruntime.RunWaiting, At: fixture.base.Add(4 * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	assertSQLFacadeEqual(t, sqlFacadeSnapshot(t, reference), sqlFacadeSnapshot(t, shared))
	for i, f := range []sqlFacadeFixture{reference, shared} {
		fixture := []workflowSQLiteWaitFixture{first, second}[i]
		if _, err := (workflowruntime.WaitCoordinator{Store: f.state.(workflowruntime.WaitStore)}).Resume(ctx, fixture.resumeCommand(t, "facade-response", fixture.base.Add(5*time.Second))); err != nil {
			t.Fatal(err)
		}
	}
	assertSQLFacadeEqual(t, sqlFacadeSnapshot(t, reference), sqlFacadeSnapshot(t, shared))
}

func TestSQLFacadeExcludedProjectionParity(t *testing.T) {
	reference := newSQLFacadeFixture(t, false, true)
	shared := newSQLFacadeFixture(t, true, true)
	for _, f := range []sqlFacadeFixture{reference, shared} {
		run, _, err := f.state.CreateRun(t.Context(), f.createRequest())
		if err != nil {
			t.Fatal(err)
		}
		for _, snapshot := range []workflowruntime.NodeInvocationSnapshot{
			{ID: workflowruntime.NodeInvocationID{RunID: run.ID, NodeID: "work", Iteration: "item-1"}, Status: workflowruntime.NodePending, CreatedAt: workflowTestTime(), UpdatedAt: workflowTestTime()},
		} {
			if _, err := f.state.CreateNodeInvocation(t.Context(), workflowruntime.CreateNodeInvocationRequest{Snapshot: snapshot}); err != nil {
				t.Fatal(err)
			}
		}
		compensation := workflowruntime.NodeInvocationSnapshot{ID: workflowruntime.NodeInvocationID{RunID: run.ID, NodeID: "undo"}, Phase: workflowruntime.InvocationCompensation, Status: workflowruntime.NodePending, CreatedAt: workflowTestTime(), UpdatedAt: workflowTestTime()}
		if _, err := f.state.CreateNodeInvocation(t.Context(), workflowruntime.CreateNodeInvocationRequest{Snapshot: compensation}); !errors.Is(err, workflowruntime.ErrInvalidRecord) {
			t.Fatalf("non-atomic saga node accepted: %v", err)
		}
		// The saga writer invokes the same hook after atomic materialization. Check
		// that its compensation snapshot is deliberately invisible to product steps.
		if err := f.host.write(t.Context(), "excluded compensation projection", func(tx workflowSQL) error {
			return (workflowProductHooks{}).AfterNodeWritten(t.Context(), tx, compensation)
		}); err != nil {
			t.Fatal(err)
		}
		if steps, err := f.product.ListWorkflowRunSteps(t.Context(), string(run.ID)); err != nil || len(steps) != 0 {
			t.Fatalf("iteration/compensation became product steps: %+v %v", steps, err)
		}
	}
	assertSQLFacadeEqual(t, sqlFacadeSnapshot(t, reference), sqlFacadeSnapshot(t, shared))
}

func TestSQLFacadeConstructionPreservesLegacyHistory(t *testing.T) {
	if _, err := NewSQLWorkflowStateStore(nil); err == nil {
		t.Fatal("nil product store accepted")
	}
	if _, err := NewSQLWorkflowStateStore(&nanitestore.Store{}); err == nil {
		t.Fatal("nil database accepted")
	}
	f := newSQLFacadeFixture(t, false, true)
	legacy := &nanitestore.WorkflowRunRow{ID: "legacy-terminal", DefinitionName: "historical", Status: "completed", InputJSON: `{"exact":"bytes"}`, StartedAt: workflowTestTime(), CompletedAt: workflowTestTime().Add(time.Minute), UpdatedAt: workflowTestTime().Add(time.Minute)}
	if err := f.product.CreateWorkflowRun(t.Context(), legacy); err != nil {
		t.Fatal(err)
	}
	before := sqlFacadeSnapshot(t, f)
	// Construction must never run the library migration or rewrite legacy rows
	// into canonical runs. SQLite schema and migration ledger stay untouched.
	schema := sqlFacadeRows(t, f.product.DB, `SELECT type,name,tbl_name,sql FROM sqlite_master ORDER BY type,name`)
	ledger := sqlFacadeRows(t, f.product.DB, `SELECT * FROM goose_db_version ORDER BY id`)
	facade, err := NewSQLWorkflowStateStore(f.product)
	if err != nil {
		t.Fatal(err)
	}
	assertSQLFacadeEqual(t, before, sqlFacadeSnapshot(t, f))
	if !reflect.DeepEqual(schema, sqlFacadeRows(t, f.product.DB, `SELECT type,name,tbl_name,sql FROM sqlite_master ORDER BY type,name`)) || !reflect.DeepEqual(ledger, sqlFacadeRows(t, f.product.DB, `SELECT * FROM goose_db_version ORDER BY id`)) {
		t.Fatal("facade constructor changed schema or migration authority")
	}
	loaded, err := f.product.GetWorkflowRun(t.Context(), legacy.ID)
	if err != nil || loaded.DefinitionName != legacy.DefinitionName || loaded.InputJSON != legacy.InputJSON || loaded.Status != "completed" {
		t.Fatalf("legacy view changed: %+v %v", loaded, err)
	}
	if facade.DB() != f.product.DB {
		t.Fatal("facade created a second database authority")
	}
}
