# Nanite ↔ Anthropic Patterns — Alignment Matrix

**Date:** 2026-04-05
**Inputs:** `anthropic-digest-cluster-1-2.md`, `anthropic-digest-cluster-3-4.md`, `anthropic-digest-cluster-5.md`, `nanite-architecture-snapshot.md`, `user-workflow-observed.md`
**Scope:** Snapshot comparison only. Gaps and recommendations live in `gaps-and-opportunities.md`.

## Legend

- **Aligned** — Nanite implements the pattern substantively; any differences are cosmetic or configuration-level.
- **Partial** — Primitive exists but is incomplete, not wired end-to-end, or missing a load-bearing piece.
- **Missing** — No implementation; may be planned in docs or deferred.
- **Divergent** — Nanite does something measurably different. Not automatically wrong — flagged for discussion.
- **N/A** — Pattern doesn't apply at Nanite's layer.

---

## 1. Chat Engine Loop & Agent Runtime

| Anthropic pattern | Source | Nanite status | Evidence | Note |
|---|---|---|---|---|
| Augmented LLM baseline (single model + tools + memory + retrieval) | building-effective-agents | **Aligned** | `engine.go:256-278`, `engine.go:695-758` | The tool-use loop *is* the augmented LLM. |
| Tool-use loop with environment feedback and ground-truth checks | building-effective-agents | **Partial** | `engine.go:952-1272` | Loop exists; ground-truth verification (tests, browser checks) is not enforced — agents self-report completion. |
| Iteration cap with safety ceiling | building-effective-agents, harness-design | **Aligned** | `engine.go:62-64, 655-658` | 10 default / 100 hard cap. |
| Stuck-loop detection (same tool + identical result) | writing-tools-for-agents (actionable errors), building-effective-agents | **Aligned** | `engine.go:1044-1062, 1190-1213` | Hard-block after 3x identical result; nice-to-have beyond the posts. |
| Think-tool (mid-loop scratchpad for new observations) | claude-think-tool | **Missing** | — | No `think` tool surfaced. Native tool usage guide in system prompt (`engine.go:88-99`) is static, not an LLM-callable scratchpad. |
| Extended thinking before the loop begins | claude-think-tool, multi-agent-research-system | **Divergent** | `provider/anthropic.go` (no thinking-block handling found in snapshot) | Providers expose streaming but extended-thinking blocks are not documented as routed through the pipeline. Needs a definitive check. |
| Auto-mode / reasoning-blind classifier over tool calls | claude-code-auto-mode | **Missing** | `toolclient/permissions.go` | Nanite has denylist + agent permissions; no two-stage classifier, no reasoning-blind judge. |
| Deny-and-continue with retry budget | claude-code-auto-mode | **Partial** | `engine.go:1164-1188` | Tool errors fed back as adaptive messages and consecutive errors escalate, but no explicit "3 retries then escalate to user" contract. |
| Separate generator from evaluator (GAN-shaped review) | harness-design, GAN | **Missing** | — | Orchestrator aggregates worker results via an LLM synthesis call (`orchestrator.go:98-145`), but there is no adversarial critic with its own tools. |

---

## 2. Context Assembly & Management

| Anthropic pattern | Source | Nanite status | Evidence | Note |
|---|---|---|---|---|
| Context as scarce resource; engineered not inherited | all posts | **Aligned** | `context_client.go:86-103, 223-288` | Explicit budget model with cascade. |
| Token budget cascade (prune → reduce tools → drop messages) | harness-design, advanced-tool-use | **Aligned** | `context_client.go:127-163, 223-288` | Well-structured; matches "engineered context" principle. |
| Per-turn compaction of stale tool results | harness-design, multi-agent-research-system | **Aligned** | `context_client.go:127-163` | Tool results >500 chars and older than 2 turns replaced with compact marker. |
| Context reset over in-place compaction for long runs | harness-design | **Missing** | — | No session-reset with structured handoff. The "boot prompt" pattern (observed in user workflow) is a human-driven analog living in `.agentrc/boot-prompt.md`. |
| Filesystem/git as continuity layer across sessions | effective-harnesses-for-long-running-agents, ralph | **Partial** | `.agentrc/boot-prompt.md`, observed user workflow | Exists *around* Nanite (user maintains boot prompt manually). Not a first-class harness primitive. |
| Contextual retrieval (chunk-specific preamble before embed) | contextual-retrieval | **N/A → Blocked** | `contextbroker/source_cortex.go:54-98` | Nanite delegates RAG to Cortex. Cortex has no embedding provider configured (`phase-c-cortex-investigation.md`), so retrieval falls back to keyword search. |
| Hybrid embeddings + BM25 + reranking | contextual-retrieval | **Missing** | — | Same Cortex blocker. |
| Progress file / feature list as persistent backlog | effective-harnesses-for-long-running-agents, ralph | **Partial** | user-workflow-observed.md §Artifact patterns | Exists as manual discipline (boot prompt, Engine tasks via `TaskCreate`). No harness affordance. |
| Multi-source context broker with budget allocation | multi-agent-research-system (memory externalization), building-effective-agents | **Aligned** | `contextbroker/broker.go`, source_cortex/pcc/session/engine | Five sources, budget-aware. |

