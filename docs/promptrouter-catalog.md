# Prompt Router Catalog

**Source ticket:** CW-20260426-0017 (M3 — agent reflex wiring)
**Date:** 2026-04-26
**Complements:** `docs/agent-pattern-catalog.md` (M2), `internal/dispatch/role.go` (AssignRole)
**Consumed by:** playbook runtime (CW-20260419-0027) — not yet implemented

> **Not to be confused with:** `internal/promptrouter` is the phrase-match dispatch router this doc describes; `internal/agent/reflexes` (under `internal/agent/`) is an unrelated system — the FU-30 predicate/event/interval steering engine for durable agents. It watches session-state signals (token usage, cache behavior, tool calls) and stages actions like `inject_reminder`/`halt_session`. Different system, different code, different lifecycle. (`internal/promptrouter` was previously named `internal/reflex`, renamed specifically to remove any naming collision with `internal/agent/reflexes` — the two no longer share a root word. `internal/agent/reflexes` itself was briefly named `internal/agent/driftguard` per CW-20260816-0062, then renamed back.)

---

## Introduction

A **reflex** is a deterministic mapping from a user-input pattern (and optional ScopeTier hint) to an agent behavior selection. Reflexes sit upstream of `dispatch.AssignRole`: they *produce* ScopeTier and ExecutionPattern hints that AssignRole consumes to select a role assignment.

```
user input
    │
    ▼
[reflex matcher]  ←── scope_tier_hint from M1 classifier
    │
    ▼  matched reflex
[ScopeTier hint + ExecutionPattern hint + profile slug + mode_signal]
    │
    ▼
dispatch.AssignRole(tier, pattern)
    │
    ▼
RoleAssignment{Role, AgentSlug, Mode}
```

**How it composes with ScopeTier (M1):** The M1 classifier emits a ScopeTier based on structural signals (message token count, attachments, tool count). A reflex may *narrow* or *override* the M1 hint — for example, a "review / assess" phrase forces `pattern=subagent` and profile=reviewer regardless of what M1 scored for size. M1 remains the authority on token-budget shape; M3 reflexes are the authority on *behavioral role* selection.

**How it composes with the playbook runtime (CW-20260419-0027):** Reflexes are the *content layer* the playbook runtime will consume. When the runtime lands, it loads the built-in reflex set (via `promptrouter.BuiltinReflexes()` or equivalent YAML), runs the matcher against each user turn, and forwards the winning reflex's hints to the dispatch layer. This catalog defines what those reflexes say; the runtime defines how to evaluate and prioritize them. See the Integration Plan section below.

---

## Reflex Schema

Each reflex entry is a YAML document (or equivalent Go literal) with the following shape:

