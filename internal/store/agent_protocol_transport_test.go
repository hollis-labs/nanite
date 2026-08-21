package store

// TASKS/agent-host-acp/11-nanite-per-agent-protocol-transport-config.md:
// tests for the agent_profiles.protocol/transport columns (migration 134)
// -- the per-agent Protocol/Transport selection docs/engineering/
// architecture/17-acp.md calls for. Mirrors agents_composition_test.go's
// existing RoleID/ModelID/RuntimeKind test shapes.

import "testing"

// TestCreateAgent_ProtocolTransportNullableRoundTrip confirms the two new
// nullable columns round-trip correctly both when left unset (empty string
// in, empty string out) and when explicitly set to "acp"/"stdio"/"tcp".
func TestCreateAgent_ProtocolTransportNullableRoundTrip(t *testing.T) {
	s := newTestStore(t)

	unset := &AgentProfile{Name: "Native", Slug: "native-protocol", SystemPrompt: "x"}
	if err := s.CreateAgent(unset); err != nil {
		t.Fatalf("CreateAgent(unset): %v", err)
	}
	got, err := s.GetAgent(unset.ID)
	if err != nil {
		t.Fatalf("GetAgent(unset): %v", err)
	}
	if got.Protocol != "" {
		t.Errorf("Protocol = %q, want empty for a row that never set one", got.Protocol)
	}
	if got.Transport != "" {
		t.Errorf("Transport = %q, want empty for a row that never set one", got.Transport)
	}

	acpAgent := &AgentProfile{
		Name: "ACP OpenCode", Slug: "acp-opencode", SystemPrompt: "x",
		DefaultProvider: "opencode", Protocol: "acp", Transport: "stdio",
	}
	if err := s.CreateAgent(acpAgent); err != nil {
		t.Fatalf("CreateAgent(acp): %v", err)
	}
	got, err = s.GetAgent(acpAgent.ID)
	if err != nil {
		t.Fatalf("GetAgent(acp): %v", err)
	}
	if got.Protocol != "acp" {
		t.Errorf("Protocol = %q, want acp", got.Protocol)
	}
	if got.Transport != "stdio" {
		t.Errorf("Transport = %q, want stdio", got.Transport)
	}
	// No new runtime_kind value -- an ACP-configured agent still shows
	// runtime_kind='cli' (inferRuntimeKind's own classification for
	// provider "opencode" is 'api'... but this task explicitly forbids a
	// NEW runtime_kind value, not asserts a specific one; the load-bearing
	// assertion is that RuntimeKind stays within {cli, api} unchanged by
	// setting Protocol/Transport, i.e. the enum's value set never grew).
	if got.RuntimeKind != "cli" && got.RuntimeKind != "api" {
		t.Errorf("RuntimeKind = %q, want 'cli' or 'api' (no new runtime_kind value introduced)", got.RuntimeKind)
	}

	// UpdateAgent must round-trip the same two columns.
	got.Transport = "tcp"
	if err := s.UpdateAgent(got); err != nil {
		t.Fatalf("UpdateAgent(transport=tcp): %v", err)
	}
	after, err := s.GetAgent(acpAgent.ID)
	if err != nil {
		t.Fatalf("GetAgent(after update): %v", err)
	}
	if after.Transport != "tcp" {
		t.Errorf("Transport after update = %q, want tcp", after.Transport)
	}
	if after.Protocol != "acp" {
		t.Errorf("Protocol after unrelated update = %q, want unchanged 'acp'", after.Protocol)
	}
}

// TestCreateAgent_ProtocolInvalidRejected confirms the Go-layer validation
// (mirroring the DB-level CHECK migration 134 adds) rejects any protocol
// value outside the known set.
func TestCreateAgent_ProtocolInvalidRejected(t *testing.T) {
	s := newTestStore(t)
	a := &AgentProfile{Name: "Bad", Slug: "bad-protocol", SystemPrompt: "x", Protocol: "made-up"}
	if err := s.CreateAgent(a); err == nil {
		t.Fatal("CreateAgent with protocol='made-up' should have failed validation")
	}
}

