package workflowhost

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hollis-labs/go-workflow/compile"
	"github.com/hollis-labs/go-workflow/graph"
	workflowruntime "github.com/hollis-labs/go-workflow/runtime"
	"github.com/hollis-labs/go-workflow/values"
	workflowwait "github.com/hollis-labs/go-workflow/wait"
	nanitestore "github.com/hollis-labs/nanite/internal/store"
)

func TestSQLStoragePreflightPopulatedOldSchema(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(map[bool]string{false: "old-writer", true: "new-writer"}[shared], func(t *testing.T) {
			f := newSQLFacadeFixture(t, shared, true)
			run, _, err := f.state.CreateRun(t.Context(), f.createRequest())
			if err != nil {
				t.Fatal(err)
			}
			// The real migrated schema/ledger predates adoption; neither facade
			// construction nor preflight introduces a new schema version.
			before := sqlFacadeSnapshot(t, f)
			schema := sqlFacadeRows(t, f.product.DB, `SELECT * FROM sqlite_master ORDER BY type,name`)
			ledger := sqlFacadeRows(t, f.product.DB, `SELECT * FROM goose_db_version ORDER BY id`)
			if checkErr := PreflightWorkflowStorage(t.Context(), f.product); checkErr != nil {
				t.Fatal(checkErr)
			}
			assertSQLFacadeEqual(t, before, sqlFacadeSnapshot(t, f))
			if !reflect.DeepEqual(schema, sqlFacadeRows(t, f.product.DB, `SELECT * FROM sqlite_master ORDER BY type,name`)) || !reflect.DeepEqual(ledger, sqlFacadeRows(t, f.product.DB, `SELECT * FROM goose_db_version ORDER BY id`)) {
				t.Fatal("preflight changed schema or migration ledger")
			}
			old, err := f.host.LoadRun(t.Context(), run.ID)
			if err != nil {
				t.Fatal(err)
			}
			newer, err := f.host.SQLWorkflowStateStore.LoadRun(t.Context(), run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(old, newer) {
				t.Fatalf("cross-reader mismatch: old=%+v new=%+v", old, newer)
			}
		})
	}
}

func TestSQLStoragePreflightRejectsDriftWithoutRepair(t *testing.T) {
	for name, statement := range map[string]string{
		"column":       "ALTER TABLE workflow_runs ADD COLUMN unqualified TEXT",
		"index":        "DROP INDEX idx_workflow_definition_revisions_exact_plan",
		"trigger":      "DROP TRIGGER workflow_plan_materials_no_update",
		"identity":     "UPDATE workflow_runs SET engine_contract_version='unknown'",
		"revision":     "UPDATE workflow_runs SET definition_revision_id=NULL",
		"generation":   "UPDATE workflow_runs SET runtime_generation=0",
		"source":       "UPDATE workflow_runs SET definition_name='wrong-source'",
		"idempotency":  "UPDATE workflow_run_start_idempotency SET result_json='{}'",
		"event-cursor": `INSERT INTO workflow_event_sequences(run_id,last_sequence) VALUES('host-run',17)`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newSQLFacadeFixture(t, false, true)
			if _, _, checkErr := f.state.CreateRun(t.Context(), f.createRequest()); checkErr != nil {
				t.Fatal(checkErr)
			}
			if _, checkErr := f.product.DB.ExecContext(t.Context(), statement); checkErr != nil {
				t.Fatal(checkErr)
			}
			before := sqlFacadeSnapshot(t, f)
			if checkErr := PreflightWorkflowStorage(t.Context(), f.product); checkErr == nil {
				t.Fatal("preflight accepted drift")
			}
			assertSQLFacadeEqual(t, before, sqlFacadeSnapshot(t, f))
		})
	}
}

