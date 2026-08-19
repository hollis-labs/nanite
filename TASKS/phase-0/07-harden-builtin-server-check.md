# Harden IsFirstPartyBuiltinServerName against the next builtin server

**Phase:** 0
**Status:** implemented
**Depends on:** none
**Touches:** `internal/mcp/naming.go` (`IsFirstPartyBuiltinServerName`, `SelfServerName`/`DevServerName`/`CodeServerName`/`GeneralServerName` constants), `internal/mcp/manager.go` (`Manager` struct, `AddServer`/`AddStdioServer`/`AddHTTPServer`/`AddPluginServer`, `assignUniformNameLocked`), `cmd/nanite/main.go` (`initMCP`, the four builtin `mcpManager.AddServer(...)` calls), `internal/mcp/naming_test.go`, `internal/mcp/manager_trust_test.go`

## Context

TASKS.md item 7: "Harden `IsFirstPartyBuiltinServerName` — same bug shape as a prior incident, currently a hand-maintained 4-name switch."

**No section of `docs/architecture-decision-log-2026-08-17.md` discusses this item directly** — unlike the other Phase 0 items in this batch, it doesn't trace to a specific numbered decision. The real evidence trail is git history plus this project's own `CLAUDE.md` "MCP trust tiers" section and `docs/mcp-trust-model.md`. Flagging this so the worker doesn't go hunting for a decision-log citation that isn't there.

**The prior incident, verified via `git show 5144590`** (commit `5144590`, "fix: protect all first-party builtin MCP tools from proxy name collisions", 2026-08-17):

> Previously only the "self" server was protected from having its bare uniform-name slot evicted by a same-named proxied tool. Once the Agent Mux proxy started returning loom/fragments-engine tools — which ship the same generic scaffold vocabulary as nanite's own dev/skill/todo/etc tools — "Agent Mux" sorted alphabetically before "dev" and silently stole dev_bash/dev_read/etc, producing "unknown MCP tool" errors for any agent that called them.

That commit's fix was to generalize the previously self-only reserved-namespace defense (`IsReservedSelfToolName`) into a new function, `IsFirstPartyBuiltinServerName` (`internal/mcp/naming.go:132-139`), covering all four in-process builtin servers:

```go
func IsFirstPartyBuiltinServerName(server string) bool {
	switch server {
	case SelfServerName, DevServerName, CodeServerName, GeneralServerName:
		return true
	default:
		return false
	}
}
```

**This is the "same bug shape" TASKS.md is warning about, one level up.** Before commit `5144590`, the defense was a hand-maintained list of exactly one name (`self`) — and it silently failed to protect the other three real builtins (`dev`/`code`/`general`) until a live production incident (a proxy server sorting alphabetically before `dev` and stealing its tools) forced the fix. The fix that landed is *also* a hand-maintained list — now four names instead of one — kept in sync by hand across two files: the literal string names passed to `mcpManager.AddServer(...)` in `cmd/nanite/main.go:908-942` (`"dev"`, `"general"`, `"code"`, `mcp.SelfServerName`), and the `switch` statement in `internal/mcp/naming.go:132-139`. Nothing enforces that these two lists stay identical. If a fifth first-party builtin server is ever registered in `main.go` without a matching update to the `naming.go` switch, the exact same collision bug reappears — a same-named proxied tool from some other server can silently evict it, the same way `dev`'s tools were stolen before `5144590`.

**Doc-comment self-awareness** — `naming.go:116-131`'s doc comment on `IsFirstPartyBuiltinServerName` already explains *why* this can't just check `TrustTier == TierBuiltin` instead: "test fixtures and future callers legitimately register arbitrary/hostile servers at TierBuiltin to exercise plain collision behavior, so tier alone can't distinguish 'one of nanite's real builtins' from 'some other server that happens to carry that tier.'" This constraint is real and still holds — any fix must preserve the property that a test-only server tagged `TierBuiltin` for other reasons does NOT get treated as first-party.

