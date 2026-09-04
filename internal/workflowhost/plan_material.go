package workflowhost

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hollis-labs/go-workflow/compile"
	"github.com/hollis-labs/go-workflow/graph"
	workflowruntime "github.com/hollis-labs/go-workflow/runtime"
	"github.com/hollis-labs/go-workflow/stepkind"
	"github.com/hollis-labs/go-workflow/values"
	"github.com/hollis-labs/go-workflow/verification"
)

// PlanMaterial is the complete immutable identity needed to resume one plan
// after the process and its mutable definition registry have disappeared.
// SourceContent is exact YAML for graph-native source, or canonical validated
// Graph JSON for generated SDK/agent definitions.
type PlanMaterial struct {
	Plan                  compile.ExecutionPlan
	Visibility            compile.ValueVisibilityPlan
	SourceLocator         string
	SourceFormat          graph.SourceFormat
	SourceDigest          string
	SourceContent         []byte
	ProductDefinitionName string
	StepKindCatalog       []stepkind.StepKindSpec
	StepKindCatalogDigest string
	VerifierCatalog       []verification.VerifierSpec
	VerifierCatalogDigest string
	HostContract          []HostComponentIdentity
	HostContractDigest    string
	CreatedAt             time.Time
}

// RecordPlanMaterial freezes plan, visibility, exact source, and the complete
// StepKindSpec catalog under the plan digest. Exact repeats are idempotent;
// any attempt to reuse the digest with different material fails closed.
func (s *WorkflowStateStore) RecordPlanMaterial(ctx context.Context, material PlanMaterial) error {
	if err := validatePlanMaterial(material); err != nil {
		return workflowInvalid(err)
	}
	planJSON, err := encodeWorkflowJSON(material.Plan)
	if err != nil {
		return err
	}
	visibilityJSON, err := encodeWorkflowJSON(material.Visibility)
	if err != nil {
		return err
	}
	catalogJSON, err := encodeWorkflowJSON(material.StepKindCatalog)
	if err != nil {
		return err
	}
	verifierJSON, err := encodeWorkflowJSON(material.VerifierCatalog)
	if err != nil {
		return err
	}
	hostContractJSON, err := encodeWorkflowJSON(material.HostContract)
	if err != nil {
		return err
	}
	planRef := workflowruntime.PlanRef{
		ID: material.Plan.ID, Version: material.Plan.Graph.Version,
		Digest: material.Plan.Digest, SchemaVersion: material.Plan.SchemaVersion,
	}
	return s.write(ctx, "record workflow plan material", func(query workflowSQL) error {
		if err := ensureWorkflowPlan(ctx, query, planRef); err != nil {
			return err
		}
		_, err := query.ExecContext(ctx, `
INSERT INTO workflow_plan_materials(
    plan_digest, plan_json, visibility_json, source_locator, source_format,
    source_digest, source_content, product_definition_name,
    stepkind_catalog_json, stepkind_catalog_digest,
    verifier_catalog_json, verifier_catalog_digest,
    host_contract_json, host_contract_digest, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(plan_digest) DO NOTHING`,
			material.Plan.Digest, planJSON, visibilityJSON, material.SourceLocator,
			material.SourceFormat, material.SourceDigest, material.SourceContent,
			material.ProductDefinitionName, catalogJSON, material.StepKindCatalogDigest,
			verifierJSON, material.VerifierCatalogDigest,
			hostContractJSON, material.HostContractDigest, workflowTime(material.CreatedAt),
		)
		if err != nil {
			return fmt.Errorf("record workflow plan material: %w", err)
		}
		stored, err := loadPlanMaterial(ctx, query, material.Plan.Digest)
		if err != nil {
			return err
		}
		if !equalPlanMaterial(material, stored) {
			return fmt.Errorf("%w: plan digest %q has different immutable material", workflowruntime.ErrAlreadyExists, material.Plan.Digest)
		}
		return nil
	})
}

