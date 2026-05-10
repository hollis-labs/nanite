# Executor Handoff — Architectural Design

## Status

DRAFT (CW-20260429-0030, sprint SP-20260429-0001 Phase B). Implementation is sibling ticket B3 (CW-20260429-0032 — envelope-rendering pilot); classifier routing is B2 (CW-20260429-0031); lens placement is B4 (CW-20260429-0033); chat prompt update is B5 (CW-20260429-0034); telemetry is B6 (CW-20260429-0035). This document commits to a single proposal for each design question and surfaces alternatives in the implementer report, not in the body.

## Motivation

The c107 → c117 incident sequence (2026-04-28 / 2026-04-29) is the proximate motivator. Cumulative effect: the chat agent's system prompt grew from ~250 to 1076 words; bullets from ~6 to 24; negative phrasings from 2 to 14; mechanical gates from 0 to 3. Each individual rule was justified by an observed incident. The cumulative effect was a "rule-following defendant" who picked `info-card` over `report-card` to avoid a grounding rule (c117), and who burned a full 10 turns navigating cached describe-gate responses (c114).

The diagnosis from `agent-context-architecture.md` is that **multi-step recovery loops were leaking into the chat agent's static substrate** — process rules ("call describe before show_card", "validate before render") that belong in classifier-driven runtime injection were instead being baked into the always-on system prompt. The recovery layer (`agentic-error-recovery.md`) was forced to fire as a **preemptive gate** rather than a **reactive lens**, training the agent to expect failure on every call.

The orchestrator-workers pattern (Anthropic, *Building Effective Agents*, Schluntz & Zhang, 2024-12) names the right shape: the chat agent is a coordinator; multi-step structured flows live behind a clean interface in dispatched executors. The chat agent decides *what* needs to happen and *whose problem it is*. The executor decides *how* to satisfy the intent — including the multi-step tool flow, the lens primitives that recover from failures, and the typed envelope that comes back. The chat surface stays narrow; the executor's surface is task-shaped.

This document specifies the handoff contract.

## 1. Boundary

The executor pattern handles **structured multi-step tool flows behind a typed contract**. The chat agent dispatches; the executor runs the flow and returns a result envelope. This is the single decision the chat agent makes per executor-eligible intent: *dispatch or handle inline?*

**Routes through an executor:**

- **Multi-step flows (≥2 tool calls) needed to satisfy a single user intent.** Examples: rendering a `report-card` envelope (describe schema → resolve fields from context → emit → validate → repair if needed); a knowledge-grounded answer that needs `memory_recall` then `knowledge_get` then synthesis; a card-render that needs to consult `lesson_capture` for prior repair hints.
- **All v1 passive renderable envelope types (the pilot scope, B3).** Per `docs/panels/envelope-render-target.md` §"v1 default render target table", the 10 passive renderables are: `report-card`, `info-card`, `list-card`, `metric-card`, `progress-card`, `table-card`, `timeline-card`, `diff-card`, `document-viewer`, `giphy-modal`. These all share the shape "agent has an intent + freeform context, and needs to produce a structurally-valid envelope". They are the cleanest pilot — the lens primitives (describe / validate / remember) already exist for these types, and the recovery loop they motivate is exactly what bloated the chat surface.
- **Recovery loops that span more than one tool.** When validation produces a `wrong_card_type` error, the recovery (re-describe, swap type, re-emit, re-validate) is multi-step by definition — and is exactly the loop the chat agent should *not* be running.

**Stays chat-direct (does NOT route through an executor):**

- **Single read-only tool calls.** `dev_grep`, `dev_glob`, `memory_recall`, `knowledge_get`, `tool_describe` (when called as discovery, not as part of a render loop). One call, one result, no recovery flow needed.
- **Conversational responses.** Anything the chat agent can answer from its existing context without calling a tool. The c117 test specifically protects this case: "let's do some testing — show me X" must still work.
- **Decision-flow envelopes.** `approval-card`, `proposal-card`, `confirmation-card`, `question-form`, `error-report`, `chat-loop-terminated`, `elicitation-prompt`, `subagent-spawn-approval` — these stay inline because they require user-in-the-loop decisions and the chat agent owns the user channel. (Per `envelope-render-target.md` v1 table, these all have `default_render_target: (none — inline)`.)
- **Lens recovery during chat-direct calls that didn't escalate.** If a single chat-direct tool call fails with a recoverable error, the chat agent gets the structured error and can reach for `tool_validate` / `lesson_capture` itself. Reactive recovery on a single-call failure does not promote to executor.

**High-stakes mutations — policy-dependent.** Mutations that cross trust boundaries (subagent spawn, plugin tool calls without prior session-grant, dispatch.ExecuteTask of a Worker-tier agent) **stay on the H1 trust path** (`internal/dispatch/trust.go::TrustResolver`, `subagent-spawn-approval` envelope). The executor pattern does NOT bypass H1 — an executor that needs to do a high-stakes mutation requests it through the same approval flow the chat agent would. The simplification is in the *flow shape* (one envelope back to chat instead of N back-and-forth tool calls), not in the trust model. The policy: **executors inherit the dispatching session's trust-tier and path-grants (see §4); they do not get an elevated trust-tier**.

