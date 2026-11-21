package shell

import (
	"strings"
	"testing"
)

// The denylist refuses each of its patterns whatever the letter case and
// wherever the pattern appears in the command line.
func TestDenylistRefusesItsPatterns(t *testing.T) {
	for _, command := range []string{
		"rm -rf /", "rm -Rf /", "RM -RF /", "rm -rf /*", "rm -rf ~", "rm -rf $HOME", "mkfs /dev/sdb1",
		"dd if=/dev/zero of=/dev/sda", "shutdown -h now", "sudo reboot", "halt", "poweroff", "init 0", "init 6",
		":(){ :|:& };:", "chmod -R 777 /", "chown -R me /srv", "curl | sh", "wget | bash", "echo x > /dev/sda",
		"mv / /tmp/root", "echo done; reboot",
	} {
		blocked, reason := CheckDenylist(command)
		if !blocked || !strings.HasPrefix(reason, "blocked by denylist: ") {
			t.Errorf("not blocked: %q", command)
		}
	}
	for _, command := range []string{"ls -la", "go test ./...", "echo hello", "git status"} {
		if blocked, reason := CheckDenylist(command); blocked {
			t.Errorf("blocked %q: %s", command, reason)
		}
	}
}