// LoadPlanMaterial returns and integrity-checks exact frozen plan material.
func (s *WorkflowStateStore) LoadPlanMaterial(ctx context.Context, digest string) (PlanMaterial, error) {
	if err := checkWorkflowContext(ctx); err != nil {
		return PlanMaterial{}, err
	}
	return loadPlanMaterial(ctx, s.db, digest)
}

// LoadRecoveryPlan implements runtime.RecoveryPlanSource. It loads only exact
// persisted material and refuses to recover legacy rows that have no snapshot.
func (s *WorkflowStateStore) LoadRecoveryPlan(ctx context.Context, run workflowruntime.RunSnapshot) (workflowruntime.RecoveryPlan, error) {
	material, err := s.LoadPlanMaterial(ctx, run.Plan.Digest)
	if err != nil {
		return workflowruntime.RecoveryPlan{}, err
	}
	ref := workflowruntime.PlanRef{
		ID: material.Plan.ID, Version: material.Plan.Graph.Version,
		Digest: material.Plan.Digest, SchemaVersion: material.Plan.SchemaVersion,
	}
	if ref != run.Plan {
		return workflowruntime.RecoveryPlan{}, workflowInvalid(errors.New("persisted recovery plan identity differs from run"))
	}
	recovery := workflowruntime.RecoveryPlan{Ref: ref, Plan: material.Plan, Visibility: material.Visibility}
	if err := recovery.Validate(); err != nil {
		return workflowruntime.RecoveryPlan{}, workflowInvalid(err)
	}
	return recovery, nil
}

func loadPlanMaterial(ctx context.Context, query workflowSQL, digest string) (PlanMaterial, error) {
	var (
		material                                            PlanMaterial
		planJSON, visibilityJSON, catalogJSON, verifierJSON string
		hostContractJSON, createdAt                         string
		sourceFormat                                        string
	)
	err := query.QueryRowContext(ctx, `
SELECT plan_json, visibility_json, source_locator, source_format,
       source_digest, source_content, product_definition_name,
       stepkind_catalog_json, stepkind_catalog_digest,
       verifier_catalog_json, verifier_catalog_digest,
       host_contract_json, host_contract_digest, created_at
FROM workflow_plan_materials WHERE plan_digest = ?`, digest).Scan(
		&planJSON, &visibilityJSON, &material.SourceLocator, &sourceFormat,
		&material.SourceDigest, &material.SourceContent, &material.ProductDefinitionName,
		&catalogJSON, &material.StepKindCatalogDigest,
		&verifierJSON, &material.VerifierCatalogDigest,
		&hostContractJSON, &material.HostContractDigest, &createdAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return PlanMaterial{}, fmt.Errorf("%w: exact material for plan %q", workflowruntime.ErrNotFound, digest)
	}
	if err != nil {
		return PlanMaterial{}, fmt.Errorf("load workflow plan material: %w", err)
	}
	material.SourceFormat = graph.SourceFormat(sourceFormat)
	if decodeErr := decodeWorkflowJSON("workflow plan material", planJSON, &material.Plan); decodeErr != nil {
		return PlanMaterial{}, decodeErr
	}
	if decodeErr := decodeWorkflowJSON("workflow visibility material", visibilityJSON, &material.Visibility); decodeErr != nil {
		return PlanMaterial{}, decodeErr
	}
	if decodeErr := decodeWorkflowJSON("workflow step-kind catalog", catalogJSON, &material.StepKindCatalog); decodeErr != nil {
		return PlanMaterial{}, decodeErr
	}
	if decodeErr := decodeWorkflowJSON("workflow verifier catalog", verifierJSON, &material.VerifierCatalog); decodeErr != nil {
		return PlanMaterial{}, decodeErr
	}
	if decodeErr := decodeWorkflowJSON("workflow host contract", hostContractJSON, &material.HostContract); decodeErr != nil {
		return PlanMaterial{}, decodeErr
	}
	material.CreatedAt, err = parseWorkflowTime("workflow plan material created_at", createdAt)
	if err != nil {
		return PlanMaterial{}, err
	}
	if material.Plan.Digest != digest {
		return PlanMaterial{}, workflowInvalid(errors.New("workflow plan material row key differs from plan digest"))
	}
	if err := validatePlanMaterial(material); err != nil {
		return PlanMaterial{}, workflowInvalid(err)
	}
	return material, nil
}

