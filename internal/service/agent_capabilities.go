package service

import (
	"context"
	"errors"

	"github.com/hollis-labs/nanite/internal/store"
)

// AgentCapabilitiesService owns per-row CRUD on an agent's capability tables:
// known tools, known skills, procedures and knowledge seeds. It is the single
// write path transports use for those rows.
//
// It shares agent_procedures with AgentConfigService, which bulk-seeds
// procedures when a profile is created, updated or copied to managed. That
// seeding stays there; editing one procedure row belongs here.
//
// The store writes known tools, known skills and knowledge seeds with INSERT
// OR REPLACE, so an update that omits a column clears it. Each Update method
// therefore copies the current row and then overwrites only the fields its
// input carries: copy-then-overwrite, not a field-by-field rebuild, so a
// column added to the table later is carried forward without anyone
// remembering to list it here. The input types are the complete set of
// caller-settable fields; everything else — usage counters, timestamps and,
// for known skills, the grant state — survives the write. Create and Update
// are read-then-write, not one transaction.
type AgentCapabilitiesService struct {
	store *store.Store
}

func NewAgentCapabilitiesService(st *store.Store) *AgentCapabilitiesService {
	return &AgentCapabilitiesService{store: st}
}

// ErrCapabilityExists reports a create for a row that already exists.
var ErrCapabilityExists = errors.New("capability already exists")

// CapabilityWriteError wraps a store write rejection, as distinct from a
// failure to read the row back afterwards. Its message is the store's own.
type CapabilityWriteError struct {
	Err error
}

func (e *CapabilityWriteError) Error() string { return e.Err.Error() }
func (e *CapabilityWriteError) Unwrap() error { return e.Err }

// KnownToolInput is the caller-settable part of a known-tool row.
type KnownToolInput struct {
	Pinned     bool
	SortOrder  int64
	TTLSeconds int64
	Reason     string
}

// KnownSkillInput is the caller-settable part of a known-skill row. Grant
// state is not settable here.
type KnownSkillInput struct {
	Pinned     bool
	TTLSeconds int64
	Reason     string
}

// ProcedureInput is the caller-settable part of a procedure row.
type ProcedureInput struct {
	Body  string
	Scope string
}

// KnowledgeSeedInput is the caller-settable part of a knowledge-seed row.
// TagsJSON must already be a normalized JSON array.
type KnowledgeSeedInput struct {
	Namespace string
	Body      string
	TagsJSON  string
}

// ── known tools ──

func (s *AgentCapabilitiesService) ListKnownTools(ctx context.Context, agentID string) ([]store.AgentKnownTool, error) {
	return s.store.ListAgentKnownTools(ctx, agentID)
}

// GetKnownTool returns store.ErrAgentKnownToolNotFound when absent.
func (s *AgentCapabilitiesService) GetKnownTool(ctx context.Context, agentID, toolName string) (*store.AgentKnownTool, error) {
	return s.store.GetAgentKnownTool(ctx, agentID, toolName)
}

// CreateKnownTool returns ErrCapabilityExists when the row exists.
func (s *AgentCapabilitiesService) CreateKnownTool(ctx context.Context, agentID, toolName string, in KnownToolInput) (*store.AgentKnownTool, error) {
	if existing, err := s.store.GetAgentKnownTool(ctx, agentID, toolName); err == nil && existing != nil {
		return nil, ErrCapabilityExists
	} else if err != nil && !errors.Is(err, store.ErrAgentKnownToolNotFound) {
		return nil, err
	}
	row := store.AgentKnownTool{
		AgentID:    agentID,
		ToolName:   toolName,
		Pinned:     in.Pinned,
		SortOrder:  in.SortOrder,
		TTLSeconds: in.TTLSeconds,
		Reason:     in.Reason,
	}
	if err := s.store.InsertAgentKnownTool(ctx, row); err != nil {
		return nil, &CapabilityWriteError{Err: err}
	}
	return s.store.GetAgentKnownTool(ctx, agentID, toolName)
}

// UpdateKnownTool overwrites the settable columns of the current row; every
// other column (activation_count, last_used_at, added_at, ...) is kept. It returns
// store.ErrAgentKnownToolNotFound when absent.
func (s *AgentCapabilitiesService) UpdateKnownTool(ctx context.Context, agentID, toolName string, in KnownToolInput) (*store.AgentKnownTool, error) {
	current, err := s.store.GetAgentKnownTool(ctx, agentID, toolName)
	if err != nil {
		return nil, err
	}
	row := *current
	row.Pinned = in.Pinned
	row.SortOrder = in.SortOrder
	row.TTLSeconds = in.TTLSeconds
	row.Reason = in.Reason
	if err := s.store.InsertAgentKnownTool(ctx, row); err != nil {
		return nil, &CapabilityWriteError{Err: err}
	}
	return s.store.GetAgentKnownTool(ctx, agentID, toolName)
}

