# `chatServiceImpl` responsibility map and `generateResponse` phase proposal

This document was the architect-review input required by audit-remediation task
`10/01`. It describes current source as of 2026-08-23. **The operator expressly
approved its six-phase action shape on 2026-08-23 under AD-12**, with
`generateResponse` retaining orchestration and each action owning its step's
logic. Implementation is now authorized only through scoped task `10/04`.

## Verified inventory

- `internal/service/chat_generate.go`: 3,684 lines; `generateResponse` begins at
  line 121 and ends at line 2,036.
- `internal/service/chat.go`: 1,407 lines; `chatServiceImpl` begins at line 219.
- `chatServiceImpl`: 52 fields and 87 production pointer-receiver methods across
  19 files. The audit/task's 84-method citation has drifted.
- Production entry remains one door: `Dispatcher.Run` invokes
  `chatRunnerAdapter`, whose bound function is `svc.generateResponse`.
- `StreamManager`: nine fields today (seven `sync.Map`s, one duration, one
  `atomic.Uint64`) and 25 pointer-receiver methods. Its comment still records
  the original extraction from six Engine maps; the type has grown since then.

Three fields currently have no production read after construction:
`commands`, `dbPath`, and `adapterRegistry`. They are recorded here as current
wiring residue, not silently assigned to a capability. Removing them or fixing
their wiring is outside this characterization/mapping task.

## `generateResponse` phase-boundary proposal

The proposal uses six phases. Phases 3–5 are the body of one iteration of the
existing tool-settling loop; extracting them must not hide that loop or turn
control flow into callbacks.

### Phase 1 — resolve and assemble the turn (lines 121–607)

- **Capability:** establish trace/cleanup, load session and agent, resolve
  model/provider, select/filter tools, assemble Context Broker slots, inject
  reminders/reflex/subagent results, and record pre-stream inspection data.
- **State read:** `sessions`, `agents`, `store`, `providers`, `tools`,
  `modelCatalog`, `pluginHost`, `context`, `reminderEngine`, `reflexEngine`,
  `subagentInbox`, `inspector`, embedding-warning state.
- **State mutated:** embedding-warning dedupe; filtered user content; slot
  window/blocks/system prompt; reminder/reflex/inbox persistence through their
  owners; inspector records; tool-partition state when lazy loading is active.
- **Dependencies:** provider/model resolution, `ToolService`, plugin filters,
  `ContextService`, Context Broker slot APIs, reminder/reflex engines,
  messaging inbox, inspector.
- **Candidate boundary:** a named `prepareTurn` method returning a `turnSetup`
  value containing session, agent, provider/model, selection/tools, filtered
  user content, slot result, system prompt, chat messages, and inspector turn
  ID. Early failures remain typed/explicit returns; the outer function emits
  their current stream errors and exits. Do not move ownership of durable
  dependencies into `turnSetup`.
- **Parameters/returns if extracted:** input `ctx`, IDs, user content, and
  stream; return `turnSetup` plus a terminal/continue result. Trace and final
  stream cleanup stay visibly owned by `generateResponse`.

### Phase 2 — open the stream and initialize the run (lines 608–737)

- **Capability:** emit stream/presence/lifecycle start signals; run the
  pre-loop compaction gate; construct/classify `loopState`; invoke informative
  reflex/route dispatch; apply effort and persisted per-tool limits.
- **State read:** `streams`, `events`, `pluginHost`, `sessionEventWriter`,
  `store`, `inspector`, `reflexEngine`, `envelopeRenderExecutor` and Phase 1's
  setup.
- **State mutated:** active presence; session/plugin events; possibly compacted
  messages/tools/slots; `loopState`; inspector classification; route/reflex
  side-channel envelopes.
- **Dependencies:** `StreamManager`, event sinks, compaction pipeline,
  classifier, effort context, reflex/route dispatch.
- **Candidate boundary:** `initializeRun(setup) -> runState`, where `runState`
  owns `loopState`, current messages/tools/system prompt, accumulators, and
  PTY-observability flags. Keep `stream_start` before loop entry and keep the
  call directly visible at top level.