---

## 3. Tool System

| Anthropic pattern | Source | Nanite status | Evidence | Note |
|---|---|---|---|---|
| Few, high-impact, task-shaped tools | writing-tools-for-agents | **Partial** | `mcp/dev_tools.go`, `mcp/general_tools.go` | Built-ins are task-shaped; external MCP catalog can explode tool count. No curation step. |
| Tool namespacing | writing-tools-for-agents | **Aligned** | `mcp/manager.go` (prefix `mcp__{server}__{name}`) | |
| Progressive disclosure (hot set + deferred catalog via meta-tool) | advanced-tool-use | **Aligned** | `engine.go:53`, `toolclient/meta_tools.go`, `engine.go:956-1026` | Threshold 5; `request_tools` meta-tool; hard cap 3 calls / 2 empty. Close match to Tool Search Tool. |
| Tool search by intent | advanced-tool-use | **Aligned** | `toolclient/broker.go:60-108`, intent-based selection | `LocalBroker.SelectTools()` drives intent matching. |
| Tool descriptions as onboarding docs (examples, unambiguous params) | writing-tools-for-agents, advanced-tool-use | **Unknown** | `toolclient/tool_knowledge.go` | Knowledge base exists; example-per-tool audit not in snapshot. |
| Programmatic tool calling (model writes code that calls tools) | advanced-tool-use, code-execution-with-mcp | **Missing** | — | No code-execution sandbox for tool orchestration. `internal/workflow/` is YAML, not code-interpreted. |
| Code execution with MCP (filesystem-as-tool-catalog) | code-execution-with-mcp | **Missing** | — | |
| Response-format enum (`concise`/`detailed`) per tool | writing-tools-for-agents | **Missing** | — | Uniform truncation policy instead (`truncate/`). |
| Truncation with steering instructions in the response | writing-tools-for-agents | **Partial** | `engine.go:1216-1253` | Truncates + saves full output to disk; "orchestrator hint" present, but no steering text telling the model how to re-query more narrowly. |
| Actionable error messages | writing-tools-for-agents | **Unknown** | — | Error propagation exists; quality of error text is not audited. |
| Tool-testing agent (separate agent exercises tools, rewrites descriptions) | multi-agent-research-system | **Missing** | — | No eval harness for tools. |
| Permission-scoped tool visibility per agent | claude-code-auto-mode, claude-code-sandboxing | **Aligned** | `toolclient/permissions.go`, `toolclient/broker.go:129-144` | Silent-drop pattern is intentional. |

---

## 4. Multi-Agent Orchestration

| Anthropic pattern | Source | Nanite status | Evidence | Note |
|---|---|---|---|---|
| Orchestrator-worker architecture with lead + spawned subagents | multi-agent-research-system, building-effective-agents | **Aligned** | `orchestrator.go:40-54, 186-251`, `delegate.go:40-184` | Structure matches; scale is smaller. |
| Lead uses extended thinking; subagents use interleaved thinking | multi-agent-research-system | **Missing** | — | No differentiated thinking policy between lead and workers. |
| Effort scaling rules encoded in the lead prompt (1 / 2-4 / 10+ subagents) | multi-agent-research-system | **Missing** | `orchestrator.go:186-251` | Decomposer is free-form. No tier heuristic. |
| Explicit non-overlapping subtask boundaries | multi-agent-research-system | **Partial** | `orchestrator.go:62-96` | Plan carries sub-tasks but boundary language is not enforced; decomposer quality is LLM-judgment. |
| Subagent context isolation | multi-agent-research-system | **Aligned** | `delegate.go:40-184` | Each worker runs as a full session with its own history. |
| Lightweight refs passed back; large artifacts to filesystem | multi-agent-research-system | **Missing** | `orchestrator.go:98-145` | Aggregation collects full outputs into the aggregator's context. At scale this reproduces the "copy large payloads through orchestrator" anti-pattern. |
| Memory externalization near context ceiling | multi-agent-research-system | **Missing** | — | No checkpoint/serialize step. |
| Parallel tool calls across subagents and within each subagent | multi-agent-research-system | **Divergent** | `engine.go:952-1272` | Tool execution is serial within a turn (see §3 note in snapshot). Parallel workers exist; parallel tools within a worker do not. |
| Lead-steer mid-flight | multi-agent-research-system | **Missing** | — | Synchronous aggregation only; acknowledged as hard even by Anthropic. |
| Worktree isolation for file-system-level subagent sandboxing | effective-harnesses-for-long-running-agents | **Missing** | `vnext-mvp.md:285` | Explicitly deferred. |
| File-lock / task-lock registry for parallel agents on a shared repo | building-c-compiler | **Missing** | — | Not yet a use case. Relevant if "parallel execution on the Nanite repo" becomes a pattern. |