func validatePlanMaterial(material PlanMaterial) error {
	if material.CreatedAt.IsZero() || material.SourceLocator == "" || len(material.SourceContent) == 0 || strings.TrimSpace(material.ProductDefinitionName) == "" {
		return errors.New("plan material requires creation time, source locator, source content, and product definition name")
	}
	if !material.SourceFormat.Valid() || material.SourceFormat == graph.SourceArchivedBlueprint || material.SourceFormat == graph.SourceArchivedPipeline {
		return fmt.Errorf("unsupported exact source format %q", material.SourceFormat)
	}
	if digest := values.SHA256Digest(material.SourceContent); digest != material.SourceDigest {
		return fmt.Errorf("source digest mismatch: recorded %q, computed %q", material.SourceDigest, digest)
	}
	sourceBound := false
	for _, source := range material.Plan.SourceDigests {
		if source.Format == material.SourceFormat && source.Digest == material.SourceDigest {
			sourceBound = true
			break
		}
	}
	if !sourceBound {
		return errors.New("exact source digest is not bound into the execution plan")
	}
	for _, node := range material.Plan.Graph.Nodes {
		if strings.TrimSpace(node.KindVersion) == "" {
			return fmt.Errorf("workflow node %q does not pin an exact StepKind version", node.ID)
		}
	}
	planDigest, err := compile.PlanDigest(material.Plan)
	if err != nil {
		return fmt.Errorf("recompute plan digest: %w", err)
	}
	if planDigest != material.Plan.Digest {
		return fmt.Errorf("plan digest mismatch: recorded %q, computed %q", material.Plan.Digest, planDigest)
	}
	ref := workflowruntime.PlanRef{
		ID: material.Plan.ID, Version: material.Plan.Graph.Version,
		Digest: material.Plan.Digest, SchemaVersion: material.Plan.SchemaVersion,
	}
	if validationErr := ref.Validate(); validationErr != nil {
		return validationErr
	}
	recovery := workflowruntime.RecoveryPlan{Ref: ref, Plan: material.Plan, Visibility: material.Visibility}
	if validationErr := recovery.Validate(); validationErr != nil {
		return validationErr
	}
	if len(material.StepKindCatalog) == 0 {
		return errors.New("step-kind catalog snapshot is required")
	}
	for index, spec := range material.StepKindCatalog {
		if validationErr := stepkind.ValidateSpec(spec); validationErr != nil {
			return fmt.Errorf("step-kind catalog item %d: %w", index, validationErr)
		}
		if index > 0 {
			previous := material.StepKindCatalog[index-1]
			if previous.Name > spec.Name || previous.Name == spec.Name && previous.Version >= spec.Version {
				return errors.New("step-kind catalog must be unique and sorted by exact name/version")
			}
		}
	}
	catalogJSON, err := json.Marshal(material.StepKindCatalog)
	if err != nil {
		return fmt.Errorf("marshal step-kind catalog: %w", err)
	}
	if digest := values.SHA256Digest(catalogJSON); digest != material.StepKindCatalogDigest {
		return fmt.Errorf("step-kind catalog digest mismatch: recorded %q, computed %q", material.StepKindCatalogDigest, digest)
	}
	if err := validateVerifierCatalog(material.VerifierCatalog, material.VerifierCatalogDigest); err != nil {
		return err
	}
	if err := validateHostContract(material.HostContract, material.HostContractDigest); err != nil {
		return err
	}
	return nil
}

func equalPlanMaterial(left, right PlanMaterial) bool {
	left.CreatedAt = right.CreatedAt
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

var _ workflowruntime.RecoveryPlanSource = (*WorkflowStateStore)(nil)
