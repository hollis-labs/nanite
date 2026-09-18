package mcp

import (
	"context"
	"testing"
)

// Regression coverage for audit 2026-04-10-mcp-client-transport finding 02
// (unbounded response body → memory DoS) now lives in go-mcp/client's own
// test suite — the response-size cap is a package-level mechanism there
// (see WithMaxResponseBytes / Client.SetMaxResponseBytes), not something
// this repo reimplements per transport kind anymore. What's left here is a
// smoke check that Manager's own wiring — AddServer's SetMaxResponseBytes
// interface assertion reaching a registered remoteTransport, and that
// transport forwarding into the pool's connection — still works end to end.

// TestAddSSEServerFromConfig_ResponseCapReachesTheConnection proves the tier
// cap set at registration time actually bounds what the underlying
// go-mcp/client connection will accept, using the SSE fixture server shared
// with remote_transport_sse_test.go.
func TestAddSSEServerFromConfig_ResponseCapReachesTheConnection(t *testing.T) {
	ts := newSSETestServer(t)
	m := NewManager()
	t.Cleanup(m.Close)

	if err := m.AddSSEServerFromConfig("probe", ts.URL, "", TierThirdPartyHTTP); err != nil {
		t.Fatalf("AddSSEServerFromConfig: %v", err)
	}

	m.mu.RLock()
	transport, ok := m.servers["probe"].(*remoteTransport)
	m.mu.RUnlock()
	if !ok {
		t.Fatalf("registered transport is a %T, want *remoteTransport", m.servers["probe"])
	}

	// Override the tier default down to something no real handshake could
	// fit in, so the assertion doesn't depend on the tier's actual byte
	// value staying small — SetMaxResponseBytes is the same live seam
	// AddServer used to apply LimitsFor(tier).MaxResultBytes; this just
	// proves it reaches the connection at all.
	transport.SetMaxResponseBytes(8)

	if _, err := transport.ListTools(context.Background()); err == nil {
		t.Fatal("ListTools succeeded with an 8-byte cap — the cap never reached the connection")
	}
}