- **Parameters/returns if extracted:** Phase 1 `turnSetup`, stream, start time;
  return `runState` or a terminal result.

### Phase 3 — govern an iteration and make the provider request (lines 738–1262)

- **Capability:** check hard stops/cancellation; enforce the per-iteration
  token budget; construct provider tracing/telemetry/cache hints; execute the
  `message.sending` pre-hook; call the CLI runtime or HTTP provider; handle
  stream-start failures and compaction/rate-budget recovery.
- **State read:** `loopState`, provider/model, current messages/tools/slots,
  `pluginHost`, `events`, `store`, `sessionEventWriter`, provider registry,
  runtime-session coordinator state.
- **State mutated:** loop continuation/recovery counters; messages/tools/system
  prompt after recovery; partial assistant persistence; provider/rate telemetry;
  content accumulators on plugin cancellation.
- **Dependencies:** token budget, OpenTelemetry, plugin pre-hooks/filters,
  provider/CLI runtime, recovery pipeline, Recovery Broker notification.
- **Candidate boundary:** `startProviderIteration(runState) -> providerAttempt`.
  The result must be an explicit tagged outcome such as `stream`, `retry`,
  `finish`, or `terminal`; it must not `continue` or `break` a hidden outer
  loop. `providerAttempt` carries its cancel function/span and guarantees they
  are ended exactly once.
- **Parameters/returns if extracted:** run/setup state and stream; return
  provider channel plus updated request state, or an explicit control outcome.

### Phase 4 — consume and normalize one provider stream (lines 1263–1603)

- **Capability:** enforce provider-stream inactivity, consume delta/tool/usage/
  error/session/thinking events, perform mid-stream overflow recovery, flush
  phase-tagged deltas, update usage/content, and decide whether the run is done
  or has tools to settle.
- **State read:** provider name, session/agent/message IDs, current recovery
  counters, provider stream/cancel/span, `streams`, `store`, `events`.
- **State mutated:** full/narration/final content, usage, thinking blocks,
  messages/tools/system prompt after recovery, presence, loop continuation,
  partial persistence and error telemetry.
- **Dependencies:** timer/context, `StreamManager`, error classification,
  recovery, persistence and event sinks.
- **Candidate boundary:** `consumeProviderIteration(attempt) -> turnResult`.
  `turnResult` contains stop reason, tool-use blocks, thinking blocks, usage,
  text fragments, and an explicit `retry`/`finish`/`terminal` outcome. Keep
  buffer-flush ordering and the current surprising behavior that a delta
  buffered before a mid-stream error is persisted but not emitted as a normal
  delta; the characterization suite pins it.
- **Parameters/returns if extracted:** `providerAttempt`, run state, stream;
  return `turnResult` and any recovered messages/tools.

### Phase 5 — settle tools and decide continuation (lines 1604–1729)

- **Capability:** append the assistant tool-use message; resolve discovery
  tools; pre-check permissions/plugin policy/execution grants; execute the
  serial/parallel batch; post-process/cache/truncate results; append tool-result
  messages; update loop activity, snapshots, and circuit-breaker state.
- **State read:** tool selection, `tools`, `permissions`, `pathGrants`,
  `argValidator`, `resultCache`, `loopDetector`, `inspector`, `pluginHost`,
  `streams`, `store`, `orchestrator`, loop state.
- **State mutated:** conversation messages; tool refs/counters/failure and
  loop-detection state; pending envelopes; direct-return state; result cache;
  tool/presence/event output.
- **Dependencies:** existing `preCheckTools`, `executeToolBatch`, and
  `postProcessToolResults` helpers already form a useful internal pipeline.
- **Candidate boundary:** first extract only a thin `settleToolTurn` method
  composing those existing helpers. Do not introduce a new type merely to
  reduce method count. Return updated message blocks plus an explicit
  `continue`/`finish` result.
- **Parameters/returns if extracted:** tool-use blocks, current selection/tools,
  IDs/model/stream and loop state; return updated messages/refs and control
  outcome.

