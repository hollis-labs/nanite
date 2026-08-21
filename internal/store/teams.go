package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ErrTeamNotFound is returned when a teams row cannot be located.
var ErrTeamNotFound = errors.New("team not found")

// Team is one row in the teams table -- a saved, named, reusable
// organizational shape (Team Slots, authority, routing, phase/gate
// sequence). See docs/engineering/architecture/15-teams.md and
// GLOSSARY.md's Team / Team Slot / TeamRun entries.
//
// Storage only: this type and its CRUD below don't compile, launch, or
// enforce anything -- TASKS/teams/07-team-compiler.md,
// 08-team-run-launcher.md, and 04-team-authority-schema.md own that logic.
// A caller should be able to launch a TeamRun "against" a saved Team by
// name with invocation-time overrides (15-teams.md's "Runtime overrides
// follow the existing cascade") without creating a new persistent Team
// definition per call -- this table is what makes a Team callable by name
// in the first place.
type Team struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`

	// SlotsJSON is a JSON-encoded array of TeamSlotDefinition (below).
	// Use Team.Slots()/Team.SetSlots() to work with the decoded value
	// instead of handling the raw JSON at call sites.
	SlotsJSON string `json:"slots_json"`

	// AuthorityJSON / RoutingJSON are storage placeholders only -- this
	// task's own scope explicitly excludes finalizing their shape.
	// TASKS/teams/04-team-authority-schema.md owns the real
	// may_spawn/may_message/may_not_review authority-grant shape (and may
	// migrate this column into its own normalized table -- coordinate,
	// don't duplicate). TASKS/teams/09-team-routing.md owns the real
	// routing-rule shape. Plain JSON text, same "minimally-typed
	// placeholder" choice as PhasesJSON below -- see migration 128's doc
	// comment for why this is `string`, not `json.RawMessage`.
	AuthorityJSON string `json:"authority_json"`
	RoutingJSON   string `json:"routing_json"`

	// PhasesJSON is the phase/gate sequence (15-teams.md's "Illustrative
	// shape": scope_work (flex) -> review_gate (gate) -> address_feedback
	// (flex) -> merge_gate (gate)) that TASKS/teams/07-team-compiler.md
	// compiles into a WorkflowDefinition at TeamRun launch. Deliberately
	// its own column/JSON sub-structure, never commingled into
	// SlotsJSON, per 15-teams.md's "What this session did not decide"
	// forward-compat instruction: keeping the phase sequence addressable
	// on its own means a later Team/Workflow definition split is "accept
	// a phase sequence from a second source," not a rewrite of the
	// compiler. Not decoded into a Go type here -- task 07 defines and
	// consumes its real shape.
	PhasesJSON string `json:"phases_json"`

	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	CreatedBy string `json:"created_by"`
}

// TeamSlotDefinition is one entry decoded from Team.SlotsJSON -- an
// organizational role within a Team (e.g. "architect", "engineer",
// "reviewer"), modeled directly on 15-teams.md's SME illustrative example.
//
// Always spelled out as "Team Slot" in identifiers, table/column names,
// and docs -- never a bare Slot/slot type. internal/context/slot.go
// already defines an unrelated, already-load-bearing "slot" concept
// (SlotOrder, the Context Broker's prompt-assembly ordering, one of six
// invariants in internal/context/INVARIANTS.md and CLAUDE.md). A Team
// Slot is an organizational role, not a prompt-assembly position -- see
// GLOSSARY.md for the full disambiguation. "Slot" alone is ambiguous in
// this codebase going forward; always qualify it.
type TeamSlotDefinition struct {
	// Name is the Team-local Team Slot identifier (e.g. "architect",
	// "engineer") -- what routing/authority/messaging address by, not a
	// role slug on its own.
	Name string `json:"name"`

	// RoleSlug references roles.slug -- the persona/behavior template a
	// resolved member is built from (15-teams.md's `role:
	// architecture-sme` field).
	RoleSlug string `json:"role_slug"`

	// Resolution is "durable" (wakes an existing identity via
	// DurableAgentService -- the same primitive WorkflowLauncher/A2A
	// CancelTask already call) or "fresh" (spawns through the ordinary
	// Agent Construction cascade). 15-teams.md's `resolution:
	// durable|fresh`. This is NOT a literal read of
	// agent_profiles.durable -- that column is an unrelated
	// eject-survival flag (migration 073). See TASKS/teams/
	// 08-team-run-launcher.md's Context for the corrected meaning used
	// here: new Team-Slot-level launch config, not a re-read of an
	// existing agent_profiles column.
	Resolution string `json:"resolution"`

	// AgentID is the concrete agent_profiles.id to wake -- present only
	// for durable Team Slots (15-teams.md's `agent_id:
	// nanite-architect`). Nil for fresh Team Slots.
	AgentID *string `json:"agent_id,omitempty"`

	// ActivationMode mirrors agent_profiles.activation_mode's real three
	// values (singleton|fresh-per-wake|concurrent) -- 15-teams.md's
	// `activation_mode: singleton`. Referenced by a Team Slot, not
	// redefined by one.
	ActivationMode string `json:"activation_mode"`

	// Required, Min, Max gate whether this Team Slot's resolution is
	// mandatory at TeamRun launch, deferred until needed (e.g. an
	// architect SME, "normally dormant"), or elastic (min/max > 1, e.g.
	// engineer). 15-teams.md's "Slot resolution and the one genuinely
	// new persistence table" section.
	Required bool `json:"required"`
	Min      int  `json:"min"`
	Max      int  `json:"max"`
}

