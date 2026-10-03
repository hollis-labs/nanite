package workflowhost

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	workflowruntime "github.com/hollis-labs/go-workflow/runtime"
	"github.com/hollis-labs/go-workflow/values"
	"github.com/hollis-labs/nanite/internal/agentworkflow"
	nanitestore "github.com/hollis-labs/nanite/internal/store"
)

// PreflightWorkflowStorage qualifies existing persistence before any worker or
// launch surface starts. It never repairs records or applies library DDL.
// Deployment must stop the old service and verify no other Nanite writer exists;
// a transaction protects this snapshot, not process ownership after it returns.
func PreflightWorkflowStorage(ctx context.Context, product *nanitestore.Store) error {
	store, err := NewSQLWorkflowStateStore(product)
	if err != nil {
		return err
	}
	return store.write(ctx, "workflow storage preflight", func(tx workflowSQL) error {
		return preflightWorkflowSnapshot(ctx, tx)
	})
}

func preflightWorkflowSnapshot(ctx context.Context, tx workflowSQL) error {
	state := preflightState(ctx)
	ctx = context.WithValue(ctx, workflowPreflightKey{}, state)
	for _, check := range []func(context.Context, workflowSQL) error{
		preflightWorkflowSchema, preflightWorkflowReferences, preflightWorkflowEnvelopes,
		preflightWorkflowRecords, preflightWorkflowRuns, preflightWorkflowIdempotency,
	} {
		if err := check(ctx, tx); err != nil {
			return err
		}
	}
	if !state.collect {
		slog.Info("workflow storage preflight complete", "runs_scanned", state.counts["run-bindings"], "distinct_plans", len(state.plans), "warnings", state.warnings, "fatal_findings", state.fatals)
	}
	return state.result()
}

func preflightWorkflowRuns(ctx context.Context, tx workflowSQL) error {
	s := preflightState(ctx)
	rows, err := tx.QueryContext(ctx, `SELECT id FROM workflow_runs ORDER BY id`)
	if err != nil {
		return s.finding("fatal", "run-bindings", "workflow_runs", "scan", "readable runs", err)
	}
	defer closeRows(rows)
	s.count("run-bindings", 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return s.finding("fatal", "run-bindings", "workflow_runs", "scan", "readable run id", err)
		}
		s.count("run-bindings", 1)
		if err := preflightWorkflowRun(ctx, tx, workflowruntime.RunID(id)); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return s.finding("fatal", "run-bindings", "workflow_runs", "scan", "complete run scan", err)
	}
	return nil
}

