//go:build linux

package sandbox

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// CW-20261001-0079: on a host whose bwrap network namespace cannot raise
// loopback, a network-granted exec used to fail with a bare exit 125 from
// the netns helper. applyOSSandbox now refuses it up front, with the cause
// and the host-fix pointer, from a probe that runs once per process.

// injectNetworkBridgeProbe swaps the probe networkBridgeCheck runs and
// clears its cached result, restoring both when the test ends.
func injectNetworkBridgeProbe(t *testing.T, probe func() error) {
	t.Helper()
	prev := networkBridgeProbe
	networkBridgeProbe = probe
	networkBridgeOnce = sync.Once{}
	networkBridgeErr = nil
	t.Cleanup(func() {
		networkBridgeProbe = prev
		networkBridgeOnce = sync.Once{}
		networkBridgeErr = nil
	})
}

func unavailableBridgeProbe() error {
	return fmt.Errorf("%w (injected)", ErrNetworkBridgeUnavailable)
}

func TestAgentExec_NetworkGrantRefusedClearlyWhereBridgeCannotRun(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap not installed")
	}
	t.Setenv("HOME", t.TempDir())
	injectNetworkBridgeProbe(t, unavailableBridgeProbe)

	_, err := AgentExec(AgentExecOpts{
		SessionID:    "test-bridge-unavailable",
		Command:      "/bin/echo",
		Args:         []string{"never runs"},
		Timeout:      5 * time.Second,
		NetworkAllow: []string{"example.com"},
	})
	if !errors.Is(err, ErrNetworkBridgeUnavailable) {
		t.Fatalf("AgentExec error = %v, want ErrNetworkBridgeUnavailable", err)
	}
	for _, want := range []string{"CAP_NET_ADMIN", "CW-20261001-0079"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not carry %q", err, want)
		}
	}
}

func TestAgentExec_NetworkDeniedNeverProbesTheBridge(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap not installed")
	}
	t.Setenv("HOME", t.TempDir())
	probes := 0
	injectNetworkBridgeProbe(t, func() error {
		probes++
		return unavailableBridgeProbe()
	})

	result, err := AgentExec(AgentExecOpts{
		SessionID: "test-no-network",
		Command:   "/bin/echo",
		Args:      []string{"hello"},
		Timeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatalf("network-denied AgentExec: %v", err)
	}
	if strings.TrimSpace(result.Stdout) != "hello" {
		t.Fatalf("stdout = %q, want hello", result.Stdout)
	}
	if probes != 0 {
		t.Fatalf("a network-denied exec ran the bridge probe %d times", probes)
	}
}

func TestNetworkBridgeCheck_ProbesOncePerProcess(t *testing.T) {
	probes := 0
	injectNetworkBridgeProbe(t, func() error {
		probes++
		return unavailableBridgeProbe()
	})
	for i := 0; i < 3; i++ {
		if err := networkBridgeCheck(); !errors.Is(err, ErrNetworkBridgeUnavailable) {
			t.Fatalf("check %d = %v, want the cached ErrNetworkBridgeUnavailable", i, err)
		}
	}
	if probes != 1 {
		t.Fatalf("probe ran %d times, want once", probes)
	}
}

func TestNetworkBridgeCheck_OtherProbeFailuresDoNotRefuse(t *testing.T) {
	injectNetworkBridgeProbe(t, func() error { return errors.New("bwrap not found") })
	if err := networkBridgeCheck(); err != nil {
		t.Fatalf("check = %v, want nil: only ErrNetworkBridgeUnavailable is a refusal", err)
	}
}
