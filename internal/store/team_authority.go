package store

// TASKS/teams/04-team-authority-schema.md -- Go-side read/write support for
// the team_authority_grants table migration 132_team_authority_grants.sql
// adds. See docs/engineering/architecture/15-teams.md's "Authority:
// generalize, don't invent" section for the design this table encodes:
// "who may [verb] whom" between two Team Slots within one saved Team.
//
// This file's real load-bearing piece is AuthorizedForVerb -- the actual
// enforcement primitive TASKS/teams/08-team-run-launcher.md (may_spawn) and
// TASKS/teams/09-team-routing.md (may_message/may_not_review) call at their
// real dispatch/message call sites. Modeled deliberately on
// dispatch.TrustResolver.ResolveTrust's "resolve, then a hard `if`-gate at
// the real call site" shape (internal/dispatch/trust.go,
// internal/subagent/service.go:785-787's `if tier == TrustUntrusted {
// return "", dispatch.ErrUntrustedRole }`) -- NOT
// internal/service/tool.go's parseParentDispatchAllowlist shape (resolve,
// then only render into an LLM-facing tool description, never enforced).
// This task file's own Context section verified that distinction directly
// against the code: agent_parent_dispatch_allowlist (migration 060) has no
// enforcement call site anywhere. AuthorizedForVerb must not repeat that
// pattern.
//
// This file is storage-only, per the task's own "This task does not wire
// enforcement into real call sites" instruction -- no call site in
// internal/dispatch, internal/selftools, or internal/service is modified
// here. AuthorizedForVerb is a pure, directly-testable check function;
// tasks 08/09 are the first real callers.

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// team_authority_grants.verb vocabulary -- migration 132's CHECK
// constraint, taken verbatim. Deliberately not widened beyond the three
// verbs 15-teams.md's own illustrative example names -- see migration
// 132's doc comment for the full reasoning (no real consumer in this batch
// needs a fourth verb yet; widening the CHECK later is a one-line
// migration when one does, per this task's own "CHECK-constraint
// widening, not a new column/migration per verb" instruction).
const (
	TeamAuthorityVerbMaySpawn     = "may_spawn"
	TeamAuthorityVerbMayMessage   = "may_message"
	TeamAuthorityVerbMayNotReview = "may_not_review"
)

// TeamAuthoritySelfSlot is the to_slot sentinel value 15-teams.md's
// `reviewer.may_not_review: self` example uses -- "excludes the granting
// slot itself as a valid target," not a literal Team Slot named "self"
// (this task file's own "What to do" section states this explicitly).
// AuthorizedForVerb resolves it by comparing fromSlot == toSlot at query
// time, not by storing a real slot name.
const TeamAuthoritySelfSlot = "self"

// ErrTeamAuthorityGrantNotFound is returned when a team_authority_grants
// row cannot be located by id (DeleteTeamAuthorityGrant's WHERE id=?
// matches nothing).
var ErrTeamAuthorityGrantNotFound = errors.New("team authority grant not found")

// TeamAuthorityGrant is one row in the team_authority_grants table -- a
// single "fromSlot may verb toSlot" grant within one saved Team. Mirrors
// the table's columns 1:1.
type TeamAuthorityGrant struct {
	ID        string `json:"id"`
	TeamID    string `json:"team_id"`
	FromSlot  string `json:"from_slot"`
	Verb      string `json:"verb"`
	ToSlot    string `json:"to_slot"`
	CreatedAt string `json:"created_at"` // TEXT column, sqlite datetime('now') format
}

const teamAuthorityGrantColumns = `id, team_id, from_slot, verb, to_slot, created_at`

func scanTeamAuthorityGrant(scanner interface{ Scan(...any) error }, g *TeamAuthorityGrant) error {
	return scanner.Scan(&g.ID, &g.TeamID, &g.FromSlot, &g.Verb, &g.ToSlot, &g.CreatedAt)
}

// validTeamAuthorityVerbs is the Go-side mirror of migration 132's CHECK
// constraint -- checked before insert so a caller gets a typed Go error
// instead of a raw sqlite CHECK-constraint-violation error for an invalid
// verb, matching team_run_members.go's UpdateTeamRunMemberStatus
// precedent for its own status vocabulary.
var validTeamAuthorityVerbs = map[string]bool{
	TeamAuthorityVerbMaySpawn:     true,
	TeamAuthorityVerbMayMessage:   true,
	TeamAuthorityVerbMayNotReview: true,
}