func sqlFacadeStartAttempt(t *testing.T, f sqlFacadeFixture) (workflowruntime.RunSnapshot, workflowruntime.StartNodeAttemptResult, workflowruntime.ClaimProof) {
	t.Helper()
	ctx := t.Context()
	at := workflowTestTime()
	run, _, err := f.state.CreateRun(ctx, f.createRequest())
	if err != nil {
		t.Fatal(err)
	}
	transition, err := f.state.TransitionRun(ctx, workflowruntime.RunTransitionRequest{RunID: run.ID, ExpectedGeneration: run.Generation, To: workflowruntime.RunRunning, At: at})
	if err != nil {
		t.Fatal(err)
	}
	run = transition.Snapshot
	node, err := f.state.CreateNodeInvocation(ctx, workflowruntime.CreateNodeInvocationRequest{Snapshot: workflowruntime.NodeInvocationSnapshot{ID: workflowruntime.NodeInvocationID{RunID: run.ID, NodeID: "work"}, Status: workflowruntime.NodePending, CreatedAt: at, UpdatedAt: at}})
	if err != nil {
		t.Fatal(err)
	}
	ready, err := f.state.TransitionNode(ctx, workflowruntime.NodeTransitionRequest{InvocationID: node.ID, ExpectedGeneration: node.Generation, To: workflowruntime.NodeReady, At: at})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := f.state.ClaimNode(ctx, workflowruntime.ClaimNodeRequest{InvocationID: node.ID, ExpectedClaimGeneration: ready.Snapshot.ClaimGeneration, Owner: "worker", Token: "token", IdempotencyKey: "claim", Now: at.Add(time.Second), LeaseUntil: at.Add(time.Hour)})
	if err != nil || claim.Lease == nil {
		t.Fatalf("claim: %+v %v", claim, err)
	}
	node, err = f.state.LoadNodeInvocation(ctx, node.ID)
	if err != nil {
		t.Fatal(err)
	}
	proof := workflowruntime.ClaimProof{Owner: claim.Lease.Owner, Token: claim.Lease.Token, Generation: claim.Lease.Generation}
	started, err := f.state.StartNodeAttempt(ctx, workflowruntime.StartNodeAttemptRequest{InvocationID: node.ID, ExpectedNodeGeneration: node.Generation, Claim: proof, Executor: workflowruntime.ExecutorMetadata{Kind: "test", Version: "v1"}, At: at.Add(2 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	return run, started, proof
}

func TestSQLFacadeExplicitTerminalProductRows(t *testing.T) {
	for _, status := range []workflowruntime.NodeStatus{workflowruntime.NodeFailed, workflowruntime.NodeTimedOut, workflowruntime.NodeCrashed, workflowruntime.NodeCanceled} {
		t.Run(string(status), func(t *testing.T) {
			for _, shared := range []bool{false, true} {
				f := newSQLFacadeFixture(t, shared, true)
				_, started, proof := sqlFacadeStartAttempt(t, f)
				at := workflowTestTime().Add(3 * time.Second)
				failure := &workflowruntime.Failure{Code: "test_failure", Message: "exact failure"}
				wantStatus, wantError, wantMessage := "skipped", int64(0), ""
				if status != workflowruntime.NodeCanceled {
					failure = &workflowruntime.Failure{Code: "test_failure", Message: "exact failure"}
					wantStatus, wantError, wantMessage = "failed", 1, "exact failure"
				}
				_, err := f.state.FinishNodeAttempt(t.Context(), workflowruntime.FinishNodeAttemptRequest{InvocationID: started.Node.ID, AttemptNumber: started.Attempt.ID.Number, ExpectedNodeGeneration: started.Node.Generation, ExpectedAttemptGeneration: started.Attempt.Generation, Claim: proof, AttemptStatus: status, NextNodeStatus: status, Failure: failure, At: at})
				if err != nil {
					t.Fatal(err)
				}
				want := [][]any{{"host-run:product-work", "host-run", "product-work", "tool", wantStatus, wantMessage, wantError, "[]", "", wantMessage, "", workflowTime(workflowTestTime().Add(2 * time.Second)), workflowTime(at), workflowTime(at), nil}}
				got := sqlFacadeRows(t, f.product.DB, `SELECT id,workflow_run_id,step_id,kind,status,output,is_error,tool_calls_json,verify_json,error,gate_input,started_at,completed_at,updated_at,loop_run_id FROM workflow_run_steps`)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("explicit product row: got=%#v want=%#v", got, want)
				}
			}
		})
	}
}

func TestSQLFacadeExplicitWaitClassRows(t *testing.T) {
	for _, class := range []string{"gate", "flex", "loop"} {
		t.Run(class, func(t *testing.T) {
			for _, shared := range []bool{false, true} {
				t.Run(map[bool]string{false: "old", true: "released"}[shared], func(t *testing.T) {
					f := newSQLFacadeFixture(t, shared, true, PlanNodeProjection{NodeID: "work", ProductStepID: "product-work", ProductKind: class, WaitClass: class})
					run, started, proof := sqlFacadeStartAttempt(t, f)
					at := workflowTestTime().Add(3 * time.Second)
					schema, err := workflowwait.NewSchemaRef(graph.Schema{"type": "string"})
					if err != nil {
						t.Fatal(err)
					}
					digest, err := workflowwait.DigestToken("token")
					if err != nil {
						t.Fatal(err)
					}
					record := workflowwait.Record{Kind: workflowwait.KindCallback, Correlation: "correlation", ResumeSchema: schema, ResumeTokenDigest: digest, Visibility: workflowwait.VisibilityPrivate, Authority: workflowwait.ResponderAuthority{Kind: "test"}, WakeSource: workflowwait.WakeCallback, Status: workflowruntime.WaitOpen}
					var loopID any
					if class == "loop" {
						if checkErr := f.product.CreateGoal(t.Context(), &nanitestore.Goal{ID: "goal", Intent: "test loop"}); checkErr != nil {
							t.Fatal(checkErr)
						}
						if checkErr := f.product.CreateLoopRun(t.Context(), &nanitestore.LoopRun{ID: "child", GoalID: "goal", DefinitionName: "child"}); checkErr != nil {
							t.Fatal(checkErr)
						}
						record.Kind, record.WakeSource, record.Correlation = workflowwait.KindChildRun, workflowwait.WakeChildRun, "child"
						loopID = "child"
					}
					_, err = f.state.(workflowruntime.WaitStore).SuspendNodeWait(t.Context(), workflowruntime.SuspendNodeWaitRequest{Wait: workflowruntime.WaitSnapshot{Ref: workflowruntime.WaitRef{ID: "wait"}, Invocation: started.Node.ID, Record: record}, ExpectedNodeGeneration: started.Node.Generation, ExpectedAttemptGeneration: started.Attempt.Generation, Claim: proof, At: at})
					if err != nil {
						t.Fatal(err)
					}
					_, err = f.state.TransitionRun(t.Context(), workflowruntime.RunTransitionRequest{RunID: run.ID, ExpectedGeneration: run.Generation, To: workflowruntime.RunWaiting, At: at})
					if err != nil {
						t.Fatal(err)
					}
					want := [][]any{{"host-run:product-work", "host-run", "product-work", class, "waiting_on_" + class, "", int64(0), "[]", "", "", "", workflowTime(workflowTestTime().Add(2 * time.Second)), "", workflowTime(at), loopID}}
					got := sqlFacadeRows(t, f.product.DB, `SELECT id,workflow_run_id,step_id,kind,status,output,is_error,tool_calls_json,verify_json,error,gate_input,started_at,completed_at,updated_at,loop_run_id FROM workflow_run_steps`)
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("explicit waiting row: got=%#v want=%#v", got, want)
					}
					got = sqlFacadeRows(t, f.product.DB, `SELECT status,completed_at FROM workflow_runs`)
					if !reflect.DeepEqual(got, [][]any{{"waiting_on_" + class, ""}}) {
						t.Fatalf("explicit waiting run: %#v", got)
					}
					if checkErr := PreflightWorkflowStorage(t.Context(), f.product); checkErr != nil {
						t.Fatal(checkErr)
					}

					oldWait, loadErr := f.host.LoadWait(t.Context(), "wait")
					if loadErr != nil {
						t.Fatal(loadErr)
					}
					newWait, loadErr := f.host.SQLWorkflowStateStore.LoadWait(t.Context(), "wait")
					if loadErr != nil {
						t.Fatal(loadErr)
					}
					if !reflect.DeepEqual(oldWait, newWait) {
						t.Fatalf("cross-reader wait: old=%+v new=%+v", oldWait, newWait)
					}
				})
			}
		})
	}
}

func TestSQLFacadeScheduledRetryRowsAndCrossReaders(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(map[bool]string{false: "old", true: "released"}[shared], func(t *testing.T) {
			f := newSQLFacadeFixture(t, shared, true)
			run, started, proof := sqlFacadeStartAttempt(t, f)
			at := workflowTestTime().Add(3 * time.Second)
			result, err := f.state.ScheduleNodeRetry(t.Context(), workflowruntime.ScheduleNodeRetryRequest{Activation: workflowruntime.RetryActivationSnapshot{ID: "retry", Attempt: started.Attempt.ID, Status: workflowruntime.RetryScheduled, FireAt: at.Add(time.Minute), Failure: workflowruntime.Failure{Code: "transient", Message: "retry failure", Retryable: true}}, ExpectedNodeGeneration: started.Node.Generation, ExpectedAttemptGeneration: started.Attempt.Generation, Claim: proof, AttemptStatus: workflowruntime.NodeFailed, At: at})
			if err != nil {
				t.Fatal(err)
			}
			if result.Node.Status != workflowruntime.NodeWaiting {
				t.Fatalf("retry node: %+v", result.Node)
			}
			_, err = f.state.TransitionRun(t.Context(), workflowruntime.RunTransitionRequest{RunID: run.ID, ExpectedGeneration: run.Generation, To: workflowruntime.RunWaiting, At: at})
			if err != nil {
				t.Fatal(err)
			}
			want := [][]any{{"host-run:product-work", "host-run", "product-work", "tool", "waiting_on_gate", "", int64(0), "[]", "", "", "", workflowTime(workflowTestTime().Add(2 * time.Second)), "", workflowTime(at), nil}}
			got := sqlFacadeRows(t, f.product.DB, `SELECT id,workflow_run_id,step_id,kind,status,output,is_error,tool_calls_json,verify_json,error,gate_input,started_at,completed_at,updated_at,loop_run_id FROM workflow_run_steps`)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("retry product row: got=%#v want=%#v", got, want)
			}
			if got := sqlFacadeRows(t, f.product.DB, `SELECT status,completed_at FROM workflow_runs`); !reflect.DeepEqual(got, [][]any{{"running", ""}}) {
				t.Fatalf("scheduled retry must remain running: %#v", got)
			}
			oldNode, err := f.host.LoadNodeInvocation(t.Context(), started.Node.ID)
			if err != nil {
				t.Fatal(err)
			}
			newNode, err := f.host.SQLWorkflowStateStore.LoadNodeInvocation(t.Context(), started.Node.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(oldNode, newNode) {
				t.Fatalf("cross-reader node: old=%+v new=%+v", oldNode, newNode)
			}
			oldRetry, err := f.host.LoadRetryActivation(t.Context(), "retry")
			if err != nil {
				t.Fatal(err)
			}
			newRetry, err := f.host.SQLWorkflowStateStore.LoadRetryActivation(t.Context(), "retry")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(oldRetry, newRetry) {
				t.Fatalf("cross-reader retry: old=%+v new=%+v", oldRetry, newRetry)
			}
			if checkErr := PreflightWorkflowStorage(t.Context(), f.product); checkErr != nil {
				t.Fatal(checkErr)
			}
		})
	}
}