// validTeamSlotResolutions / validTeamSlotActivationModes are the enum
// values validateTeamSlots checks against -- mirrors the Go-layer
// validation approach agents.go's validateAgentMultiAgentFields already
// uses for activation_mode (no DB-level CHECK on a JSON blob column is
// possible here).
var (
	validTeamSlotResolutions     = map[string]bool{"": true, "durable": true, "fresh": true}
	validTeamSlotActivationModes = map[string]bool{"": true, "singleton": true, "fresh-per-wake": true, "concurrent": true}
)

// validateTeamSlots checks each TeamSlotDefinition's Resolution/
// ActivationMode enum fields and Min/Max/Required consistency. An empty
// slice is a legitimate, if unusual, starting shape for a Team definition
// still being authored -- not rejected here.
func validateTeamSlots(slots []TeamSlotDefinition) error {
	for _, slot := range slots {
		if slot.Name == "" {
			return fmt.Errorf("team slot: name is required")
		}
		if !validTeamSlotResolutions[slot.Resolution] {
			return fmt.Errorf("team slot %q: resolution %q invalid: must be 'durable' or 'fresh'", slot.Name, slot.Resolution)
		}
		if !validTeamSlotActivationModes[slot.ActivationMode] {
			return fmt.Errorf("team slot %q: activation_mode %q invalid: must be 'singleton', 'fresh-per-wake', or 'concurrent'", slot.Name, slot.ActivationMode)
		}
		if slot.Max > 0 && slot.Min > slot.Max {
			return fmt.Errorf("team slot %q: min %d exceeds max %d", slot.Name, slot.Min, slot.Max)
		}
	}
	return nil
}

// Slots decodes Team.SlotsJSON into []TeamSlotDefinition. Returns (nil,
// nil) for an empty/unset SlotsJSON.
func (t *Team) Slots() ([]TeamSlotDefinition, error) {
	if t.SlotsJSON == "" {
		return nil, nil
	}
	var out []TeamSlotDefinition
	if err := json.Unmarshal([]byte(t.SlotsJSON), &out); err != nil {
		return nil, fmt.Errorf("decode team slots: %w", err)
	}
	return out, nil
}

// SetSlots validates and encodes slots into Team.SlotsJSON. A nil slice
// encodes to "[]", matching the column's own DEFAULT.
func (t *Team) SetSlots(slots []TeamSlotDefinition) error {
	if err := validateTeamSlots(slots); err != nil {
		return err
	}
	if slots == nil {
		slots = []TeamSlotDefinition{}
	}
	b, err := json.Marshal(slots)
	if err != nil {
		return fmt.Errorf("encode team slots: %w", err)
	}
	t.SlotsJSON = string(b)
	return nil
}

