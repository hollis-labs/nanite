# Set the default `runtime_kind` (app default CLI, overridable system-wide and per-agent)

**Phase:** 8
**Status:** not-started

## Superseded framing — operator decision, 2026-08-19

**This task previously asked for a live CLI-vs-API experiment against the real Curator wake path** (requiring explicit, live operator sign-off before dispatch — see git history for the original text). That framing is retired. The operator had a separate agent do a dedicated review of the CLI vs. API paths and concluded: **keep both paths** — after the Phase 2-5 work, they're much closer in behavior than originally assumed, and the open architecture question ("should durable agents eventually run CLI-based instead of API-based") is no longer the thing to resolve. There is no live-system experiment left to run, and no sign-off requirement — this is now an ordinary, low-risk settings/config task.

**The real remaining task**: make the CLI-vs-API choice explicitly configurable at three tiers — the app has its own built-in default (**CLI**), the operator can override that system-wide, and any individual agent can still override both. Update this task and the architecture docs to reflect that.

**Depends on:** none functionally (Phase 2's `runtime_kind` wiring and Phase 2's boot-profile-catalog retirement — both already landed, see `TASKS/phase-2/01-wire-runtime-kind-routing.md` and `TASKS/phase-2/04-retire-boot-profile-catalog.md`).
**Touches:** `internal/store/user_settings.go` (`UserSettings` struct + its migration — add the system-wide default), `internal/store/migrations/117_agent_profiles_composition_columns.sql` (already-nullable `agent_profiles.runtime_kind` — the per-agent override tier, no schema change needed there), `internal/service/chat.go`'s `classifyNilProvider` (`chat.go:1321`, the actual routing decision), `internal/service/chat.go`'s `resolveProvider`/`tryProviderCandidate` (a separate, adjacent fallback chain — see Context).

## Context

### The three tiers this task builds, and what already exists for each

1. **App default** — hardcoded, ships with the binary: **CLI**. Nothing to build except making sure the cascade below actually falls through to this value when nothing else is set — there is no existing "app default" constant for this today.
2. **System-wide override** — does not exist yet. `internal/store/user_settings.go`'s `UserSettings` struct already carries this exact shape of setting for other concerns (`DefaultProvider`, `DefaultModel`, `DefaultAgent`, `UtilityProvider`, etc. — single-user app, so "system-wide" and "user settings" are the same table). Add a `DefaultRuntimeKind string` field (`cli` | `api` | `""` = inherit the app default) the same way those existing fields work, with its own migration.
3. **Per-agent override** — **already exists and needs no new schema.** `agent_profiles.runtime_kind` (migration `117_agent_profiles_composition_columns.sql:37-38`) is already `TEXT CHECK (runtime_kind IS NULL OR runtime_kind IN ('cli', 'api'))` — nullable by design, specifically so NULL can mean "inherit." Every one of the 28 real agent rows in the current production backup has a non-NULL, backfilled `runtime_kind` (verified during Phase 2 `01`'s own work — see that task's Work Log), all currently `'api'`. Decide during implementation whether to leave those backfilled values as-is (explicit per-agent `'api'` pins, which will now diverge from the new CLI app default until an operator explicitly changes them) or re-null them so they pick up the new default — this is a real, visible behavior-change decision, not a mechanical detail; **surface it to the operator rather than picking silently**, since re-nulling would flip 28 real agents from API to CLI runtime on next boot.

### The actual routing decision, and a piece of dead-weight to resolve

`internal/service/chat.go:1321`'s `classifyNilProvider(runtimeKind, providerName string)` is the real CLI-vs-API decision point (Phase 2 `01` converted the four/five old string-prefix call sites down to this one). Its current gate is `runtimeKind != "cli" && !chat.IsCLIProvider(providerName)` — an **OR** with the legacy provider-name-string check, deliberately not a straight replacement, because at the time Phase 2 `01` landed the boot-profile catalog was still live and could force CLI routing independent of `runtime_kind`. **The boot-profile catalog is now fully retired** (`TASKS/phase-2/04-retire-boot-profile-catalog.md`, done) — confirm whether the legacy OR-fallback in `classifyNilProvider` is still needed for any other reason (a file-discovered agent profile with no DB row still has `RuntimeKind == ""`, per Phase 2 `01`'s Work Log — that case still exists and still needs a fallback, but the fallback should now resolve through the new three-tier cascade above, not a provider-name string heuristic). This task should replace the OR-fallback's *source of truth* for the "haven't decided yet" case with the cascade (agent → system-wide setting → app default), not necessarily delete the function.

`resolveProvider`/`tryProviderCandidate` (`chat.go`, session→agent→user-default→fallback-chain→inferred) is a separate, adjacent mechanism that also calls `chat.IsCLIProvider` internally — Phase 2 `01` explicitly deferred converting it (that was `TASKS/phase-3/01-collapse-resolveprovider-into-cascade.md`'s job). Confirm that task's current state before assuming `resolveProvider` already reads the new cascade; if it doesn't, this task needs to wire it in there too, since a user-level `default_provider` override and a user-level `default_runtime_kind` override are both first-class settings living on the same `UserSettings` row and should resolve consistently.

### No frontend work

Per the standing "no frontend work in any phase" instruction (already applied to Phase 5's `05`/`01`), this task is backend/API-surface only: the DB column, the settings read/write path, and the resolution cascade. Exposing the new setting in the Settings UI or the agent-edit UI is a separate, future frontend task — do not scope it in here.

## What to do

1. Add `DefaultRuntimeKind` to `store.UserSettings` (system-wide/user-level tier) with a migration; empty string means "inherit the app default."
2. Define the app-level default (**CLI**) as an explicit constant, consulted only when both the per-agent and system-wide values are unset.
3. Wire the three-tier cascade (per-agent `agent_profiles.runtime_kind` → `UserSettings.DefaultRuntimeKind` → app default `cli`) into `classifyNilProvider`'s "haven't decided yet" path, replacing the legacy provider-name-string OR-fallback as the source of truth for that case (confirm whether the fallback can be deleted outright now that the boot-profile catalog is gone, or whether the file-discovered-profile edge case still needs it — see Context).
4. Check `TASKS/phase-3/01-collapse-resolveprovider-into-cascade.md`'s actual landed state; if `resolveProvider` doesn't already consult the same cascade, wire it there too so the two mechanisms don't disagree.
5. Decide (with operator input, don't guess) whether the 28 already-backfilled `agent_profiles.runtime_kind = 'api'` rows stay explicit or get re-nulled to pick up the new CLI default — record the decision and its consequence explicitly in this file's Work Log.
6. Verify: an agent with no override picks up CLI by default (new behavior); an agent with an explicit per-agent `runtime_kind` keeps it regardless of the system-wide setting; a system-wide `DefaultRuntimeKind` override changes the default for every agent that hasn't pinned its own value.

## Done means

- `UserSettings.DefaultRuntimeKind` exists, is read/write, and is honored as the system-wide tier.
- The app-level default is CLI, applied only when neither the per-agent nor the system-wide value is set.
- Per-agent `agent_profiles.runtime_kind` (already existing) continues to take precedence over both other tiers, unchanged in shape.
- `classifyNilProvider` (and `resolveProvider`, if it doesn't already) consult this cascade — no remaining routing decision depends on the provider-name-string heuristic once the boot-profile-catalog-era fallback reason no longer applies.
- The 28-real-agent backfill question (stay pinned to `'api'` vs. re-null to inherit CLI) is resolved by explicit operator decision, recorded in the Work Log, not silently picked.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