### Phase 6 — finalize, persist, and close the run (lines 1731–2036)

- **Capability:** filter/parse/correct envelopes, build the structured response,
  persist the assistant message, record usage/execution metrics, emit
  `stream_end` and response events, and schedule title/tag enrichment.
- **State read:** content/usage/tool refs/pending envelopes, `outputFilter`,
  `pluginHost`, `store`, `events`, `streams`, utility provider/model,
  `appConfig`.
- **State mutated:** durable assistant/usage/metrics/artifact data; plugin and
  stream output; async utility metrics/title/tags.
- **Dependencies:** envelope parser/correction, response wrapper, store,
  filters/events, utility LLM calls.
- **Candidate boundary:** `finalizeRun(setup, runState) error` with persistence
  and stream-event ordering kept in one named method. The outer function still
  owns its deferred channel close, stream cleanup, presence cleanup, trace end,
  and CLI turn terminal event.
- **Parameters/returns if extracted:** immutable setup plus final accumulators;
  return only a terminal error/status because no later phase consumes state.

## Outer-flow constraint

Any approved extraction must leave this state machine readable in
`generateResponse` itself:

`session load → agent resolve → model/provider resolve → tool selection → slot assembly → stream/run initialization → [provider request → stream consumption → optional tool settlement]* → post-processing/persistence`.

The same constraint applies to `chatServiceImpl`: public entry methods must
remain thin, legible owners of their lifecycle semantics even when a narrower
collaborator performs the work. Neither proposal permits a callback graph or a
full rewrite that hides the sequence. A full rewrite is a last resort, not the
default; this mapping found no concrete reason incremental extraction is unsafe.

## `chatServiceImpl` responsibility map

Each entry uses the audit guide's required fields. Shared dependencies are
listed where they are actually used; `fields owned` is reserved for the one
primary owner assigned by the canonical inventory below.

### Canonical exactly-once field ownership

This table is the authoritative field inventory. It supersedes any descriptive
cross-capability field mention elsewhere in the map: each of the 52 struct
fields has exactly one primary capability owner here, while other capabilities'
access appears only as shared state or a dependency.

| Primary capability | Count | Fields owned exactly once |
|---|---:|---|
| 1. Message entry, dispatch, stream, and in-flight run ownership | 7 | `sessions`, `streams`, `processTracker`, `lifecycle`, `activeGenMu`, `activeGen`, `dispatcher` |
| 2. Harness/provider/context/recovery state machine | 9 | `agents`, `context`, `events`, `providers`, `store`, `pluginHost`, `sessionEventWriter`, `modelCatalog`, `reminderEngine` |
| 3. Tool selection, execution, result handling, and per-session tool state | 7 | `tools`, `permissions`, `pathGrants`, `argValidator`, `resultCache`, `toolPartitionStates`, `loopDetector` |
| 4. CLI-based subprocess session lifecycle and recovery | 10 | `agentDeps`, `agentSessionsManager`, `activeSessions`, `freshBootSessions`, `agentEventBridge`, `agentBootDirAdapter`, `activeSessionSlots`, `rebootingSessions`, `displacedSessions`, `activeSessionContextBlocks` |
| 5. Delegation, task tracking, and worker orchestration | 3 | `orchestrator`, `tasks`, `workers` |
| 6. Reflexes, route dispatch, inbox injection, and wake policies | 3 | `reflexEngine`, `envelopeRenderExecutor`, `subagentInbox` |
| 7. Inspector and broker diagnostics | 1 | `inspector` |
| 8. Output enrichment, artifacts, embedding warning, and utility LLM calls | 9 | `embeddingStatus`, `embeddingProvider`, `embeddingWarnedMu`, `embeddingWarnedSessions`, `embeddingWarnedOrder`, `utilityProvider`, `utilityModel`, `appConfig`, `outputFilter` |
| 9. Glass-4 pre-compaction handoff integration | 0 | none |
| 10. Loop-termination card emission | 0 | none |
| 11. Construction-only wiring residue | 3 | `commands`, `dbPath`, `adapterRegistry` |