// CreateTeamAuthorityGrant inserts a new team_authority_grants row. g.ID is
// generated via uuid.New().String() when the caller leaves it empty,
// matching this package's insert-time-ID-generation convention (agents.go's
// CreateAgent, team_run_members.go's InsertTeamRunMember). team_id, from_slot,
// to_slot, and verb are all required; verb is validated against migration
// 132's CHECK vocabulary before the write.
func (s *Store) CreateTeamAuthorityGrant(ctx context.Context, g TeamAuthorityGrant) (*TeamAuthorityGrant, error) {
	if g.TeamID == "" {
		return nil, fmt.Errorf("create team authority grant: team_id is required")
	}
	if g.FromSlot == "" {
		return nil, fmt.Errorf("create team authority grant: from_slot is required")
	}
	if g.ToSlot == "" {
		return nil, fmt.Errorf("create team authority grant: to_slot is required")
	}
	if !validTeamAuthorityVerbs[g.Verb] {
		return nil, fmt.Errorf("create team authority grant: invalid verb %q", g.Verb)
	}
	if g.ID == "" {
		g.ID = uuid.New().String()
	}

	if g.CreatedAt == "" {
		// Let the column default (datetime('now')) populate created_at,
		// then read it back so the returned struct reflects the row that
		// actually landed rather than a Go-side approximation of "now" --
		// same pattern team_run_members.go's InsertTeamRunMember uses for
		// resolved_at.
		_, err := s.DB.ExecContext(ctx,
			`INSERT INTO team_authority_grants (id, team_id, from_slot, verb, to_slot)
			 VALUES (?, ?, ?, ?, ?)`,
			g.ID, g.TeamID, g.FromSlot, g.Verb, g.ToSlot,
		)
		if err != nil {
			return nil, fmt.Errorf("create team authority grant: %w", err)
		}
		row := s.DB.QueryRowContext(ctx,
			`SELECT `+teamAuthorityGrantColumns+` FROM team_authority_grants WHERE id = ?`, g.ID,
		)
		if err := scanTeamAuthorityGrant(row, &g); err != nil {
			return nil, fmt.Errorf("read back inserted team authority grant: %w", err)
		}
		return &g, nil
	}

	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO team_authority_grants (id, team_id, from_slot, verb, to_slot, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		g.ID, g.TeamID, g.FromSlot, g.Verb, g.ToSlot, g.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create team authority grant: %w", err)
	}
	return &g, nil
}

