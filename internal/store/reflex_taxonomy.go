package store

// TASKS/reflex-taxonomy/01-taxonomy-schema-foundation.md — Go-side read
// support for the taxonomy lookup tables migration
// 124_reflex_action_taxonomy.sql adds:
// reflex_action_categories/reflex_action_kinds/reflex_provenance_tiers. See
// docs/engineering/architecture/10-reflex-action-taxonomy.md ("Facet 1"
// through "Facet 4") for the design these tables encode.
//
// Seed-only lookup data as of this task — no CRUD surface is built here on
// purpose (no operator-editable API exists yet and none was asked for by
// this task). GetReflexActionKind is the minimum read primitive
// TASKS/reflex-taxonomy/02-recurrence-cascade.md (recurrence resolution)
// and 03-shared-decision-engine.md (combining-algorithm resolution) both
// need to consume a reflex's category/combining_algorithm/
// default_recurrence_seconds by action_kind name.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrReflexActionKindNotFound is returned when a reflex_action_kinds row
// cannot be located by name.
var ErrReflexActionKindNotFound = errors.New("reflex action kind not found")

// ReflexActionKind is one row in the reflex_action_kinds lookup table —
// the per-action-kind facets from docs/engineering/architecture/
// 10-reflex-action-taxonomy.md: Category (Facet 1, system_message vs
// execute_action), CombiningAlgorithm (Facet 2, deny_overrides /
// first_applicable / all_applicable), and DefaultRecurrenceSeconds (Facet
// 4, nil = inherit the system default defined as a Go constant elsewhere,
// not a DB row; 0 is a real "no cooldown" override, distinct from nil).
type ReflexActionKind struct {
	Name                     string `json:"name"`
	Category                 string `json:"category"`
	CombiningAlgorithm       string `json:"combining_algorithm"`
	DefaultRecurrenceSeconds *int64 `json:"default_recurrence_seconds"`
}

// GetReflexActionKind returns the reflex_action_kinds row for name (one of
// the store.ReflexAction* constants), or ErrReflexActionKindNotFound.
func (s *Store) GetReflexActionKind(ctx context.Context, name string) (*ReflexActionKind, error) {
	var (
		out               ReflexActionKind
		defaultRecurrence sql.NullInt64
	)
	err := s.DB.QueryRowContext(ctx,
		`SELECT name, category, combining_algorithm, default_recurrence_seconds
		   FROM reflex_action_kinds
		  WHERE name = ?`,
		name,
	).Scan(&out.Name, &out.Category, &out.CombiningAlgorithm, &defaultRecurrence)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrReflexActionKindNotFound
		}
		return nil, fmt.Errorf("get reflex_action_kinds: %w", err)
	}
	if defaultRecurrence.Valid {
		v := defaultRecurrence.Int64
		out.DefaultRecurrenceSeconds = &v
	}
	return &out, nil
}

// ActionKindAllowsProvenanceTier reports whether tierName may declare an
// agent_reflexes row of the given action kind — TASKS/reflex-taxonomy/
// 05-provenance-tier-enforcement.md's per-kind declare allow-list (Facet
// 3), backed by migration 125_reflex_action_kind_provenance_allow.sql's
// reflex_action_kind_provenance_allow table. Presence of a (kindName,
// tierName) row means "allowed"; a miss means "denied" — this is a plain
// existence check, not a soft default, so an unrecognized kindName or
// tierName also returns false rather than erroring (the caller's own
// action_kind/status validation is responsible for rejecting an unknown
// kind name before this check is reached).
func (s *Store) ActionKindAllowsProvenanceTier(ctx context.Context, kindName, tierName string) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM reflex_action_kind_provenance_allow
		  WHERE kind_name = ? AND tier_name = ?`,
		kindName, tierName,
	).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("check reflex_action_kind_provenance_allow: %w", err)
	}
	return n > 0, nil
}

// ListReflexActionKinds returns every reflex_action_kinds row, ordered by
// name. TASKS/reflex-taxonomy/02-recurrence-cascade.md's consumer: loaded
// once (not per-reflex-per-turn) into internal/agent/reflexes.Engine's
// in-memory action-kind cache, so the recurrence cascade's kind-level tier
// (Facet 4) can be resolved without a query per candidate reflex.
func (s *Store) ListReflexActionKinds(ctx context.Context) ([]ReflexActionKind, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT name, category, combining_algorithm, default_recurrence_seconds
		   FROM reflex_action_kinds
		  ORDER BY name`,
	)
	if err != nil {
		return nil, fmt.Errorf("list reflex_action_kinds: %w", err)
	}
	defer rows.Close()
	out := make([]ReflexActionKind, 0)
	for rows.Next() {
		var (
			row               ReflexActionKind
			defaultRecurrence sql.NullInt64
		)
		if err := rows.Scan(&row.Name, &row.Category, &row.CombiningAlgorithm, &defaultRecurrence); err != nil {
			return nil, fmt.Errorf("scan reflex_action_kinds: %w", err)
		}
		if defaultRecurrence.Valid {
			v := defaultRecurrence.Int64
			row.DefaultRecurrenceSeconds = &v
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