// Schema validation has already attested these identifiers. Check parked
// state too, including idempotency and scheduler records outside active runs.
func preflightWorkflowEnvelopes(ctx context.Context, tx workflowSQL) error {
	s := preflightState(ctx)
	for _, key := range preflightSchemaKeys() {
		if !strings.HasPrefix(key, "table:") {
			continue
		}
		table := strings.TrimPrefix(key, "table:")
		rows, err := tx.QueryContext(ctx, `PRAGMA table_info("`+table+`")`) //nolint:gosec // Table names come from the compiled schema manifest.
		if err != nil {
			if err := s.finding("fatal", "envelopes", table, "columns", "readable columns", err); err != nil {
				return err
			}
			continue
		}
		type columnCheck struct{ name, predicate, expected string }
		var checks []columnCheck
		var identity []string
		for rows.Next() {
			var cid, notNull, primaryKey int
			var name, kind string
			var defaultValue sql.NullString
			if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
				closeRows(rows)
				return s.finding("fatal", "envelopes", table, "columns", "readable column declaration", err)
			}
			column := `"` + name + `"`
			if primaryKey > 0 {
				identity = append(identity, `'`+name+`',CAST(`+column+` AS TEXT)`)
			}
			if strings.HasSuffix(name, "_json") {
				checks = append(checks, columnCheck{name, `(` + column + ` IS NOT NULL AND ` + column + `<>'' AND NOT json_valid(` + column + `))`, "NULL, empty or valid JSON"})
			}
			if name == "generation" || name == "runtime_generation" || name == "claim_generation" {
				checks = append(checks, columnCheck{name, `(` + column + ` IS NOT NULL AND (typeof(` + column + `) <> 'integer' OR ` + column + `<0))`, "NULL or nonnegative integer"})
			}
		}
		err = rows.Err()
		closeRows(rows)
		if err != nil {
			return s.finding("fatal", "envelopes", table, "columns", "complete column scan", err)
		}
		rowExpression := `printf('rowid=%d',rowid)`
		if len(identity) > 0 {
			rowExpression = `json_object(` + strings.Join(identity, ",") + `)`
		}
		for _, check := range checks {
			statement := `SELECT ` + rowExpression + `,quote("` + check.name + `"),typeof("` + check.name + `") FROM "` + table + `" WHERE ` + check.predicate
			rows, err := tx.QueryContext(ctx, statement)
			if err != nil {
				if err := s.finding("fatal", "envelopes", table, "column="+check.name, check.expected, err); err != nil {
					return err
				}
				continue
			}
			for rows.Next() {
				var id, value, kind string
				if err := rows.Scan(&id, &value, &kind); err != nil {
					closeRows(rows)
					return s.finding("fatal", "envelopes", table, "scan", check.expected, err)
				}
				if err := s.finding("fatal", "envelopes", table, id+" column="+check.name, check.expected, value+" ("+kind+")"); err != nil {
					closeRows(rows)
					return err
				}
			}
			err = rows.Err()
			closeRows(rows)
			if err != nil {
				return s.finding("fatal", "envelopes", table, "scan", check.expected, err)
			}
			var count int64
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM "`+table+`"`).Scan(&count); err != nil {
				return s.finding("fatal", "envelopes", table, "scan", "counted source rows", err)
			}
			s.count("envelopes:"+table+":"+check.name, count)
		}
	}
	return nil
}

func preflightWorkflowSchema(ctx context.Context, tx workflowSQL) error {
	s := preflightState(ctx)
	var foreignKeys int
	if err := tx.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		return s.finding("fatal", "foreign-key-enforcement", "PRAGMA foreign_keys", "connection", "1", err)
	}
	s.count("foreign-key-enforcement", 1)
	if foreignKeys != 1 {
		if err := s.finding("fatal", "foreign-key-enforcement", "PRAGMA foreign_keys", "connection", "1", foreignKeys); err != nil {
			return err
		}
	}
	var version sql.NullInt64
	var ledgerRows int64
	if err := tx.QueryRowContext(ctx, `SELECT max(version_id),count(*) FROM goose_db_version WHERE is_applied=1`).Scan(&version, &ledgerRows); err != nil {
		return s.finding("fatal", "migration-ledger", "goose_db_version", "is_applied=1", "applied ledger floor >=172", err)
	}
	s.count("migration-ledger", ledgerRows)
	if !version.Valid || version.Int64 < 172 {
		if err := s.finding("fatal", "migration-ledger", "goose_db_version", "max applied version", ">=172", version.Int64); err != nil {
			return err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT type,name,sql FROM sqlite_master WHERE tbl_name LIKE 'workflow\_%' ESCAPE '\' AND sql IS NOT NULL AND tbl_name NOT IN ('workflow_scheduled_activations','workflow_activation_schedules','workflow_activation_fires') ORDER BY type,name`)
	if err != nil {
		return s.finding("fatal", "schema-shape", "sqlite_master", "workflow objects", "qualified workflow schema", err)
	}
	defer closeRows(rows)
	seen := make(map[string]bool)
	s.count("schema-shape", 0)
	for rows.Next() {
		var kind, name, statement string
		if err := rows.Scan(&kind, &name, &statement); err != nil {
			return s.finding("fatal", "schema-shape", "sqlite_master", "scan", "readable CREATE statement", err)
		}
		s.count("schema-shape", 1)
		key := kind + ":" + name
		actual := fmt.Sprintf("%x", sha256.Sum256([]byte(statement)))
		expected, qualified := workflowStorageSchema[key]
		if !qualified {
			expected = "absent (unqualified object)"
		}
		if expected != actual {
			if err := s.finding("fatal", "schema drift in "+key, "sqlite_master", key, expected, actual); err != nil {
				return err
			}
		}
		seen[key] = true
	}
	if err := rows.Err(); err != nil {
		return s.finding("fatal", "schema-shape", "sqlite_master", "scan", "complete object scan", err)
	}
	for _, key := range preflightSchemaKeys() {
		if !seen[key] {
			if err := s.finding("fatal", "missing schema object", "sqlite_master", key, workflowStorageSchema[key], "absent"); err != nil {
				return err
			}
		}
	}
	return nil
}

