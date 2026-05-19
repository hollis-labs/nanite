package gomsg_test

import (
	"testing"

	messaging "github.com/hollis-labs/go-messaging"
	"github.com/hollis-labs/nanite/internal/messaging/gomsg"
)

func TestAgentAddress_Roundtrip(t *testing.T) {
	addr := gomsg.AgentAddress("acme.example", "sess-42", "agent-x")
	if got := addr.URN(); got != "msg://agent/acme.example/sess-42/agent-x" {
		t.Fatalf("URN = %q", got)
	}
	if _, err := messaging.ParseURN(addr.URN()); err != nil {
		t.Fatalf("ParseURN rejects minted address: %v", err)
	}

	sess, agent, err := gomsg.Tuple(addr)
	if err != nil {
		t.Fatalf("Tuple: %v", err)
	}
	if sess != "sess-42" || agent != "agent-x" {
		t.Errorf("Tuple = (%q,%q), want (sess-42,agent-x)", sess, agent)
	}
}

func TestAgentAddress_UserSentinel(t *testing.T) {
	addr := gomsg.AgentAddress("nanite.local", "sess-1", "user")
	if addr.Kind != messaging.KindUser {
		t.Errorf("Kind = %q, want user", addr.Kind)
	}
	if got := addr.URN(); got != "msg://user/nanite.local/sess-1" {
		t.Fatalf("URN = %q", got)
	}
	sess, agent, err := gomsg.Tuple(addr)
	if err != nil {
		t.Fatalf("Tuple: %v", err)
	}
	if sess != "sess-1" || agent != "user" {
		t.Errorf("Tuple = (%q,%q), want (sess-1,user)", sess, agent)
	}
}

func TestAgentAddress_EmptyAuthorityDefaults(t *testing.T) {
	addr := gomsg.AgentAddress("", "s", "a")
	if addr.Authority != gomsg.DefaultAuthority {
		t.Errorf("Authority = %q, want %q", addr.Authority, gomsg.DefaultAuthority)
	}
}

func TestTuple_RejectsNonAddressableKinds(t *testing.T) {
	svc := messaging.Address{Kind: messaging.KindService, Authority: "x", ID: "y"}
	if _, _, err := gomsg.Tuple(svc); err == nil {
		t.Error("expected error for service-kind address")
	}
}

func TestIsLocal(t *testing.T) {
	local := gomsg.AgentAddress("nanite.local", "s", "a")
	foreign := gomsg.AgentAddress("peer.example", "s", "a")

	if !gomsg.IsLocal(local, "nanite.local") {
		t.Error("local address not recognized as local")
	}
	if gomsg.IsLocal(foreign, "nanite.local") {
		t.Error("foreign address misclassified as local")
	}
	// Empty local authority defaults to DefaultAuthority.
	if !gomsg.IsLocal(local, "") {
		t.Error("default-authority address not local under empty local")
	}
}
