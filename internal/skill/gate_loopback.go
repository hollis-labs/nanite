package skill

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/hollis-labs/substrate/harness/sandbox"
)

// errLoopbackSandboxUnavailable reports a host on which a loopback-granted
// skill cannot run (CW-20261001-0079). go-sandbox's loopback helper needs
// CAP_NET_ADMIN inside bwrap's network namespace to bring lo up; on Ubuntu
// with kernel.apparmor_restrict_unprivileged_userns=1, bwrap's child runs
// under the unpriv_bwrap AppArmor profile, which denies every capability,
// and the helper exits 125. The gate returns this instead of that exit code.
var errLoopbackSandboxUnavailable = errors.New("loopback-granted sandboxed exec is unavailable on this host: bwrap's network namespace denies CAP_NET_ADMIN, so go-sandbox's loopback helper cannot bring up lo (on Ubuntu: AppArmor's unpriv_bwrap profile under kernel.apparmor_restrict_unprivileged_userns=1). Run the skill without a network capability, or have the operator apply the host fix in Torque CW-20261001-0079")

// loopbackSandboxProbe is the probe loopbackSandboxCheck runs; tests replace it.
var loopbackSandboxProbe = probeLoopbackSandbox

var (
	loopbackSandboxOnce sync.Once
	loopbackSandboxErr  error
)

// loopbackSandboxCheck reports, from a probe run once per process, whether a
// loopback-granted skill can run here: the cached
// errLoopbackSandboxUnavailable error when it cannot, nil otherwise. A probe
// failure of any other kind is not cached as a refusal; the real exec
// reports its own error.
func loopbackSandboxCheck() error {
	loopbackSandboxOnce.Do(func() {
		if err := loopbackSandboxProbe(); errors.Is(err, errLoopbackSandboxUnavailable) {
			loopbackSandboxErr = err
		}
	})
	return loopbackSandboxErr
}

// usesLoopbackHelper reports whether go-sandbox will start its loopback
// helper for p: on linux, a profile without full network that allows
// loopback or forwards loopback ports (its apply_linux.go condition).
func usesLoopbackHelper(p sandbox.Profile) bool {
	return runtime.GOOS == "linux" && !p.Net && (p.AllowLoopback || len(p.LoopbackForwardPorts) > 0)
}

// probeLoopbackSandbox runs `true` under a loopback-granted profile in a
// temp workspace and returns errLoopbackSandboxUnavailable when the loopback
// helper cannot bring lo up.
func probeLoopbackSandbox() error {
	dir, err := os.MkdirTemp("", "nanite-loopback-probe-")
	if err != nil {
		return fmt.Errorf("loopback sandbox probe: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(dir) // Best-effort removal of the probe's own temp workspace.
	}()
	cmd := exec.Command("true")
	cleanup, err := applySandbox(cmd, sandbox.Profile{ID: "loopback-probe", AllowLoopback: true, Subprocess: true}, dir)
	if err != nil {
		return fmt.Errorf("loopback sandbox probe: %w", err)
	}
	defer cleanup()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if strings.Contains(msg, "bring up loopback") {
			return fmt.Errorf("%w (%s)", errLoopbackSandboxUnavailable, msg)
		}
		return fmt.Errorf("loopback sandbox probe: %w: %s", err, msg)
	}
	return nil
}