func preflightSchemaKeys() []string {
	keys := make([]string, 0, len(workflowStorageSchema))
	for key := range workflowStorageSchema {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func preflightWorkflowReferences(ctx context.Context, tx workflowSQL) error {
	s := preflightState(ctx)
	for _, key := range preflightSchemaKeys() {
		if !strings.HasPrefix(key, "table:") {
			continue
		}
		table := strings.TrimPrefix(key, "table:")
		rows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check("`+table+`")`) //nolint:gosec // Identifier is from the compiled schema manifest.
		if err != nil {
			if err := s.finding("fatal", "foreign-keys", table, "scan", "valid references", err); err != nil {
				return err
			}
			continue
		}
		for rows.Next() {
			var child, parent string
			var rowID sql.NullInt64
			var constraint int
			if err := rows.Scan(&child, &rowID, &parent, &constraint); err != nil {
				closeRows(rows)
				return s.finding("fatal", "foreign-keys", table, "scan", "readable FK result", err)
			}
			if err := s.finding("fatal", "foreign-keys", child, fmt.Sprintf("rowid=%d fk=%d", rowID.Int64, constraint), "referenced row in "+parent, "missing parent row"); err != nil {
				closeRows(rows)
				return err
			}
		}
		err = rows.Err()
		closeRows(rows)
		if err != nil {
			return s.finding("fatal", "foreign-keys", table, "scan", "complete FK scan", err)
		}
		var count int64
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM "`+table+`"`).Scan(&count); err != nil {
			if err := s.finding("fatal", "foreign-keys", table, "scan", "counted source rows", err); err != nil {
				return err
			}
			continue
		}
		s.count("foreign-keys:"+table, count)
	}
	return nil
}

func preflightStrings(ctx context.Context, tx workflowSQL, statement string, args ...any) ([]string, error) {
	rows, err := tx.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)
	var result []string
	for rows.Next() {
		var id string
		if checkErr := rows.Scan(&id); checkErr != nil {
			return nil, checkErr
		}
		result = append(result, id)
	}
	return result, rows.Err()
}

