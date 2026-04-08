package sandbox

import (
	"strings"
)

// defaultDenyPatterns are command prefixes/patterns that are blocked by default.
// These are destructive or dangerous commands that should never run without
// explicit opt-in via YOLO mode.
var defaultDenyPatterns = []struct {
	pattern string
	reason  string
}{
	{"rm -rf /", "recursive delete of root filesystem"},
	{"rm -rf /*", "recursive delete of root filesystem"},
	{"rm -rf ~", "recursive delete of home directory"},
	{"rm -rf $HOME", "recursive delete of home directory"},
	{"mkfs", "filesystem format command"},
	{"dd if=", "raw disk write (dd)"},
	{"dd of=/dev", "raw disk write (dd)"},
	{"shutdown", "system shutdown"},
	{"reboot", "system reboot"},
	{"halt", "system halt"},
	{"poweroff", "system poweroff"},
	{"init 0", "system shutdown (init 0)"},
	{"init 6", "system reboot (init 6)"},
	{":(){ :|:& };:", "fork bomb"},
	{"chmod -R 777 /", "recursive permission change on root"},
	{"chown -R", "recursive ownership change"},
	{"curl | sh", "pipe remote script to shell"},
	{"curl | bash", "pipe remote script to shell"},
	{"wget | sh", "pipe remote script to shell"},
	{"wget | bash", "pipe remote script to shell"},
	{"> /dev/sda", "raw disk overwrite"},
	{"> /dev/disk", "raw disk overwrite"},
	{"mv / ", "move root filesystem"},
	{"mv /* ", "move root filesystem contents"},
}

// CheckDenylist checks a command against the set of blocked patterns.
// Returns (true, reason) if blocked, (false, "") if allowed.
func CheckDenylist(command string) (blocked bool, reason string) {
	lower := strings.ToLower(strings.TrimSpace(command))
	for _, p := range defaultDenyPatterns {
		if strings.Contains(lower, strings.ToLower(p.pattern)) {
			return true, "blocked by denylist: " + p.reason + " (matches " + repr(p.pattern) + ")"
		}
	}
	return false, ""
}

func repr(s string) string {
	return "\"" + s + "\""
}
