package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/hollis-labs/nanite/internal/store"
)

// ReflexService owns reflex definitions: an agent's own and inherited
// class-wide reflexes, per-agent opt-outs, and the pending-reflex review
// queue. It is the write path transports use for agent_reflexes and
// pending_reflexes; the runtime evaluation and execution of reflexes belongs
// to reflexes.Engine.
//
// It never writes the reflex vocabulary tables (reflex_action_kinds and
// friends), which are schema-owned; ValidateDefinition only reads them.
type ReflexService struct {
	store *store.Store
}

func NewReflexService(st *store.Store) *ReflexService {
	return &ReflexService{store: st}
}

var (
	// ErrReflexNotOwned reports a write to a reflex that exists but belongs
	// to another agent or is inherited class-wide.
	ErrReflexNotOwned = errors.New("reflex is inherited or belongs to a different agent")
	// ErrReflexOptOutNotAllowed reports an opt-out from a reflex whose
	// opt_out_allowed is false.
	ErrReflexOptOutNotAllowed = errors.New("reflex does not allow opt-out")
)

// ReflexValidationError carries every problem ValidateDefinition found.
type ReflexValidationError struct {
	Errors []string
}

func (e *ReflexValidationError) Error() string {
	return "invalid reflex definition: " + strings.Join(e.Errors, "; ")
}

// ReflexWriteError wraps a store write rejection, as distinct from a failure
// to read the row back afterwards. Its message is the store's own.
type ReflexWriteError struct {
	Err error
}

func (e *ReflexWriteError) Error() string { return e.Err.Error() }
func (e *ReflexWriteError) Unwrap() error { return e.Err }

// ReflexPatch is a partial update. A nil field leaves the column alone.
// RecurrenceOverrideSeconds of 0 clears the override back to "inherit the
// kind/system default"; a recurrence of exactly zero seconds is never a
// meaningful override, so it serves as the clear sentinel.
type ReflexPatch struct {
	Name                      *string
	TriggerKind               *string
	TriggerSpec               *string
	ActionKind                *string
	ActionSpec                *string
	Status                    *string
	Priority                  *int64
	FiredCount                *int64
	LastFiredAt               *string
	OptOutAllowed             *bool
	RecurrenceOverrideSeconds *int64
}

// ── pending review queue ──

func (s *ReflexService) ListPending(ctx context.Context, status string) ([]store.PendingReflex, error) {
	return s.store.ListPendingReflexes(ctx, status)
}

// ApprovePending turns a pending reflex into an agent reflex. It returns
// store.ErrPendingReflexNotFound when there is no such pending row. It does
// not run ValidateDefinition; the store inserts under its own constraints.
func (s *ReflexService) ApprovePending(ctx context.Context, id, reviewedBy string) (*store.AgentReflex, error) {
	return s.store.ApprovePendingReflex(ctx, id, reviewedBy)
}

// RejectPending returns store.ErrPendingReflexNotFound when there is no such
// pending row or it was already reviewed.
func (s *ReflexService) RejectPending(ctx context.Context, id, reviewedBy, reason string) error {
	return s.store.RejectPendingReflex(ctx, id, reviewedBy, reason)
}

// ── agent reflexes ──

// ListForAgent returns the agent's own reflexes plus the class-wide ones it
// inherits and has not opted out of.
func (s *ReflexService) ListForAgent(ctx context.Context, agentID, classTag string) ([]store.AgentReflex, error) {
	return s.store.ListAgentReflexesForAgent(ctx, agentID, classTag)
}

// Create validates row and inserts it. It returns *ReflexValidationError for
// an invalid definition and *ReflexWriteError when the store rejects the
// insert.
func (s *ReflexService) Create(ctx context.Context, row store.AgentReflex) (*store.AgentReflex, error) {
	if errs := s.ValidateDefinition(ctx, row); len(errs) > 0 {
		return nil, &ReflexValidationError{Errors: errs}
	}
	id, err := s.store.InsertAgentReflex(ctx, row)
	if err != nil {
		return nil, &ReflexWriteError{Err: err}
	}
	return s.store.GetAgentReflex(ctx, id)
}