func preflightWorkflowRun(ctx context.Context, tx workflowSQL, id workflowruntime.RunID) error {
	s := preflightState(ctx)
	row := "id=" + string(id)
	var kind, contract, name string
	var digest, revision, status sql.NullString
	var generation int64
	if err := tx.QueryRowContext(ctx, `SELECT engine_kind,engine_contract_version,definition_name,plan_digest,definition_revision_id,runtime_status,runtime_generation FROM workflow_runs WHERE id=?`, id).Scan(&kind, &contract, &name, &digest, &revision, &status, &generation); err != nil {
		return s.finding("fatal", "run structural integrity", "workflow_runs", row, "readable run metadata", err)
	}
	if kind == "nanite_builtin_v1" && contract == "" {
		if digest.Valid || status.Valid || generation != 0 {
			return s.finding("fatal", "run structural integrity", "workflow_runs", row, "legacy identity without canonical runtime state", fmt.Sprintf("plan=%v runtime_status=%v generation=%d", digest, status, generation))
		}
		return nil
	}
	run, err := loadWorkflowRun(ctx, tx, id)
	if err != nil {
		return s.finding("fatal", "run structural integrity", "workflow_runs", row, "valid run envelope and positive generation", fmt.Errorf("%w; runtime_generation=%d runtime_status=%q engine=%q/%q plan_digest=%v", err, generation, status.String, kind, contract, digest.String))
	}
	severity := "fatal"
	validation := fmt.Sprintf("run %q blocked", id)
	if run.Status.Terminal() {
		severity = "warning"
		validation = fmt.Sprintf("terminal run %q compatibility", id)
	}
	problem := func(expected string, actual any) error {
		return s.finding(severity, validation, "workflow_runs", row, expected, actual)
	}
	if (kind != EngineKindGoWorkflow || contract != EngineContractVersion) && (kind != EngineKindPilotHadron || contract != PilotContractVersion) {
		if err := problem("supported engine identity "+EngineKindGoWorkflow+"/"+EngineContractVersion+" or "+EngineKindPilotHadron+"/"+PilotContractVersion, fmt.Sprintf("unsupported engine identity %q/%q", kind, contract)); err != nil {
			return err
		}
	}
	check := s.material(ctx, tx, run.Plan.Digest)
	if check.materialErr != nil {
		if err := problem("valid frozen material for plan "+run.Plan.Digest, check.materialErr); err != nil {
			return err
		}
	} else {
		material := check.material
		if planRef(material.Plan) != run.Plan || material.ProductDefinitionName != name {
			if err := problem(fmt.Sprintf("plan=%+v definition=%q", run.Plan, name), fmt.Sprintf("material plan=%+v definition=%q", planRef(material.Plan), material.ProductDefinitionName)); err != nil {
				return err
			}
		}
		s.qualify(ctx, check)
		if check.kind != kind {
			if err := problem("frozen host contract for engine "+kind, "material host contract belongs to "+check.kind); err != nil {
				return err
			}
		}
		if check.identityErr != nil {
			if err := problem("exact installed StepKind/verifier/host identity", check.identityErr); err != nil {
				return err
			}
		}
		if check.planErr != nil {
			if err := problem("compiled plan accepted by installed binary", check.planErr); err != nil {
				return err
			}
		}
	}
	if !revision.Valid {
		if err := problem("non-NULL exact immutable revision for resumable execution", "definition_revision_id=NULL (historical/conformance binding)"); err != nil {
			return err
		}
	} else {
		var boundName, boundPlan, boundKind, boundContract, boundSource, boundLocator, boundFormat, boundSchema, boundGraph string
		var content []byte
		err := tx.QueryRowContext(ctx, `SELECT definition_name,compiled_plan_digest,engine_kind,engine_contract_version,source_digest,source_content,source_locator,source_format,schema_version,compiled_graph_digest FROM workflow_definition_revisions WHERE revision_id=?`, revision.String).Scan(&boundName, &boundPlan, &boundKind, &boundContract, &boundSource, &content, &boundLocator, &boundFormat, &boundSchema, &boundGraph)
		if err != nil {
			if err := problem("readable exact revision "+revision.String, err); err != nil {
				return err
			}
		} else {
			expected := fmt.Sprintf("name=%q plan=%q engine=%q/%q", name, digest.String, kind, contract)
			actual := fmt.Sprintf("revision=%q name=%q plan=%q engine=%q/%q", revision.String, boundName, boundPlan, boundKind, boundContract)
			mismatch := boundName != name || boundPlan != digest.String || boundKind != kind || boundContract != contract
			if check.materialErr == nil {
				m := check.material
				mismatch = mismatch || boundSource != m.SourceDigest || string(content) != string(m.SourceContent) || boundLocator != m.SourceLocator || boundFormat != string(m.SourceFormat) || boundSchema != m.Plan.SchemaVersion || boundGraph != check.graphDigest
				expected += fmt.Sprintf(" source=%q locator=%q format=%q schema=%q graph=%q", m.SourceDigest, m.SourceLocator, m.SourceFormat, m.Plan.SchemaVersion, check.graphDigest)
				actual += fmt.Sprintf(" source=%q locator=%q format=%q schema=%q graph=%q", boundSource, boundLocator, boundFormat, boundSchema, boundGraph)
			}
			if mismatch {
				if err := problem("immutable revision binding "+expected, actual); err != nil {
					return err
				}
			}
		}
	}
	for _, ref := range []*values.ValueSetRef{run.Inputs, run.Outputs} {
		if ref != nil {
			if _, err := loadWorkflowValues(ctx, tx, *ref); err != nil {
				if err := s.finding("fatal", "run value reference", "workflow_runs", row, "readable values "+ref.ID+" digest="+ref.Digest, err); err != nil {
					return err
				}
			}
		}
	}
	rows, err := tx.QueryContext(ctx, workflowNodeSelect+` WHERE n.run_id=?`, id)
	if err != nil {
		return s.finding("fatal", "node references", "workflow_node_invocations", row, "readable node references", err)
	}
	for rows.Next() {
		scanner := preflightRecordScanner{row: rows, keys: []int{0, 1, 2}, names: []string{"run_id", "node_id", "iteration"}}
		node, err := scanWorkflowNode(&scanner)
		if err != nil {
			if err := s.finding("fatal", "node references", "workflow_node_invocations", scanner.id, "valid node record", err); err != nil {
				closeRows(rows)
				return err
			}
			continue
		}
		if node.Wait != nil {
			if _, err := loadWorkflowWait(ctx, tx, node.Wait.ID); err != nil {
				if err := s.finding("fatal", "node wait reference", "workflow_node_invocations", scanner.id, "readable wait "+string(node.Wait.ID), err); err != nil {
					closeRows(rows)
					return err
				}
			}
		}
		if node.LatestAttempt > 0 {
			if _, err := loadWorkflowAttempt(ctx, tx, workflowruntime.AttemptID{Invocation: node.ID, Number: node.LatestAttempt}); err != nil {
				if err := s.finding("fatal", "node attempt reference", "workflow_node_invocations", scanner.id, fmt.Sprintf("readable attempt %d", node.LatestAttempt), err); err != nil {
					closeRows(rows)
					return err
				}
			}
		}
		for _, ref := range []*values.ValueSetRef{node.Inputs, node.Outputs} {
			if ref != nil {
				if _, err := loadWorkflowValues(ctx, tx, *ref); err != nil {
					if err := s.finding("fatal", "node value reference", "workflow_node_invocations", scanner.id, "readable values "+ref.ID+" digest="+ref.Digest, err); err != nil {
						closeRows(rows)
						return err
					}
				}
			}
		}
	}
	err = rows.Err()
	closeRows(rows)
	if err != nil {
		return s.finding("fatal", "node references", "workflow_node_invocations", row, "complete reference scan", err)
	}
	return preflightEachID(ctx, tx, "external-receipts", "workflow_external_execution_receipts", "idempotency_key", `SELECT idempotency_key FROM workflow_external_execution_receipts WHERE run_id=?`, func(key string) error { _, err := loadExternalExecutionReceipt(ctx, tx, key); return err }, id)
}