// DeleteKnownTool returns store.ErrAgentKnownToolNotFound when absent.
func (s *AgentCapabilitiesService) DeleteKnownTool(ctx context.Context, agentID, toolName string) error {
	return s.store.DeleteAgentKnownTool(ctx, agentID, toolName)
}

// ── known skills ──

func (s *AgentCapabilitiesService) ListKnownSkills(ctx context.Context, agentID string) ([]store.AgentKnownSkill, error) {
	return s.store.ListAgentKnownSkills(ctx, agentID)
}

// GetKnownSkill returns store.ErrAgentKnownSkillNotFound when absent.
func (s *AgentCapabilitiesService) GetKnownSkill(ctx context.Context, agentID, skillName string) (*store.AgentKnownSkill, error) {
	return s.store.GetAgentKnownSkill(ctx, agentID, skillName)
}

// CreateKnownSkill returns ErrCapabilityExists when a row carrying real
// known-skill data exists. A bare assignment row — one written by the skill
// assignment endpoint, which shares this (agent_id, skill_name) row space —
// is upserted instead: the bare row is copied and only the settable fields
// are overwritten, so its added_at and any other column survive (see
// store.AgentKnownSkill.IsBareAssignment).
func (s *AgentCapabilitiesService) CreateKnownSkill(ctx context.Context, agentID, skillName string, in KnownSkillInput) (*store.AgentKnownSkill, error) {
	existing, err := s.store.GetAgentKnownSkill(ctx, agentID, skillName)
	if err != nil && !errors.Is(err, store.ErrAgentKnownSkillNotFound) {
		return nil, err
	}
	row := store.AgentKnownSkill{AgentID: agentID, SkillName: skillName}
	if existing != nil {
		if !existing.IsBareAssignment() {
			return nil, ErrCapabilityExists
		}
		row = *existing
	}
	row.Pinned = in.Pinned
	row.TTLSeconds = in.TTLSeconds
	row.Reason = in.Reason
	if err := s.store.InsertAgentKnownSkill(ctx, row); err != nil {
		return nil, &CapabilityWriteError{Err: err}
	}
	return s.store.GetAgentKnownSkill(ctx, agentID, skillName)
}

// UpdateKnownSkill overwrites the settable columns of the current row; every
// other column — usage, added_at and the grant state (approved_content_hash,
// granted_at, granted_by, capabilities_granted) — is kept, so a field edit
// can never silently revoke a grant. It returns
// store.ErrAgentKnownSkillNotFound when absent.
func (s *AgentCapabilitiesService) UpdateKnownSkill(ctx context.Context, agentID, skillName string, in KnownSkillInput) (*store.AgentKnownSkill, error) {
	current, err := s.store.GetAgentKnownSkill(ctx, agentID, skillName)
	if err != nil {
		return nil, err
	}
	row := *current
	row.Pinned = in.Pinned
	row.TTLSeconds = in.TTLSeconds
	row.Reason = in.Reason
	if err := s.store.InsertAgentKnownSkill(ctx, row); err != nil {
		return nil, &CapabilityWriteError{Err: err}
	}
	return s.store.GetAgentKnownSkill(ctx, agentID, skillName)
}

// DeleteKnownSkill returns store.ErrAgentKnownSkillNotFound when absent.
func (s *AgentCapabilitiesService) DeleteKnownSkill(ctx context.Context, agentID, skillName string) error {
	return s.store.DeleteAgentKnownSkill(ctx, agentID, skillName)
}

// ── procedures ──

func (s *AgentCapabilitiesService) ListProcedures(ctx context.Context, agentID string) ([]store.AgentProcedure, error) {
	return s.store.ListAgentProcedures(ctx, agentID)
}

// GetProcedure returns store.ErrAgentProcedureNotFound when absent.
func (s *AgentCapabilitiesService) GetProcedure(ctx context.Context, agentID, name string) (*store.AgentProcedure, error) {
	return s.store.GetAgentProcedure(ctx, agentID, name)
}

// CreateProcedure returns ErrCapabilityExists when the row exists.
func (s *AgentCapabilitiesService) CreateProcedure(ctx context.Context, agentID, name string, in ProcedureInput) (*store.AgentProcedure, error) {
	if existing, err := s.store.GetAgentProcedure(ctx, agentID, name); err == nil && existing != nil {
		return nil, ErrCapabilityExists
	} else if err != nil && !errors.Is(err, store.ErrAgentProcedureNotFound) {
		return nil, err
	}
	row := store.AgentProcedure{AgentID: agentID, Name: name, Body: in.Body, Scope: in.Scope}
	if err := s.store.InsertAgentProcedure(ctx, row); err != nil {
		return nil, &CapabilityWriteError{Err: err}
	}
	return s.store.GetAgentProcedure(ctx, agentID, name)
}