---

## 5. Plugin Host, Events, Filters

| Anthropic pattern | Source | Nanite status | Evidence | Note |
|---|---|---|---|---|
| Pre-hook cancellation on tool calls and message sends | claude-code-auto-mode | **Partial** | `plugin/events.go:15-64` | Pre-hook events *defined* (`EventMessageSending`, `EventToolExecuting`) but not emitted. Dead wiring. |
| Structured event catalog covering lifecycle | writing-tools-for-agents (observability), multi-agent-research-system (tracing) | **Partial** | `plugin/events.go:15-64` | 21 dead events — defined but never fired. `plugin-hooks-events-filters.md` task 1 is to wire them. |
| Synchronous filter chain on prompt/message/tool-result/response | writing-tools-for-agents, claude-code-auto-mode | **Missing** | `plugin-hooks-events-filters.md:110-150` | Designed, not built. Task 2 in the open thread. |
| Reasoning-blind safety classifier (plugin-like gate) | claude-code-auto-mode | **Missing** | — | Would fit as a filter at `user_message` + `tool_result` entry points once the filter chain exists. |
| Alias map for cross-harness hook names (`PreToolUse` ↔ `tool.executing`) | — | **Aligned** (Nanite original) | `plugin/events.go:71-78` | Nanite-side convenience; matches Claude Code lexicon. |

---

## 6. Sandboxing & Safety

| Anthropic pattern | Source | Nanite status | Evidence | Note |
|---|---|---|---|---|
| Dual isolation: filesystem + network | claude-code-sandboxing | **Missing** | `internal/sandbox/sandbox.go:19-30` | Current "sandbox dir" is a *context directory* for spawned CLIs, not an OS-level isolation boundary. No bubblewrap / seatbelt / proxy. |
| CWD-scoped filesystem read+write | claude-code-sandboxing | **Missing** | — | Tool-level `dev_read` / `dev_write` enforce prefix checks, but the OS is not constrained. |
| Out-of-sandbox network proxy with domain allowlist | claude-code-sandboxing | **Missing** | — | |
| Replace-prompting-with-sandbox UX | claude-code-sandboxing | **Divergent** | planned shell feature: 3-state YOLO (`shell/shell.go:9-42`) | Nanite keeps prompting (`ask` mode) and layers a denylist. Anthropic explicitly argues this creates approval fatigue; Nanite retains it for dev-workflow ergonomics. Worth revisiting against the sandbox model. |
| Denylist of destructive commands | — (Nanite original) | **Aligned** | `shell/denylist.go:10-35` | 24 patterns. Useful but not a replacement for sandboxing. |
| Claude Code on Web-style git-credential proxy | claude-code-sandboxing | **N/A** | — | Nanite is local-first; cloud variant not in scope. |
| Headroom between guaranteed alloc and hard-kill (infra noise) | infrastructure-noise | **N/A** | — | Nanite isn't a benchmark harness; agents run in user's OS process. Relevant only if/when Nanite hosts eval runs. |

---

## 7. Memory & Continuity

| Anthropic pattern | Source | Nanite status | Evidence | Note |
|---|---|---|---|---|
| External memory substrate surviving session boundaries | effective-harnesses-for-long-running-agents, multi-agent-research-system | **Planned** | `phase-c-cortex-investigation.md` | Design in flight. Single `memory` type with subtypes recommended. |
| Namespace isolation by user/project/session | effective-harnesses-for-long-running-agents | **Planned** | `phase-c-cortex-investigation.md` | `app/nanite/{user|project|session}/{id}`. |
| Extraction at compaction + per-turn hooks | harness-design, multi-agent-research-system | **Planned** | phase-c doc | Triggers proposed; not yet wired. |
| Memory-recall view / retrieval model | contextual-retrieval, multi-agent-research-system | **Blocked** | `contextbroker/source_cortex.go` | Blocked on Cortex embedding provider. |
| Self-updating `AGENT.md` / learnings file (ralph pattern) | ralph | **Partial** | `sandbox/sandbox.go:41-65` (CLAUDE.md per session) | CLAUDE.md exists for PTY CLIs; it is not agent-written at runtime, only seeded at session start. |
| Progress file / fix_plan.md | effective-harnesses-for-long-running-agents, ralph | **Manual** | observed: boot-prompt.md maintained by user | Cross-session plan artifact exists but as user convention. |

