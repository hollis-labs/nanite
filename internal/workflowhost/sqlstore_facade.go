package workflowhost

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/hollis-labs/go-workflow-host/sqlstore"
	workflowruntime "github.com/hollis-labs/go-workflow/runtime"
	nanitestore "github.com/hollis-labs/nanite/internal/store"
)

// SQLWorkflowStateStore binds the released runtime store to Nanite's existing
// tables and transactional product projections. Nanite owns schema migration,
// frozen material, definition publication, and the database lifetime.
type SQLWorkflowStateStore struct {
	releasedRuntimeStore
	shared  *sqlstore.Store
	db      *sql.DB
	product *nanitestore.Store
}

// The private interface exposes canonical operations but never the released
// store's raw DB or transaction escape hatch. Callers cannot replace it.
type releasedRuntimeStore interface {
	workflowruntime.StateStore
	workflowruntime.WaitStore
	workflowruntime.WaitTimeoutStore
	workflowruntime.RecoveryStore
	workflowruntime.ChildTerminalWaitStore
	workflowruntime.ControlFlowStore
	workflowruntime.MemoStore
	workflowruntime.PinStore
	workflowruntime.OutputReuseStore
	workflowruntime.ValueRecordStore
	workflowruntime.ReactorStore
	workflowruntime.ReplayStore
	workflowruntime.NodeInputStore
	workflowruntime.RunControlStore
	workflowruntime.RunPolicyStore
	workflowruntime.SchedulerResourceStore
	workflowruntime.ServiceStore
	workflowruntime.CompensationStore
}

// Host-owned writes share the released store's BEGIN IMMEDIATE owner.
func (s *SQLWorkflowStateStore) write(ctx context.Context, operation string, fn func(workflowSQL) error) error {
	return s.shared.WriteTx(ctx, operation, func(tx sqlstore.DBTX) error { return fn(tx) })
}

// NewSQLWorkflowStateStore shares an already migrated product database. It
// neither applies library DDL nor starts workers. Hooks are mandatory and cannot
// be replaced through constructor options.
func NewSQLWorkflowStateStore(product *nanitestore.Store) (*SQLWorkflowStateStore, error) {
	if product == nil || product.DB == nil {
		return nil, fmt.Errorf("workflow state store requires an open persistence store")
	}
	shared, err := sqlstore.New(product.DB,
		sqlstore.WithRunColumns(sqlstore.RunColumns{
			Table: "workflow_runs", ID: "id", Status: "runtime_status",
			Generation: "runtime_generation", CreatedAt: "started_at",
		}),
		sqlstore.WithHooks(workflowProductHooks{}),
	)
	if err != nil {
		return nil, err
	}
	return &SQLWorkflowStateStore{releasedRuntimeStore: shared, shared: shared, db: product.DB, product: product}, nil
}

// Hooks use only tx: opening another connection here breaks atomicity and can
// deadlock a single-connection database.
type workflowProductHooks struct{}

func (workflowProductHooks) AfterRunWritten(ctx context.Context, tx sqlstore.DBTX, run workflowruntime.RunSnapshot) error {
	status, err := projectWorkflowRunStatus(ctx, tx, run)
	if err != nil {
		return err
	}
	if _, projectionErr := tx.ExecContext(ctx, `UPDATE workflow_runs SET status=?, completed_at=? WHERE id=?`,
		status, projectedCompletionTime(run.Status, run.UpdatedAt), run.ID); projectionErr != nil {
		return fmt.Errorf("project workflow run: %w", projectionErr)
	}
	if run.Generation != 1 {
		// Storage adoption is not an engine upgrade: preserve existing identity,
		// including the frozen pilot, on every subsequent canonical write.
		return nil
	}
	name := run.Plan.ID
	var revision any
	err = tx.QueryRowContext(ctx, `SELECT product_definition_name FROM workflow_plan_materials WHERE plan_digest=?`, run.Plan.Digest).Scan(&name)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// Raw-plan StateStore callers follow the existing conformance fallback.
		// Engine.launch must publish frozen material and an exact revision first.
	case err != nil:
		return fmt.Errorf("load workflow product definition identity: %w", err)
	default:
		var id string
		if revisionErr := tx.QueryRowContext(ctx, `
SELECT revision_id FROM workflow_definition_revisions
WHERE definition_name=? AND compiled_plan_digest=? AND engine_kind=? AND engine_contract_version=?`,
			name, run.Plan.Digest, EngineKindGoWorkflow, EngineContractVersion).Scan(&id); revisionErr != nil {
			if errors.Is(revisionErr, sql.ErrNoRows) {
				return workflowInvalid(fmt.Errorf("exact immutable definition revision is not published for plan %q", run.Plan.Digest))
			}
			return fmt.Errorf("resolve exact workflow definition revision: %w", revisionErr)
		}
		revision = id
	}
	_, err = tx.ExecContext(ctx, `
UPDATE workflow_runs SET definition_name=?, engine_kind=?, engine_contract_version=?, definition_revision_id=? WHERE id=?`,
		name, EngineKindGoWorkflow, EngineContractVersion, revision, run.ID)
	if err != nil {
		return fmt.Errorf("project workflow definition identity: %w", err)
	}
	return nil
}

func (workflowProductHooks) AfterNodeWritten(ctx context.Context, tx sqlstore.DBTX, node workflowruntime.NodeInvocationSnapshot) error {
	return projectWorkflowNode(ctx, tx, node)
}

var _ sqlstore.Hooks = workflowProductHooks{}
