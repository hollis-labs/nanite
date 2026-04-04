package shell

import (
	"strings"
)

// defaultDenyPatterns are command prefixes/patterns that are blocked by default.
// These are destructive or dangerous commands that should never run without
// explicit opt-in via YOLO mode.
var defaultDenyPatterns = []string{
	"rm -rf /",
	"rm -rf /*",
	"rm -rf ~",
	"rm -rf $HOME",
	"mkfs",
	"dd if=",
	"dd of=/dev",
	"shutdown",
	"reboot",
	"halt",
	"poweroff",
	"init 0",
	"init 6",
	":(){ :|:& };:",     // fork bomb
	"chmod -R 777 /",
	"chown -R",
	"curl | sh",
	"curl | bash",
	"wget | sh",
	"wget | bash",
	"> /dev/sda",
	"> /dev/disk",
	"mv / ",
	"mv /* ",
}

// Denylist checks commands against a set of blocked patterns.
type Denylist struct {
	patterns []string
	enabled  bool
}

// NewDenylist creates a Denylist pre-populated with the default blocked patterns.
func NewDenylist() *Denylist {
	return &Denylist{
		patterns: append([]string{}, defaultDenyPatterns...),
		enabled:  true,
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
	lower := strings.ToLower(strings.TrimSpace(command))
	for _, pattern := range d.patterns {
		if strings.Contains(lower, strings.ToLower(pattern)) {
			return "blocked by denylist: matches pattern " + repr(pattern)
		}
	}
	return ""
}

func repr(s string) string {
	return "\"" + s + "\""
}
