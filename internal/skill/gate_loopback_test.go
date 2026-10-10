package skill

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// CW-20261001-0079: a loopback-granted skill on a host where go-sandbox's
// loopback helper cannot raise lo used to fail with a bare exit 125. The gate
// now refuses it up front, with the cause and the host-fix pointer, from a
// probe that runs once per process.

// injectLoopbackSandboxProbe swaps the probe loopbackSandboxCheck runs and
// clears its cached result, restoring both when the test ends.
func injectLoopbackSandboxProbe(t *testing.T, probe func() error) {
	t.Helper()
	prev := loopbackSandboxProbe
	loopbackSandboxProbe = probe
	loopbackSandboxOnce = sync.Once{}
	loopbackSandboxErr = nil
	t.Cleanup(func() {
		loopbackSandboxProbe = prev
		loopbackSandboxOnce = sync.Once{}
		loopbackSandboxErr = nil
	})
}

func unavailableLoopbackProbe() error {
	return fmt.Errorf("%w (injected)", errLoopbackSandboxUnavailable)
}

func TestExecuteGated_LoopbackGrantRefusedClearlyWhereHelperCannotRun(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the loopback helper is linux-only")
	}
	probes := 0
	injectLoopbackSandboxProbe(t, func() error { probes++; return unavailableLoopbackProbe() })
	g, agentID, skillSlug := newAuthorizedGate(t, `{"network":{"allow_loopback":true}}`)
	before := skillBoundarySnapshot(t, g.Skills.(*store.Store))
	_, err := g.ExecuteGated(context.Background(), ExecRequest{
		SkillSlug: skillSlug, AgentID: agentID,
		Command: []string{"/bin/true"}, WorkDir: t.TempDir(),
		Kind: ExecKindMarker, Label: "loopback-unavailable",
	})
	if !errors.Is(err, errLoopbackSandboxUnavailable) {
		t.Fatalf("ExecuteGated error = %v, want errLoopbackSandboxUnavailable", err)
	}
	for _, want := range []string{"CAP_NET_ADMIN", "CW-20261001-0079"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not carry %q", err, want)
		}
	}
	if probes != 1 {
		t.Fatalf("unavailable helper probed %d times", probes)
	}
	if after := skillBoundarySnapshot(t, g.Skills.(*store.Store)); !reflect.DeepEqual(before, after) {
		t.Fatalf("loopback refusal changed state: %#v -> %#v", before, after)
	}
}

func TestExecuteGated_NoLoopbackGrantNeverProbes(t *testing.T) {
	requireSandboxTool(t)
	probes := 0
	injectLoopbackSandboxProbe(t, func() error {
		probes++
		return unavailableLoopbackProbe()
	})
	g, agentID, skillSlug := newAuthorizedGate(t, `{}`)
	if _, err := g.ExecuteGated(context.Background(), ExecRequest{
		SkillSlug: skillSlug, AgentID: agentID,
		Command: []string{"/bin/true"}, WorkDir: t.TempDir(),
		Kind: ExecKindMarker, Label: "no-network",
	}); err != nil {
		t.Fatalf("network-denied skill exec: %v", err)
	}
	if probes != 0 {
		t.Fatalf("a skill without a loopback grant ran the probe %d times", probes)
	}
}

func TestLoopbackSandboxCheck_ProbesOncePerProcess(t *testing.T) {
	probes := 0
	injectLoopbackSandboxProbe(t, func() error {
		probes++
		return unavailableLoopbackProbe()
	})
	for i := 0; i < 3; i++ {
		if err := loopbackSandboxCheck(); !errors.Is(err, errLoopbackSandboxUnavailable) {
			t.Fatalf("check %d = %v, want the cached errLoopbackSandboxUnavailable", i, err)
		}
	}
	if probes != 1 {
		t.Fatalf("probe ran %d times, want once", probes)
	}
}
