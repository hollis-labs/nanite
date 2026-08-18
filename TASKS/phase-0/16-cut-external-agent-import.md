# Cut the external-format agent-import tier

**Phase:** 0
**Status:** not-started
**Depends on:** none
**Touches:** `internal/plugin/builtin/adapter-claude/plugin.go` (`Adapter.Discover`), `internal/plugin/builtin/adapter-codex/plugin.go` (`Adapter.Discover`), `internal/plugin/builtin/adapter-gemini/plugin.go` (`Adapter.Discover`), `internal/plugin/builtin/adapter-opencode/plugin.go` (`Adapter.Discover`), and each adapter's `Discover`-specific test cases in `internal/plugin/builtin/adapter-{claude,codex,gemini,opencode}/plugin_test.go`; `internal/agent/discovery_test.go` (`TestDiscover_ClaudeCodeAgents`, and the `.claude/agents`/`.agentrc/agents` fixtures inside `TestDiscover_SlugDedup`).

**Explicitly NOT touched by this task** (verified live, separate concerns — see Context): `internal/agent/discovery.go`'s `Discover()` function and its "Priority 5+: Adapter-discovered agents" block (~lines 81-94) — this must keep running, because it's also how the `nanite-native` adapter's `Discover` (reading `.nanite/config.yaml`'s own `agents:` block) gets invoked, and that one is *not* being cut; `internal/agent/adapter.go`'s `AdapterRegistry`/`CLIAgentAdapter` interface — stays as-is, all four adapters keep implementing it; every adapter's `PopulateSandbox` and `SyncProjectRoot` methods, in all four files plus `adapter-nanite-native` — these are a completely different direction (Nanite *writing* its own config/agent-list out into `CLAUDE.md`/`AGENTS.md`/`GEMINI.md`/`OPENCODE.md`, and — for `PopulateSandbox` — populating a CLI-based subprocess's sandbox at boot time), not import; `internal/service/install/adapters.go` (`newBuiltinAdapterRegistry`, `syncAdaptersForProject`) — this wires the adapters for the *sync* direction only (`nanite-agent init`/`adopt`'s `--adapters`/`--no-adapters` flags), never calls `Discover`; `internal/plugin/builtin/adapter-nanite-native/plugin.go` — stays fully live, not part of this cut.

## Context

TASKS.md Phase 0 item 16: "**The external-format agent-import tier** (`.claude/agents/`, codex/gemini/opencode config import in `Discover()`) — no dependency on the new `roles`/`agents` schema."

`docs/architecture-decision-log-2026-08-17.md` §4 ("Agent-definition spec — early requirements"): "External-format agent import (adapter-discovery tier: `.claude/agents/`, codex/gemini/opencode) — cut, no replacement needed." §6 ("Agent composition model...") confirms this is settled, not reopened: "**Construction is settled.** External-format import cut, plugins stay (must conform), files dropped except builtin/seed..."

Architecture doc `docs/engineering/architecture/01-agent-construction.md`, "What's cut": "**External-format agent import** (importing `.claude/agents/`, codex/gemini/opencode config formats as Nanite agents) — no replacement. Nanite agents are defined in Nanite's own schema."

### Verified against real code (2026-08-18) — this cut is narrower than it might first look

There is a real `CLIAgentAdapter` interface (`internal/agent/adapter.go:14-31`) with **three** distinct responsibilities per adapter, not one:

```go
type CLIAgentAdapter interface {
    Name() string
    Discover(projectDir string) ([]Definition, error)       // ← IMPORT direction (this task's target)
    PopulateSandbox(sandboxDir string, agent store.AgentProfile, session SandboxContext) error  // ← boot-time sandbox population, unrelated
    SyncProjectRoot(projectDir string, agents []store.AgentProfile) error  // ← EXPORT direction, unrelated
    Priority() int
}
```