**Minor doc/reality note, not central to this task but worth knowing:** `docs/mcp-trust-model.md`'s trust-tier table (line 29) lists "memory tools" as part of the `builtin` tier's source alongside dev/general/code/self tools. There is no such server today — `internal/service/container.go:1340-1347` documents that the `nanite-memory` MCP server registration was removed (CW-20260508-0017; Vanta is now the canonical memory substrate). So the *current* real closed set is genuinely four (self/dev/code/general), matching `naming.go`, not five. The doc's "memory tools" mention is stale; not in scope to fix here, just noting it so nobody "fixes" `naming.go` to a five-name list based on that doc line.

## What to do

Replace the hardcoded switch with something that can't drift from what `cmd/nanite/main.go` actually registers — i.e. a real registry populated at the point of registration, not a string list mirrored by hand in a second file.

Concretely: the `Manager` (`internal/mcp/manager.go`) already tracks `serverTiers map[string]TrustTier` per server at registration time (`AddServer`, `AddStdioServer`, `AddHTTPServer` all populate it; `AddPluginServer` hardcodes `TierPluginStdio`). Add a parallel, narrower piece of state — e.g. a `firstPartyBuiltinNames map[string]bool` (or equivalent) — populated ONLY by a registration path that `cmd/nanite/main.go`'s four real builtin-registration call sites use, and consulted by `IsFirstPartyBuiltinServerName`'s replacement instead of a switch statement. This preserves the doc comment's constraint (test fixtures registering arbitrary servers at `TierBuiltin` via the *ordinary* `AddServer` path would NOT populate this new map, so they still don't count as first-party) while making "register a builtin" and "protect that builtin's bare tool-name slot" the same action instead of two actions a human has to remember to keep in sync.

Two call sites inside `manager.go` currently call the package-level function directly (`assignUniformNameLocked`, lines 598-599 and 632) — both already have a `*Manager` receiver in scope, so converting to a method on `Manager` (or keeping a package-level function but backing it with `Manager`-scoped state passed in) is straightforward. Check `internal/mcp/naming_test.go` and `internal/mcp/manager_trust_test.go` for existing tests that call `IsFirstPartyBuiltinServerName` directly as a pure function (`naming_test.go:342` area) — if the signature changes (e.g. becomes a `Manager` method), those tests need updating, not just the production call sites.

Whatever shape this takes, the acceptance bar is: **adding a fifth first-party builtin server in `cmd/nanite/main.go` should require touching exactly one call site (the registration itself), not two.** If your fix still requires a human to remember to update a second, separate list, it hasn't actually closed the gap TASKS.md is describing.

Do not change `IsReservedSelfToolName` (`naming.go:91-114`) or the self-only reserved-namespace defense unless your fix naturally subsumes it — check whether it becomes fully redundant once `IsFirstPartyBuiltinServerName` covers `self` too (both are checked together at `manager.go:598` and `632` via `||`). If it does turn out redundant, that's a legitimate small cleanup to fold in, but verify first rather than assuming — `IsReservedSelfToolName` takes a `toolName` parameter `IsFirstPartyBuiltinServerName` doesn't, kept "for completeness... and forward-compatibility" per its own doc comment, so there may be a reason they're still separate.

## Done means

- `IsFirstPartyBuiltinServerName` (or its replacement) is no longer a hardcoded 4-name switch statement — it derives its answer from state populated at the actual registration call sites in `cmd/nanite/main.go`, not a second hand-maintained list.
- The existing regression coverage from commit `5144590` still passes: the "Agent Mux sorts before dev" collision test and the slice-growth-doesn't-go-stale test in `internal/mcp/naming_test.go` (added by that commit — locate via `git show 5144590 --stat` if needed).
- A new test proves the hardening actually works: register a fifth "builtin-style" server through whatever the new first-party registration path is, confirm its bare tool-name slot resists eviction by an alphabetically-earlier proxied server with a colliding tool name — the same shape as the existing "Agent Mux before dev" test, but for a server not in the original four-name list, to prove this isn't just re-testing the old hardcoded set.
- A test confirms the doc comment's stated constraint still holds: a test-only server registered at `TierBuiltin` through the *ordinary* registration path (not the new first-party path) does NOT get treated as first-party — i.e., tier alone still isn't sufficient, on purpose.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass, including `internal/mcp/...`.