// TestCreateAgent_TransportRequiresACPProtocol confirms a non-empty
// transport is rejected unless protocol="acp" -- native protocols/the
// unset default have their transport implied by the protocol itself, so a
// transport value alongside them is rejected as misleading rather than
// silently ignored.
func TestCreateAgent_TransportRequiresACPProtocol(t *testing.T) {
	s := newTestStore(t)
	cases := []struct {
		name     string
		protocol string
	}{
		{"empty protocol", ""},
		{"native protocol", "opencode-native"},
	}
	for _, c := range cases {
		a := &AgentProfile{
			Name: "Bad Transport", Slug: "bad-transport-" + c.name, SystemPrompt: "x",
			Protocol: c.protocol, Transport: "stdio",
		}
		if err := s.CreateAgent(a); err == nil {
			t.Errorf("%s: CreateAgent with transport set but protocol=%q should have failed validation", c.name, c.protocol)
		}
	}
}

// TestUpdateAgentACPConfig_DirectWrite confirms the store.UpdateAgentACPConfig
// direct-DB write path (mirroring UpdateAgentComposition's RoleID/ConsumerID/
// ModelID precedent) sets/clears protocol/transport independent of the
// managed-file round-trip, and that every OTHER existing agent's row is
// left completely untouched by it.
func TestUpdateAgentACPConfig_DirectWrite(t *testing.T) {
	s := newTestStore(t)

	untouched := &AgentProfile{Name: "Untouched", Slug: "untouched", SystemPrompt: "x"}
	if err := s.CreateAgent(untouched); err != nil {
		t.Fatalf("CreateAgent(untouched): %v", err)
	}

	target := &AgentProfile{Name: "Target", Slug: "acp-target", SystemPrompt: "x", DefaultProvider: "copilot"}
	if err := s.CreateAgent(target); err != nil {
		t.Fatalf("CreateAgent(target): %v", err)
	}

	protocol, transport := "acp", "tcp"
	if err := s.UpdateAgentACPConfig(target.ID, &protocol, &transport); err != nil {
		t.Fatalf("UpdateAgentACPConfig: %v", err)
	}

	got, err := s.GetAgent(target.ID)
	if err != nil {
		t.Fatalf("GetAgent(target): %v", err)
	}
	if got.Protocol != "acp" || got.Transport != "tcp" {
		t.Errorf("target Protocol/Transport = %q/%q, want acp/tcp", got.Protocol, got.Transport)
	}
	if got.RuntimeKind != "cli" && got.RuntimeKind != "api" {
		t.Errorf("target RuntimeKind = %q, want an existing value ('cli' or 'api'), no new value introduced", got.RuntimeKind)
	}

	other, err := s.GetAgent(untouched.ID)
	if err != nil {
		t.Fatalf("GetAgent(untouched): %v", err)
	}
	if other.Protocol != "" || other.Transport != "" {
		t.Errorf("untouched agent's Protocol/Transport = %q/%q, want both empty (UpdateAgentACPConfig must not affect other rows)", other.Protocol, other.Transport)
	}

	// nil pointers leave both columns untouched.
	if err := s.UpdateAgentACPConfig(target.ID, nil, nil); err != nil {
		t.Fatalf("UpdateAgentACPConfig(nil, nil): %v", err)
	}
	still, err := s.GetAgent(target.ID)
	if err != nil {
		t.Fatalf("GetAgent(target, after nil-nil update): %v", err)
	}
	if still.Protocol != "acp" || still.Transport != "tcp" {
		t.Errorf("Protocol/Transport after nil-nil update = %q/%q, want unchanged acp/tcp", still.Protocol, still.Transport)
	}

	// Explicit empty-string pointers clear both columns back to "native".
	empty := ""
	if err := s.UpdateAgentACPConfig(target.ID, &empty, &empty); err != nil {
		t.Fatalf("UpdateAgentACPConfig(clear): %v", err)
	}
	cleared, err := s.GetAgent(target.ID)
	if err != nil {
		t.Fatalf("GetAgent(target, after clear): %v", err)
	}
	if cleared.Protocol != "" || cleared.Transport != "" {
		t.Errorf("Protocol/Transport after clear = %q/%q, want both empty", cleared.Protocol, cleared.Transport)
	}
}

// TestUpdateAgentACPConfig_RejectsInvalidTransportWithoutACP confirms the
// direct-write path enforces the same enum/pairing validation
// CreateAgent/UpdateAgent do, rather than bypassing it.
func TestUpdateAgentACPConfig_RejectsInvalidTransportWithoutACP(t *testing.T) {
	s := newTestStore(t)
	target := &AgentProfile{Name: "Target", Slug: "acp-invalid-target", SystemPrompt: "x"}
	if err := s.CreateAgent(target); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	transport := "stdio"
	if err := s.UpdateAgentACPConfig(target.ID, nil, &transport); err == nil {
		t.Fatal("UpdateAgentACPConfig setting transport without protocol='acp' should have failed validation")
	}
}