// UpdateProcedure replaces body and scope. The store's upsert sets only body,
// scope and updated_at on conflict, so it keeps created_at itself and the row
// needs no copy. It returns
// store.ErrAgentProcedureNotFound when absent.
func (s *AgentCapabilitiesService) UpdateProcedure(ctx context.Context, agentID, name string, in ProcedureInput) (*store.AgentProcedure, error) {
	if _, err := s.store.GetAgentProcedure(ctx, agentID, name); err != nil {
		return nil, err
	}
	row := store.AgentProcedure{AgentID: agentID, Name: name, Body: in.Body, Scope: in.Scope}
	if err := s.store.InsertAgentProcedure(ctx, row); err != nil {
		return nil, &CapabilityWriteError{Err: err}
	}
	return s.store.GetAgentProcedure(ctx, agentID, name)
}

// DeleteProcedure returns store.ErrAgentProcedureNotFound when absent.
func (s *AgentCapabilitiesService) DeleteProcedure(ctx context.Context, agentID, name string) error {
	return s.store.DeleteAgentProcedure(ctx, agentID, name)
}

// ── knowledge seeds ──

func (s *AgentCapabilitiesService) ListKnowledgeSeeds(ctx context.Context, agentID string) ([]store.AgentKnowledgeSeed, error) {
	return s.store.ListAgentKnowledgeSeeds(ctx, agentID)
}

// GetKnowledgeSeed returns store.ErrAgentKnowledgeSeedNotFound when absent.
func (s *AgentCapabilitiesService) GetKnowledgeSeed(ctx context.Context, agentID, seedKey string) (*store.AgentKnowledgeSeed, error) {
	return s.store.GetAgentKnowledgeSeed(ctx, agentID, seedKey)
}

// CreateKnowledgeSeed returns ErrCapabilityExists when the row exists.
func (s *AgentCapabilitiesService) CreateKnowledgeSeed(ctx context.Context, agentID, seedKey string, in KnowledgeSeedInput) (*store.AgentKnowledgeSeed, error) {
	if existing, err := s.store.GetAgentKnowledgeSeed(ctx, agentID, seedKey); err == nil && existing != nil {
		return nil, ErrCapabilityExists
	} else if err != nil && !errors.Is(err, store.ErrAgentKnowledgeSeedNotFound) {
		return nil, err
	}
	row := store.AgentKnowledgeSeed{
		AgentID:   agentID,
		SeedKey:   seedKey,
		Namespace: in.Namespace,
		Body:      in.Body,
		TagsJSON:  in.TagsJSON,
	}
	if err := s.store.InsertAgentKnowledgeSeed(ctx, row); err != nil {
		return nil, &CapabilityWriteError{Err: err}
	}
	return s.store.GetAgentKnowledgeSeed(ctx, agentID, seedKey)
}

// UpdateKnowledgeSeed overwrites the settable columns of the current row;
// every other column (applied_at, created_at, ...) is kept. It returns store.ErrAgentKnowledgeSeedNotFound
// when absent.
func (s *AgentCapabilitiesService) UpdateKnowledgeSeed(ctx context.Context, agentID, seedKey string, in KnowledgeSeedInput) (*store.AgentKnowledgeSeed, error) {
	current, err := s.store.GetAgentKnowledgeSeed(ctx, agentID, seedKey)
	if err != nil {
		return nil, err
	}
	row := *current
	row.Namespace = in.Namespace
	row.Body = in.Body
	row.TagsJSON = in.TagsJSON
	if err := s.store.InsertAgentKnowledgeSeed(ctx, row); err != nil {
		return nil, &CapabilityWriteError{Err: err}
	}
	return s.store.GetAgentKnowledgeSeed(ctx, agentID, seedKey)
}

// DeleteKnowledgeSeed returns store.ErrAgentKnowledgeSeedNotFound when absent.
func (s *AgentCapabilitiesService) DeleteKnowledgeSeed(ctx context.Context, agentID, seedKey string) error {
	return s.store.DeleteAgentKnowledgeSeed(ctx, agentID, seedKey)
}

// MarkKnowledgeSeedApplied stamps applied_at and returns the updated row. It
// returns store.ErrAgentKnowledgeSeedNotFound when absent.
func (s *AgentCapabilitiesService) MarkKnowledgeSeedApplied(ctx context.Context, agentID, seedKey string) (*store.AgentKnowledgeSeed, error) {
	if err := s.store.MarkAgentKnowledgeSeedApplied(ctx, agentID, seedKey); err != nil {
		return nil, err
	}
	return s.store.GetAgentKnowledgeSeed(ctx, agentID, seedKey)
}
