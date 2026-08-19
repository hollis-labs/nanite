# Wire `runtime_kind` as the single CLI/API routing mechanism, replacing four string-matching call sites

**Phase:** 2
**Status:** not-started
**Depends on:** Phase 1's `runtime_kind` column on `agent_profiles` (`TASKS/phase-1/02-add-agents-composition-columns.md` — backfilled per-agent, not yet consulted by anything)
**Touches:** `internal/chat/engine.go` (`IsCLIProvider`, `IsPTYProvider`, `NormalizeCLIProvider` — read/replace call sites, not necessarily delete the functions themselves, see What to do), `internal/runtime/agent/factory.go` (`shouldUsePTY`), `internal/runtime/agent/bootdir.go` (layout dispatch), `internal/service/agent_deps.go` (`stripRegistryPrefix`), `internal/service/chat.go` (`resolveProvider`, `chat_generate.go`'s CLI bypass), `internal/bootprofile/compiler.go` (the catalog's own `pty-claude`/`pty-codex`/`pty-opencode` normalization — retired by `02`, not this task, but this task must not leave it as a second live routing decision point)

## Context

Architecture doc `02-agent-launching.md`: *"`agents.runtime_kind` (`cli` | `api`) replaces a fragile four-site string-prefix convention... that has caused real historical misrouting bugs. One typed field, checked once, not a string pattern matched in four places expected to stay in sync by convention."*

### The four sites, exact and self-documented in the code itself

`internal/chat/engine.go:283-291`'s own doc comment on `NormalizeCLIProvider` names all four explicitly as "the single source of truth consulted by": (1) `chat_generate.go`'s CLI bypass when the registry returns `prov == nil`, (2) `runtime/agent/bootdir.go`'s layout dispatch, (3) `runtime/agent/factory.go`'s `shouldUsePTY` check, (4) `service/agent_deps.go`'s `stripRegistryPrefix` (which delegates to `NormalizeCLIProvider` rather than re-implementing prefix logic itself — already partially consolidated, but still a fifth call site consuming the string-prefix convention rather than a typed field).

`IsCLIProvider` (`engine.go:256-258`): `name == "pty" || strings.HasPrefix(name, "pty-") || strings.HasPrefix(name, "sub-")`. `IsPTYProvider` (`:261-263`): narrower, PTY-only variant. `NormalizeCLIProvider` (`:292-303`): strips `pty-`/`sub-` prefixes, maps legacy bare `"pty"` → `"claude"`.

**A fifth, distinct site**: the boot-profile catalog has its *own* parallel `pty-claude`/`pty-codex`/`pty-opencode` handling in `internal/bootprofile/compiler.go` (confirmed via its test fixtures using exactly these provider-alias strings) — this is the "boot-profile catalog's own layered `pty-` prefix convention" architecture doc `02` names separately from the four `engine.go`-documented sites. This task does not need to fix the boot-profile catalog's version (`02-retire-boot-profile-catalog.md` removes the whole mechanism), but must confirm it isn't left as a still-live, un-migrated sixth routing decision once this task's four/five sites are converted — i.e. sequence such that nothing routes through the old convention by the time both this task and `02` are done.

### `resolveProvider` is a separate, adjacent mechanism — not one of the four, but reads `IsCLIProvider`

`internal/service/chat.go:1141-1195`'s `resolveProvider` calls `chat.IsCLIProvider(runtimeProvider)` at line 1157 as one step in its own fallback chain (session → agent → user-default → fallback-chain → inferred). This task's job is the four/five string-prefix sites; `resolveProvider`'s own chain collapsing into the construction cascade is `05-collapse-resolveprovider-into-cascade.md`'s job — but that task's replacement logic will itself need `runtime_kind`, so land this task first or in the same batch.

## What to do

1. Replace each of the four/five call sites' string-prefix check with a direct read of `agent_profiles.runtime_kind` (via whatever the composition-resolution path is by this point — likely the same cascade-resolved agent record `resolveForSession`/equivalent already produces). `IsCLIProvider`/`IsPTYProvider`/`NormalizeCLIProvider`/`shouldUsePTY`/`stripRegistryPrefix` themselves may still be needed as *string-shape* helpers for other purposes (e.g. deriving the underlying provider name once `runtime_kind == 'cli'` is already known) — this task's job is removing them as the *routing decision*, not necessarily deleting every function. Confirm per call site whether the function becomes dead code (delete it) or is repurposed as a narrower string-parsing helper consulted only after `runtime_kind` has already decided CLI-vs-API (keep it, narrow its doc comment).
2. Confirm the boot-profile catalog's own `pty-*` handling is not left as a live alternate routing path once `02-retire-boot-profile-catalog.md` lands — coordinate sequencing, don't assume it resolves itself.
3. Per Phase 0 #31 (`31-rename-pty-naming-scrub.md`), the naming scrub (`shouldUsePTY`→ whatever it's renamed to, `IsPTYProvider`, `pty-*` provider-string convention) already lands in Phase 0, independent of this task's mechanism change — confirm Phase 0 #31 has landed and use its post-rename identifiers, don't reintroduce "PTY" naming while wiring this.
4. Verify every existing CLI and API agent still launches correctly post-conversion — this is a routing-mechanism swap with zero intended behavior change for any already-correctly-configured agent; a misrouted agent post-change is a real regression, not an acceptable side effect.

## Done means

- All four/five sites read `runtime_kind` instead of string-matching a provider name.
- No remaining live routing decision depends on `pty-`/`sub-` prefix matching (verified by grep: any surviving prefix-matching helper is confirmed narrowed to post-decision string parsing only, not decision-making).
- Every current agent (post Phase 1 backfill) launches via the correct runtime (CLI or API) after this change — verified by exercising at least one CLI-based and one API-based agent in a real session.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
