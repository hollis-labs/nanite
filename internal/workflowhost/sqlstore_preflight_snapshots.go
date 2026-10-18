package workflowhost

import (
	"context"
	"fmt"
	"strings"

	workflowruntime "github.com/hollis-labs/libs/workflow/engine/runtime"
)

// These readers run on the same transaction as the other preflight scans. They
// cover parked and terminal snapshots too: JSON syntax alone cannot establish
// that the released runtime can consume a persisted record.
func preflightWorkflowSnapshotFamilies(ctx context.Context, tx workflowSQL) error {
	invocation := func(ids []string) workflowruntime.NodeInvocationID {
		return workflowruntime.NodeInvocationID{RunID: workflowruntime.RunID(ids[0]), NodeID: ids[1], Iteration: ids[2]}
	}
	checks := []struct {
		name, table, columns string
		load                 func([]string) error
	}{
		{"retry-activations", "workflow_retry_activations", "activation_id,run_id", func(ids []string) error {
			_, err := loadWorkflowRetryActivation(ctx, tx, ids[0])
			return err
		}},
		{"fanouts", "workflow_fanouts", "run_id,node_id,iteration", func(ids []string) error {
			_, err := loadWorkflowFanOut(ctx, tx, invocation(ids))
			return err
		}},
		{"services", "workflow_services", "run_id,node_id,iteration", func(ids []string) error {
			_, err := loadWorkflowService(ctx, tx, invocation(ids))
			return err
		}},
		{"cancellation-intents", "workflow_cancellation_intents", "intent_id,run_id", func(ids []string) error {
			_, err := loadWorkflowCancellationIntent(ctx, tx, ids[0])
			return err
		}},
		{"control-decisions", "workflow_control_decisions", "run_id,node_id,iteration,kind", func(ids []string) error {
			_, err := loadWorkflowControlDecision(ctx, tx, workflowruntime.ControlDecisionID{Source: invocation(ids), Kind: workflowruntime.ControlDecisionKind(ids[3])})
			return err
		}},
		{"compensation-entries", "workflow_compensation_entries", "run_id,entry_id", func(ids []string) error {
			_, err := loadWorkflowCompensationEntry(ctx, tx, workflowruntime.RunID(ids[0]), ids[1])
			return err
		}},
		{"run-policy-decisions", "workflow_run_policy_decisions", "run_id", func(ids []string) error {
			_, err := loadWorkflowRunPolicyDecision(ctx, tx, workflowruntime.RunID(ids[0]))
			return err
		}},
		{"run-updates", "workflow_run_updates", "idempotency_key,run_id", func(ids []string) error {
			_, err := loadWorkflowRunUpdate(ctx, tx, ids[0])
			return err
		}},
		{"replay-provenance", "workflow_replay_provenance", "run_id", func(ids []string) error {
			var run, source, from, digest, created, encoded string
			if scanErr := tx.QueryRowContext(ctx, `SELECT run_id,source_run_id,from_node_id,plan_digest,created_at,snapshot_json FROM workflow_replay_provenance WHERE run_id=?`, ids[0]).Scan(&run, &source, &from, &digest, &created, &encoded); scanErr != nil {
				return scanErr
			}
			var result workflowruntime.BeginReplayResult
			if decodeErr := decodeWorkflowJSON("replay result", encoded, &result); decodeErr != nil {
				return decodeErr
			}
			return validateWorkflowReplayProjection(run, source, from, digest, created, result)
		}},
		{"memo-entries", "workflow_memo_entries", "sequence,source_run_id", func(ids []string) error {
			var encoded string
			if scanErr := tx.QueryRowContext(ctx, `SELECT entry_json FROM workflow_memo_entries WHERE sequence=?`, ids[0]).Scan(&encoded); scanErr != nil {
				return scanErr
			}
			var entry workflowruntime.MemoEntry
			if decodeErr := decodeWorkflowJSON("memo entry", encoded, &entry); decodeErr != nil {
				return decodeErr
			}
			return entry.Validate()
		}},
		{"plan-refs", "workflow_plan_refs", "digest", func(ids []string) error { _, err := loadWorkflowPlan(ctx, tx, ids[0]); return err }},

		{"cache-entries", "workflow_cache_entries", "cache_key", func(ids []string) error { _, err := loadWorkflowCacheEntry(ctx, tx, ids[0]); return err }},
		{"pinned-values", "workflow_pinned_values", "pin_key", func(ids []string) error { _, err := loadWorkflowPinnedValue(ctx, tx, ids[0]); return err }},
		{"reactor-deliveries", "workflow_reactor_deliveries", "reactor_id,idempotency_key,run_id", func(ids []string) error { _, err := loadWorkflowReactorDelivery(ctx, tx, ids[0], ids[1]); return err }},
		{"child-runs", "workflow_child_runs", "child_run_id,parent_run_id", func(ids []string) error {
			var encoded string
			if err := tx.QueryRowContext(ctx, `SELECT link_json FROM workflow_child_runs WHERE child_run_id=?`, ids[0]).Scan(&encoded); err != nil {
				return err
			}
			var link workflowruntime.ChildRunLink
			if err := decodeWorkflowJSON("child run link", encoded, &link); err != nil {
				return err
			}
			return link.Validate()
		}},

		{"pin-bindings", "workflow_pin_bindings", "run_id,node_id,iteration", func(ids []string) error {
			_, err := loadWorkflowPinBinding(ctx, tx, invocation(ids))
			return err
		}},
	}
	state := preflightState(ctx)
	for _, check := range checks {
		name := "records:" + check.name
		rows, queryErr := tx.QueryContext(ctx, `SELECT `+check.columns+` FROM `+check.table)
		if queryErr != nil {
			return state.finding("fatal", name, check.table, "scan", "readable snapshot keys", queryErr)
		}
		columns := strings.Split(check.columns, ",")
		state.count(name, 0)
		for rows.Next() {
			ids := make([]string, len(columns))
			destinations := make([]any, len(columns))
			for i := range ids {
				destinations[i] = &ids[i]
			}
			if scanErr := rows.Scan(destinations...); scanErr != nil {
				closeRows(rows)
				return state.finding("fatal", name, check.table, "scan", "readable snapshot keys", scanErr)
			}
			state.count(name, 1)
			if loadErr := check.load(ids); loadErr != nil {
				fields := make([]string, len(columns))
				for i, column := range columns {
					fields[i] = column + "=" + ids[i]
				}
				if findingErr := state.finding("fatal", name, check.table, strings.Join(fields, " "), "runtime-readable snapshot", loadErr); findingErr != nil {
					closeRows(rows)
					return findingErr
				}
			}
		}
		iterationErr := rows.Err()
		closeRows(rows)
		if iterationErr != nil {
			return state.finding("fatal", name, check.table, "scan", "complete snapshot scan", iterationErr)
		}
	}
	// Scheduler readers also check indexed projections against their snapshots.
	// Use their full scans, including holders and waiters for inactive runs.
	for _, check := range []struct {
		table string
		load  func() error
	}{
		{"workflow_scheduler_resources", func() error { return validateWorkflowSchedulerDefinitions(ctx, tx) }},
		{"workflow_scheduler_holders", func() error { _, err := listWorkflowSchedulerHolders(ctx, tx); return err }},
		{"workflow_scheduler_waiters", func() error { _, err := listWorkflowSchedulerWaiters(ctx, tx); return err }},
	} {
		name := "records:" + strings.TrimPrefix(check.table, "workflow_")
		if loadErr := check.load(); loadErr != nil {
			if findingErr := state.finding("fatal", name, check.table, "scan", "runtime-readable scheduler snapshots and projections", loadErr); findingErr != nil {
				return findingErr
			}
			continue
		}
		var count int64
		if countErr := tx.QueryRowContext(ctx, `SELECT count(*) FROM `+check.table).Scan(&count); countErr != nil {
			return fmt.Errorf("count validated %s: %w", check.table, countErr)
		}
		state.count(name, count)
	}
	return nil
}
