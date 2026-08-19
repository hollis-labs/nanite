# Port forward the `cmd`/`http` dynamic-resolver capability as a first-class launching-time mechanism

**Phase:** 2
**Status:** not-started
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
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