**Pilot scope (B3) — explicit list:** the 10 passive renderable envelope types named above. This is the smallest bite that exercises the full handoff contract (§2), the lens placement question (§3), and the failure-mode escape valves (§5) without taking on coordination concerns from interactive envelopes.

## 2. Contract

The handoff is a single dispatch round-trip: chat agent calls one tool; the executor returns one result envelope. The shape is the orchestrator-workers protocol, typed for Go.

**Wire-format / Go types — `internal/dispatch/executor.go` (new):**

```go
// ExecutorRequest is the payload the chat agent sends to dispatch an
// executor. Carries intent + minimum-viable context, NOT the chat
// transcript or the agent's full message history.
type ExecutorRequest struct {
    // Intent is the executor-recognized verb. The classifier (B2)
    // populates this from the user message; the chat agent does not
    // free-text it. Initial vocabulary for the B3 pilot:
    //   "render_envelope" — produce one of the 11 v1 passive types.
    // Future vocabulary lands as additional executor profiles register.
    Intent string `json:"intent"`

    // TargetEnvelopeType is the envelope type slug when Intent is
    // "render_envelope" (e.g. "report-card", "list-card"). Empty for
    // intents that do not produce an envelope. The classifier may
    // leave this empty; the executor's first step is to pick the
    // type via tool_describe if so.
    TargetEnvelopeType string `json:"target_envelope_type,omitempty"`

    // UserRequest is the verbatim user message that motivated the
    // dispatch. The executor reads this, not the chat agent's
    // paraphrase. Preserves the user's exact framing for grounding.
    UserRequest string `json:"user_request"`

    // ContextHandles names the slices of session state the executor
    // is allowed to read. Each handle is opaque to the chat agent —
    // it points at scratchpad keys, message-window slices, or
    // memory-recall result IDs the executor can fetch through its own
    // tool surface. Empty = executor starts from scratch.
    ContextHandles []ContextHandle `json:"context_handles,omitempty"`

    // SessionID is the dispatching session. The executor inherits
    // this session's path-grants via the lineage walk (§4).
    SessionID string `json:"session_id"`
}

// ContextHandle is a typed pointer to a slice of session state.
// The handle's Source determines which read API resolves it.
type ContextHandle struct {
    Source string `json:"source"` // "scratchpad" | "memory" | "message_window"
    Key    string `json:"key"`    // source-specific identifier
}

// ExecutorResponse is the single envelope returned to the chat agent.
// One response per dispatch — no streaming, no multi-message reply.
type ExecutorResponse struct {
    // Result is the executor's primary output. Free-form text the
    // chat agent can narrate. Empty when Envelope is set and the
    // chat agent should rely on the envelope's render path.
    Result string `json:"result,omitempty"`

    // Envelope is the structured card the executor produced. Optional;
    // populated for "render_envelope" intent. The envelope is fully
    // validated and (if applicable) repair-stamped by the executor
    // before this response is sent — the chat agent does NOT re-validate.
    //
    // Go shape note: the in-package type is `*dispatch.Envelope` (a
    // projection; lives in `internal/dispatch` to avoid the chat→mcp→
    // dispatch import cycle that `*chat.Envelope` would create). The
    // service-layer seam projects → `chat.Envelope` for over-the-wire
    // consumers; the wire-format JSON is unchanged. See `ExecuteTask`
    // for the precedent.
    Envelope *dispatch.Envelope `json:"envelope,omitempty"`

    // Lessons are repair-hints the executor learned during the flow.
    // **Informational / telemetry only** — the executor persists them
    // via `lesson_capture` itself before responding (see §3 lens
    // placement). The chat agent does NOT call `lesson_capture` (it
    // no longer has the tool). The field is kept for narration ("I
    // learned X") and observability, not for persistence.
    Lessons []learnings.Hint `json:"lessons,omitempty"`

    // Summary is a one-paragraph account of what the executor did,
    // what got coerced/repaired, and what (if anything) was deferred.
    // The chat agent narrates this to the user verbatim or in
    // condensed form. Mirrors the "repair always informs the caller"
    // contract from agentic-error-recovery.md.
    Summary string `json:"summary"`

    // Failure is set when the executor could not produce a valid
    // result within its budget. Carries a typed code + message; the
    // chat agent's response is to surface the failure to the user
    // (NOT to retry the dispatch — see §5).
    Failure *ExecutorFailure `json:"failure,omitempty"`
}

type ExecutorFailure struct {
    Code    ExecutorFailureCode `json:"code"`
    Message string              `json:"message"`
    // PartialEnvelope is set when the executor produced an envelope
    // that failed final validation. The chat agent can still render
    // it as a degraded card if appropriate, with a banner indicating
    // the failure.
    PartialEnvelope *dispatch.Envelope `json:"partial_envelope,omitempty"`
}

type ExecutorFailureCode string

const (
    ExecutorFailureBudgetExhausted   ExecutorFailureCode = "budget_exhausted"
    ExecutorFailureUnrecoverable     ExecutorFailureCode = "unrecoverable"
    ExecutorFailureInvalidIntent     ExecutorFailureCode = "invalid_intent"
    ExecutorFailureMissingContext    ExecutorFailureCode = "missing_context"
    ExecutorFailureTrustDenied       ExecutorFailureCode = "trust_denied"
)
```

