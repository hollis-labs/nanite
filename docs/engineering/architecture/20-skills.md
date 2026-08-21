# Skills

## Where this doc starts, and where it doesn't

This is the output of an architecture-alignment session (2026-08-21) working from an operator handoff doc ("Skills: Standards-Compatible Files + Deterministic Runtime Materialization," external to this repo) that proposed treating standard Agent-Skills-spec `SKILL.md` packages as canonical filesystem content, with Nanite layering parameterization, composition, deterministic runtime materialization, and security policy on top — the filesystem holding the portable artifact, the database holding only Nanite-owned metadata (discovery/index, enablement, grants/trust, relationships, config, telemetry).

That proposal assumed an active filesystem-first skill system to extend. **It doesn't exist anymore.** `TASKS/phase-1/08-kill-file-reingest-on-boot-pattern.md` (2026-08-18 — three days before this session, applied repo-wide to agents and skills identically, "same bug class, same fix, no special-casing") deliberately and completely removed file-based skill discovery: `internal/skill.Discover()` is now a stub returning `nil, nil` (`internal/skill/discovery.go:33-35`), and all four filesystem tiers it used to scan (`.nanite/skills/` project, `~/.nanite/skills/` user, `.claude/skills/`, `plugins/*/skills/*.md`) were removed outright, not merely stopped. This was one instance of a stated, operator-endorsed, repo-wide principle: *"The only file based agents [and skills] should be from seeding. […] no debt carries forward."* — files are seed-only, the DB is authoritative from first ingest onward, and there is no re-ingest-on-boot, period.

The 2026-08-17 system-audit doc's skills chapter (`docs/system-audit/2026-08-17/code-architecture/14-skills-and-knowledge.md`) — written one day before that cut landed — is stale on exactly this point, and also stale on the Skill Broker (`internal/skillbroker`, deleted in full the same day, `TASKS/phase-0/22-remove-skill-and-tool-broker-abstractions.md`) and on `roleSkills:` frontmatter auto-seeding (cut the same day, `TASKS/phase-0/17-cut-role-skills-legacy.md`, confirming `agent_known_skills` has zero functional effect on prompt assembly and never did). Where that doc and this one disagree, this one wins — verified directly against live code during this session, not against a four-day-old snapshot.

**This isn't a design for extending a working system. Skills were never fully implemented in Nanite**, and what existed before 2026-08-18 was itself thin — no `scripts:`/`references:`/`assets:` support ever, no composition, no real parameterization, and (see "The invocation gap" below) no path for a skill's actual body content to ever reach a model regardless of runtime. This doc designs the real thing, on a codebase that turned out to be a smaller and more disconnected starting point than either source document assumed.

## Current state, verified against live code (2026-08-21)

