# PTY naming scrub

**Phase:** 0
**Status:** not-started
**Depends on:** none
**Touches:** ~90 files across `cmd/nanite/`, `internal/api/`, `internal/bootprofile/`, `internal/chat/`, `internal/launcher/`, `internal/messaging/`, `internal/mcpserver/`, `internal/plugin/`, `internal/runtime/agent/` (root files only, not the `recovery/` subpackage), `internal/service/`, `internal/skill/`, `internal/store/`, `internal/subagent/`, `pkg/models/registry.go`, `config/nanite.yaml`, `examples/boot-profiles/launches/claude-smoke.yaml`, `AGENTS.md`, `CLAUDE.md`, and `ui/src/{components/chat,components/settings,lib,__tests__}/*`. Full file list in "What to do" below — every file is listed, not a sample, per the no-partial-rename requirement. Explicitly **excludes** `internal/background/*` (a different, unrelated subsystem — see Context) and any symbol whose definition lives in the external `agentkit`/`go-providers` Go modules (also see Context).

## Context

TASKS.md Phase 0 "Renames" item 25 (canonical slug for this file: `31-rename-pty-naming-scrub`): "PTY naming scrub (`shouldUsePTY`, `IsPTYProvider`, the `pty-*` provider-string convention) — the *name* can be fixed now, independent of `runtime_kind`'s full migration (still real Phase 2 work). Rename now; redesign the mechanism later."

`docs/engineering/architecture/02-agent-launching.md` ("CLI-vs-API routing is an explicit typed field"): `agents.runtime_kind` (`cli`/`api`) is the **Phase 2** replacement for "a fragile four-site string-prefix convention (`chat.IsCLIProvider`/`NormalizeCLIProvider`, `shouldUsePTY`, `agent_deps.go`'s `stripRegistryPrefix`, plus the boot-profile catalog's own layered `pty-` prefix convention)... Part of this work: fully scrub the remaining 'PTY' naming."

`docs/engineering/GLOSSARY.md`'s **CLI-based subprocess** entry is the authoritative definition to rename toward: "a long-lived (Claude, via `StreamingStdio` — NDJSON over stdin/stdout pipes) or per-turn (Codex, OpenCode) subprocess Nanite spawns and manages. **There is no real pseudo-terminal anywhere in the current runtime.** 'PTY' is a naming fossil surviving in a few provider-name strings and function names (`shouldUsePTY`, `pty-*` provider aliases) that are being scrubbed. If you see 'PTY' in an older doc or comment, mentally substitute 'CLI-based subprocess'."

`docs/engineering/architecture/02-agent-launching.md`'s "CLI-based subprocess launching" section reiterates: "Nanite has never run agents through a real pseudo-terminal in the current design — that approach was abandoned for `StreamingStdio`... or a fresh subprocess per turn (Codex, OpenCode)."

**Two things this task's own framing doesn't tell you, verified directly against the repo — read before starting:**

### 1. This is a NAME-only change at the Go-identifier/comment/UI-label level. It is explicitly NOT a change to the persisted provider-string values.

The `pty` / `pty-claude` / `pty-codex` / `pty-opencode` / `pty-gemini` / `pty-copilot` / `pty-aider` strings are not just internal variable names — they are the literal values stored in `providers.provider_type` (seeded in `internal/store/seed.go`) and in live `sessions.provider` column rows for any session ever started against a CLI adapter. Renaming these literal string values (e.g. `"pty-claude"` → `"cli-claude"`) would silently orphan every already-persisted session row — `NormalizeCLIProvider`/`IsCLIProvider` would stop recognizing them, breaking recovery, resume, and provider-badge rendering for old sessions, and would require a companion `UPDATE sessions SET provider = ...` data migration to avoid doing so. That's a materially bigger, riskier change than "rename now; redesign the mechanism later" describes, and decision log/architecture docs assign the *actual* replacement of this string convention to Phase 2's `runtime_kind` field, not this task.

**Default scope for this task: rename Go/TS identifiers, doc comments, and user-facing UI label text away from "PTY" — do NOT change the literal `"pty"`/`"pty-<x>"` string constants used as provider-type values in seed data, DB rows, or wire payloads.** If you disagree after reading the actual call sites (e.g. you find a specific spot where the literal string can be safely renamed without a data-migration cost), escalate rather than guessing — this is exactly the kind of ambiguity `EXECUTION-PROCESS.md` wants surfaced, not silently resolved either way.

### 2. Not everything matching `/PTY/i` in this repo is the CLI-launching fossil. Two things must NOT be touched by this task:

- **`internal/background/pty.go`, `internal/background/pty_test.go`, `internal/background/types.go`, `internal/background/service.go`** — this is the **P9 BackgroundJob primitive** (CW-20260420-0016), a completely different subsystem (detached shell-command dispatch, not agent launching). Its own doc comment is explicit: *"Despite the name, this MVP does NOT allocate a real PTY... The 'PTY' naming is preserved from the ticket's D2 language — the backend swap to agent-mux (D3) replaces this whole file rather than the type name."* That file has its own "Decision pins (do NOT reopen)" — renaming `PTYBackend` here is out of scope and would fight a separate, still-open ticket's naming convention. `internal/plugin/events.go`'s `Shell Events (User Shell feature — wired with Task 2 PTY shell tab)` comment is very likely the same unrelated feature — verify before touching, and if in doubt, leave it.
- **`runtimekind.PTY` / `runtimekind.PTYDebug`** (`internal/api/frontend_readiness.go` lines 445-446) and **`Caps.PTY`** (the boolean field set in `internal/runtime/agent/factory.go:145`) are **not Nanite symbols** — they're defined in the external `github.com/hollis-labs/agentkit` module (`agentruntime/runtimekind`, `agentsessions.Caps`), versioned and vendored at `libs/agentkit` as a sibling repo, consumed via a normal Go module dependency (not a local `replace` in this repo's `go.mod`). Renaming an external module's exported symbol is a cross-repo change outside this task's scope — reference it under its existing name. What you **can** rename in this repo: the hardcoded, Nanite-authored display strings around it — e.g. `Label: "PTY"` / `Label: "PTY debug"` in `frontend_readiness.go` (lines 445-446) are just Go string literals Nanite chose for a UI dropdown; reword those away from "PTY" without touching `runtimekind.PTY`/`runtimekind.PTYDebug` themselves.
- Similarly, `provider.CLIAdapter`, `provider.NewClaudeAdapterStreamingStdio`, etc. live in the external `github.com/hollis-labs/go-providers` module (also vendored at `libs/go-providers`, also a real module dependency not a local `replace`) — same rule: reference, don't rename.

