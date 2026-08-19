package store

// Phase 2 item 02 (TASKS/phase-2/02-port-forward-dynamic-resolver.md) —
// store accessors for agent_context_resolvers, the DB-configurable home
// for the cmd/http dynamic-resolver capability architecture/
// 02-agent-launching.md names as a first-class mechanism "available to
// every agent, not gated behind a separate catalog system." See
// migrations/118_agent_context_resolvers.sql for the full design
// rationale and internal/runtime/agent/context_resolver.go for the
// launch-time resolution logic that consumes AgentContextResolver rows.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/oklog/ulid/v2"
)

// ErrAgentContextResolverNotFound is returned when an
// agent_context_resolvers row cannot be located.
var ErrAgentContextResolverNotFound = errors.New("agent context resolver not found")

// AgentContextResolver is one row in agent_context_resolvers -- a single
// cmd- or http-kind dynamic resolver bound to an agent. Its resolved
// output folds into that agent's assembled launch context under SlotName
// (see internal/runtime/agent/prompt.go's appendDynamicContext).
//
// Field shape mirrors the shared agentkit agentcontext.SlotSource
// sub-structs (CmdSource / HTTPTextSource / HTTPJSONSource) field for
// field so contextResolverToSlotSpec's conversion
// (internal/runtime/agent/context_resolver.go) is a straight mapping —
// see that file's doc comment for the exact table.
type AgentContextResolver struct {
	ID       string `json:"id"`
	AgentID  string `json:"agent_id"`
	SlotName string `json:"slot_name"`
	// Kind is "cmd" or "http" -- this task's scope is deliberately
	// narrower than the pre-port boot-profile catalog's four deferred
	// slot kinds (cmd/http/role_summary/skill_index); see
	// TASKS/phase-2/02-port-forward-dynamic-resolver.md's Work Log for
	// the role_summary/skill_index disposition.
	Kind string `json:"kind"`
	// Run is the shell command line ("cmd" kind only).
	Run string `json:"run"`
	// CWD overrides the resolver's working directory ("cmd" kind
	// only); empty defers to the launch workdir.
	CWD string `json:"cwd"`
	// Timeout is a Go duration string (e.g. "30s"); empty defers to
	// the shared resolver's own default (30s for cmd, 10s for http).
	Timeout string `json:"timeout"`
	// URL is the target to GET ("http" kind only).
	URL string `json:"url"`
	// HeadersJSON is a JSON object of extra request headers ("http"
	// kind only). Empty / "{}" means no extra headers.
	HeadersJSON string `json:"headers_json"`
	// ResponseFormat is "text" or "json" ("http" kind only) --
	// selects between the shared http_text and http_json resolvers.
	ResponseFormat string `json:"response_format"`
	// JSONPath is the shared resolver's tiny JSONPath subset,
	// consulted only when ResponseFormat == "json".
	JSONPath string `json:"json_path"`
	// Enabled lets an operator pause a resolver without deleting its
	// configuration. ListEnabledAgentContextResolvers only returns
	// enabled=1 rows.
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// validateAgentContextResolver enforces the shape every write path
// (Insert/Update) needs regardless of caller (store-internal callers and
// the REST API both route through this). Returns nil on success.
func validateAgentContextResolver(row AgentContextResolver) error {
	if row.AgentID == "" {
		return fmt.Errorf("agent_context_resolvers: agent_id is required")
	}
	if row.SlotName == "" {
		return fmt.Errorf("agent_context_resolvers: slot_name is required")
	}
	switch row.Kind {
	case "cmd":
		if row.Run == "" {
			return fmt.Errorf("agent_context_resolvers: kind %q requires run", row.Kind)
		}
	case "http":
		if row.URL == "" {
			return fmt.Errorf("agent_context_resolvers: kind %q requires url", row.Kind)
		}
		switch row.ResponseFormat {
		case "", "text", "json":
		default:
			return fmt.Errorf("agent_context_resolvers: response_format %q invalid: must be 'text' or 'json'", row.ResponseFormat)
		}
	default:
		return fmt.Errorf("agent_context_resolvers: kind %q invalid: must be 'cmd' or 'http'", row.Kind)
	}
	return nil
}

const agentContextResolverColumns = `id, agent_id, slot_name, kind, run, cwd, timeout, url,
       headers_json, response_format, json_path, enabled, created_at, updated_at`

func scanAgentContextResolver(scanner interface{ Scan(...any) error }, r *AgentContextResolver) error {
	return scanner.Scan(
		&r.ID, &r.AgentID, &r.SlotName, &r.Kind, &r.Run, &r.CWD, &r.Timeout, &r.URL,
		&r.HeadersJSON, &r.ResponseFormat, &r.JSONPath, &r.Enabled, &r.CreatedAt, &r.UpdatedAt,
	)
}

// InsertAgentContextResolver inserts a new agent_context_resolvers row.
// If row.ID is empty, a ULID is generated. HeadersJSON/ResponseFormat
// default to "{}" / "text" when empty. Fails on the table's
// UNIQUE(agent_id, slot_name) constraint if the agent already has a
// resolver bound to that slot.
func (s *Store) InsertAgentContextResolver(ctx context.Context, row AgentContextResolver) (string, error) {
	if row.HeadersJSON == "" {
		row.HeadersJSON = "{}"
	}
	if row.ResponseFormat == "" {
		row.ResponseFormat = "text"
	}
	if err := validateAgentContextResolver(row); err != nil {
		return "", err
	}
	if row.ID == "" {
		row.ID = "acr-" + ulid.Make().String()
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO agent_context_resolvers
		    (id, agent_id, slot_name, kind, run, cwd, timeout, url,
		     headers_json, response_format, json_path, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?,
		         ?, ?, ?, ?, datetime('now'), datetime('now'))`,
		row.ID, row.AgentID, row.SlotName, row.Kind, row.Run, row.CWD, row.Timeout, row.URL,
		row.HeadersJSON, row.ResponseFormat, row.JSONPath, row.Enabled,
	)
	if err != nil {
		return "", fmt.Errorf("insert agent_context_resolvers: %w", err)
	}
	return row.ID, nil
}

// GetAgentContextResolver returns the row by ID, or
// ErrAgentContextResolverNotFound.
func (s *Store) GetAgentContextResolver(ctx context.Context, id string) (*AgentContextResolver, error) {
	var out AgentContextResolver
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+agentContextResolverColumns+` FROM agent_context_resolvers WHERE id = ?`, id,
	)
	if err := scanAgentContextResolver(row, &out); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrAgentContextResolverNotFound
		}
		return nil, fmt.Errorf("get agent_context_resolvers: %w", err)
	}
	return &out, nil
}

// ListAgentContextResolvers returns every resolver row bound to agentID
// (enabled and disabled), ordered by slot_name. Used by the operator/API
// surface, distinct from ListEnabledAgentContextResolvers's boot-time
// read.
func (s *Store) ListAgentContextResolvers(ctx context.Context, agentID string) ([]AgentContextResolver, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+agentContextResolverColumns+`
		   FROM agent_context_resolvers
		  WHERE agent_id = ?
		  ORDER BY slot_name`,
		agentID,
	)
	if err != nil {
		return nil, fmt.Errorf("list agent_context_resolvers: %w", err)
	}
	defer rows.Close()
	out := make([]AgentContextResolver, 0)
	for rows.Next() {
		var r AgentContextResolver
		if err := scanAgentContextResolver(rows, &r); err != nil {
			return nil, fmt.Errorf("scan agent_context_resolvers: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListEnabledAgentContextResolvers returns only enabled=1 rows for
// agentID, ordered by slot_name for deterministic resolution order. This
// is the launch-time read path
// (internal/service/chat_boot_drive.go's resolveAgentContextForBoot).
func (s *Store) ListEnabledAgentContextResolvers(ctx context.Context, agentID string) ([]AgentContextResolver, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+agentContextResolverColumns+`
		   FROM agent_context_resolvers
		  WHERE agent_id = ? AND enabled = 1
		  ORDER BY slot_name`,
		agentID,
	)
	if err != nil {
		return nil, fmt.Errorf("list enabled agent_context_resolvers: %w", err)
	}
	defer rows.Close()
	out := make([]AgentContextResolver, 0)
	for rows.Next() {
		var r AgentContextResolver
		if err := scanAgentContextResolver(rows, &r); err != nil {
			return nil, fmt.Errorf("scan agent_context_resolvers: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpdateAgentContextResolver updates an existing row's editable fields.
// agent_id is immutable (delete + recreate to rebind a resolver to a
// different agent).
func (s *Store) UpdateAgentContextResolver(ctx context.Context, row AgentContextResolver) error {
	if row.ID == "" {
		return fmt.Errorf("update agent_context_resolvers: id is required")
	}
	if row.HeadersJSON == "" {
		row.HeadersJSON = "{}"
	}
	if row.ResponseFormat == "" {
		row.ResponseFormat = "text"
	}
	if err := validateAgentContextResolver(row); err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx,
		`UPDATE agent_context_resolvers
		    SET slot_name = ?, kind = ?, run = ?, cwd = ?, timeout = ?, url = ?,
		        headers_json = ?, response_format = ?, json_path = ?, enabled = ?,
		        updated_at = datetime('now')
		  WHERE id = ?`,
		row.SlotName, row.Kind, row.Run, row.CWD, row.Timeout, row.URL,
		row.HeadersJSON, row.ResponseFormat, row.JSONPath, row.Enabled, row.ID,
	)
	if err != nil {
		return fmt.Errorf("update agent_context_resolvers: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update agent_context_resolvers rows affected: %w", err)
	}
	if n == 0 {
		return ErrAgentContextResolverNotFound
	}
	return nil
}

// DeleteAgentContextResolver removes a row by id.
func (s *Store) DeleteAgentContextResolver(ctx context.Context, id string) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM agent_context_resolvers WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete agent_context_resolvers: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete agent_context_resolvers rows affected: %w", err)
	}
	if n == 0 {
		return ErrAgentContextResolverNotFound
	}
	return nil
}