func TestSQLFacadeCrossReaderLiveAndExpiredLeases(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(map[bool]string{false: "old", true: "released"}[shared], func(t *testing.T) {
			f := newSQLFacadeFixture(t, shared, true)
			_, started, _ := sqlFacadeStartAttempt(t, f)
			for _, now := range []time.Time{workflowTestTime().Add(3 * time.Second), workflowTestTime().Add(2 * time.Hour)} {
				query := workflowruntime.RecoveryQuery{Now: now}
				old, err := f.host.Recovery(t.Context(), query)
				if err != nil {
					t.Fatal(err)
				}
				newer, err := f.host.SQLWorkflowStateStore.Recovery(t.Context(), query)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(old, newer) {
					t.Fatalf("cross-reader recovery: old=%+v new=%+v", old, newer)
				}
				if checkErr := PreflightWorkflowStorage(t.Context(), f.product); checkErr != nil {
					t.Fatal(checkErr)
				}
			}
			old, err := f.host.ListAttempts(t.Context(), started.Node.ID)
			if err != nil {
				t.Fatal(err)
			}
			newer, err := f.host.SQLWorkflowStateStore.ListAttempts(t.Context(), started.Node.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(old, newer) {
				t.Fatalf("cross-reader attempt: old=%+v new=%+v", old, newer)
			}
		})
	}
}

