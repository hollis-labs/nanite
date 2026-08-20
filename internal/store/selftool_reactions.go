package store

// TASKS/harness-reactive-self-tools/02-reactive-layer-schema.md — Go-side
// read/write support for the reactive-layer tables migration
// 126_selftool_reactions.sql adds: selftool_reaction_kinds (lookup) and
// selftool_reactions (per-tool config, many rows per tool). See
// docs/engineering/architecture/11-harness-reactive-self-tools.md
// ("Definition & reaction shape") for the design these tables encode.
//
// Reactive layer only — this file does not touch or migrate the existing
// self-tool catalog itself (the ~70+ tool definitions and their dispatch
// stay static Go in internal/selftools). ListEnabledSelftoolReactions is
// the read path TASKS/harness-reactive-self-tools/
// 03-reaction-engine-core.md's Fire() needs: every enabled reaction row
// for a given tool name. InsertSelftoolReaction is the write path
// 07-worked-example-task-update-report.md's worked example needs to seed
// its two reaction rows. No CRUD API surface (internal/api/...) is built
// here for either table — no operator-facing UI/endpoint is asked for by
// this task, matching how 01-taxonomy-schema-foundation.md scoped its own
// lookup-table helpers as read/insert-only.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ErrSelftoolReactionKindNotFound is returned when a
// selftool_reaction_kinds row cannot be located by slug.
var ErrSelftoolReactionKindNotFound = errors.New("selftool reaction kind not found")

// SelftoolReactionKind is one row in the selftool_reaction_kinds lookup
// table — Slug is the primary key (render_card, internal_api_call,
// external_api_call, callback), Category is "render" or "execute",
// Implemented reports whether 03-reaction-engine-core.md's Fire() actually
// executes this kind yet (external_api_call/callback are seeded-but-inert
// in this batch), and Description is the operator-facing summary from
// this task's own seed table.
type SelftoolReactionKind struct {
	Slug        string `json:"slug"`
	Category    string `json:"category"`
	Implemented bool   `json:"implemented"`
	Description string `json:"description"`
}

// GetSelftoolReactionKind returns the selftool_reaction_kinds row for
// slug, or ErrSelftoolReactionKindNotFound.
func (s *Store) GetSelftoolReactionKind(ctx context.Context, slug string) (*SelftoolReactionKind, error) {
	var out SelftoolReactionKind
	err := s.DB.QueryRowContext(ctx,
		`SELECT slug, category, implemented, description
		   FROM selftool_reaction_kinds
		  WHERE slug = ?`,
		slug,
	).Scan(&out.Slug, &out.Category, &out.Implemented, &out.Description)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrSelftoolReactionKindNotFound
		}
		return nil, fmt.Errorf("get selftool_reaction_kinds: %w", err)
	}
	return &out, nil
}

// SelftoolReaction is one row in the selftool_reactions config table —
// many rows per tool_name are expected. ReactionKindID stores the owning
// selftool_reaction_kinds row's slug (named ReactionKindID/reaction_kind_id
// to match the design doc's own naming, despite holding a slug value, not
// a surrogate integer id). Config is kind-specific JSON (envelope
// type/template for render_card; endpoint + body template for
// internal_api_call/external_api_call; target identifier for callback).
// created_at is a plain TEXT column (per this task's own DDL, not a
// DATETIME-affinity column like catalog_sources' — the sqlite driver only
// auto-scans into time.Time for DATETIME/TIMESTAMP-affinity columns), so
// InsertSelftoolReaction/ListEnabledSelftoolReactions format/parse it as
// RFC3339 explicitly rather than relying on driver auto-conversion.
type SelftoolReaction struct {
	ID             string    `json:"id"`
	ToolName       string    `json:"tool_name"`
	ReactionKindID string    `json:"reaction_kind_id"`
	Config         string    `json:"config"`
	Enabled        bool      `json:"enabled"`
	CreatedAt      time.Time `json:"created_at"`
}

// ListEnabledSelftoolReactions returns every enabled selftool_reactions
// row for toolName — the read path TASKS/harness-reactive-self-tools/
// 03-reaction-engine-core.md's Fire() needs to look up a tool's own
// configured reactions.
func (s *Store) ListEnabledSelftoolReactions(ctx context.Context, toolName string) ([]SelftoolReaction, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, tool_name, reaction_kind_id, config, enabled, created_at
		   FROM selftool_reactions
		  WHERE tool_name = ? AND enabled = 1
		  ORDER BY created_at`,
		toolName,
	)
	if err != nil {
		return nil, fmt.Errorf("list selftool_reactions: %w", err)
	}
	defer rows.Close()

	out := make([]SelftoolReaction, 0)
	for rows.Next() {
		var row SelftoolReaction
		var createdAt string
		if err := rows.Scan(&row.ID, &row.ToolName, &row.ReactionKindID, &row.Config, &row.Enabled, &createdAt); err != nil {
			return nil, fmt.Errorf("scan selftool_reactions: %w", err)
		}
		parsed, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse selftool_reactions.created_at %q: %w", createdAt, err)
		}
		row.CreatedAt = parsed
		out = append(out, row)
	}
	return out, rows.Err()
}

// CountSelftoolReactionsByToolAndKind returns the count of
// selftool_reactions rows for toolName + reactionKindID, regardless of
// enabled state — the idempotent-seed check TASKS/harness-reactive-
// self-tools/07-worked-example-task-update-report.md's
// SeedTaskUpdateReportReactions needs (mirroring
// internal/agent/reflexes/seeds.go's CountClassBaseReflexByName use in
// SeedBaseReflexes). Deliberately not scoped to enabled=1 only, unlike
// ListEnabledSelftoolReactions above — a seeder must not re-insert a row
// an operator has since disabled, so "does a row exist at all" (not
// "does an enabled row exist") is the correct idempotency check here.
func (s *Store) CountSelftoolReactionsByToolAndKind(ctx context.Context, toolName, reactionKindID string) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM selftool_reactions WHERE tool_name = ? AND reaction_kind_id = ?`,
		toolName, reactionKindID,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count selftool_reactions: %w", err)
	}
	return n, nil
}

// InsertSelftoolReaction inserts a new selftool_reactions row. r.ID is
// generated via uuid.New().String() when the caller leaves it empty,
// matching this codebase's own insert-time-ID-generation convention
// (internal/store/agents.go's InsertAgent, internal/store/artifacts.go's
// CreateArtifact, etc.). r.CreatedAt is stamped to the current time when
// the caller leaves it zero-valued.
func (s *Store) InsertSelftoolReaction(ctx context.Context, r SelftoolReaction) error {
	if r.ID == "" {
		r.ID = uuid.New().String()
	}
	if r.Config == "" {
		r.Config = "{}"
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO selftool_reactions (id, tool_name, reaction_kind_id, config, enabled, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		r.ID, r.ToolName, r.ReactionKindID, r.Config, r.Enabled, r.CreatedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("insert selftool_reaction: %w", err)
	}
	return nil
}
