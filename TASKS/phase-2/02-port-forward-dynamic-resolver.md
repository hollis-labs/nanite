# Port forward the `cmd`/`http` dynamic-resolver capability as a first-class launching-time mechanism

**Phase:** 2
**Status:** implemented
**Depends on:** none functionally, but land before `02-retire-boot-profile-catalog.md` deletes the source implementation this task ports from
**Touches:** `internal/bootprofile/requirements.go`/`slots.go`/`agentcontext_adapter.go` (source implementation, read-only reference — the package itself is deleted by `02`, not this task), new home for the resolver capability (likely `internal/context` or `internal/runtime/agent`, a real design decision — see What to do), new migration if resolver definitions become DB-configurable per this task's scope

## Context

Architecture doc `02-agent-launching.md`: *"Two pieces carry forward as first-class, DB-configurable mechanisms available to every agent (not gated behind a separate catalog): The `cmd`/`http` dynamic-resolver capability (fetch live data at launch time and fold it into assembled context)."* Decision log §7: *"genuinely valuable, real runtime flexibility ('the callback idea... meant it was flexible at runtime too'). Decision: carry this forward as a first-class, DB-configurable part of launching-time context assembly, available to every agent — not YAML-file-based, not gated behind opting into a separate catalog system."*

### Current implementation, verified — actually four deferred-resolution kinds, not two

`internal/bootprofile/requirements.go:89` and `slots.go:112` both switch on **four** kinds: `"cmd"`, `"http"`, `"role_summary"`, `"skill_index"` — not just the two the architecture doc names. `role_summary`/`skill_index` are likely already-solvable through the new construction model's own data (a role's summary, an agent's skill catalog) rather than needing a general-purpose dynamic-fetch mechanism — confirm this during implementation rather than assuming they need porting too. This task's explicit scope is `cmd`/`http` only, per the architecture doc; if `role_summary`/`skill_index` turn out to have no equivalent in the new model, flag that as a real gap rather than silently dropping capability, but don't expand this task to rebuild them without confirming they're actually still needed.

`agentcontext_adapter.go:53`'s `sharedResolverEnv` and `:139`'s `requirementResolverEnv` wire the deferred-resolution execution environment — these are the actual `cmd`-exec / `http`-fetch implementations this task ports forward. `resolveResolverWorkdir` (`requirements.go:154`) resolves the working directory a `cmd`-kind resolver runs in — carry this concept forward too, a resolver needs a real, safe execution context.

## What to do

