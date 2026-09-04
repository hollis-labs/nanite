package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrWorkflowDefinitionRevisionNotFound = errors.New("workflow definition revision not found")
	ErrWorkflowDefinitionRevisionConflict = errors.New("workflow definition revision conflicts with immutable material")
	ErrWorkflowDefinitionHeadNotFound     = errors.New("workflow definition head not found")
	ErrWorkflowDefinitionHeadConflict     = errors.New("workflow definition head generation conflict")
)

// WorkflowDefinitionRevision is the exact, immutable authored and compiled
// material selected for a named workflow. Generated Team definitions store
// their canonical schema-validated graph bytes in SourceContent just as a
// file-backed definition stores its exact authored bytes.
type WorkflowDefinitionRevision struct {
	RevisionID          string
	DefinitionName      string
	SourceLocator       string
	SourceFormat        string
	SchemaVersion       string
	SourceDigest        string
	SourceContent       []byte
	CompiledGraphDigest string
	CompiledPlanDigest  string
	Engine              WorkflowEngineIdentity
	RegisteredBy        string
	CreatedAt           time.Time
}

const workflowDefinitionRevisionColumns = `revision_id, definition_name,
       source_locator, source_format, schema_version, source_digest,
       source_content, compiled_graph_digest, compiled_plan_digest,
       engine_kind, engine_contract_version, registered_by, created_at`

func scanWorkflowDefinitionRevision(scanner interface{ Scan(...any) error }) (WorkflowDefinitionRevision, error) {
	var revision WorkflowDefinitionRevision
	var createdAt string
	if err := scanner.Scan(
		&revision.RevisionID, &revision.DefinitionName, &revision.SourceLocator,
		&revision.SourceFormat, &revision.SchemaVersion, &revision.SourceDigest,
		&revision.SourceContent, &revision.CompiledGraphDigest,
		&revision.CompiledPlanDigest, &revision.Engine.Kind,
		&revision.Engine.ContractVersion, &revision.RegisteredBy, &createdAt,
	); err != nil {
		return WorkflowDefinitionRevision{}, err
	}
	parsed, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return WorkflowDefinitionRevision{}, fmt.Errorf("parse workflow definition revision created_at: %w", err)
	}
	revision.CreatedAt = parsed
	return revision, nil
}

// CreateWorkflowDefinitionRevision inserts immutable definition material.
// Replaying the same revision identity and exact content is idempotent;
// reusing that identity for different material is rejected.
func (s *Store) CreateWorkflowDefinitionRevision(ctx context.Context, revision WorkflowDefinitionRevision) (WorkflowDefinitionRevision, error) {
	createdAtSupplied := !revision.CreatedAt.IsZero()
	if err := validateWorkflowDefinitionRevision(revision); err != nil {
		return WorkflowDefinitionRevision{}, err
	}
	if revision.CreatedAt.IsZero() {
		revision.CreatedAt = time.Now().UTC()
	}
	_, insertErr := s.DB.ExecContext(ctx, `
INSERT INTO workflow_definition_revisions(
    revision_id, definition_name, source_locator, source_format,
    schema_version, source_digest, source_content, compiled_graph_digest,
    compiled_plan_digest, engine_kind, engine_contract_version,
    registered_by, created_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(revision_id) DO NOTHING`,
		revision.RevisionID, revision.DefinitionName, revision.SourceLocator,
		revision.SourceFormat, revision.SchemaVersion, revision.SourceDigest,
		revision.SourceContent, revision.CompiledGraphDigest,
		revision.CompiledPlanDigest, revision.Engine.Kind,
		revision.Engine.ContractVersion, revision.RegisteredBy,
		workflowCutoverTime(revision.CreatedAt))
	if insertErr != nil {
		return WorkflowDefinitionRevision{}, fmt.Errorf("%w: insert: %w", ErrWorkflowDefinitionRevisionConflict, insertErr)
	}
	persisted, err := s.GetWorkflowDefinitionRevision(ctx, revision.RevisionID)
	if err != nil {
		return WorkflowDefinitionRevision{}, err
	}
	if !equalWorkflowDefinitionRevision(persisted, revision, createdAtSupplied) {
		return WorkflowDefinitionRevision{}, ErrWorkflowDefinitionRevisionConflict
	}
	return persisted, nil
}

