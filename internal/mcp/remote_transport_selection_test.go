package mcp

import (
	"strings"
	"testing"

	gmcpclient "github.com/hollis-labs/go-mcp/client"

	"github.com/hollis-labs/nanite/internal/store"
)

// AddRemoteServerFromConfig is the one place a stored transport_type turns
// into a transport. Both registration paths — startup and the API — go through
// it, so what it picks is what "sse" and "streamable" actually mean.
//
// Both kinds now register the same Go type (*remoteTransport) — the Pool
// already owns per-transport dialing uniformly — so what distinguishes them
// is the underlying gmcpclient.ServerConfig.Transport kind the pool actually
// dialed with, read back via Pool.Get.
func TestAddRemoteServerFromConfigPicksTheTransportTheNameClaims(t *testing.T) {
	tests := []struct {
		transportType string
		want          string // gmcpclient.Transport* kind the pool dialed with
	}{
		{store.TransportSSE, gmcpclient.TransportSSE},
		{store.TransportStreamable, gmcpclient.TransportHTTP},
	}

	for _, tt := range tests {
		t.Run(tt.transportType, func(t *testing.T) {
			m := NewManager()
			t.Cleanup(m.Close)
			err := m.AddRemoteServerFromConfig(
				"probe", tt.transportType,
				"http://gateway.invalid/servers/x/"+tt.transportType,
				`{"Authorization":"Bearer probe"}`,
				TierThirdPartyHTTP,
			)
			if err != nil {
				t.Fatalf("AddRemoteServerFromConfig: %v", err)
			}

			m.mu.RLock()
			transport, ok := m.servers["probe"].(*remoteTransport)
			m.mu.RUnlock()
			if !ok {
				t.Fatalf("transport_type %q registered a %T, want *remoteTransport", tt.transportType, m.servers["probe"])
			}
			if transport.kind != tt.want {
				t.Fatalf("transport_type %q dialed kind %q, want %q", tt.transportType, transport.kind, tt.want)
			}
		})
	}
}

// An unknown transport type must be an error, not a silent fallback: a server
// nobody can register is visible, one registered against the wrong protocol is
// not.
func TestAddRemoteServerFromConfigRejectsAnUnknownTransport(t *testing.T) {
	m := NewManager()
	err := m.AddRemoteServerFromConfig("probe", "websocket", "http://gateway.invalid/x", "", TierThirdPartyHTTP)
	if err == nil {
		t.Fatal("AddRemoteServerFromConfig accepted an unknown transport type")
	}
	if !strings.Contains(err.Error(), "websocket") {
		t.Errorf("error does not name the offending value: %v", err)
	}
	if _, ok := m.servers["probe"]; ok {
		t.Error("a server was registered despite the error")
	}
}

// The tier ceiling reaching the underlying connection is covered end to end
// by TestAddSSEServerFromConfig_ResponseCapReachesTheConnection
// (transport_response_cap_test.go) — go-mcp/client's Client keeps its
// effective cap unexported, so a same-package field-level assertion like
// this one used to make isn't reachable across the package boundary
// anymore; the behavioral proof there is the equivalent coverage.

// Unusable headers must not cost the server its registration — it registers
// without them and fails loudly on the first request instead of vanishing from
// the tool surface. Same contract AddHTTPServerFromConfig has.
func TestAddSSEServerFromConfigRegistersDespiteUnusableHeaders(t *testing.T) {
	m := NewManager()
	if err := m.AddSSEServerFromConfig("probe", "http://gateway.invalid/sse", `{"Authorization": "Bearer oops`, TierThirdPartyHTTP); err != nil {
		t.Fatalf("AddSSEServerFromConfig: %v", err)
	}
	if _, ok := m.servers["probe"]; !ok {
		t.Fatal("server did not register")
	}
}
