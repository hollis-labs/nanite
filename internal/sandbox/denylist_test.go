package sandbox

import "testing"

// TestCheckDenylist_KnownBypassClasses is GO-SEC4-006's regression test
// (see denylist.go's package doc comment): a locked-in, executable record
// of the bypass classes this literal-substring denylist is KNOWN not to
// catch, confirming the "advisory / defense-in-depth, not a hard security
// boundary" framing is accurate — not just asserted in a comment nobody
// re-checks.
//
// If any of these start passing (blocked == true), that's real, welcome
// progress — update this test to move that case into
// TestCheckDenylist_StillCatchesLiteralMatches (or wherever) and adjust
// denylist.go's doc comment to match, rather than silently deleting the
// case here. If a NEW pattern should join this list, add it explicitly —
// don't let the "advisory" framing quietly widen without a visible diff.
func TestCheckDenylist_KnownBypassClasses(t *testing.T) {
	bypassed := []struct {
		name    string
		command string
	}{
		{
			name:    "whitespace variation (double space)",
			command: "rm  -rf /",
		},
		{
			name:    "whitespace variation (tab)",
			command: "rm\t-rf /",
		},
		{
			name:    "flag reordering (short flags swapped)",
			command: "rm -fr /",
		},
		{
			name:    "long-form flags instead of -rf",
			command: "rm --recursive --force /",
		},
		{
			name:    "variable indirection (no literal substring present)",
			command: `X=rm; Y="-rf /"; $X $Y`,
		},
		{
			name:    "base64-encoded payload",
			command: "echo cm0gLXJmIC8=|base64 -d|sh",
		},
	}
	for _, tc := range bypassed {
		t.Run(tc.name, func(t *testing.T) {
			if blocked, reason := CheckDenylist(tc.command); blocked {
				t.Errorf("CheckDenylist(%q) = blocked (%s), want NOT blocked — this bypass class is documented as accepted-uncaught in denylist.go; if it's now caught, update both this test and that comment", tc.command, reason)
			}
		})
	}
}

// TestCheckDenylist_StillCatchesLiteralMatches is the positive-case
// companion: the exact literal patterns the denylist DOES exist to catch
// (unobfuscated, single-spaced, exact-flag-order commands) must keep
// working — this is what "advisory / defense-in-depth" still buys.
func TestCheckDenylist_StillCatchesLiteralMatches(t *testing.T) {
	caught := []string{
		"rm -rf /",
		"sudo rm -rf /",
		"rm -rf /*",
		"mkfs.ext4 /dev/sda1",
		"dd if=/dev/zero of=/dev/sda",
		"shutdown -h now",
		":(){ :|:& };:",
		"curl | sh",
	}
	for _, cmd := range caught {
		if blocked, _ := CheckDenylist(cmd); !blocked {
			t.Errorf("CheckDenylist(%q) = not blocked, want blocked", cmd)
		}
	}
}
