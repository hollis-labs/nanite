# Skills — real redesign, clean-slate migration

Implements `docs/engineering/architecture/20-skills.md` in full — the output of a dedicated
2026-08-21 architecture-alignment session that found skills were **never fully implemented**
in Nanite, and that the one filesystem-based mechanism that used to exist was cut in full three
days earlier (`TASKS/phase-1/08-kill-file-reingest-on-boot-pattern.md`). This batch is a
sibling to `TASKS/teams/`, `TASKS/reflex-taxonomy/`, `TASKS/harness-reactive-self-tools/`,
`TASKS/scheduling/`, `TASKS/agent-host-acp/`, and `TASKS/plugin-system/` — kept in its own
top-level `TASKS/` subfolder for the same reason those are: this work originates from a
dedicated architecture-review pass, not `docs/engineering/TASKS.md`'s original plan.

**This batch deliberately goes against the project's recently-established "DB over files"
default** — the vendored, content-addressed filesystem store (Phase 2, tasks `02`-`03`) is a
real, load-bearing exception, not an oversight. The reasoning is the architecture doc's own
(see "The model: DB is an index, a vendored store is content"): `scripts/`/`references/`/
`assets/` have to be real, executable/readable files on disk at materialization time regardless
of where their authoritative copy nominally lives, so pre-flattening a directory tree into DB
columns would just force a "stage back out to disk anyway" step — pointless overhead. The DB
stays authoritative for everything it's actually good at (index, enablement, grants, trust,
relationships) and never holds body/script/asset bytes. This is the same operation
`phase-1/08` already trusts elsewhere (`agent_procedures.BodyFile`, live MCP tool-list reads) —
splitting "silent every-boot re-ingest" (stays dead) from "read a file's content at the moment
it's actually needed after an explicit install" (the new mechanism) resolves the tension without
carving out an exception to that decision.

## Read before starting any task here

1. `docs/engineering/architecture/20-skills.md` **in full** — the actual target design. Every
   task below cites specific sections of it; read the whole thing first so the citations land
   in context, not the other way around.