// Catalog construction must never perform a product effect during preflight.
type storagePreflightExecutor struct{}

func (storagePreflightExecutor) ExecuteLLMStep(context.Context, agentworkflow.LLMStepRequest) (agentworkflow.LLMStepResult, error) {
	return agentworkflow.LLMStepResult{}, errors.New("execution is disabled during storage preflight")
}
func (storagePreflightExecutor) ExecuteToolStep(context.Context, agentworkflow.ToolStepRequest) (agentworkflow.ToolStepResult, error) {
	return agentworkflow.ToolStepResult{}, errors.New("execution is disabled during storage preflight")
}
func (storagePreflightExecutor) Verify(context.Context, agentworkflow.VerifyRequest) (agentworkflow.VerifyResult, error) {
	return agentworkflow.VerifyResult{}, errors.New("execution is disabled during storage preflight")
}

func preflightWorkflowIdempotency(ctx context.Context, tx workflowSQL) error {
	s := preflightState(ctx)
	for _, check := range []struct {
		name, table string
		validate    func(string, string) error
	}{
		{"run-start-replay", "workflow_run_start_idempotency", func(requestJSON, resultJSON string) error {
			var request workflowruntime.CreateRunRequest
			var result workflowruntime.RunSnapshot
			if err := decodeWorkflowJSON("run start request", requestJSON, &request); err != nil {
				return err
			}
			if err := decodeWorkflowJSON("run start result", resultJSON, &result); err != nil {
				return err
			}
			if err := validateWorkflowCreateRun(request); err != nil {
				return err
			}
			if err := result.Validate(); err != nil {
				return err
			}
			if result.ID != request.ID || result.Plan != request.Plan || result.Status != request.Status || result.Generation != 1 || !result.CreatedAt.Equal(request.CreatedAt) || !result.UpdatedAt.Equal(request.CreatedAt) || !equalWorkflowValueRef(result.Inputs, request.Inputs) || result.Outputs != nil {
				return fmt.Errorf("expected replay id=%q plan=%q status=%q generation=1 timestamps=%s inputs=%v outputs=NULL; actual id=%q plan=%q status=%q generation=%d timestamps=%s/%s inputs=%v outputs=%v", request.ID, request.Plan.Digest, request.Status, request.CreatedAt, request.Inputs, result.ID, result.Plan.Digest, result.Status, result.Generation, result.CreatedAt, result.UpdatedAt, result.Inputs, result.Outputs)
			}
			_, err := loadWorkflowRun(ctx, tx, result.ID)
			return err
		}},
		{"claim-replay", "workflow_claim_idempotency", func(requestJSON, resultJSON string) error {
			var request workflowruntime.ClaimNodeRequest
			var result workflowruntime.ClaimResult
			if err := decodeWorkflowJSON("claim request", requestJSON, &request); err != nil {
				return err
			}
			if err := decodeWorkflowJSON("claim result", resultJSON, &result); err != nil {
				return err
			}
			if err := validateWorkflowClaim(request); err != nil {
				return err
			}
			if err := validateWorkflowClaimResult(result); err != nil {
				return err
			}
			node, err := loadWorkflowNode(ctx, tx, request.InvocationID)
			if err != nil {
				return err
			}
			if result.Lease != nil && (result.Lease.Owner != request.Owner || result.Lease.Token != request.Token || result.Lease.Generation > node.ClaimGeneration || !result.Lease.ExpiresAt.Equal(request.LeaseUntil)) {
				return fmt.Errorf("expected replay lease owner=%q token=%q generation<=%d expiry=%s; actual owner=%q token=%q generation=%d expiry=%s", request.Owner, request.Token, node.ClaimGeneration, request.LeaseUntil, result.Lease.Owner, result.Lease.Token, result.Lease.Generation, result.Lease.ExpiresAt)
			}
			return nil
		}},
	} {
		rows, err := tx.QueryContext(ctx, `SELECT idempotency_key,request_json,result_json FROM `+check.table)
		if err != nil {
			if err := s.finding("fatal", check.name, check.table, "scan", "readable idempotency records", err); err != nil {
				return err
			}
			continue
		}
		s.count(check.name, 0)
		for rows.Next() {
			var key, request, result string
			if err := rows.Scan(&key, &request, &result); err != nil {
				closeRows(rows)
				return s.finding("fatal", check.name, check.table, "scan", "readable replay record", err)
			}
			s.count(check.name, 1)
			if err := check.validate(request, result); err != nil {
				if err := s.finding("fatal", check.name, check.table, "idempotency_key="+key, "valid replay bound to request and referenced rows", err); err != nil {
					closeRows(rows)
					return err
				}
			}
		}
		err = rows.Err()
		closeRows(rows)
		if err != nil {
			if err := s.finding("fatal", check.name, check.table, "scan", "complete replay scan", err); err != nil {
				return err
			}
		}
	}
	return nil
}

