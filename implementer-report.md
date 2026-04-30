# SP5 — dev_glob/dev_read allowed-roots — implementer report

**Ticket:** CW-20260430-0005
**Branch:** `fix/c112-regression-cluster` (worktree only — not pushed)
**Scope:** Permission-layer / config concern. ZERO changes to `internal/agent/builtin/default.md`.

---

## Decision-rules pass

Run against `docs/architecture/agent-context-architecture.md` §"Decision rules for new work":

1. **Where does this constraint actually need to be enforced?** — Permission layer (Layer Map: orthogonal). Specifically: config-loaded allow-list + path-safety escape check at the dev-tool boundary. NOT the system prompt.
2. **Preemptive or reactive?** — Reactive: the existing escape check fires only when a tool is called with a path. No new gate added.
3. **Is another layer already doing this?** — No. The path-safety check (`internal/pathsafe`) and the allow-list it operates on are the canonical place. This change widens the list and adds tilde expansion at the same boundary; it does not introduce a parallel check.
4. **Could this go in a tool description instead of the system prompt?** — N/A. This is a runtime config concern, not a prompting concern.
5. **Could this be a runtime classifier injection?** — No. It's a per-installation security capability, not per-task framing.
6. **Handcuffs OFF or ON?** — **OFF.** Widens the allow-list + fixes a canonicalization bug that was rejecting valid paths. The agent ends a session more capable than before.
7. **c117 test (would this cause an agent to dodge a "show me X" request?)** — No. The agent will gain access to paths it currently can't reach; nothing new is being denied.
8. **Prompt density?** — Untouched. No system-prompt edits. Word count, bullet count, and negative-phrasing count are all unchanged from the parent commit.

Load-bearing rules per the ticket: 1 (permission layer enforcement), 6 (handcuffs OFF), 8 (no system-prompt change). All pass.

---

## Allow-list location (current state)

Before this ticket the allow-list was **hardcoded in two places**:

- `cmd/nanite/main.go:540-543` — `initMCP()` for the chat-harness server, hardcoded to `~/Projects-apps`, `~/Projects`.
- `cmd/nanite/main.go:828-832` — `cmdMCPServe()` for the stdio MCP server (used by Claude CLI), same hardcoded pair.

The list was sourced from Go code at startup — no config knob existed. Note that two YAML files share the `nanite` name in the user's home: the *agent framework* config at `~/.nanite/config.yaml` (defines roles, agents, projects — separate concept) and the *chat-harness runtime* config at `~/.nanite/nanite.yaml` (loaded by `internal/config.Load()`). The chat-harness's allow-list belongs in `~/.nanite/nanite.yaml` per the existing chat-harness conventions; the ticket's reference to `~/.nanite/config.yaml` was likely shorthand. The doc update calls out the distinction.

After this ticket the list is read from `Config.DevToolsAllowedPaths` (YAML key `dev_tools_allowed_paths`) and falls back to a widened default when unset.

---

## Canonicalization bug investigation

**Root cause: Go's `filepath` package does not expand the shell `~`.** 

The user reported `~/Projects-apps/nanite/coordination` was rejected with "escapes root `~/Projects`". Tracing through `internal/mcp/dev_tools.go:resolveAllowed`:

1. The LLM passes `userPath = "~/Projects-apps/nanite/coordination"`.
2. `filepath.Abs(userPath)` produces `/<cwd>/~/Projects-apps/nanite/coordination` because Go treats `~` as a literal character.
3. `filepath.Rel(/Users/chrispian/Projects-apps, /<cwd>/~/Projects-apps/...)` returns a `..`-prefixed string → escape error fires.
4. The "escapes root `~/Projects`" wording in the user's report was the agent shortening `/Users/chrispian/Projects` to `~/Projects` for display; the actual stored Root in the EscapeError was the absolute form. So the "exact-match path escaping itself" smell was real but stems from the same tilde-expansion gap, not a separate bug in `isUnder` or `filepath.Clean`.

**Fix.** Added `expandHome()` helper inside `internal/mcp/dev_tools.go` and called it at two places:

- Inside `resolveAllowed`, on the user-supplied path, *before* `filepath.Abs` runs. This preserves the symlink-aware escape check downstream — expansion happens at the boundary, not by skipping checks.
- Inside `NewDevToolsTransport`, on each configured allow-list entry, so `~/Projects-apps` from config canonicalizes correctly at construction.

Both forms are covered by tests (`TestExpandHome_Forms`, `TestResolveAllowed_TildeUserPath`, `TestResolveAllowed_EscapeStillBlocked`). The escape regression test confirms `~/../../etc/hosts` still fails — expansion runs *before* the safety check, not in place of it.

---

## Changes made

**`internal/config/config.go`** — added `DevToolsAllowedPaths []string` field to `Config` (YAML key `dev_tools_allowed_paths`), wired into the project-overrides-user merge logic, and added `ResolvedDevToolsAllowedPaths()` accessor that tilde-expands entries and drops empties. Returns nil when unset so callers can fall back to defaults.

**`internal/config/config_test.go`** — added `TestResolvedDevToolsAllowedPaths` (nil/expansion/empty-drop subtests) and `TestLoadFrom_DevToolsAllowedPaths_Merge` covering project-replaces-user override semantics.

**`internal/mcp/dev_tools.go`** —
- Added `expandHome(path string) string` helper covering `~`, `~/`, and `~` + path-separator forms; bare `~root` (other-user) intentionally not expanded.
- `NewDevToolsTransport` now expands tildes on every allow-list entry before `filepath.Abs`.
- `resolveAllowed` now expands a leading tilde on the user-supplied path before `filepath.Abs`, fixing the canonicalization gap. Comment block calls out the bug and links the ticket.

