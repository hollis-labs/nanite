package shell

import (
	"github.com/hollis-labs/nanite/internal/sandbox"
)

// Denylist checks commands against a set of blocked patterns.
// This is a thin wrapper around sandbox.CheckDenylist for backward compatibility.
type Denylist struct {
	enabled bool
}

// NewDenylist creates a Denylist that delegates to sandbox.CheckDenylist.
func NewDenylist() *Denylist {
	return &Denylist{
		enabled: true,
	}
}

// Enabled returns whether the denylist is currently active.
func (d *Denylist) Enabled() bool {
	return d.enabled
}

// SetEnabled toggles the denylist on or off.
func (d *Denylist) SetEnabled(on bool) {
	d.enabled = on
}

// Check returns a non-empty reason string if the command matches a blocked
// pattern. Returns "" if the command is allowed. Always returns "" when the
// denylist is disabled.
func (d *Denylist) Check(command string) string {
	if !d.enabled {
		return ""
	}
	if blocked, reason := sandbox.CheckDenylist(command); blocked {
		return reason
	}
	return ""
}
