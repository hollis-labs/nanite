package service

import (
	"context"
	"errors"

	"github.com/hollis-labs/nanite/internal/store"
)

// LoopService owns the records side of Loops: goal CRUD, goal evidence, and
// reading loop runs and their iterations. The actions side — launching,
// canceling and resolving a loop run — belongs to loop.LoopLauncher, which is
// wired separately and may be absent; keeping records here means goal CRUD
// and loop-run reads work whether or not a launcher is configured.
type LoopService struct {
	store *store.Store
}

func NewLoopService(st *store.Store) *LoopService {
	return &LoopService{store: st}
}

// GoalWriteError wraps a caller-correctable failure to write a goal: a list
// field that will not encode, or a store rejection. Its message is the
// underlying one.
type GoalWriteError struct {
	Err error
}

func (e *GoalWriteError) Error() string { return e.Err.Error() }
func (e *GoalWriteError) Unwrap() error { return e.Err }

// GoalInput is a new goal's authoring fields. Status defaults to draft in
// the store when empty.
type GoalInput struct {
	ParentGoalID       string
	Intent             string
	DesiredState       []string
	Constraints        []string
	AcceptanceCriteria []string
	Invariants         []string
	Priority           string
	Scope              string
	Owner              string
	Source             string
	Status             string
}

// GoalPatch is a partial update. A nil field leaves the column alone.
// Status goes through the narrow lifecycle updater, every other field
// through the definition updater; id and the lifecycle timestamps are not
// patchable.
type GoalPatch struct {
	ParentGoalID       *string
	Intent             *string
	DesiredState       *[]string
	Constraints        *[]string
	AcceptanceCriteria *[]string
	Invariants         *[]string
	Priority           *string
	Scope              *string
	Owner              *string
	Source             *string
	Status             *string
}

// ── goals ──

// CreateGoal creates a goal and returns it with its generated fields
// filled in. Every failure is a *GoalWriteError: field validation (intent
// required, status membership) is the store's.
func (s *LoopService) CreateGoal(ctx context.Context, in GoalInput) (*store.Goal, error) {
	g := &store.Goal{
		ParentGoalID: in.ParentGoalID,
		Intent:       in.Intent,
		Priority:     in.Priority,
		Scope:        in.Scope,
		Owner:        in.Owner,
		Source:       in.Source,
		Status:       in.Status,
	}
	if err := setGoalLists(g, &in.DesiredState, &in.Constraints, &in.AcceptanceCriteria, &in.Invariants); err != nil {
		return nil, err
	}
	if err := s.store.CreateGoal(ctx, g); err != nil {
		return nil, &GoalWriteError{Err: err}
	}
	return g, nil
}

// GetGoal returns store.ErrGoalNotFound when absent.
func (s *LoopService) GetGoal(ctx context.Context, id string) (*store.Goal, error) {
	return s.store.GetGoal(ctx, id)
}

func (s *LoopService) ListGoals(ctx context.Context, filter store.GoalFilter) ([]store.Goal, error) {
	return s.store.ListGoals(ctx, filter)
}

// PatchGoal copies the current goal, applies p, and writes the definition
// columns (only when p touches one) and then the status (only when p sets
// it). The two writes are separate statements, not one transaction: a
// rejected status leaves an accepted definition change in place. It returns
// store.ErrGoalNotFound when absent and *GoalWriteError for a rejected field
// or write.
func (s *LoopService) PatchGoal(ctx context.Context, id string, p GoalPatch) (*store.Goal, error) {
	current, err := s.store.GetGoal(ctx, id)
	if err != nil {
		return nil, err
	}
	updated := *current
	touchedDefinition := false
	for _, f := range []struct {
		src *string
		dst *string
	}{
		{p.ParentGoalID, &updated.ParentGoalID},
		{p.Intent, &updated.Intent},
		{p.Priority, &updated.Priority},
		{p.Scope, &updated.Scope},
		{p.Owner, &updated.Owner},
		{p.Source, &updated.Source},
	} {
		if f.src != nil {
			*f.dst = *f.src
			touchedDefinition = true
		}
	}
	if p.DesiredState != nil || p.Constraints != nil || p.AcceptanceCriteria != nil || p.Invariants != nil {
		if err := setGoalLists(&updated, p.DesiredState, p.Constraints, p.AcceptanceCriteria, p.Invariants); err != nil {
			return nil, err
		}
		touchedDefinition = true
	}

	if touchedDefinition {
		if err := s.store.UpdateGoal(ctx, &updated); err != nil {
			return nil, goalWriteErr(err)
		}
	}
	if p.Status != nil {
		if err := s.store.UpdateGoalStatus(ctx, id, *p.Status); err != nil {
			return nil, goalWriteErr(err)
		}
	}
	return s.store.GetGoal(ctx, id)
}

// DeleteGoal returns store.ErrGoalNotFound when absent and *GoalWriteError
// for any other rejection.
func (s *LoopService) DeleteGoal(ctx context.Context, id string) error {
	if err := s.store.DeleteGoal(ctx, id); err != nil {
		return goalWriteErr(err)
	}
	return nil
}

// ListGoalEvidence returns store.ErrGoalNotFound when the goal is absent:
// the evidence list alone cannot tell "no evidence yet" from "no such goal".
func (s *LoopService) ListGoalEvidence(ctx context.Context, goalID string, filter store.GoalEvidenceFilter) ([]store.GoalEvidence, error) {
	if _, err := s.store.GetGoal(ctx, goalID); err != nil {
		return nil, err
	}
	return s.store.ListGoalEvidence(ctx, goalID, filter)
}

// ── loop runs ──

// GetLoopRun returns store.ErrLoopRunNotFound when absent.
func (s *LoopService) GetLoopRun(ctx context.Context, id string) (*store.LoopRun, error) {
	return s.store.GetLoopRun(ctx, id)
}

func (s *LoopService) ListLoopRuns(ctx context.Context, filter store.LoopRunFilter) ([]store.LoopRun, error) {
	return s.store.ListLoopRuns(ctx, filter)
}

// ListLoopRunIterations returns store.ErrLoopRunNotFound when the loop run is
// absent: the iteration list alone cannot tell "none yet" from "no such run".
func (s *LoopService) ListLoopRunIterations(ctx context.Context, loopRunID string) ([]store.LoopRunIteration, error) {
	if _, err := s.store.GetLoopRun(ctx, loopRunID); err != nil {
		return nil, err
	}
	return s.store.ListLoopRunIterations(ctx, loopRunID)
}

// setGoalLists encodes each non-nil list onto g, in a fixed order.
func setGoalLists(g *store.Goal, desired, constraints, acceptance, invariants *[]string) error {
	for _, f := range []struct {
		items *[]string
		set   func([]string) error
	}{
		{desired, g.SetDesiredState},
		{constraints, g.SetConstraints},
		{acceptance, g.SetAcceptanceCriteria},
		{invariants, g.SetInvariants},
	} {
		if f.items == nil {
			continue
		}
		if err := f.set(*f.items); err != nil {
			return &GoalWriteError{Err: err}
		}
	}
	return nil
}

// goalWriteErr passes store.ErrGoalNotFound through and wraps anything else.
func goalWriteErr(err error) error {
	if errors.Is(err, store.ErrGoalNotFound) {
		return err
	}
	return &GoalWriteError{Err: err}
}
