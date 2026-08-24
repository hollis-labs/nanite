# Plugin SDK Reference

API reference for `github.com/hollis-labs/plugin-sdk` **v0.3.0** — the
universal SDK both Nanite and its plugins depend on.

For the walkthrough, see [plugin-authoring-guide.md](./plugin-authoring-guide.md).
For the manifest, see [plugin-yaml-reference.md](./plugin-yaml-reference.md).

## Installation

```bash
go get github.com/hollis-labs/plugin-sdk@v0.3.0
```

The SDK is an external Go module with zero dependencies on Nanite
internals. Plugins import it as `github.com/hollis-labs/plugin-sdk` and
the subprocess helpers as `github.com/hollis-labs/plugin-sdk/subprocess`.

## Module layout

```
github.com/hollis-labs/plugin-sdk
├── plugin.go          Plugin, Host, CRUDHandler, EventHook, ...
├── errors.go          Error type, sentinels, constructors
├── envelope.go        EnvelopeOut, MessageOut
├── logger.go          Logger interface
└── subprocess/
    ├── protocol.go    JSON-RPC 2.0 wire types, method + error codes
    ├── types.go       Wire types (InitParams, LoadResult, ...)
    ├── types_sdk.go   Friendly SDK types (CommandRequest/Result, ...)
    ├── server.go      Serve(Plugin) entry point
    ├── config.go      ConfigReader
    ├── data.go        DataHelper, CacheHelper
    ├── log.go         stderr JSON-lines logger
    └── subprocesstest/
        └── harness.go test harness for driving plugins without spawn
```

## Top-level package (`plugin`)

### `Plugin` (builtin-plugin interface)

The in-process plugin contract used by builtin plugins. Subprocess plugins
implement `subprocess.Plugin` instead (see below).

```go
type Plugin interface {
    ID() string
    Name() string
    Version() string
    Description() string
    Dependencies() []string
    Load(host Host) error
    Unload() error
    Status() PluginStatus
}
```

### `Host`

The runtime surface a builtin plugin uses. Subprocess plugins do **not**
receive a `Host` — their surface is the JSON-RPC stdio channel plus the
`InitParams` / `Config` / `DataDir` / `CacheDir` fields delivered at init.

```go
type Host interface {
    GetPlugin(id string) (Plugin, bool)
    RegisterCRUDHandler(resourceType string, handler CRUDHandler) error
    RegisterEventHook(eventTypes []string, hook EventHook) error
    RegisterUIComponent(component UIComponent) error
    GetService(name string) (interface{}, error)
    GetConfig(key string) (string, error)
    SetConfig(key, value string) error
    RegisterConfigSchema(fields []ConfigFieldDef) error
    RegisterConnector(name string, connector Connector) error
    RegisterProvider(name string, provider interface{}) error
    RegisterCLIAdapter(name string, adapter interface{}) error
    Logger() Logger
    Context() context.Context
}
```

### `EventHook`

```go
type EventHook interface {
    Handle(ctx context.Context, event Event) error
    EventTypes() []string
    PluginID() string
}
```

Post-Track-C event hooks must report `PluginID()` so the host can unregister
all hooks owned by a plugin during unload. Pre-audit hooks without
`PluginID()` leaked; any new `EventHook` must implement it.

### `Event`

```go
type Event struct {
    Type      string
    Source    string
    Timestamp time.Time
    Data      map[string]interface{}
    SessionID string
}
```

Returning `ErrCanceled` from a pre-hook (e.g. `message.sending`) cancels
the pending action.

### `CRUDHandler`

Generic Create/Read/Update/Delete/List over host-owned resources. Most
plugins will not implement this directly — prefer declarative
`registers.crud` in the manifest when possible.

### `UIComponent`, `Connector`, `Installable`, `Uninstallable`

Optional capability types. See `plugin.go` for full definitions.

### `EnvelopeOut` / `MessageOut`

The wire type a plugin returns when it wants to emit a rendered envelope:

```go
type EnvelopeOut struct {
    Type string                 `json:"type"`
    Data map[string]interface{} `json:"data"`
}
```

Populate `Type` with the `registers.envelopes[].type` value from
`plugin.yaml`. The host validates `Data` against the matching
`envelopes/*.schema.json` at emission time. In production mode, envelopes
that fail validation are dropped; in developer mode they pass through with
a warning marker.

### `Error`