// GetOwned returns the reflex when it belongs to agentID. It returns
// store.ErrAgentReflexNotFound when absent and ErrReflexNotOwned when the
// reflex is inherited or another agent's.
func (s *ReflexService) GetOwned(ctx context.Context, agentID, reflexID string) (*store.AgentReflex, error) {
	current, err := s.store.GetAgentReflex(ctx, reflexID)
	if err != nil {
		return nil, err
	}
	if current.AgentID != agentID {
		return nil, ErrReflexNotOwned
	}
	return current, nil
}

// Patch applies p to the agent's own reflex: it copies the current row,
// overwrites the fields p sets, validates the result and writes it. Errors
// are those of GetOwned, *ReflexValidationError, store.ErrAgentReflexNotFound
// from the write, or *ReflexWriteError.
func (s *ReflexService) Patch(ctx context.Context, agentID, reflexID string, p ReflexPatch) (*store.AgentReflex, error) {
	current, err := s.GetOwned(ctx, agentID, reflexID)
	if err != nil {
		return nil, err
	}
	updated := *current
	if p.Name != nil {
		updated.Name = *p.Name
	}
	if p.TriggerKind != nil {
		updated.TriggerKind = *p.TriggerKind
	}
	if p.TriggerSpec != nil {
		updated.TriggerSpec = *p.TriggerSpec
	}
	if p.ActionKind != nil {
		updated.ActionKind = *p.ActionKind
	}
	if p.ActionSpec != nil {
		updated.ActionSpec = *p.ActionSpec
	}
	if p.Status != nil {
		updated.Status = *p.Status
	}
	if p.Priority != nil {
		updated.Priority = *p.Priority
	}
	if p.FiredCount != nil {
		updated.FiredCount = *p.FiredCount
	}
	if p.LastFiredAt != nil {
		updated.LastFiredAt = *p.LastFiredAt
	}
	if p.OptOutAllowed != nil {
		updated.OptOutAllowed = *p.OptOutAllowed
	}
	if p.RecurrenceOverrideSeconds != nil {
		if *p.RecurrenceOverrideSeconds == 0 {
			updated.RecurrenceOverrideSeconds = nil
		} else {
			updated.RecurrenceOverrideSeconds = p.RecurrenceOverrideSeconds
		}
	}
	if errs := s.ValidateDefinition(ctx, updated); len(errs) > 0 {
		return nil, &ReflexValidationError{Errors: errs}
	}
	if err := s.store.UpdateAgentReflex(ctx, updated); err != nil {
		if errors.Is(err, store.ErrAgentReflexNotFound) {
			return nil, err
		}
		return nil, &ReflexWriteError{Err: err}
	}
	return s.store.GetAgentReflex(ctx, reflexID)
}

// DeleteOwned deletes the agent's own reflex. Errors are those of GetOwned,
// or the store's delete error.
// Delete removes a reflex by id without an ownership check. It is for
// internal cleanup of reflexes the caller itself installed, such as a team
// run's partially installed routing.
func (s *ReflexService) Delete(ctx context.Context, reflexID string) error {
	return s.store.DeleteAgentReflex(ctx, reflexID)
}

func (s *ReflexService) DeleteOwned(ctx context.Context, agentID, reflexID string) error {
	if _, err := s.GetOwned(ctx, agentID, reflexID); err != nil {
		return err
	}
	return s.store.DeleteAgentReflex(ctx, reflexID)
}

// SetOptOut detaches the agent from a class-wide reflex it would otherwise
// inherit. It returns store.ErrAgentReflexNotFound when absent and
// ErrReflexOptOutNotAllowed when the reflex has opt_out_allowed=false, which
// ListAgentReflexesForAgent would ignore anyway.
func (s *ReflexService) SetOptOut(ctx context.Context, agentID, reflexID string) error {
	reflex, err := s.store.GetAgentReflex(ctx, reflexID)
	if err != nil {
		return err
	}
	if !reflex.OptOutAllowed {
		return ErrReflexOptOutNotAllowed
	}
	return s.store.SetAgentReflexOptOut(ctx, agentID, reflexID)
}

// ClearOptOut re-enables a class-wide reflex the agent opted out of. It
// returns store.ErrAgentReflexNotFound when the reflex is absent.
func (s *ReflexService) ClearOptOut(ctx context.Context, agentID, reflexID string) error {
	if _, err := s.store.GetAgentReflex(ctx, reflexID); err != nil {
		return err
	}
	return s.store.ClearAgentReflexOptOut(ctx, agentID, reflexID)
}