func TestSQLStoragePreflightKillRollback(t *testing.T) {
	if path := os.Getenv("NANITE_PREFLIGHT_TEST_DB"); path != "" {
		product, err := nanitestore.New(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		state, err := NewWorkflowStateStore(product)
		if err != nil {
			t.Fatal(err)
		}
		stage, _ := strconv.Atoi(os.Getenv("NANITE_PREFLIGHT_TEST_STAGE"))
		if stage == 0 {
			_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
			t.Fatal("kill returned")
		}
		err = state.write(t.Context(), "crash qualification", func(tx workflowSQL) error {
			if checkErr := preflightWorkflowSchema(t.Context(), tx); checkErr != nil {
				return checkErr
			}
			if stage == 1 {
				_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
				t.Fatal("kill returned")
			}
			if checkErr := preflightWorkflowReferences(t.Context(), tx); checkErr != nil {
				return checkErr
			}
			if checkErr := preflightWorkflowRecords(t.Context(), tx); checkErr != nil {
				return checkErr
			}
			if checkErr := preflightWorkflowRun(t.Context(), tx, "host-run"); checkErr != nil {
				return checkErr
			}
			if checkErr := preflightWorkflowIdempotency(t.Context(), tx); checkErr != nil {
				return checkErr
			}
			_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
			t.Fatal("kill returned")
			return nil
		})
		t.Fatalf("child did not reach kill: %v", err)
	}
	for stage := 0; stage < 3; stage++ {
		t.Run(strconv.Itoa(stage), func(t *testing.T) {
			f := newSQLFacadeFixture(t, false, true)
			_, _, _ = sqlFacadeStartAttempt(t, f)
			before := sqlFacadeSnapshot(t, f)
			path := f.product.DBPath(t.Context())
			if checkErr := f.product.Close(context.Background()); checkErr != nil {
				t.Fatal(checkErr)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(t.Context(), executable, "-test.run=^TestSQLStoragePreflightKillRollback$", "-test.timeout=20s") //nolint:gosec // The executable is this test binary, obtained from os.Executable.
			command.Env = append(os.Environ(), "NANITE_PREFLIGHT_TEST_DB="+path, "NANITE_PREFLIGHT_TEST_STAGE="+strconv.Itoa(stage), "HOME="+filepath.Join(t.TempDir(), "isolated-home"))
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				t.Fatalf("kill child result: %v %s", err, output)
			}
			status, ok := exit.Sys().(syscall.WaitStatus)
			if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
				t.Fatalf("child was not killed: %v %s", err, output)
			}
			reopened, err := nanitestore.New(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reopened.Close(context.Background()) })
			f.product = reopened
			if checkErr := PreflightWorkflowStorage(t.Context(), reopened); checkErr != nil {
				t.Fatal(checkErr)
			}
			assertSQLFacadeEqual(t, before, sqlFacadeSnapshot(t, f))
		})
	}
}

func TestSQLStoragePreflightCanceledStartupReleasesTransaction(t *testing.T) {
	f := newSQLFacadeFixture(t, false, true)
	_, _, _ = sqlFacadeStartAttempt(t, f)
	before := sqlFacadeSnapshot(t, f)
	ctx, cancel := context.WithCancel(t.Context())
	entered := false
	checkErr := f.host.SQLWorkflowStateStore.write(ctx, "mid-transaction cancellation", func(tx workflowSQL) error {
		entered = true
		if err := preflightWorkflowSchema(ctx, tx); err != nil {
			return err
		}
		cancel()
		return preflightWorkflowSnapshot(ctx, tx)
	})
	if !entered || !errors.Is(checkErr, context.Canceled) {
		t.Fatalf("callback entered=%v error=%v", entered, checkErr)
	}
	if checkErr := PreflightWorkflowStorage(t.Context(), f.product); checkErr != nil {
		t.Fatal(checkErr)
	}
	assertSQLFacadeEqual(t, before, sqlFacadeSnapshot(t, f))
}

