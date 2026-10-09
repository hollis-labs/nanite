package agent

// TASKS/agent-host-acp/11-nanite-per-agent-protocol-transport-config.md:
// pins factory.go's useACPProtocol/effectiveACPTransport dispatch
// predicates and acp_adapter.go's shipped-adapter dispatch.

import (
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/substrate/harness/adapters"
	"github.com/hollis-labs/substrate/harness/adapters/launch"
	"github.com/hollis-labs/substrate/llm-core/contracts/runtimes"
)

func TestUseACPProtocol(t *testing.T) {
	cases := []struct {
		name    string
		profile *store.AgentProfile
		want    bool
	}{
		{"nil profile", nil, false},
		{"empty protocol (default native)", &store.AgentProfile{}, false},
		{"native protocol explicitly pinned", &store.AgentProfile{Protocol: "opencode-native"}, false},
		{"acp protocol", &store.AgentProfile{Protocol: "acp"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := useACPProtocol(c.profile); got != c.want {
				t.Errorf("useACPProtocol(%+v) = %v, want %v", c.profile, got, c.want)
			}
		})
	}
}

func TestEffectiveACPTransport(t *testing.T) {
	cases := []struct {
		name    string
		profile *store.AgentProfile
		want    adapters.Transport
	}{
		{"nil profile defaults stdio", nil, adapters.TransportStdio},
		{"unset transport defaults stdio", &store.AgentProfile{Protocol: "acp"}, adapters.TransportStdio},
		{"explicit stdio", &store.AgentProfile{Protocol: "acp", Transport: "stdio"}, adapters.TransportStdio},
		{"explicit tcp", &store.AgentProfile{Protocol: "acp", Transport: "tcp"}, adapters.TransportTCP},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := effectiveACPTransport(c.profile); got != c.want {
				t.Errorf("effectiveACPTransport(%+v) = %q, want %q", c.profile, got, c.want)
			}
		})
	}
}

// TestUseACPProtocolDoesNotTouchRuntimeKind confirms an ACP-configured
// profile's RuntimeKind is whatever the caller set it to (including the
// existing 'cli' every CLI-launched agent already carries) -- useACPProtocol
// never reads or mutates RuntimeKind, so there is no new agents.runtime_kind
// value introduced by this dispatch.
func TestUseACPProtocolDoesNotTouchRuntimeKind(t *testing.T) {
	profile := &store.AgentProfile{RuntimeKind: "cli", Protocol: "acp"}
	if !useACPProtocol(profile) {
		t.Fatal("expected useACPProtocol to report true for protocol=acp")
	}
	if profile.RuntimeKind != "cli" {
		t.Errorf("RuntimeKind = %q, want unchanged 'cli' -- useACPProtocol must not introduce/consult a new runtime_kind value", profile.RuntimeKind)
	}
}

func TestNewACPAdapter(t *testing.T) {
	if _, err := newACPAdapter("opencode", adapters.TransportStdio); err != nil {
		t.Errorf("newACPAdapter(opencode): unexpected error: %v", err)
	}
	if _, err := newACPAdapter("copilot", adapters.TransportStdio); err != nil {
		t.Errorf("newACPAdapter(copilot, stdio): unexpected error: %v", err)
	}
	if _, err := newACPAdapter("copilot", adapters.TransportTCP); err != nil {
		t.Errorf("newACPAdapter(copilot, tcp): unexpected error: %v", err)
	}
	// "pty-opencode" normalizes to "opencode" via normalizeProviderName --
	// same alias handling every other provider-name dispatch in this
	// package (shouldUseStreamingStdio, bootdirLayoutFor) already honors.
	if _, err := newACPAdapter("pty-opencode", adapters.TransportStdio); err != nil {
		t.Errorf("newACPAdapter(pty-opencode): unexpected error: %v", err)
	}
	// TASKS/agent-host-acp/23: the three Phase 4 bridge-mediated providers
	// (Claude via claudeacp, Codex via codexacp, Pi via piacp) are now wired
	// into dispatch, same as opencode/copilot above.
	if _, err := newACPAdapter("claude", adapters.TransportStdio); err != nil {
		t.Errorf("newACPAdapter(claude): unexpected error: %v", err)
	}
	if _, err := newACPAdapter("codex", adapters.TransportStdio); err != nil {
		t.Errorf("newACPAdapter(codex): unexpected error: %v", err)
	}
	if _, err := newACPAdapter("pi", adapters.TransportStdio); err != nil {
		t.Errorf("newACPAdapter(pi): unexpected error: %v", err)
	}
	// "pty-claude"/"sub-codex" normalize the same way "pty-opencode" does
	// above -- confirms the bridge providers get the same alias handling
	// the two pre-existing native ACP providers already had.
	if _, err := newACPAdapter("pty-claude", adapters.TransportStdio); err != nil {
		t.Errorf("newACPAdapter(pty-claude): unexpected error: %v", err)
	}
	if _, err := newACPAdapter("sub-codex", adapters.TransportStdio); err != nil {
		t.Errorf("newACPAdapter(sub-codex): unexpected error: %v", err)
	}
	// The bridge providers ignore the transport argument entirely (stdio
	// only -- see newACPAdapter's own doc comment) -- confirm a tcp request
	// still succeeds rather than erroring, unlike an unsupported provider.
	if _, err := newACPAdapter("claude", adapters.TransportTCP); err != nil {
		t.Errorf("newACPAdapter(claude, tcp): unexpected error: %v", err)
	}
	if _, err := newACPAdapter("nonsense-provider", adapters.TransportStdio); err == nil {
		t.Error("newACPAdapter(nonsense-provider): expected an error for an unsupported provider")
	}
}

// TestNewACPAdapter_EveryRegistryACPRuntime: with the provider table gone,
// newACPAdapter accepts exactly the registry runtimes go-agent-wrapper has an
// acp-stdio launch factory for (CW-20260930-0113).
func TestNewACPAdapter_EveryRegistryACPRuntime(t *testing.T) {
	var got []string
	for _, k := range launch.Supported() {
		if k.Mode != runtimes.ModeACPStdio {
			continue
		}
		got = append(got, string(k.Runtime))
		if _, err := newACPAdapter(string(k.Runtime), adapters.TransportStdio); err != nil {
			t.Errorf("newACPAdapter(%s): %v", k.Runtime, err)
		}
	}
	for _, want := range []string{"claude", "codex", "opencode", "copilot", "pi"} {
		found := false
		for _, g := range got {
			found = found || g == want
		}
		if !found {
			t.Errorf("registry has no acp-stdio launch for %s (got %v)", want, got)
		}
	}
}