Arithmetic reconciliation: 7 + 9 + 7 + 10 + 3 + 3 + 1 + 9 + 0 + 0 + 3 =
52 fields. The 52 names in the table are unique; there are no omissions or
duplicate primary owners.

### 1. Message entry, dispatch, stream, and in-flight run ownership

- **capability:** accept chat/background messages, create streams, enforce
  takeover or reject-if-busy semantics, route through the one Dispatcher door,
  and drain on shutdown.
- **fields owned:** `sessions`, `streams`, `processTracker`, `lifecycle`,
  `activeGenMu`, `activeGen`, `dispatcher`.
- **methods owned:** `goTracked`, `trackedDone`, `Dispatcher`,
  `registerGeneration`, `deregisterGeneration`, `registerGenerationIfIdle`,
  `CancelActiveGeneration`, `launchGeneration`, `runGeneration`,
  `HandleMessage`, `RetryLastMessage`, `SendAgentMessage`, `IsGenerating`,
  `TriggerHarnessTurn`, `TriggerMessageWake`, `GetStream`, `Shutdown`,
  `shutdownWithMaxWait`, `hasActiveGeneration`.
- **shared mutable state:** `activeGen` under `activeGenMu`; StreamManager's own
  synchronized registries; lifecycle manager; durable messages/events.
- **dependencies:** Session/Agent/Store services, `StreamManager`, Dispatcher,
  lifecycle, plugin and path-grant hooks, runtime shutdown dependencies.
- **callers:** HTTP message/harness handlers, durable-agent runtime controller,
  messaging and subagent completion reactors, retry/cancel/reboot endpoints,
  `ChatRunner`/delegation through the shared Dispatcher.
- **candidate extraction boundary:** a small `generationCoordinator` could own
  `activeGenMu`, `activeGen`, lifecycle registration, and Dispatcher invocation.
  Keep ChatService entry methods as thin lifecycle/persistence accessors. This
  is cohesive but lower value than the runtime-session boundary and
  `generateResponse` phases.

### 2. Harness/provider/context/recovery state machine

- **capability:** assemble a turn, resolve/provider-call it, run the bounded
  tool-settling loop, recover from budget/provider failures, persist partial or
  final output, and emit completion/error telemetry.
- **fields owned:** `agents`, `context`, `events`, `providers`, `store`,
  `pluginHost`, `sessionEventWriter`, `modelCatalog`, `reminderEngine`.
- **methods owned:** `generateResponse`, `tryProviderCandidate`,
  `resolveProvider`, `classifyNilProvider`, `assembleTurnContext`,
  `contextWindowSize`, `enforceBudgetOrCompact`, `buildSummarizer`,
  `recoverFromContextOverflow`, `persistPartialAssistant`,
  `persistPartialAssistantPreClassified`, `persistPartialAssistantCancelled`,
  `surfaceErrorOrSuppress`, `suppressSurfaceIfSubagentCaused`,
  `persistPartialAssistantAndNotifyBroker`,
  `persistPartialAssistantAndNotifyBrokerPreClassified`,
  `notifyRecoveryBrokerForHTTPStreamError`, `pauseAndMaybeRetryRateBudget`,
  `earlyStopSynthesis`.
- **shared mutable state:** slot windows and current message/tool slices;
  `loopState`; persisted messages, events, compaction records and metrics;
  provider callbacks; stream/presence state.
- **dependencies:** Context Broker, providers/runtime adapter, plugins,
  tool service, compaction, store, Dispatcher caller tag, telemetry.
- **callers:** only `chatRunnerAdapter` in production (subagent and background
  callers converge on Dispatcher before it).
- **candidate extraction boundary:** execute the six incremental phase methods
  above before considering a standalone type. Moving this whole capability to
  another monolith would only relocate the concentration.

### 3. Tool selection, execution, result handling, and per-session tool state

- **capability:** discovery/lazy-load state, permission and grant checks,
  serial/parallel execution, result caching/truncation, loop detection and tool
  event construction.