func TestSQLStoragePreflightPreservesTerminalLegacyHistory(t *testing.T) {
	f := newSQLFacadeFixture(t, false, true)
	row := &nanitestore.WorkflowRunRow{ID: "legacy-terminal", DefinitionName: "historical definition", Status: "completed", InputJSON: `{"historic":"exact bytes ☃"}`, Error: "", StartedAt: workflowTestTime(), CompletedAt: workflowTestTime().Add(time.Second)}
	if createErr := f.product.CreateWorkflowRun(t.Context(), row); createErr != nil {
		t.Fatal(createErr)
	}
	before, loadErr := f.product.GetWorkflowRun(t.Context(), row.ID)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if preflightErr := PreflightWorkflowStorage(t.Context(), f.product); preflightErr != nil {
		t.Fatal(preflightErr)
	}
	after, loadErr := f.product.GetWorkflowRun(t.Context(), row.ID)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("legacy history changed: before=%+v after=%+v", before, after)
	}
}

func TestSQLStoragePreflightRejectsMissingOrDriftedMaterial(t *testing.T) {
	for _, statement := range []string{`DELETE FROM workflow_plan_materials`, `UPDATE workflow_plan_materials SET source_content=x'00'`} {
		t.Run(statement, func(t *testing.T) {
			f := newSQLFacadeFixture(t, false, true)
			if _, _, createErr := f.state.CreateRun(t.Context(), f.createRequest()); createErr != nil {
				t.Fatal(createErr)
			}
			var updateSQL, deleteSQL string
			if queryErr := f.product.DB.QueryRowContext(t.Context(), `SELECT sql FROM sqlite_master WHERE name='workflow_plan_materials_no_update'`).Scan(&updateSQL); queryErr != nil {
				t.Fatal(queryErr)
			}
			if queryErr := f.product.DB.QueryRowContext(t.Context(), `SELECT sql FROM sqlite_master WHERE name='workflow_plan_materials_no_delete'`).Scan(&deleteSQL); queryErr != nil {
				t.Fatal(queryErr)
			}
			for _, command := range []string{`PRAGMA foreign_keys=OFF`, `DROP TRIGGER workflow_plan_materials_no_update`, `DROP TRIGGER workflow_plan_materials_no_delete`, statement, updateSQL, deleteSQL, `PRAGMA foreign_keys=ON`} {
				if execErr := func() error { _, err := f.product.DB.ExecContext(t.Context(), command); return err }(); execErr != nil {
					t.Fatal(execErr)
				}
			}
			before := sqlFacadeSnapshot(t, f)
			if preflightErr := PreflightWorkflowStorage(t.Context(), f.product); preflightErr == nil {
				t.Fatal("missing/drifted exact source accepted")
			}
			assertSQLFacadeEqual(t, before, sqlFacadeSnapshot(t, f))
		})
	}
}

func TestSQLFacadeCrossReaderReceiptAndEventCursor(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(map[bool]string{false: "old", true: "released"}[shared], func(t *testing.T) {
			f := newSQLFacadeFixture(t, shared, true)
			run, _, _ := sqlFacadeStartAttempt(t, f)
			at := workflowTestTime().Add(3 * time.Second)
			request := externalExecutionRequest{Request: ExternalStepRequest{IdempotencyKey: "receipt", Engine: "langgraph", WorkflowName: "external", Params: map[string]any{"exact": "request"}}, ProductStepID: "product-work", ProductKind: "tool", NodeID: "work"}
			if shared {
				if _, prepareErr := f.host.prepareExternalExecution(t.Context(), string(run.ID), "work", "", request, at); prepareErr != nil {
					t.Fatal(prepareErr)
				}
			} else {
				encoded, digest, _, encodeErr := encodeExternalExecutionRequest(request)
				if encodeErr != nil {
					t.Fatal(encodeErr)
				}
				// The pre-adoption receipt writer uses the old transaction owner. Its
				// retained host encoding is the exact protocol read by the new facade.
				if writeErr := f.host.write(t.Context(), "old receipt writer", func(tx workflowSQL) error {
					_, err := tx.ExecContext(t.Context(), `INSERT INTO workflow_external_execution_receipts(idempotency_key,run_id,node_id,iteration,request_digest,request_json,state,created_at,updated_at) VALUES(?,?,?,?,?,?,'prepared',?,?)`, "receipt", run.ID, "work", "", digest, encoded, workflowTime(at), workflowTime(at))
					return err
				}); writeErr != nil {
					t.Fatal(writeErr)
				}
			}
			oldReceipt, loadErr := loadExternalExecutionReceipt(t.Context(), f.host.db, "receipt")
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			newReceipt, loadErr := f.host.LoadExternalExecutionReceipt(t.Context(), "receipt")
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			if !reflect.DeepEqual(oldReceipt, newReceipt) {
				t.Fatalf("cross-reader receipt: old=%+v new=%+v", oldReceipt, newReceipt)
			}
			for after := uint64(0); after < 6; after++ {
				query := workflowruntime.EventQuery{RunID: run.ID, AfterSequence: after, Limit: 2}
				oldEvents, loadErr := f.host.ListEvents(t.Context(), query)
				if loadErr != nil {
					t.Fatal(loadErr)
				}
				newEvents, loadErr := f.host.SQLWorkflowStateStore.ListEvents(t.Context(), query)
				if loadErr != nil {
					t.Fatal(loadErr)
				}
				if !reflect.DeepEqual(oldEvents, newEvents) {
					t.Fatalf("cross-reader cursor %d: old=%+v new=%+v", after, oldEvents, newEvents)
				}
			}
			if preflightErr := PreflightWorkflowStorage(t.Context(), f.product); preflightErr != nil {
				t.Fatal(preflightErr)
			}
		})
	}
}