Five adapters implement this interface: `adapter-claude`, `adapter-codex`, `adapter-gemini`, `adapter-opencode` (the four named in this cut), and `adapter-nanite-native` (not named, must stay). All five are registered into one shared `agent.AdapterRegistry` in **two** places — `internal/service/container.go:341-349` (`newRuntimeAdapterRegistry`, used by the real running server, wired into `agent.Discover(...)` at `container.go:399-403`) and `internal/service/install/adapters.go:44-52` (`newBuiltinAdapterRegistry`, used **only** for the sync/`SyncAllProjectRootsFiltered` direction via `syncAdaptersForProject`, called from `install.go`/`adopt.go` — never calls `Discover`).

**Only `Discover` is the import path this item targets.** Verified each adapter's `Discover` body:
- `adapter-claude` (`plugin.go:129-157`): scans `{projectDir}/.claude/agents/*.md`, parses each as a Nanite `Definition` via `agent.ParseMDFile`, tags `Source: "claude"`.
- `adapter-codex` (`plugin.go:123-153`): reads `{projectDir}/AGENTS.md` as one big system prompt, synthesizes a single `Definition{Slug: "codex-default", Source: "codex"}`.
- `adapter-gemini` (`plugin.go:123-153`): same shape, reads `GEMINI.md`, `Source: "gemini"`.
- `adapter-opencode` (`plugin.go:126-156`): same shape, reads `OPENCODE.md`, `Source: "opencode"`.

**`PopulateSandbox` and `SyncProjectRoot` are a different mechanism entirely and must not be touched.** `PopulateSandbox` writes CLI-specific files *into* a session's sandbox directory at boot time so a CLI-based subprocess (e.g. Claude Code) sees the right content — `adapter-claude`'s version is already a no-op (superseded by `internal/runtime/agent/bootdir_claude.Setup`, per its own doc comment, unrelated to this cut), but `adapter-codex`/`adapter-gemini`/`adapter-opencode`'s versions are real and live (write `AGENTS.md`/`GEMINI.md`/`OPENCODE.md` into the sandbox with the *Nanite* agent's identity/tools/constraints — this is Nanite telling the external CLI tool who it's running as, the opposite direction from import). `SyncProjectRoot` writes a managed "Available Nanite agents" section into the project's own `CLAUDE.md`/`AGENTS.md`/`GEMINI.md`/`OPENCODE.md` — also export direction, also live, gated by the `nanite-agent init --adapters=...`/`--no-adapters` CLI flags (`internal/cli/installcmd/installcmd.go:27-28`). Cutting `Discover` must not affect either of these.

**`nanite-native`'s `Discover` (`internal/plugin/builtin/adapter-nanite-native/plugin.go:262-...`) is a fourth, unrelated source — not in scope.** It reads `.nanite/config.yaml`'s own `agents:` block (composing system prompts from `~/.nanite/roles/`), a completely different mechanism from `.claude/agents/*.md`/`AGENTS.md`/`GEMINI.md`/`OPENCODE.md` import. It shares the same `Discover()` call site in `internal/agent/discovery.go:81-94` ("Priority 5+: Adapter-discovered agents") as the four being cut — **that shared call site must stay**, since removing it would also silence `nanite-native`'s legitimate discovery. This is why the cut has to happen *inside* each of the four named adapters' `Discover` methods, not by deleting the registry loop that calls them.

**Confirmed this import tier is genuinely live in the real server**, not already-dead: `internal/service/container.go:390,399-403` builds `newRuntimeAdapterRegistry()` (registering all five adapters) and passes it as `agent.DiscoverOptions.Adapters` into the real `agent.Discover(...)` call that seeds `agentDefs` at server startup. (A second call site, `cmd/nanite/message_cmd.go:378-382`, passes an *empty* `agent.NewAdapterRegistry()` — a no-op for CLI messaging use — so that call site is unaffected either way.)

No doc/reality mismatch found — the decision log's framing ("no dependency on the new roles/agents schema," "no replacement needed") holds up; the only nuance is that the real cut surface is narrower (four `Discover` method bodies) than "delete the adapter-discovery tier" might suggest, because `PopulateSandbox`/`SyncProjectRoot`/`nanite-native` all share the same interface and registry but are unrelated, live mechanisms.