func validateWorkflowDefinitionRevision(revision WorkflowDefinitionRevision) error {
	if strings.TrimSpace(revision.RevisionID) == "" || strings.TrimSpace(revision.DefinitionName) == "" ||
		strings.TrimSpace(revision.SourceLocator) == "" || strings.TrimSpace(revision.SourceFormat) == "" ||
		strings.TrimSpace(revision.SchemaVersion) == "" || len(revision.SourceContent) == 0 ||
		strings.TrimSpace(revision.RegisteredBy) == "" {
		return fmt.Errorf("create workflow definition revision: identity, source metadata/content, and registered_by are required")
	}
	if !SupportedEmbeddedWorkflowEngineIdentity(revision.Engine) {
		return fmt.Errorf("create workflow definition revision: unsupported engine identity %q@%q", revision.Engine.Kind, revision.Engine.ContractVersion)
	}
	for label, digest := range map[string]string{
		"source":         revision.SourceDigest,
		"compiled graph": revision.CompiledGraphDigest,
		"compiled plan":  revision.CompiledPlanDigest,
	} {
		if !validWorkflowSHA256Digest(digest) {
			return fmt.Errorf("create workflow definition revision: %s digest is not canonical SHA-256", label)
		}
	}
	if workflowSHA256Digest(revision.SourceContent) != revision.SourceDigest {
		return fmt.Errorf("create workflow definition revision: source digest does not match authored bytes")
	}
	return nil
}

func equalWorkflowDefinitionRevision(persisted, requested WorkflowDefinitionRevision, compareCreatedAt bool) bool {
	if persisted.RevisionID != requested.RevisionID || persisted.DefinitionName != requested.DefinitionName ||
		persisted.SourceLocator != requested.SourceLocator || persisted.SourceFormat != requested.SourceFormat ||
		persisted.SchemaVersion != requested.SchemaVersion || persisted.SourceDigest != requested.SourceDigest ||
		!bytes.Equal(persisted.SourceContent, requested.SourceContent) ||
		persisted.CompiledGraphDigest != requested.CompiledGraphDigest ||
		persisted.CompiledPlanDigest != requested.CompiledPlanDigest || persisted.Engine != requested.Engine ||
		persisted.RegisteredBy != requested.RegisteredBy {
		return false
	}
	return !compareCreatedAt || persisted.CreatedAt.Equal(requested.CreatedAt)
}

func (s *Store) GetWorkflowDefinitionRevision(ctx context.Context, revisionID string) (WorkflowDefinitionRevision, error) {
	revision, err := scanWorkflowDefinitionRevision(s.DB.QueryRowContext(ctx,
		`SELECT `+workflowDefinitionRevisionColumns+` FROM workflow_definition_revisions WHERE revision_id = ?`,
		revisionID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return WorkflowDefinitionRevision{}, ErrWorkflowDefinitionRevisionNotFound
	}
	if err != nil {
		return WorkflowDefinitionRevision{}, fmt.Errorf("get workflow definition revision: %w", err)
	}
	return revision, nil
}

func (s *Store) ListWorkflowDefinitionRevisions(ctx context.Context, definitionName string) ([]WorkflowDefinitionRevision, error) {
	if strings.TrimSpace(definitionName) == "" {
		return nil, fmt.Errorf("list workflow definition revisions: definition name is required")
	}
	rows, err := s.DB.QueryContext(ctx, `
SELECT `+workflowDefinitionRevisionColumns+`
FROM workflow_definition_revisions
WHERE definition_name = ?
ORDER BY created_at, revision_id`, definitionName)
	if err != nil {
		return nil, fmt.Errorf("list workflow definition revisions: %w", err)
	}
	defer closeRows(rows)
	result := make([]WorkflowDefinitionRevision, 0)
	for rows.Next() {
		revision, scanErr := scanWorkflowDefinitionRevision(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("list workflow definition revisions: scan: %w", scanErr)
		}
		result = append(result, revision)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list workflow definition revisions: iterate: %w", err)
	}
	return result, nil
}

type WorkflowDefinitionHead struct {
	DefinitionName string
	RevisionID     string
	Generation     int64
	UpdatedAt      time.Time
}

const workflowDefinitionHeadColumns = `definition_name, revision_id, generation, updated_at`

func scanWorkflowDefinitionHead(scanner interface{ Scan(...any) error }) (WorkflowDefinitionHead, error) {
	var head WorkflowDefinitionHead
	var updatedAt string
	if err := scanner.Scan(&head.DefinitionName, &head.RevisionID, &head.Generation, &updatedAt); err != nil {
		return WorkflowDefinitionHead{}, err
	}
	parsed, err := time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return WorkflowDefinitionHead{}, fmt.Errorf("parse workflow definition head updated_at: %w", err)
	}
	head.UpdatedAt = parsed
	return head, nil
}