- **Discovery is dead.** `skill.Discover()` always returns nothing. `internal/service/container.go:558` still calls it, then separately loads the 8 Go-embedded builtin skills (`internal/skill/builtin/*.md` + `embed.go`) and merges both into `AutoIngestSkills`.
- **The DB is authoritative from first ingest, forever.** `upsertSkillDef` (`internal/service/ingest.go:413-475`) no-ops once a row exists under its current source (`if existing.Source == source { return nil }`) — a file changing on disk, even if discovery still ran, would never resync. `version` (`internal/store/skills.go:31`) is a change-counter bumped on the rare provenance-transition path, not a content hash.
- **`skills.Prompt` stores the full markdown body directly in SQLite** (`internal/store/skills.go:23`) — content duplication was already real before discovery was cut, not something this redesign introduces.
- **The `skills` table conflates two unrelated things.** `mcp.Manager.AutoDiscover` (`internal/mcp/manager.go:921-1007`, still fully live, untouched by any of the 2026-08-18 cuts) mechanically creates one `skills` row per tool visible to Nanite — every self-tool plus every connected MCP server's tools — with `category="auto-discovered"`, `tool_bindings=["<tool>"]`, and **no prompt body**. This pipeline accounts for the overwhelming majority of rows and has nothing to do with authored, Agent-Skills-spec content; it exists only to make tools individually assignable through the skills admin surface.
- **No `scripts/`, `references/`, or `assets/` support exists anywhere** — confirmed absent by grep across `internal/skill` and `internal/selftools`; `skill.Definition` and `store.Skill` have no fields for any of the three.
- **No composition or nesting exists** — confirmed absent by grep across the parser, service, and context-assembly layers; no field on `Definition`/`Skill` references another skill.
- **No real parameterization exists.** `ArgumentHint` (`internal/skill/parser.go:24`) is a static display string only, never bound to anything at invocation. `BrokerHints []string` (parser.go:29) is now orphaned — its only consumer, `internal/skillbroker`, was deleted 2026-08-18.
- **The inline dynamic-context marker is dead code with a real design flaw.** `internal/skill/context.go`'s `` !`cmd` `` regex (`context.go:16`) has zero production callers — only its own unit tests exercise it. If it were wired up as-is, it executes via bare `exec.Command("/bin/sh", "-c", cmd)` (context.go:41) with a 10-second timeout and **no sandboxing, policy, or allowlist of any kind** (context.go:19, 62-64), and the regex has no awareness of markdown code-fence context — a documentation example containing the literal text `` !`...` `` inside a code fence would execute exactly the same as a real marker.
- **`Context: "inline"|"fork"`** (`internal/skill/parser.go:28`, default `"inline"`) is parsed and stuffed into a `Settings["context"]` JSON blob at ingest (`internal/skill/convert.go:65-66`) — and never read back by anything. It's a stub name with no wired behavior, not a partial implementation of composition, confirmed by grepping every reference to it in the runtime.
- **Skills are structurally invisible to every CLI-hosted agent.** `composeSystemPrompt` (`internal/runtime/agent/prompt.go:24`) and `composeBootContent` (`internal/runtime/agent/kickoff.go:41`) — the two functions that build boot-dir content for Claude/Codex/OpenCode agents Nanite launches — build from `profile.SystemPrompt` plus role/mode framing only; neither calls anything skill-related. `regenerateBootDirSlots` (`internal/service/chat_boot_drive.go:639`) recomputes the same skill-free content mid-session. The only function that renders skills into a prompt, `assembleAgentSlotContent` (`internal/chat/context_client.go:194`), is reachable exclusively through `AssembleSlotSources` (context_client.go:146) → its sole caller, `internal/service/context.go:204` — the API-direct chat path only.
- **Even where skills do reach a prompt, only the teaser reaches it — never the content.** `buildSkillListForSession` (`internal/chat/context.go:251-287`, the post-skillbroker-cut selection function: no mode filter, no scoring, a flat cap at `SkillEssentialCap=25` in `ORDER BY sk.name` order) renders exactly `- name: description [tools: ...]` per skill and nothing else — `sk.Prompt` is never interpolated into anything. There is no `skill_get`/`skill_invoke`/equivalent self-tool anywhere in `internal/selftools`. `skill_create`'s own input schema doesn't even have a `prompt` field (self_tools.go:65-133) — it authors tool-wrapper rows, not content. **The practical effect: no skill's actual instructional content has ever reached a model through any live path in Nanite, regardless of runtime.** This is the single most load-bearing gap this doc addresses — see "The invocation gap" below.
- **The MCP trust-tier model doesn't apply here.** `docs/mcp-trust-model.md`'s four tiers (`builtin`/`plugin_stdio`/`plugin_http`/`third_party_http`) are a size/DoS ceiling keyed to transport origin, enforced in `internal/mcp/validate.go` — a "how much of this will we accept" boundary, not a "what is this allowed to do" capability model, and it's scoped entirely to MCP servers. The tiering-by-source-with-fail-closed-default *pattern* is reusable for skills; the size-ceiling *mechanism* isn't what a skill's script/materializer capability grant needs.
- **The Skills/Procedures boundary is already settled and stays unchanged by this doc** — see [13-memory-and-knowledge-tools.md](13-memory-and-knowledge-tools.md) §4a: Skills are general, agent-independent capabilities; Procedures are role-specific SOPs. Nothing here revisits that axis.

## The central tension, and how it resolves