---

## 8. Workflows & Determinism

| Anthropic pattern | Source | Nanite status | Evidence | Note |
|---|---|---|---|---|
| Prompt chaining (sequential LLM calls with gates between steps) | building-effective-agents | **Partial** | `internal/workflow/engine.go, loader.go` | YAML workflow engine exists, not integrated with chat loop. |
| Routing (classify then dispatch) | building-effective-agents | **Missing** | — | No intent classifier routes to a specialized model/prompt. All traffic goes through one agent loop. |
| Parallelization (sectioning / voting) | building-effective-agents | **Missing** | — | Orchestrator decomposition is sequential-aggregate, not voting. |
| Evaluator-optimizer loop | building-effective-agents, harness-design | **Missing** | — | |
| Planner → Generator → Evaluator stack | harness-design | **Missing** | — | This is *exactly* the shape the user wants for ideation→review workflow. |
| Deterministic bash-loop runner (ralph) | ralph | **N/A / reference** | — | Worth keeping as a simpler alternative model for the long-horizon project mode. |

---

## 9. Evals

| Anthropic pattern | Source | Nanite status | Evidence | Note |
|---|---|---|---|---|
| Any eval harness at all | demystifying-evals-for-ai-agents | **Missing** | — | Nanite has Go unit tests for components. No agent-level eval suite. |
| Bootstrap with 20-50 tasks | demystifying-evals | **Missing** | — | |
| End-state / outcome grading | demystifying-evals, multi-agent-research-system | **Missing** | — | |
| pass@k vs pass^k metrics | demystifying-evals | **Missing** | — | |
| LLM-as-judge with calibrated rubric | demystifying-evals, multi-agent-research-system | **Missing** | — | |
| Capability suite → regression suite graduation | demystifying-evals | **Missing** | — | |
| Tool-testing agent | multi-agent-research-system | **Missing** | — | |
| Eval-awareness mitigations | eval-awareness-browsecomp | **N/A** | — | Not until Nanite ships evals. |

---

## 10. Observed User Workflow ↔ Anthropic Patterns

(From `user-workflow-observed.md`.)

| Observed behavior | Closest Anthropic pattern | Status | Note |
|---|---|---|---|
| Boot-prompt as canonical plan state, updated every session | fix_plan.md, progress file, feature list | **Manual analog** | The user has organically reinvented the ralph/long-running-harness pattern. No harness support today. |
| Parallel child-session dispatch by a "parent orchestrator" session | multi-agent orchestrator-worker | **Manual analog** | Parent session relays state in prose; Nanite's orchestrator is used for intra-session delegation, not cross-session dispatch. |
| `Explore:` subagent for read-only audits | tool-testing agent, progressive search broadening | **Manual analog** | Pattern is consistent across 72% of sessions. No first-class primitive. |
| Pre-spawn clarification (answer 3–5 scoping questions before a subagent runs) | effort scaling rules in lead prompt | **Manual analog** | Currently a user habit; could be a harness convention. |
| Review = "I used it and it works" or Copilot PR comments | end-state grading over path grading | **Aligned in spirit** | Matches Anthropic's preference for outcome over trajectory, but no structured review handoff. |
| Ideation sessions explicitly suppress code output | separation of planning from execution | **Manual** | No session-type primitive. |
| TaskCreate/TaskUpdate replaces TodoWrite | project-specific tracker | **Divergent** | Nanite uses Engine tasks; observed user never touches native TodoWrite. Harness reminders still point at TodoWrite, creating friction. |

---

## Summary Counts

| Status | Count across §§1–9 |
|---|---|
| Aligned | ~14 |
| Partial | ~13 |
| Missing | ~25 |
| Divergent | ~4 |
| Blocked (external) | ~2 |
| N/A | ~4 |

The count is not a score — many "Missing" rows are deliberate deferrals or outside Nanite's current scope (evals harness, OS-level sandbox, multi-agent voting). The actionable set is much smaller and is prioritized in `gaps-and-opportunities.md`.