// ListTeamAuthorityGrants returns every team_authority_grants row for
// teamID, ordered by from_slot, verb, to_slot for a stable result order
// across calls. Returns an empty (non-nil) slice, not an error, for a team
// with no grants -- an ungoverned Team is a legitimate, gate-free shape
// (15-teams.md: "A Team is not required to declare any gates at all -- a
// fully fluid team is a legitimate, gate-free shape" -- the same reasoning
// applies to authority: no grants at all is a valid starting shape, not an
// error condition).
func (s *Store) ListTeamAuthorityGrants(ctx context.Context, teamID string) ([]TeamAuthorityGrant, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+teamAuthorityGrantColumns+` FROM team_authority_grants
		  WHERE team_id = ?
		  ORDER BY from_slot, verb, to_slot`,
		teamID,
	)
	if err != nil {
		return nil, fmt.Errorf("list team authority grants: %w", err)
	}
	defer closeRows(rows)

	out := make([]TeamAuthorityGrant, 0)
	for rows.Next() {
		var g TeamAuthorityGrant
		if err := scanTeamAuthorityGrant(rows, &g); err != nil {
			return nil, fmt.Errorf("scan team authority grant: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// DeleteTeamAuthorityGrant removes a team_authority_grants row by id.
// Returns ErrTeamAuthorityGrantNotFound if no row matched.
func (s *Store) DeleteTeamAuthorityGrant(ctx context.Context, id string) error {
	res, err := s.DB.ExecContext(ctx,
		`DELETE FROM team_authority_grants WHERE id = ?`, id,
	)
	if err != nil {
		return fmt.Errorf("delete team authority grant: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete team authority grant rows affected: %w", err)
	}
	if n == 0 {
		return ErrTeamAuthorityGrantNotFound
	}
	return nil
}

// AuthorizedForVerb reports whether a team_authority_grants row exists
// matching (teamID, fromSlot, verb, toSlot) -- the real enforcement
// primitive tasks 08/09 call at their actual dispatch/message call sites.
//
// This is a pure grant-existence lookup, not a full per-verb semantic
// authorization decision -- what a caller does with the returned bool
// depends on the verb's own semantics, which this function does not
// interpret:
//   - For an allow-listing verb (may_spawn, may_message): true means "this
//     grant exists, proceed is allowed." Absence (false) means refuse --
//     the same "fail closed on no matching row" shape dispatch.TrustResolver
//     uses when an agent_profiles row is missing (defaults to TrustNormal,
//     never TrustTrusted).
//   - For a deny-listing verb (may_not_review): true means "this exclusion
//     grant matches this (fromSlot, toSlot) pair" -- the caller (task 09)
//     is responsible for inverting that into a block, the same way a
//     firewall deny-rule match and a firewall allow-rule match are both
//     "rule matched" at the lookup layer, with the calling policy deciding
//     what "matched" means for that rule's kind. This task's own scope is
//     explicitly the storage and lookup primitive, not that policy --
//     15-teams.md's own text only commits to "may_not_review: self" as an
//     illustrative example, not the full may_not_review call-site
//     semantics, which is deferred to task 09.
//
// to_slot = 'self' resolution: a stored grant row with to_slot ==
// TeamAuthoritySelfSlot ("self") matches when the queried toSlot argument
// equals fromSlot (self-targeting) -- 15-teams.md's `reviewer.may_not_
// review: self` example, "a slot excluding itself as a valid target, not
// addressing another slot literally named 'self'" (this task file's own
// "What to do" section, verbatim). A to_slot='self' row does NOT match a
// query where toSlot != fromSlot.
//
// Fails closed, not open, in every one of these cases -- verified by this
// package's regression tests, not just assumed:
//   - Unknown teamID: the WHERE clause matches zero rows -> false, nil.
//   - Unknown/unresolvable fromSlot or toSlot (any string not present in
//     any stored grant row for this team): zero rows match -> false, nil.
//   - No grant row at all for this (teamID, fromSlot, verb) triple: zero
//     rows -> false, nil.
//   - Empty teamID/fromSlot/verb/toSlot argument: rejected before the query
//     even runs -> false, nil. An empty string can never accidentally
//     satisfy a real grant row, since every column is NOT NULL and every
//     CreateTeamAuthorityGrant call already rejects empty inputs -- this is
//     defense in depth, not a case that can occur via this package's own
//     writer, but a caller passing an empty string by a bug in its own
//     resolution logic must still get false, never an accidental match.
//   - A real DB error: false is returned alongside the wrapped error --
//     callers must check the error, but the bool itself is never true on a
//     failure path. This mirrors dispatch.TrustResolver's own "fail closed
//     on resolve error" call-site pattern (internal/subagent/service.go:
//     778, "Fail closed: treat resolve error as normal (require
//     approval)") -- the equivalent here is "treat resolve error as not
//     granted."
//
// There is deliberately no "default true" branch anywhere in this
// function. This codebase has a documented, real history of exactly this
// class of bug (TASKS/reflex-taxonomy/08-fix-resolve-fail-open-visibility.md,
// a fail-open regression in Resolve()'s combining-algorithm lookup found by
// a fresh reviewer) -- see TestAuthorizedForVerb_FailClosed_* in
// team_authority_test.go for the explicit regression coverage this
// function's own task file requires, not just incidental pass-through
// coverage from the happy-path tests.
func (s *Store) AuthorizedForVerb(ctx context.Context, teamID, fromSlot, verb, toSlot string) (bool, error) {
	if teamID == "" || fromSlot == "" || verb == "" || toSlot == "" {
		return false, nil
	}

	rows, err := s.DB.QueryContext(ctx,
		`SELECT to_slot FROM team_authority_grants
		  WHERE team_id = ? AND from_slot = ? AND verb = ?`,
		teamID, fromSlot, verb,
	)
	if err != nil {
		return false, fmt.Errorf("authorized for verb: %w", err)
	}
	defer closeRows(rows)

	for rows.Next() {
		var grantToSlot string
		if err := rows.Scan(&grantToSlot); err != nil {
			return false, fmt.Errorf("authorized for verb: scan: %w", err)
		}
		if grantToSlot == toSlot {
			return true, nil
		}
		if grantToSlot == TeamAuthoritySelfSlot && fromSlot == toSlot {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("authorized for verb: %w", err)
	}
	return false, nil
}
