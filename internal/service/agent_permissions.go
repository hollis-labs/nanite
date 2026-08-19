package service

import (
	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/toolclient"
)

// newFileAgentPermissionResolver used to answer permission lookups for
// file-based agent IDs ("file-<slug>") by matching against an in-memory
// Definition slice, so a file-discovered agent (no agent_profiles row) could
// still get a real toolclient.ToolPermissions decision instead of always
// falling through to a store lookup that was guaranteed to miss.
//
// TASKS/adhoc/01-eliminate-file-based-agent-runtime.md eliminated the file-
// based agent runtime this resolver existed for: agent.IsFileBasedID/
// SlugFromFileID/the "file-<slug>" synthetic ID no longer exist anywhere,
// and every agent (including the 9 internal builtin profiles) is a real
// agent_profiles row with a real ID by the time any permission check runs.
// There is nothing left for this function to resolve, so it is now a
// permanent no-op -- kept in place (not deleted, not wired into
// container.go's cfg.ToolClient.PermissionResolver) per that task's own
// instruction, since its full removal is tangled up with
// tool_permissions/PermissionResolver/CheckPermission itself, which is
// TASKS/adhoc/02-remove-tool-permissions-collapse-to-agent-tools.md's job,
// not this one's.
func newFileAgentPermissionResolver(_ []*agent.Definition) toolclient.PermissionResolver {
	return nil
}
