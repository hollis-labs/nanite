package sandbox

import (
	"strings"
)

// GO-SEC4-006 (docs/audits/2026-08-21-go-quality/REPORT.md §8.12; see also
// TASKS/audit-remediation/02-linux-sandbox-fail-open/01-sandbox-fail-closed-without-bwrap.md):
// this denylist is DEFENSE-IN-DEPTH, not a hard security boundary. It is a
// fixed set of literal-substring patterns matched case-insensitively after
// trimming — trivially defeated by whitespace variation ("rm  -rf /"),
// flag reordering/long-form flags ("rm -fr /", "rm --recursive --force /"),
// variable/command substitution, or any indirection that keeps the exact
// literal substring from ever appearing verbatim in the string this
// function is handed. See denylist_test.go's
// TestCheckDenylist_KnownBypassClasses for a locked-in, executable record
// of exactly which bypass shapes are (deliberately, currently) uncaught —
// a regression there means either the matching got stronger (update the
// test) or someone re-narrowed the accepted-gap list without noticing.
//
// The REAL security boundary for agent-controlled execution is OS-level
// isolation (bwrap on linux, seatbelt on darwin — see os_linux.go /
// os_darwin.go and AD-01 in TASKS/audit-remediation/
// ARCHITECT-DECISIONS.md). This denylist becomes the ONLY remaining
// control precisely when that boundary is missing AND an operator has
// explicitly accepted degraded execution via
// NANITE_ALLOW_UNSANDBOXED_AGENT_EXEC=1 (degraded.go) — a deliberate,
// visible, operator-initiated tradeoff, not something this file's own
// matching logic should be relied on to compensate for by itself.
//
// Direction chosen for GO-SEC4-006 (cheaper of the two options the task
// file named — reframe as advisory here in comments/docs, vs. replacing
// substring matching with a real shell tokenizer): tokenization would
// meaningfully narrow the bypass surface but pulls in a new external
// dependency (e.g. mvdan.cc/sh) for a control that, even fully hardened,
// still would not be the primary boundary. Revisit if AD-01's opt-in is
// ever observed being used routinely rather than as a rare, deliberate
// escape hatch — at that point this denylist stops being pure
// defense-in-depth and the tokenization option should be reconsidered.

// defaultDenyPatterns are command prefixes/patterns that are always blocked,
// even in YOLO mode. These are destructive or dangerous commands that no
// automation should execute regardless of the approval mode.
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
// Returns (true, reason) if blocked, (false, "") if allowed. Advisory /
// defense-in-depth only — see this file's package-level doc comment
// (GO-SEC4-006) for what it does and does not catch.
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
