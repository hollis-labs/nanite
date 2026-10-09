package mcpconfig

import (
	"errors"
	"net/url"
	"strings"

	"github.com/hollis-labs/nanite/internal/store"
)

const TransportTypeError = "transport_type must be 'stdio' or 'streamable'"

// ErrLegacySSE leaves the replacement endpoint to the server's operator:
// changing the transport discriminator alone cannot migrate its wire protocol.
var ErrLegacySSE = errors.New("legacy MCP SSE transport and /sse endpoints are unsupported; verify the server's Streamable HTTP endpoint and configure transport_type 'streamable' (type 'http' in .mcp.json); Nanite does not rewrite endpoints or fall back to SSE")

// ValidateTransport is shared by configuration writes and runtime registration.
// Existing legacy rows remain readable/exportable, but cannot be activated.
func ValidateTransport(transport, endpoint string) error {
	switch transport {
	case store.TransportStdio:
		return nil
	case store.TransportSSE:
		return ErrLegacySSE
	case store.TransportStreamable:
		u, err := url.Parse(endpoint)
		if err != nil {
			// url.Parse errors can contain credentials from the input URL.
			return errors.New("invalid MCP endpoint URL")
		}
		if strings.HasSuffix(strings.ToLower(strings.TrimRight(u.Path, "/")), "/sse") {
			return ErrLegacySSE
		}
		return nil
	default:
		return errors.New(TransportTypeError)
	}
}
