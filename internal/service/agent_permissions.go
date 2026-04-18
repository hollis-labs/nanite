package service

import (
	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/toolclient"
)

// newFileAgentPermissionResolver returns a toolclient.PermissionResolver that
// answers permission lookups for file-based agent IDs ("file-<slug>") by
// matching against in-memory Definition slice. Returns ok=false for any ID
// that is not file-based or whose slug is unknown — the caller will fall
// through to the store-backed lookup, preserving the WARN-on-miss signal for
// real DB-backed agents.
//
// File agents may set a `toolPermissions` frontmatter; when absent, the
// implicit allow_list derived from `tools:` is used (see Definition.ToProfile).
// An empty permissions block parses to default-permit with the standard
// MaxCallsPerTurn cap, matching the long-standing fallback behavior the
// pre-fix code provided via WARN-then-default.
func newFileAgentPermissionResolver(defs []*agent.Definition) toolclient.PermissionResolver {
	if len(defs) == 0 {
		return nil
	}
	bySlug := make(map[string]*agent.Definition, len(defs))
	for _, d := range defs {
		if d == nil || d.Slug == "" {
			continue
		}
		bySlug[d.Slug] = d
	}

	return func(agentID string) (toolclient.ToolPermissions, bool) {
		if !agent.IsFileBasedID(agentID) {
			return toolclient.ToolPermissions{}, false
		}
		d, ok := bySlug[agent.SlugFromFileID(agentID)]
		if !ok {
			return toolclient.ToolPermissions{}, false
		}
		return toolclient.ParsePermissions(d.ToProfile().ToolPermissions), true
	}
}
