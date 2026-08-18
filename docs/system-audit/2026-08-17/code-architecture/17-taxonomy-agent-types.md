# 17 — Taxonomy: What Is An "Agent," Which "Nanite," and Which "Reflex"?

> Synthesis doc, written after reading all 16 sibling code-architecture docs. Not a review; no recommendations. This doc exists specifically to answer "which of these am I actually looking at?" — every claim below is sourced from a sibling doc, cited by filename.

## 1. Purpose

The project owner named this confusion directly: file-based agents vs. database agents vs. durable agents; CLI-launched vs. API-launched agents; and — sharpest of all — the local Claude Code agents/subagents a developer runs (the very mechanism that produced this audit) getting mentally conflated with Nanite's own agent system. Reading all 16 subsystem docs surfaced that this is not one confusion but **at least eight independent naming collisions**, several of which are historical accidents (two unrelated things absorbed into the same binary and given the same name) rather than a single conceptual muddle. This doc inventories all of them in one place.

## 2. The big one: two completely unrelated things are both called "Nanite"

- **Nanite the product** — the Go binary (`cmd/nanite/`, `internal/`) that is the actual system this whole audit is about: a multi-agent chat harness with its own agent definitions, boot process, tool broker, etc.
- **"nanite" the operator's personal meta-dev-tooling framework** — a separate framework (originally a distinct project called **"agentrc"**) for structuring *how a human boots a Claude Code CLI session to work on a codebase* — role files, skill files, a `Boot <agent>` convention, a `boot-prompt.md` continuity note. This framework was **absorbed wholesale into the Nanite binary and renamed to match it** (`01-agent-definition-and-config.md` §6). It now ships inside the Nanite repo (as `.nanite/config.yaml`'s `agents:` block, this repo's own `CLAUDE.md`, and the `~/.nanite/` directory tree) purely by historical accident of that absorption, not because it has any runtime relationship to the product.

These two systems now **share the exact same directory name** (`.nanite/` in a project root, `~/.nanite/` in a user home) and even some of the same *subdirectory* names (`agents/`, `skills/`), which is why the collision is so easy to fall into:

| Path | Product meaning | Meta-framework meaning |
|---|---|---|
| `.nanite/agents/*.md` | Real `agent_profiles` definitions the product ingests at boot (if the file has YAML frontmatter) | N/A — the meta-framework doesn't use this path |
| `~/.nanite/skills/*.md` | Scanned by `internal/skill.Discover` as user-tier Nanite skills (if the file starts with `---` frontmatter) | The operator's own skill library for Claude Code sessions (plain markdown, no frontmatter) |
| `.nanite/config.yaml`'s `agents:` block | **Not read by any Go code at all** | The `Boot <agent>` convention's role lookup table |
| `.nanite/boot-prompt.md` | Not part of the product | Session-continuity note for the human operator's Claude Code session |

The overlap at `~/.nanite/skills/` is not coincidental — the product's `skill.Discover` was written to scan that exact path — but the two systems **disagree on file format**: the meta-framework's files are plain markdown with no frontmatter; the product's parser requires a leading `---` block or silently skips the file. Verified directly: of 42 files in `~/.nanite/skills/`, only 1 (`interview.md`) happens to have frontmatter and loads as a real Nanite skill; the other 41 — exactly the files the operator's own `CLAUDE.md` boot process expects to load — are silently invisible to the product (`14-skills-and-knowledge.md` §4). This is the single most literal, concrete instance of "file-based agent confusion" found anywhere in this audit: two systems, same directory, disjoint file formats, one silently ignoring the other's files.

**A third, unrelated thing also lives at `internal/assets/framework/`**: a binary-embedded *copy* of the meta-framework's own content (roles/skills/commands), extracted to disk by an install pipeline to scaffold a fresh `~/.nanite/` tree for a new operator. It is not read by `skill.Discover` and does not participate in the product's skill pipeline at all (`14-skills-and-knowledge.md` §4).