```yaml
id: <string>
  # Unique slug. Convention: <pattern-slug>-<trigger-keyword>, e.g. planner-mention.
  # Used as a stable key for override lookup and match logging.

triggers:
  user_phrase_any_of: [<string>, ...]
  # List of lower-case phrase substrings. A match fires when the user's
  # message contains ANY entry (case-insensitive substring match).
  # The matcher normalizes whitespace and strips punctuation before comparison.

  scope_tier_hint: <tier>
  # Optional. When set, this reflex only fires when the M1 classifier
  # produced this tier (or broader). One of: trivial, small, medium, large, open.
  # Omit to match any tier.

  execution_pattern_hint: <pattern>
  # Optional guard on M1's execution pattern output.
  # One of: inline, subagent, background. Omit to match any pattern.

resolves_to:
  pattern: <string>
  # Pattern slug from docs/agent-pattern-catalog.md. One of:
  # chat, strategist, planner, researcher, documentor, worker, reviewer.

  role: <role>
  # dispatch.Role string from internal/dispatch/role.go.
  # One of: chat, worker, planner.
  # Strategist / Researcher / Documentor / Reviewer are Worker-role
  # specializations; they set role=worker and profile=<slug>.

  profile: <slug>
  # Optional agent profile slug to spawn. Overrides the AssignRole default
  # (WorkerRoleSlug or PlannerRoleSlug). Omit to use AssignRole's default.
  # Maps to RoleAssignment.AgentSlug in internal/dispatch/role.go.

  workflow_name: <string>
  # Optional (CW-20260814-0002). When set, this reflex routes the matched
  # task to a named, registered workflow run (docs/architecture/
  # agent-workflows-design.md) instead of an ordinary Worker/Planner
  # dispatch. Forwarded to dispatch.ReflexHints.WorkflowName, which is the
  # only way dispatch.ExecuteTask bypasses AssignRole's (tier, pattern)
  # mapping. When set, `pattern`/`role`/`profile` above are ignored —
  # ExecuteTask forces role=workflow regardless of what they say. The
  # named workflow must already be registered (loaded from the workflow
  # definitions directory) or the run fails at launch time — reflexes do
  # not validate workflow existence themselves. Omit for ordinary reflexes.

side_effects:
  mode_signal: <string>
  # Optional. Emitted to the session mode bus. Consumers (UI, workflow layer)
  # observe this signal to open drawers, change display mode, etc.
  # Defined values (v1): planning, research, review, document, execute.
  # Undefined values pass through but produce no harness behavior.

  dispatch_via: <string>
  # Optional. Hint to the dispatch primitive on HOW to run the resolved agent.
  # Values: executeTask (sync subagent), executeBackground (async subagent).
  # Omit to let AssignRole + caller decide.

priority: <int>
  # Tiebreak when multiple reflexes match the same input.
  # Higher number wins. Default: 0. Range: 0–100.
  # If two reflexes share the same priority on the same input, the first
  # registered reflex (builtin order) wins. User overrides should set
  # priority >= 50 to reliably beat built-ins.
```

**Field sources:**
- `role` values: `dispatch.RoleChat`, `dispatch.RoleWorker`, `dispatch.RolePlanner` — see `internal/dispatch/role.go`.
- `scope_tier_hint` values: `classify.ScopeTier` string form — see `internal/classify/scope.go`.
- `execution_pattern_hint` values: `classify.ExecutionPattern` string form — see `internal/classify/scope.go`.
- `pattern` values: pattern slugs defined in `docs/agent-pattern-catalog.md`.

---

## v1 Reflex Set

### reflex: planner-mention

```yaml
id: planner-mention
triggers:
  user_phrase_any_of:
    - "let's plan"
    - "let's work on"
    - "sprint"
    - "plan this"
    - "plan out"
    - "create a plan"
  scope_tier_hint: open
resolves_to:
  pattern: planner
  role: planner
  profile: planner
side_effects:
  mode_signal: planning
  dispatch_via: executeTask
priority: 20
```

**Rationale:** Planning language combined with open scope is the clearest signal that `AssignRole`'s `TierOpen × PatternSubagent → RolePlanner` path should fire. The mode_signal opens the Work + Workflows drawers in J8 v1. Priority 20 because planning intent is high-confidence and should beat generic worker routing.

---

### reflex: researcher-mention

```yaml
id: researcher-mention
triggers:
  user_phrase_any_of:
    - "research"
    - "investigate"
    - "look into"
    - "find out"
    - "dig into"
    - "what does"
    - "find all"
    - "summarize the state"
resolves_to:
  pattern: researcher
  role: worker
  profile: researcher
side_effects:
  mode_signal: research
  dispatch_via: executeTask
priority: 15
```

**Rationale:** Investigation-only intent maps to the Researcher specialization (Worker-role, read-only surface). No tier guard — research can be small ("what does this file say?") or large ("investigate the whole codebase"). The profile hint routes to a Researcher-surface Worker.

---

### reflex: reviewer-mention

```yaml
id: reviewer-mention
triggers:
  user_phrase_any_of:
    - "review"
    - "assess"
    - "second opinion"
    - "critique"
    - "audit"
    - "check this"
    - "give me feedback on"
resolves_to:
  pattern: reviewer
  role: worker
  profile: reviewer
side_effects:
  mode_signal: review
priority: 15
```

