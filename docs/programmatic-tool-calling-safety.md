# Programmatic Tool Calling — Safety Reference

**Ticket:** CW-20260420-0019 (D6 — last D-track ticket of Phase 5)
**Status:** Landed on `feat/arch-seq-phase-5-broker-strategy`
**Implementation:** `internal/mcp/self_tools_python.go`, `internal/mcp/self_tools_python_test.go`

---

## Overview

`nanite_run_python` lets a Worker or Planner role agent execute a Python
script in an isolated subprocess sandbox. From inside the script, the model
can call Nanite tools via a `tool_call(name, args)` helper. Every such call
round-trips through Go before returning, traversing the same permission engine
and chat-surface enforcement as a direct tool call.

The value proposition: instead of N discrete tool calls separated by LLM
turns (expensive, slow), the model writes a short Python loop that performs
all N calls with logic between them and returns a single structured result.

Typical use cases:

- **Pagination loops** — iterate `offset` until a page is short.
- **Multi-entity lookup-and-join** — list, then fetch detail for each item.
- **Numeric aggregation** — sum/mean/filter over tool-call result data.

---

## Sandbox isolation guarantees

### What IS guaranteed

| Property | Mechanism |
|---|---|
| Isolated Python env | `-IBES` flags: no PYTHONPATH, no site-packages, no PYTHON* env vars |
| Process-group kill | `Setpgid = true` + SIGTERM → SIGKILL cascade on timeout (unix) |
| Stdout capture limit | 256 KiB hard cap per stream; excess silently discarded |
| No filesystem write | Python inherits no writable FDs; model code has no path to write host files |
| Tool-call auth | Every `tool_call()` from inside the sandbox traverses `permission.Engine.Check()` before dispatch |
| Traceback truncation | Python errors are capped at 20 lines before being returned to the LLM |
| Code not in argv | Model code arrives via the stdin envelope's `"code"` field, not as an arg visible in `ps` |

### What is NOT guaranteed (explicit non-guarantees)

This is a **subprocess sandbox**, not a hardware-isolated microVM. It provides
defense-in-depth, not a hard isolation boundary. Specifically:

- **Not a hard isolation boundary.** A sufficiently privileged exploit in
  CPython itself could escape. If the deployment threat model requires stronger
  isolation, file a follow-up to evaluate Pyodide/wazero/Firecracker (see
  [Follow-ups](#follow-ups)).
- **Network block is best-effort.** The preamble monkey-patches
  `socket.socket.__init__` to raise `PermissionError`. This stops naive socket
  creation but does not prevent a `ctypes`-based raw syscall or a native C
  extension that bypasses the Python socket module.
  **Correction (2026-08-22,
  `TASKS/audit-remediation/02-linux-sandbox-fail-open/02-macos-seatbelt-read-boundary-disclosure.md`):**
  `nanite_run_python`'s subprocess (`internal/selftools/self_tools_python.go`'s
  `RunPythonSandbox`) is spawned via a direct `exec.CommandContext` call with
  only `Setpgid` applied (`applySandboxSysProcAttr`) — it does **not** route
  through `sandbox.AgentExec`/`applyOSSandbox`, so neither the macOS seatbelt
  profile nor Linux bwrap wraps this process today. The import-level
  monkey-patch above is, currently, the *only* network defense for
  `python_run` on every platform. (The OS-level sandbox — seatbelt on macOS,
  bwrap on Linux — does apply to `sandbox.AgentExec`/`UserExec` callers, e.g.
  shell tools; see `docs/hardening-phase-plan.md`'s Tier 2 section for what
  that boundary does and doesn't cover.)
- **CPU time cap is RLIMIT_CPU (CPU time, not wall-clock).** A tight spin loop
  is caught; a process blocked in I/O against its own socket is not CPU-billed.
  The Go-side wall-clock `context.WithTimeout` bounds total execution time
  regardless.
- **Memory cap via RLIMIT_AS may not be supported on all kernels.** The
  preamble silently ignores `setrlimit` failures on `RLIMIT_AS`. If the host
  kernel does not support address-space limits (rare), memory is uncapped.
- **Windows: rlimits are not applied.** The `resource` module does not exist on
  Windows. Caps are skipped and documented as a Windows limitation.
- **No import whitelist.** The model can `import` any stdlib module (math,
  json, itertools, etc.). There is no explicit whitelist; the network block and
  the lack of useful FDs/paths are the primary constraints.

---

## Permission engine pass-through

Every `tool_call(name, args)` from inside the Python sandbox invokes
`permission.Engine.Check()` before the call is dispatched. The check uses
the session ID of the calling agent (passed into `RunPythonSandbox`) so
session grants apply correctly.

Denial produces a `RuntimeError` inside the sandbox (the model's code must
catch it or the script errors out). The denial is recorded in the
`tool_calls` log returned to the LLM with `status: "denied"`.

The permission engine instance is wired via `SelfToolsTransport.PythonPermChecker`.
In tests, `allowAllPermChecker` and `denyAllPermChecker` stubs are used.
In production, this should be the same `*permission.Engine` instance used by
the rest of the chat loop.

**No tool can be called from the sandbox that the calling agent's permission
rules would deny for a direct call.** The sandbox does not have elevated rights.

---

## Resource caps

| Cap | Default | Absolute max | Override field |
|---|---|---|---|
| CPU / wall-clock time | 10 s | 60 s | `time_limit_seconds` |
| Memory (RLIMIT_AS) | 256 MB | 1024 MB | `memory_limit_mb` |

The harness clamps caller-provided values to the absolute maximums regardless
of what the model requests. The wall-clock deadline is set by the Go-side
`context.WithTimeout` (cap + 500ms buffer).

---

## Transport architecture: dual-FD JSON-RPC

```
Go parent                            Python subprocess
   |                                       |
   |   stdin: envelope JSON (one line)     |
   |-------------------------------------->|
   |                                       |  preamble runs; installs rlimits
   |                                       |  exposes tool_call() helper
   |                                       |  exec(model_code, ns)
   |                                       |
   |  FD3 (req pipe, py→go):              |
   |<---------- {"id":1,"name":...} -------|  tool_call() called
   |                                       |
   |  permission check + dispatch          |
   |                                       |
   |  FD4 (resp pipe, go→py):             |
   |---------- {"id":1,"result":...} ----->|  tool_call() returns dict
   |                                       |
   |  ... (repeated per tool call) ...     |
   |                                       |
   |  FD3 closes (Python exits)            |
   |<--------------------------------------|
   |  FD4 write-end closed (pump exits)   |
   |                                       |
   |  stdout: final result JSON            |
   |<------|{"result":...,"stdout":...}----|
```

Rationale for dual-FD over stdin/stdout multiplexing: stdout is reserved for
the final result (one JSON line after model code runs) and for user `print()`
output (captured into the `stdout` field of the result). Mixing tool-call
requests with these on the same stream would require a protocol discriminator
and complicate both sides.

The sync/blocking model (Python blocks on `_resp_fd.readline()` during a tool
call) is intentional. Async tool calls would require `asyncio` plumbing with
no v1 benefit.

---

## Failure modes

| Failure | Behaviour |
|---|---|
| `python3` not on PATH | Tool returns `errorResult` immediately; no subprocess spawned |
| Timeout (wall-clock) | Go sends SIGTERM to process group, then SIGKILL backstop; `error` field set to `"execution timed out after Ns"` |
| Syntax error in model code | Python catches it in `except Exception`; `error` field contains first 20 traceback lines |
| Runtime exception in model code | Same as syntax error |
| Tool call denied | `RuntimeError` raised in Python; error propagates through model code unless caught; `tool_calls` log shows `status: "denied"` |
| Tool call error (dispatcher) | `RuntimeError` raised in Python; `tool_calls` log shows `status: "error"` |
| Bad tool name (unknown) | Dispatcher returns `fmt.Errorf("no handler for tool %q")`; surfaces as `RuntimeError` inside Python |
| stdout larger than 256 KiB | Silently truncated at Go capture limit |

---

## Chat surface restriction

`nanite_run_python` is **NOT** on the Chat agent's static surface
(`dispatch.ChatToolSurface`). The tool is in `selfToolDefinitions()` so it
appears for Worker/Planner role agents, but `EnforceChatSurface` filters it
out because its name does not match any prefix in `ChatToolSurface`.

The dispatch role test (`internal/dispatch/role_test.go`) asserts this:
`nanite_run_python` is not added to the Chat allow-list, and the mcp package
test `TestSelfToolsTransport_RunPython_NotOnChatSurface` verifies it directly.

The Chat agent dispatches work via `nanite_execute_task` (the high-level
primitive); it does not execute code directly.

---

## Python binary dependency

`python3` must be available on `PATH` in the execution environment, version
≥ 3.10. The `resource` module (part of the standard library on POSIX) is
used for rlimit caps; `json`, `sys`, `os`, `io`, `builtins`, `socket`,
`traceback` are used by the preamble (all stdlib, always present).

**Deploy-side TODO:** Verify that the Cerberus deploy environment provides
`python3 ≥ 3.10` on PATH for the `nanite-api` service. If it does not, add
it to the deployment manifest or document it as a host requirement in the
runbook.

---

## Follow-ups

The following are explicitly deferred from this ticket:

1. **Pyodide / wazero / Firecracker (hard isolation)** — evaluate for future
   hardening when the eval harness exists to measure quality impact. Subprocess
   sandbox is defense-in-depth for v1; these options are strictly better
   long-term.
2. **Eval harness baseline comparison** — measure quality improvement of PTC
   vs. non-PTC baseline. Venue: eval ticket 7.1 (not yet created).
3. **Rich proxy helpers** (`tools.some_tool(...)` stubs instead of bare
   `tool_call()`) — deferred pending usage patterns; bare helper is sufficient
   for v1.
4. **Cross-language support (JavaScript, shell)** — Python is the only target
   for this ticket. Other languages deferred.
5. **Windows rlimit support** — `resource` module unavailable on Windows;
   caps are skipped. Documented as a known limitation.
6. **Import whitelist** — no explicit stdlib allowlist; network block + FD
   constraints are the primary guards. A whitelist would be more restrictive
   but also more brittle. Deferred.

---

## Known limitations (preserved)

- Subprocess sandbox is defense-in-depth, not a hard boundary.
- Network block is import-level only, not kernel-level — `python_run` does
  not currently route through the OS-level sandbox (seatbelt/bwrap) at all;
  see the "Network block is best-effort" correction above.
- `RLIMIT_AS` may be silently ignored on some kernels.
- Windows: no rlimit caps.
- No import whitelist.
- Model code has no access to the host filesystem via inherited FDs, but it
  can try to `open()` any path the OS user can read. **Correction
  (2026-08-22):** since `python_run` does not route through the OS-level
  sandbox, nothing today restricts these reads at the process level — this
  is a gap in the Python-only defenses, not "the OS sandbox blocks most of
  these." Where `sandbox.AgentExec`/`UserExec` *are* used elsewhere in
  Nanite, note macOS's seatbelt profile also does not restrict reads by
  design (`docs/hardening-phase-plan.md`'s Tier 2 section) — only Linux's
  narrowed `bwrap --ro-bind` set does.