**Tool surface on the chat side:** one tool, `dispatch_executor`, with input shape `ExecutorRequest` and output shape `ExecutorResponse`. The chat agent's existing `dispatch.ExecuteTask` primitive is **not reused** — that primitive is for Worker/Planner spawn (full agent profile, full tool surface). Note: `ExecuteTask` is sync today (it coerces `async → sync` at `internal/dispatch/execute.go:221-226` because it must capture the result before returning); the "async-capable" framing applies to its `Mode` field for telemetry, not to runtime behavior. The executor handoff is also synchronous, single-round-trip, and does not boot a new agent profile from scratch; it dispatches into a long-lived executor session managed by the harness.

**Chat agent's contract:**

1. Receive user message.
2. Classifier (B2) produces `Intent` + `TargetEnvelopeType` if applicable.
3. If the intent maps to an executor, build `ExecutorRequest` (verbatim user message, minimum context handles, session ID) and call `dispatch_executor`.
4. Receive `ExecutorResponse`. Render the envelope if present; narrate the summary. (Lessons in the response are informational only — the executor already persisted them via `lesson_capture`; see §3.)
5. **Do not re-execute the executor's flow.** No re-running of `tool_describe`, no re-validating the envelope, no second-pass repair attempts. The executor's response is authoritative for its turn.

**Anti-patterns explicitly excluded from the contract:**

- **Multi-message replies from the executor.** One dispatch = one response. If the executor needs more from the user, it returns a `question-form` envelope as `Envelope` in the response and lets the chat agent route it through the existing user-in-the-loop flow.
- **Streaming intermediate tool calls back through the chat surface.** The executor's tool calls are invisible to the chat agent. Telemetry (B6) records them for observability; the chat agent's transcript shows only the dispatch + response.
- **Free-form `Intent` strings.** The intent vocabulary is registry-controlled. The classifier (B2) selects from the registered set; unregistered intents fail fast with `ExecutorFailureInvalidIntent`.

## 3. Lens placement

**The lens primitives (`describe` / `validate` / `repair` / `remember`) move to the executor's tool surface. The chat agent's surface no longer carries them.**

Concretely:

- **Pre-handoff (today):** `tool_describe`, `tool_validate`, `lesson_capture`, plus the per-type validator at the handler boundary, are all reachable from the chat agent's tool list. The chat agent can call them directly. This is the surface that bloated under c107 → c117.
- **Post-handoff (this design):** the lens primitives are registered on the executor session's profile, not on the chat session's profile. The chat agent's tool list shrinks by exactly these tools; the executor's grows by them. Per-type validators stay at the handler boundary (they fire on every emit regardless of caller, per anti-pattern 5 / decision rule 3 — this is mechanical enforcement, not a tool the agent calls).
- **What the chat agent's tool surface looks like post-handoff:** the existing surface MINUS the lens primitives, PLUS one new tool (`dispatch_executor`). Net: the surface narrows. This is the "handcuffs OFF" direction — the chat agent has fewer tools but a broader capability framing ("dispatch the executor for any structured envelope").
- **What "tool surface" means for each side:**
  - *Chat side:* user-facing meta-tools (`message_send`, `panel_open`), plan/todo primitives (`todo_*`, `plan_*`), the new `dispatch_executor`, and pass-throughs the chat agent uses for its own work (`memory_recall` for context, `knowledge_get` for grounding the user-facing narration).
  - *Executor side:* lens primitives (`tool_describe`, `tool_validate`, `lesson_capture`), the type-specific data tools (`giphy_search` etc.), the envelope emit primitive (`card_show`), plus read-only context tools (`memory_recall`, `knowledge_get`) for grounding.

**Why this works:** the lens was designed as a **reactive** recovery layer (§3 of `agent-context-architecture.md`). It belongs *next to* the action that might fail. After the handoff, the action that might fail (multi-step envelope rendering) lives on the executor. The lens lives there too. The chat agent never "passes through" the lens because the chat agent isn't doing the action — it's dispatching the action. This is exactly the orchestrator-workers split: the worker has the recovery surface; the orchestrator has the user surface.

**Trade-off considered:** the executor could **delegate** lens calls to a sub-tool (i.e., make `describe` / `validate` / `remember` tools the executor calls, rather than tools the executor profile carries). The trade-off is whether "describe" is a tool a model reaches for or a function the executor invokes deterministically. **Decision: keep them as tools on the executor's profile**, not as deterministic functions. The lens was always designed to be model-reached (per `agentic-error-recovery.md`'s "ask a peer" variation — the LLM repair pass needs a tool boundary). Making them deterministic on the executor closes off the LLM-repair fallback. Stay with tools.

