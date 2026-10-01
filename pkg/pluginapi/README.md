# Nanite plugin API

An independently versioned public Go module for subprocess plugin authors:

```sh
go get github.com/hollis-labs/nanite/pkg/pluginapi
```

`Block` is the `nanite` object in a shared `plugin-sdk/manifest.Manifest`.
`EncodeBlock` validates it and produces the raw JSON value assigned to
`Manifest.Nanite`. Declare `pluginapi.Version` as the minimum `hosts.nanite`
contract version. `DecodeBlock` uses the SDK's strict decoder; the host then
checks runtime compatibility, available slots, registration conflicts and
filesystem confinement before loading anything.

UI registrations name exports of `UI.Bundle`. Drawer slots use
`SlotPrimaryDrawer` and `SlotWorkingDrawer`; panels and envelopes also name
component exports. Command, event, CRUD and plugin-relative HTTP route
registrations are declarative. The host owns registration conflicts and unload
cleanup. Common config, secrets, capabilities and agent tools live in the
shared manifest rather than this extension.

`Client` forwards a `ToolCall` to an explicitly configured literal loopback
HTTP(S) address at `/api/tools/call`, preserving session scope and MCP content
blocks. It refuses redirects, disables environment proxies, bounds responses,
respects context cancellation and never retries an uncertain action. A tool
error is returned as `ToolResult.IsError`; transport errors are Go errors.

This tool-call API executes as the desktop user. Session IDs are scope, not
credentials. The helper does not grant permissions or provide the read-only
host query surface. A plugin must still follow its host's approval rules.

Run `GOWORK=off go vet ./...` and `GOWORK=off go test -race -count=20 ./...`
from this directory. Tags for this nested module use `pkg/pluginapi/vX.Y.Z`.
The application root's `go test ./...` does not traverse a nested Go module.
