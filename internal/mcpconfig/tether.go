package mcpconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hollis-labs/nanite/internal/store"
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

// CheckTetherTokenFile fails early, at registration, when the token file
// cannot be the credential `tether mcp` will read: missing, not a regular
// file, or readable by group or others. Without it the same mistakes surface
// only when a launched agent's gateway starts.
func CheckTetherTokenFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("tether messaging config: token file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("tether messaging config: token file %q is not a regular file", path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("tether messaging config: token file %q has mode %04o; it holds a credential, chmod 0600", path, info.Mode().Perm())
	}
	return nil
}

// CheckTetherSingleActor refuses a second actor's token on a host that already
// has the "tether" entry. The entry is host-wide: every agent listing it
// launches the gateway with this one credential, and the Import skips an
// existing name, so a second token would be silently ignored while its agent
// sent under the first actor's identity. Per-agent planting is the follow-up
// that lifts this; until then one actor per host is explicit. existing is the
// stored row, or nil.
func CheckTetherSingleActor(existing *store.MCPServerConfig, tokenFile string) error {
	if existing == nil {
		return nil
	}
	var args []string
	if err := json.Unmarshal([]byte(existing.Args), &args); err == nil {
		for i := 0; i+1 < len(args); i++ {
			if args[i] == "--token-file" && args[i+1] == tokenFile {
				return nil
			}
		}
	}
	return fmt.Errorf("tether messaging config: server %q is already registered with a different token file; "+
		"the entry is host-wide, so one Tether actor per host is supported (a second actor would send under the first's token). "+
		"Remove the existing entry to replace it", TetherMessagingServerName)
}
