package service

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/hollis-labs/nanite/internal/store"
)

// TeamStore is the store surface TeamService uses.
type TeamStore interface {
	ListTeams(ctx context.Context) ([]store.Team, error)
	GetTeam(ctx context.Context, id string) (*store.Team, error)
	CreateTeam(ctx context.Context, t *store.Team) error
	UpdateTeam(ctx context.Context, t *store.Team) error
	DeleteTeam(ctx context.Context, id string) error
}

// TeamService owns team definitions and the rule for what a valid one is
// (validateTeamDefinition). Store errors come back unwrapped.
type TeamService struct {
	store TeamStore
}

func NewTeamService(st TeamStore) *TeamService { return &TeamService{store: st} }

// TeamValidationError lists every reason a team definition was rejected.
type TeamValidationError struct {
	Errs []string
}

func (e *TeamValidationError) Error() string {
	if len(e.Errs) == 0 {
		return "invalid team definition"
	}
	return e.Errs[0]
}

// TeamWriteError wraps a store rejection of a team write, as distinct from
// a missing team or a failure to read one back. Its message is the store's.
type TeamWriteError struct {
	Err error
}

func (e *TeamWriteError) Error() string { return e.Err.Error() }
func (e *TeamWriteError) Unwrap() error { return e.Err }

// TeamPatch is an update to a team. A nil field keeps the stored value.
type TeamPatch struct {
	Name          *string
	Description   *string
	SlotsJSON     *string
	AuthorityJSON *string
	RoutingJSON   *string
	PhasesJSON    *string
}

// List returns every team, ordered by name.
func (s *TeamService) List(ctx context.Context) ([]store.Team, error) {
	return s.store.ListTeams(ctx)
}

// Get returns a team, or store.ErrTeamNotFound.
func (s *TeamService) Get(ctx context.Context, id string) (*store.Team, error) {
	return s.store.GetTeam(ctx, id)
}

// Create validates team (canonicalizing its JSON columns) and inserts it.
// An invalid definition is a *TeamValidationError; a rejected insert is a
// *TeamWriteError.
func (s *TeamService) Create(ctx context.Context, team *store.Team) error {
	if errs := validateTeamDefinition(team); len(errs) > 0 {
		return &TeamValidationError{Errs: errs}
	}
	if err := s.store.CreateTeam(ctx, team); err != nil {
		return &TeamWriteError{Err: err}
	}
	return nil
}

// Update applies patch to the stored team current (copy then overwrite),
// validates the result, writes it and returns the team as stored. An invalid
// definition is a *TeamValidationError and nothing is written; a team
// deleted in between is store.ErrTeamNotFound; any other rejected write is a
// *TeamWriteError; a failure to read the team back is returned as is.
func (s *TeamService) Update(ctx context.Context, current *store.Team, patch TeamPatch) (*store.Team, error) {
	updated := *current
	if patch.Name != nil {
		updated.Name = *patch.Name
	}
	if patch.Description != nil {
		updated.Description = *patch.Description
	}
	if patch.SlotsJSON != nil {
		updated.SlotsJSON = *patch.SlotsJSON
	}
	if patch.AuthorityJSON != nil {
		updated.AuthorityJSON = *patch.AuthorityJSON
	}
	if patch.RoutingJSON != nil {
		updated.RoutingJSON = *patch.RoutingJSON
	}
	if patch.PhasesJSON != nil {
		updated.PhasesJSON = *patch.PhasesJSON
	}

	if errs := validateTeamDefinition(&updated); len(errs) > 0 {
		return nil, &TeamValidationError{Errs: errs}
	}
	if err := s.store.UpdateTeam(ctx, &updated); err != nil {
		if errors.Is(err, store.ErrTeamNotFound) {
			return nil, err
		}
		return nil, &TeamWriteError{Err: err}
	}
	return s.store.GetTeam(ctx, current.ID)
}

// Delete removes a team. It does not touch team runs launched from it.
func (s *TeamService) Delete(ctx context.Context, id string) error {
	return s.store.DeleteTeam(ctx, id)
}

