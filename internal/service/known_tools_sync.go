package service

// Phase 1 item 04 (TASKS/phase-1/04-add-known-tools-and-agent-tools-fk.md):
// SyncKnownTools live-syncs the known_tools global catalog against the
// currently-registered tool universe (builtins + current MCP discovery) at
// container startup. Mirrors how agent_known_tools.tool_name values are
// populated today, but against a global catalog instead of a per-agent
// roster -- see internal/store/known_tools.go's doc comment for the
// known_tools-vs-agent_known_tools distinction.

import (
	"context"
	"log/slog"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/store"
)

// KnownToolsSyncResult reports what SyncKnownTools did, for startup logging.
type KnownToolsSyncResult struct {
	Upserted          int // rows inserted or refreshed (source/status/description)
	MarkedUnavailable int // previously-available rows no longer in the live catalog
}

// SyncKnownTools upserts one known_tools row per tool in catalog (keyed by
// name), then marks any known_tools row NOT in catalog as
// status='unavailable' -- never deleted, per architecture/
// 01-agent-construction.md ("status=unavailable, not deleted, when a
// server disconnects"). isBuiltin classifies each tool's source; nil-safe
// (every tool is classified "mcp" when isBuiltin is nil).
//
// Called once at container startup, after the ToolClient's builtin
// registration + MCP AutoDiscover have both already run (mirroring the
// existing knownTools map[string]bool construction in container.go used by
// AutoIngestAgents' unknown-tool-reference check).
func SyncKnownTools(ctx context.Context, st *store.Store, catalog []llmtypes.ToolDefinition, isBuiltin func(name string) bool) KnownToolsSyncResult {
	var result KnownToolsSyncResult
	names := make([]string, 0, len(catalog))
	for _, t := range catalog {
		if t.Name == "" {
			continue
		}
		names = append(names, t.Name)
		source := "mcp"
		if isBuiltin != nil && isBuiltin(t.Name) {
			source = "builtin"
		}
		if _, err := st.UpsertKnownTool(ctx, t.Name, source, "available", t.Description); err != nil {
			slog.Warn("service: sync known_tools upsert", "tool", t.Name, "err", err)
			continue
		}
		result.Upserted++
	}

	marked, err := st.MarkKnownToolsUnavailableExcept(ctx, names)
	if err != nil {
		slog.Warn("service: sync known_tools mark-unavailable", "err", err)
	} else {
		result.MarkedUnavailable = marked
	}
	return result
}
