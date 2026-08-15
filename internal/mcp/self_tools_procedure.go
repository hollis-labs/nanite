package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/hollis-labs/nanite/internal/store"
)

// callProcedureGet implements procedure_get — fetch a named procedure body
// seeded for the calling agent's own profile.
//
// Procedures are written by the ingest pipeline from an agent profile's
// `procedures:` frontmatter (service/ingest.go seedProcedures) into
// agent_procedures, keyed by (agent_id, name) where agent_id is the agent
// PROFILE's ID — the same ID space CallerProfileFromContext resolves, not
// a session or a specific run.
//
// CW-20260815-0014: project-manager.md and system-architect.md referenced
// this tool in their boot/checklist instructions before it existed
// anywhere in the codebase — agent_procedures had a write path (ingest +
// REST API for external management) but no read-back into a running
// agent's tool surface. This closes that gap.
func (st *SelfToolsTransport) callProcedureGet(ctx context.Context, args map[string]any) (*ToolResult, error) {
	if st.Store == nil {
		return errorResult("procedure_get: no store configured"), nil
	}

	name := strArg(args, "name", "")
	if name == "" {
		return errorResult("name is required"), nil
	}

	_, agentID := CallerProfileFromContext(ctx)
	if agentID == "" {
		return errorResult("procedure_get: no calling agent in context"), nil
	}

	proc, err := st.Store.GetAgentProcedure(ctx, agentID, name)
	if err != nil {
		if errors.Is(err, store.ErrAgentProcedureNotFound) {
			return errorResult(fmt.Sprintf("procedure %q not found for this agent", name)), nil
		}
		return errorResult(fmt.Sprintf("procedure_get: %v", err)), nil
	}

	return textResult(proc.Body), nil
}