// Validate terminal records as well as recovery candidates. A valid JSON
// envelope alone does not prove a runtime record can be read after restart.
func preflightWorkflowRecords(ctx context.Context, tx workflowSQL) error {
	s := preflightState(ctx)
	checks := []struct {
		name, table, statement string
		keys                   []int
		names                  []string
		scan                   func(workflowScanner) error
	}{
		{"records:nodes", "workflow_node_invocations", workflowNodeSelect, []int{0, 1, 2}, []string{"run_id", "node_id", "iteration"}, func(row workflowScanner) error { _, err := scanWorkflowNode(row); return err }},
		{"records:attempts", "workflow_attempts", workflowAttemptSelect, []int{0, 1, 2, 3}, []string{"run_id", "node_id", "iteration", "attempt_number"}, func(row workflowScanner) error { _, err := scanWorkflowAttempt(row); return err }},
		{"records:waits", "workflow_waits", workflowWaitSelect, []int{0}, []string{"wait_id"}, func(row workflowScanner) error { _, err := scanWorkflowWait(row); return err }},
		{"records:events", "workflow_events", workflowEventSelect, []int{0, 1}, []string{"run_id", "sequence"}, func(row workflowScanner) error { _, err := scanWorkflowEvent(row); return err }},
		{"records:reactors", "workflow_reactors", workflowReactorSelect, []int{1}, []string{"reactor_id"}, func(row workflowScanner) error { _, err := scanWorkflowReactor(row); return err }},
		{"records:external-operations", "workflow_external_operations", workflowExternalOperationSelect, []int{0, 1, 2, 3}, []string{"run_id", "node_id", "iteration", "attempt_number"}, func(row workflowScanner) error { _, err := scanWorkflowExternalOperation(row); return err }},
	}
	for _, check := range checks {
		rows, err := tx.QueryContext(ctx, check.statement)
		if err != nil {
			if err := s.finding("fatal", check.name, check.table, "scan", "readable persisted records", err); err != nil {
				return err
			}
			continue
		}
		s.count(check.name, 0)
		for rows.Next() {
			s.count(check.name, 1)
			columns, columnErr := rows.Columns()
			if columnErr != nil {
				closeRows(rows)
				return s.finding("fatal", check.name, check.table, "scan", "readable record columns", columnErr)
			}
			scanner := preflightRecordScanner{row: rows, keys: check.keys, names: check.names, columns: columns}
			if err := check.scan(&scanner); err != nil {
				if err := s.finding("fatal", check.name, check.table, scanner.id, "valid persisted record", fmt.Errorf("%w; stored fields: %s", err, scanner.actual)); err != nil {
					closeRows(rows)
					return err
				}
			}
		}
		err = rows.Err()
		closeRows(rows)
		if err != nil {
			if err := s.finding("fatal", check.name, check.table, "scan", "complete record scan", err); err != nil {
				return err
			}
		}
	}
	for _, check := range []struct{ name, table, severity, statement, source string }{
		{"claim-lease-generations", "workflow_node_leases", "fatal", `SELECT json_object('run_id',n.run_id,'node_id',n.node_id,'iteration',n.iteration),'claim_generation='||n.claim_generation,'lease_generation='||l.generation FROM workflow_node_invocations n JOIN workflow_node_leases l USING(run_id,node_id,iteration) WHERE n.claim_generation!=l.generation`, `SELECT count(*) FROM workflow_node_leases`},
		{"event-cursor-bindings", "workflow_events", "fatal", `SELECT json_object('run_id',e.run_id,'sequence',e.sequence),'sequence <= ledger='||coalesce(s.last_sequence,'NULL'),'sequence='||e.sequence FROM workflow_events e LEFT JOIN workflow_event_sequences s USING(run_id) WHERE s.last_sequence IS NULL OR e.sequence>s.last_sequence`, `SELECT count(*) FROM workflow_events`},
		{"event-cursor-ledger", "workflow_event_sequences", "fatal", `SELECT 'run_id='||s.run_id,'max committed sequence='||coalesce((SELECT max(e.sequence) FROM workflow_events e WHERE e.run_id=s.run_id),0),'last_sequence='||s.last_sequence FROM workflow_event_sequences s WHERE s.last_sequence!=coalesce((SELECT max(e.sequence) FROM workflow_events e WHERE e.run_id=s.run_id),0)`, `SELECT count(*) FROM workflow_event_sequences`},
		{"product-step-references", "workflow_run_steps", "warning", `SELECT 'id='||s.id||' workflow_run_id='||s.workflow_run_id,'workflow_runs.id='||s.workflow_run_id,'missing product parent' FROM workflow_run_steps s LEFT JOIN workflow_runs r ON r.id=s.workflow_run_id WHERE r.id IS NULL`, `SELECT count(*) FROM workflow_run_steps`},
	} {
		if err := preflightRelation(ctx, tx, check.name, check.table, check.severity, check.statement, check.source); err != nil {
			return err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT sequence,digest FROM workflow_value_sets ORDER BY sequence`)
	if err != nil {
		return s.finding("fatal", "value-sets", "workflow_value_sets", "scan", "readable value sets", err)
	}
	s.count("value-sets", 0)
	for rows.Next() {
		var sequence int64
		var digest string
		if err := rows.Scan(&sequence, &digest); err != nil {
			closeRows(rows)
			return s.finding("fatal", "value-sets", "workflow_value_sets", "scan", "readable value reference", err)
		}
		s.count("value-sets", 1)
		ref := values.ValueSetRef{ID: workflowValueID(sequence), Digest: digest}
		if _, err := loadWorkflowValues(ctx, tx, ref); err != nil {
			if err := s.finding("fatal", "value-sets", "workflow_value_sets", fmt.Sprintf("sequence=%d id=%s", sequence, ref.ID), "valid values with digest="+digest, err); err != nil {
				closeRows(rows)
				return err
			}
		}
	}
	err = rows.Err()
	closeRows(rows)
	if err != nil {
		return s.finding("fatal", "value-sets", "workflow_value_sets", "scan", "complete value scan", err)
	}
	rows, err = tx.QueryContext(ctx, `SELECT plan_digest FROM workflow_plan_materials ORDER BY plan_digest`)
	if err != nil {
		return s.finding("fatal", "frozen-material", "workflow_plan_materials", "scan", "readable frozen material", err)
	}
	s.count("frozen-material", 0)
	for rows.Next() {
		var digest string
		if err := rows.Scan(&digest); err != nil {
			closeRows(rows)
			return s.finding("fatal", "frozen-material", "workflow_plan_materials", "scan", "readable plan digest", err)
		}
		s.count("frozen-material", 1)
		check := s.material(ctx, tx, digest)
		if check.materialErr != nil {
			var total, active int64
			if err := tx.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(CASE WHEN runtime_status IN ('succeeded','failed','canceled','timed_out','crashed') THEN 0 ELSE 1 END),0) FROM workflow_runs WHERE plan_digest=?`, digest).Scan(&total, &active); err != nil {
				closeRows(rows)
				return s.finding("fatal", "frozen-material", "workflow_plan_materials", "plan_digest="+digest, "readable referencing runs", err)
			}
			severity := "fatal"
			if total > 0 && active == 0 {
				severity = "warning"
			}
			if err := s.finding(severity, "frozen-material", "workflow_plan_materials", "plan_digest="+digest, "valid frozen material", check.materialErr); err != nil {
				closeRows(rows)
				return err
			}
		}
	}
	err = rows.Err()
	closeRows(rows)
	if err != nil {
		return s.finding("fatal", "frozen-material", "workflow_plan_materials", "scan", "complete material scan", err)
	}
	for _, check := range []struct {
		name, table, column, statement string
		load                           func(string) error
	}{
		{"compensation-ledgers", "workflow_compensation_ledgers", "run_id", `SELECT run_id FROM workflow_compensation_ledgers`, func(id string) error {
			_, err := loadWorkflowCompensationLedger(ctx, tx, workflowruntime.RunID(id))
			return err
		}},
		{"terminal-intents", "workflow_terminal_intents", "run_id", `SELECT run_id FROM workflow_terminal_intents`, func(id string) error {
			_, err := loadWorkflowTerminalIntent(ctx, tx, workflowruntime.RunID(id))
			return err
		}},
		{"reactor-continuations", "workflow_reactor_continuations", "idempotency_key", `SELECT idempotency_key FROM workflow_reactor_continuations`, func(id string) error { _, err := loadWorkflowReactorContinuation(ctx, tx, id); return err }},
	} {
		if err := preflightEachID(ctx, tx, check.name, check.table, check.column, check.statement, check.load); err != nil {
			return err
		}
	}
	return nil
}