1. Design where this capability lives once it's "available to every agent, not gated behind a separate catalog system" — likely a new table (e.g. `agent_context_resolvers`: `agent_id` or `role_id` FK, `kind` (`cmd`/`http`), `spec` (command/URL + args), `slot_name`) resolved at launch time by whatever assembles an agent's boot context (the same seam `01`'s `runtime_kind` check and `04`'s post-compaction re-read both touch — coordinate, this is launching-time context assembly generally, not three unrelated mechanisms).
2. Port the actual `cmd`-exec and `http`-fetch execution logic from `agentcontext_adapter.go`, preserving its safety properties (timeout, working-directory scoping, error handling — read the source in full before assuming a naive reimplementation is equivalent).
3. Confirm whether `role_summary`/`skill_index` need porting (see Context) — document the finding in this file's Work Log either way.
4. Build minimal DB-CRUD/API surface for defining a resolver on an agent (or role, if that's the right binding level per the cascade) — coordinate with `TASKS/phase-1/09-build-assignment-ui-api.md` if UI exposure is wanted in this pass, or leave API-only if the UI can follow later (a judgment call — note the choice).

## Done means

- A real `cmd`- or `http`-kind resolver can be configured for an agent (DB-backed, not YAML) and its output is verifiably folded into that agent's assembled launch context — exercised in a real session.
- Any prior boot-profile-catalog-configured resolver (check `examples/boot-profiles/` and any real, currently-used catalog entries before `02` deletes them) has an equivalent DB-configured resolver post-migration, with no loss of the specific data it was fetching.
- The `role_summary`/`skill_index` disposition (ported or confirmed unnecessary) is documented in this file's Work Log.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log

### Design decision: new home is `internal/runtime/agent`, not `internal/context`

Chose `internal/runtime/agent` (new file `context_resolver.go`) over `internal/context` (the Context Broker / slot package). Reasons:

- The Context Broker's slot system (`internal/context/slot.go`'s `SlotOrder`) has six load-bearing invariants (`internal/context/INVARIANTS.md`, enforced by `internal/service/slot_invariants_test.go`) around a fixed, positionally-stable set of per-turn slots for the API-based dispatch path. Adding a new slot there would be a structural change to that invariant set, out of this task's scope and risking a regression the invariants doc explicitly guards against.
- "Launch time" (per the architecture doc's own phrase, "fetch live data at launch time") is a CLI-boot-specific concept today — the only place Nanite currently has a "boot prompt assembled once, planted into a file the process reads" mechanism is `internal/runtime/agent/prompt.go`'s `composeSystemPrompt`/`resolveBootPrompt`/`ResolveSystemPrompt`. That's also the exact seam `TASKS/phase-2/03-mandatory-post-compaction-reread.md` is pointed at, confirming this is the intended "launching-time context assembly" seam the task description's coordination note refers to.
- The actual cmd/http execution logic itself doesn't need a new home at all — it already lives in the shared, already-vendored `github.com/hollis-labs/agentkit` module (`agentcontext` + `agentcontext/resolvers`). `internal/bootprofile/agentcontext_adapter.go` never reimplemented cmd-exec/HTTP-fetch; it was a thin `SlotSpec`-conversion adapter over that shared package. `internal/runtime/agent/context_resolver.go` is the same shape of adapter, aimed at `store.AgentContextResolver` rows instead of boot-profile `Requirement` structs — genuinely reusing the safety properties (cmd timeout + 5-minute hard ceiling, stderr-tail capture, HTTP timeout + 1 MiB body cap, status-code validation), not re-implementing them.

### What was built

1. **Migration** `internal/store/migrations/118_agent_context_resolvers.sql` — `agent_context_resolvers` table: `agent_id` FK → `agent_profiles(id)` ON DELETE CASCADE, `slot_name`, `kind` (`cmd`|`http`, CHECK-constrained), `run`/`cwd`/`timeout` (cmd), `url`/`headers_json`/`response_format`/`json_path` (http), `enabled`, timestamps. `UNIQUE(agent_id, slot_name)` so two resolvers can't race on the same fold-in target. Tested against a real backup DB copy (see below).
2. **Store CRUD** `internal/store/agent_context_resolvers.go` — `AgentContextResolver` struct + `Insert/Get/List/ListEnabled/Update/Delete`, following the `roles.go`/`agent_reflexes.go` established pattern (ULID ids, sentinel not-found error, Go-layer validation mirroring `validateRoleFields`). `ListEnabledAgentContextResolvers` is the boot-time read path (only `enabled=1` rows); `ListAgentContextResolvers` is the full CRUD/operator view.
3. **Resolution logic** `internal/runtime/agent/context_resolver.go` — `ResolveContextBlocks(ctx, []store.AgentContextResolver, workdir) (map[string]string, error)`: converts each row into an `agentcontext.SlotSpec`, wires the shared `resolvers.NewCmdResolver()`/`NewHTTPTextResolver()`/`NewHTTPJSONResolver()` into an `agentcontext.NewProvider(...)`, calls `Assemble`, and promotes the first per-slot resolver error to a hard failure — mirroring `bootprofile.assembleRequirements`'s exact "a half-resolved boot prompt is worse than a clean stop" philosophy. `role_summary`/`skill_index` are deliberately not wired (see disposition below).
4. **Options + prompt composition** `internal/runtime/agent/agent.go`/`prompt.go` — new `Options.DynamicContext map[string]string` field; `resolveBootPrompt`/`ResolveSystemPrompt` now append each non-empty block as a `## Dynamic context: <slot>` section (sorted by slot name for determinism) AFTER the role-derived-or-overridden base prompt, so the resolved data folds into the assembled context for every layout (claude/codex/opencode all route through `resolveBootPrompt`) regardless of whether a `BootPromptOverride` is also present.
5. **Chat-service wiring** `internal/service/chat_boot_drive.go` — `resolveAgentContextForBoot(ctx, agentID, workdir)` loads+resolves an agent's enabled resolvers and is called from `driveBootSession`'s cold-boot branch (unconditionally for every agent with `agent.ID != ""` — not gated behind a bootprofile session, satisfying "available to every agent"); result is threaded into `bootOpts.DynamicContext` and stashed in a new `chatServiceImpl.activeSessionContextBlocks sync.Map` (mirrors `activeSessionLaunchSpecs`'s pattern, cleared in `CloseAgentSession`). `regenerateBootDirSlots` (the mid-session CLAUDE.md regen path) reads the same stash so a slot-triggered regen doesn't silently drop the resolved content. Added `Store.ListEnabledAgentContextResolvers` to the narrow `service.Store` interface (`internal/service/store.go`) so `chatServiceImpl` can call it through its existing `store Store` field.
6. **REST API** `internal/api/agent_context_resolvers.go` + `types.go` additions + route registration in `api.go` — `GET/POST /api/agents/{id}/context-resolvers`, `GET/PATCH/DELETE /api/agents/{id}/context-resolvers/{resolverId}`, following the `reflexes.go`/`roles.go` handler pattern exactly (`requireAgent`/`requireMutableAgent`, cross-agent-ownership guard on PATCH/DELETE).

### `role_summary`/`skill_index` disposition — confirmed NOT needed, not silently dropped

Per this task's explicit requirement to document the finding either way: confirmed during implementation that both have real structural equivalents in the Phase 1 construction model, so no porting is needed and no capability is lost:

- **`role_summary`** (pre-port: read a role markdown file's content/section at launch time) is superseded by `roles.system_prompt` (`internal/store/roles.go`, landed in `TASKS/phase-1/01-add-roles-table-and-cascade-resolution.md`) — the role's system prompt is now first-class DB data, already part of the role → agent → task cascade, with zero dynamic-fetch step required. The pre-port mechanism was working around role content living only in a file; that's no longer true.
- **`skill_index`** (pre-port: filesystem-scanned a skills directory and rendered a discovery table) is superseded by the `agent_skills` FK join (`TASKS/phase-1/05-fix-agent-skills-and-agent-projects-fks.md`, architecture doc's "Relational references replace free-text strings" section) — an agent's skill catalog is now a direct DB query, not something that needs re-discovering via a dynamic resolver at every boot.

Both were also verified to have zero live usage in Nanite's own actual boot-profile catalog (see below) — there was no real Nanite session depending on either kind.

### Prior boot-profile-catalog-configured resolver data-loss check

Checked both `examples/boot-profiles/` and the catalog Nanite's own live config actually points at:

- `examples/boot-profiles/boot-profiles/*.yaml` (the repo's example catalog, wired into `TestBootProfileSmoke_*`) uses **only `text` and `static`** slot types — zero `cmd`/`http`/`role_summary`/`skill_index` entries.
- `~/.config/nanite/config.yaml`'s `boot_profile_catalog_path` on this operator's machine points at exactly that same `examples/boot-profiles` directory — i.e. Nanite's own real, currently-configured catalog is the example catalog, and it has no deferred-resolver slots at all.
- `~/.tether/catalog/boot-profiles/` (Tether's own catalog directory, the pattern's origin per `GLOSSARY.md`) *does* contain real `cmd`/`http`/`skill_index` entries (e.g. `fragments-engine.backend.main.yaml`'s `history`/`status` cmd slots, `recap`/`memory` http slots with a Tether-specific `response_format: tesseract_recall` Nanite's schema doesn't even model) — but this is a different application's own catalog, not consumed by Nanite's `boot_profile_catalog_path`, and per the architecture doc cross-app portability with Tether is "deliberately opt-in, not a structural default." Nothing there is a Nanite-side data-loss risk.

Conclusion: there is no prior Nanite-side cmd/http resolver whose specific fetched data needs an equivalent DB row — the Done-means bullet is satisfied vacuously (nothing to migrate) rather than by a migration script, and that finding is the actual answer, not an assumption.

### API-only, no UI (judgment call)

`TASKS/phase-1/09-build-assignment-ui-api.md` (the natural place a resolver-assignment UI would live) is itself `not-started` as of this task, so there is no established assignment-UI convention to extend yet. Built the REST CRUD surface only, matching this task's own "or leave API-only if the UI can follow later" option. A future UI pass can consume `GET/POST/PATCH/DELETE /api/agents/{id}/context-resolvers` directly.

### Migration tested against a real backup

Copied `~/.local/share/nanite/workspaces/default/backups/main.db.pre-execution-backup-20260818-132726` into an isolated scratch directory (never the tracked working tree), ran the full embedded migration chain (`store.New`) against the copy via a throwaway, manually-invoked test (`internal/store/zz_migration_backup_verify_test.go`, deleted after use — not shipped). Result: the full chain (including every Phase 0/1 rename/drop migration) applied cleanly, found a real pre-existing `agent_profiles` row (`slug=default`), inserted/round-tripped/deleted an `agent_context_resolvers` row against it successfully — the new table's FK and validation work against real historical data, not just an empty fixture. Ran `git status --short` immediately after (and again after deleting the scratch DB copy + throwaway test file) — clean both times, no accidental writes into the tracked tree. This migration does not touch `AgentConfigService`/`writeManaged` or any `.nanite/agents/*.md` file-write path, so the stricter isolated-whole-tree-copy requirement for that specific risk class did not apply; the scratch-copy-of-the-DB-file precaution was still followed.

### "Exercised in a real session" — real cmd execution + real file output, not a live CLI subprocess

Could not spawn a real Claude/Codex CLI subprocess in this sandboxed dispatch (no provider credentials, no interactive terminal). Instead, `internal/service/chat_boot_drive_context_resolver_test.go` exercises the complete real pipeline `driveBootSession`'s cold-boot branch runs, against a real (temp-dir-isolated) `*store.Store` and a real shell command (not mocked):
`agent_context_resolvers` DB row → `resolveAgentContextForBoot` (real `sh -c` exec via the shared agentkit `CmdResolver`) → `activeSessionContextBlocks` stash (same `Store` call `driveBootSession` makes) → `regenerateBootDirSlots` (the exact function that plants/replants `CLAUDE.md` for a live CLI session) → real file bytes on disk, asserted to contain both the agent's own system prompt and the resolved `## Dynamic context: weather` / `72F-and-sunny` block. This is the same code path a real session boot runs; the only untested hop is the CLI subprocess actually reading the resulting file, which is unrelated to this task's mechanism.

### Tests added

- `internal/store/agent_context_resolvers_test.go` — CRUD round-trip, defaults, `enabled` boot-time filtering, per-agent slot-name uniqueness, Go-layer validation rejections, `DeleteAgent` cascade.
- `internal/runtime/agent/context_resolver_test.go` — real `cmd` exec (success + non-zero-exit-names-the-slot), real `httptest` HTTP text + JSON+JSONPath, non-2xx-names-the-slot, unsupported-kind rejection, malformed-timeout rejection, multi-slot ordering.
- `internal/runtime/agent/streaming_stdio_frame_test.go` — extended `TestResolveSystemPrompt` with a dynamic-context-blocks subtest (append after base prompt, blank-content block omitted); updated the 3 pre-existing call sites for the new `dynamicContext` parameter.
- `internal/api/agent_context_resolvers_test.go` — full REST CRUD round trip + bad-kind 400 + cross-agent PATCH/DELETE rejection.
- `internal/service/chat_boot_drive_context_resolver_test.go` — `resolveAgentContextForBoot` against a real store (enabled-only filtering, no-resolvers-configured no-op, resolver-failure-aborts-with-named-slot), and the full DB-row-to-CLAUDE.md-bytes end-to-end proof described above.

### Build/vet/test

- `go build ./cmd/nanite/` — pass.
- `go vet ./...` — pass except two pre-existing warnings in `internal/service/container.go` (`stopReaper`/`stopRuntimeReaper` possible-context-leak) confirmed via `git stash` to predate this task's changes entirely — unrelated.
- `go test ./...` — all packages pass, including every new test above.

### Known scope boundary (not a gap, a deliberate boundary)

Resolution runs once at a chat session's initial cold boot (`driveBootSession`'s `sess == nil` branch) — "launch time," per the architecture doc's own framing, not "every turn." A recovery-broker crash-relaunch reuses the `Options` (including `DynamicContext`) tracked at the original boot via `agentBootDirAdapter.Track`, rather than re-resolving fresh data on every relaunch; this matches how the pre-existing slot-regen/BootPromptOverride mechanisms already behave for a relaunch and was not something Done-means asked this task to change. The standalone `nanite launch` CLI (`internal/launcher`) was not wired — Done-means asks for this to be "exercised in a real session," which is the chat-service boot path; the launcher's own boot-profile-driven `BootPromptOverride` assignment is untouched and out of this task's scope.

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