// TeamPhase is one entry decoded from Team.PhasesJSON -- one step in a
// Team's phase/gate sequence (15-teams.md's "Illustrative shape":
// scope_work (flex) -> review_gate (gate) -> address_feedback (flex) ->
// merge_gate (gate)). TASKS/teams/07-team-compiler.md's CompileTeam
// (internal/service/team_compiler.go) is this type's one real consumer: it
// walks a []TeamPhase in slice order and emits one matching
// agentworkflow.StepDefinition per phase.
//
// A phase sequence is a v1-linear chain: CompileTeam derives each compiled
// step's DependsOn from slice order (the previous phase's ID) rather than
// from an authored field here -- 15-teams.md's SME example never shows a
// non-linear phase sequence, and the task that defined this type found no
// concrete reason (yet) to support one. There is deliberately no
// DependsOn/depends_on field on this type for that reason; if a later Team
// definition needs a non-linear sequence, that's a real, documented
// addition to this type, not an assumption baked in silently here.
//
// Deliberately its own decoded type/column, never folded into
// TeamSlotDefinition/SlotsJSON -- see PhasesJSON's own doc comment above
// for the forward-compat reasoning this type completes: a later Team/
// Workflow definition split becomes "accept a phase sequence from a second
// source," not a rewrite of the compiler.
type TeamPhase struct {
	// ID is this phase's step identifier -- becomes the compiled
	// StepDefinition.ID verbatim (15-teams.md's `scope_work`,
	// `review_gate`, `address_feedback`, `merge_gate`).
	ID string `json:"id"`

	// Kind is "flex" or "gate" -- maps 1:1 to agentworkflow.StepKindFlex /
	// StepKindGate. No other kind is a legal phase: a Team's phase
	// sequence never authors an llm/tool step directly (those happen
	// inside a flex phase's self-organizing member turns, not as a phase
	// of their own).
	Kind string `json:"kind"`

	// ActiveSlots names the Team Slots active during this flex phase.
	// Required (non-empty) when Kind == "flex"; ignored when Kind ==
	// "gate". Compiles to the flex StepDefinition's Config["active_slots"]
	// verbatim -- Team Slot *names*, not resolved (agent_id, session_id)
	// tuples. See CompileTeam's own doc comment for why names (not
	// resolved tuples) is this task's documented design call.
	ActiveSlots []string `json:"active_slots,omitempty"`

	// ExitTrigger is this flex phase's exit condition, in the exact
	// shorthand shape internal/service/workflow_engine_flex.go's
	// parseFlexStepConfig (task 06) accepts: {"self_tool": "<name>"},
	// {"event": "<name>"}, or {"kind": "...", "spec": {...}}, plus an
	// optional "authorized_slot" key. Required (non-nil) when Kind ==
	// "flex"; ignored when Kind == "gate". Compiles to the flex
	// StepDefinition's Config["exit_trigger"] verbatim -- reusing the
	// reflex trigger-spec vocabulary, per 15-teams.md's own instruction
	// not to invent a second condition language.
	ExitTrigger map[string]any `json:"exit_trigger,omitempty"`

	// ApproverSlot is this gate phase's approving identity -- a Team Slot
	// name (e.g. "reviewer") or a human sentinel (15-teams.md's `operator`
	// -- "human, not a Team Slot"). Required (non-empty) when Kind ==
	// "gate"; ignored when Kind == "flex". Compiles to the gate
	// StepDefinition's Config["approver_slot"]. As of task 06/07,
	// StepKindGate's real engine handling (runStep's gate branch,
	// internal/service/workflow_engine.go) reads no Config key at all --
	// approver_slot is carried here as forward-compatible metadata for a
	// not-yet-built gate-approval-enforcement mechanism, not a claim that
	// anything currently reads or enforces it. See CompileTeam's own doc
	// comment for the full citation trail confirming this.
	ApproverSlot string `json:"approver_slot,omitempty"`
}

// validTeamPhaseKinds is the enum validateTeamPhases checks Kind against --
// same Go-layer-validation-over-a-JSON-blob-column approach
// validTeamSlotResolutions/validTeamSlotActivationModes already use above.
var validTeamPhaseKinds = map[string]bool{"flex": true, "gate": true}

// validateTeamPhases checks each TeamPhase's Kind enum and the
// Kind-specific fields CompileTeam requires (ActiveSlots/ExitTrigger for
// flex, ApproverSlot for gate), plus non-empty/unique IDs. An empty slice
// is a legitimate starting shape (mirrors validateTeamSlots). Deliberately
// does NOT validate ExitTrigger's inner shape (self_tool/event/kind+spec)
// -- that's internal/service/workflow_engine_flex.go's parseFlexStepConfig
// job (a different package; this one can't import it, and duplicating its
// validation here would be a second copy of the same rule to keep in
// sync). This function is not wired into CreateTeam/UpdateTeam -- see
// SetPhases' own doc comment for why.
func validateTeamPhases(phases []TeamPhase) error {
	seen := make(map[string]bool, len(phases))
	for _, p := range phases {
		if p.ID == "" {
			return fmt.Errorf("team phase: id is required")
		}
		if seen[p.ID] {
			return fmt.Errorf("team phase %q: duplicate id", p.ID)
		}
		seen[p.ID] = true
		if !validTeamPhaseKinds[p.Kind] {
			return fmt.Errorf("team phase %q: kind %q invalid: must be \"flex\" or \"gate\"", p.ID, p.Kind)
		}
		switch p.Kind {
		case "flex":
			if len(p.ActiveSlots) == 0 {
				return fmt.Errorf("team phase %q: flex phase requires a non-empty active_slots", p.ID)
			}
			if p.ExitTrigger == nil {
				return fmt.Errorf("team phase %q: flex phase requires exit_trigger", p.ID)
			}
		case "gate":
			if p.ApproverSlot == "" {
				return fmt.Errorf("team phase %q: gate phase requires approver_slot", p.ID)
			}
		}
	}
	return nil
}