**Explicitly out of scope of the product, and unrelated to any of the above**: the local Claude Code agents/subagents launched via the CLI's own Task/Agent tool (`general-purpose`, `Explore`, `Plan`, `fork`, etc. — the exact mechanism that produced this audit). These are ephemeral, in-process constructs of the Claude Code CLI/SDK itself. They never touch `agent_profiles`, are not read/written/launched by any code in the Nanite repo, and have no relationship to Nanite's runtime whatsoever (`01-agent-definition-and-config.md` §6). The one place the vocabularies can textually brush against each other: Nanite's adapter-discovery tier (priority 5+ in `agent.Discover()`) is *capable* of importing `.claude/agents/*.md`-formatted **files** as additional Nanite agent definitions — that is Nanite reading a file format Claude Code's own subagent convention happens to also use, not any interaction with a live Claude Code subagent session.

## 3. Agent identity: file-based vs. database-backed vs. durable

This is not three independent mechanisms — it's one real mechanism (files) with a DB-side runtime cache, plus a lifecycle wrapper.

- **File-based `agent_profiles` definitions** are the actual source of truth. Markdown + YAML frontmatter, discovered from (priority order): a `--agent` CLI flag, `.nanite/agents/*.md` (project), `~/.nanite/agents/*.md` (user), `plugins/*/agents/*.md`, adapter-discovered external formats, and `internal/agent/builtin/profiles/*.md` (compiled into the binary). Live DB proof: **every one of the 28 `agent_profiles` rows has `format='markdown'` and a `source_ref` pointing at a real file** — 9 embedded, 19 on-disk. `AutoIngestAgents` overwrites the DB row's content from the file on every boot; the file is authoritative (`01-agent-definition-and-config.md` §3–4, `06-live-runtime-db.md`'s dedicated file-vs-DB section).
- **"Database-backed" agents are not a distinct fourth mechanism.** In practice this phrase describes the `agent_profiles` table itself — a runtime-queryable projection of the files above, rebuilt on every boot. The one genuine exception: a `durable_agent_instances` row created via the recipe-apply flow has no YAML file counterpart, though it still points at a file-backed `agent_profiles` row for its actual persona (`01-agent-definition-and-config.md` §3.2–3.3).
- **Durable-agent YAML** (`.nanite/durable-agents/*.yaml`) is a **lifecycle wrapper, not an identity**. It never defines behavior on its own — it points at an *existing* file-based `agent_profiles` row by `profile_slug` and adds scheduling/runtime-kind/launch-source metadata, turning that profile into a standing, wakeable `durable_agent_instances` row (`09-durable-agents-runtime.md` §1, `01-agent-definition-and-config.md` §3.2).

**"Durable" is a runtime-lifecycle property, not a different kind of agent.** A durable agent's actual "thinking" step is the identical `ChatService.HandleMessage` call a human's chat message takes — what's different is everything *around* that call: an identity that outlives any single session, a schedule/trigger mechanism, and session-reuse bookkeeping (`09-durable-agents-runtime.md` §1). Four `lifecycle_class` values (`advisor`/`process`/`template`/`harness`) determine whether a wake reuses one long-lived session, creates a fresh one every time, or something in between.

## 4. Launch mechanisms: how many ways does a session actually start?

Nine distinct triggers exist, but by code-level inspection they converge on exactly two runtime primitives: `internal/runtime/agent.Boot` (for CLI-shaped providers — spawns a subprocess) and `provider.StreamChat` (for API-shaped providers — an in-process HTTP call, no subprocess) (`03-launch-paths.md` §7). The proliferation is in the number of *entry points*, not in the number of underlying mechanisms:

1. `nanite launch <profile-id>` — standalone CLI, no `nanite serve` required
2. Browser chat UI — `POST /api/sessions` + first-turn cold boot
3. `nanite chat` — CLI client of the same HTTP control plane as #2
4. Durable-agent Start/Resume/Wake — API-triggered
5. Durable-agent Wake — internal 2-minute scheduler tick
6. Loom Curator callback wake — external webhook, purpose-built decode handler
7. A2A JSON-RPC — external protocol front door, routes to #4/#5 or a workflow run
8. In-session subagent delegation — the LLM's own `subagent_spawn` tool, or `POST /api/sessions/{id}/delegate`
9. Workflow-runner launch — a deliberately separate mechanism (external script subprocess), not a "Nanite agent session" in the same sense as the other eight

