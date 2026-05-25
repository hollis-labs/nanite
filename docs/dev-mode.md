# Dev-Mode Build Tag

**Scope:** G5 + G6 (CW-20260419-0001, CW-20260420-0017)
**Gate mechanism:** `//go:build devmode` compile-time tag
**Implemented:** 2026-04-27

---

## What dev-mode is

`devmode` is a **compile-time build tag**, not a runtime setting. Code compiled without
`-tags devmode` contains zero dev-only symbols, types, or tool strings. There is no
flag to flip at runtime — the gate is enforced by the Go toolchain at build time.

This is distinct from the *Developer Mode* user setting (which gates `dev_*` tools at
runtime). Those are two independent mechanisms:

| Mechanism | Layer | Controls |
|---|---|---|
| `//go:build devmode` build tag | Compile-time | Whether mux_* tools exist in the binary at all |
| `developer_mode` user setting | Runtime | Whether `dev_*` MCP tools are visible to the LLM |

See `docs/developer-mode-gate.md` for the runtime `developer_mode` gate.

---

## Building dev-mode

```bash
# Canonical dev build (also generates envelopes and rebuilds UI)
make build-dev

# Minimal — skip envelope generation and UI (fast iteration)
go build -tags devmode -o nanite ./cmd/nanite

# Run tests with the devmode tag
go test -tags devmode ./...

# Vet with devmode tag
go vet -tags devmode ./...
```

The `build-dev` Makefile target sets `-tags devmode` and outputs the binary to `./nanite`.
The production `make build` target explicitly must NOT use `-tags devmode` — the Makefile
comment at line 4 documents this invariant.

---

## Production builds explicitly do NOT include mux tools

**Invariant:** the production binary (`make build` / `go build ./cmd/nanite`) contains
zero occurrences of `mux_list_launches`, `mux_launch`, `mux_send`, or `mux_stop`.

Every file in `internal/muxproxy/` that defines real behaviour carries `//go:build devmode`
at the top. Non-devmode stub files (`*_notdev.go`) expose only no-op shims and empty
type aliases so the rest of the binary compiles. The builtin mux-orchestrator agent
profile is likewise absent from the production binary (see
`internal/agent/builtin/embed_mux_notdev.go`).

Verification:

```bash
# Build production binary
go build -o /tmp/nanite-prod ./cmd/nanite

# Expect: 0
strings /tmp/nanite-prod | grep -cE "mux_(list_launches|launch|send|stop)"

# Build dev binary
go build -tags devmode -o /tmp/nanite-dev ./cmd/nanite

# Expect: ≥4
strings /tmp/nanite-dev | grep -cE "mux_(list_launches|launch|send|stop)"
```

This check is part of the G5/G6 acceptance suite and should be re-run after any change
to `internal/muxproxy/` or `cmd/nanite/mux_wiring_devmode.go`.

---

## Mux orchestrator (dev-mode only)

The **mux orchestrator** is a built-in chat agent profile that lets a front (chat)
session spawn and coordinate N subordinate CLI agents running under
[Tether](https://github.com/hollis-labs/go-tether-client).

It is compiled in only when `-tags devmode` is set. Default users (production binary)
see no change — the profile, transport, and all four tool strings are absent.

### When to use it

Use the mux orchestrator when you want a single chat session to:

- Spin up multiple parallel CLI agents (e.g. one researcher, one coder)
- Route work to each subordinate independently
- Watch subordinate output inline via chat SSE (`subordinate_delta`, `subordinate_tool_use`,
  `subordinate_done` event types)

This is a POC. Production promotion (config-schema endpoint, per-chat cleanup hooks,
profile-based tool allowlist) is tracked in CW-20260421-0001.

### The four mux_* tools

| Tool | Description |
|---|---|
| `mux_list_launches` | List available tether launches (subordinate templates). |
| `mux_launch` | Start a subordinate claudestream agent from a launch ID. Returns `session_id`. |
| `mux_send` | Send text to a subordinate and block until it finishes the turn. Returns transcript + tool_uses. |
| `mux_stop` | Stop a subordinate session. Optional — cleanup runs on chat-session exit. |

### Trust gating (H1)

The mux-orchestrator agent profile is seeded as `trusted` in every workspace by
migration `036_role_trust_seed_dogfood.sql`. This means an orchestrator session invokes
`mux_*` tools without approval prompts.

At runtime, `internal/muxproxy/transport.go` enforces the H1 trust gate: when a
`TrustResolver` is wired, callers with `TrustUntrusted` tier are denied with an error.
Normal-tier callers proceed (subject to the standard approval flow). Trusted callers
proceed silently.

The resolver reads `workspace_role_trust` at call time — no per-session caching.
Toggling a role's trust tier in the DB takes effect on the next `mux_*` call.

Denial events are logged at `slog.Warn` level (`muxproxy: trust gate denied`).

### Lifecycle logs

The muxproxy package emits structured slog entries at key lifecycle events:

| Event | Level | Key fields |
|---|---|---|
| Subordinate registered (attach goroutine started) | Info | `chat_session`, `subordinate_session`, `nickname` |
| `mux_launch` called | Info | `launch_id`, `nickname` |
| Subordinate launched (daemon confirmed) | Info | `session_id`, `nickname` |
| `mux_send` dispatched | Debug | `session_id`, `text_len` |
| `mux_send` completed | Debug | `session_id`, `exit_status`, `input_tokens`, `output_tokens` |
| `mux_stop` called | Info | `session_id` |
| Subordinate stopped | Info | `session_id` |
| Subordinate unregistered | Info | `session_id`, `nickname` |
| Attach stream ended | Debug | `session`, `err` |
| Attach scan error | Debug | `session`, `err` |
| Waiter channel full (event dropped) | Debug | `session`, `kind` |
| Trust gate denied | Warn | `tool`, `workspace`, `agent_profile` |

Run with `NANITE_LOG_LEVEL=debug` (or equivalent slog handler configuration) to see
Debug-level events.

### Wiring in cmd/nanite

```
cmd/nanite/mux_wiring_devmode.go   — Transport + Manager construction, MCP server registration
cmd/nanite/mux_wiring_notdev.go    — no-op stub (production builds)
cmd/nanite/main.go:503             — call site (registerMuxTransport)
```

The mux-orchestrator MCP server is registered under the name `"mux-orchestrator"` at
`mcp.TierBuiltin`. The built-in tool broker picks up the four tool definitions from
`muxproxy.ToolDefinitions()` under the same name.
