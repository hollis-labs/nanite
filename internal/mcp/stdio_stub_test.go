package mcp

import "testing"

// stubMCPServerScript completes the MCP initialize handshake and then blocks,
// holding the process open without consuming further input.
const stubMCPServerScript = `
IFS= read -r initialize
printf '%s\n' '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"stub","version":"0"}}}'
IFS= read -r initialized
while :; do sleep 300; done
`

// newHandshakingStubTransport returns a transport over a shell that answers
// the handshake and then stays alive.
//
// Tests that need a *running* transport need this rather than a bare `sleep`:
// start() performs the handshake, so a command that never answers initialize
// is correctly rejected and leaves no started subprocess to exercise. Use
// startSleepTransport for the opposite case — modeling a server that hangs.
//
// Passed via `sh -c` rather than written to a temp file, which keeps the
// helper free of file-permission and path-traversal concerns for something
// that only ever runs in-process.
func newHandshakingStubTransport(t *testing.T) *StdioTransport {
	t.Helper()
	return NewStdioTransport("sh", []string{"-c", stubMCPServerScript}, nil, []string{"PATH"})
}