// Phases decodes Team.PhasesJSON into []TeamPhase. Returns (nil, nil) for
// an empty/unset PhasesJSON. Mirrors Slots() exactly: decode-only, no
// validation (SetPhases is where validation happens, matching
// SetSlots/validateTeamSlots's own split).
func (t *Team) Phases() ([]TeamPhase, error) {
	if t.PhasesJSON == "" {
		return nil, nil
	}
	var out []TeamPhase
	if err := json.Unmarshal([]byte(t.PhasesJSON), &out); err != nil {
		return nil, fmt.Errorf("decode team phases: %w", err)
	}
	return out, nil
}

// SetPhases validates and encodes phases into Team.PhasesJSON. A nil slice
// encodes to "[]", matching the column's own DEFAULT and SetSlots' own
// convention.
//
// Deliberately NOT called from CreateTeam/UpdateTeam (unlike SetSlots,
// which validateTeamSlots is wired into both of): task 01's own
// TestTeam_RoundTrip fixture (internal/store/teams_test.go) already saved
// a PhasesJSON string authored before this type existed
// (`{"id":"scope_work","kind":"flex"}`, no active_slots/exit_trigger --
// task 01 deliberately left PhasesJSON undecoded and unvalidated,
// TASKS/teams/01-team-definition-schema.md). Wiring validateTeamPhases
// into CreateTeam/UpdateTeam would retroactively reject that
// already-reviewed-clean, merged fixture and break a passing test outside
// this task's own scope. SetPhases itself still validates for any new
// caller (e.g. a future Team-authoring API, TASKS/teams/
// 10-team-crud-api.md) that chooses to use it.
func (t *Team) SetPhases(phases []TeamPhase) error {
	if err := validateTeamPhases(phases); err != nil {
		return err
	}
	if phases == nil {
		phases = []TeamPhase{}
	}
	b, err := json.Marshal(phases)
	if err != nil {
		return fmt.Errorf("encode team phases: %w", err)
	}
	t.PhasesJSON = string(b)
	return nil
}

const teamColumns = `id, name, COALESCE(description,''), slots_json, authority_json,
       routing_json, phases_json, created_at, updated_at, COALESCE(created_by,'')`

func scanTeam(scanner interface{ Scan(...any) error }, t *Team) error {
	return scanner.Scan(
		&t.ID, &t.Name, &t.Description, &t.SlotsJSON, &t.AuthorityJSON,
		&t.RoutingJSON, &t.PhasesJSON, &t.CreatedAt, &t.UpdatedAt, &t.CreatedBy,
	)
}

// CreateTeam inserts a new teams row. Generates an ID via uuid.New() if
// t.ID is empty, and defaults every JSON sub-structure column to "[]" if
// left empty (matching the column DEFAULTs migration 128 sets, replicated
// here because every value is passed explicitly in this INSERT so
// SQLite's column DEFAULT never actually applies -- same trade-off
// agent_schedules.go's InsertAgentSchedule documents for its own JSON/
// policy defaults). SlotsJSON is validated via validateTeamSlots before
// insert if non-empty/non-"[]".
func (s *Store) CreateTeam(ctx context.Context, t *Team) error {
	if t.Name == "" {
		return fmt.Errorf("create team: name is required")
	}
	if t.ID == "" {
		t.ID = uuid.New().String()
	}
	if t.SlotsJSON == "" {
		t.SlotsJSON = "[]"
	}
	if t.AuthorityJSON == "" {
		t.AuthorityJSON = "[]"
	}
	if t.RoutingJSON == "" {
		t.RoutingJSON = "[]"
	}
	if t.PhasesJSON == "" {
		t.PhasesJSON = "[]"
	}
	if slots, err := t.Slots(); err != nil {
		return fmt.Errorf("create team: %w", err)
	} else if err := validateTeamSlots(slots); err != nil {
		return fmt.Errorf("create team: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO teams
		    (id, name, description, slots_json, authority_json, routing_json,
		     phases_json, created_at, updated_at, created_by)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Name, nullIfEmpty(t.Description), t.SlotsJSON, t.AuthorityJSON,
		t.RoutingJSON, t.PhasesJSON, now, now, nullIfEmpty(t.CreatedBy),
	)
	if err != nil {
		return fmt.Errorf("create team: %w", err)
	}
	t.CreatedAt = now
	t.UpdatedAt = now
	return nil
}

// GetTeam returns a team by id, or ErrTeamNotFound.
func (s *Store) GetTeam(ctx context.Context, id string) (*Team, error) {
	var t Team
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+teamColumns+` FROM teams WHERE id = ?`, id,
	)
	if err := scanTeam(row, &t); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrTeamNotFound
		}
		return nil, fmt.Errorf("get team: %w", err)
	}
	return &t, nil
}

