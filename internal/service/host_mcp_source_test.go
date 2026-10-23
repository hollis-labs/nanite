package service

import (
	"context"
	"slices"
	"testing"

	"github.com/hollis-labs/nanite/internal/runtime/agent"
)

type fixedGrantReader []string

func (f fixedGrantReader) ResolveToolNames(context.Context, string, string) ([]string, error) {
	return slices.Clone(f), nil
}

type fixedStanding CallerStanding

func (f fixedStanding) CallerStanding(context.Context, string) (CallerStanding, error) {
	return CallerStanding(f), nil
}

func capCtx(t *testing.T, host, rev string, narrow ToolNarrowing) context.Context {
	t.Helper()
	c, err := newCapabilityRegistry().issueRoot("view", "turn", "run", host, rev, "cred-1", narrow)
	if err != nil {
		t.Fatal(err)
	}
	return withExecutionCapability(context.Background(), c)
}

// The CLI projection goes through the same decider as the API runtime: the
// credential's standing and scope and the turn's narrowing all apply.
func TestStoreHostMCP_GrantedToolsAppliesTheNativeChecks(t *testing.T) {
	names := fixedGrantReader{"tether_message_send", "tether_message_get"}
	hm := func(standing CallerStanding) storeHostMCP {
		return storeHostMCP{grants: NewNativeGrants(names, fixedStanding(standing))}
	}
	granted := func(h storeHostMCP, ctx context.Context) []string {
		t.Helper()
		got, err := h.GrantedTools(ctx, "host-1", "rev-1")
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	if got := granted(hm(CallerStanding{Valid: true, Tools: true}), capCtx(t, "host-1", "rev-1", nil)); !slices.Equal(got, []string{"tether_message_send", "tether_message_get"}) {
		t.Fatalf("baseline = %v", got)
	}
	if got := granted(hm(CallerStanding{Valid: true, Tools: true}), capCtx(t, "host-1", "rev-1", ToolNarrowing{"tether_message_get": true})); !slices.Equal(got, []string{"tether_message_get"}) {
		t.Fatalf("narrowed = %v", got)
	}
	for name, c := range map[string]CallerStanding{"revoked caller": {}, "operate-only caller": {Valid: true}} {
		if got := granted(hm(c), capCtx(t, "host-1", "rev-1", nil)); len(got) != 0 {
			t.Errorf("%s got %v", name, got)
		}
	}
	h := hm(CallerStanding{Valid: true, Tools: true})
	if got := granted(h, context.Background()); len(got) != 0 {
		t.Errorf("no capability got %v", got)
	}
	if got := granted(h, capCtx(t, "host-1", "other-rev", nil)); len(got) != 0 {
		t.Errorf("another revision's capability got %v", got)
	}
	if got := granted(storeHostMCP{}, capCtx(t, "host-1", "rev-1", nil)); len(got) != 0 {
		t.Errorf("no grants wired got %v", got)
	}
}

func TestBuildAgentDependencies_WiresHostMCPThroughTheNativeGrants(t *testing.T) {
	st := newConfigTestStore(t)
	grants := NewNativeGrants(fixedGrantReader{"tether_message_send"}, fixedStanding{Valid: true, Tools: true})
	bundle, err := BuildAgentDependencies(AgentDepsConfig{Store: st, Streams: NewStreamManager(st), BinaryPath: "/bin/true", NativeGrants: grants})
	if err != nil {
		t.Fatal(err)
	}
	src, ok := bundle.Deps.HostMCP.(storeHostMCP)
	if !ok || src.st != st || src.grants != grants {
		t.Fatalf("Dependencies.HostMCP = %#v", bundle.Deps.HostMCP)
	}
	var _ agent.HostMCPSource = src
}