func (s *Store) GetWorkflowDefinitionHead(ctx context.Context, definitionName string) (WorkflowDefinitionHead, error) {
	head, err := scanWorkflowDefinitionHead(s.DB.QueryRowContext(ctx,
		`SELECT `+workflowDefinitionHeadColumns+` FROM workflow_definition_heads WHERE definition_name = ?`,
		definitionName,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return WorkflowDefinitionHead{}, ErrWorkflowDefinitionHeadNotFound
	}
	if err != nil {
		return WorkflowDefinitionHead{}, fmt.Errorf("get workflow definition head: %w", err)
	}
	return head, nil
}

type SetWorkflowDefinitionHeadRequest struct {
	DefinitionName     string
	RevisionID         string
	ExpectedGeneration int64
	At                 time.Time
}

// SetWorkflowDefinitionHead selects an immutable revision using generation
// CAS. Selecting the already-active revision is an idempotent replay. Selecting
// an older immutable revision is the supported definition rollback mechanism.
func (s *Store) SetWorkflowDefinitionHead(ctx context.Context, request SetWorkflowDefinitionHeadRequest) (WorkflowDefinitionHead, error) {
	if strings.TrimSpace(request.DefinitionName) == "" || strings.TrimSpace(request.RevisionID) == "" || request.ExpectedGeneration < 0 {
		return WorkflowDefinitionHead{}, fmt.Errorf("set workflow definition head: definition, revision, and non-negative expected generation are required")
	}
	if request.At.IsZero() {
		request.At = time.Now().UTC()
	}
	revision, err := s.GetWorkflowDefinitionRevision(ctx, request.RevisionID)
	if err != nil {
		return WorkflowDefinitionHead{}, err
	}
	if revision.DefinitionName != request.DefinitionName {
		return WorkflowDefinitionHead{}, ErrWorkflowDefinitionHeadConflict
	}

	head, loadErr := s.GetWorkflowDefinitionHead(ctx, request.DefinitionName)
	if errors.Is(loadErr, ErrWorkflowDefinitionHeadNotFound) {
		if request.ExpectedGeneration != 0 {
			return WorkflowDefinitionHead{}, ErrWorkflowDefinitionHeadConflict
		}
		if _, insertErr := s.DB.ExecContext(ctx, `
INSERT INTO workflow_definition_heads(definition_name, revision_id, generation, updated_at)
VALUES (?, ?, 1, ?)
ON CONFLICT(definition_name) DO NOTHING`, request.DefinitionName, request.RevisionID, workflowCutoverTime(request.At)); insertErr != nil {
			return WorkflowDefinitionHead{}, fmt.Errorf("%w: insert: %w", ErrWorkflowDefinitionHeadConflict, insertErr)
		}
		head, err = s.GetWorkflowDefinitionHead(ctx, request.DefinitionName)
		if err != nil {
			return WorkflowDefinitionHead{}, err
		}
		if head.RevisionID != request.RevisionID {
			return WorkflowDefinitionHead{}, ErrWorkflowDefinitionHeadConflict
		}
		return head, nil
	}
	if loadErr != nil {
		return WorkflowDefinitionHead{}, loadErr
	}
	if head.RevisionID == request.RevisionID {
		return head, nil
	}
	if head.Generation != request.ExpectedGeneration {
		return WorkflowDefinitionHead{}, ErrWorkflowDefinitionHeadConflict
	}
	result, updateErr := s.DB.ExecContext(ctx, `
UPDATE workflow_definition_heads
SET revision_id = ?, generation = generation + 1, updated_at = ?
WHERE definition_name = ? AND generation = ?`, request.RevisionID,
		workflowCutoverTime(request.At), request.DefinitionName, request.ExpectedGeneration)
	if updateErr != nil {
		return WorkflowDefinitionHead{}, fmt.Errorf("set workflow definition head: update: %w", updateErr)
	}
	count, rowsErr := result.RowsAffected()
	if rowsErr != nil {
		return WorkflowDefinitionHead{}, fmt.Errorf("set workflow definition head: rows affected: %w", rowsErr)
	}
	if count != 1 {
		current, currentErr := s.GetWorkflowDefinitionHead(ctx, request.DefinitionName)
		if currentErr == nil && current.RevisionID == request.RevisionID {
			return current, nil
		}
		return WorkflowDefinitionHead{}, ErrWorkflowDefinitionHeadConflict
	}
	head, err = s.GetWorkflowDefinitionHead(ctx, request.DefinitionName)
	if err != nil {
		return WorkflowDefinitionHead{}, err
	}
	return head, nil
}

func (s *Store) GetActiveWorkflowDefinitionRevision(ctx context.Context, definitionName string) (WorkflowDefinitionRevision, WorkflowDefinitionHead, error) {
	head, err := s.GetWorkflowDefinitionHead(ctx, definitionName)
	if err != nil {
		return WorkflowDefinitionRevision{}, WorkflowDefinitionHead{}, err
	}
	revision, err := s.GetWorkflowDefinitionRevision(ctx, head.RevisionID)
	if err != nil {
		return WorkflowDefinitionRevision{}, WorkflowDefinitionHead{}, err
	}
	return revision, head, nil
}

func workflowSHA256Digest(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validWorkflowSHA256Digest(digest string) bool {
	if len(digest) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(digest, "sha256:") {
		return false
	}
	hexValue := strings.TrimPrefix(digest, "sha256:")
	if strings.ToLower(hexValue) != hexValue {
		return false
	}
	_, err := hex.DecodeString(hexValue)
	return err == nil
}
