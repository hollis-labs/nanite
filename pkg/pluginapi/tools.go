package pluginapi

import (
	"fmt"
	"regexp"

	"github.com/hollis-labs/plugin-sdk/manifest"
)

// Tool effects are reviewed declarations, not runtime guarantees or grants.
// Agent roster membership, load preferences and execution permission still
// apply. Hosts must never infer these effects from the tool's name.
const (
	ToolEffectRead        = "read"
	ToolEffectWrite       = "write"
	ToolEffectDestructive = "destructive"
	MaxAgentTools         = 128
)

var agentToolName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// ToolEffectHints converts the host's closed effect vocabulary to behavior
// hints consumed by its permission engine. Unknown effects fail closed.
func ToolEffectHints(effect string) (readOnly, destructive bool, err error) {
	switch effect {
	case ToolEffectRead:
		return true, false, nil
	case ToolEffectWrite:
		return false, false, nil
	case ToolEffectDestructive:
		return false, true, nil
	default:
		return false, false, fmt.Errorf("nanite: unknown tool effect %q", effect)
	}
}

// ValidateAgentTools adds Nanite's tool limits and effect vocabulary to the
// SDK's common manifest validation. The common manifest must also be validated;
// schema structure and duplicate declaration checks belong to plugin-sdk.
func ValidateAgentTools(tools []manifest.Tool) error {
	if len(tools) > MaxAgentTools {
		return fmt.Errorf("nanite: too many agent tools (maximum %d)", MaxAgentTools)
	}
	for _, tool := range tools {
		if !agentToolName.MatchString(tool.Name) {
			return fmt.Errorf("nanite: invalid agent tool name %q", tool.Name)
		}
		if len(tool.Description) > 32*1024 || len(tool.InputSchema) > 256*1024 {
			return fmt.Errorf("nanite: agent tool %q exceeds metadata limits", tool.Name)
		}
		if _, _, err := ToolEffectHints(tool.Effect); err != nil {
			return fmt.Errorf("agent tool %q: %w", tool.Name, err)
		}
	}
	return nil
}
