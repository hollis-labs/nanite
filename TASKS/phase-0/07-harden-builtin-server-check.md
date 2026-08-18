# Harden IsFirstPartyBuiltinServerName against the next builtin server

**Phase:** 0
**Status:** not-started
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
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
