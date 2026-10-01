//go:build linux

package sandbox

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// netnsLoopbackFailure prefixes the helper's message when it cannot bring up
// the namespace's loopback; ProbeNetworkBridge matches it.
const netnsLoopbackFailure = "bring up loopback"

// ErrNetworkBridgeUnavailable reports a host on which a network-granted
// sandboxed command cannot run: the netns helper needs CAP_NET_ADMIN inside
// bwrap's network namespace to bring its loopback up, and the host denies
// it. On Ubuntu with kernel.apparmor_restrict_unprivileged_userns=1, bwrap's
// child runs under the unpriv_bwrap AppArmor profile, which denies every
// capability (CW-20261001-0079). Network-denied sandboxed commands are
// unaffected.
var ErrNetworkBridgeUnavailable = errors.New("sandbox: network-granted exec is unavailable on this host: bwrap's network namespace denies CAP_NET_ADMIN, so the netns bridge cannot bring up loopback (on Ubuntu: AppArmor's unpriv_bwrap profile under kernel.apparmor_restrict_unprivileged_userns=1). Run the command without network access, or have the operator apply the host fix in Torque CW-20261001-0079")

// networkBridgeProbe is the probe networkBridgeCheck runs; tests replace it.
var networkBridgeProbe = ProbeNetworkBridge

var (
	networkBridgeOnce sync.Once
	networkBridgeErr  error
)

// networkBridgeCheck reports, from a probe run once per process, whether a
// network-granted sandboxed command can run here (CW-20261001-0079): the
// cached ErrNetworkBridgeUnavailable error when the netns helper cannot raise
// loopback, nil otherwise. A probe failure of any other kind is not cached as
// a refusal; the real exec reports its own error. applyOSSandbox calls it
// only for a network-granted command, so a network-denied one never probes.
func networkBridgeCheck() error {
	networkBridgeOnce.Do(func() {
		if err := networkBridgeProbe(); errors.Is(err, ErrNetworkBridgeUnavailable) {
			networkBridgeErr = err
		}
	})
	return networkBridgeErr
}

// ProbeNetworkBridge runs a no-op command through the network-granted
// sandbox path (bwrap's network namespace plus the netns bridge) and reports
// whether this host can run it: nil when it can, an error wrapping
// ErrNetworkBridgeUnavailable when the helper cannot bring up loopback, and
// any other failure as it came. It touches nothing outside a temp dir.
func ProbeNetworkBridge() error {
	// A listener stands in for the allowlist proxy the bridge forwards to.
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("sandbox: network bridge probe: listen: %w", err)
	}
	defer func() {
		_ = ln.Close() // The probe's stand-in listener; nothing reads its close error.
	}()
	dir, err := os.MkdirTemp("", "nanite-netns-probe-")
	if err != nil {
		return fmt.Errorf("sandbox: network bridge probe: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(dir) // Best-effort removal of the probe's own temp dir.
	}()

	cmd := exec.Command("true")
	cleanup, _, err := applyBwrapSandbox(cmd, dir, "", []string{"probe.invalid"}, ln.Addr().String(), nil)
	if err != nil {
		return fmt.Errorf("sandbox: network bridge probe: %w", err)
	}
	defer cleanup()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if strings.Contains(msg, netnsLoopbackFailure) {
			return fmt.Errorf("%w (%s)", ErrNetworkBridgeUnavailable, msg)
		}
		return fmt.Errorf("sandbox: network bridge probe: %w: %s", err, msg)
	}
	return nil
}
