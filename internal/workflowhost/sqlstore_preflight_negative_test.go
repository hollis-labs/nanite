package workflowhost

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/hollis-labs/go-workflow/graph"
	workflowruntime "github.com/hollis-labs/go-workflow/runtime"
	"github.com/hollis-labs/go-workflow/values"
)

func preflightPlantSQL(t *testing.T, f sqlFacadeFixture, statement string, args ...any) {
	t.Helper()
	if _, err := f.product.DB.ExecContext(t.Context(), statement, args...); err != nil {
		t.Fatal(err)
	}
}

func preflightMutateImmutable(t *testing.T, f sqlFacadeFixture, trigger, statement string, args ...any) {
	t.Helper()
	var restore string
	if err := f.product.DB.QueryRowContext(t.Context(), `SELECT sql FROM sqlite_master WHERE name=?`, trigger).Scan(&restore); err != nil {
		t.Fatal(err)
	}
	preflightPlantSQL(t, f, `DROP TRIGGER "`+trigger+`"`)
	preflightPlantSQL(t, f, statement, args...)
	preflightPlantSQL(t, f, restore)
}

func plantPreflightReactor(t *testing.T, f sqlFacadeFixture, invalid bool) {
	t.Helper()
	at := workflowTestTime()
	digest := f.material.Plan.Digest
	snapshot := workflowruntime.ReactorSnapshot{Identity: workflowruntime.ReactorIdentity{ID: "reactor-record", RegistrationID: "events", RegistrationGeneration: 1, Correlation: "project-1", Definition: graph.DefinitionRef{Authority: "project", Kind: "workflow", ID: "fixture", Version: "v1", Digest: digest}, Plan: planRef(f.material.Plan), Provenance: graph.Provenance{Authority: "project", Origin: "source", Digest: values.SHA256Digest([]byte("provenance"))}}, CurrentGeneration: 1, CurrentRunID: "host-run", ContinueAfterEvents: 10, Status: workflowruntime.ReactorWaiting, Generation: 1, CreatedAt: at, UpdatedAt: at}
	if err := snapshot.Validate(); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if invalid {
		encoded = []byte(`{}`)
	}
	preflightPlantSQL(t, f, `INSERT INTO workflow_reactors(reactor_id,registration_id,registration_generation,correlation,current_generation,current_run_id,continue_after_events,event_count,status,generation,snapshot_json,created_at,updated_at) VALUES('reactor-record','events',1,'project-1',1,'host-run',10,0,'waiting',1,?,?,?)`, string(encoded), workflowTime(at), workflowTime(at))
}