Typed plugin errors with HTTP-friendly status codes and stable error codes.
Used by both builtin and subprocess surfaces. See `errors.go` for the full
constructor list.

### `Logger`

```go
type Logger interface {
    Debug(msg string, kv ...interface{})
    Info(msg string, kv ...interface{})
    Warn(msg string, kv ...interface{})
    Error(msg string, kv ...interface{})
    With(kv ...interface{}) Logger
}
```

In subprocess plugins, use `subprocess.Log()` to retrieve the package-level
logger, which writes newline-delimited JSON to stderr with automatic secret
redaction (values passed as `InitParams.Config` entries marked
`type: secret` in the manifest are tracked and redacted from log output).

## Subprocess package (`plugin-sdk/subprocess`)

The subprocess package is what plugin authors touch most often.

### Plugin contract

```go
type Plugin interface {
    Init(ctx context.Context, params InitParams) (InitResult, error)
    Load(ctx context.Context) (LoadResult, error)
    Unload(ctx context.Context) error
}
```

Every subprocess plugin implements these three methods. The identity
fields (`ID`, `Name`, `Version`, `Description`, `Protocol`) are returned by
`Init`; the manifest is still authoritative, and the host compares what
`Init` returns against `plugin.yaml` at install time.

### Capability interfaces

Implemented on top of `Plugin` via type assertion — declare only the ones
you need:

| Interface | Method | Dispatched by |
|---|---|---|
| `CommandHandler` | `Command(ctx, CommandRequest) (CommandResult, error)` | `command/execute` |
| `EventHandler` | `EventHandle(ctx, EventRequest) (EventResult, error)` | `event/handle` |
| `CRUDHandler` | `Create / Read / Update / Delete / List` | `crud/*` |
| `HealthChecker` | `Health(ctx) (HealthStatus, error)` | `plugin/health` |
| `Migrator` | `Migrate(ctx, from, to string) error` | `plugin/migrate` |
| `MCPHandler` | `MCPCallTool(ctx, MCPCallRequest) (MCPCallResult, error)` | `mcp/call_tool` |
| `HTTPHandler` | `HTTPHandle(ctx, HTTPRequest) (HTTPResponse, error)` | `http/handle` |

### Entry point

```go
func Serve(p Plugin) error
```

Runs the JSON-RPC 2.0 server loop on stdin/stdout. Installs SIGTERM/SIGINT
handlers, dispatches requests in per-request goroutines with panic
recovery, and calls `Unload` on EOF or shutdown signal. A typical `main`
is:

```go
func main() {
    if err := subprocess.Serve(&myPlugin{}); err != nil {
        os.Exit(1)
    }
}
```

### Init / Load / Unload types

```go
type InitParams struct {
    PluginDir string            // plugin install dir
    DataDir   string            // persistent per-plugin data root
    CacheDir  string            // ephemeral per-plugin cache root
    Config    map[string]string // resolved config values
    LogLevel  string            // "debug" | "info" | "warn" | "error"
    HostInfo  HostInfo
}

type HostInfo struct {
    Version  string
    Protocol int
}

type InitResult struct {
    ID, Name, Version, Description string
    Protocol int
}

type LoadResult struct {
    SkippedRegistrations []SkippedRegistration
}

type SkippedRegistration struct {
    Kind   string // "command", "slot", "component", "keybinding", "event", "crud", "mcp_server"
    ID     string
    Reason string
}
```

`InitParams.ResolvedDataDir()` returns the host-provided `DataDir` or the
sentinel `ErrNoDataDir` — plugins should treat that error as fatal for
persistence paths. `InitParams.ResolvedCacheDir()` falls back to
`os.TempDir()` if the host did not supply one.

### Command / Event request shapes

```go
type CommandRequest struct {
    Name      string
    SessionID string
    Args      string
}

type CommandResult struct {
    Action    string                // "message", "noop", "error"
    Content   string
    Envelopes []plugin.EnvelopeOut
}

type EventRequest struct {
    Type      string
    Source    string
    Data      map[string]interface{}
    SessionID string
    PreHook   bool
}

type EventResult struct {
    Cancel    bool // true to cancel a pre-hook action
    Reason    string
    Envelopes []plugin.EnvelopeOut
}
```

### MCP integration

```go
type MCPCallRequest struct {
    ToolName  string
    Arguments map[string]interface{}
    SessionID string
}

type MCPCallResult struct {
    Content   json.RawMessage
    IsError   bool
    Envelopes []plugin.EnvelopeOut
}
```

