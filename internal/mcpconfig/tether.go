package mcpconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
)

// TetherMessagingServerName is the registry name of the Tether messaging
// gateway. Tool names the gateway publishes (tether_message_send, ...) are
// uniform across servers, so an agent's tool grants name the tools, not this
// server.
const TetherMessagingServerName = "tether"

// TetherMessagingScope is the one Tether MCP scope the messaging gateway asks
// for: send, consume and cancel. Reads need no scope.
const TetherMessagingScope = "message.write"

// TetherMessagingTools is the messaging tool set an agent needs to take part
// in Tether mail: write (send, reply), read (inbox, list, get) and settle
// (mark_read). Nothing here is granted automatically. A grant is an operator
// act through the native authority (docs/architecture/native-authority.md);
// registering the gateway only makes the tools discoverable.
var TetherMessagingTools = []string{
	"tether_message_send",
	"tether_message_reply",
	"tether_message_inbox",
	"tether_message_list",
	"tether_message_get",
	"tether_message_mark_read",
}

// TetherMessagingConfig renders the .mcp.json the messaging gateway imports
// from. The credential is a token FILE path, never an inline secret: the file
// holds the bearer token issued for the agent's stable actor, so the address a
// message is sent from is the address the #532 bridge delivers to. tokenFile
// must be absolute.
//
// The entry runs `tether mcp --no-discover --token-file <path>` with only the
// message.write scope. It does not use --proxy, so no upstream Torque or
// Tesseract server is reachable through it.
func TetherMessagingConfig(tokenFile string) ([]byte, error) {
	if tokenFile == "" {
		return nil, errors.New("tether messaging config: token file is required")
	}
	if !filepath.IsAbs(tokenFile) {
		return nil, fmt.Errorf("tether messaging config: token file %q must be an absolute path", tokenFile)
	}
	cfg := ClaudeCodeConfig{MCPServers: map[string]ServerEntry{
		TetherMessagingServerName: {
			Command: "tether",
			Args:    []string{"mcp", "--no-discover", "--token-file", tokenFile},
			Env:     map[string]string{"TETHER_MCP_SCOPES": TetherMessagingScope},
		},
	}}
	return json.MarshalIndent(cfg, "", "  ")
}
