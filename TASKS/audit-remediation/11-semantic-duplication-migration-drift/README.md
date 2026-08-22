# 11 — Semantic duplication / migration drift

This folder covers Wave 6 of the remediation guide (`~/dev/chrispian/inbox/nanite-audit-triage-remediation-planning-guide.md`
§4, "Wave 6 — Semantic duplication / migration drift"): 16 instances of "the
same concept implemented twice" found across the whole audit — naming
collisions, migration drift, and mechanical/semantic duplication, spanning
package clusters reviewed in `REPORT.md` §8.1 through §8.10 and §8.12. Unlike
most of this batch's other folders, this one is not organized around a single
root cause — it is a deliberate cross-cluster collection, because the guide's
own Wave 6 instruction is to build exactly this kind of table across the
whole audit rather than leave each duplication finding scattered inside its
own package-cluster section.

## The guide's classification scheme — read before triaging any item below

The guide's §4 gives five classifications for every duplication finding:

1. **textual-only boilerplate** — same shape, no semantic weight; a pure DRY
   question.
2. **same semantics/stable** — two implementations of the same rule that
   currently agree, with no bug riding on the duplication, but no mechanism
   keeping them in sync either.
3. **same semantics/divergent behavior** — two implementations of what is
   supposed to be the same rule that have **already** produced different,
   real, observable behavior for the same case.
4. **migration drift** — a newer/shared mechanism exists and an older
   implementation was never migrated onto it, usually visible as one sibling
   already using a shared primitive while the other still hand-rolls the
   identical logic.
5. **intentionally independent** — the duplication is deliberate and
   documented; the two implementations are meant to answer genuinely
   different questions, even if they look similar on the surface.

The guide's own instruction, quoted directly: **"Prioritize semantic
divergence and migration drift over LOC reduction."** Classification 3 is
the single highest-priority category in this folder — a confirmed behavioral
divergence in a shared semantic contract is a correctness concern, not a
maintainability one, per the guide's own "Semantic Duplication" standard
(§4 Wave 7): *"Duplicating syntax is a maintainability concern. Duplicating
a semantic rule is a correctness concern."* Classification 4 items follow
close behind, since they represent a real, identified gap between a known-good
shared mechanism and a sibling that was never migrated onto it — the fix is
usually clear (migrate the sibling), but skipping it leaves a correctness fix
applied to only one of two places a future bug report could originate from.
Classifications 1, 2, and 5 are, respectively, pure LOC-reduction cleanup,
currently-stable-but-unenforced duplication, and deliberate design the audit
confirmed is not actually a problem — all lower priority than 3 and 4 per the
guide's own ordering, even though several of them (particularly the
classification-1 items) are individually cheap, low-risk fixes a planner
may still choose to schedule early simply because they're easy.

## Classification table

Sorted with classification-3 and classification-4 items first, per the
guide's own priority instruction — this is **not** the numeric order of the
task files themselves (see each task file's own `Depends on`/`Touches` for
sequencing-relevant detail; this table is a priority index, not an execution
order).