- **fields owned:** `tools`, `permissions`, `pathGrants`, `argValidator`,
  `resultCache`, `toolPartitionStates`, `loopDetector`.
- **methods owned:** `handleRequestTools`, `detectStuckLoop`, `preCheckTools`,
  `executeToolBatch`, `executeSingleTool`, `postProcessToolResults`,
  `handleResultCacheMetaTool`, `handleFetchToolResult`,
  `handleSearchToolResult`, `loadToolPartitionState`,
  `storeToolPartitionState`, `enforceExecutionRules`,
  `enforceExecutionRulesViaAgentTools`, `enforceExecutionRulesForTool`.
- **shared mutable state:** `loopState`; `toolPartitionStates`; result cache;
  path grants; inspector/loop-detector stores; durable tool events.
- **dependencies:** ToolService, permission engine, MCP caller context,
  truncation/result cache, plugins, StreamManager, inspector and store.
- **callers:** phases 1 and 5 of `generateResponse`; no external caller.
- **candidate extraction boundary:** the existing helpers already form a
  coherent internal pipeline. A later `toolTurnExecutor` is plausible, but it
  should follow—not precede—the phase extraction and must receive a narrow
  dependency bundle. `toolPartitionStates` belongs here, not in the runtime
  session owner merely because runtime teardown currently deletes it. Preserve
  the characterized `tool.executing` cancellation contract: skip
  `ToolService.Execute`, emit blocked `tool_call` then `tool_result`, append the
  blocked result to the continuation provider request, and finish/persist the
  run normally.

### 4. CLI-based subprocess session lifecycle and recovery

- **capability:** boot/reuse/observe/replace/stop a CLI-based subprocess
  session, manage boot-dir context, reboot vs. recover semantics, and plant a
  cold-boot recovery pack.
- **fields owned:** `agentDeps`, `agentSessionsManager`, `activeSessions`,
  `freshBootSessions`, `agentEventBridge`, `agentBootDirAdapter`,
  `activeSessionSlots`, `rebootingSessions`, `displacedSessions`,
  `activeSessionContextBlocks`.
- **methods owned:** `CloseAgentSession`, `driveBootSession`,
  `resolveAgentContextForBoot`, `slotsChangedFor`, `adoptReplacementSession`,
  `stopDisplacedSession`, `observeSessionForRecovery`,
  `regenerateBootDirSlots`, `RebootSessionAgent`, `RecoverSession`,
  `rebootRuntime`, `shouldRecoverColdBoot`, `buildSessionRecoveryPrefix`,
  `composeBootPayload`.
- **shared mutable state:** six runtime maps (`activeSessions`,
  `freshBootSessions`, `activeSessionSlots`, `rebootingSessions`,
  `displacedSessions`, `activeSessionContextBlocks`) plus router/boot-dir
  adapter state and durable runtime/provider-session rows. `activeGen` is read
  to reject reboot while a run is active.
- **dependencies:** `internal/runtime/agent`, agent wrapper session lifecycle,
  recovery broker/pack, dynamic context resolvers, filesystem/boot-dir writes,
  store and StreamManager.
- **callers:** provider phase of `generateResponse`; archive/reboot/recover and
  shutdown paths; runtime Wait observer callbacks.
- **candidate extraction boundary:** strongest type-level candidate. Introduce
  one runtime-session owner with a constructor receiving runtime dependencies,
  bridge, boot-dir adapter, store and stream sink. Move the six runtime maps
  and private lifecycle methods to it; retain thin ChatService accessors for
  `CloseAgentSession`, `RebootSessionAgent`, and `RecoverSession`, plus a narrow
  `StreamTurn` call used by the provider phase.

**Correction to the task's pre-populated nine-field claim:** the named set of
seven maps plus `agentEventBridge`/`agentBootDirAdapter` does exist, but it is
not one clean runtime cluster. `toolPartitionStates` is tool-selection state;
`freshBootSessions`, `rebootingSessions`, and `displacedSessions` are not all
cleared at `CloseAgentSession`; and the actual runtime owner also needs
`agentDeps`, `agentSessionsManager`, and `activeSessionContextBlocks`.
`CloseAgentSession` currently coordinates only `activeSessions`,
`activeSessionSlots`, `toolPartitionStates`, `activeSessionContextBlocks`, the
bridge and boot-dir adapter. The extraction should follow real ownership, not
the old adjacency/count.