`phase-1/08`'s objection was never to the filesystem being content-authoritative in principle — it was to **silent, unscoped, every-boot mutation**: any file sitting in a watched directory got re-ingested on every process start with no operator action taken, occasionally reverting a deliberate DB-side edit. That's a different operation from **reading a file's content at the moment it's actually needed**, which is a pattern this codebase already trusts elsewhere (`agent_procedures.BodyFile` resolves relative to a profile file at parse time; an MCP server's tool list is read live on every discovery pass, never cached-and-forgotten).

Splitting those two operations resolves the conflict without carving out a special exception to `phase-1/08`:

- **Automatic boot-time directory sweep** — stays dead, for skills exactly as for agents. No code path scans a directory and silently upserts rows on process start.
- **Explicit, single-target install/sync** — a new mechanism, scoped to exactly one named skill per invocation (a CLI command, a self-tool call, or an admin-UI import), never a sweep. This is the *only* way a skill's index row is created or updated.

The database never stores skill body/script/asset content, even after an explicit sync — so there's no "stale DB copy silently diverging from the file" class of bug to reintroduce, because the DB was never the content authority in the first place.

## The model: DB is an index, a vendored store is content

- **Filesystem package** — a standard `SKILL.md` + `scripts/` + `references/` + `assets/` directory, exactly the Agent-Skills-spec shape. This is what an operator authors or drops in from another tool's ecosystem (e.g. a `.claude/skills/` directory).
- **Install/sync** (explicit, single-target) reads that package, computes a content hash, and **vendors a copy into a Nanite-owned, content-addressed store** — keyed by hash, immutable once written, matching the existing project convention of treating installed content as a lockfile-style artifact rather than a live pointer at an operator's mutable path. A changed original at the source path has zero effect on the running system until the next explicit install/sync targets it again.
- **DB row** (the `skills` table, or its successor) is purely an index: vendored-store location, content hash, version, source tier, enablement, grant/trust state, and (once composition ships) declared dependencies. It never holds `SKILL.md` body text, script contents, or asset bytes.
- **Materialization always reads the vendored copy live**, every time a skill is used — never a value baked into a DB column at install time. This is what makes `scripts/`/`references/`/`assets/` coherent at all: a script has to be a real, executable file at materialization time regardless of where its authoritative copy nominally lives, so treating the vendored directory as the read-time source rather than pre-flattening it into the DB avoids the "stage back out to disk anyway" trap that would make DB storage pointless overhead.
- A hash change on the *original* source path is irrelevant until the next explicit re-install; a hash change relative to the *vendored, approved* copy is what the trust model keys off of (see "Security, sandboxing, and trust" below).

## Scope: skills are authored packages only

`mcp.Manager.AutoDiscover`'s writes into the `skills` table stop entirely. Tool visibility, permissions, and allowlisting remain exactly what they already are — a tool-catalog concern (`ToolService.SelectForAgent`, `tool_list`, the tool-allowlist admin surface) — untouched by this redesign. Nothing today depends on the auto-discovered rows functioning as skills: `agent_skills`, the actual per-agent assignment join table, has zero live rows workspace-wide, so nothing currently reads a tool-wrapper row through the skill-selection path in practice. Going forward, "skill" means exactly one thing: an authored, `SKILL.md`-compatible package, installed explicitly, vendored, indexed.

## Migration: clean slate, no carried-forward content

All existing skill content is deleted outright as part of shipping this redesign — the 8 Go-embedded builtin skills (`dev-read`/`dev-write`/`dev-grep`/`dev-bash`/`dev-glob`/`dev-edit`/`math-evaluate`/`encoding-convert`) and the small number of DB-originated rows alongside them. Operator call: none of this content has ever been observed in use, the new system changes the package shape, storage model, and invocation path completely, and nothing about the old rows is worth preserving through a migration. Skills get added back one at a time, authored against the new format, once the system described here actually ships.

## Delivery: one source, two boundary-specific adapters

Per [16-agent-host.md](16-agent-host.md)'s boundary — a host owns boot-dir file planting; a host does not own product identity or skills content — skill delivery is harness-owned, not host-owned, but it still has to differ by runtime, because a CLI-hosted agent has a filesystem boot dir to plant into and an API-direct agent doesn't.

- **CLI-hosted agents** (Claude/Codex/OpenCode, launched by Nanite): the vendored package is copied or symlinked into the agent's own native skill location inside its boot dir (e.g. a Claude-launched agent gets it staged at `.claude/skills/<slug>/`). The agent then uses its own native skill mechanism — unmodified, no Nanite-specific rendering, no Nanite self-tool required. Nanite's responsibility stops at planting real files at the path the runtime already expects.
- **API-direct agents** (no subprocess, no boot dir — `llmtypes.StreamEvent` is the whole observable surface per [19-api-cli-runtime-parity.md](19-api-cli-runtime-parity.md)): the existing lightweight catalog block stays, listing name/description/argument-hint per assigned skill, capped and rendered the same way `buildSkillListForSession` already does today. Full content only reaches the model through explicit invocation (see "The invocation gap").

Same vendored package, one canonical content source, two boundary-specific materializations — the handoff doc's own stated principle ("keep canonical semantics centralized; materialize runtime-specific representations at the boundary"), applied to a gap neither source document had actually named.

## Materialization pipeline

Skill Resolver → Skill Materializer → Policy/Sandbox → Materialized Skill, per the handoff doc's sketch, now grounded in concrete Nanite mechanisms rather than left abstract:

- **Parameters.** Static invocation arguments come from the caller (an agent's `skill_invoke` call, or an install-time default). Dynamically-resolved values reuse **`agent_context_resolvers`** as-is (`internal/runtime/agent/context_resolver.go`, `cmd`/`http` resolver kinds, DB-configurable per agent, `GET/POST/PATCH/DELETE /api/agents/{id}/context-resolvers` — the carried-forward mechanism from the boot-profile-catalog retirement, documented in this project's own `CLAUDE.md`). No second resolver-provider system gets built for skills specifically.
- **Nested skills.** Resolved through the same vendored store, by declared dependency — composition only works between installed, trusted packages, never an arbitrary path (see "Composition" below).
- **Inline deterministic execution.** The retired `` !`cmd` `` marker is not revived as-is. It's rebuilt as one materializer provider among the others above — structurally parsed with real awareness of markdown code-fence boundaries (closing the accidental-execution gap the old regex had), and routed through the same policy/sandbox gate every script execution goes through, rather than a bare, ungated shell-out.
- **Scripts.** `scripts/` files execute through the same policy/sandbox gate as inline markers — see below. Distinct from inline execution only in that a script is an explicit file the package ships, not text computed from the `SKILL.md` body.

## Composition: inline vs. fork

`Context: "inline"|"fork"` already exists as a frontmatter field and is fully dead — parsed, stored, never read. Given real semantics:

- **`inline`** — a nested skill's materialized content is spliced into the parent's materialized output before the model ever sees either. Pure content composition (e.g. `release-review` pulling in `changelog`'s rendered body). No new execution path — it's the Resolver recursively materializing a dependency and concatenating the result.
- **`fork`** — invoking the parent instead delegates the nested skill to its own agent turn/subagent invocation (riding the harness's existing subagent/fork machinery, not a new execution mechanism), and only that invocation's *result* folds back into the parent's materialized output. Real delegation, not text-splicing.

Provenance is tracked as a chain — user → agent → root skill → nested skill → script/materializer → requested capability, per the handoff doc's security section — and cycle/recursion-limit detection runs in the Skill Resolver against the install-time dependency graph (checked once, at install/sync time, against already-installed dependencies) rather than discovered live during a materialization pass.

## Security, sandboxing, and trust

- **Script/materializer execution** reuses `go-sandbox`'s `Profile`+`Apply` (the same primitive [16-agent-host.md](16-agent-host.md) documents `go-agent-wrapper` wrapping for whole agent processes) directly — a short-lived, sandboxed, read/compute subprocess exec needs none of the full host/wrapper session machinery (no PTY, no protocol adapter, no long-lived process supervision).
- **Policy enforcement** (what a given skill's script/materializer may touch — filesystem scope, network, subprocess, environment/secrets) plugs into the same `policy.Engine`/`policy.Store` shape the host already runs for CLI tool-call interception — mechanism is host/product-shared, the actual rule set is skill-specific, same "host owns the interception mechanism, product owns the policy" line [16-agent-host.md](16-agent-host.md) already draws.
- **Default posture is read/compute/materialize**, matching the handoff doc directly — anything mutating or higher-risk requires an explicit opt-in grant, not an ambient capability.
- **Trust invalidation is the vendored content hash.** Approval is granted against a specific hash; a changed source requires a new explicit install before it affects anything, and that new install carries a new hash requiring its own approval. "Changed executable source invalidates prior trust" falls out of the vendoring model directly rather than needing separate bookkeeping.

## The invocation gap

The most load-bearing gap found in this session: **no skill's body content has ever reached a model, in any runtime, through any live mechanism.** The catalog block (API path) renders name/description/tool-bindings only; there's no CLI-side skill delivery at all today (see "Delivery" above). Composition and parameterization are moot until this is fixed, since they only matter once content can actually reach a model.

- **API-direct agents**: a new self-tool (`skill_invoke` or equivalent naming) is the real entry point into the Resolver → Materializer → Policy/Sandbox → Materialized Skill pipeline. An agent calls it with a slug plus parameters, gets back fully materialized content (parameters resolved, `inline` dependencies spliced in, `fork` dependencies delegated and folded back) as a tool result. The catalog block stays a lightweight teaser — progressive disclosure, matching the Agent-Skills-spec's own pattern, not a departure from it.
- **CLI-hosted agents**: no Nanite self-tool needed. Once the vendored package is planted at the runtime's native skill location (see "Delivery"), the agent's own native skill mechanism is the invocation path. Nanite's responsibility ends at planting.

## API surface

Frontend/admin-UI work for this redesign is an explicitly separate stream, out of scope here — but the REST contract it will need is this stream's responsibility to define and expose:

- **Install/sync** a skill package (by path or upload) into the vendored store + index; re-sync re-hashes and re-vendors on change.
- **List/get** indexed skills — trust tier, content hash, version, install provenance, declared composition dependencies.
- **Assign/revoke** a skill to an agent (the actual grant, distinct from merely existing in the index).
- **Grants/policy view** — what capabilities a skill's materializer is approved for, and whether that approval is still valid against the skill's current vendored hash (surfacing "content changed since approval, re-approval required" states).
- **Invoke/preview materialization** — run the Resolver/Materializer pipeline for a given skill + params outside of a live agent turn, so authoring/debugging doesn't require a real chat session.
- **Delete/uninstall** a skill from the vendored store and index.

Additional endpoints (authoring/scaffold helpers, usage/activation telemetry, etc.) are deferred until the frontend stream actually needs them rather than speculatively built now.

## What's cut

- `mcp.Manager.AutoDiscover`'s writes into the `skills` table — tool visibility stays the tool catalog's job, never a skill again.
- All 8 embedded builtin skills and the small set of DB-originated rows alongside them — clean-slate migration, no carried-forward content, per operator direction.
- The old `` !`cmd` `` marker as it existed — unsandboxed, code-fence-unaware, zero production callers. Rebuilt (see "Materialization pipeline"), not revived.
- `BrokerHints []string` on `skill.Definition` — orphaned since `internal/skillbroker`'s deletion; no replacement, no consumer, drop the field.
- The `Context: "inline"|"fork"` field's *old* meaning (none — it was parsed and never read). The field name and values survive with the real semantics defined in "Composition" above.

## What's genuinely still open

- Exact table/column shape for the new index (this doc specifies what it must hold — vendored path, hash, version, source tier, enablement, grants, dependencies — not literal DDL).
- Exact `skill_invoke`-equivalent self-tool schema (slug, params shape, how a `fork`-composed result gets represented in the tool result).
- Exact vendored-store directory/addressing convention.
- Exact policy rule vocabulary for skill capability grants (fs scope shape, network allowlist shape, etc.) — likely needs to be defined alongside, not independently of, whatever [16-agent-host.md](16-agent-host.md)'s `policy.Store` rule shape ends up being for host-level tool-call interception, so the two don't diverge into parallel vocabularies for the same kind of decision.
- Whether `agent_known_skills`' existing telemetry shape (`activation_count`/`pinned`/`ttl_seconds`, currently dead for prompt assembly per `TASKS/phase-0/17`) gets reused for the new grants/telemetry table or replaced outright — a real precedent worth checking before building a third shape from scratch, not resolved in this session.
- Whether ecosystem-format packages (e.g. a `.claude/skills/` directory authored outside Nanite) can be installed through the same explicit-sync path unmodified, or need a format-detection/adaptation step first.
- Migration/rollout sequencing into actual phased tasks — explicitly out of scope for this doc, which is architecture only.