// ValidateDefinition returns every problem with row's shape (trigger/action
// kind and spec JSON) and enforces the provenance-tier declare allow-list:
// whether the resolved provenance tier for this write may declare row's
// action_kind. Create, Patch and the dry-run validate endpoint all call it,
// so the same gate applies whether a definition is written or previewed.
//
// It returns nil, not an empty slice, for a valid row.
//
// The tier is row.ProvenanceTier when the caller has set it (Patch carries
// the existing row's tier forward; a test row can name a tier with no live
// insert path, such as "plugin"). Otherwise it falls back to the rule
// store.InsertAgentReflex applies to an unset tier: CreatedBy "system"
// resolves to "system", anything else to "operator". This fallback never
// invents "plugin".
func (s *ReflexService) ValidateDefinition(ctx context.Context, row store.AgentReflex) []string {
	var errs []string
	if row.Name == "" {
		errs = append(errs, "name is required")
	}
	switch row.TriggerKind {
	case store.ReflexTriggerPredicate, store.ReflexTriggerEvent, store.ReflexTriggerInterval:
	default:
		errs = append(errs, fmt.Sprintf("invalid trigger_kind %q", row.TriggerKind))
	}
	if row.TriggerSpec == "" {
		errs = append(errs, "trigger_spec is required")
	} else {
		var spec map[string]any
		if err := json.Unmarshal([]byte(row.TriggerSpec), &spec); err != nil {
			errs = append(errs, "trigger_spec: invalid JSON: "+err.Error())
		}
	}
	validActionKind := true
	switch row.ActionKind {
	case store.ReflexActionInjectReminder, store.ReflexActionForceToolChoice,
		store.ReflexActionSendMessage, store.ReflexActionHaltSession, store.ReflexActionAddSchedule,
		store.ReflexActionDispatchToAgent, store.ReflexActionResumeLoopRun:
	default:
		validActionKind = false
		errs = append(errs, fmt.Sprintf("invalid action_kind %q", row.ActionKind))
	}
	if validActionKind {
		tier := row.ProvenanceTier
		if tier == "" {
			// Same default InsertAgentReflex applies to an unset tier.
			if row.CreatedBy == "system" {
				tier = "system"
			} else {
				tier = "operator"
			}
		}
		allowed, err := s.store.ActionKindAllowsProvenanceTier(ctx, row.ActionKind, tier)
		if err != nil {
			errs = append(errs, fmt.Sprintf("provenance tier check failed: %v", err))
		} else if !allowed {
			errs = append(errs, fmt.Sprintf("provenance tier %q may not declare action_kind %q", tier, row.ActionKind))
		}
	}
	if row.ActionSpec == "" {
		errs = append(errs, "action_spec is required")
	} else {
		var spec map[string]any
		if err := json.Unmarshal([]byte(row.ActionSpec), &spec); err != nil {
			errs = append(errs, "action_spec: invalid JSON: "+err.Error())
		} else if row.ActionKind == store.ReflexActionDispatchToAgent {
			// agent_slug is dispatch_to_agent's one required field: it names
			// the target agent profile. confidence and reason are optional
			// (reason defaults to "reflex:"+name at the executor).
			slug, _ := spec["agent_slug"].(string)
			if slug == "" {
				errs = append(errs, "action_spec: dispatch_to_agent requires a non-empty agent_slug")
			}
		} else if row.ActionKind == store.ReflexActionResumeLoopRun {
			// loop_run_id is resume_loop_run's one required field:
			// Store.ListAgentReflexesForLoopRun reads it back via
			// json_extract against exactly this key.
			loopRunID, _ := spec["loop_run_id"].(string)
			if loopRunID == "" {
				errs = append(errs, "action_spec: resume_loop_run requires a non-empty loop_run_id")
			}
		}
	}
	switch row.Status {
	case "", store.ReflexStatusActive, store.ReflexStatusPaused, store.ReflexStatusExpired:
	default:
		errs = append(errs, fmt.Sprintf("invalid status %q", row.Status))
	}
	return errs
}