## Work log

Implemented per "What to do." Replaced the hand-maintained 4-name switch
(`IsFirstPartyBuiltinServerName`, `internal/mcp/naming.go:132-139` pre-change)
with real registration-time state on `Manager`, so "register a builtin" and
"protect that builtin's bare tool-name slot" are the same action.

**Design chosen:** added `Manager.firstPartyBuiltinNames map[string]bool`
(initialized in `NewManager`), populated ONLY by a new
`Manager.AddBuiltinServer(name string, transport MCPTransport) error` —
calls `AddServer(name, transport, TierBuiltin)` then marks `name` first-party
under the lock; propagates any `AddServer` error (empty name, nil transport,
duplicate registration) without touching the new map. A new
`Manager.isFirstPartyBuiltinServerLocked(name string) bool` (caller holds
`m.mu`) reads the map and replaces the 3 `IsFirstPartyBuiltinServerName(...)`
call sites inside `assignUniformNameLocked`. `RemoveServer` now also deletes
`firstPartyBuiltinNames[name]` so a removed-then-reused name doesn't retain
stale first-party status.

**Files changed:**
- `internal/mcp/manager.go` — `Manager` struct field, `NewManager` init,
  `AddBuiltinServer`, `isFirstPartyBuiltinServerLocked`, `RemoveServer`
  cleanup, and the 3 `assignUniformNameLocked` call sites.
