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
	// TASKS/agent-host-acp/23: the three Phase 4 bridge-mediated providers
	// (Claude via claudeacp, Codex via codexacp, Pi via piacp) are now wired
	// into dispatch, same as opencode/copilot above.
	if _, err := newACPClient("claude", adapters.TransportStdio); err != nil {
		t.Errorf("newACPClient(claude): unexpected error: %v", err)
	}
	if _, err := newACPClient("codex", adapters.TransportStdio); err != nil {
		t.Errorf("newACPClient(codex): unexpected error: %v", err)
	}
	if _, err := newACPClient("pi", adapters.TransportStdio); err != nil {
		t.Errorf("newACPClient(pi): unexpected error: %v", err)
	}
	// "pty-claude"/"sub-codex" normalize the same way "pty-opencode" does
	// above -- confirms the bridge providers get the same alias handling
	// the two pre-existing native ACP providers already had.
	if _, err := newACPClient("pty-claude", adapters.TransportStdio); err != nil {
		t.Errorf("newACPClient(pty-claude): unexpected error: %v", err)
	}
	if _, err := newACPClient("sub-codex", adapters.TransportStdio); err != nil {
		t.Errorf("newACPClient(sub-codex): unexpected error: %v", err)
	}
	// The bridge providers ignore the transport argument entirely (stdio
	// only -- see newACPClient's own doc comment) -- confirm a tcp request
	// still succeeds rather than erroring, unlike an unsupported provider.
	if _, err := newACPClient("claude", adapters.TransportTCP); err != nil {
		t.Errorf("newACPClient(claude, tcp): unexpected error: %v", err)
	}
	if _, err := newACPClient("nonsense-provider", adapters.TransportStdio); err == nil {
		t.Error("newACPClient(nonsense-provider): expected an error for an unsupported provider")
	}
}

// TestAcpSupportedProvidersTable pins the exact provider set newACPClient
// dispatches for -- the two native ACP adapters (task 09 OpenCode, task 10
// Copilot CLI) plus the three Phase 4 bridge-mediated adapters
// (TASKS/agent-host-acp/23: Claude, Codex, Pi).
func TestAcpSupportedProvidersTable(t *testing.T) {
	want := map[string]bool{
		"opencode": true,
		"copilot":  true,
		"claude":   true,
		"codex":    true,
		"pi":       true,
	}
	if len(acpSupportedProviders) != len(want) {
		t.Fatalf("acpSupportedProviders has %d entries, want %d: %v", len(acpSupportedProviders), len(want), acpSupportedProviders)
	}
	for name, wantOK := range want {
		if got := acpSupportedProviders[name]; got != wantOK {
			t.Errorf("acpSupportedProviders[%q] = %v, want %v", name, got, wantOK)
		}
	}
}
