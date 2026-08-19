package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ErrKnownToolNotFound is returned when a known_tools row cannot be located.
var ErrKnownToolNotFound = errors.New("known tool not found")

// KnownTool is one row in the known_tools table -- Phase 1 item 04's global
// tool catalog (TASKS/phase-1/04-add-known-tools-and-agent-tools-fk.md).
//
// Not the same table as AgentKnownTool (agent_known_tools, migration 069):
// that is a live, per-agent roster/pinning table with its own REST CRUD and
// GUI. KnownTool is a global catalog -- one row per tool that exists in the
// system at all, live-synced against builtins + current MCP discovery by
// SyncKnownTools (internal/service/known_tools_sync.go). See this migration's
// own doc comment (116_known_tools_and_agent_tools.sql) for the full
// naming-collision analysis.
type KnownTool struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Source      string `json:"source"` // "builtin" | "mcp" | "plugin"
	Status      string `json:"status"` // "available" | "unavailable" -- never deleted on disconnect
	Description string `json:"description"`
	// ConcurrencySafe is a placeholder for Phase 3 item 06
	// (TASKS/phase-3/06-tool-concurrency-safety-classification.md).
	// nil means "not yet classified" -- this package neither sets nor
	// reads it beyond plain storage.
	ConcurrencySafe *bool `json:"concurrency_safe,omitempty"`
	// AlwaysIncluded marks the tool-discovery escape hatch tools
	// (request_tools/tool_list/tool_describe) that must survive on every
	// agent regardless of explicit grants, per
	// architecture/01-agent-construction.md.
	AlwaysIncluded bool   `json:"always_included"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

const knownToolColumns = `id, name, source, status, description, concurrency_safe, always_included, created_at, updated_at`

func scanKnownTool(scanner interface{ Scan(...any) error }, t *KnownTool) error {
	return scanner.Scan(
		&t.ID, &t.Name, &t.Source, &t.Status, &t.Description,
		&t.ConcurrencySafe, &t.AlwaysIncluded, &t.CreatedAt, &t.UpdatedAt,
	)
}

// ListKnownTools returns every known_tools row, ordered by name.
func (s *Store) ListKnownTools(ctx context.Context) ([]KnownTool, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+knownToolColumns+` FROM known_tools ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list known_tools: %w", err)
	}
	defer rows.Close()

	out := make([]KnownTool, 0)
	for rows.Next() {
		var t KnownTool
		if err := scanKnownTool(rows, &t); err != nil {
			return nil, fmt.Errorf("scan known_tools: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ListAvailableKnownTools returns every known_tools row with status =
// 'available', ordered by name.
func (s *Store) ListAvailableKnownTools(ctx context.Context) ([]KnownTool, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+knownToolColumns+` FROM known_tools WHERE status = 'available' ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list available known_tools: %w", err)
	}
	defer rows.Close()

	out := make([]KnownTool, 0)
	for rows.Next() {
		var t KnownTool
		if err := scanKnownTool(rows, &t); err != nil {
			return nil, fmt.Errorf("scan known_tools: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ListAlwaysIncludedKnownTools returns every known_tools row flagged
// always_included=true -- the tool-discovery escape hatch baseline every
// agent gets regardless of explicit agent_tools grants.
func (s *Store) ListAlwaysIncludedKnownTools(ctx context.Context) ([]KnownTool, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+knownToolColumns+` FROM known_tools WHERE always_included = TRUE ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list always-included known_tools: %w", err)
	}
	defer rows.Close()

	out := make([]KnownTool, 0)
	for rows.Next() {
		var t KnownTool
		if err := scanKnownTool(rows, &t); err != nil {
			return nil, fmt.Errorf("scan known_tools: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetKnownToolByName returns a known_tools row by name, or
// ErrKnownToolNotFound if no such row exists.
func (s *Store) GetKnownToolByName(ctx context.Context, name string) (*KnownTool, error) {
	var t KnownTool
	row := s.DB.QueryRowContext(ctx, `SELECT `+knownToolColumns+` FROM known_tools WHERE name = ?`, name)
	if err := scanKnownTool(row, &t); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrKnownToolNotFound
		}
		return nil, fmt.Errorf("get known_tools by name %s: %w", name, err)
	}
	return &t, nil
}

// UpsertKnownTool inserts a known_tools row keyed by name, or updates
// source/status/description/updated_at on an existing row. Deliberately
// does NOT touch always_included or concurrency_safe on conflict -- both
// are operator/Phase-3-classification-owned fields that a routine catalog
// re-sync must not silently reset. Returns the row's ID (existing or
// newly minted).
func (s *Store) UpsertKnownTool(ctx context.Context, name, source, status, description string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("upsert known_tools: name is required")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	id := uuid.New().String()
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO known_tools (id, name, source, status, description, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET
		     source = excluded.source,
		     status = excluded.status,
		     description = excluded.description,
		     updated_at = excluded.updated_at`,
		id, name, source, status, description, now, now,
	)
	if err != nil {
		return "", fmt.Errorf("upsert known_tools %s: %w", name, err)
	}
	row, err := s.GetKnownToolByName(ctx, name)
	if err != nil {
		return "", fmt.Errorf("upsert known_tools %s: reload: %w", name, err)
	}
	return row.ID, nil
}

// MarkKnownToolsUnavailableExcept sets status='unavailable' on every
// known_tools row whose name is not in currentNames. Rows are never
// deleted -- per architecture/01-agent-construction.md ("status=unavailable,
// not deleted, when a server disconnects") -- so existing agent_tools /
// agent_dispatch_tool_allowlist grants referencing them stay intact and can
// re-activate automatically if the tool reappears. Returns the count of
// rows newly marked unavailable (rows already unavailable are not
// recounted).
func (s *Store) MarkKnownToolsUnavailableExcept(ctx context.Context, currentNames []string) (int, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT name FROM known_tools WHERE status = 'available'`)
	if err != nil {
		return 0, fmt.Errorf("mark known_tools unavailable: list available: %w", err)
	}
	present := make(map[string]bool, len(currentNames))
	for _, n := range currentNames {
		present[n] = true
	}
	var stale []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return 0, fmt.Errorf("mark known_tools unavailable: scan: %w", err)
		}
		if !present[name] {
			stale = append(stale, name)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("mark known_tools unavailable: %w", err)
	}
	rows.Close()

	now := time.Now().UTC().Format(time.RFC3339)
	for _, name := range stale {
		if _, err := s.DB.ExecContext(ctx,
			`UPDATE known_tools SET status = 'unavailable', updated_at = ? WHERE name = ?`,
			now, name,
		); err != nil {
			return 0, fmt.Errorf("mark known_tools unavailable %s: %w", name, err)
		}
	}
	return len(stale), nil
}
