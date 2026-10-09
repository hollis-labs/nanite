package plugin

import (
	"fmt"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
)

// ValidatePluginID applies the shared identity rule at filesystem boundaries.
// Nanite additionally limits directory names to 63 bytes.
func ValidatePluginID(id string) error {
	if len(id) > 63 || !manifest.ValidID(id) {
		return fmt.Errorf("plugin: invalid plugin id %q (shared identity, at most 63 bytes)", id)
	}
	return nil
}