**Doc/reality mismatch worth flagging to the Orchestrator, not silently fixed here:** this project's own `CLAUDE.md` (both `/Users/chrispian/.claude` root and this repo's) and `AGENTS.md` list `internal/provider/` as a real package ("LLM provider abstractions (Anthropic, OpenAI, Ollama, PTY bridge)"). **`internal/provider/` does not exist in this repo** — provider adapters were extracted to the external `go-providers` module at some point and the architecture-summary docs were never updated. Out of scope to fix as part of this rename (it's an unrelated doc-staleness issue, not a PTY-naming one), but real and worth a one-line note back to the Orchestrator.

## What to do

1. **Rename the two named functions the task explicitly calls out**, preserving current behavior exactly (do not invert booleans, do not change what a caller passes/receives):
   - `internal/chat/engine.go:261` — `func IsPTYProvider(name string) bool { return name == "pty" || strings.HasPrefix(name, "pty-") }`. This is narrower than the existing `IsCLIProvider` (line 256-258, which also matches `sub-`-prefixed names) and is called from exactly two sites, both gating "PTY observability" event emission: `internal/service/chat_generate.go:659` and `:2034`. Before renaming, check whether any current adapter is actually registered under a `sub-`-prefixed alias (`internal/store/seed.go`, `internal/api/provider_manage.go`) — if none is, `IsPTYProvider` and `IsCLIProvider` are behaviorally identical today and collapsing the two call sites onto `IsCLIProvider` is a reasonable bonus simplification, but not required. At minimum, rename `IsPTYProvider` to something that doesn't say PTY and accurately describes "the legacy pty-aliased provider family" — e.g. `IsCLIAliasedProvider` or `IsPTYAliasProvider` is a fallback if you determine the pty-vs-sub distinction is still meaningful and want to preserve it in the name; pick one and document the reasoning in the Work Log rather than silently choosing.
   - `internal/runtime/agent/factory.go:59` — `func shouldUsePTY(providerName string, mode Mode) bool`. **Read this carefully before renaming — the obvious analogy from the task brief (`shouldUsePTY` → `shouldUseCLISubprocess`) is semantically wrong and will invert the function's meaning if applied naively.** `shouldUsePTY` decides whether `cfg.Caps.PTY` (the external agentkit field — see above) should be set `true`; `Caps.PTY == true` selects the long-lived *raw-terminal* runtime, and `Caps.PTY == false` (the current, permanent value for every supported adapter per `bootdir_alias_test.go`'s `Test_shouldUsePTY`/"no provider currently requires a PTY" contract) is what causes `StreamingStdio` (the CLI-based-subprocess mechanism) to be selected instead. So `shouldUsePTY`'s **true** branch is the raw-terminal path, not the CLI-based-subprocess path — naming it `shouldUseCLISubprocess` would describe the false branch as if it were the true one. Rename to something that preserves the actual meaning without saying "PTY" — e.g. `shouldAllocateRawTerminal` or `shouldRequestRawTerminalRuntime`. Its single call site is `factory.go:145`: `cfg.Caps.PTY = shouldAllocateRawTerminal(providerName, mode)` (field name `Caps.PTY` stays — external symbol).
   - Update every comment, test name, and error string that references these two functions by their old name (both files' own `_test.go` counterparts, plus `internal/chat/engine_test.go`, `internal/runtime/agent/bootdir_alias_test.go`, `internal/runtime/agent/agent_test.go`, `internal/runtime/agent/boot_test.go`, `internal/service/agent_deps.go:409`, `internal/api/provider_manage.go:104`, `cmd/nanite/main.go:779`).

2. **Scrub "PTY"/"Pty" from comments, doc-strings, and struct/var names Nanite itself owns** (not the two functions above, not external-module references) across the full file list below. This is the bulk of the task — mostly doc comments and a few local variable/type names. Full list of files containing `PTY`/`Pty`/`pty-`/`pty_` (verified via case-sensitive + prefix greps, `internal/background/*` already excluded, `mpty`/"empty" false positives already filtered out):

   ```
   cmd/nanite/main.go, cmd/nanite/main_test.go
   config/nanite.yaml
   examples/boot-profiles/launches/claude-smoke.yaml
   internal/api/durable_agents_test.go, internal/api/frontend_readiness.go, internal/api/harness_v1.go,
     internal/api/harness_v1_test.go, internal/api/meta_harnesses_test.go, internal/api/provider_manage.go,
     internal/api/providers.go, internal/api/providers_test.go
   internal/bootprofile/compile_for_test.go, internal/bootprofile/compiler.go, internal/bootprofile/compiler_test.go,
     internal/bootprofile/launchplan_bridge_test.go, internal/bootprofile/loader_test.go, internal/bootprofile/profile.go,
     internal/bootprofile/registry.go, internal/bootprofile/registry_test.go
   internal/chat/engine.go, internal/chat/engine_test.go, internal/chat/proctrack.go
   internal/dispatcher/dispatcher.go
   internal/launcher/launcher.go
   internal/mcpserver/handlers.go
   internal/messaging/events.go, internal/messaging/events_test.go
   internal/plugin/events.go   (verify the "PTY shell tab" comment is this feature, not internal/background's — see Context)
   internal/runtime/agent/agent.go, internal/runtime/agent/agent_test.go, internal/runtime/agent/boot_test.go,
     internal/runtime/agent/bootdir.go, internal/runtime/agent/bootdir_alias_test.go,
     internal/runtime/agent/bootdir_claude.go, internal/runtime/agent/bootdir_claude_test.go,
     internal/runtime/agent/deps.go, internal/runtime/agent/doc.go, internal/runtime/agent/factory.go,
     internal/runtime/agent/manager.go, internal/runtime/agent/manager_test.go, internal/runtime/agent/orphan_sweep.go,
     internal/runtime/agent/prompt.go, internal/runtime/agent/recovery/doc.go (comment only — do not move this file,
     that's task 32's job), internal/runtime/agent/sandbox_content_claude.go, internal/runtime/agent/workspace.go
   internal/service/agent_deps.go, internal/service/chat.go, internal/service/chat_boot_drive.go,
     internal/service/chat_boot_drive_launch_spec_test.go, internal/service/chat_bootprofile_recovery_test.go,
     internal/service/chat_bootprofile_resolve.go, internal/service/chat_bootprofile_resolve_test.go,
     internal/service/chat_bootprofile_smoke_test.go, internal/service/chat_generate.go,
     internal/service/chat_generate_bootprofile_route_test.go, internal/service/chat_generate_cli_bypass_test.go,
     internal/service/chat_http_broker_notify.go, internal/service/chat_http_broker_notify_test.go,
     internal/service/chat_resolve_provider_test.go, internal/service/chat_test.go, internal/service/container.go,
     internal/service/durable_agent_recipes.go, internal/service/pty_observability_test.go (also rename the FILE,
     e.g. to cli_observability_test.go — it's the test for the events.go rename in step 3),
     internal/service/recovery_credentials.go, internal/service/recovery_credentials_test.go,
     internal/service/subagent_runner.go, internal/service/subagent_runner_boot.go,
     internal/service/subagent_runner_boot_test.go, internal/service/subagent_runner_test.go
   internal/skill/context.go
   internal/store/execution_metrics.go, internal/store/execution_metrics_test.go, internal/store/seed.go (seed.go's
     provider_type STRING VALUES stay per point 1 above — only rename its Go-side comments/var names, e.g.
     ptyProviderID is a local Go variable name, not a persisted value, and can be renamed)
   internal/subagent/service.go, internal/subagent/service_test.go, internal/subagent/types.go
   pkg/models/registry.go   (comment "PTY CLIs" only — Provider: "pty-codex" etc. are the seeded string values, stay)
   ui/src/__tests__/phase-10-sidebar-session-presence.test.tsx, ui/src/__tests__/phase-11-start-surface.test.tsx,
     ui/src/__tests__/phase-12-session-details-panel.test.tsx
   ui/src/components/chat/AdapterBadge.tsx, ui/src/components/chat/ComposerToolbar.tsx
   ui/src/components/settings/MetaHarnessManager.tsx, ui/src/components/settings/ProviderManager.tsx,
     ui/src/components/settings/observability/RecentExecutionsTable.tsx
   ui/src/lib/sidebar-session.ts, ui/src/lib/types.ts
   AGENTS.md, CLAUDE.md   (the "internal/provider/... PTY bridge" line — see doc/reality mismatch above; fine to
     reword "PTY bridge" → "CLI-based subprocess bridge" here even though internal/provider/ itself doesn't exist)
   ```

3. **UI label text (Nanite-owned, safe to reword without touching external enums or wire strings):**
   - `ui/src/components/chat/AdapterBadge.tsx` — `PTY_CONFIG`, `label: 'PTY'`, `'CLI (PTY) session'` title strings → reword to something like "CLI" / "CLI (subprocess) session".
   - `ui/src/components/chat/ComposerToolbar.tsx` — `isPty` variable/prop naming, `{group.isPty ? "PTY" : "API"}` badge text.
   - `ui/src/components/settings/ProviderManager.tsx`, `MetaHarnessManager.tsx`, `RecentExecutionsTable.tsx` — any `'PTY'`-labeled UI text; the `'pty-claude'` etc. dictionary **keys** are wire-string values (stay, per point 1), only literal display labels change.
   - `internal/api/frontend_readiness.go:445-446` — reword `Label: "PTY"` / `Label: "PTY debug"` (the `Value:` fields stay as `string(runtimekind.PTY)`/`string(runtimekind.PTYDebug)` — external enum, unchanged).

4. **Event name constants** — `internal/messaging/events.go:44-46`:
   ```go
   EventPTYTurnStart    = "pty_turn_start"
   EventPTYTurnComplete = "pty_turn_complete"
   EventPTYTurnFailed   = "pty_turn_failed"
   ```
   These are written via `event_log`/messaging-layer diagnostics (see `internal/service/chat_generate.go:150,654,657,2067-2068` for the emit sites, and `internal/messaging/events_test.go:179-202` for the tests) and read back by `session_diagnose`-style tooling. Per the standing "no live production traffic" constraint and aggressive dead-code policy (decision log §8), both the Go constant names and their string values can be renamed cleanly (e.g. `EventCLITurnStart = "cli_turn_start"`) — there's no external consumer depending on the literal string surviving. Rename both; update all emit/read/test call sites listed above.

## Done means

- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- `cd ui && npm run build` passes (or the frontend-equivalent lint/typecheck if build is slow to iterate on).
- `grep -rnw "PTY" --include="*.go" --include="*.ts" --include="*.tsx" cmd internal ui/src config examples pkg` (excluding `internal/background/*`, and excluding literal references to the external `runtimekind.PTY`/`runtimekind.PTYDebug`/`Caps.PTY` symbols, which legitimately still say PTY because they're not this repo's to rename) returns nothing.
- `shouldUsePTY` and `IsPTYProvider` no longer exist under those names anywhere in the repo; their replacements preserve identical behavior (confirm via the existing test suites for both, renamed but not logic-changed).
- The literal provider-type strings (`"pty"`, `"pty-claude"`, `"pty-codex"`, etc.) used as DB/seed values are **unchanged** — verify by diffing `internal/store/seed.go`'s actual string literals against `git show HEAD:internal/store/seed.go` before/after.
- `internal/background/pty.go` and its test are untouched (`git diff` shows no changes there).
- A one-line note is surfaced to the Orchestrator about the `internal/provider/` doc/reality mismatch in `CLAUDE.md`/`AGENTS.md` (not fixed here, just flagged — it's an architecture-doc staleness issue, not a PTY-naming one).

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