func TestSQLFacadePopulatedReactorContinuationReopen(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(map[bool]string{false: "old", true: "released"}[shared], func(t *testing.T) {
			f := newSQLFacadeFixture(t, shared, true)
			state := f.state.(workflowruntime.ReactorStore)
			for _, id := range []workflowruntime.RunID{"reactor-run-1", "reactor-run-2"} {
				request := f.createRequest()
				request.ID = id
				request.StartIdempotencyKey = string(id)
				if _, _, createErr := f.state.CreateRun(t.Context(), request); createErr != nil {
					t.Fatal(createErr)
				}
			}
			base := workflowTestTime()
			digest := f.material.Plan.Digest
			provenance := values.SHA256Digest([]byte("reactor-provenance"))
			identity := workflowruntime.ReactorIdentity{ID: "reactor-fixture", RegistrationID: "events", RegistrationGeneration: 3, Correlation: "project-1",
				Definition: graph.DefinitionRef{Authority: "project", Kind: "workflow", ID: "fixture", Version: "v1", Digest: digest},
				Plan:       planRef(f.material.Plan), Provenance: graph.Provenance{Authority: "project", Origin: "source", Digest: provenance}}
			payload, err := values.NewInline(map[string]any{"sequence": 1}, values.Metadata{Producer: values.Producer{Kind: "test", Reference: "delivery"}, MediaType: "application/json", Redaction: values.RedactionPrivate, Retention: values.RetentionRun})
			if err != nil {
				t.Fatal(err)
			}
			delivery := workflowruntime.ReactorDeliveryRequest{ReactorID: identity.ID, IdempotencyKey: "delivery-1", SignalName: "project.changed", Payload: payload,
				Responder: workflowwait.Responder{Kind: "test", Reference: "source"}, OccurredAt: base, ReceivedAt: base}
			reactor, first, outcome, err := state.BeginReactorDelivery(context.Background(), workflowruntime.BeginReactorDeliveryRequest{Identity: identity, InitialRunID: "reactor-run-1", ContinueAfterEvents: 1, Delivery: delivery, At: base})
			if err != nil || outcome != workflowruntime.IdempotencyApplied || first.Status != workflowruntime.ReactorDeliveryPending || reactor.Status != workflowruntime.ReactorStarting {
				t.Fatalf("BeginReactorDelivery = %#v / %#v / %s, %v", reactor, first, outcome, err)
			}
			reactor, err = state.MarkReactorWaiting(context.Background(), identity.ID, reactor.Generation, base.Add(time.Second))
			if err != nil || reactor.Status != workflowruntime.ReactorWaiting {
				t.Fatalf("MarkReactorWaiting = %#v, %v", reactor, err)
			}
			claimed, err := state.ClaimReactorDelivery(context.Background(), workflowruntime.ClaimReactorDeliveryRequest{ReactorID: identity.ID, IdempotencyKey: delivery.IdempotencyKey, ExpectedGeneration: first.Generation, At: base.Add(2 * time.Second)})
			if err != nil || claimed.Status != workflowruntime.ReactorDeliveryApplying {
				t.Fatalf("ClaimReactorDelivery = %#v, %v", claimed, err)
			}
			receipt := workflowruntime.ReactorDeliveryReceipt{Kind: workflowruntime.ReactorDeliveryStartedRun, RunID: "reactor-run-1", ProcessedAt: base.Add(3 * time.Second)}
			reactor, completed, err := state.CompleteReactorDelivery(context.Background(), workflowruntime.CompleteReactorDeliveryRequest{ReactorID: identity.ID, IdempotencyKey: delivery.IdempotencyKey, ExpectedGeneration: claimed.Generation, Status: workflowruntime.ReactorDeliveryApplied, Receipt: receipt, At: base.Add(3 * time.Second)})
			if err != nil || completed.Status != workflowruntime.ReactorDeliveryApplied || reactor.EventCount != 1 {
				t.Fatalf("CompleteReactorDelivery = %#v / %#v, %v", reactor, completed, err)
			}
			_, replay, replayOutcome, err := state.BeginReactorDelivery(context.Background(), workflowruntime.BeginReactorDeliveryRequest{Identity: identity, InitialRunID: "reactor-run-1", ContinueAfterEvents: 1, Delivery: delivery, At: base.Add(4 * time.Second)})
			if err != nil || replayOutcome != workflowruntime.IdempotencyReplayed || replay.Status != workflowruntime.ReactorDeliveryApplied {
				t.Fatalf("delivery replay = %#v / %s, %v", replay, replayOutcome, err)
			}

			payload2, _ := values.NewInline(map[string]any{"sequence": 2}, values.Metadata{Producer: values.Producer{Kind: "test", Reference: "delivery"}, MediaType: "application/json", Redaction: values.RedactionPrivate, Retention: values.RetentionRun})
			delivery2 := delivery
			delivery2.IdempotencyKey, delivery2.Payload, delivery2.OccurredAt, delivery2.ReceivedAt = "delivery-2", payload2, base.Add(4*time.Second), base.Add(4*time.Second)
			reactor, queued, _, err := state.BeginReactorDelivery(context.Background(), workflowruntime.BeginReactorDeliveryRequest{Identity: identity, InitialRunID: "reactor-run-1", ContinueAfterEvents: 1, Delivery: delivery2, At: base.Add(4 * time.Second)})
			if err != nil {
				t.Fatal(err)
			}
			if _, claimErr := state.ClaimReactorDelivery(context.Background(), workflowruntime.ClaimReactorDeliveryRequest{ReactorID: identity.ID,
				IdempotencyKey: delivery2.IdempotencyKey, ExpectedGeneration: queued.Generation, WaitID: "wait-after-ceiling", At: base.Add(5 * time.Second)}); !errors.Is(claimErr, workflowruntime.ErrReactorRolling) {
				t.Fatalf("delivery beyond max_events ceiling = %v", claimErr)
			}
			if preflightErr := PreflightWorkflowStorage(t.Context(), f.product); preflightErr != nil {
				t.Fatal(preflightErr)
			}
			stateValue, _ := values.NewInline("cursor-1", values.Metadata{Producer: values.Producer{Kind: "test", Reference: "state"}, MediaType: "application/json", Redaction: values.RedactionPrivate, Retention: values.RetentionRun})
			rolling, continuation, _, err := state.BeginReactorContinuation(context.Background(), workflowruntime.ReactorContinuationRequest{IdempotencyKey: "continue-1", ReactorID: identity.ID,
				ExpectedGeneration: reactor.Generation, FromGeneration: 1, FromRunID: "reactor-run-1", ToRunID: "reactor-run-2", State: values.ValueSet{"cursor": stateValue}, At: base.Add(5 * time.Second)})
			if err != nil || rolling.Status != workflowruntime.ReactorRolling {
				t.Fatalf("BeginReactorContinuation = %#v / %#v, %v", rolling, continuation, err)
			}
			if _, claimErr := state.ClaimReactorDelivery(context.Background(), workflowruntime.ClaimReactorDeliveryRequest{ReactorID: identity.ID, IdempotencyKey: delivery2.IdempotencyKey, ExpectedGeneration: queued.Generation, At: base.Add(6 * time.Second)}); !errors.Is(claimErr, workflowruntime.ErrReactorRolling) {
				t.Fatalf("rolling delivery claim = %v", claimErr)
			}
			continued, sealed, err := state.CompleteReactorContinuation(context.Background(), continuation.Request.IdempotencyKey, continuation.Generation, base.Add(6*time.Second))
			if err != nil || sealed.Status != workflowruntime.ReactorContinuationCompleted || continued.CurrentGeneration != 2 || continued.CurrentRunID != "reactor-run-2" || continued.EventCount != 0 {
				t.Fatalf("CompleteReactorContinuation = %#v / %#v, %v", continued, sealed, err)
			}
			reassigned, err := state.LoadReactorDelivery(context.Background(), identity.ID, delivery2.IdempotencyKey)
			if err != nil || reassigned.ReactorGeneration != 2 || reassigned.RunID != "reactor-run-2" || reassigned.Status != workflowruntime.ReactorDeliveryPending {
				t.Fatalf("reassigned delivery = %#v, %v", reassigned, err)
			}

			if preflightErr := PreflightWorkflowStorage(t.Context(), f.product); preflightErr != nil {
				t.Fatal(preflightErr)
			}
			path := f.product.DBPath(t.Context())
			if closeErr := f.product.Close(context.Background()); closeErr != nil {
				t.Fatal(closeErr)
			}
			reopened, openErr := nanitestore.New(t.Context(), path)
			if openErr != nil {
				t.Fatal(openErr)
			}
			t.Cleanup(func() { _ = reopened.Close(context.Background()) })
			if preflightErr := PreflightWorkflowStorage(t.Context(), reopened); preflightErr != nil {
				t.Fatal(preflightErr)
			}
			newReader, openErr := NewWorkflowStateStore(reopened)
			if openErr != nil {
				t.Fatal(openErr)
			}
			oldReader, openErr := newLegacyWorkflowStateStore(reopened)
			if openErr != nil {
				t.Fatal(openErr)
			}
			oldReactor, loadErr := oldReader.LoadReactor(t.Context(), identity.ID)
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			newReactor, loadErr := newReader.LoadReactor(t.Context(), identity.ID)
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			if !reflect.DeepEqual(oldReactor, newReactor) {
				t.Fatalf("cross-reader continued reactor: old=%+v new=%+v", oldReactor, newReactor)
			}
			_, replayedContinuation, replayErr := newReader.CompleteReactorContinuation(t.Context(), "continue-1", continuation.Generation, base.Add(7*time.Second))
			if replayErr != nil || replayedContinuation.Status != workflowruntime.ReactorContinuationCompleted {
				t.Fatalf("continuation after reopen: %+v %v", replayedContinuation, replayErr)
			}

		})
	}
}

