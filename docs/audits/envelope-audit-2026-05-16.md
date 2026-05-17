# Envelope System Audit — 2026-05-16

Read-only audit of the Nanite envelope system, feeding a future "envelope
hardening sprint." Scope: handler/three-way-sync coverage, default display
lane, interactive-card state persistence + hydration, and robustness
(partial-data crash class, transient-vs-persisted, data-shape drift).

## Intro

Envelopes are structured UI cards injected into chat. The system has a
backend side and a frontend side kept in sync by a manifest. Current state
verified against the repo:

- **Manifest (source of truth):** the **external** `go-envelopes` Go module
  — `github.com/hollis-labs/go-envelopes` (see `go.mod`), manifest at
  `manifest/envelopes.yaml` *within that module*. It is NOT vendored in this
  repo (`libs/go-envelopes/` does not exist); the build loads core types from
  the module via `envelopes.LoadCore`. The project CLAUDE.md still references
  `config/envelopes.yaml` and `internal/chat/envelope.go` comments say the
  same; that is **stale** — both should be corrected during the sprint.
  Changing the manifest itself means a change to the `go-envelopes` module
  plus a dependency bump here, not an in-repo edit.
- **Backend:** `internal/chat/envelope.go` (registry + `ValidateEnvelope`),
  `internal/envelope/validator.go` (`PassiveRenderableTypes` allow-list +
  per-type schema validation), `internal/chat/structured.go` (`EnvelopeRef`
  persistence projection).
- **Frontend generated:** `ui/src/generated/plugin-envelopes.ts` (core
  registry, generated from the manifest).
- **Renderer:** `ui/src/components/chat/envelopes/EnvelopeRenderer.tsx`.
- **Inline wiring:** `ui/src/components/chat/ChatMessage.tsx` (envelope rides
  `message.envelope`; renders `RenderTargetStub` when `render_target` set).
- **Standalone "alert lane":** `ChatTranscript.tsx` `pluginEnvelopes.map(...)`.
- **Response path:** `ui/src/lib/envelope-response.ts` (`submitEnvelopeResponse`
  → `POST /api/envelopes/{id}/respond`).
- **Hydration:** `internal/api/envelopes.go` `injectEnvelopePriorResponses`
  injects a `prior_response` key into persisted envelope JSON.

### Three-way-sync method

Core types come from the manifest `core:` list (29 entries). The FE generated
file lists 19 with components; the file's own header documents 7 deliberate
"backend-only" omissions. Plugin types come from `plugins/*/plugin.yaml`
`registers.envelopes`. KB / ticket-* types are plugin-shipped (support-ticket
plugin) and ship FE components in the host repo
(`KBResultCard`, `TicketFormCard`, `TicketConfirmationCard`,
`ResolutionCaptureCard`, `TicketInitFlow`).

## Per-type findings table

Lanes: **inline** = attached to a message via `ChatMessage`; **drawer** =
routed to `bottom_chat_drawer` (RenderTargetStub inline + card in drawer);
**alert** = standalone `pluginEnvelopes` block in `ChatTranscript`;
**none** = no FE component (`renderUnreachableFallback` if it ever reaches FE).

