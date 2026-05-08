# 06 · Permission & Authority

> **Scope:** the gate stack for tool execution. Path grants, profile permissions, lineage walk, `resolveAllowed`, sandbox redundancy, devmode gates, notify-pause. Post-Phase-3 these gates are the **only filter** between an LLM tool call and execution.
>
> **Related:** [02 Tool Invocation](02-tool-invocation-and-authority.md) (callsite), [05 External Agent Execution](05-external-agent-execution.md) (lineage), [03 SSE](03-sse-envelope-and-interaction-protocol.md) (`notify_pause`, `approval_request`).

## Purpose

Decide, for a given (session, agent, tool, args) tuple, whether the tool should run. Provide the path-grant primitive that lets agents work with paths the user mentioned, without granting them the whole filesystem.

## Key files

- `internal/permission/path_grants.go` — the authority file
- `internal/permission/engine.go` — permission engine
- `internal/permission/rules.go` — rule definitions
- `internal/permission/home_dir.go` — `permission.HomeDir` (launchd-safe)
- `internal/mcp/dev_tools.go:112-261` — `resolveAllowed` + path-grant fallback callsite for `dev_*`

## The gate stack (in order)

1. **Profile permissions / `tools_allow_list` glob** at `SelectForAgent` (visibility filter — what tools even reach the LLM).
2. **`resolveAllowed`** allow-list root match against `AllowedPaths` per workspace.
3. **Path-grant fallback** if the static list rejects: `tryResolveViaSessionGrant`. Walks session bucket → ancestor lineage.
4. **Symlink-aware escape check** via `pathsafe.ResolveUnder`.
5. **`notify_pause`** 1.5s pre-execution pause for `dev_*` (skip `dev_grep`/`dev_glob`).
6. **Provider/transport auth** for `web_fetch` (provider safety policy) and Vanta tools (transport auth per MCP server).

## Path-grant lifecycle

### Register (incoming user message)

`pathGrants.RegisterFromUserMessage(sessionID, content)` (`path_grants.go:94-140`):

- Strict-prefix tokens with `~/`, `/`, `./`
- Each token registers as **literal + parent dir** — gives the agent reach for both the file and its directory
- Markdown / punctuation strip (trailing `,`, `.`, `)` etc removed)
- Guards: URL guard (no http://, https://), `//` guard, bare `/` guard

Result: each session has a "bucket" of path strings the user mentioned in this conversation.

### Lookup (tool execution)

`LookupPath(sessionID, candidate)`:

1. Resolve candidate (absolute, symlink-safe).
2. Check session bucket for own grant → return `LookupKindOwn` with the matching grant.
3. Walk `lineage[child] = parent` map up to `lineageMaxHops=4` (`path_grants.go:52, 176-240`).
4. If found in ancestor: return `LookupKindAncestorSession` with `viaSessionID` of the granting session.
5. Otherwise: not granted.

`match_kind` is reported back to the caller for telemetry / logging — distinguishes own grants from ancestor walks.

### Lineage register (subagent spawn)

`pathGrants.RegisterLineage(childID, parentSessionID)` is called once at spawn (`subagent_runner.go:249`), cleared by defer when the spawn completes.

`BestSessionDir` mirrors the lineage walk to provide a default `cwd` for `dev_bash` (CW-20260504-0003) — when an agent runs a shell command without specifying `cwd`, `BestSessionDir` finds the most-specific granted directory.

## Logic gates

- **Profile permissions are NOT inherited by workers.** Only path grants. `subagent_runner.go:249` only calls `RegisterLineage`; `EnsureSessionAgent(childID, agent.ID, ...)` binds the worker's own profile. ([05](05-external-agent-execution.md))
- **Strict mode default OFF** (commit 42a77a7). Tool input schema strict-validation isn't applied. Combined with the empty Phase 3 deny-list, this means the LLM has the full surface and must reason about its own tool inputs.
- **`permission.HomeDir` not `os.UserHomeDir`** — `home_dir.go` is launchd-safe (Cerberus deploy fix, CW-20260502-0014, Glass-8). HOME-less launchd env still resolves.
- **`tildeAcceptanceNote()`** — referenced in spec / tool descriptions. Not located as a code symbol — likely a doc-comment string referenced from a description template. The intent: tool descriptions should accept `~/` verbatim and let the permission layer expand, not embed a hardcoded HomeDir in the description.
- **Tool descriptions carry authority with the model** — generic phrasing for paths (no embedding HomeDir), accept `~/`. Per the agent-context-architecture.md lessons doc, this matters because the model treats path strings in descriptions as authoritative.

## Notify-pause flow

For `dev_*` tools (skip `dev_grep`, `dev_glob`):

1. `chat_notify_pause.go:111` emits SSE `notify_pause` with the tool name + args summary.
2. Sleep 1.5s.
3. Run the tool.

The pause is intended to give a future UX path for cancellation. **FE doesn't currently subscribe** ([03 gap](03-sse-envelope-and-interaction-protocol.md#current-gaps)).

## Current gaps

- **G-NOTIFY-PAUSE-FE** — FE doesn't render notify-pause; the 1.5s pause happens silently. See [gaps.md](gaps.md#g-sse-unsubscribed).
- **G-TILDE-NOTE-LOCATION** — `tildeAcceptanceNote()` referenced in spec/decisions but not located as a code symbol; could be a comment in description templates. Worth resolving before tool-description docs are revised.
- **G-BG-PRIVILEGED** — Background spawn (`internal/background/pty.go`) bypasses every gate above. ([05](05-external-agent-execution.md), [gaps.md](gaps.md#g-bg-privileged))

## Test surface

- Real `permission.PathGrants` is too load-bearing to mock; `dev_tools_path_grants_test.go` exercises the full grant lineage scenarios.
- Lineage walk: register grant on session A, spawn child B, spawn grandchild C, assert C resolves the path with `match_kind=ancestor_session viaSessionID=A` and walks 2 hops.
- `lineageMaxHops=4` boundary: a chain of 5 sessions, assert hop 4 succeeds, hop 5 fails.
- `RegisterFromUserMessage` parsing: fixture messages with edge cases (markdown links, bare punctuation, URL guards, `~/` expansion).
- Strict-mode-off behavior: a tool with declared schema receiving an extra field — assert call proceeds.
- launchd HOME absence: assert `permission.HomeDir` resolves correctly with `HOME` unset.
