package plugin

import (
	"errors"
	"fmt"
)

// ValidatePluginID guards every filesystem boundary keyed by a plugin ID.
// The allowlist matches the v1 install contract:
// ^[a-z][a-z0-9-]{1,62}$ (2-63 bytes total).
//
// This function lives in the parent plugin package so both management sinks
// and the install subpackage can consume one canonical rule without an import
// cycle.
func ValidatePluginID(id string) error {
	if id == "" {
		return errors.New("plugin: empty plugin id")
	}
	if len(id) < 2 || len(id) > 63 {
		return fmt.Errorf("plugin: plugin id %q length %d out of range [2,63]", id, len(id))
	}
	first := rune(id[0])
	if first < 'a' || first > 'z' {
		return fmt.Errorf("plugin: plugin id %q must start with lowercase letter", id)
	}
	for _, r := range id[1:] {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-':
		default:
			return fmt.Errorf("plugin: invalid plugin id %q", id)
		}
	}
	return nil
}