**`StreamManager` precedent:** follow its structure, not its stale counts. It
has an explicit `NewStreamManager` constructor, owns synchronized state behind
domain methods, and leaves `chatServiceImpl` calling a narrow surface such as
`CreateStream`, `GetStream`, `ScheduleCleanup`, and presence delivery. A runtime
owner should similarly construct its maps internally, expose lifecycle verbs,
and keep ChatService accessors thin. Unlike StreamManager, it will need injected
runtime/store/bridge dependencies rather than an empty constructor. Session
teardown becomes one owner call; tool-partition cleanup remains a separate tool
state call unless a general per-session cleanup hook is introduced.

### 5. Delegation, task tracking, and worker orchestration

- **capability:** synchronous delegation, decomposition, worker fan-out,
  aggregation and optional task lifecycle tracking.
- **fields owned:** `orchestrator`, `tasks`, `workers`.
- **methods owned:** `SetWorkers`, `DelegateTask`, `DelegateAndAggregate`.
- **shared mutable state:** durable worker sessions/messages/tasks; lifecycle
  goroutines; worker manager.
- **dependencies:** chat Orchestrator, task service, worker spawner, Dispatcher,
  Store/Session services.
- **callers:** ChatService delegation API and self-tool/subagent surfaces.
- **candidate extraction boundary:** domain is cohesive, but `DelegateTask`
  depends on the same Dispatcher/stream semantics as chat. Prefer a dedicated
  delegation service only after the shared run-entry contract is stable; keep
  `chatServiceImpl` as a thin compatibility facade if extracted.

### 6. Reflexes, route dispatch, inbox injection, and wake policies

- **capability:** inject reflex/subagent context, emit dispatch side channels,
  and resolve message/subagent wake policy.
- **fields owned:** `reflexEngine`, `envelopeRenderExecutor`, `subagentInbox`.
- **methods owned:** `attemptReflexDispatch`, `evaluateAndInjectReflexes`,
  `attemptRouteDispatch`, `evaluateAndInjectSubagentResults`,
  `resolveMessageWakePolicy`, `resolveSubagentCompletionPolicy`.
- **shared mutable state:** reflex rows/cooldowns/firings, inbox ack state,
  `loopState` pending envelopes, session stream.
- **dependencies:** Reflex engine, dispatcher executor, messaging store,
  Context Broker slots.
- **callers:** Phase 1/2 of `generateResponse` and messaging/subagent reactors.
- **candidate extraction boundary:** no single owner should combine these just
  because all influence a turn. Each already delegates to a domain engine; the
  `chatServiceImpl` methods are integration seams. Keep them thin and colocated
  with their reactors unless one seam independently grows.

### 7. Inspector and broker diagnostics

- **capability:** capture assembled slots, provider message views and broker
  decisions for the developer inspector.
- **fields owned:** `inspector`.
- **methods owned:** `recordInspectorSlots`, `recordInspectorLLMMessages`,
  `persistBrokerCallEx`.
- **shared mutable state:** inspector's synchronized per-turn records.
- **dependencies:** inspector service, Context Broker slots, broker decision
  types.
- **callers:** `generateResponse`, reflex/route/tool execution.
- **candidate extraction boundary:** already thin adapters around the inspector;
  no new owner warranted.

### 8. Output enrichment, artifacts, embedding warning, and utility LLM calls

- **capability:** one-time embedding warnings, automatic artifacts, envelope
  correction, title/tag generation, debug-mode lookup and utility metrics.
- **fields owned:** `embeddingStatus`, `embeddingProvider`,
  `embeddingWarnedMu`, `embeddingWarnedSessions`, `embeddingWarnedOrder`,
  `utilityProvider`, `utilityModel`, `appConfig`, `outputFilter`.
