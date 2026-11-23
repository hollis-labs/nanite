package agent

import (
	"github.com/hollis-labs/libs/agent/agentdir"
	rules "github.com/hollis-labs/libs/security/policy/permission"
)

// MCPServer is one MCP server registration, in .mcp.json's shape (stdio command, or http/sse url).
type MCPServer = agentdir.MCPServer

// PermissionRule is one permission rule, as in permissions.yaml: a tool name or glob, an
// optional pattern on the tool's first string argument, and allow, ask or deny.
type PermissionRule = rules.Rule