**Rationale:** Review intent must NOT dispatch via `executeTask` automatically — the user may want inline commentary. No `dispatch_via` means the caller decides. No write tools; Reviewer is read-only. Priority 15, same as Researcher, because intent is similarly unambiguous.

---

### reflex: strategist-mention

```yaml
id: strategist-mention
triggers:
  user_phrase_any_of:
    - "brainstorm"
    - "explore"
    - "what about"
    - "what if"
    - "think through"
    - "options for"
    - "tradeoffs"
    - "pros and cons"
  scope_tier_hint: medium
resolves_to:
  pattern: strategist
  role: worker
  profile: strategist
side_effects:
  mode_signal: planning
priority: 10
```

**Rationale:** Exploratory / decision-framing language maps to Strategist (analysis without execution). The `scope_tier_hint: medium` guard keeps truly trivial brainstorms (one-liners) handled inline by Chat. No `dispatch_via` — Strategist is conversational. Priority 10 because brainstorm phrases are more ambiguous than research or review phrases.

---

### reflex: documentor-mention

```yaml
id: documentor-mention
triggers:
  user_phrase_any_of:
    - "document"
    - "write up"
    - "summarize"
    - "capture"
    - "write a doc"
    - "add to the kb"
    - "create an adr"
    - "write the changelog"
resolves_to:
  pattern: documentor
  role: worker
  profile: documentor
side_effects:
  mode_signal: document
  dispatch_via: executeTask
priority: 12
```

**Rationale:** Documentation intent produces a structured artifact (file or KB entry). Auto-dispatch via `executeTask` is appropriate — the user expects a written output. Priority 12: above generic worker (0) but below review/research (15) to avoid grabbing "summarize" when the user means quick inline summary.

---

### reflex: worker-execute

```yaml
id: worker-execute
triggers:
  user_phrase_any_of:
    - "build"
    - "implement"
    - "fix"
    - "refactor"
    - "write the code"
    - "add the feature"
    - "make it"
    - "run the migration"
resolves_to:
  pattern: worker
  role: worker
  profile: worker
side_effects:
  mode_signal: execute
  dispatch_via: executeTask
priority: 10
```

**Rationale:** Explicit execution language maps to the base Worker. Priority 10 — lower than specialist reflexes so that "review and fix" correctly fires reviewer-mention first (priority 15), not this one. The Worker profile uses `allow: ["*"]` — full tool surface.

---

### reflex: background-long-task

```yaml
id: background-long-task
triggers:
  user_phrase_any_of:
    - "in the background"
    - "async"
    - "when you get a chance"
    - "overnight"
    - "index the whole"
    - "crawl the entire"
  execution_pattern_hint: background
resolves_to:
  pattern: worker
  role: worker
  profile: worker
side_effects:
  dispatch_via: executeBackground
priority: 25
```

**Rationale:** Background-execution intent is the strongest signal in the reflex set — when a user explicitly asks for async work, that should always override other routing. Priority 25 (highest). The `executeBackground` dispatch_via hint maps to `classify.PatternBackground → RoleWorker, ModeAsync` in AssignRole.

---

### reflex: planner-large-task

```yaml
id: planner-large-task
triggers:
  user_phrase_any_of:
    - "big project"
    - "multi-step"
    - "break this down"
    - "sequence of"
    - "phases"
    - "end to end"
  scope_tier_hint: large
resolves_to:
  pattern: planner
  role: planner
  profile: planner
side_effects:
  mode_signal: planning
  dispatch_via: executeTask
priority: 18
```

**Rationale:** Large multi-step tasks warrant decomposition even when the user doesn't use "plan" explicitly. Guarded by `scope_tier_hint: large` to avoid firing on small "end to end" questions. This is the M3-level extension of the `TierLarge × PatternSubagent → Planner` candidate noted in the M2 catalog (currently routes to Worker; this reflex promotes it to Planner). Priority 18 — below background (25) but above generic planning (20 would conflict; set to 18 to let `planner-mention` win when both fire).