**Lessons handling — decision:** the §2 contract returns `Lessons []learnings.Hint` in `ExecutorResponse`, but the **executor calls `lesson_capture` itself** before responding (it has the tool); the `Lessons` field is informational only (so the chat agent can narrate "I learned X" if it wants), not load-bearing for persistence. Rationale: (a) the executor has fresher context for the lesson; (b) keeps the chat agent's surface narrower (it no longer needs `lesson_capture`); (c) avoids a race where the chat agent forgets to record. (Field naming note: `learnings.Hint` is the actual Go type — `internal/learnings/learnings.go:268`; the recovery package referenced elsewhere doesn't define `Hint`.)

## 4. Trust + permission

**The executor inherits the dispatching session's trust-tier and path-grants.** Trust does not elevate or de-elevate at the handoff.

Mechanism:

- **Path grants** (`internal/permission/path_grants.go::LookupPath`): the existing lineage walk (`lineageMaxHops = 4`) handles this for free. When the executor session is spawned, the dispatch wiring stamps `lineage[executorSessionID] = chatSessionID` (same as the worker-spawn path does today). `LookupPath` walks the lineage chain; the executor's path lookups resolve through the chat session's bucket. No new code path needed; reuse `RegisterLineage`.
- **Trust-tier** (`internal/dispatch/trust.go::TrustResolver`): the executor agent profile registers with `default_trust_tier = 'normal'` in `agent_profiles` (per migration 035 — that's the project-wide default for built-ins; only the chat role stays at default and explicitly remains `normal`). Per-workspace promotion to `trusted` happens via `workspace_role_trust` seeding (the dogfood pattern in migration 036, which seeds every workspace × every internal built-in agent at `trusted`). Plugin-shipped executor profiles register with `default_trust_tier = 'normal'` and require the existing approval flow on first dispatch unless a workspace operator promotes them. `ResolveTrust(workspace, executorAgentProfileID)` returns the resolved tier; the chat agent's tier is irrelevant at dispatch time. Per the H1 contract: an `untrusted` executor can never be dispatched (`ErrUntrustedRole`). A `normal` executor goes through the approval flow; a `trusted` executor dispatches without approval.
- **Implications for high-stakes mutations:** if an executor's flow needs a high-stakes capability (subagent spawn, plugin write, etc.), it requests it through the same H1 channel — emit a `subagent-spawn-approval` (or analogous) envelope as part of its response, let the chat agent route it to the user, then resume on the next dispatch with the approval result in `ContextHandles`. The executor does not silently elevate.
- **Reference:** `decisions.nanite.permission.trust_agent_model` (H1, CW-20260421-0014). The executor handoff is a new dispatch *target*; it does not change the trust model. It uses the existing untrusted/normal/trusted ladder and the existing audit-event log.

**Why "inherit, don't elevate":**

- **Decision rule 6 (handcuffs OFF):** giving the executor a *broader* capability than the chat session would be putting handcuffs on the user — they would face approval prompts via the chat agent's path that the executor silently bypasses. The right direction is the executor inherits and the chat agent's surface narrows, both of which are user-facing wins.
- **Decision rule 3 (redundant enforcement):** if both the chat session AND the executor have their own path-grant buckets, every grant is duplicated. The lineage walk avoids this.
- **Anti-pattern 5 (mechanism-without-trust):** elevating the executor would say "we don't trust the chat agent's grants; the executor needs its own." The chat agent's grants are correct — the user issued them. Inherit.

**Special case — plugin-shipped executor profiles:** plugins MAY register executor profiles (future, post-pilot). These follow the same trust ladder: a plugin-shipped executor with `default_trust_tier: "normal"` requires the existing approval prompt before its first dispatch in a session, and stays at `normal` in `workspace_role_trust` until a workspace operator explicitly promotes it. Subsequent dispatches in the same session reuse the granted approval (per session-scoped trust caching, which is how subagent dispatch already works).

## 5. Failure modes

The executor is bounded on three axes — turn budget, wall-clock budget, and recovery-loop budget — all already present in the harness. **Reuse existing budgets; do not invent new ones.**

| Budget | Mechanism | File:Symbol |
|---|---|---|
| Tool failures (consecutive) — **terminates** | `defaultRunawayFailCap = 10` per executor session | `internal/service/chat_loop_state.go:77` |
| Soft turn budget — **does NOT terminate** | `defaultMaxTurns = 75` (SOFT per CW-20260504-0001) — emits a one-shot `chat-loop-budget-soft-warning` SSE envelope; only `defaultRunawayFailCap`, `defaultHardCeiling`, and `end_turn` actually terminate | `internal/service/chat_loop_state.go:60` |
| Hard turn ceiling — **terminates** | `defaultHardCeiling = 200`, terminates loop | `internal/service/chat_loop_state.go:61` |
| Idle timeout — **terminates** | `defaultIdleTimeoutSeconds = 900` (15m) | `internal/service/chat_loop_state.go:78` |
| Per-tool repeat cap | `defaultMaxRequestToolsCalls = 6` | `internal/service/chat_loop_state.go:84` |

The executor session runs the same chat loop as the chat session does; the same termination codes apply (`runaway_tool_failures`, `hard_ceiling`, `idle_timeout`, `retry_budget_exhausted`). `defaultMaxTurns` does NOT terminate — it just emits a soft warning envelope. When any *terminating* budget fires, the executor's loop ends and the harness composes an `ExecutorResponse` with `Failure.Code = ExecutorFailureBudgetExhausted` (or `ExecutorFailureUnrecoverable` for non-budget terminations) and the partial envelope if one was produced.

**The chat agent receives a failure response, not a hung dispatch.** The dispatch is a single tool call from the chat agent's perspective; if the executor hits a budget, the response comes back with `Failure` set. The chat agent's reaction is to **surface the failure to the user** — not to retry the dispatch on the same intent. (Retrying without changed inputs would just exhaust the budget again.)

**Recovery via broker-dispatched replacement sessions:** when the executor session terminates due to a recoverable failure (e.g., `runaway_tool_failures` triggered by a transient provider issue), the existing recovery broker can dispatch a replacement session (commit `75fae7e`, "fix(service): adopt broker-dispatched replacement sessions"). The broker's `SetReplacementSessionHook` (wired in `internal/service/container.go:701-712`) adopts the replacement into `activeSessions` so the next dispatch from the chat agent reuses it. **No new replacement-session machinery is invented for executors** — they hook into the same broker. The chat agent does not see the replacement; it sees a failed dispatch and the user's next prompt may dispatch again, this time hitting the (replaced) executor.

**Specific failure shapes and how the chat agent narrates:**

- `budget_exhausted` (executor ran out of turns): chat agent says "I tried to render the report card but ran out of turns; here's a partial." If `PartialEnvelope` is set, render it with a banner.
- `unrecoverable` (validator returned a hard error outside the recoverable taxonomy): chat agent narrates the error verbatim; does not retry.
- `invalid_intent` (classifier emitted an intent the executor doesn't recognize): chat agent falls back to chat-direct handling. This is a classifier bug, recorded by B6 telemetry; never user-visible.
- `missing_context` (executor needed a context handle the chat agent didn't provide): chat agent narrates "I need more context — could you share X?" and re-dispatches once the user replies. This is the only case the chat agent ever re-dispatches automatically.
- `trust_denied` (executor profile is `untrusted` for this workspace): chat agent narrates the denial; user must change workspace trust settings.

**Loop containment:** the chat agent does NOT itself loop on dispatches. One user message → at most one executor dispatch per intent (with the `missing_context` exception above, which is bounded by user reply). If the user follows up, that's a new intent classification; the chat agent dispatches again, but it is not in a loop. The orchestrator-workers pattern keeps the loop inside the worker.

## 6. Rollout path

Three phases. Each phase has explicit graduation criteria measured by B6 telemetry.

**Phase 1 — Envelope-rendering pilot (B3, CW-20260429-0032).** Scope: the 11 v1 passive renderable types (`report-card`, `info-card`, `list-card`, `metric-card`, `progress-card`, `table-card`, `timeline-card`, `diff-card`, `document-viewer`, `giphy-modal`, `artifact-mini`). One executor profile registered (`envelope-renderer`). Classifier (B2) routes `render_envelope` intents at it. Chat agent's lens primitives stay registered during phase 1 as a fallback (so a classifier miss doesn't break existing behavior); chat agent's prompt (B5) tells the agent to prefer dispatch but allows fallback to the inline lens flow.

**Graduation to Phase 2:**

- B6 telemetry shows ≥80% of envelope renders going through the executor (versus chat-direct lens calls), measured over 2 weeks of UAT.
- Median executor wall-clock time ≤ p50 of pre-handoff multi-step lens flow.
- Zero regressions on the c117 test (agent does not dodge "show me X" requests).
- System prompt word count for the chat agent drops below 600 (recovered from current ~1076; target progress).

**Phase 2 — Lens primitives removed from chat surface (B4, CW-20260429-0033).** Once Phase 1 graduates, the chat agent's tool surface drops the lens primitives. `tool_describe`, `tool_validate`, `lesson_capture` are no longer registered on the chat profile. Chat-direct fallback is removed; classifier misses surface as `invalid_intent` and the chat agent narrates the gap rather than improvising. Chat prompt (B5) is updated to remove the lens framing entirely; system prompt budget targets ~310 words (the pre-c107 baseline).

**Phase 2 graduation status (2026-05-09).** B4 has landed (`internal/dispatch/chat_surface.go`, `dispatch.ChatToolSurface` + `DefaultChatToolSurface()`; wired in `service.toolServiceImpl.SelectForAgent` for chat-role profiles only — slug `default`). The four lens primitives (`tool_describe`, `tool_validate`, `lesson_capture`, `card_show`) are filtered out of the chat agent's tool catalog; non-chat profiles (Worker, Planner, executor sessions, hint-selector, mux-orchestrator) bypass the filter. Phase 2 graduation criteria met: (a) ≤ 400-word prompt — current 347w / 12 bullets at ceiling per B5; (b) lens cluster removed from chat surface — measured at ~7,467 chars / ~1,867 tokens by B6's `docs/measurements/executor-handoff-pilot.md`. The live ≤ 5/100 `invalid_intent` measurement remains gated on `dispatch_executor` self-tool wiring (B2) plus the live-LLM benchmark procedure (B6); B4 unblocks that measurement but does not itself produce it.

**Phase 2 graduation closure (2026-05-09, CW-20260429-0036).** The `dispatch_executor` self-tool now wires the chat agent to the B3 in-process `internal/executor/envelope_render` executor (`internal/mcp/self_tools_dispatch_executor.go` — `dispatchExecutorToolDefinition` + `(st *SelfToolsTransport).callDispatchExecutor`; injection point `selfTools.Executor = envelope_render.New()` in `cmd/nanite/main.go`). Closes the loop the rest of Phase B opened: **B5 cue → B4 surface narrowing → this self-tool → B3 executor → returned envelope → FE renders**. The chat agent's static surface no longer carries the lens primitives (B4) AND now has a callable destination for the executor-handoff capability bullet B5 added to the prompt — the missing piece between cue and capability is closed. The live ≤ 5/100 `invalid_intent` rate measurement is now achievable end-to-end on the tool side; the remaining gates are operational (API-key provisioning + the FE drawer-render acceptance gate per B6's `docs/measurements/executor-handoff-pilot.md`), not architectural. v1 ships `intent="render_envelope"` only; future LLM-driven executor profiles register additional intents through the same dispatch.Executor seam without changing the chat-facing surface of `dispatch_executor`.

**Graduation to Phase 3:**

- ≤ 5 `invalid_intent` failures per 100 envelope-emit attempts (classifier coverage validated).
- System prompt word count at or below 400.
- No regressions in the 30-day error-recovery rate (executors recover at least as well as the chat-direct lens flow did).
- B6 telemetry shows the lens primitives unused on the chat surface for 7 consecutive days.

**Phase 3 — Executor scope expansion.** Additional executor profiles register for new intent vocabularies. Candidate intents: `knowledge_grounded_answer` (multi-step `memory_recall` + `knowledge_get` + synthesis); `ticket_capture_flow` (the existing chat-side multi-step ticket flow that lives in `commands.go` / orchestrator); `research_synthesis` (current decomposer.go consumers). Each expansion is a separate ticket and follows the same handoff contract. **Phase 3 is open-ended** — the executor pattern becomes the default shape for any new multi-step flow.

**Graduation criteria for new executor profiles (Phase 3+):** any new executor profile must demonstrate (a) ≥ 2-tool flow length on average, (b) reactive lens recovery is needed (not just a single tool call), (c) the chat agent's surface would otherwise grow to accommodate the flow. If a candidate intent does not meet all three, it stays chat-direct.

**Rollback path:** any phase can be rolled back by re-registering the lens primitives on the chat profile and routing the relevant intents back to chat-direct via classifier toggle. The contract (§2 types) is additive — keeping the lens tools on both surfaces is a no-op cost during transition. Rollback fires when telemetry shows regression on the c117 test, error-recovery rate, or user-visible failure mode count.

## Decision rules pass — agent-context-architecture.md

Every rule, run against this design.

### Rule 1 — Where does this constraint actually need to be enforced?

> "Where does this constraint actually need to be enforced? (Handler boundary? Tool description? Permission layer? Or is this an identity-level disposition?) The answer is rarely 'the system prompt.'"

**Run:** The constraint "multi-step structured flows belong in an executor" is enforced at the **classifier layer** (B2 routes the dispatch) and the **tool-surface layer** (the chat agent's profile no longer carries the lens primitives, so it cannot run the flow even if it tried). It is NOT enforced in the system prompt.

**Verdict:** **Respects.** The chat-agent prompt (B5) carries identity-level framing ("you dispatch structured envelopes"); enforcement is at lower layers.

### Rule 2 — Is this preemptive or reactive?

> "Is this preemptive or reactive? Prefer reactive. Preemptive enforcement inside the chat loop is almost always wrong."

**Run:** This is the highest-stakes rule. The handoff is **dispatch on intent**, which is preemptive in shape (chat agent decides to dispatch BEFORE the action runs). Is that the same anti-pattern as the describe-required gate (CW-20260429-0027)?

**No, and the difference is structural.** The describe-required gate forced a specific tool call as a precondition for another tool call inside the *same agent's* loop — it added a turn of friction to every emission and trained the agent to expect failure. The executor handoff, by contrast, is **routing**, not gating. The chat agent dispatches and is done; there is no "pass through this gate, then call the tool" pattern. The lens primitives that remain in the executor's surface are still **reactive** — the executor calls `validate` / `repair` / `remember` only when something fails, exactly the same as the post-c117 design intends.

The dispatch decision itself is classifier-driven, which is the right layer for per-task framing (decision rule 5). The chat agent does not run a preemptive check.

**Verdict:** **Respects, but mitigation noted.** Mitigation: the dispatch tool's description is verb-led ("dispatch this intent"), not gate-led ("you must dispatch before X"). System prompt (B5) frames it as capability ("you can dispatch X to a worker"), not constraint ("you may not do X without dispatching").

### Rule 3 — Is another layer already doing this?

> "Is another layer already doing this? If yes, remove the addition (and possibly remove the existing layer if it's redundant). Two layers enforcing the same concern is anti-pattern (3)."

**Run:** What enforces "envelope is structurally valid before render"? Today: (a) per-type validator at the handler boundary (mechanical, fires on every emit); (b) the lens primitives the chat agent calls via the describe-validate-emit flow; (c) the system-prompt grounding rule. Three layers.

The handoff design **removes layers (b) and (c) from the chat agent's path**. Layer (a) — per-type validator at the handler boundary — stays put on the executor's emit path and is the single mechanical enforcement. The lens primitives still exist on the executor's profile, but as **reactive recovery** (called when (a) emits a typed error), not as enforcement. One concern, one enforcement layer.

**Verdict:** **Respects, and actively reduces.** This design is a net removal of redundant layers, not an addition.

### Rule 4 — Could this go in a tool description instead of the system prompt?

> "Could this go in a tool description instead of the system prompt? If yes, do that. Tool descriptions are evaluated when the agent considers the tool; system prompt rules are evaluated globally."

**Run:** The "you dispatch envelopes via the executor" framing is deliberately **at the tool description layer** for `dispatch_executor`. The description carries: when to use it, what intents it accepts, what it returns. The system prompt update (B5) carries only the identity-level framing ("you are an agent that dispatches structured work"); the detailed when-to-call lives on the tool.

**Verdict:** **Respects.** B5 prompt diff is small; tool description is where the depth lives.

### Rule 5 — Could this be a runtime classifier injection?

> "Could this be a runtime classifier injection? If the constraint is per-task / per-intent, the classifier is the right layer."

**Run:** The dispatch decision IS classifier-driven. B2 (CW-20260429-0031) is exactly the classifier-injection ticket — it inspects the user message, picks the intent, and injects the dispatch hint into the per-turn prefix. This is the canonical right placement.

**Verdict:** **Respects.** The classifier is the dispatch decision-maker.

### Rule 6 — Does this take handcuffs OFF the agent or PUT them ON?

> "Does this take handcuffs OFF the agent or PUT them ON? Default-off. The agent should leave a session more capable than it entered."

**Run:** The chat agent's surface narrows (lens primitives removed) but its **capability** broadens — it can dispatch any registered executor profile, not just the lens flows. The user's experience: the chat agent answers more reliably (executors recover from failures the chat agent used to dodge), without more friction. The executor's surface is task-shaped — it has the lens primitives, the data tools, the emit primitive. Within its scope it has more capability than the chat agent did before, with no new gates to pass.

**Crucial:** the *user* should not feel a new gate. The user prompts; the chat agent dispatches; an envelope comes back. The user does not see the dispatch primitive.

**Verdict:** **Respects.** Net direction is handcuffs OFF — narrower chat surface, broader capability, no new user-facing gates. The c117 incident's "agent picked info-card to dodge a rule" should resolve here: post-handoff, the chat agent dispatches the report-card render to the executor, which has the recovery surface to handle the framing the chat agent was dodging.

### Rule 7 — Apply the c117 test

> "Apply the c117 test: would this rule cause an agent to dodge a 'let's do some testing — show me X' request? If yes, the framing is too strict."

**Run:** The c117 test is the gate. Walk through the scenario: user types "let's do some testing — show me a report card with the latest sprint metrics." Pre-handoff: chat agent reads system-prompt rule "do not render a card from data you don't have", picks `info-card` instead, dodges. Post-handoff: chat agent classifier (B2) emits `Intent: render_envelope, TargetEnvelopeType: report-card, UserRequest: "show me a report card with the latest sprint metrics"`. Chat agent dispatches via `dispatch_executor`. Executor receives the request, runs the multi-step flow (describe report-card schema → resolve fields from context / scratchpad → emit → validate → repair if needed → return). Chat agent renders the result.

The chat agent **never reads the prohibition rule** because the rule no longer exists on its surface (B5 prompt update). The executor reads its own task-shaped framing ("render the requested envelope; if you can't ground a field, return a question-form for it"). The dodging behavior is structurally impossible.

**Verdict:** **Respects.** The c117 scenario is the design's first-order goal.

### Rule 8 — Measure prompt density

> "Measure prompt density. Word count, bullet count, negative phrasings. Ratcheting up over time without ratcheting down is the prompt-accretion anti-pattern."

**Run:** Pre-handoff chat agent system prompt: ~1076 words, 24 bullets, 14 negative phrasings, 3 mechanical gates. Phase 1 target (B5 prompt update): ≤ 600 words, ≤ 18 bullets, ≤ 8 negative phrasings, 0 gates. Phase 2 target: ≤ 400 words, ≤ 12 bullets, ≤ 5 negative phrasings, 0 gates. The handoff is the active mechanism that lets the prompt shrink — every rule that bloated the prompt was about the multi-step lens flow, which is no longer the chat agent's concern.

**Verdict:** **Respects, and is the recovery vehicle.** Density measurements are the graduation criteria for §6 phases. If the prompt does not shrink, the design is failing.

### Summary

| Rule | Verdict | Note |
|---|---|---|
| 1 — Right layer | Respects | Classifier + tool surface, not system prompt. |
| 2 — Preemptive vs reactive | Respects (mitigated) | Dispatch is routing, not gating; tool description is verb-led. |
| 3 — Redundant enforcement | Respects, reduces | Net layer removal. |
| 4 — Tool description vs prompt | Respects | Depth on `dispatch_executor` description. |
| 5 — Classifier injection | Respects | B2 is the dispatch decision-maker. |
| 6 — Handcuffs OFF | Respects | Surface narrows, capability broadens, no user-facing gates. |
| 7 — c117 test | Respects | Dodging behavior structurally impossible post-handoff. |
| 8 — Prompt density | Respects (target) | Density reduction is the graduation criterion. |

8 / 8 respect, with one explicit mitigation (rule 2: framing of the dispatch tool).

## Cross-references

- `docs/measurements/executor-handoff-pilot.md` — B6 measurement report; static + dispatch-overhead metrics against the pilot, plus the live-LLM benchmark procedure (CW-20260429-0035).
- `agent-context-architecture.md` — gating reference; decision rules + anti-patterns this design respects.
- `agentic-error-recovery.md` — co-lens; the recovery layer the executor pattern wraps.
- `docs/architecture/chat-system/02-tool-invocation-and-authority.md` — current chat tool surface (Phase 3 deny-list empty); §"Tool surface today" is the pre-handoff baseline. **Naming drift note:** that doc still uses pre-rename `nanite_*` self-tool names (`nanite_show_card`, `nanite_tool_describe`, etc.); this design assumes the post-rename baseline. Use the audit + convention docs (`docs/tool-naming-audit.md`, `docs/tool-naming-convention.md`) as the canonical source of truth for tool names; the chat-system doc needs a follow-up reflow (tracked separately, see implementer report).
- `docs/architecture/envelope-pipeline.md` — the envelope construction + validation pipeline executors emit into.
- `docs/panels/envelope-render-target.md` — the v1 default-render-target table and the "10 passive renderables" list defining the B3 pilot scope.
- `docs/agent-pattern-catalog.md` — the three-role agent model the executor pattern slots into (Chat / Worker / Planner; the executor is a Worker-role specialization).
- `internal/dispatch/role.go` — `AssignRole`, `RoleAssignment`, `WorkerRoleSlug`. Executor profile is a Worker-role profile with a task-shaped tool surface.
- `internal/dispatch/trust.go` — `TrustResolver`, `TrustTier`, `ErrUntrustedRole`. Executors inherit the chat session's path-grants and resolve their own trust-tier (§4).
- `internal/permission/path_grants.go::LookupPath` — lineage walk reused for executor sessions.
- `internal/service/chat_loop_state.go` — budget mechanisms reused for executor sessions (§5).
- `internal/service/container.go:701-712` — broker-dispatched replacement-session hook reused for executor recovery.
- `decisions.nanite.architecture.chat_surface_v2` — Phase 3 empty deny-list; this design narrows the chat surface further (lens primitives removed in Phase 2 graduation).
- `decisions.nanite.permission.trust_agent_model` — H1 trust ladder (untrusted / normal / trusted); executors use it unchanged.
- `decisions_nanite_architecture_tool_naming_and_vanta_integration` (rev `01KR5B5MGRTM3NWVY7BKC7VVPV`) — just-landed rename arc; this design assumes post-rename baseline (no `nanite_*` aliases).
- Anthropic, *Building Effective Agents* (Schluntz / Zhang, 2024-12) — orchestrator-workers pattern; canonical reference for the §2 contract shape.
- Sibling tickets:
  - CW-20260429-0031 (B2 — classifier routing)
  - CW-20260429-0032 (B3 — envelope-rendering pilot, the implementation of this design)
  - CW-20260429-0033 (B4 — lens placement / Phase 2 graduation)
  - CW-20260429-0034 (B5 — chat prompt update)
  - CW-20260429-0035 (B6 — telemetry)
- Originating evidence: chat sessions c107 (2026-04-28) → c117 (2026-04-29).