// validateTeamDefinition mirrors internal/api/reflexes.go's
// validateReflexDefinition: collect every problem with team's current field
// values into a []string (rather than stopping at the first), so a caller
// gets one clear 400 response listing everything wrong, not a
// fix-one-resubmit-find-the-next loop.
//
// Per this task's own instruction, each of the three free-form JSON
// sub-structure columns gets exactly the validation its current typed
// coverage in internal/store/teams.go supports as of this task's dispatch
// -- documented explicitly here so a later task tightening one doesn't have
// to rediscover which is which:
//
//   - slots_json -- FULL typed validation. task 01's TeamSlotDefinition is
//     fully defined; team.SetSlots (which this function calls) runs
//     validateTeamSlots, which is the actual enforcement point for this
//     task's step 3 requirement -- Resolution/ActivationMode are checked
//     against their real enum values ("durable"|"fresh" and
//     "singleton"|"fresh-per-wake"|"concurrent") here, at the write
//     boundary, not left to fail silently at launch (task 08).
//   - phases_json -- FULL typed validation. task 07's TeamPhase is fully
//     defined; team.SetPhases (called here) runs validateTeamPhases. Note
//     SetPhases' own doc comment: it is deliberately NOT wired into
//     store.CreateTeam/UpdateTeam themselves (an already-merged pre-typed
//     test fixture would otherwise retroactively fail), so this API layer
//     calling it explicitly is what actually gives phases_json write-time
//     validation at all -- exactly the "future Team-authoring API" caller
//     that doc comment names by this task's own file path.
//   - routing_json -- FULL typed validation. task 09's TeamRouting/
//     TeamRoutingRule are fully defined; team.SetRouting (called here) runs
//     validateTeamRouting. Same "not wired into CreateTeam/UpdateTeam,
//     validates via this explicit call instead" shape as phases_json above,
//     per SetRouting's own doc comment. "" and the column's own "[]"
//     DEFAULT are both treated as "no routing configured yet" (matching
//     Team.Routing()'s identical special-case for those two literal
//     values) and are not run through TeamRouting's struct decode, which
//     would otherwise fail on an array literal.
//   - authority_json -- JSON-SHAPE-ONLY validation, not full typed
//     validation. Confirmed directly against internal/store/teams.go and
//     internal/store/team_authority.go before writing this: task 04 built
//     the real, enforced authority-grant shape as its own normalized table
//     (team_authority_grants, internal/store/team_authority.go's
//     TeamAuthorityGrant/AuthorizedForVerb) rather than a typed shape for
//     this column -- Team.AuthorityJSON itself remains exactly what task
//     01's own doc comment called it, "a storage placeholder", with no
//     TeamAuthority Go type anywhere in this codebase to validate against.
//     Per this task's own explicit instruction for exactly this
//     no-typed-shape case, this validates only that a non-empty value is
//     well-formed JSON of the column's own expected top-level shape -- an
//     array, matching store.CreateTeam/UpdateTeam's own "[]" DEFAULT for
//     this column. A later task giving this column a real typed shape (or
//     retiring it now that team_authority_grants exists) can tighten this
//     without rediscovering the gap.
func validateTeamDefinition(team *store.Team) []string {
	var errs []string
	if team.Name == "" {
		errs = append(errs, "name is required")
	}

	if team.SlotsJSON != "" {
		var slots []store.TeamSlotDefinition
		if err := json.Unmarshal([]byte(team.SlotsJSON), &slots); err != nil {
			errs = append(errs, "slots_json: invalid JSON: "+err.Error())
		} else if err := team.SetSlots(slots); err != nil {
			// SetSlots' own validateTeamSlots call is the real Resolution/
			// ActivationMode enum + Min/Max-consistency enforcement point.
			errs = append(errs, "slots_json: "+err.Error())
		}
	}

	if team.RoutingJSON != "" && team.RoutingJSON != "[]" {
		var routing store.TeamRouting
		if err := json.Unmarshal([]byte(team.RoutingJSON), &routing); err != nil {
			errs = append(errs, "routing_json: invalid JSON: "+err.Error())
		} else if err := team.SetRouting(routing); err != nil {
			errs = append(errs, "routing_json: "+err.Error())
		}
	}

	if team.PhasesJSON != "" {
		var phases []store.TeamPhase
		if err := json.Unmarshal([]byte(team.PhasesJSON), &phases); err != nil {
			errs = append(errs, "phases_json: invalid JSON: "+err.Error())
		} else if err := team.SetPhases(phases); err != nil {
			errs = append(errs, "phases_json: "+err.Error())
		}
	}

	if team.AuthorityJSON != "" {
		var arr []json.RawMessage
		if err := json.Unmarshal([]byte(team.AuthorityJSON), &arr); err != nil {
			errs = append(errs, "authority_json: must be a well-formed JSON array: "+err.Error())
		}
	}

	return errs
}