| Type | Handler ok? | Current lane | Correct lane? | State persistence | Other issues |
|---|---|---|---|---|---|
| document-viewer | yes | drawer (schema default) | yes | n/a (passive) | — |
| report-card | yes | drawer (schema default) | yes | n/a (passive) | See Issue 1 — alert-lane leak when emitted without render_target |
| error-report | yes | inline | yes | n/a | — |
| approval-card | yes | inline | yes | **BROKEN** — see Issue 2 | response not hydrated on reload |
| proposal-card | yes | inline | yes | **BROKEN** — see Issue 2 | response not hydrated on reload |
| question-form | partial — no registry entry, renders via `renderLegacyFallback` → `InterviewCard` | inline | yes | **OK** — `InterviewCard` reads `prior_response` | only interactive card that hydrates correctly; fragile (legacy fallback path, not registry) |
| todo-list | yes | inline | yes | n/a | `data.todos` guarded (`todos` const) |
| plan-review | yes | inline | yes (action-bearing, server-state via react-query) | server-side (plan store) | **`data.steps.map` unguarded** — partial-data crash (Issue 4) |
| info-card | yes | inline (cancel_token → recovery footer) | yes | n/a | — |
| list-card | yes | inline | yes | n/a | guarded (PR #188) |
| metric-card | yes | inline | yes | n/a | — |
| progress-card | yes | inline | yes | n/a | `data.steps` guarded with `&&` |
| confirmation-card | yes | inline | yes | **BROKEN** — see Issue 2; also still on legacy `onSendMessage` path, no typed response | not addressable via card_show |
| table-card | yes | inline | yes | n/a | guarded (PR #188) |
| timeline-card | yes | inline | yes | n/a | guarded (PR #188) |
| diff-card | yes | inline | yes | n/a | **`data.before.label` / `data.after.*` unguarded** — partial-data crash (Issue 3) |
| artifact-mini | yes | drawer (schema default) | yes | n/a | `data?.artifact_id` partly guarded; `data.name` etc. unguarded if `data` present-but-empty |
| session-task | backend-only, no FE component | none | n/a (validation only) | n/a | manifest documents intentional omission |
| message-request / -reply / -notification / -handoff | backend-only, no FE component | none | n/a | n/a | manifest documents intentional omission; if ever emitted to FE → `renderUnreachableFallback` |
| subagent-spawn-approval | yes | alert (emitted via `ApprovalEmitterImpl`, standalone `plugin_envelope`) | yes (action-required, runtime-emitted) | **BROKEN** — see Issue 2 | transient: standalone plugin_envelope, persisted as EnvelopeInstance but card never reads `prior_response` |
| chat-loop-terminated | yes | alert (standalone `plugin_envelope`) | yes (terminal status) | n/a | transient — see Issue 5 |
| chat-loop-budget-soft-warning | backend-only, no FE component | none/alert | **mismatch** — manifest says "frontend component intentionally omitted" but `ChatTranscript` LOAD-BEARING comment lists `chat_loop_budget_soft_warning` as a producer feeding the block → reaches FE with no component → `renderUnreachableFallback`. See Issue 6 |
| elicitation-prompt | yes | alert (standalone `plugin_envelope` via `ApprovalEmitterImpl`) | yes (action-required, runtime-emitted) | **BROKEN** — see Issue 2 | transient — see Issue 5 |
| giphy-modal (plugin: giphy) | yes (dynamic registry) | drawer (schema default) | yes | n/a | — |
| oembed-card (plugin: oembed) | yes (dynamic registry) | inline | yes | n/a | — |
| kb-result (plugin: support-ticket) | yes — `KBResultCard` in host repo | inline | yes | n/a | `data.results || data.articles || []` guarded; tolerates both shapes (drift) |
| ticket-form (plugin: support-ticket) | yes — `TicketFormCard` | inline | yes | local-only, no `prior_response` | response not hydrated on reload (Issue 2 class) |
| ticket-confirmation (plugin) | yes — `TicketConfirmationCard` | inline | yes | n/a | — |
| resolution-capture (plugin) | yes — `ResolutionCaptureCard` | inline | yes | local-only, no `prior_response` | response not hydrated on reload (Issue 2 class) |

**Handler-coverage gaps found:** 1 hard mismatch (`chat-loop-budget-soft-warning`
— backend producer wired to a block with no FE component → fallback card);
`question-form` is structurally fragile (renders only via the legacy-fallback
branch, not the registry — a future cleanup of `renderLegacyFallback` would
silently kill it). No casing/name drift detected between manifest, backend
registry, and the generated FE file. `kb-result` tolerates a `results` vs
`articles` shape ambiguity — soft data-shape drift, currently absorbed.

**Lane assessment:** No type renders in a lane that is wrong *for its
nature*, with two caveats — Issue 1 (report-card *can* leak into the alert
lane depending on emission path) and Issue 6 (`chat-loop-budget-soft-warning`
reaches a lane it has no component for).

## Interactive-card state persistence + hydration

The backend persistence/hydration plumbing is **sound**:
`store.EnvelopeInstance` has `RespondedAt` / `ResponseStatus` / `ResponseJSON`;
`POST /api/envelopes/{id}/respond` writes them; `injectEnvelopePriorResponses`
injects a `prior_response` key into the persisted envelope JSON on message
fetch (handles both single-object and array envelope fields).

**The frontend does not consume it.** A repo-wide search for
`prior_response` in `ui/src` returns exactly two files: `ui/src/lib/types.ts`
(the type declaration) and `InterviewCard.tsx`. **`InterviewCard` is the only
card that hydrates** — it reads `envelope.prior_response != null` and seeds
`submitted` state from it.

Every other response-bearing card initializes its decision state from a
hardcoded default and never inspects `prior_response`:

- `ApprovalCard.tsx` — `useState<...>('pending')`, no `prior_response` read.
- `ProposalCard.tsx` — `useState<...>('pending')`.
- `ConfirmationCard.tsx` — `useState<...>('pending')` (also still legacy
  `onSendMessage`, no typed response at all).
- `SubagentSpawnApprovalCard.tsx` — `useState<...>(null)`.
- `ElicitationPromptCard.tsx` — `useState<...>('pending')`.
- `ResolutionCaptureCard.tsx` / `TicketFormCard.tsx` — local-only state.

### Approval-card root cause (the user-reported bug)

Two compounding causes:

1. **The card never reads `prior_response`.** `ApprovalCard` holds decision
   state purely in local `useState('pending')`. On reload the message is
   re-fetched (with `prior_response` injected by the backend), the card
   re-mounts, and `decision` resets to `'pending'` — the approved/rejected
   terminal shell is lost. The response *is* persisted server-side; the FE
   just discards it.

2. **The prop-shape discriminator blocks the fix path.** `approval-card`
   uses `props: "approval"` in the manifest. `EnvelopeRenderer` feeds the
   card `{ approval: envelope.data ?? envelope.approval }` — it receives
   *only* the approval payload, never the envelope wrapper. `prior_response`
   lives at envelope top-level (sibling of `id`/`type`/`data`), so even a
   card that wanted to hydrate cannot reach it through the `approval` prop.
   The same applies to `proposal-card` (`props: "proposal"`). `InterviewCard`
   works because it receives the full `envelope` object.

This is a **class bug**, not a single-card bug: every interactive card except
`question-form`/`InterviewCard` loses its resolved state on reload.

## Robustness

- **Partial-data crash class (cards NOT covered by PR #188):**
  - `DiffCard` — `data.before.label`, `data.before.content`,
    `data.after.label`, `data.after.content` accessed with no guard. A
    partially-loaded envelope (missing `before`/`after`) throws
    "cannot read properties of undefined". PR #188 fixed the four array-card
    cases; nested-object cards were not swept.
  - `PlanReviewCard` — `data.steps.map(...)` (line ~106) unguarded; crashes
    if `steps` is undefined.
  - `ArtifactMiniCard` — `data?.artifact_id` is optional-chained but
    `data.name`, `data.size_bytes`, `data.mime_type` are not; safe only if
    `data` is wholly absent, not if `data` is present-but-empty.
  - Guarded/OK: `ReportCard` (`data.metrics ?? []`), `ListCard`,
    `TableCard`, `TimelineCard`, `ProgressCard` (`data.steps && ...`),
    `TodoListCard`.

- **Transient-vs-persisted:** Standalone `plugin_envelope` cards rendered
  from the `ChatTranscript` `pluginEnvelopes` block live **only in the
  Zustand chat store** (`addPluginEnvelope`, capped at last 50, cleared on
  inactivity). They are **not** part of `messages` and have **no DB
  persistence** as transcript items. Affected: `chat-loop-terminated`,
  `chat-loop-budget-soft-warning`, recovery envelopes — all vanish on
  refresh. `subagent-spawn-approval` and `elicitation-prompt` DO get an
  `EnvelopeInstance` row (via `ApprovalEmitterImpl.Emit`) so the response
  endpoint resolves, but the **card itself** is still only in the transient
  store — on reload the card disappears entirely (and with it any "you
  already approved this" affordance). This is worse than Issue 2: it is not
  just lost state, it is a lost card.

- **Data-shape contract drift:** `kb-result` accepts both `data.results`
  and `data.articles` — a tolerated ambiguity, not a hard bug. No other
  hard drift found; `EnvelopeRef` (CW-20260429-0019) round-trips routing
  fields so persisted cards re-route correctly.

## Prioritized issue list (hardening-sprint backlog)

### Issue 1 — report-card can leak into the standalone alert lane — HIGH
Content envelopes emitted **without** a `render_target` (e.g. an LLM-authored
`report-card` envelope block, or any path that does not stamp the schema
`default_render_target`) are broadcast as `plugin_envelope` SSE events only
when they carry a routing hint — but a `report-card` emitted via `card_show`
*does* get `default_render_target: bottom_chat_drawer` stamped. The ambiguity
is the dedup contract: `ChatTranscript` only skips a `pluginEnvelopes` item
when `render_target` is set AND not blocked. A content card that reaches the
`pluginEnvelopes` store with an empty/blocked `render_target` renders fully in
the standalone alert lane *after all messages*, detached from its turn.
- Files: `internal/service/chat_generate.go` (`broadcastShowCardEnvelopeEvents`,
  ~L1739-1781), `ui/src/components/chat/ChatTranscript.tsx` (~L456-466),
  `ui/src/hooks/useChat.ts` (`PLUGIN_ENVELOPE` handler).
- Fix direction: classify envelope types as content vs alert vs
  action-required; the `pluginEnvelopes` block should only render
  *alert/action* kinds. Content cards (`report-card`, `document-viewer`,
  primitives) must always render inline or in their drawer, never in the
  standalone block, regardless of `render_target` presence.

### Issue 2 — interactive cards do not hydrate their persisted response — HIGH
Every response-bearing card except `question-form`/`InterviewCard`
(`approval-card`, `proposal-card`, `confirmation-card`,
`subagent-spawn-approval`, `elicitation-prompt`, plus plugin `ticket-form`
and `resolution-capture`) discards the server-persisted response on reload.
Backend persistence + `prior_response` injection already work.
- Files: `ui/src/components/chat/envelopes/ApprovalCard.tsx`,
  `ProposalCard.tsx`, `primitives/ConfirmationCard.tsx`,
  `SubagentSpawnApprovalCard.tsx`, `ElicitationPromptCard.tsx`,
  `EnvelopeRenderer.tsx` (prop-discriminator logic, ~L161-174),
  `internal/api/envelopes.go` (`injectEnvelopePriorResponses` — reference).
- Fix direction: (a) pass `prior_response` (or the whole `envelope`) into
  every interactive card — the `props: "approval"` / `props: "proposal"`
  discriminators currently strip the wrapper; either add `prior_response` to
  the discriminated prop or switch interactive cards to `props: "envelope"`;
  (b) each card seeds its decision/answered state from `prior_response` like
  `InterviewCard` does.

### Issue 3 — DiffCard crashes on partial data — MEDIUM
`data.before.*` / `data.after.*` accessed with no guard; nested-object
analogue of the PR #188 array-card class, not covered by that PR.
- Files: `ui/src/components/chat/envelopes/primitives/DiffCard.tsx`.
- Fix direction: normalize `const before = data.before ?? { label: '', content: '' }`
  (and `after`); render an empty/degraded state when absent.

### Issue 4 — PlanReviewCard crashes on partial data — MEDIUM
`data.steps.map(...)` unguarded.
- Files: `ui/src/components/chat/envelopes/PlanReviewCard.tsx` (~L105-106).
- Fix direction: `data.steps ?? []` before `.map`.

### Issue 5 — runtime-emitted cards vanish on reload — MEDIUM
Standalone `plugin_envelope` cards (`chat-loop-terminated`,
`elicitation-prompt`, `subagent-spawn-approval`, recovery envelopes) live only
in the transient Zustand store and are not persisted as transcript items. On
refresh the cards disappear — including action-required cards whose
`EnvelopeInstance` row still exists server-side. The user can lose an
un-actioned elicitation/approval prompt simply by refreshing.
- Files: `internal/service/envelope_emit.go` (`ApprovalEmitterImpl.Emit`),
  `ui/src/stores/useChatStore.ts` (`addPluginEnvelope`, ~L397-412),
  `ui/src/components/chat/ChatTranscript.tsx` (`pluginEnvelopes` block).
- Fix direction: persist runtime-emitted envelopes as transcript-anchored
  rows (or rehydrate the `pluginEnvelopes` store from `EnvelopeInstance` on
  session load) so action-required cards survive a refresh.

### Issue 6 — chat-loop-budget-soft-warning has a producer but no FE component — MEDIUM
The manifest declares the type "frontend component intentionally omitted,"
but the `ChatTranscript` LOAD-BEARING comment lists
`chat_loop_budget_soft_warning` as one of the five `plugin_envelope`
producers feeding the block. If it ever reaches the FE it hits
`renderUnreachableFallback` ("Unsupported envelope").
- Files: `internal/service/chat_loop_budget_soft_warning.go`,
  `ui/src/components/chat/ChatTranscript.tsx` (LOAD-BEARING comment),
  the `go-envelopes` module manifest (`manifest/envelopes.yaml`, external —
  see Intro).
- Fix direction: decide intent — either ship a small FE component (signal
  strip) or confirm the producer never emits to the FE block and correct the
  stale comment / manifest note.

### Issue 7 — stale "config/envelopes.yaml" references — LOW
`internal/chat/envelope.go` comments (`registeredTypes`, `InitCoreTypes`) and
the project `CLAUDE.md` still say core types load from `config/envelopes.yaml`;
they now load from the go-envelopes lib manifest.
- Files: `internal/chat/envelope.go`, `CLAUDE.md`.
- Fix direction: doc-only correction.

### Issue 8 — question-form renders only via the legacy fallback branch — LOW
`question-form` has no registry entry; it reaches `InterviewCard` only
through `EnvelopeRenderer.renderLegacyFallback` (the `envelope.questions`
branch). A future cleanup of the legacy-fallback path would silently break
the only interactive card that currently hydrates correctly.
- Files: `ui/src/components/chat/envelopes/EnvelopeRenderer.tsx`,
  the `go-envelopes` module manifest (`manifest/envelopes.yaml`, external —
  see Intro; a true core-type addition needs a module change + dep bump).
- Fix direction: give `question-form` a first-class registry entry with
  `props: "envelope"` so it no longer depends on the legacy branch.