## What to do

For each of `adapter-claude`, `adapter-codex`, `adapter-gemini`, `adapter-opencode`:

1. Replace the `Discover` method body with a no-op returning `(nil, nil)` — mirroring the precedent already set by `adapter-claude.PopulateSandbox` (`plugin.go:159-170`), which was turned into a no-op with a doc comment explaining why when its own mechanism was superseded. Keep the method (the `CLIAgentAdapter` interface still requires it) but gut its body; add a doc comment referencing this task/the decision-log citation above so a future reader understands *why* it's a no-op rather than assuming it's a bug.
2. Remove now-dead helper code that existed *only* to support `Discover` (none found during verification — each adapter's `Discover` is fully self-contained, no shared private helpers to clean up — but double-check when you're actually in the file, since a helper could exist that this survey missed).
3. Do not touch `PopulateSandbox`, `SyncProjectRoot`, `Name`, `Priority`, or any of the `plugin.Plugin` interface methods (`Load`, `Unload`, `Status`, `Manifest`, etc.) in any of the four files.
4. Do not touch `internal/agent/discovery.go` at all — the "Priority 5+: Adapter-discovered agents" block must keep calling `opts.Adapters.DiscoverAll(...)` so `nanite-native`'s `Discover` keeps working.
5. Update tests: `internal/agent/discovery_test.go`'s `TestDiscover_ClaudeCodeAgents` (~line 190-202) currently asserts that dropping a file into `.claude/agents/` produces a `Definition` with `Source: "claude"` — since `adapter-claude.Discover` now returns `nil, nil`, this test's expected behavior flips (it should now assert **no** agent is discovered from that directory). Update the assertion rather than deleting the test — it's still valuable as a regression guard that the cut adapter really is a no-op. Check `TestDiscover_SlugDedup` (~line 204+) too — it exercises `.agentrc/agents` as one of several project-local sources feeding the same slug; confirm whether that fixture depends on any of the four cut adapters and adjust if so.
6. Each adapter package's own `plugin_test.go` (`adapter-claude`, `adapter-codex`, `adapter-gemini`, `adapter-opencode`) likely has direct unit tests asserting `Discover` parses `.claude/agents/*.md`/`AGENTS.md`/`GEMINI.md`/`OPENCODE.md` into real `Definition`s — update those to assert the no-op behavior instead of deleting coverage. Leave any `PopulateSandbox`/`SyncProjectRoot` tests in those same files untouched.
7. Run `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` — pay particular attention to `internal/agent/...` and `internal/plugin/builtin/adapter-*/...`.

## Done means

- All four adapters' `Discover(projectDir string) ([]agent.Definition, error)` return `(nil, nil)` unconditionally (no filesystem reads of `.claude/agents/`, `AGENTS.md`, `GEMINI.md`, or `OPENCODE.md` for import purposes remain in any of the four files).
- `nanite-native`'s `Discover` still works exactly as before — dropping an `agents:` block into `.nanite/config.yaml` still produces real `Definition`s (verify with a targeted test or manual run, not just by inspection — this is the thing most likely to accidentally break if the shared registry loop is touched by mistake).
- `PopulateSandbox` and `SyncProjectRoot` still work exactly as before for all five adapters — a `nanite-agent init` run against a fresh project still writes the expected managed sections into `CLAUDE.md`/`AGENTS.md`/`GEMINI.md`/`OPENCODE.md`, and a CLI-based subprocess launch still gets its sandbox populated correctly (dogfeed at least one CLI-based agent launch if practical, per `EXECUTION-PROCESS.md`'s validation guidance — this is exactly the kind of "looks safe on paper" cut that's cheap to verify and expensive to get wrong silently).
- Updated tests pass and genuinely assert the new (no-op) behavior rather than being deleted/skipped.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` all pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