// The installed Nanite tool adapter cannot author reversible effects. Storage
// adoption must preserve this compiler floor rather than invent frozen identity.
func TestSQLFacadeCompensationAuthoringRetainsReversibilityFloor(t *testing.T) {
	registry, registryErr := newFrozenRegistry(&recordingStepExecutor{})
	if registryErr != nil {
		t.Fatal(registryErr)
	}
	workflow := graph.Graph{ID: "compensation-adoption", Version: "v1", Compensation: &graph.CompensationPolicy{Triggers: []graph.CompensationTrigger{graph.CompensationManual}}, Nodes: []graph.Node{
		{ID: "work", Kind: StepKindTool, KindVersion: StepKindVersion, Config: graph.Config{"tool": "noop"}, Compensation: &graph.CompensationSpec{Handler: "undo"}},
		{ID: "undo", Kind: StepKindTool, KindVersion: StepKindVersion, Config: graph.Config{"tool": "noop"}},
	}}
	source, marshalErr := json.Marshal(workflow)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	compiled := compile.CompileGraph(workflow, compile.GraphCompileOptions{SourceFormat: graph.SourceAgent, SourceDigest: values.SHA256Digest(source)})
	if compiled.Plan == nil || hasDiagnosticErrors(compiled.Diagnostics) {
		t.Fatalf("compile compensation: %#v", compiled.Diagnostics)
	}
	_, compileErr := finalizeCompiledPlan(t.Context(), *compiled.Plan, compile.ValueVisibilityPlan{}, "nanite-agent:compensation-adoption@v1", graph.SourceAgent, source, registry)
	if compileErr == nil || !strings.Contains(compileErr.Error(), "HADR-SOURCE-038") {
		t.Fatalf("reversibility floor changed: %v", compileErr)
	}

	f := newSQLFacadeFixture(t, false, true)
	if preflightErr := PreflightWorkflowStorage(t.Context(), f.product); preflightErr != nil {
		t.Fatalf("normal startup blocked: %v", preflightErr)
	}
	// Plant an internally digest-consistent unsupported source-bound row, as an
	// old writer or a restore could supply. Do not invent a reversible catalog.
	inferred := compile.InferValueDependencies(compiled.Plan, compile.DependencyOptions{})
	if inferred.Plan == nil || hasDiagnosticErrors(inferred.Diagnostics) {
		t.Fatalf("infer fixture: %#v", inferred.Diagnostics)
	}
	material := f.material
	material.Plan, material.Visibility = *inferred.Plan, inferred.Visibility
	material.SourceFormat, material.SourceLocator, material.SourceContent, material.SourceDigest = graph.SourceAgent, "nanite-agent:compensation-adoption@v1", source, values.SHA256Digest(source)
	material.ProductDefinitionName = workflow.ID
	if recordErr := f.host.RecordPlanMaterial(t.Context(), material); recordErr != nil {
		t.Fatal(recordErr)
	}
	revision, publishErr := f.host.publishDefinition(t.Context(), material)
	if publishErr != nil {
		t.Fatal(publishErr)
	}
	f.material, f.revision = material, revision.RevisionID
	if _, _, createErr := f.state.CreateRun(t.Context(), f.createRequest()); createErr != nil {
		t.Fatal(createErr)
	}
	before := sqlFacadeSnapshot(t, f)
	preflightErr := PreflightWorkflowStorage(t.Context(), f.product)
	if preflightErr == nil || !strings.Contains(preflightErr.Error(), `run "host-run" blocked`) || !strings.Contains(preflightErr.Error(), "HADR-SOURCE-038") || !strings.Contains(preflightErr.Error(), "roll back to the previous binary") {
		t.Fatalf("unsupported row diagnostic: %v", preflightErr)
	}
	assertSQLFacadeEqual(t, before, sqlFacadeSnapshot(t, f))
}