---

## Test Cases

The following inputs should trigger the indicated reflex. These are functional test cases for the matcher when the playbook runtime lands.

| Input | Expected Reflex | Notes |
|---|---|---|
| "Let's plan out the migration strategy for the auth service" | `planner-mention` | planning phrase + open scope |
| "Research how other projects handle rate limiting" | `researcher-mention` | research verb, no execution |
| "Can you review this PR and give me your assessment?" | `reviewer-mention` | review phrase; no dispatch_via |
| "Brainstorm some options for the caching layer — tradeoffs matter" | `strategist-mention` | exploratory; medium scope expected |
| "Document the new playbook API in the KB" | `documentor-mention` | write + structured output |
| "Implement the reflex matcher module" | `worker-execute` | explicit build/implement verb |
| "Index the whole codebase in the background tonight" | `background-long-task` | background phrase; highest priority |
| "Break this down into phases — it's a big project" | `planner-large-task` | multi-step phrase + large scope hint |

---

## Conductor Reflex Set (user-level overrides, CW-20260816-0067)

Deterministic routing for Conductor's directive vocabulary (`docs/architecture/conductor-console-design.md`, "Deterministic input middleware" section). Content only — the router/matcher itself was already live infrastructure before this set was authored.

These four reflexes are **not** built-ins. They live as user-level YAML overrides at `~/.nanite/reflexes/*.yaml` (outside this repo, per the authoring guide's "start as user-level overrides for fast iteration" convention), each with `priority >= 65` — well above the `>= 50` floor documented in `docs/promptrouter-authoring.md`. This section documents them for reference; promote them into `internal/promptrouter/catalog.go` only once the rule set has stabilized against real usage.

**Shared design choice:** all four leave `resolves_to.profile` empty and set `resolves_to.role: chat`. `profile` is the operationally load-bearing field — it becomes `ReflexHints.AgentSlug` in `internal/dispatch/execute.go`'s production path, and it's what `agentkit/broker.DeterministicBroker.Decide`'s Rule 1 checks (`ReflexAgentSlug != ""`) to force a synchronous subagent dispatch. Leaving it empty means a match never forces dispatch — the turn stays with the chat agent (Conductor), which performs the actual tool call (`memory_write`, `torque_task_create`, `torque_task_checkpoint_respond`, etc.) itself per its own system prompt. `role` is set to `chat` for schema correctness (the field `AssignRoleWithReflex`/`reflexFromString` reads) even though the current production dispatch path (`execute.go`) doesn't consult it — see `internal/promptrouter/dispatcher.go` for the (test-only, as of this writing) code path that does.

### reflex: conductor-note-capture

Covers: `"note for "`, `"note to self"`, `"capture a note for"`, `"add a note for"`, `"log a note for"`, `"log this note for"`, `"jot down a note for"`, `"jot this down for"`, `"remember this for"`, `"make a note for"`.

Resolves to `pattern: chat`, `role: chat`, `mode_signal: document`, priority 85. Matches the design doc's "note for Nil" example and `.nanite/agents/conductor.md`'s "Capture" behavior (`memory_write`).

### reflex: conductor-approval

Covers: `"approve the checkpoint"`, `"approve this checkpoint"`, `"checkpoint approved"`, `"give it your approval"`, `"give the go-ahead"`, `"give the go ahead"`, `"you have the go-ahead"`, `"you have the go ahead"`, `"sign off on this"`, `"sign off on that"`, `"approve the sprint"`, `"reject the checkpoint"`, `"deny the checkpoint"`, `"send it back for changes"`, `"hold off on approving"`.

Resolves to `pattern: chat`, `role: chat`, `mode_signal: review`, priority 80. Phrasing is deliberately scoped to checkpoint/sprint/sign-off language rather than bare "approve"/"review"/"ship it" — those are too collision-prone in a global reflex set, and "review"/"audit"/"check this" already belong to the built-in `reviewer-mention` for a different meaning (code review, not checkpoint approval).

### reflex: conductor-research-drop

Covers: `"drop the results in"`, `"drop results in"`, `"drop the findings in"`, `"drop findings in"`, `"put the results in"`, `"put the findings in"`, `"file the results as a task"`, `"file a task with the results"`, `"research it and file a task"`, `"look into it and open a task"`, `"research this and drop it in"`, `"research that and drop it in"`.

Resolves to `pattern: chat`, `role: chat`, `mode_signal: research`, priority 75. This is the "research X, drop results in Y" non-linear ask from the design doc — its higher priority beats the built-in `researcher-mention` (priority 15, which resolves to a synchronous `researcher` worker dispatch) so the turn routes to Torque task creation instead, per `.nanite/agents/conductor.md`'s "How you delegate" bullet.

### reflex: conductor-dispatch-project

Covers: `"dispatch this to"`, `"dispatch that to"`, `"send this to project"`, `"send this over to"`, `"hand this off to"`, `"hand this over to"`, `"kick this over to"`, `"queue this for"`, `"queue this up for"`, `"queue that up for"`, `"queue it up for"`, `"add this to the queue for"`, `"assign this to"`, `"put this on the queue for"`, `"fire this off to"`.

Resolves to `pattern: chat`, `role: chat`, `mode_signal: execute`, priority 65. Covers handing Conductor something meant for a specific project's Torque queue — echoing the design doc's live-session research quote: "I'll add them to my personal queue to fire off when other agents finish working."

### Conductor reflex test cases

Verified locally against the real `~/.nanite/reflexes/*.yaml` files (not synthetic fixtures) via a temporary manual test using `promptrouter.LoadUserReflexes("")` + `promptrouter.MergeReflexes(promptrouter.BuiltinReflexes(), user)` + `promptrouter.Match(...)`, then removed — not part of the committed suite.

| Input | Expected Reflex | Notes |
|---|---|---|
| "note for Nil: check the mcp catalog before shipping" | `conductor-note-capture` | design-doc example phrasing |
| "note for FE: use the new envelope type" | `conductor-note-capture` | project-addressed note |
| "note to self, follow up on the reaper timer tomorrow" | `conductor-note-capture` | self-addressed note |
| "research how other teams handle rate limiting, drop results in the fragments-engine task" | `conductor-research-drop` | beats built-in `researcher-mention` |
| "look into the auth flow and drop the findings in torque" | `conductor-research-drop` | "findings" variant |
| "dispatch this to fragments-engine when you get a chance" | `conductor-dispatch-project` | note: also contains "when you get a chance" (background-long-task trigger), but conductor-dispatch-project's priority 65 wins |
| "queue this up for nil, they can pick it up later" | `conductor-dispatch-project` | |
| "go ahead and approve the checkpoint for CW-20260816-0067" | `conductor-approval` | |
| "sign off on this so the orchestrator can proceed" | `conductor-approval` | |
| "reject the checkpoint, send it back for changes" | `conductor-approval` | matches on the first triggering phrase encountered by substring scan |
| "implement the reflex matcher module" | `worker-execute` | sanity check — unrelated phrasing still hits the pre-existing built-in, unaffected by the new user overrides |

---

## Integration Plan with Playbook Runtime (CW-20260419-0027)

### How the playbook runtime consumes reflexes

When CW-20260419-0027 ships, the playbook runtime will own the matching loop. Reflexes are *inputs* to that runtime — they are the built-in entry points that the runtime evaluates before reaching for the broader playbook library.

The reflex layer is intentionally simpler than the full playbook system: reflexes use phrase-match triggers only (no size signals, no filesystem heuristics). The playbook system handles richer matching (e.g., "Analyzing medium-to-large datasets on filesystem" from the CW-20260419-0027 ticket). Reflexes are the fast first-pass; playbooks are the enriched second-pass.

### Reflex matcher contract

```
match(user_input string, m1_tier ScopeTier, m1_pattern ExecutionPattern) → (Reflex, bool)
```

- Normalize `user_input`: lower-case, collapse whitespace, strip leading/trailing punctuation.
- Iterate registered reflexes in descending priority order.
- For each reflex, check:
  1. `user_phrase_any_of` — any phrase is a substring of normalized input.
  2. `scope_tier_hint` — if set, M1 tier must match or be broader (e.g., hint=medium matches medium/large/open).
  3. `execution_pattern_hint` — if set, M1 pattern must match exactly.
- Return the first matching reflex (highest priority wins). If no reflex matches, return `(_, false)` — fall through to AssignRole defaults.

The matcher is a cheap rule evaluator, not an LLM call. This matches the CW-20260419-0027 design intent: "Match shouldn't be an LLM call at the top level."

### Loading

- **Built-in reflexes:** `internal/promptrouter/builtin/*.yaml` files loaded at startup, or equivalently the `promptrouter.BuiltinReflexes()` Go function in `internal/promptrouter/catalog.go`. The playbook runtime calls one of these; the Go literal is preferred for type safety and zero-dependency loading.
- **User overrides:** `~/.nanite/reflexes/*.yaml`, loaded after built-ins. User overrides with `priority >= 50` reliably beat all built-ins. Same schema; same loader.
- **DB sync:** Optional — the playbook runtime may sync loaded reflexes to a `reflexes` table (similar to `playbooks` table in CW-20260419-0027) for match logging and telemetry.

### Dispatch

A matched reflex emits three artifacts to the calling layer:

1. **ScopeTier hint override** — forwarded to `dispatch.AssignRole(tier, pattern)`.
2. **ExecutionPattern hint override** — forwarded to `dispatch.AssignRole(tier, pattern)`.
3. **ProfileSlug override** — passed as the `AgentSlug` field of `RoleAssignment`, overriding the default WorkerRoleSlug or PlannerRoleSlug.
4. **ModeSignal** — emitted on the session mode bus (consumed by UI/workflow layer).
5. **DispatchVia** — consumed by the dispatch primitive to choose sync vs. async.

The downstream flow remains: `dispatch.AssignRole` → `RoleAssignment` → `nanite_execute_task` (or equivalent). Reflexes are the upstream hint layer; they do not bypass AssignRole.

### Handoff to playbook runtime

When the playbook runtime ships:
1. It calls `promptrouter.BuiltinReflexes()` (or loads `internal/promptrouter/builtin/*.yaml`) at boot.
2. It runs `match(input, tier, pattern)` at the start of each user turn.
3. On a reflex hit: extract `resolves_to` and `side_effects`; emit ScopeTier/Pattern hints and mode signal; call the dispatch layer with the profile slug.
4. On a miss: fall through to the full playbook match (CW-20260419-0027 playbook library), then to AssignRole defaults.
5. Match results are logged to `playbook_match_log` (already defined in CW-20260419-0027) with a `source=reflex` tag.

---

## Out of Scope

The following are explicitly out of scope for this catalog (CW-20260426-0017):

- **Runtime implementation** — the matcher, dispatcher, storage layer, and built-in loader are owned by CW-20260419-0027. This catalog is content only.
- **ScopeTier classifier internals** — M1's logic for emitting TierTrivial through TierOpen is in `internal/classify/`; this catalog only *consumes* the output.
- **Auto-mining reflexes from session history** — learning new reflexes from user behavior is a future capability, not part of M3.
- **Reflex composition** — the question of whether two reflexes can match simultaneously and compose their hints is deferred to the playbook runtime design.
- **LLM-assisted matching fallback** — noted in CW-20260419-0027 as a future capability; reflexes use rule-based matching only.
- **UI/drawer behavior wiring** — the `mode_signal` field is defined here, but the J8 UI code consuming it is out of scope.