- **methods owned:** `maybeEmitEmbeddingWarning`, `maybeCreateAutoArtifact`,
  `retryEnvelopeCorrection`, `autoTitle`, `autoTags`, `recordUtilityMetrics`,
  `isGlobalDebugMode`.
- **shared mutable state:** bounded embedding-warning dedupe; durable artifact,
  session title/tag and utility metric rows.
- **dependencies:** store, provider registry, artifact config, filters/plugins.
- **callers:** Phase 1, tool post-processing, and Phase 6.
- **candidate extraction boundary:** auto-title/tag/metrics could form a small
  post-response utility service; embedding warning and artifacts are unrelated
  and should not be bundled with it. Low priority and not justified by counts.

### 9. Glass-4 pre-compaction handoff integration

- **capability:** deterministically ensure a continuity handoff exists before
  compaction.
- **fields owned:** none.
- **methods owned:** `ensureGlass4HandoffPreCompact`.
- **shared mutable state:** durable handoff stash and Context Broker handoff
  slot (through sibling functions).
- **dependencies:** handoff validation/store helpers and compaction recovery.
- **callers:** `recoverFromContextOverflow`.
- **candidate extraction boundary:** already a thin integration seam; leave it
  with handoff helpers or move behind a handoff service only if that service is
  independently introduced.

### 10. Loop-termination card emission

- **capability:** render the typed terminal card/status for bounded-loop exits.
- **fields owned:** none.
- **methods owned:** `emitChatLoopTerminated`.
- **shared mutable state:** none beyond current `loopState` and stream output.
- **dependencies:** Cards/envelope rendering and `loopState`.
- **callers:** provider iteration governance.
- **candidate extraction boundary:** already a narrow helper; no type extraction
  warranted.

### 11. Construction-only wiring residue

- **capability:** none in current production receiver methods.
- **fields owned:** `commands`, `dbPath`, `adapterRegistry`.
- **methods owned:** none.
- **shared mutable state:** referenced objects may be live elsewhere, but these
  stored copies are not read through `chatServiceImpl`.
- **dependencies:** command registry, database path, CLI adapter registry.
- **callers:** constructor assignment only.
- **candidate extraction boundary:** none. Architect should treat these as
  deletion/wiring-audit candidates, not as a new collaborator.

## Complete 87-method reconciliation

The method lists in capabilities 1–10 contain every production
`*chatServiceImpl` receiver exactly once: 19 entry/dispatch + 19 harness/
provider/recovery + 14 tool + 14 CLI-runtime + 3 delegation + 6 reflex/route/
inbox + 3 inspector + 7 enrichment/utility + 1 handoff + 1 loop-termination =
87.

## Requirements for any architect-approved follow-on extraction

1. Extract one phase or one cohesive owner at a time. The characterization
   suite added by task 10/01 must pass unchanged after every increment.
2. After **each individual extraction**, not only at the end, run:
   `go test ./internal/service/...`, `go test -race ./internal/service/...`, and
   the audit complexity tools from `docs/audits/2026-08-21-go-quality/REPORT.md`
   §10/§25 (`golangci-lint` with `cyclop`, `gocyclo`, `gocognit`, `maintidx`,
   `nestif`, and `funlen`, preserving the audit thresholds/configuration).
3. Record the per-step behavioral, race, cognitive/cyclomatic, and
   maintainability results so a locally cleaner helper cannot conceal worse
   aggregate flow.
4. Preserve the recognizable outer state machine described above for both
   `generateResponse` and the public `chatServiceImpl` lifecycle methods.
5. Consider a full rewrite only if incremental extraction later produces a
   concrete, documented safety problem. This mapping found none; incremental
   extraction is the default recommendation.

## AD-12 implementation progress

- Phase 1 (`prepareTurn`) extracted on 2026-08-23. The coordinator retains root
  tracing and deferred cleanup, and receives an immutable `turnSetup` through a
  typed directive outcome. Current coordinator complexity after this phase is
  cognitive 397 / cyclop 185 / gocyclo 183 (baseline 458 / 228 / 225).