Tool registration is declarative in `plugin.yaml registers.mcp_servers[].tools`.
The `mcp/list_tools` method is served automatically from the manifest.

**Manual `mcp/list_tools` handling** (advanced, rare): plugins that
dynamically change their tool catalog at runtime can intercept
`mcp/list_tools` by type-asserting a custom method; in practice this is
only needed by plugins whose tool set depends on runtime config. Most
plugins should keep yaml as the source of truth and let the host answer
`mcp/list_tools` directly (see BLG-20260414-012 for the pattern and
caveats).

### HTTP route handling

```go
type HTTPRequest struct {
    Method, Path string
    Query, Headers map[string]string
    Body   []byte
    SessionID string
}

type HTTPResponse struct {
    Status  int
    Headers map[string]string
    Body    []byte
}
```

Streaming is not supported on the `http/handle` path; for streaming output
emit SSE envelopes from `EventHandleResult` or `CommandResult`.

### Migration

```go
type Migrator interface {
    Migrate(ctx context.Context, from, to string) error
}
```

The host calls `plugin/migrate` before `plugin/load` when the installed
manifest version differs from the declared version. Return `nil` for a
no-op migration; plugins that don't implement `Migrator` simply return
`ErrCodeMethodNotFound` and the host skips the step.

### Helpers

- `subprocess.NewDataHelper(dir)` / `subprocess.NewCacheHelper(dir)` —
  wrap a directory as a `DataHelper` / `CacheHelper` with `DataPath`,
  `CachePath`, and `EnsureDataDir` / `EnsureCacheDir` accessors.
- `subprocess.NewConfigReader(values)` — wraps `map[string]string` as a
  `ConfigReader` with typed accessors (`String`, `Bool`, `Int`, `Secret`,
  `Required`, `Has`). Secret values registered via the reader are auto-
  redacted from the stderr logger.
- `subprocess.Log()` — package-level logger (stderr, newline-delimited
  JSON).

## Wire protocol

Over stdio: newline-delimited JSON-RPC 2.0 messages. The host writes
requests to the plugin's stdin and reads responses from stdout.

### Method constants

```
plugin/init          plugin/load          plugin/unload        plugin/health
plugin/migrate       command/execute      event/handle
crud/create          crud/read            crud/update          crud/delete   crud/list
mcp/list_tools       mcp/call_tool        http/handle
```

### Error codes

Standard JSON-RPC: `-32700` parse, `-32600` invalid request,
`-32601` method not found, `-32602` invalid params, `-32603` internal.

Application: `-32000` not found, `-32001` conflict, `-32002` validation,
`-32003` canceled (pre-hook cancellation).

### Protocol version

`subprocess.ProtocolVersion = 1`. Host and plugin must match exactly at
init time.

## Testing without spawning

The `subprocess/subprocesstest` package provides an in-process harness
that drives the plugin directly (no subprocess fork, no JSON):

```go
import "github.com/hollis-labs/plugin-sdk/subprocess/subprocesstest"

func TestMyPlugin_Command(t *testing.T) {
    h := subprocesstest.New(&myPlugin{},
        subprocesstest.WithConfig(map[string]string{"api_key": "test"}),
    )
    defer h.Close()

    ctx := context.Background()
    h.Init(ctx)
    h.Load(ctx)

    result, err := h.Command(ctx, "my-cmd", "args", "session-id")
    require.NoError(t, err)
    require.Equal(t, "message", result.Action)
    require.Len(t, result.Envelopes, 1)
}
```

For wire-format validation, set `NANITE_PLUGIN_SDK_JSON_ROUNDTRIP=1` (or
pass `WithJSONRoundtrip(true)`) — every request and response is forced
through marshal + unmarshal to catch wire issues.

## Version compatibility

- **v0.3.0** — `MCPHandler` and `HTTPHandler` are now dispatched by
  `Serve` (the wire types existed in v0.2.0 but weren't routed). `Migrator`
  is dispatchable.
- **v0.2.0** — `LoadResult` stripped of the old declarative fields. Use
  `plugin.yaml` as the source of truth; use `SkippedRegistrations` to
  decline at runtime.
- **v0.1.2** — `InitParams.DataDir` / `CacheDir` / `LogLevel` added with
  `Resolved*` helpers.

Plugins should pin a specific minor version in `go.mod` and advance
deliberately — breaking changes below `v1.0.0` are possible.
