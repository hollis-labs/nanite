# Per-agent Protocol/Transport selection (DB-configurable surface)

**Phase:** 3 — ACP client abstraction & native adapters (`TASKS/agent-host-acp`)
**Status:** not-started
**Depends on:** `09`, `10` (at least the native ACP adapters must exist to be selectable);
`07` (host migration validated — this wires real agents into the migrated path)
**Touches:** possibly a new migration (`134` onward — check current highest at dispatch
time), `internal/runtime/agent/context_resolver.go` or a sibling file, `internal/service/
chat.go` (`classifyNilProvider`), `internal/plugin/agent_profiles.go`. Repo: Nanite.

## Context

17-acp.md is explicit: "No new top-level `agents.runtime_kind` value... ACP-driven agents
still route through the existing `cli` value... `Protocol`/`Transport` are internal to the
host descriptor, not a database concern. ... Per-agent protocol/transport selection belongs
in the same DB-configurable surface as the existing `agent_context_resolvers` pattern, not a
new schema axis."

**The precedent this task follows, verified directly**: `internal/runtime/agent/
context_resolver.go`'s `agent_context_resolvers` table (`internal/store/
agent_context_resolvers.go`, migration `118_agent_context_resolvers.sql`) has a `Kind string`
field (`:44`, values `"cmd"`/`"http"`, validated at `:97`) that drives a switch —
`contextResolverToSlotSpec` (`context_resolver.go:113`) builds a different typed spec per
kind, CRUD already exists per-agent, and resolution happens once at launch inside the `agent`
package (`ResolveContextBlocks`, `:62`). This is the exact shape 17-acp.md points at — this
task should follow it closely, not invent a new mechanism shape.

**Where this plugs into real routing, verified directly**: `runtime_kind` is a column on
`agent_profiles` (migration `080_agent_runtime_kind.sql:10`, `'cli'`/`'api'`, validated
`internal/plugin/agent_profiles.go:209`). The actual CLI-vs-API routing decision is
`chatServiceImpl.classifyNilProvider(runtimeKind, providerName string)` at
`internal/service/chat.go:1327` — the authoritative decision point per
`internal/chat/engine.go:260-264`'s own doc comment (`chat.IsCLIProvider` is a demoted
fallback, consulted only when `runtime_kind` isn't populated). A per-agent Protocol/Transport
choice sits *alongside* `runtime_kind='cli'`, not as a replacement for it — an ACP-driven
agent is still `runtime_kind='cli'`, just resolved to a different concrete adapter once
`internal/runtime/agent/factory.go`'s provider-dispatch logic runs (`factory.go:48` already
consults `runtime_kind`; `bootdir.go:238-240` too).

## What to do

1. Add a per-agent Protocol/Transport selection field — following the `agent_context_
   resolvers` `Kind`-driven precedent exactly: a typed column (or small table, if a per-agent
   1:many shape is genuinely needed — check whether one agent might plausibly want more than
   one Protocol/Transport configured, e.g. a fallback; if not, a single column on
   `agent_profiles` is simpler and matches `runtime_kind`'s own precedent more closely than
   a new table would). Values: the native protocols (`claude-stream-json`/`codex-app-server`/
   `opencode-native`) plus `acp`, with `Transport` following whichever the chosen protocol
   allows (most are fixed to `stdio`; ACP can be `stdio` or `tcp` per task `10`'s Copilot-CLI
   case).
2. Wire this into `internal/runtime/agent/factory.go`'s provider-dispatch logic (the same
   place `runtime_kind` is already consulted) so an agent configured for ACP actually resolves
   to one of tasks `09`/`10`'s adapters (or, once Phase 4 lands, a bridge adapter) instead of
   its native one.
3. Default: unset/empty means "use the existing native protocol for this provider," per
   17-acp.md's explicit "additive, not a cutover" framing — no agent should silently switch to
   ACP without an explicit per-agent configuration choice.
4. Add CRUD for this field following whatever the closest existing precedent is (likely
   `agent_context_resolvers`' own REST surface, or a plain field on the existing
   `agent_profiles` update endpoint if it's a single column) — check `docs/engineering/
   architecture/02-agent-launching.md`'s stated rejection of "a proliferating string-prefix
   convention in favor of one typed field" before choosing a shape.
5. If this needs a new migration, check the current highest number on disk immediately before
   writing it (provisionally `134`, per this batch's README — but other tasks/batches may have
   landed migrations first; re-check).

## Done means

- A per-agent Protocol/Transport selection exists, DB-configurable, following the
  `agent_context_resolvers` `Kind`-driven precedent's shape (typed field/small table, CRUD,
  resolved once at launch) rather than inventing a new mechanism.
- No new `agents.runtime_kind` value — confirmed by test, an ACP-configured agent still shows
  `runtime_kind='cli'`.
- An agent explicitly configured for `acp`+OpenCode (or Copilot CLI) actually launches through
  task `09`'s (or `10`'s) adapter — verified end-to-end, not just at the config-storage layer.
- Every existing agent's behavior is unchanged (empty/unset selection = native protocol,
  verified against real existing `agent_profiles` rows, not just a fresh-row test).
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` clean.
