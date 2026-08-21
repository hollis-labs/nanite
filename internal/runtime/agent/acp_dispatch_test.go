package agent

// TASKS/agent-host-acp/11-nanite-per-agent-protocol-transport-config.md:
// pins factory.go's useACPProtocol/effectiveACPTransport dispatch
// predicates and acp_session.go's newACPClient provider-name dispatch.

import (
	"testing"

	"github.com/hollis-labs/go-agent-wrapper/adapters"
	"github.com/hollis-labs/nanite/internal/store"
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

func TestNewACPClient(t *testing.T) {
	if _, err := newACPClient("opencode", adapters.TransportStdio); err != nil {
		t.Errorf("newACPClient(opencode): unexpected error: %v", err)
	}
	if _, err := newACPClient("copilot", adapters.TransportStdio); err != nil {
		t.Errorf("newACPClient(copilot, stdio): unexpected error: %v", err)
	}
	if _, err := newACPClient("copilot", adapters.TransportTCP); err != nil {
		t.Errorf("newACPClient(copilot, tcp): unexpected error: %v", err)
	}
	// "pty-opencode" normalizes to "opencode" via normalizeProviderName --
	// same alias handling every other provider-name dispatch in this
	// package (shouldUseStreamingStdio, bootdirLayoutFor) already honors.
	if _, err := newACPClient("pty-opencode", adapters.TransportStdio); err != nil {
		t.Errorf("newACPClient(pty-opencode): unexpected error: %v", err)
	}
	if _, err := newACPClient("claude", adapters.TransportStdio); err == nil {
		t.Error("newACPClient(claude): expected an error -- Claude ACP is a Phase 4 bridge, not selectable yet")
	}
}