// GetTeamByName returns a team by its unique name, or ErrTeamNotFound.
// This is what lets a caller (Loom, or any Nanite consumer) launch
// against a saved Team by name (15-teams.md's "Runtime overrides follow
// the existing cascade").
func (s *Store) GetTeamByName(ctx context.Context, name string) (*Team, error) {
	var t Team
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+teamColumns+` FROM teams WHERE name = ?`, name,
	)
	if err := scanTeam(row, &t); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrTeamNotFound
		}
		return nil, fmt.Errorf("get team by name: %w", err)
	}
	return &t, nil
}

// ListTeams returns every team, ordered by name.
func (s *Store) ListTeams(ctx context.Context) ([]Team, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+teamColumns+` FROM teams ORDER BY name`,
	)
	if err != nil {
		return nil, fmt.Errorf("list teams: %w", err)
	}
	defer rows.Close()
	out := make([]Team, 0)
	for rows.Next() {
		var t Team
		if err := scanTeam(rows, &t); err != nil {
			return nil, fmt.Errorf("scan team: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpdateTeam updates every mutable column (name, description, and all
// four JSON sub-structure columns) by id, bumping updated_at. CreatedAt/
// CreatedBy are immutable after insert. Returns ErrTeamNotFound if no row
// matched. SlotsJSON is validated via validateTeamSlots before update if
// non-empty/non-"[]".
func (s *Store) UpdateTeam(ctx context.Context, t *Team) error {
	if t.ID == "" {
		return fmt.Errorf("update team: id is required")
	}
	if t.Name == "" {
		return fmt.Errorf("update team: name is required")
	}
	if t.SlotsJSON == "" {
		t.SlotsJSON = "[]"
	}
	if t.AuthorityJSON == "" {
		t.AuthorityJSON = "[]"
	}
	if t.RoutingJSON == "" {
		t.RoutingJSON = "[]"
	}
	if t.PhasesJSON == "" {
		t.PhasesJSON = "[]"
	}
	if slots, err := t.Slots(); err != nil {
		return fmt.Errorf("update team: %w", err)
	} else if err := validateTeamSlots(slots); err != nil {
		return fmt.Errorf("update team: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.ExecContext(ctx,
		`UPDATE teams
		    SET name = ?, description = ?, slots_json = ?, authority_json = ?,
		        routing_json = ?, phases_json = ?, updated_at = ?
		  WHERE id = ?`,
		t.Name, nullIfEmpty(t.Description), t.SlotsJSON, t.AuthorityJSON,
		t.RoutingJSON, t.PhasesJSON, now, t.ID,
	)
	if err != nil {
		return fmt.Errorf("update team: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update team rows affected: %w", err)
	}
	if n == 0 {
		return ErrTeamNotFound
	}
	t.UpdatedAt = now
	return nil
}

// DeleteTeam removes a team by id. Returns ErrTeamNotFound if no row
// matched. Storage-only: this does not check for or cascade into any
// TeamRun (workflow_runs) that was launched against this Team --
// TASKS/teams/08-team-run-launcher.md's job, not this task's.
func (s *Store) DeleteTeam(ctx context.Context, id string) error {
	res, err := s.DB.ExecContext(ctx,
		`DELETE FROM teams WHERE id = ?`, id,
	)
	if err != nil {
		return fmt.Errorf("delete team: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete team rows affected: %w", err)
	}
	if n == 0 {
		return ErrTeamNotFound
	}
	return nil
}