| Concept | Old/weaker implementation | New/shared implementation | Classification (1–5) | Task file |
|---|---|---|---|---|
| Provider streaming: malformed tool-call-argument JSON handling | Anthropic (`internal/llm/anthropic/stream.go:232-253`) degrades gracefully | OpenAI (`internal/llm/openai/stream.go:96-123`) aborts the entire turn — **confirmed real behavioral divergence, not just risk of one** | **3 — same semantics/divergent behavior (highest priority in this folder)** | `06-provider-streaming-error-handling-divergence.md` |
| Subagent completion policy vs. message wake policy | `resolveSubagentCompletionPolicy` (`internal/service/subagent_reactor.go`) hand-rolls the cascade walk | `resolveMessageWakePolicy` (`internal/service/messaging_reactor.go`) uses the shared `override.Resolve` primitive | **4 — migration drift** | `01-subagent-completion-vs-message-wake-policy.md` |
| Harness v1 vs. native durable-agent start/resume/wake handlers | `internal/api/harness_v1.go` (hand-copied) | `internal/api/durable_agents.go` / `durable_agent_wake.go` (native) | **1 or 5 — genuinely ambiguous; architect decision determines which** (treated as priority pending resolution, since the "accidental duplication" reading would make this classification 4-adjacent migration drift in spirit) | `02-harness-v1-vs-native-durable-agent-handlers.md` |
| SSRF CIDR denylist (loopback/RFC1918/CGNAT/IMDS/IPv6 ULA) — **cross-referenced only, implemented in `08-remaining-security-hardening/`** | `internal/sandbox/proxy.go` (comment states intent is parity, unenforced) | `internal/mcp/general_tools.go` (independent copy) | **4 — migration drift / 1 — textual boilerplate** (parity intended but not mechanically enforced) | `07-ssrf-cidr-denylist-duplication.md` (pointer only — see `08-remaining-security-hardening/` for the real task) |
| `internal/config`'s `Config` vs. `AppConfig` vs. XDG layout, all under one package name | N/A — not code duplication | N/A | **Naming collision, closest to 4 — migration drift in spirit** (organic growth of unrelated concerns under one name, not intentional design; explicitly not code-copy duplication) | `08-config-package-naming-collision.md` |
| Elicitation client-side near-duplicates + aspirational dead-code doc | `ElicitUserInput` / `routeClientElicitation` (`internal/mcp/elicitation.go`) — inconsistent `context.Canceled` handling | N/A — no shared mechanism yet | **4 — migration drift** (GO-CHAT-004) **+ dead-code/stale-doc, not a duplication classification** (GO-MCPTOOL-004: package doc describes a `ClientElicitMiddleware` that was never built) | `09-elicitation-client-side-duplication-and-dead-doc.md` |
| StructuredMessage JSON-unwrap algorithm | `recovery/pack.MessagePlainText` (hand-rolled anonymous struct) | `chat.replayContent` (full shared type) | **2 — same semantics/stable** (currently agree; the comment-stated import-cycle constraint keeping them separate may itself be false — verification is this task's first step) | `03-structuredmessage-unwrap-duplication.md` |
| Traffic-light calculation | `internal/service/inspector_producers.go`'s `trafficLightFor` (byte-for-byte reimplementation, created because the original is unexported) | `internal/inspector/service.go`'s `trafficLight` (unreachable in production, only its own test calls it) | **2 — same semantics/stable** | `04-traffic-light-calculation-duplication.md` |
| MCP post-`CallTool` result-processing tail | N/A — both call sites are genuinely necessary | `Manager.ExecuteTool` / `Manager.ExecuteToolOnServer` (`internal/mcp/manager.go`), identical shared tail duplicated between them | **1 — textual-only boilerplate** | `05-mcp-result-processing-tail-duplication.md` |
| `*envelopes.Registry` pointer, held independently 3 times | N/A — each holder serves a genuinely distinct consumer | `internal/chat`, `internal/envelope`, `internal/plugin.Host` each hold their own copy; `main.go` makes 3 manual setter calls | **1 — textual-only boilerplate / plumbing duplication** | `10-envelope-registry-triplication.md` |
| `DevServerName = "dev"` constant | `internal/toolclient/broker.go` (redundant redeclaration) | `internal/mcp/naming.go` (canonical, already imported by `toolclient`) | **1 — textual-only boilerplate** (trivial) | `12-devservername-constant-duplication.md` |
| Query → scan-loop → append shape across ~20+ `internal/store` list methods | N/A — no generic helper exists | N/A — 41 `dupl` hits, every site individually correct | **1 — textual-only boilerplate** (explicitly optional per the audit's own false-positive framing) | `13-store-scan-loop-duplication.md` |
| Manifest-loading/registration/lifecycle boilerplate across 4 CLI-ecosystem adapter plugins | N/A — no shared helper exists | `adapter-claude` / `adapter-codex` / `adapter-gemini` / `adapter-opencode`, byte-for-byte aside from names/strings (`adapter-nanite-native` correctly excluded — genuinely diverges) | **1 — textual-only boilerplate** | `14-adapter-plugin-boilerplate-duplication.md` |
| `internal/api` response/decode boilerplate + `agent_capabilities.go`'s 4 parallel CRUD families | 3 `*API`-receiver call sites bypass the existing `a.decode` helper; 7 `dupl`-flagged pairs within 4 CRUD families | `a.decode` (`internal/api/api.go`) already exists and is bypassed for no apparent reason | **1 — textual-only boilerplate** (both GO-API-006 and GO-API-009) | `15-api-response-boilerplate-duplication.md` |
| `dispatch_to_agent` reflex evaluation, upstream vs. inside `task_execute` | `internal/service/chat_reflex_dispatch.go` (upstream: should the LLM reach for `task_execute`) | `internal/selftools/self_tools_dispatch.go` (inside `task_execute`: which agent to target) | **5 — intentionally independent** (self-documented; residual risk is untested rule-interpretation sync, not the dual-evaluation design itself) | `11-dispatch-reflex-double-evaluation.md` |
| "Workflow"-branded package naming (`internal/workflow` / `workflowrunner` / `agentworkflow`) + `internal/dispatch` vs. `internal/dispatcher` | N/A | N/A | **5 — intentionally independent** (both already well-documented in-repo; no action required) | `16-workflow-naming-and-dispatch-naming-collisions.md` |

## What "creating the task" means here, restated for this folder specifically

Per this batch's top-level `README.md`, none of the 16 tasks above have been
executed — every file's `Status` is `not-started`. Six of the sixteen carry
`requires_architect_decision: true` (items 01, 02, 03, 06, 08, and the
GO-CHAT-004/GO-MCPTOOL-004 half of 09) because the audit's own finding
records flag them that way, or because this task-creation pass identified a
genuine two-reading ambiguity the audit itself could not resolve (most
notably item 02's Harness v1 question and item 06's OpenAI "fail loud"
question) — both readings are presented in full in the relevant task file
rather than this pass picking one. Item 07 is a pointer, not an
implementation task — its real work lives in
`TASKS/audit-remediation/08-remaining-security-hardening/`. Item 16 is the
shortest file in this folder by design: both findings it covers require no
action beyond continued awareness.

## Cross-references worth noting for a future planner

- **Item 07** (SSRF CIDR denylist) is the one item in this table whose actual
  implementation task lives outside this folder, in
  `08-remaining-security-hardening/`, because its remediation is
  security-flavored and benefits from that folder's reviewer context. It is
  listed here anyway so it isn't lost from the Wave 6 classification record.
- **Items 12 and 15** (the `DevServerName` constant, and `internal/api`'s
  bypassed-helper/CRUD-family duplication) are both flagged in their own
  task files as candidates a planner could alternatively batch into
  `13-mechanical-cleanup/`'s pass instead of running as standalone Wave 6
  tasks — neither carries semantic-divergence risk, so either home is
  defensible; this folder's own task files are the canonical description of
  the work either way.