2. `docs/engineering/architecture/13-memory-and-knowledge-tools.md`'s §4a
   ("Procedures vs. Skills vs. Workflows") — the already-settled Skills/Procedures boundary this
   batch does not revisit, and the source of two filed follow-ups this batch resolves (skill
   composability — task `07`; the catalog+attachment table shape — task `02` reuses and extends
   `agent_known_skills` directly, per that section's own citation of it as the reference pattern).
3. `docs/engineering/architecture/16-agent-host.md` and `docs/engineering/architecture/
   09-plugin-system.md` — cross-referenced below for two reused primitives (`go-sandbox`'s
   `Profile`+`Apply`, and the plugin capability-grant model's real, decided shape) and one
   real correction this planning session made to `16-agent-host.md`'s framing (see below).
4. `docs/engineering/GLOSSARY.md` — check before introducing any new name, per this repo's
   standing discipline. The file currently has **no entry at all for "Skill," "Skill catalog,"**
   **or "Skill attachment"** despite `13-memory-and-knowledge-tools.md` already treating them as
   settled concepts — a real, pre-existing gap this batch's task `02` closes as part of landing
   the new index schema, following the same disambiguation pattern as the existing "Team Slot"
   and "Snapshot (filesystem)" entries. Every other new term below (`Skill Resolver`, vendored
   store, etc.) is confirmed collision-free against the current 80-line file.
5. `docs/engineering/EXECUTION-PROCESS.md` — the task-file format, worker/reviewer discipline,
   and escalation rules every task file below follows.
6. `TASKS/plugin-system/README.md` and its tasks `04`-`06` — the closest sibling precedent
   (capability manifest schema, install-time approval gate, RPC-proxy enforcement) this batch's
   Phase 5 deliberately mirrors in shape, not in code — see the correction below on why it does
   **not** share literal code with that batch's mechanism.

## Real corrections found during this planning session's own research

Verified directly against live code (five independent, parallel read-only research passes),
not assumed from either architecture doc's prose — same discipline `TASKS/plugin-system`'s
planning pass used, logged the same way.

- **`docs/engineering/architecture/16-agent-host.md`'s framing of `policy.Engine`/`policy.Store`
  is accurate about the *library*, but the mechanism is dormant in *Nanite* today — a real gap
  between what reads as settled and what's actually running.** `go-agent-wrapper@v0.7.0`'s
  `policy/` package genuinely does separate mechanism (`policy.Engine.Decide`, run by the host at
  every interception point) from policy (`policy.Store`, app-supplied rule backing) — the doc's
  description of the *design* is correct. But `grep -rn "policy.Engine\|policy.Store"
  internal/` returns zero hits: `wrapper.Config.Policy` is never set anywhere in Nanite, so no
  tool-call interception through this mechanism is actually happening in production. `20-skills.md`'s
  "Security, sandboxing, and trust" section, written before this check, says skill capability
  enforcement "plugs into the same `policy.Engine`/`policy.Store` shape the host already runs for
  CLI tool-call interception" — that shape isn't actually running yet, so there's nothing live
  to plug into. Separately, `policy.Rule.Match` is a bare, store-defined-syntax string with no
  typed fs/network/subprocess/secrets schema — there is no existing typed vocabulary to extend
  even if the mechanism were wired. **Resolution, task `09`**: build narrow, direct capability
  enforcement at the skill-script/materializer execution call site, gated on task `02`'s
  grant-state columns — the same real, decided shape `TASKS/plugin-system/06`
  (`capability-enforcement-at-rpc-proxy-layer`) independently arrived at for exactly the same
  reason (four RPC-proxy call sites enforced directly, not through the dormant host mechanism).
  Vocabulary (a rule's mode: observe/nudge/rewrite/block/approval) stays loosely aligned with
  `policy.Rule`'s taxonomy for future compatibility if the host mechanism ever does get wired
  live, but no code is shared, because there's no live code to share yet.
- **`agent_known_skills` is not dead — only dead for prompt assembly, which is a narrower claim
  than it can read as.** `TASKS/phase-0/17-cut-role-skills-legacy.md` confirmed no chat/turn-loop
  code reads this table, but its own file documents (and this session re-confirmed) a fully live
  REST CRUD API (`internal/api/agent_capabilities.go`, `GET/POST/PUT/DELETE
  /api/agents/{id}/known-skills...`) and two live frontend surfaces — `AgentBuilderWizard.tsx`'s
  submit-time seeding loop and a standalone `AgentCapabilitiesPanel.tsx` CRUD editor — both still
  writing real rows today. `docs/engineering/architecture/13-memory-and-knowledge-tools.md`'s
  §4a independently cites this exact table (`skills` + `agent_known_skills`) as "a genuine
  catalog+attachment split" — the reference pattern a sibling Procedures redesign should mirror,
  not something flagged as needing replacement itself. **Resolution, task `02`**: extend
  `agent_known_skills` in place with the new grant-state columns this redesign needs (rather than
  building a third, parallel assignment table) — additive columns only, so the existing Wizard
  and Panel frontend surfaces keep working unmodified. This directly resolves one of
  `20-skills.md`'s "genuinely still open" questions ("whether `agent_known_skills`'s existing
  telemetry shape gets reused... a real precedent worth checking before building a third shape
  from scratch").
- **The existing `skill_create`/`skill_update` self-tools are structurally incompatible with the
  new model and must be cut, not carried forward as-is — a real scope item `20-skills.md` doesn't
  name explicitly.** Their current input schema (`name`/`slug`/`description`/`category`/
  `tool_bindings`/`input_schema` — confirmed, `internal/selftools/self_tools.go:66-120`) lets an
  agent construct an ad-hoc `skills` row directly via flat JSON fields, with no way to represent
  a real `SKILL.md` + `scripts/`/`references/`/`assets/` package. Under "skills are authored
  packages only" (the architecture doc's own scope line), free-form agent-authored rows have no
  place — authoring now means dropping a real package on disk and running the new explicit
  install/sync path (tasks `04`-`05`). **Resolution, task `01`**: cut `skill_create` and
  `skill_update` outright. `skill_list` and `skill_delete` stay (their *names*, not their current
  bodies — both get rewired against the new index schema once it lands, `skill_list` in task `02`,
  `skill_delete`/uninstall in task `12`). The new content-retrieval self-tool (task `11`) is
  named `skill_get`, not `skill_invoke` as `20-skills.md`'s placeholder phrasing suggested —
  `docs/tool-naming-convention.md`'s verb table already anticipates a `get` verb for "fetch a
  single resource by ID," and `docs/tool-naming-audit.md`'s verdict table already renamed this
  exact family (`skill_create`/`_list`/`_update`/`_delete`) to the bare, prefix-free
  `skill_*` shape task `11` extends.

## What this batch does NOT do

Explicit scope fences, not silent narrowing:

- **Frontend/admin-UI work.** `20-skills.md`'s own "API surface" section calls this "an
  explicitly separate stream, out of scope here" — matches this project's standing
  "no frontend work in any backend phase" discipline (`EXECUTION-PROCESS.md`'s Source-of-truth
  section). No task here touches `ui/src/`. The existing Agent Builder Wizard / Agent
  Capabilities Panel frontend keeps working against `agent_known_skills`' original columns
  unmodified — task `02`'s extension is purely additive.
- **Ecosystem-format package adaptation** (installing a `.claude/skills/`-authored-externally
  directory that doesn't already match the standard Agent-Skills-spec shape unmodified).
  `20-skills.md` lists this as "genuinely still open" and explicitly not resolved by that doc.
  Task `04`'s parser targets the real spec shape only; a foreign package that already matches it
  will likely install unmodified, but verifying that and building any real format-detection/
  adaptation shim is a separate follow-up, not this batch.
- **Explicit skill *triggering*** — an agent's skill activating automatically off a predicate/
  situation (as opposed to the agent explicitly calling `skill_get`, or a CLI-hosted agent's own
  native mechanism choosing to read a planted file). This is a real, separate follow-up filed in
  `13-memory-and-knowledge-tools.md`'s §4a (suggesting reuse of reflexes' `dispatch_to_agent`
  predicate-matching pattern) — `20-skills.md` does not design it, and this planning session is
  not inventing it. Out of scope.
- **Wiring `wrapper.Config.Policy`/`policy.Engine` live in Nanite for the first time.** A real,
  much larger change (host-wide tool-call interception) that this batch's narrow, direct
  capability-gate approach (task `09`) deliberately avoids depending on — see the correction
  above. Not ruled out for the future; just not a prerequisite here.
- **Sandboxing untrusted in-process code beyond the script/materializer exec path** (task `09`'s
  scope is exactly "short-lived, sandboxed, read/compute subprocess exec" — the same boundary
  `TASKS/plugin-system`'s Tier 1/Tier 2 split draws for builtins vs. subprocess plugins).
- **Reviving `internal/skillbroker`** or any rule-matching "which skill fires when" layer — cut
  in full during steering consolidation (`TASKS/phase-0/22`), and `20-skills.md` doesn't propose
  reviving it. The catalog block (`buildSkillListForSession`) stays a flat, capped, ordered list;
  no scoring, no mode filter, matches current behavior exactly (task `11` only updates its
  rendering to match the new index columns, not its selection logic).

## Task sequence

Flat-numbered `01`-`12` across seven phases, same convention as `TASKS/plugin-system/` and
`TASKS/agent-host-acp/`. Each task file's own header states its Phase.

**Phase 1 — Clean-slate cut.** Removes every dead/incompatible mechanism first, so later phases
build on a decluttered base rather than working around code that's about to be deleted underneath
them. Matches `20-skills.md`'s own "Migration: clean slate, no carried-forward content" —
operator call already made there: none of the 8 builtin skills or DB-originated auto-discovered
rows has ever been observed in use.

| Task | Depends on |
|---|---|
| `01-cut-legacy-skill-discovery-autodiscover-and-adhoc-authoring.md` | none |

**Phase 2 — Index + vendored store.** The DB/storage foundation everything else builds on.

| Task | Depends on |
|---|---|
| `02-redesign-skills-index-schema-and-extend-agent-known-skills.md` | `01` (both touch `internal/store/skills.go`) |
| `03-build-content-addressed-vendored-skill-store.md` | none (brand-new package, zero file overlap with `01`/`02` — safe to run in Wave 1 alongside `01`) |

**Phase 3 — Explicit install/sync.** The only way a skill's index row is ever created or
updated — single-target, never a directory sweep, per `20-skills.md`'s "Explicit,
single-target install/sync" mechanism.

| Task | Depends on |
|---|---|
| `04-build-skill-package-parser-and-install-sync-pipeline.md` | `02`, `03` |
| `05-install-sync-rest-api-and-cli-command.md` | `04` |

**Phase 4 — Materialization pipeline.** Skill Resolver → Skill Materializer, per `20-skills.md`'s
own sketch, now grounded in concrete Nanite mechanisms.

| Task | Depends on |
|---|---|
| `06-build-skill-resolver-and-parameter-binding.md` | `02`, `03`, `04` (reuses `04`'s parsed `parameters:` declaration shape) |
| `07-implement-inline-fork-composition-semantics.md` | `04`, `06` |
| `08-rebuild-inline-marker-and-scripts-execution.md` | `06` |

**Phase 5 — Security: sandbox + policy gate.** The one gate every script/materializer execution
routes through, regardless of caller.

| Task | Depends on |
|---|---|
| `09-sandbox-and-capability-policy-gate-for-skill-execution.md` | `02`, `08` |

**Phase 6 — Delivery.** Two boundary-specific materializations of one canonical vendored source,
per `20-skills.md`'s "Delivery: one source, two boundary-specific adapters."

| Task | Depends on |
|---|---|
| `10-cli-hosted-native-skill-delivery-boot-dir-planting.md` | `02`, `03` |
| `11-api-direct-skill-get-self-tool.md` | `06`, `07`, `08`, `09` |

**Phase 7 — Remaining REST API surface.**

| Task | Depends on |
|---|---|
| `12-remaining-skills-rest-api-list-grants-preview-uninstall.md` | `02`, `05`, `09` |

## Parallelization plan

**Wave 1 — parallel, worktree-isolated.** `01`, `03`. Zero file overlap: `01` touches
`internal/service/ingest.go`, `internal/mcp/manager.go`, `internal/skill/{builtin,context,parser}.go`,
`internal/store/skills.go` (removing builtin-seed plumbing), and `internal/selftools/self_tools*.go`;
`03` is a brand-new package (`internal/skillstore/`) with no existing-file edits at all.

**Wave 2.** `02` (needs `01` landed on `internal/store/skills.go` first — both redefine the
`Skill` struct and its migrations, real collision risk if run concurrently).

**Wave 3 — parallel.** `04` (needs `02`+`03`). Solo in this wave — `06` needs `04`'s parameter
declaration shape, so it can't start until `04` lands.

**Wave 4 — parallel.** `05` (needs `04` only — REST/CLI surface, `internal/api/` + `cmd/nanite/`,
no overlap with `06`), `06` (needs `02`+`03`+`04` — resolver logic, `internal/skill/resolver.go`
+ reuse of `internal/runtime/agent/context_resolver.go`, no overlap with `05`).

**Wave 5 — parallel.** `07` (needs `04`+`06`), `08` (needs `06` only) — disjoint files
(`07` touches composition/dependency-graph logic; `08` touches a rebuilt execution-marker file).

**Wave 6.** `09` (needs `02`+`08` — the sandbox/policy gate both `07`'s fork-delegation path and
`08`'s script/marker execution route through). Solo — high-consequence, isolate rather than run
alongside anything else.

**Wave 7 — parallel.** `10` (needs `02`+`03` only — CLI boot-dir planting, `internal/runtime/agent/`,
no overlap with `11`), `11` (needs `06`+`07`+`08`+`09` — the API self-tool, `internal/selftools/`,
no overlap with `10`).

**Wave 8.** `12` (needs `02`+`05`+`09` — remaining REST surface, cleanup/completion pass).

## Migration numbering

Highest existing goose migration on disk at this planning session's authoring time (2026-08-21)
is `134_agent_profiles_protocol_transport.sql`. `TASKS/plugin-system/04` provisionally claims
`135` (re-check-before-landing, per that batch's own README). This batch provisionally claims
`136` (task `02`'s skills-index redesign) and `137` (task `02`'s `agent_known_skills` extension +
`agent_skills` drop) — **both provisional**. Re-list `internal/store/migrations/` immediately
before either lands; `TASKS/agent-host-acp`, `TASKS/plugin-system`, and `TASKS/filesystem-snapshots`
are all concurrently in flight per `TASKS/INDEX.md` and any of them may have claimed `135`-`137`
first by dispatch time.

## `agent_skills` table — cut, not migrated

Confirmed **zero rows workspace-wide** against a real production DB backup at the time
`internal/store/migrations/113_agent_skills_agent_projects_fk.sql` rebuilt it with an enforced
FK (that migration's own header comment records the verified row count). `13-memory-and-knowledge-tools.md`'s
§4a cites `agent_known_skills` — not `agent_skills` — as the real catalog+attachment pattern.
Task `02` drops `agent_skills` outright in favor of consolidating all per-agent skill
assignment/grant/telemetry state on the one table that's both already live and already
recognized as the right shape — no data migration needed, nothing currently depends on it.

## Reused primitives, and where each comes from

Grounded against live code during this planning session, not assumed from the architecture doc:

- **Content-addressing / immutable vendored storage** — `internal/contextbroker/stash.go`'s
  `DeterministicArtifactID` (sha256-based, format `art-stash-<hash[:16]>`) and
  `internal/service/slot_stash.go`'s `artifactStasher.StashSlot` (FS-write-before-DB-insert
  ordering, idempotency fast-path, disk-loss recovery, insert-race recovery) is the real,
  already-hardened precedent task `03` adapts — for a directory tree (`SKILL.md` + `scripts/` +
  `references/` + `assets/`), not a single content string, so the hashing function needs real
  adaptation (hash the whole normalized tree), not literal reuse.
- **Sandboxed subprocess execution** — `go-sandbox@v0.2.1`'s `sandbox.Profile` +
  `sandbox.Apply(cmd *exec.Cmd, p Profile, workspace string) (cleanup func(), err error)`
  (`apply_{darwin,linux,unsupported}.go`) is the real primitive task `09` calls directly.
  **Nanite has no existing call site that invokes `sandbox.Apply` against a short-lived,
  non-agent subprocess today** — every current use is threaded through `agentkit`'s long-lived
  session runtimes via `wrapper.Config.SandboxProfile`. Task `09` is a genuinely new, narrower
  call site; `internal/runtime/agent/sandbox_profile.go`'s `buildSandboxProfile` is a
  composition-style pattern worth matching, not literal plumbing to reuse.
- **Explicit, single-target install-with-approval pipeline** — `internal/plugin/install/`'s
  `Installer` state machine (`NotInstalled → Downloading → Verifying → Extracting → Validating →
  [atomic Staging.Commit] → Loading → Ready/Failed`, injected `Verifier`/`Extractor`/`Validator`/
  `Loader`/`Staging` interfaces) is the real precedent task `04` pattern-matches its own state
  machine against — same shape, different package (`internal/skillinstall/`, not shared code).
- **Dynamic parameter resolution** — `agent_context_resolvers` (`cmd`/`http` resolver kinds,
  `internal/runtime/agent/context_resolver.go`'s `ResolveContextBlocks`) is reused **with
  adaptation**, not as-is: that table is keyed `(agent_id, slot_name)` — one resolver per agent
  per named slot, not naturally shaped for "this skill's parameter X, this invocation's args."
  Task `06` defines the actual binding: a skill's declared parameter can reference an existing
  agent context-resolver by slot name, resolved through the same `ResolveContextBlocks`
  mechanism at materialization time — no second resolver-provider system gets built, per
  `20-skills.md`'s own explicit instruction.

## Escalations and design-latitude notes from this planning pass

See `TASKS/ESCALATIONS.md`'s 2026-08-21 entry for the full record of the three corrections above
(policy.Engine/Store dormancy, `agent_known_skills` reuse decision, `skill_create`/`_update`
cut) — logged as design-latitude notes per the "promote recommendations, don't just log them"
discipline, not open escalations blocking dispatch.

## Planned 2026-08-21, not yet dispatched

Per `EXECUTION-PROCESS.md`'s Phase A discipline, this is the planning checkpoint — present to
the operator for review before any worker is dispatched.
