package workflowhost

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/hollis-labs/go-workflow/compile"
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
	s, err := NewSQLWorkflowStateStore(product)
	if err != nil {
		return err
	}
	return s.write(ctx, "workflow storage preflight", func(tx workflowSQL) error {
		if checkErr := preflightWorkflowSchema(ctx, tx); checkErr != nil {
			return checkErr
		}
		if checkErr := preflightWorkflowReferences(ctx, tx); checkErr != nil {
			return checkErr
		}
		if checkErr := preflightWorkflowRecords(ctx, tx); checkErr != nil {
			return checkErr
		}
		if checkErr := preflightWorkflowEnvelopes(ctx, tx); checkErr != nil {
			return checkErr
		}
		ids, err := preflightStrings(ctx, tx, `SELECT id FROM workflow_runs ORDER BY id`)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if checkErr := preflightWorkflowRun(ctx, tx, workflowruntime.RunID(id)); checkErr != nil {
				return fmt.Errorf("workflow storage preflight: run %q blocked: %w; operator: keep the service stopped and restore verified original frozen material from backup, or escalate this run for explicit audited disposition before restarting", id, checkErr)
			}
		}
		return preflightWorkflowIdempotency(ctx, tx)
	})
}

// Schema validation has already attested these identifiers. Check parked
// state too, including idempotency and scheduler records outside active runs.
func preflightWorkflowEnvelopes(ctx context.Context, tx workflowSQL) error {
	for key := range workflowStorageSchema {
		if !strings.HasPrefix(key, "table:") {
			continue
		}
		table := strings.TrimPrefix(key, "table:")
		rows, err := tx.QueryContext(ctx, `PRAGMA table_info("`+table+`")`) //nolint:gosec // Table names come from the compiled schema manifest.
		if err != nil {
			return err
		}
		var predicates []string
		for rows.Next() {
			var cid, notNull, primaryKey int
			var name, kind string
			var defaultValue sql.NullString
			if scanErr := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primaryKey); scanErr != nil {
				closeRows(rows)
				return scanErr
			}
			column := `"` + name + `"`
			if strings.HasSuffix(name, "_json") {
				predicates = append(predicates, `(`+column+` IS NOT NULL AND `+column+`<>'' AND NOT json_valid(`+column+`))`)
			}
			if name == "generation" || name == "runtime_generation" || name == "claim_generation" {
				predicates = append(predicates, `(`+column+` IS NOT NULL AND typeof(`+column+`) <> 'integer')`)
			}
		}
		err = rows.Err()
		closeRows(rows)
		if err != nil {
			return err
		}
		if len(predicates) == 0 {
			continue
		}
		var invalid int
		statement := `SELECT EXISTS(SELECT 1 FROM "` + table + `" WHERE ` + strings.Join(predicates, ` OR `) + `)`
		if queryErr := tx.QueryRowContext(ctx, statement).Scan(&invalid); queryErr != nil {
			return queryErr
		}
		if invalid != 0 {
			return fmt.Errorf("workflow storage preflight: malformed persisted envelope or generation in %s", table)
		}
	}
	return nil
}

