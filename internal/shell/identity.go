package shell

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ProcessIdentity identifies a live process-group leader by its start time and
// group. Recovery uses it to avoid signaling a reused process ID.
func ProcessIdentity(ctx context.Context, pid int) string {
	if pid <= 1 {
		return ""
	}
	limit, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	cmd := exec.CommandContext(limit, "/bin/ps", "-o", "pgid=", "-o", "lstart=", "-p", strconv.Itoa(pid))
	cmd.Env = []string{"LC_ALL=C"}
	raw, err := cmd.Output()
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(raw))
	if len(fields) < 2 || fields[0] != strconv.Itoa(pid) {
		return ""
	}
	return strings.Join(fields, " ")
}

func TerminateRecordedGroup(ctx context.Context, pid int, identity string) error {
	if identity == "" || ProcessIdentity(ctx, pid) != identity {
		return nil
	}
	return terminateGroup(pid)
}
