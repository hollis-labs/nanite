package service

import (
	"context"
	"errors"

	"github.com/hollis-labs/nanite/internal/store"
)

// AgentCapabilitiesService owns per-row CRUD on an agent's capability tables:
// known tools, known skills, procedures and knowledge seeds. It is the single
// write path transports use for those rows. Known-skill rows are also where
// skill assignment and skill grants live, so assigning, removing, granting
// and revoking a skill are methods here too; SkillService stays the skills
// index.
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

// ErrAgentNotFound and ErrSkillNotFound report a missing agent or skill for
// AssignSkill.
var (
	ErrAgentNotFound = errors.New("agent not found")
	ErrSkillNotFound = errors.New("skill not found")
)

// ErrNoSkillGrant reports a revoke for an agent/skill pair with no grant:
// either no known-skill row, or a row with no grant state.
var ErrNoSkillGrant = errors.New("no grant exists for this agent/skill")

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

// ── tool grants ──
//
// agent_tools grants a known_tools catalog row to an agent. It is a different
// table from agent_known_tools above, and together with the always-included
// escape hatch it is the gate on which tools an agent may call.

// ListToolCatalog returns every known_tools catalog row.
func (s *AgentCapabilitiesService) ListToolCatalog(ctx context.Context) ([]store.KnownTool, error) {
	return s.store.ListKnownTools(ctx)
}

// GetCatalogTool returns one known_tools row by ID, or
// store.ErrKnownToolNotFound.
func (s *AgentCapabilitiesService) GetCatalogTool(ctx context.Context, toolID string) (*store.KnownTool, error) {
	return s.store.GetKnownTool(ctx, toolID)
}

// ListGrantedToolNames returns the names of the tools granted to an agent.
func (s *AgentCapabilitiesService) ListGrantedToolNames(ctx context.Context, agentID string) ([]string, error) {
	return s.store.ListAgentToolNames(ctx, agentID)
}

// ListAlwaysAllowedToolNames returns the always-included tools an agent may
// call without a grant. A row counts only while its status is "available".
func (s *AgentCapabilitiesService) ListAlwaysAllowedToolNames(ctx context.Context) ([]string, error) {
	rows, err := s.store.ListAlwaysIncludedKnownTools(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(rows))
	for _, kt := range rows {
		if kt.Status == "available" {
			names = append(names, kt.Name)
		}
	}
	return names, nil
}

// GrantTool grants a known_tools row to an agent. An empty grantedVia is
// recorded as "explicit"; store.GrantAgentTool applies that default.
func (s *AgentCapabilitiesService) GrantTool(ctx context.Context, agentID, toolID, grantedVia string) error {
	return s.store.GrantAgentTool(ctx, agentID, toolID, grantedVia)
}

// RevokeTool removes an agent's grant of a known_tools row.
func (s *AgentCapabilitiesService) RevokeTool(ctx context.Context, agentID, toolID string) error {
	return s.store.RevokeAgentTool(ctx, agentID, toolID)
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
	if err := s.store.UpdateAgentSkillFamiliarity(ctx, agentID, skillName, in.Pinned, in.TTLSeconds, in.Reason); err != nil {
		return nil, &CapabilityWriteError{Err: err}
	}
	return s.store.GetAgentKnownSkill(ctx, agentID, skillName)
}

// DeleteKnownSkill returns store.ErrAgentKnownSkillNotFound when absent.
func (s *AgentCapabilitiesService) DeleteKnownSkill(ctx context.Context, agentID, skillName string) error {
	return s.store.DeleteAgentKnownSkill(ctx, agentID, skillName)
}

// ── skill assignment and grants ──

// ListAssignedSkills returns the skills-index rows assigned to an agent.
func (s *AgentCapabilitiesService) ListAssignedSkills(ctx context.Context, agentID string) ([]store.Skill, error) {
	return s.store.ListAgentSkills(ctx, agentID)
}

// AssignSkill assigns a skill to an agent and returns the agent's assigned
// skills afterwards. It returns ErrAgentNotFound, then ErrSkillNotFound, in
// that order; a failed skill lookup is reported as not found.
//
// The agent check reads agent_profiles directly rather than through
// AgentService: AssignSkillToAgent writes an agent_known_skills row, which
// carries a real foreign key to agent_profiles(id), so the existence check
// must match what that key enforces, independent of AgentService.
func (s *AgentCapabilitiesService) AssignSkill(ctx context.Context, agentID, skillID, config string) ([]store.Skill, error) {
	if _, err := s.store.GetAgentForActor(ctx, agentID); err != nil {
		return nil, err
	}
	if sk, err := s.store.GetSkill(ctx, skillID); err != nil || sk == nil {
		return nil, ErrSkillNotFound
	}
	if err := s.store.AssignSkillToAgent(ctx, agentID, skillID, config); err != nil {
		return nil, err
	}
	return s.store.ListAgentSkills(ctx, agentID)
}

// RemoveSkill removes a skill assignment. The store deletes a bare
// assignment row and keeps a row that carries known-skill data.
func (s *AgentCapabilitiesService) RemoveSkill(ctx context.Context, agentID, skillID string) error {
	return s.store.RemoveSkillFromAgent(ctx, agentID, skillID)
}

// SkillGrant is the grant state GrantSkill writes. ApprovedContentHash is
// the skill's current vendored content hash, never a caller-supplied value.
type SkillGrant struct {
	ApprovedContentHash string
	GrantedBy           string
	CapabilitiesGranted string
}

// GrantSkill records a grant for skillSlug on the agent's known-skill row,
// creating the row if there is none. It copies an existing row and
// overwrites only the grant state (with GrantedAt set to now), so the row's
// pin, usage and every other column survive.
func (s *AgentCapabilitiesService) GrantSkill(ctx context.Context, agentID, skillSlug string, g SkillGrant) (*store.AgentKnownSkill, error) {
	return nil, store.ErrVerifiedActorRequired
}

// RevokeSkillGrant clears only the grant state on the agent's known-skill
// row; revoking a grant is not a reason to forget the row's pin or usage. It
// returns ErrNoSkillGrant when there is no row or the row has no grant.
func (s *AgentCapabilitiesService) RevokeSkillGrant(ctx context.Context, agentID, skillSlug string) error {
	revoked, err := s.store.RevokeAgentSkillGrant(ctx, agentID, skillSlug)
	if err != nil {
		return &CapabilityWriteError{Err: err}
	}
	if !revoked {
		return ErrNoSkillGrant
	}
	return nil
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