func preflightWorkflowSchema(ctx context.Context, tx workflowSQL) error {
	var foreignKeys int
	if checkErr := tx.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); checkErr != nil {
		return checkErr
	}
	if foreignKeys != 1 {
		return fmt.Errorf("workflow storage preflight: foreign key enforcement is disabled")
	}
	var version int64
	if checkErr := tx.QueryRowContext(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied=1`).Scan(&version); checkErr != nil {
		return fmt.Errorf("workflow migration ledger: %w", checkErr)
	}
	if version < 172 {
		return fmt.Errorf("workflow storage preflight: migration ledger stops at %d", version)
	}
	rows, err := tx.QueryContext(ctx, `SELECT type,name,sql FROM sqlite_master WHERE tbl_name LIKE 'workflow_%' AND sql IS NOT NULL AND tbl_name NOT IN ('workflow_scheduled_activations','workflow_activation_schedules','workflow_activation_fires') ORDER BY type,name`)
	if err != nil {
		return err
	}
	defer closeRows(rows)
	seen := make(map[string]bool)
	for rows.Next() {
		var kind, name, statement string
		if checkErr := rows.Scan(&kind, &name, &statement); checkErr != nil {
			return checkErr
		}
		key := kind + ":" + name
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(statement)))
		if workflowStorageSchema[key] != digest {
			return fmt.Errorf("workflow storage preflight: schema drift in %s", key)
		}
		seen[key] = true
	}
	if checkErr := rows.Err(); checkErr != nil {
		return checkErr
	}
	for key := range workflowStorageSchema {
		if !seen[key] {
			return fmt.Errorf("workflow storage preflight: missing schema object %s", key)
		}
	}
	return nil
}

func preflightWorkflowReferences(ctx context.Context, tx workflowSQL) error {
	rows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	defer closeRows(rows)
	for rows.Next() {
		var table, parent string
		var rowID sql.NullInt64
		var constraint int
		if checkErr := rows.Scan(&table, &rowID, &parent, &constraint); checkErr != nil {
			return checkErr
		}
		if strings.HasPrefix(table, "workflow_") {
			return fmt.Errorf("workflow storage preflight: broken reference from %s to %s", table, parent)
		}
	}
	return rows.Err()
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
	var kind, contract, name string
	var digest, revision, status sql.NullString
	var generation int64
	if checkErr := tx.QueryRowContext(ctx, `SELECT engine_kind,engine_contract_version,definition_name,plan_digest,definition_revision_id,runtime_status,runtime_generation FROM workflow_runs WHERE id=?`, id).Scan(&kind, &contract, &name, &digest, &revision, &status, &generation); checkErr != nil {
		return checkErr
	}
	if kind == "nanite_builtin_v1" && contract == "" {
		if digest.Valid || status.Valid || generation != 0 {
			return fmt.Errorf("legacy identity carries canonical runtime state")
		}
		// Legacy disposition remains owned by the existing cutover coordinator,
		// which runs before any worker and accepts explicit audited decisions.
		return nil
	}
	if (kind != EngineKindGoWorkflow || contract != EngineContractVersion) && (kind != EngineKindPilotHadron || contract != PilotContractVersion) {
		return fmt.Errorf("unsupported engine identity %q/%q", kind, contract)
	}
	run, err := loadWorkflowRun(ctx, tx, id)
	if err != nil {
		return err
	}
	material, err := loadPlanMaterial(ctx, tx, run.Plan.Digest)
	if err != nil {
		return err
	}
	if checkErr := validatePlanMaterial(material); checkErr != nil {
		return checkErr
	}
	if planRef(material.Plan) != run.Plan || material.ProductDefinitionName != name {
		return fmt.Errorf("frozen material identity differs from run")
	}
	registry, err := newFrozenRegistry(storagePreflightExecutor{})
	if kind == EngineKindPilotHadron {
		registry, err = newPilotRegistry(storagePreflightExecutor{})
	}
	if err != nil {
		return err
	}
	verifiers, identityErr := verifyInstalledExecutionIdentity(material, registry)
	if identityErr != nil {
		return identityErr
	}
	findings := compile.ValidatePlan(ctx, &material.Plan, compile.ValidationOptions{StepKinds: registry, Verifiers: verifiers})
	if hasDiagnosticErrors(findings) {
		return diagnosticsError("unsupported frozen execution plan", findings)
	}
	// Historical pilot rows predate revision binding and may retain NULL. Current
	// production rows always require the exact source-bound immutable revision.
	if !revision.Valid {
		if kind != EngineKindPilotHadron {
			return fmt.Errorf("exact immutable revision is missing")
		}
	} else {
		var boundName, boundPlan, boundKind, boundContract, boundSource, boundLocator, boundFormat, boundSchema, boundGraph string
		var content []byte
		if checkErr := tx.QueryRowContext(ctx, `SELECT definition_name,compiled_plan_digest,engine_kind,engine_contract_version,source_digest,source_content,source_locator,source_format,schema_version,compiled_graph_digest FROM workflow_definition_revisions WHERE revision_id=?`, revision.String).Scan(&boundName, &boundPlan, &boundKind, &boundContract, &boundSource, &content, &boundLocator, &boundFormat, &boundSchema, &boundGraph); checkErr != nil {
			return checkErr
		}
		graphDigest, graphErr := compile.GraphDigest(material.Plan.Graph)
		if graphErr != nil {
			return graphErr
		}
		if boundName != name || boundPlan != digest.String || boundKind != kind || boundContract != contract || boundSource != material.SourceDigest || string(content) != string(material.SourceContent) || boundLocator != material.SourceLocator || boundFormat != string(material.SourceFormat) || boundSchema != material.Plan.SchemaVersion || boundGraph != graphDigest {
			return fmt.Errorf("immutable revision binding differs from frozen material")
		}
	}
	rows, err := tx.QueryContext(ctx, workflowNodeSelect+` WHERE n.run_id=?`, id)
	if err != nil {
		return err
	}
	var nodes []workflowruntime.NodeInvocationSnapshot
	for rows.Next() {
		node, scanErr := scanWorkflowNode(rows)
		if scanErr != nil {
			closeRows(rows)
			return scanErr
		}
		nodes = append(nodes, node)
	}
	err = rows.Err()
	closeRows(rows)
	if err != nil {
		return err
	}
	for _, node := range nodes {
		if node.Lease != nil && node.Lease.Generation != node.ClaimGeneration {
			return fmt.Errorf("node lease differs from claim generation")
		}
		if node.Wait != nil {
			if _, checkErr := loadWorkflowWait(ctx, tx, node.Wait.ID); checkErr != nil {
				return checkErr
			}
		}
		if node.LatestAttempt > 0 {
			if _, checkErr := loadWorkflowAttempt(ctx, tx, workflowruntime.AttemptID{Invocation: node.ID, Number: node.LatestAttempt}); checkErr != nil {
				return checkErr
			}
		}
		for _, ref := range []*values.ValueSetRef{node.Inputs, node.Outputs} {
			if ref != nil {
				if _, checkErr := loadWorkflowValues(ctx, tx, *ref); checkErr != nil {
					return checkErr
				}
			}
		}
	}
	keys, err := preflightStrings(ctx, tx, `SELECT idempotency_key FROM workflow_external_execution_receipts WHERE run_id=?`, id)
	if err != nil {
		return err
	}
	for _, key := range keys {
		if _, checkErr := loadExternalExecutionReceipt(ctx, tx, key); checkErr != nil {
			return checkErr
		}
	}
	return nil
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
	rows, err := tx.QueryContext(ctx, `SELECT request_json,result_json FROM workflow_run_start_idempotency`)
	if err != nil {
		return err
	}
	defer closeRows(rows)
	for rows.Next() {
		var requestJSON, resultJSON string
		if checkErr := rows.Scan(&requestJSON, &resultJSON); checkErr != nil {
			return checkErr
		}
		var request workflowruntime.CreateRunRequest
		var result workflowruntime.RunSnapshot
		if checkErr := decodeWorkflowJSON("run start request", requestJSON, &request); checkErr != nil {
			return checkErr
		}
		if checkErr := decodeWorkflowJSON("run start result", resultJSON, &result); checkErr != nil {
			return checkErr
		}
		if checkErr := validateWorkflowCreateRun(request); checkErr != nil {
			return checkErr
		}
		if checkErr := result.Validate(); checkErr != nil {
			return checkErr
		}
		if result.ID != request.ID || result.Plan != request.Plan || result.Status != request.Status || result.Generation != 1 || !result.CreatedAt.Equal(request.CreatedAt) || !result.UpdatedAt.Equal(request.CreatedAt) || !equalWorkflowValueRef(result.Inputs, request.Inputs) || result.Outputs != nil {
			return fmt.Errorf("workflow storage preflight: run-start replay differs from request")
		}
		if _, checkErr := loadWorkflowRun(ctx, tx, result.ID); checkErr != nil {
			return checkErr
		}
	}
	if checkErr := rows.Err(); checkErr != nil {
		return checkErr
	}
	closeRows(rows)
	claims, err := tx.QueryContext(ctx, `SELECT request_json,result_json FROM workflow_claim_idempotency`)
	if err != nil {
		return err
	}
	defer closeRows(claims)
	for claims.Next() {
		var requestJSON, resultJSON string
		if checkErr := claims.Scan(&requestJSON, &resultJSON); checkErr != nil {
			return checkErr
		}
		var request workflowruntime.ClaimNodeRequest
		var result workflowruntime.ClaimResult
		if checkErr := decodeWorkflowJSON("claim request", requestJSON, &request); checkErr != nil {
			return checkErr
		}
		if checkErr := decodeWorkflowJSON("claim result", resultJSON, &result); checkErr != nil {
			return checkErr
		}
		if checkErr := validateWorkflowClaim(request); checkErr != nil {
			return checkErr
		}
		if checkErr := validateWorkflowClaimResult(result); checkErr != nil {
			return checkErr
		}
		node, err := loadWorkflowNode(ctx, tx, request.InvocationID)
		if err != nil {
			return err
		}
		if result.Lease != nil && (result.Lease.Owner != request.Owner || result.Lease.Token != request.Token || result.Lease.Generation > node.ClaimGeneration || !result.Lease.ExpiresAt.Equal(request.LeaseUntil)) {
			return fmt.Errorf("workflow storage preflight: claim replay differs from request")
		}
	}
	return claims.Err()
}

// Validate terminal records as well as recovery candidates. A valid JSON
// envelope alone does not prove a runtime record can be read after restart.
func preflightWorkflowRecords(ctx context.Context, tx workflowSQL) error {
	checks := []struct {
		statement string
		scan      func(workflowScanner) error
	}{
		{workflowNodeSelect, func(row workflowScanner) error { _, err := scanWorkflowNode(row); return err }},
		{workflowAttemptSelect, func(row workflowScanner) error { _, err := scanWorkflowAttempt(row); return err }},
		{workflowWaitSelect, func(row workflowScanner) error { _, err := scanWorkflowWait(row); return err }},
		{workflowEventSelect, func(row workflowScanner) error { _, err := scanWorkflowEvent(row); return err }},
		{workflowReactorSelect, func(row workflowScanner) error { _, err := scanWorkflowReactor(row); return err }},
		{workflowExternalOperationSelect, func(row workflowScanner) error { _, err := scanWorkflowExternalOperation(row); return err }},
	}
	for _, check := range checks {
		rows, err := tx.QueryContext(ctx, check.statement)
		if err != nil {
			return err
		}
		for rows.Next() {
			if checkErr := check.scan(rows); checkErr != nil {
				closeRows(rows)
				return fmt.Errorf("workflow storage preflight: unreadable persisted record: %w", checkErr)
			}
		}
		err = rows.Err()
		closeRows(rows)
		if err != nil {
			return err
		}
	}
	var broken int
	if checkErr := tx.QueryRowContext(ctx, `SELECT count(*) FROM workflow_node_invocations n JOIN workflow_node_leases l USING(run_id,node_id,iteration) WHERE n.claim_generation != l.generation`).Scan(&broken); checkErr != nil {
		return checkErr
	}
	if broken != 0 {
		return fmt.Errorf("workflow storage preflight: node claim and lease generations differ")
	}
	if checkErr := tx.QueryRowContext(ctx, `SELECT count(*) FROM workflow_events e LEFT JOIN workflow_event_sequences s USING(run_id) WHERE s.last_sequence IS NULL OR e.sequence>s.last_sequence`).Scan(&broken); checkErr != nil {
		return checkErr
	}
	if broken != 0 {
		return fmt.Errorf("workflow storage preflight: event sequence ledger differs from events")
	}
	if checkErr := tx.QueryRowContext(ctx, `SELECT count(*) FROM workflow_event_sequences s WHERE s.last_sequence != COALESCE((SELECT max(e.sequence) FROM workflow_events e WHERE e.run_id=s.run_id),0)`).Scan(&broken); checkErr != nil {
		return checkErr
	}
	if broken != 0 {
		return fmt.Errorf("workflow storage preflight: event sequence ledger contains an uncommitted cursor")
	}
	if checkErr := tx.QueryRowContext(ctx, `SELECT count(*) FROM workflow_run_steps s LEFT JOIN workflow_runs r ON r.id=s.workflow_run_id WHERE r.id IS NULL`).Scan(&broken); checkErr != nil {
		return checkErr
	}
	if broken != 0 {
		return fmt.Errorf("workflow storage preflight: product step references a missing run")
	}
	rows, err := tx.QueryContext(ctx, `SELECT sequence,digest FROM workflow_value_sets`)
	if err != nil {
		return err
	}
	var refs []values.ValueSetRef
	for rows.Next() {
		var seq int64
		var digest string
		if checkErr := rows.Scan(&seq, &digest); checkErr != nil {
			closeRows(rows)
			return checkErr
		}
		refs = append(refs, values.ValueSetRef{ID: workflowValueID(seq), Digest: digest})
	}
	err = rows.Err()
	closeRows(rows)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if _, checkErr := loadWorkflowValues(ctx, tx, ref); checkErr != nil {
			return checkErr
		}
	}
	digests, err := preflightStrings(ctx, tx, `SELECT plan_digest FROM workflow_plan_materials`)
	if err != nil {
		return err
	}
	for _, digest := range digests {
		material, err := loadPlanMaterial(ctx, tx, digest)
		if err != nil {
			return err
		}
		if checkErr := validatePlanMaterial(material); checkErr != nil {
			return checkErr
		}
		if material.Plan.Digest != digest {
			return fmt.Errorf("workflow storage preflight: frozen material key differs from its digest")
		}
	}
	for _, check := range []struct {
		statement string
		load      func(string) error
	}{
		{`SELECT run_id FROM workflow_compensation_ledgers`, func(id string) error {
			_, err := loadWorkflowCompensationLedger(ctx, tx, workflowruntime.RunID(id))
			return err
		}},
		{`SELECT run_id FROM workflow_terminal_intents`, func(id string) error {
			_, err := loadWorkflowTerminalIntent(ctx, tx, workflowruntime.RunID(id))
			return err
		}},
		{`SELECT idempotency_key FROM workflow_reactor_continuations`, func(id string) error { _, err := loadWorkflowReactorContinuation(ctx, tx, id); return err }},
	} {
		ids, err := preflightStrings(ctx, tx, check.statement)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if checkErr := check.load(id); checkErr != nil {
				return checkErr
			}
		}
	}
	return nil
}
