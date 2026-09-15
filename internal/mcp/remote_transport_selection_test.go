package mcp

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// AddRemoteServerFromConfig is the one place a stored transport_type turns
// into a transport. Both registration paths — startup and the API — go through
// it, so what it picks is what "sse" and "streamable" actually mean.
func TestAddRemoteServerFromConfigPicksTheTransportTheNameClaims(t *testing.T) {
	tests := []struct {
		transportType string
		want          string // %T of the registered transport
	}{
		{store.TransportSSE, "*mcp.SSETransport"},
		{store.TransportStreamable, "*mcp.HTTPTransport"},
	}

	for _, tt := range tests {
		t.Run(tt.transportType, func(t *testing.T) {
			m := NewManager()
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
			transport := m.servers["probe"]
			m.mu.RUnlock()
			if got := fmt.Sprintf("%T", transport); got != tt.want {
				t.Fatalf("transport_type %q registered a %s, want %s", tt.transportType, got, tt.want)
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

// The tier ceiling reaches an SSE server the same way it reaches every other
// remote one — through AddServer's SetMaxResponseBytes assertion.
func TestAddSSEServerFromConfigAppliesTheTierCap(t *testing.T) {
	m := NewManager()
	if err := m.AddSSEServerFromConfig("probe", "http://gateway.invalid/sse", "", TierThirdPartyHTTP); err != nil {
		t.Fatalf("AddSSEServerFromConfig: %v", err)
	}

	m.mu.RLock()
	transport := m.servers["probe"]
	m.mu.RUnlock()

	sse, ok := transport.(*SSETransport)
	if !ok {
		t.Fatalf("registered transport is a %T", transport)
	}
	want := int64(LimitsFor(TierThirdPartyHTTP).MaxResultBytes)
	if got := sse.effectiveMaxResponseBytes(); got != want {
		t.Fatalf("effective cap = %d, want the tier ceiling %d", got, want)
	}
}

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