func TestSQLFacadeOldSchemaCompensationRecoveryRetainsTerminalWarnings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "compensation-old-schema.db")
	_, state := openWorkflowStateTest(t, path)
	// This existing host-neutral fixture persists a saga, closes the database,
	// reopens the real migrated schema, activates its handler and seals the saga.
	if recoveryErr := runNaniteCompensationRecovery(t.Context(), state); recoveryErr != nil {
		t.Fatal(recoveryErr)
	}
	reopened, openErr := nanitestore.New(t.Context(), path)
	if openErr != nil {
		t.Fatal(openErr)
	}
	t.Cleanup(func() { _ = reopened.Close(context.Background()) })
	current, openErr := NewWorkflowStateStore(reopened)
	if openErr != nil {
		t.Fatal(openErr)
	}
	old, openErr := newLegacyWorkflowStateStore(reopened)
	if openErr != nil {
		t.Fatal(openErr)
	}
	newLedger, loadErr := current.LoadCompensationLedger(t.Context(), "comp-recovery")
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	oldLedger, loadErr := old.LoadCompensationLedger(t.Context(), "comp-recovery")
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if !reflect.DeepEqual(newLedger, oldLedger) || newLedger.Outcome != workflowruntime.CompensationOutcomeSucceeded {
		t.Fatalf("compensation cross-reader: old=%+v new=%+v", oldLedger, newLedger)
	}
	// Terminal host-neutral history remains readable without fabricating source.
	report := newWorkflowPreflight(true)
	if preflightErr := current.write(t.Context(), "terminal compensation preflight", func(tx workflowSQL) error {
		return preflightWorkflowSnapshot(context.WithValue(t.Context(), workflowPreflightKey{}, report), tx)
	}); preflightErr != nil || report.warnings == 0 {
		t.Fatalf("terminal compensation warnings=%d error=%v", report.warnings, preflightErr)
	}
}