**CLI-launched vs. API-launched is a per-session property, not a separate universe of agents.** Whether a given launch produces a subprocess or a direct API call is decided by whether `sessions.provider` resolves to a CLI-shaped name (`pty`, `pty-*`, `sub-*`) or a registered `llmcontracts.Provider` (currently only `anthropic`/`openai`) — the same agent profile, the same durable-agent instance, can in principle run through either path (`03-launch-paths.md` §2.2, `06-provider-llm-roundtrip.md` §3.3).

**"Boot-profile catalog" launches are a variant of #2/#3, not a separate mechanism.** Selecting a `bootprofile:<id>` row in the provider dropdown is the same `POST /api/sessions` call with `provider` set to that encoded string; the difference is purely in *what prompt content gets used* (a catalog-compiled `LaunchSpec.BootPrompt` overriding the DB profile's `SystemPrompt`), not in how the session is started (`02-boot-process.md` §3b, `03-launch-paths.md` §2.2).

## 5. Torque does not launch Nanite agents

This was an explicit premise going into this audit ("Torque orchestrates launching Nanite agents remotely") and turned out to be **factually wrong**, confirmed by direct inspection of the Torque repository: Torque has its own, complete, independent agent-boot runtime (its own `internal/runtime/agent.Boot`, its own `LaunchProfile`/`LaunchPlan` assembly, its own session store). It never invokes Nanite's HTTP API, never shells out to the `nanite` binary, and has no dependency on the `nanite` Go module at all. The two apps share only low-level libraries (`agentkit`, `go-providers`) — Torque's own engineering notes state this outright: *"Torque has no notion of mux subagents... The nanite/mux `subagent_spawn` parent-ID requirement is an external constraint that does not apply to torque-orchestrated runs."* (`03-launch-paths.md` §2.10). Torque tracks work against the Nanite repo (as a *target codebase*, the same way it tracks work against any other repo) in the same task-ID namespace as its own internal development, distinguished only by a per-task `WorkingDir` string (`04-torque-dev-sessions.md`, "Orchestration-specific pain"). Where confusion is warranted going forward: a shared `agentlaunch.LaunchPlan` *contract* means a plan that validates for one app validates for the other — but that is a format-compatibility statement, not a runtime call relationship (`03-launch-paths.md` §2.10).

## 6. Naming collisions inside the product itself

These are not user-facing taxonomy questions so much as internal vocabulary reuse that every sibling doc had to explicitly work around — listed here because several directly produced live incidents in the chat-analysis findings.

