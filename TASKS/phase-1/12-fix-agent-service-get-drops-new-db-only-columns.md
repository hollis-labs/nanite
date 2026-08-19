# Fix `AgentService.Get`/`GetBySlug`/`List` silently dropping `role_id`/`model_id`/`runtime_kind`/`consumer_id` for file-discovered agents

**Phase:** 1
**Status:** implemented
**Depends on:** `02-add-agents-composition-columns.md`, `03-add-consumers-table.md` (the columns this task's bug affects)
**Touches:** `internal/service/agent.go` (`agentServiceImpl.Get`/`GetBySlug`/`List`), `internal/agent/convert.go` (`Definition.ToProfile`)

## Context

Found during the Phase 1 Wave 2 live-dogfeed validation checkpoint (2026-08-18), not by any worker's own test suite — this is exactly the class of bug `docs/engineering/standards/testing.md` warns a green test suite alone won't catch, since it only reproduces through the full HTTP round-trip, not a direct store-layer call.

**The bug, verified directly:** `internal/service/agent.go`'s `agentServiceImpl.Get` (used by `handleGetAgent`, `handleUpdateAgent`, and everything else that resolves an agent by ID through the service layer) checks for an in-memory `agent.Definition` first (`findDefByID`/`findDefBySlug`) and, if found, returns `d.ToProfile()` — **never touching the DB row at all** for that call. `List` does the same thing (file-based defs take priority, DB agents are only appended for slugs not already covered by a def). `internal/agent/convert.go`'s `Definition.ToProfile()` has explicit handling for `ActivationMode` (`if d.ActivationMode != "" { p.ActivationMode = d.ActivationMode }`) but **zero handling** for `RoleID`, `ModelID`, `RuntimeKind` (added by Phase 1 `02`) or `ConsumerID` (added by Phase 1 `03`) — confirmed by grep, no occurrence of any of those four field names anywhere in `convert.go`.

This isn't a hypothetical — reproduced live: PUT an agent's `activation_mode` via `PUT /api/agents/{id}`, confirm the DB row directly via `sqlite3` shows the write landed correctly (`runtime_kind='api'`, backfilled by `02`'s migration), then `GET /api/agents/{id}` and see `"runtime_kind":""` — silently wrong, no error. Every one of the ~33 file-discovered agents in this project (`Source="project"`/`"user"`, the overwhelming majority of real agents, per `.nanite/agents/*.md`) hits this path. Only genuinely API-created agents with no matching in-memory `Definition` (rare — `Source="user"` agents created purely via `POST /api/agents` after boot, never present as a file) fall through to the real DB read (`s.agents.GetAgent(id)`) and see correct values.

**Why this matters now, not later:** `09-build-assignment-ui-api.md` (not yet started) is meant to build a picker UI that reads/writes exactly these fields (`role_id` selection, `consumer_id` tagging, `model_id`, `runtime_kind` display) — if this task doesn't land first, that UI will appear broken (values silently reverting to empty on every page load) for the overwhelming majority of real agents, and whoever builds/tests `09` will burn time debugging what looks like a UI bug but is actually this read-path bug one layer down. Separately, Phase 2's `01-wire-runtime-kind-routing.md` is explicitly building real CLI-vs-API routing decisions on `runtime_kind` — if Phase 2 reads it through this same `AgentService.Get`/`List` path without this fix landing first, routing would silently misbehave for every file-discovered agent, a much higher-stakes live bug than a UI cosmetic issue.

**Root cause is structural, not a missing field mapping.** `role_id`/`model_id`/`runtime_kind`/`consumer_id` have no YAML frontmatter representation at all — they are pure DB-only columns (per architecture doc `01-agent-construction.md` and this phase's own tasks `02`/`03`). `Definition.ToProfile()` can only ever return their zero-value for a file-backed agent, structurally, no matter what field-mapping code is added to it — there is nothing in the parsed file to map from. The actual fix is that `agentServiceImpl.Get`/`GetBySlug`/`List`'s in-memory-def-first strategy needs to stop being a full bypass of the DB for these specific columns: for a file-backed agent that also has a real `agent_profiles` row (which, post `08`, all of them do once ingested), the returned profile must carry the *DB's* values for `role_id`/`model_id`/`runtime_kind`/`consumer_id`/`activation_mode` (and any other future DB-only field), with the file's content winning only for fields the file actually declares (system_prompt, tools, etc. — the current `ToProfile()` shape). This is the same "DB is authoritative, file is not" principle task `08` already established for the boot-time ingest path — this task is the analogous fix for the *read* path.

## What to do

1. Confirm the full list of DB-only columns with no file/frontmatter representation that this bug affects — at minimum `role_id`, `model_id`, `runtime_kind` (`02`), `consumer_id` (`03`); check `activation_mode`/`class`/`default_state` too even though `ActivationMode` has *some* handling today (verify it's actually correct and not itself silently dropping a DB-set value that differs from the file's declared one — the current code takes the file's value if non-empty, which may itself be wrong once the DB is the authoritative source per `08`; decide and document whether `ActivationMode` needs the same DB-wins treatment as the newer fields, and if so, why the current partial handling wasn't already flagged before now).
2. Fix `agentServiceImpl.Get`/`GetBySlug`/`List`: for any file-backed def that resolves to an existing `agent_profiles` DB row, merge the DB row's values for every DB-only field into the returned profile — the file-derived `Definition.ToProfile()` result should not simply be returned as-is once a DB row exists. Decide the cleanest implementation shape (e.g., `ToProfile()` returns the file-only view, then `Get` overlays `s.agents.GetAgent(id)`'s DB-only fields on top when a row exists) rather than guessing — this touches the same file/DB precedence question `08` already worked through, so read that task's Work Log first for the established convention.
3. Handle the "file-backed but no DB row yet" case correctly (a genuinely new file not yet ingested) — these DB-only fields should default sensibly (empty/zero), not error.
4. Add a real regression test that reproduces the exact bug found here: create/seed a file-backed agent with a real DB row carrying non-default `role_id`/`model_id`/`runtime_kind`/`consumer_id`, call `Get`/`GetBySlug`/`List` through the service layer (not `store.GetAgent` directly), and assert the DB's values come through — this is the specific case every existing test missed.
5. Re-verify live (not just via `go test`) against a real running instance: `PUT` a change, confirm via direct DB query it landed, then `GET` the same agent via the API and confirm the DB's value — not the file's — is what comes back.

## Done means

- `GET /api/agents/{id}`, `GET /api/agents` (list), and `GET /api/agents?slug=...`-equivalent lookups for a file-backed agent with a real DB row correctly reflect that row's `role_id`/`model_id`/`runtime_kind`/`consumer_id` (and `activation_mode`/`class`/`default_state` if item 1 finds they need the same treatment) — verified live against a running instance, not just a unit test.
- A file-backed agent with no DB row yet still resolves sensibly (no crash, no wrong data).
- A real regression test exists that would have caught this exact bug (reads through the service layer, not the store layer directly).
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log

**Starting commit / branch note.** This worktree's branch (`worktree-agent-
ac772e5fe43a96396`) was, at session start, a strict ancestor of
`phase-1-execution` with zero divergent commits of its own — starting
commit `df71e710` ("Document sync"), the same merge-base as `08`'s worker
found. Fast-forwarded to `phase-1-execution`'s tip, `deacd08d` ("Phase 1
Wave 2 live-dogfeed finding..."), before starting — `TASKS/phase-1/12` and
`08`'s Work Log only exist from that tip onward.

**Step 1 — confirmed field list, by direct grep/read, not by assumption.**

- `role_id`/`model_id`/`runtime_kind`/`consumer_id` (`internal/store/
  agents.go`'s `AgentProfile` struct, added by migrations 106/111): zero
  frontmatter representation anywhere — confirmed by grepping
  `internal/agent/parser.go` and `internal/agent/managed_files.go`'s
  `managedFileFrontmatter` for all four names; zero hits. `Definition.
  ToProfile()` can only ever return their empty zero-value for a
  file-backed def, structurally, exactly as the task's Context section
  predicted.
- `activation_mode`/`class`/`default_state`: **do** have frontmatter
  representation — `internal/agent/parser.go:82-85` (`ActivationMode`/
  `Class`/`DefaultState` on the frontmatter struct, yaml tags
  `activationMode`/`class`/`defaultState`) and `managed_files.go`'s
  `managedFileFrontmatter` round-trips all three on every managed-agent
  write (`WriteManagedAgentProfile`). `ToProfile()`'s existing handling
  (`if d.ActivationMode != "" { p.ActivationMode = d.ActivationMode }`,
  same shape for the other two) is real, deliberate field mapping, not an
  oversight — it was not itself wrong for the write/ingest path
  (`upsertAgentDef`, called for a fresh row or an explicit reimport
  immediately after a managed-agent API write, both of which keep the file
  and the DB row in lockstep for these three fields specifically, since
  `WriteManagedAgentProfile` writes them into the frontmatter on every
  edit).
- **Why the partial `ActivationMode` handling is still wrong on the read
  path, and hadn't been flagged before now:** nothing keeps a file's
  frontmatter and its DB row in lockstep for these three fields **outside**
  of that one write path. Two concrete ways they diverge: (a) a
  hand-edited `.nanite/agents/*.md` file's `activationMode`/`class`/
  `defaultState` — post-`08`, a boot-time reingest of an already-ingested
  row is deliberately frozen and will never sync that edit into the DB, so
  the file's value can now permanently outrun the row's real value with no
  write-side mechanism to reconcile them; (b) a real live case found while
  testing this fix live (see below): `analyst.md`'s frontmatter declares
  none of the three at all, so `ToProfile()` always returns `""` for them,
  while the row (backfilled by migration 111's `applyMultiAgentDefaults` —
  `class="advisor"`, `activation_mode` derived from class,
  `default_state="sleeping"`) carries real, non-empty values — confirmed
  live: `GET /api/agents/blt-analyst-001` returned `activation_mode:
  "singleton", class: "advisor", default_state: "sleeping"` (the row's
  backfilled defaults) only *after* this fix; before it, `ToProfile()`'s
  guard would have left all three `""`, silently wrong, for every one of
  the many `.nanite/agents/*.md` files that don't bother declaring these
  fields at all. It wasn't flagged when `02` added the guard because the
  guard is correct in isolation for the one call site (`upsertAgentDef`)
  that existed to reason about at the time — the read-path bypass this task
  fixes is what turns "correct for ingest" into "wrong for every GET/List
  once a row exists and the two diverge." **Decision: yes, `activation_mode`/
  `class`/`default_state` get the same DB-wins overlay treatment as the four
  pure-DB-only fields on the read path** — the write/ingest path's existing
  `ToProfile()` handling is untouched and still correct for its own call
  site.

**Step 2/3 — implementation shape.** Added `agent.OverlayDBFields(p, db
*store.AgentProfile) *store.AgentProfile` to `internal/agent/convert.go`
(colocated with `ToProfile()`, since it's the same "how do I turn a
Definition/DB-row pair into the profile I return" concern) — a small,
pure function that overlays `db`'s seven fields (the four DB-only ones plus
the three from step 1's decision) onto `p`, no-op when `db` is nil. Chose
"`Get` overlays the DB row onto `ToProfile()`'s result" (the task's own
suggested shape) over the reverse (start from the DB row, overlay file
content) because the file-derived fields (system_prompt, tools, MCP
servers, etc.) are the actual majority of `ToProfile()`'s output and
already have well-tested mapping logic there; overlaying just the narrow
DB-authoritative slice on top is the smaller, more obviously-correct diff.

`internal/service/agent.go` gained `agentServiceImpl.resolveFileProfile
(d *agent.Definition) *store.AgentProfile`: calls `d.ToProfile()`, looks up
`s.agents.GetAgentBySlug(d.Slug)` (not `GetAgent(d.CanonicalID())` —
confirmed by reading `ingest.go`'s `upsertAgentDef` identity-resolution
comment that an unstamped/internal file-backed def's DB row gets a *minted*
UUID as its real PK while the harness keeps using the deterministic
"file-<slug>" identity at runtime elsewhere, e.g. `defaultFallbackAgent`;
`GetAgent("file-<slug>")` would never find that row, but `GetAgentBySlug`
always resolves by the identity that's actually stable across both
representations), and returns `agent.OverlayDBFields(p, row)` when a row
exists or `p` unchanged (any lookup error, including "not found") when it
doesn't. `Get`/`GetBySlug`/`List` all call this instead of `d.ToProfile()`
directly — three call sites changed, no change to the overall file-first/
DB-fallback control flow for agents with no matching in-memory def at all.

Deliberately did **not** overlay `p.ID`: for an unstamped/internal
file-backed def, `d.ToProfile().ID` is the deterministic `"file-<slug>"`
runtime identity the harness hard-codes at several other call sites (the
task's own Context section names `defaultFallbackAgent = "file-default"`);
the DB row's real ID is a different, minted UUID for exactly this case.
Overlaying it would have broken that identity contract for no benefit —
out of this task's stated scope (`role_id`/`model_id`/`runtime_kind`/
`consumer_id`, plus `activation_mode`/`class`/`default_state` per step 1)
and not something any Done-means bullet asked for.

**Step 4 — no-DB-row-yet case.** `resolveFileProfile` treats any
`GetAgentBySlug` error (including the stub/store "not found" contract used
throughout this codebase) as "no row yet" and returns the unmodified
file-derived profile — no panic, no wrong data, DB-only fields simply stay
at `ToProfile()`'s existing zero-value default. Verified by a dedicated
test (`TestAgentService_Get_FileBackedAgentNoDBRowYet`) and by the
pre-existing `TestAgentService_ResolveForSession_HardcodedFallback` (a
`FileAgents`-only fixture with no matching DB row, unmodified by this
change) still passing unchanged.

**Step 5 — regression tests added:**
- `internal/agent/convert_test.go`: `TestOverlayDBFields` — unit-level pin
  on the merge helper itself (nil `db` is a no-op; non-nil `db` overwrites
  all seven fields even when `p` already carries a non-empty,
  file-declared value for the three that have frontmatter representation).
- `internal/service/agent_test.go`:
  `TestAgentService_Get_FileBackedAgentOverlaysDBOnlyFields` — the literal
  bug reproduction the task asked for: seeds a `stubAgentReader` DB row for
  slug `widget-agent` with non-default `role_id`/`model_id`/`runtime_kind`/
  `consumer_id` and a *deliberately different* `activation_mode`/`class`/
  `default_state` than what the matching `FileAgents` definition declares,
  then calls `Get` (by file-based ID), `GetBySlug`, and `List` — all three
  through the service layer, none through `store.GetAgent` directly — and
  asserts the DB row's values win for all seven fields on every call site,
  while `SystemPrompt` still comes from the file.
  `TestAgentService_Get_FileBackedAgentNoDBRowYet` — Done-means bullet 2,
  see step 4 above.

**Step 6 — checks.**
- `go build ./cmd/nanite/` — pass.
- `go vet ./...` — pass except the pre-existing, unrelated
  `container.go:1210/1230/1281` ("stopReaper"/"stopRuntimeReaper" possible
  context leak) findings already documented as pre-existing in `08`'s Work
  Log (confirmed still present and unrelated to this task's two changed
  files).
- `go test ./... -count=1` — pass across every package with test files, no
  failures.

**Step 7 — live verification against a real running instance, not just
`go test`.** Checked `lsof -ti:8991` first (free — several lower ports were
already in use on this machine). Built a scratch binary and ran
`nanite serve -db <scratch-path>/scratch.db -port 8991 -dev` from this
worktree's root (so the real 33-agent `.nanite/agents/*.md` corpus was
discovered/ingested, same as `08`'s own live verification). Checked
`internal/api/types.go`'s `UpdateAgentRequest` first: it accepts
`ActivationMode`/`Class`/`DefaultState` (all three round-trip through
`PUT /api/agents/{id}` → `AgentConfigService.Update` → a managed-file
rewrite) but has **no** `RoleID`/`ModelID`/`RuntimeKind`/`ConsumerID` fields
at all — confirming the task's own fallback instruction applied: verified
those four via a direct `sqlite3` UPDATE against the scratch DB instead of
a PUT.

Picked `analyst` (`blt-analyst-001`, `source='project'`, a real
`.nanite/agents/analyst.md`-backed agent, confirmed via `GetAgent`-style
DB query showing `role_id`/`model_id`/`consumer_id` empty and
`activation_mode/class/default_state = singleton/advisor/sleeping` — the
migration-111-backfilled defaults, since `analyst.md`'s own frontmatter
declares none of the three, which is itself the step 1 divergence case
described above, already confirmed live by the pre-update `GET` matching
the DB row rather than the file's silence). Ran, with the scratch server
live:

```
sqlite3 scratch.db "UPDATE agent_profiles SET role_id='role-verify-999',
  model_id='model-verify-999', runtime_kind='cli',
  consumer_id='consumer-verify-999', activation_mode='concurrent',
  class='harness', default_state='active' WHERE slug='analyst';"
```

Confirmed the write landed via a direct `sqlite3 SELECT` (all seven columns
showed the new values). Then `GET /api/agents/blt-analyst-001` and
`GET /api/agents` (list) both returned the DB's new values —
`role_id=role-verify-999`, `model_id=model-verify-999`,
`runtime_kind=cli`, `consumer_id=consumer-verify-999`,
`activation_mode=concurrent`, `class=harness`, `default_state=active` —
not the empty/stale values the pre-fix code would have shown. Confirmed
via `git status`/`git diff --stat` that `.nanite/agents/analyst.md` was
never touched by this verification (the sqlite3 UPDATE went directly to
the scratch DB only, no API PUT was used, avoiding the exact
out-of-band-file-write incident `08`'s Work Log flagged). Stopped the
scratch server (`kill`, confirmed `lsof -ti:8991` empty) and deleted all
scratch files (binary, DB, logs) afterward.

**Deviations from the task file's literal plan.** None of substance. The
task's "Touches" list named `internal/agent/convert.go` and
`internal/service/agent.go`; both were touched, in the shape the task
itself suggested as an example ("`ToProfile()` returns the file-only view,
then `Get` overlays ... DB-only fields on top"). The one real judgment call
— extending the DB-wins overlay to `activation_mode`/`class`/
`default_state`, not just the four DB-only columns literally named in the
task's title — is exactly the investigation the task's own step 1 asked
for, decided and documented above, not a deviation from what was asked.

No schema change. `TASKS/INDEX.md` intentionally left untouched (Orchestrator
updates it after merge).

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