**`internal/mcp/dev_tools_test.go`** — added three tests:
- `TestExpandHome_Forms` — table test for the helper covering empty, bare `~`, `~/`, deep `~/x/y`, absolute, relative, and the `~root`-not-current-user case.
- `TestResolveAllowed_TildeUserPath` — sets `HOME` to a tempdir, configures `~/sub` as the allow-list root, then verifies that `~/sub/file.txt` and bare `~/sub` both resolve to paths under the root (the c120 regression scenario, simulated without touching the running user's actual filesystem).
- `TestResolveAllowed_EscapeStillBlocked` — confirms `/etc/hosts` and `~/../../etc/hosts` both still fail with escape errors, so the allow-list widening hasn't weakened the path-safety check.

**`cmd/nanite/main.go`** —
- Added `defaultDevToolsAllowedPaths()` returning the hardcoded fallback list, widened to include `~/.nanite` and `~/.claude` per the ticket. `~/Projects-apps/agent-workspaces` is covered by the `~/Projects-apps` parent root and does not need a separate entry.
- Added `resolveDevToolsAllowedPaths(cfg *config.Config)` that uses the config-supplied list when set and falls back to defaults otherwise.
- Added `devAllowedSource(cfg *config.Config)` for an info-log breadcrumb at startup so future "why didn't this path resolve?" debugging has the source visible.
- `initMCP` now takes `cfg *config.Config` and uses the resolver. Logs the resolved allow-list at startup.
- `cmdMCPServe` (stdio entry point used by Claude CLI) now also goes through `resolveDevToolsAllowedPaths`, so the two entry points share one source of truth.

**`docs/developer-mode-gate.md`** — added a new "Filesystem allow-list (`dev_tools_allowed_paths`)" section under "Wiring in production" covering: default list, user-override syntax, the `~/.nanite/config.yaml` vs `~/.nanite/nanite.yaml` distinction, and a paragraph documenting the canonicalization fix so the next person who wonders "why does this expand `~`" can find the answer.

---

## Tests added / updated

**Added:**
- `TestExpandHome_Forms` (`internal/mcp/dev_tools_test.go`)
- `TestResolveAllowed_TildeUserPath` (`internal/mcp/dev_tools_test.go`)
- `TestResolveAllowed_EscapeStillBlocked` (`internal/mcp/dev_tools_test.go`)
- `TestResolvedDevToolsAllowedPaths` (3 subtests) (`internal/config/config_test.go`)
- `TestLoadFrom_DevToolsAllowedPaths_Merge` (`internal/config/config_test.go`)

**Untouched (verified still passing):**
- All `internal/pathsafe` tests — escape detection, symlink resolution, EmptyRoot, NonexistentRoot.
- `TestDevRead_SymlinkEscape`, `TestResolveAllowed_ReturnsEscapeErrorType` — the security regression suite.
- `TestDevGlob_RespectsAllowedPaths`, `TestDevEdit_PathScoping`, `TestDevBash_RejectsBadWorkingDir` — denylist tests.

---

## Verification

- **`go build ./cmd/nanite/`** — pass.
- **`go test ./...`** — pass. All ~70 packages green; no regressions in `pathsafe`, `mcp`, `mcpserver`, `config`, `cmd/nanite`, or any downstream consumer.
- **`go vet ./...`** — pass.
- **Sample config / docs updated:** yes — `docs/developer-mode-gate.md` gains a "Filesystem allow-list" section. There is no `nanite.yaml.example` in the repo (the user-level config is created on demand), so the doc is the canonical reference. `config/nanite.yaml` is `AppConfig`, not the agentrc config that owns the new field — no edit needed there.

Acceptance checklist:

- [x] `dev_glob` with `~/Projects-apps/nanite/**/*.go` succeeds — covered by `TestResolveAllowed_TildeUserPath` (simulates the runtime config scenario without touching the running user's fs).
- [x] `dev_read` with `~/Projects-apps/nanite/CLAUDE.md` succeeds — same path through `resolveAllowed`, verified by the same test.
- [x] Allow-list is read from user config — `~/.nanite/nanite.yaml`, key `dev_tools_allowed_paths`. Documented in `docs/developer-mode-gate.md`. Project-level `nanite.yaml` overrides per existing merge rules.
- [x] Bare `~/Projects` no longer "escapes" itself — `TestResolveAllowed_TildeUserPath` includes the bare-root case. Root cause documented above (Go's `filepath` doesn't expand tildes); fix is at the boundary in `expandHome()`.
- [x] Existing path-safety tests still pass — escape prevention intact; `TestResolveAllowed_EscapeStillBlocked` is a new test specifically covering this.
- [x] Sample / docs updated — `docs/developer-mode-gate.md`.
- [x] `go build ./cmd/nanite/` and `go test ./...` pass in the worktree.

---

## Commit SHAs

- `044259e` — SP5 — dev_glob/dev_read configurable allow-list + tilde-expansion fix
- (this commit) — SP5 — implementer report

---

## Deviations / open questions

- **`~/.nanite/config.yaml` vs `~/.nanite/nanite.yaml`.** The ticket directs the user-level config to `~/.nanite/config.yaml`. That file actually belongs to the *Nanite agent framework* (roles/agents/projects registry) and is loaded by tooling outside the chat-harness binary. The chat-harness's own user-level config is `~/.nanite/nanite.yaml` (loaded by `internal/config.Load()`). I added the new field to `internal/config.Config` (i.e. the chat-harness's config) which means it goes into `~/.nanite/nanite.yaml`. Doc update calls out the distinction. If the orchestrator wants the field readable from `~/.nanite/config.yaml` instead, that requires a second loader and is a non-trivial cross-product change — left as an open question rather than scope-creeping. The current design works for the user's stated need (widen access to project paths) and is internally consistent with how every other path field in the chat-harness config is loaded.
- **Override semantics.** The user-supplied `dev_tools_allowed_paths` *replaces* the defaults rather than merging. This matches the existing semantics for `WritePaths` and `ProtectedPaths`. A user who wants to retain the defaults plus add a new root must list all of them explicitly. If you'd prefer additive semantics, that's a one-line change in `resolveDevToolsAllowedPaths` — not changed here because the defaults are sensible for most users and explicit-replace is the existing pattern in this Config type.
- **`agent-workspaces` not added separately.** The ticket asks "check whether `~/Projects-apps/agent-workspaces` needs explicit listing or is covered by the parent." It's covered by the `~/Projects-apps` root via `pathsafe.isUnder`; verified by inspection. Not added separately.
- **`nanite_*` subagent path of inheritance.** The MCP stdio server entry point (`cmdMCPServe`) now also reads the config, but since it spawns from Claude CLI it operates in the user's environment. If the orchestrator needs a different list for stdio MCP than for the chat-harness server, that's a future ticket; today both use the same resolver.