| Term | Collides across | Resolution as documented |
|---|---|---|
| **"Reflex"** | (1) `internal/agent/reflexes` — a DB-backed, host-evaluated predicate/event/interval engine that injects `<system-reminder>` text outside the LLM's own tool-calling loop. (2) `internal/promptrouter` (formerly literally named `internal/reflex`) — a deterministic phrase-match router that picks which sub-agent role a message dispatches to. | Explicitly renamed apart at the package level specifically because of this collision (`08-reflexes-internal-tooling.md` §1; `11-broker-strategy-steering.md` §8 confirms the rename history). Chat evidence shows this took **two live renames across two sessions** to fully settle (`02-nanite-dev-sessions-late.md`, "Taxonomy confusion"). |
| **"Broker"** | Five independently-evolved rule-based deciders share the word: the **agent broker** (`agentkit/broker`, decides which agent profile handles a turn), the **strategy planner** (not literally called "broker" in code but performs the same class of decision — turn budget/approach), the **tool broker** (`go-toolbroker`, decides which tools are exposed), plus a documented **"broker quartet"** referenced in `internal/skillbroker`'s own doc comment (Context/Tool/Skill/Agent). | `agent_broker_decisions` exists as a separately-named table *specifically because* `broker_decisions` (the tool broker's table) was already taken (`11-broker-strategy-steering.md` §9) — i.e., the collision predates and shaped the current schema. |
| **"Workflow"** | `internal/workflow` — a pre-existing, unrelated generic YAML pipeline runner. `internal/agentworkflow` — the newer Agent Workflows subsystem (DAG-based, `WorkflowEngine`/`StepExecutor`). | Independently rediscovered and re-verified from scratch in 3 separate dev sessions before being filed as a known issue, and was still unresolved (dead legacy code left in the tree) as of the chat-analysis window (`01-nanite-dev-sessions-early.md`, `02-nanite-dev-sessions-late.md`, both "Taxonomy confusion"). |
| **"Template"** | `prompt_templates`/`agent_prompt_templates` — composable system-prompt fragments. `templates` — an unrelated content-formatting feature (task-summary/code-review/standup output snippets). | Two unrelated tables, unrelated jobs, same word (`14-skills-and-knowledge.md` §8). |
| **"Mode"** | Four separate, independently-persisted mechanisms share the vocabulary `chat`/`plan`/`work`: (1) **Session Mode** — `modes` table + `sessions.current_mode_id`, the current live path. (2) **Legacy Agent Mode** — `agent_modes` table, keyed to the agent not the session. (3) **Mode↔Agent assignment** — `agent_mode_assignments`, 0 rows, no found production writer. (4) **Per-turn classified mode signal** — `classify.ClassifyMode`, ephemeral, not persisted as a Mode at all, but the one that actually drives the agent broker's routing rules. | Not a layered system — four distinct tables/mechanisms that happen to share vocabulary (`11-broker-strategy-steering.md` §5). |
| **"Session handoff" / "handoff stash"** | `session_handoffs` — a fully-schema'd, **zero-store-method**, never-implemented multi-agent handoff request/approval workflow. `handoff_stashes` — the actively-used pre-compaction continuity snapshot (two payload schemas: legacy P7 and Glass-4). | Same word, unrelated concepts, one dormant and one live (`13-session-lifecycle-recovery.md` §6). |
| **"Boot"** (the CLI-boot bootdir mechanics vs. the meta-framework's `Boot <agent>` convention) | See §2 above. | Structurally unconnected, sharing only vocabulary. |

## 7. Quick-reference: "which one is this?"

Use this when a mention of "agent" or a related term is ambiguous:

- **A markdown file with `---` frontmatter under `.nanite/agents/`** → a real Nanite `agent_profiles` definition (see §3).
- **A markdown file *without* frontmatter under `.nanite/agents/` (e.g. `backend.md`, `planner.md`)** → the meta-framework's dev-persona doc, read only by the `Boot <agent>` CLAUDE.md convention, not by any Nanite Go code (see §2).
- **A YAML file under `.nanite/durable-agents/`** → a lifecycle wrapper for an *existing* profile, not an identity of its own (see §3).
- **"Claude booted a subagent to do X"** → almost certainly the Claude Code CLI's own Task/Agent tool, unrelated to Nanite (see §2), *unless* the transcript is explicitly about Nanite's `subagent_spawn` self-tool inside a running Nanite session (see §4, launch #8).
- **"Torque launched an agent against the nanite repo"** → Torque's own independent agent-boot runtime, targeting the Nanite *codebase* as a work item — not Nanite's runtime launching anything (see §5).
- **"The agent's reflex fired"** → check whether it's the predicate/event engine (`internal/agent/reflexes`) or the phrase-match router (`internal/promptrouter`) — see §6.
- **"The broker decided X"** → check which of the five deciders — agent, tool, strategy, skill, or context — see §6.

## 8. Cross-references

Every claim above is sourced from one or more of: `01-agent-definition-and-config.md`, `02-boot-process.md`, `03-launch-paths.md`, `06-provider-llm-roundtrip.md`, `08-reflexes-internal-tooling.md`, `09-durable-agents-runtime.md`, `11-broker-strategy-steering.md`, `13-session-lifecycle-recovery.md`, `14-skills-and-knowledge.md`, and the chat-analysis docs `01`/`02-nanite-dev-sessions-*.md`, `04-torque-dev-sessions.md`. See `00-overview.md` for how this taxonomy interacts with the broader complexity/steering findings.
