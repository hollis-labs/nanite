# Architecture Enforcement Tests

A follow-up architecture topic from the harness audit: use CI/static tests for important boundaries — no provider imports outside adapters; no direct process spawn outside Agent Host; no frontend transport calls outside typed clients; no plugin bypass of host capability/registration surfaces.

**Findings only. This topic needs its own dedicated architecture session** — the mechanism choice, exact boundary list, exception handling, and CI wiring are real design work, not resolved here. This doc exists so the findings aren't lost, and so the obvious next steps are visible to whoever runs that session.

## Existing precedent

Two real patterns already exist in this codebase for exactly this kind of rule:

- **Runtime-assertion tests with deliberate-violation coverage** — `internal/context/INVARIANTS.md` + `internal/service/slot_invariants_test.go`: each invariant names its enforcing check function and has a test proving it has teeth. This is the established style for "make an architectural rule enforceable" here — a Go test, not CI-only static lint.
- **Path-scoped static lint** — `.golangci.yml` already enables `forbidigo` for path-scoped forbidden-pattern rules, currently narrow (only `os.WriteFile`/`filepath.Join` under a Phase 1 Wave 1 adoption). The tooling for path-scoped enforcement already exists in this repo's lint config; it's just not applied to the four boundaries below.

No `depguard` config or custom static analyzer exists.

## Finding 1 — provider-import boundary: real, open

`CLAUDE.md`'s reference to `internal/provider/` is stale — that directory doesn't exist. The real adapter packages are `internal/llm/anthropic` and `internal/llm/openai`. Both are imported directly from non-adapter code: `internal/service/chat.go`, `chat_generate.go`, `embedder_select.go`, `recovery_credentials.go`, `chat_rate_budget_pause.go` (plus `cmd/nanite/main.go` as the composition root — arguably a legitimate exception, not a violation, if this is enforced). No test or lint catches this today.

## Finding 2 — frontend typed-client boundary: real, open

`ui/src/lib/api.ts` is the typed client. Raw `fetch`/`axios` calls bypass it in `CatalogBrowser.tsx`, `PluginManager.tsx`, `slashCommandSuggestion.tsx`, `fileMentionSuggestion.tsx`, `ErrorCard.tsx`, `ArtifactsContent.tsx`, `envelope-response.ts`, `plugin-loader.ts`. Unenforced today.

## Finding 3 — "no process spawn outside Agent Host": not applicable as literally stated

`internal/runtime/agent/` (the real Agent Host consumer, per [16-agent-host.md](16-agent-host.md)) already has **zero** `exec.Command` calls — actual CLI-agent spawn logic lives in the external `go-agent-wrapper`/`agentkit` modules, outside this repo entirely. Meanwhile 23 files across this repo use `os/exec` for unrelated, legitimate domains: MCP stdio transport, the plugin subprocess manager, git worktree operations, the shell self-tool, the `python_run` sandbox, CLI installer/dev tooling, the background PTY backend. A literal "no `exec.Command` outside package X" rule would false-positive on all of them.

If this is pursued, it needs reframing — e.g. assert `internal/runtime/agent/` itself has zero `exec.Command` calls (cheap, already true, trivial to keep true) — rather than a repo-wide ban that doesn't match how process-spawn responsibility is actually distributed in this codebase.

## Finding 4 — "no plugin bypass of host capability/registration surfaces": real, but not a new independent test

Confirmed: `internal/plugin/builtin/adapter-{claude,nanite-native,gemini,codex,opencode}/plugin.go` all import `internal/store` directly — exactly the gap [09-plugin-system.md](09-plugin-system.md) already documents ("nothing stops... an unscoped `GetService("store")`... call returning the raw `*sql.DB`"). This isn't a separate enforcement gap to invent a test for now — it's the same problem the `PluginStore`/`PluginMCPClient` proxy work already queued in `TASKS/plugin-system/02`–`03` is designed to fix. An enforcement test here only becomes meaningful once those proxies exist to enforce *against* — testing for their absence today would just restate the known gap.

## Obvious remediation candidates (starting points, not designed here)

- **Provider-import boundary** — a `slot_invariants_test.go`-style Go test, or a `forbidigo` path rule, restricting `internal/llm/*` imports to an explicit allowlist.
- **Frontend fetch boundary** — an equivalent test/lint restricting raw `fetch`/`axios` usage to `ui/src/lib/api.ts` plus named, reviewed exceptions.

Both are cheap and have concrete, already-enumerated violation lists above to fix as part of landing the test — not purely aspirational rules.

## What's genuinely still open (the dedicated session's job)

- Mechanism choice for each boundary: `forbidigo` vs. a custom Go test vs. `depguard` vs. a JS/TS equivalent for the frontend rule.
- The exact allowlist/exception list for each boundary (e.g. is `cmd/nanite/main.go`'s provider import a sanctioned composition-root exception, or should it route through something else).
- CI wiring — where these run, and whether they block merge or just report.
- Finding 3's reframed version (`internal/runtime/agent/` process-spawn assertion) — worth doing, cheap, but not written yet.
- Finding 4 stays blocked on `TASKS/plugin-system/02`–`03` landing before an enforcement test has anything real to check.
- `CLAUDE.md`'s stale `internal/provider/` reference — a known documentation staleness, not fixed in this pass (out of scope for `docs/engineering/architecture/`).