func TestSQLPreflightNegativeChecksNameRows(t *testing.T) {
	cases := []struct {
		name, validation, table, id string
		plant                       func(*testing.T, sqlFacadeFixture)
	}{
		{"foreign-key-enforcement", "foreign-key-enforcement", "PRAGMA foreign_keys", "connection", func(t *testing.T, f sqlFacadeFixture) { preflightPlantSQL(t, f, `PRAGMA foreign_keys=OFF`) }},
		{"ledger-floor", "migration-ledger", "goose_db_version", "max applied version", func(t *testing.T, f sqlFacadeFixture) {
			preflightPlantSQL(t, f, `UPDATE goose_db_version SET is_applied=0 WHERE version_id>=172`)
		}},
		{"claim-replay-lease", "claim-replay", "workflow_claim_idempotency", "claim", func(t *testing.T, f sqlFacadeFixture) {
			var encoded string
			if err := f.product.DB.QueryRowContext(t.Context(), `SELECT result_json FROM workflow_claim_idempotency WHERE idempotency_key='claim'`).Scan(&encoded); err != nil {
				t.Fatal(err)
			}
			var result workflowruntime.ClaimResult
			if err := json.Unmarshal([]byte(encoded), &result); err != nil {
				t.Fatal(err)
			}
			result.Lease.Owner = "wrong-owner"
			bytes, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			preflightPlantSQL(t, f, `UPDATE workflow_claim_idempotency SET result_json=? WHERE idempotency_key='claim'`, string(bytes))
		}},
		{"claim-lease-generation", "claim-lease-generations", "workflow_node_leases", "work", func(t *testing.T, f sqlFacadeFixture) {
			preflightPlantSQL(t, f, `UPDATE workflow_node_leases SET generation=2 WHERE run_id='host-run'`)
		}},
		{"json-envelope", "envelopes", "workflow_memo_entries", "entry_json", func(t *testing.T, f sqlFacadeFixture) {
			preflightPlantSQL(t, f, `INSERT INTO workflow_memo_entries(cache_key,source_run_id,source_node_id,source_attempt,entry_json,created_at,expires_at) VALUES('memo','host-run','work',1,'not-json',?,?)`, workflowTime(workflowTestTime()), workflowTime(workflowTestTime()))
		}},
		{"generation-type", "envelopes", "workflow_services", "generation", func(t *testing.T, f sqlFacadeFixture) {
			preflightPlantSQL(t, f, `INSERT INTO workflow_services(run_id,node_id,status,generation,updated_at,snapshot_json) VALUES('host-run','work','ready',1.5,?,'{}')`, workflowTime(workflowTestTime()))
		}},
		{"foreign-key-row", "foreign-keys", "workflow_services", "rowid=", func(t *testing.T, f sqlFacadeFixture) {
			preflightPlantSQL(t, f, `PRAGMA foreign_keys=OFF`)
			preflightPlantSQL(t, f, `INSERT INTO workflow_services(run_id,node_id,status,generation,updated_at,snapshot_json) VALUES('host-run','missing-node','ready',1,?,'{}')`, workflowTime(workflowTestTime()))
			preflightPlantSQL(t, f, `PRAGMA foreign_keys=ON`)
		}},
		{"node-decoder", "records:nodes", "workflow_node_invocations", "work", func(t *testing.T, f sqlFacadeFixture) {
			preflightPlantSQL(t, f, `PRAGMA ignore_check_constraints=ON`)
			preflightPlantSQL(t, f, `UPDATE workflow_node_invocations SET generation=0 WHERE run_id='host-run'`)
			preflightPlantSQL(t, f, `PRAGMA ignore_check_constraints=OFF`)
		}},
		{"attempt-decoder", "records:attempts", "workflow_attempts", "attempt_number=1", func(t *testing.T, f sqlFacadeFixture) {
			preflightPlantSQL(t, f, `PRAGMA ignore_check_constraints=ON`)
			preflightPlantSQL(t, f, `UPDATE workflow_attempts SET generation=0 WHERE run_id='host-run'`)
			preflightPlantSQL(t, f, `PRAGMA ignore_check_constraints=OFF`)
		}},
		{"wait-decoder", "records:waits", "workflow_waits", "decoder-wait", func(t *testing.T, f sqlFacadeFixture) {
			preflightPlantSQL(t, f, `INSERT INTO workflow_waits(wait_id,run_id,node_id,status,generation,created_at,updated_at,record_json) VALUES('decoder-wait','host-run','work','open',1,?,?,'{}')`, workflowTime(workflowTestTime()), workflowTime(workflowTestTime()))
		}},
		{"event-decoder", "records:events", "workflow_events", "host-run", func(t *testing.T, f sqlFacadeFixture) {
			preflightMutateImmutable(t, f, "workflow_events_reject_update", `UPDATE workflow_events SET attributes_json='[]' WHERE run_id='host-run'`)
		}},
		{"reactor-decoder", "records:reactors", "workflow_reactors", "reactor-record", func(t *testing.T, f sqlFacadeFixture) { plantPreflightReactor(t, f, true) }},
		{"external-operation-decoder", "records:external-operations", "workflow_external_operations", "attempt_number=1", func(t *testing.T, f sqlFacadeFixture) {
			preflightPlantSQL(t, f, `INSERT INTO workflow_external_operations(run_id,node_id,attempt_number,ref_json,invocation_json,status,generation,created_at,updated_at) VALUES('host-run','work',1,'{}','{}','waiting',1,?,?)`, workflowTime(workflowTestTime()), workflowTime(workflowTestTime()))
		}},
		{"value-set-load", "value-sets", "workflow_value_sets", "sequence=", func(t *testing.T, f sqlFacadeFixture) {
			preflightPlantSQL(t, f, `INSERT INTO workflow_value_sets(digest,owner_json,values_json) VALUES(?,'{}','{}')`, values.SHA256Digest([]byte("wrong-values")))
		}},
		{"standalone-material-load", "frozen-material", "workflow_plan_materials", "plan_digest=", func(t *testing.T, f sqlFacadeFixture) {
			registry, err := newFrozenRegistry(&recordingStepExecutor{})
			if err != nil {
				t.Fatal(err)
			}
			material, err := CompileSource(t.Context(), f.material.SourceLocator, append([]byte("# unused material\n"), f.material.SourceContent...), registry)
			if err != nil {
				t.Fatal(err)
			}
			material.CreatedAt = workflowTestTime()
			if err := f.host.RecordPlanMaterial(t.Context(), material); err != nil {
				t.Fatal(err)
			}
			preflightMutateImmutable(t, f, "workflow_plan_materials_no_update", `UPDATE workflow_plan_materials SET source_content=x'00' WHERE plan_digest=?`, material.Plan.Digest)
		}},
		{"compensation-ledger-load", "compensation-ledgers", "workflow_compensation_ledgers", "host-run", func(t *testing.T, f sqlFacadeFixture) {
			preflightPlantSQL(t, f, `INSERT INTO workflow_compensation_ledgers(run_id,plan_digest,status,generation,updated_at,snapshot_json) VALUES('host-run',?,'frozen',1,?,'{}')`, f.material.Plan.Digest, workflowTime(workflowTestTime()))
		}},
		{"terminal-intent-load", "terminal-intents", "workflow_terminal_intents", "host-run", func(t *testing.T, f sqlFacadeFixture) {
			preflightPlantSQL(t, f, `INSERT INTO workflow_terminal_intents(run_id,intended_status,status,idempotency_key,generation,created_at,updated_at,immutable_json,snapshot_json) VALUES('host-run','failed','pending','intent',1,?,?,'{}','{}')`, workflowTime(workflowTestTime()), workflowTime(workflowTestTime()))
		}},
		{"reactor-continuation-load", "reactor-continuations", "workflow_reactor_continuations", "continuation", func(t *testing.T, f sqlFacadeFixture) {
			plantPreflightReactor(t, f, false)
			preflightPlantSQL(t, f, `INSERT INTO workflow_reactor_continuations(idempotency_key,reactor_id,from_generation,to_generation,from_run_id,to_run_id,status,generation,request_json,created_at,updated_at) VALUES('continuation','reactor-record',1,2,'host-run','future-run','pending',1,'{}',?,?)`, workflowTime(workflowTestTime()), workflowTime(workflowTestTime()))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSQLFacadeFixture(t, false, true)
			sqlFacadeStartAttempt(t, f)
			if err := PreflightWorkflowStorage(t.Context(), f.product); err != nil {
				t.Fatalf("baseline is not qualified: %v", err)
			}
			tc.plant(t, f)
			state, err := collectWorkflowPreflight(t, f)
			if err == nil {
				t.Fatal("planted defect accepted")
			}
			found := false
			for _, finding := range state.findings {
				if finding.Severity == "fatal" && finding.Validation == tc.validation && finding.Table == tc.table && strings.Contains(finding.Row, tc.id) {
					found = true
					if finding.Expected == "" || finding.Actual == "" {
						t.Fatalf("missing expected/actual: %+v", finding)
					}
				}
			}
			if !found {
				t.Fatalf("missing named %s/%s/%s refusal: %v findings=%+v", tc.validation, tc.table, tc.id, err, state.findings)
			}
			if startupErr := PreflightWorkflowStorage(t.Context(), f.product); startupErr == nil || !strings.Contains(startupErr.Error(), "table=") || !strings.Contains(startupErr.Error(), "row=") || !strings.Contains(startupErr.Error(), "expected=") || !strings.Contains(startupErr.Error(), "actual=") {
				t.Fatalf("startup diagnostic=%v", startupErr)
			}
		})
	}
}

func TestSQLPreflightSchemaDriftNamesBothDigests(t *testing.T) {
	f := newSQLFacadeFixture(t, false, true)
	preflightPlantSQL(t, f, `DROP INDEX idx_workflow_definition_revisions_exact_plan`)
	preflightPlantSQL(t, f, `CREATE UNIQUE INDEX idx_workflow_definition_revisions_exact_plan ON workflow_definition_revisions(definition_name,compiled_plan_digest,engine_kind,engine_contract_version)`)
	state, err := collectWorkflowPreflight(t, f)
	if err == nil {
		t.Fatal("schema drift accepted")
	}
	for _, finding := range state.findings {
		if finding.Row == "index:idx_workflow_definition_revisions_exact_plan" {
			if len(finding.Expected) != 64 || len(finding.Actual) != 64 || finding.Expected == finding.Actual {
				t.Fatalf("digest diagnostic=%+v", finding)
			}
			return
		}
	}
	t.Fatal(fmt.Sprintf("missing named schema object: %+v", state.findings))
}
