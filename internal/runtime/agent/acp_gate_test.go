package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/hollis-labs/go-agent-wrapper/adapters"
	"github.com/hollis-labs/nanite/internal/store"
)

// The ACP launch gate (CW-20260930-0113): go-agent-wrapper before v0.21.1 panics
// the host process when an ACP agent exits during launch, so every ACP mode
// is refused by default. TestMain opts the package in for its fake-adapter
// ACP tests; these turn the gate back on with t.Setenv.

const acpGateMessage = "ACP runtimes are disabled until Nanite runs go-agent-wrapper ≥ v0.21.1 (ACP session panic on early exit); set NANITE_ALLOW_ACP_RUNTIMES=1 to override"

func TestACPGate_RefusesEveryACPModeByDefault(t *testing.T) {
	t.Setenv("NANITE_ALLOW_ACP_RUNTIMES", "")
	acp := &store.AgentProfile{Protocol: "acp"}
	acpTCP := &store.AgentProfile{Protocol: "acp", Transport: "tcp"}
	for _, tc := range []struct {
		provider string
		profile  *store.AgentProfile
	}{
		{"copilot", nil}, {"pi", nil}, {"copilot", acpTCP},
		{"claude", acp}, {"codex", acp}, {"opencode", acp}, {"codex", acpTCP},
	} {
		_, err := selectRuntime(tc.provider, tc.profile)
		if !errors.Is(err, ErrACPRuntimesDisabled) || err.Error() != acpGateMessage {
			t.Errorf("selectRuntime(%q, %+v) = %v, want %q", tc.provider, tc.profile, err, acpGateMessage)
		}
	}
	for _, name := range []string{"copilot", "pi"} {
		if CanLaunch(name) {
			t.Errorf("CanLaunch(%q) = true while ACP is gated", name)
		}
		if err := LaunchError(name); !errors.Is(err, ErrACPRuntimesDisabled) {
			t.Errorf("LaunchError(%q) = %v, want ErrACPRuntimesDisabled", name, err)
		}
	}
}

func TestACPGate_NativeRuntimesUnaffected(t *testing.T) {
	t.Setenv("NANITE_ALLOW_ACP_RUNTIMES", "")
	for _, name := range []string{"claude", "pty-claude", "codex", "opencode"} {
		sel, err := selectRuntime(name, nil)
		if err != nil || sel.ACP() {
			t.Errorf("selectRuntime(%q) = %+v, %v; want a native selection", name, sel, err)
		}
		if !CanLaunch(name) {
			t.Errorf("CanLaunch(%q) = false", name)
		}
	}
}

func TestACPGate_EnvOverrideAllowsACP(t *testing.T) {
	for _, v := range []string{"1", "true", "YES"} {
		t.Setenv("NANITE_ALLOW_ACP_RUNTIMES", v)
		if !CanLaunch("copilot") || !CanLaunch("pi") {
			t.Errorf("NANITE_ALLOW_ACP_RUNTIMES=%q: copilot/pi not launchable", v)
		}
		if _, err := selectRuntime("claude", &store.AgentProfile{Protocol: "acp"}); err != nil {
			t.Errorf("NANITE_ALLOW_ACP_RUNTIMES=%q: claude acp refused: %v", v, err)
		}
	}
}

// Boot refuses before any adapter or process exists: the ACP factory is
// never called.
func TestACPGate_BootRefusesBeforeLaunching(t *testing.T) {
	t.Setenv("NANITE_ALLOW_ACP_RUNTIMES", "")
	deps, _ := makeBootDeps(t, "codex")
	called := false
	deps.ACPAdapterFactory = func(string, adapters.Transport) (adapters.Adapter, error) {
		called = true
		return nil, errors.New("must not be reached")
	}
	_, err := Boot(context.Background(), deps, Options{Mode: ModeLongLived, Provider: "copilot"})
	if !errors.Is(err, ErrACPRuntimesDisabled) {
		t.Fatalf("Boot(copilot) = %v, want ErrACPRuntimesDisabled", err)
	}
	if called {
		t.Fatal("the ACP adapter factory was called while ACP is gated")
	}
}