- `internal/mcp/naming.go` — removed `IsFirstPartyBuiltinServerName`
  entirely; left a pointer comment explaining the replacement and why;
  updated the `DevServerName`/`CodeServerName`/`GeneralServerName` doc
  comment (no longer "a closed set `IsFirstPartyBuiltinServerName` checks
  against"). `IsReservedSelfToolName` and the const declarations themselves
  are unchanged — see "IsReservedSelfToolName" note below.
- `cmd/nanite/main.go` (`initMCP`) — all four builtin registrations (`dev`,
  `general`, `code`, `self`) now call `mcpManager.AddBuiltinServer(...)`
  instead of `mcpManager.AddServer(..., mcp.TierBuiltin)`. Also switched the
  `dev`/`general`/`code` call sites from raw string literals to the
  existing `mcp.DevServerName`/`mcp.GeneralServerName`/`mcp.CodeServerName`
  constants (previously only `self` used its constant) — small consistency
  fix, in scope since main.go's four registration call sites are explicitly
  listed as touched.

**Acceptance bar met:** a fifth first-party builtin now needs exactly one
new `mcpManager.AddBuiltinServer("name", transport)` line in `main.go`'s
`initMCP` — `naming.go` has no switch/list left to update. Proven by the new
`TestManager_UniformIndex_FifthBuiltinServerResistsEviction`, which
registers a server named "widget" (never part of the old four-name set)
purely through `AddBuiltinServer` and confirms it resists eviction by an
alphabetically-earlier colliding proxy — same collision shape as the
existing "Agent Mux vs dev" regression, but for a name that was never
hardcoded anywhere.

**`IsReservedSelfToolName` — checked, NOT removed (per task instructions'
explicit caution to verify, not assume).** Since `self` is now also
registered via `AddBuiltinServer` in `main.go`, `self` is in
`firstPartyBuiltinNames` too, which makes the
`IsReservedSelfToolName(...) || m.isFirstPartyBuiltinServerLocked(...)`
checks redundant for `self` specifically *in production*. Kept anyway
because: (1) it's a pure, registration-path-independent identity check —
it still protects `self` even if a future refactor accidentally registers
the self-tools transport through plain `AddServer` instead of
`AddBuiltinServer` (defense-in-depth against exactly the kind of
registration-path slip this task hardens against elsewhere); (2) it's
exercised directly by its own test (`TestIsReservedSelfToolName`) and by
direct-call assertions inside `TestManager_UniformIndex_PostRenameShadowDefense`,
independent of `Manager` state. Not fully redundant in the defense-in-depth
sense, so left in place.

**Existing tests updated (required, not scope creep):**
`TestManager_UniformIndex_NonSelfBuiltinReservedNamespace` and
`TestManager_UniformIndex_RenameSurvivesSliceGrowth` (both from commit
5144590, both named in Done means as regression coverage that "still
passes") registered `dev` via `mgr.AddServer(DevServerName, ..., TierBuiltin)`.
Under the new design that no longer grants first-party status — only
`AddBuiltinServer` does — so both were switched to
`mgr.AddBuiltinServer(DevServerName, ...)` to keep exercising the same
collision scenario the Done means requires. Checked every other test file in
the repo that registers `dev`/`general`/`self` via plain
`AddServer(..., TierBuiltin)` (`internal/mcp/restart_stdio_test.go`,
`internal/toolclient/devmode_gate_test.go`,
`internal/service/chat_path_grants_e2e_test.go`) — none exercise a collision
scenario (no competing same-named server registered alongside), so none
needed changes; confirmed by full `go test ./...` passing.

**New tests added** (`internal/mcp/naming_test.go`), covering both Done
means bullets plus plumbing:
1. `TestManager_AddBuiltinServer` — tier assignment + duplicate-registration
   error propagation.
2. `TestManager_UniformIndex_FifthBuiltinServerResistsEviction` — Done means
   bullet 3 ("a fifth builtin-style server resists eviction").
3. `TestManager_UniformIndex_TierAloneDoesNotGrantFirstPartyStatus` — Done
   means bullet 4 ("TierBuiltin alone is not sufficient" / the doc
   comment's constraint). Registers two servers at `TierBuiltin` via the
   ordinary `AddServer` path (neither via `AddBuiltinServer`) with a
   colliding tool name; asserts ordinary collision-disambiguation applies
   to both (neither force-evicts the other).

**Correction to the record (EXECUTION-PROCESS worker step 7):** none
needed. The task file's own account of the prior incident and the fix
required (verified independently via `git show 5144590`) matches the code
as found. This item doesn't trace to a numbered decision-log entry (the
task file already flags this), so there's no decision-log rationale to
correct either.

**Escalations:** none. No genuine ambiguity, no zero-doc-coverage item, no
item-vs-item TASKS.md contradiction. Handled the security-sensitive nature
of this task (tool-name-collision defense) by being conservative — kept
`IsReservedSelfToolName` as defense-in-depth rather than removing it on the
theory that it's now redundant, and added an explicit test proving the
"tier alone is insufficient" doc-comment constraint still holds rather than
just asserting it.

**Verification:**
- `go build ./cmd/nanite/`: pass.
- `go vet ./...`: 2 pre-existing failures in `internal/service/container.go`
  (`stopReaper`/`stopRuntimeReaper` possibly-unused-on-some-paths) —
  unrelated file, not touched by this task. `go vet ./internal/mcp/...
  ./cmd/nanite/...` clean.
- `go test ./...`: exactly 2 failing packages, `internal/envelope`
  (`TestEnvelopeSchemas_AllTypesHaveSchemas`) and `internal/mcp`
  (`TestNaniteToolDescribe_ShowCardExamplesValidateAgainstSchemas`), both
  tracing to the same root cause (missing `question-form` envelope schema
  file — appears related to the still-pending `13-cut-question-form` task).
  Confirmed via `git stash` that both fail identically on the unmodified
  baseline commit `df71e71` — pre-existing, unrelated to this change. Every
  test this task touched or added passes, including the full
  `internal/mcp` suite modulo that one pre-existing failure.
- `gofmt -l` clean on all four touched files.

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
