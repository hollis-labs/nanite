package workflowhost

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hollis-labs/go-workflow/compile"

	nanitestore "github.com/hollis-labs/nanite/internal/store"
)

const workflowDefinitionPublisher = "nanite-go-workflow-host@v0.1.0"

// publishDefinition records the immutable authored definition selected by a
// launch and advances its named head before the run is bound. Plan material is
// deliberately persisted first: the product catalog rejects a revision whose
// compiled plan cannot be recovered from workflow_plan_materials.
func (s *WorkflowStateStore) publishDefinition(ctx context.Context, material PlanMaterial) (nanitestore.WorkflowDefinitionRevision, error) {
	if s == nil || s.product == nil {
		return nanitestore.WorkflowDefinitionRevision{}, errors.New("publish workflow definition: product store is unavailable")
	}
	graphDigest, err := compile.GraphDigest(material.Plan.Graph)
	if err != nil {
		return nanitestore.WorkflowDefinitionRevision{}, fmt.Errorf("publish workflow definition: graph digest: %w", err)
	}
	revision := nanitestore.WorkflowDefinitionRevision{
		RevisionID:          workflowDefinitionRevisionID(material),
		DefinitionName:      material.ProductDefinitionName,
		SourceLocator:       material.SourceLocator,
		SourceFormat:        string(material.SourceFormat),
		SchemaVersion:       material.Plan.SchemaVersion,
		SourceDigest:        material.SourceDigest,
		SourceContent:       append([]byte(nil), material.SourceContent...),
		CompiledGraphDigest: graphDigest,
		CompiledPlanDigest:  material.Plan.Digest,
		Engine:              nanitestore.WorkflowEngineIdentityShared,
		RegisteredBy:        workflowDefinitionPublisher,
	}
	persisted, err := s.product.CreateWorkflowDefinitionRevision(ctx, revision)
	if err != nil {
		return nanitestore.WorkflowDefinitionRevision{}, fmt.Errorf("publish workflow definition revision: %w", err)
	}

	// Definition heads are mutable CAS selectors over immutable revisions. A
	// concurrent publisher may advance the same name, so retry only the bounded
	// CAS read/update sequence. The run itself binds by its exact compiled plan,
	// not by whichever revision remains the latest head after this returns.
	for attempts := 0; attempts < 8; attempts++ {
		head, loadErr := s.product.GetWorkflowDefinitionHead(ctx, revision.DefinitionName)
		expected := int64(0)
		switch {
		case loadErr == nil:
			if head.RevisionID == revision.RevisionID {
				return persisted, nil
			}
			expected = head.Generation
		case errors.Is(loadErr, nanitestore.ErrWorkflowDefinitionHeadNotFound):
		default:
			return nanitestore.WorkflowDefinitionRevision{}, fmt.Errorf("publish workflow definition head: load: %w", loadErr)
		}
		_, setErr := s.product.SetWorkflowDefinitionHead(ctx, nanitestore.SetWorkflowDefinitionHeadRequest{
			DefinitionName: revision.DefinitionName, RevisionID: revision.RevisionID,
			ExpectedGeneration: expected, At: material.CreatedAt,
		})
		if setErr == nil {
			return persisted, nil
		}
		if !errors.Is(setErr, nanitestore.ErrWorkflowDefinitionHeadConflict) {
			return nanitestore.WorkflowDefinitionRevision{}, fmt.Errorf("publish workflow definition head: %w", setErr)
		}
	}
	return nanitestore.WorkflowDefinitionRevision{}, fmt.Errorf("publish workflow definition head: %w", nanitestore.ErrWorkflowDefinitionHeadConflict)
}

// workflowDefinitionRevisionID is derived from the exact, source-bound plan
// digest. RecordPlanMaterial already rejects any attempt to reuse that digest
// for a different product definition or authored source.
func workflowDefinitionRevisionID(material PlanMaterial) string {
	return "go-workflow-v0.1.0-" + strings.TrimPrefix(material.Plan.Digest, "sha256:")
}
