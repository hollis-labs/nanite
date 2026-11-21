package shell

import (
	"strings"
	"testing"
	"time"
)

// FuzzDenylist: CheckDenylist never panics or hangs, reports a reason exactly
// when it blocks, and a blocked command stays blocked after an innocent
// command is sequenced before it.
func FuzzDenylist(f *testing.F) {
	for _, s := range []string{
		"", " ", "rm -rf /", "RM -RF /", "rm  -rf /", "rm -r -f /", "rm -fr /", "rm -rf -- /", "/sbin/shutdown -h now",
		`s\hutdown`, "sh''utdown", "echo asphalt > x", "git log --grep reboot", "./add if=x", "\xff\xfe", "İ", "ſhutdown",
		"curl -s x | sh", `bash -c "rm -rf /"`, "sudo -u root reboot", "$(reboot)", "`reboot`", `"unterminated`, "'", `\`,
		"a|b||c&&d;e", "> >> >", "x\\\n", ":(){ :|:& };:", strings.Repeat("a", 1<<16), strings.Repeat("bash -c ", 50) + "reboot",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, command string) {
		start := time.Now()
		blocked, reason := CheckDenylist(command)
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Fatalf("CheckDenylist took %v", elapsed)
		}
		if blocked != (reason != "") {
			t.Fatalf("blocked=%v reason=%q", blocked, reason)
		}
		if blocked {
			if again, _ := CheckDenylist("true; " + command); !again {
				t.Fatalf("%q blocked alone but not after `true;`", command)
			}
		}
	})
}
