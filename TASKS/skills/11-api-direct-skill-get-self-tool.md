# API-direct delivery — `skill_get` self-tool

**Phase:** 6 — Delivery (`TASKS/skills`)
**Status:** not-started
**Depends on:** `06`, `07`, `08`, `09` (the full Resolver → Materializer → Policy/Sandbox
pipeline this self-tool is the entry point into)
**Touches:** `internal/selftools/self_tools.go` (new `skill_get` `InputSchema`),
`internal/selftools/self_tools_transport.go` (new dispatch case), `internal/chat/context.go`
(`buildSkillListForSession` — update rendering only, not selection logic, to match task `02`'s
new index columns).

## Context

`docs/engineering/architecture/20-skills.md`'s "The invocation gap" section: *"The most
load-bearing gap found in this session: no skill's body content has ever reached a model, in any
runtime, through any live mechanism... API-direct agents: a new self-tool (`skill_invoke` or
equivalent naming) is the real entry point into the Resolver → Materializer → Policy/Sandbox →
Materialized Skill pipeline. An agent calls it with a slug plus parameters, gets back fully
materialized content... as a tool result. The catalog block stays a lightweight teaser —
progressive disclosure."*

**Naming: `skill_get`, not `skill_invoke`** — a deliberate departure from the architecture doc's
own placeholder phrasing, decided during this batch's planning (see
`TASKS/skills/README.md`'s corrections section and `TASKS/ESCALATIONS.md`'s 2026-08-21 entry).
`docs/tool-naming-convention.md`'s verb table already anticipates a `get` verb ("Fetch a single
resource by ID"), and `docs/tool-naming-audit.md`'s verdict table already renamed this exact
self-tool family (`skill_create`→cut in task `01`, `skill_list`, `skill_update`→cut in task `01`,
`skill_delete`) to the bare, prefix-free `skill_*` shape — `skill_get` matches that established,
already-renamed family directly. Confirm no other `skill_get` reference exists before locking
this in — the only prior hit found this planning session
(`internal/service/ingest_test.go:350,370-371`) is an explicit test-fixture placeholder for a
*non-existent* tool name (comment: `"skill_get"], // not real either`) proving the name was free,
not a real prior implementation to reconcile with; update or remove that test comment once this
tool is real.

**Confirmed this planning session: no `skill_get`/`skill_invoke`/any content-retrieval self-tool
exists anywhere today** — the four current `skill_*` self-tools (`self_tools.go:66,85,102,122`)
are pure CRUD-metadata operations; none has a `prompt` field in either input or (implicitly)
output. This task is genuinely new, not an extension of an existing tool.

**The catalog teaser stays exactly as it is, selection-wise** — `buildSkillListForSession`
(`internal/chat/context.go:251-287`) renders `- name: description [tools: ...]` with no mode
filter, no scoring, a flat `SkillEssentialCap=25` cap, `ORDER BY sk.name`. Per `20-skills.md`:
"progressive disclosure, matching the Agent-Skills-spec's own pattern, not a departure from it."
This task only needs to update what fields get rendered (task `02` dropped `Prompt`/`ToolBindings`
from `store.Skill` — confirm the teaser never referenced `Prompt` anyway, per this planning
session's research, so likely no change needed there at all beyond `ToolBindings`' removal) —
**do not** add scoring, mode-filtering, or any selection logic here; that's explicitly out of
scope per `TASKS/skills/README.md`'s scope fences.

## What to do

1. Add `skill_get`'s `InputSchema` to `internal/selftools/self_tools.go`: `slug` (required,
   string), `params` (optional, JSON object — static invocation arguments per task `06`'s
   Resolver). Output: the fully materialized skill content (task `06`-`08`'s pipeline result,
   with `inline` dependencies spliced and `fork` dependencies delegated-and-folded per task `07`).
2. Add the dispatch case in `self_tools_transport.go`: look up the skill by slug, confirm the
   invoking agent has a valid grant (task `09`'s same trust-validity check — an agent calling
   `skill_get` for a skill it isn't granted, or whose approved hash is stale, gets a clear
   "not granted"/"re-approval required" error, not silently-empty or unauthorized-but-succeeding
   content), then run the full Resolver → Materializer → Policy/Sandbox pipeline
   (tasks `06`-`09`) and return the result as the tool result.
3. Update `buildSkillListForSession`'s rendering only (drop any now-removed-column references) —
   verify against task `02`'s actual final `Skill` struct shape once that task lands.
4. Update `internal/service/ingest_test.go:350,370-371`'s stale `"skill_get" // not real either`
   comment/test now that the tool is real (confirm what that test was actually asserting — likely
   "unknown tool references get logged" using `skill_get` as one example of several nonexistent
   names; if so, swap in a different, still-nonexistent placeholder name rather than just deleting
   the assertion).

## Done means

- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- A real dogfeed (API-direct agent, e.g. `anthropic` provider, real chat turn) confirms an agent
  calling `skill_get` with a granted skill's slug receives real materialized content back as a
  tool result — verified against the actual message/turn transcript, not just a unit test of the
  handler.
- Calling `skill_get` for an ungranted skill, or a skill whose approved hash is stale, returns a
  clear denial — verified the same way task `09`'s equivalent test does, reused/adapted rather
  than reinvented.
- The catalog teaser (`buildSkillListForSession`) still renders with the same selection
  behavior (cap, ordering, no scoring/mode-filter) as before this task — only the rendered field
  set changed.
- `internal/service/ingest_test.go`'s stale placeholder comment/assertion is updated to reflect
  that `skill_get` is now real.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
