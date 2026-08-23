# `SelfToolsTransport` capability-domain map

This is the evidence artifact for audit-remediation task `10/02` and the
input to architect decision AD-13. It describes the pre-extraction
implementation. **The operator expressly approved its selective-delegation
recommendation and four first candidates on 2026-08-23 under AD-13.**
Implementation is now authorized only through scoped task `10/05`.

Inventory was re-derived from commit `3942f3c4`:

- `internal/selftools/self_tools_transport.go`: 2,481 lines.
- `SelfToolsTransport`: 31 fields and 82 production receiver methods across 19
  files.
- `CallTool`: 68 names in 66 `case` clauses. The three `scratchpad_*` names
  share one clause and are rejected by this transport because the chat loop,
  not `SelfToolsTransport`, owns their per-turn state.
- No receiver method assigns a `SelfToolsTransport` field. In the map, `R`
  means the field/reference is read and `W` means the referenced service,
  store, registry, or counter is invoked through a mutating operation.

The field and method counts are discovery signals only. Every extraction
judgment below is based on domain cohesion, shared state, and dependency
coupling, not on reducing either count.

## Source-file key

| Key | File |
|---|---|
| `T` | `self_tools_transport.go` |
| `AS` | `self_tools_agent_source.go` |
| `CS` | `self_tools_chat_search.go` |
| `D` | `self_tools_describe.go` |
| `DX` | `self_tools_dispatch.go` |
| `DE` | `self_tools_dispatch_executor.go` |
| `H` | `self_tools_handoff.go` |
| `L` | `self_tools_list.go` |
| `P` | `self_tools_panels.go` |
| `PR` | `self_tools_procedure.go` |
| `R` | `self_tools_remember.go` |
| `RP` | `self_tools_reminders_pins.go` |
| `SC` | `self_tools_schedule_create.go` |
| `SG` | `self_tools_skill_get.go` |
| `TR` | `self_tools_task_update_report.go` |
| `V` | `self_tools_validate.go` |
| `W` | `self_tools_whoami.go` |
| `WF` | `self_tools_workflow.go` |
| `WR` | `self_tools_workflow_run.go` |

## Capability map

### Catalog and dispatch framework

| Capability | Tool names | Fields | Receiver methods | Dependencies | Coupling and extractability |
|---|---|---|---|---|---|
| MCP catalog/dispatch adapter | All names below | None | `CallTool`, `ListTools` (`T`) | `internal/mcp`, static `selfToolDefinitions` | This is the transport's legitimate remaining responsibility. Keep the outer catalog and unknown-tool behavior here even if selected handlers later delegate to owners. |

### Agent and authored-capability management

| Capability | Tool names | Fields read / reached | Receiver methods (file) | External dependencies | Shared state and extractability |
|---|---|---|---|---|---|
| Skill catalog, uninstall, and materialization | `skill_list`, `skill_delete`, `skill_get` | `Store` R/W, `SkillVendor` R/W, `Subagent` R/W for fork composition | `callListSkills`, `callDeleteSkill` (`T`); `callSkillGet` (`SG`) | `store`, `skillinstall`, `skillvendor`, `skill`, `subagent`, MCP caller/session identity | Cohesive as “skills,” but not a first extraction: `skill_get` is a security-sensitive authorize → load → compose/fork → execute-marker pipeline, while delete has vendor/index atomicity and list is simple. It also shares `Subagent` with the subagent domain. Preserve this pipeline intact until a dedicated skill-facing owner already used by the REST path is demonstrably viable. |
| Agent profile management and source resolution | `agent_create`, `agent_list`, `agent_update`, `agent_source_resolve` | `Store` R/W, `AgentClassifier` R | `callCreateAgent`, `callListAgents`, `callUpdateAgent`, `classifyAgent` (`T`); `callAgentSourceResolve` (`AS`) | `store`, `internal/agent` management classification, MCP | Strong extraction candidate. The four tools share one entity, one narrow classification policy, and no receiver helper or field with another domain except the generic `Store`. A narrow store interface would avoid carrying the full store. |
| Builder wizard | `builder_start`, `builder_step` | `BuilderRegistry` R, `BuilderSessions` R/W | `callStartBuilder`, `callBuilderStep` (`T`) | `internal/builders` | Clean boundary but low payoff: both handlers already delegate directly to `builders.Handle*`; the only local policy is the fixed session key. Moving two pass-throughs would add indirection without relocating meaningful business logic. |
| Agent-framework installation | `install_home`, `install_project`, `install_diff` | None | `callInstallHome`, `callInstallProject`, `callInstallDiff` (`T`) | `internal/service/install`, filesystem side effects | Cohesive and stateless, but already constructs and delegates to `install.Service`; `install_diff` is an explicit unimplemented response. Keep as adapters unless dispatch modularization needs a registration unit. |

### Work, communication, and continuity

| Capability | Tool names | Fields read / reached | Receiver methods (file) | External dependencies | Shared state and extractability |
|---|---|---|---|---|---|
| Todo and plan work tracking | `todo_create`, `todo_update`, `todo_list`, `plan_create`, `plan_update`, `plan_step_add`, `plan_list`, `plan_get`, `plan_delete` | `TodoStore` R/W, `Work` W; `Store` R through project/session resolution | `callTodoCreate`, `callTodoUpdate`, `callTodoList`, `callPlanCreate`, `callPlanUpdate`, `callPlanStepAdd`, `callPlanList`, `callPlanGet`, `callPlanDelete`, `notifyWorkChanged`, `resolveProjectIDFromSession` (`T`) | `store` todo/plan models, MCP session identity, envelope marker output, work-change broadcaster | Strong extraction candidate. The handlers share one narrow persistence interface and one post-mutation broadcast invariant. The only cross-domain helper is project resolution, also used by reminder/pin handling; inject that lookup or a narrow resolver rather than retaining the entire transport. |
| Internal messaging and session handoff | `message_send`, `message_inbox`, `message_thread`, `message_ack`, `message_resolve`, `message_catch_up`, `handoff_request`, `handoff_approve`, `handoff_reject` | `Messaging` R/W, `Elicitation` R/W | `callMessageSend`, `callMessageInbox`, `callMessageThread`, `callMessageAck`, `callMessageResolve`, `callMessageCatchUp`, `callHandoffRequest`, `callHandoffApprove`, `callHandoffReject` (`T`) | `internal/messaging`, MCP elicitation and caller/session context | Strong extraction candidate. All nine operations use one service and one common timeout; elicitation is a policy local to directive sends, not a separate domain. It has no `Store` or helper coupling to the rest of the transport. |
| Glass-4 context handoff | `handoff_stash`, `handoff_pointers_expand` | `Store` R/W | `callHandoffStash`, `callHandoffPointersExpand` (`H`) | `internal/context` handoff validation/envelopes, `store`, MCP session identity | Cleanly extractable but smaller/less urgent. This is distinct from messaging's session handoff: it persists compaction continuity content rather than changing the session's primary agent. |
| Reminder and pinned-context controls | `reminder_set`, `context_pin`, `context_unpin` | `Store` R/W, `ReminderEngine` W | `callSetReminder`, `callPin`, `callUnpin`, `currentTurnCount` (`RP`); shares `resolveProjectIDFromSession` (`T`) | `store`, `internal/reminders`, MCP session identity | Partially extractable. Reminder creation and context pins share scope/session/project resolution but are different persisted concepts. A common “attention controls” owner would be artificial; either keep together as adapters or separate only when one side gains more behavior. |
| Learning capture | `lesson_capture` | `LearningRecorder` W, `RememberCounters` W | `callRemember` (`R`) | `internal/learnings`, MCP caller/session identity, in-memory atomic counters | Cleanly extractable: capture owns both fields exclusively and has no helper coupling to another domain. `LearningRecaller` is not part of this path; its only production consumer is `callToolDescribe` below. Moving one handler is lower priority than the first candidates, but there is no cross-layer seam blocking it. |
| Per-turn scratchpad gate | `scratchpad_write`, `scratchpad_read`, `scratchpad_clear` | None | Inline branch in `CallTool` (`T`) | Chat-loop `loopState` owns the real implementation | Do not create an owner here. These names are catalogued self-tools but deliberately unavailable through this transport path; this branch only explains that boundary to CLI-launch callers. |

### Orchestration and execution

| Capability | Tool names | Fields read / reached | Receiver methods (file) | External dependencies | Shared state and extractability |
|---|---|---|---|---|---|
| Workflow step callbacks and named workflow launch | `workflow_execute_llm_step`, `workflow_execute_tool_step`, `workflow_verify_step`, `workflow_run` | `WorkflowExecutor` R/W, `WorkflowLauncher` R/W, `WorkflowRegistry` R, `DispatchWrapper` R | `callWorkflowExecuteLLMStep`, `callWorkflowExecuteToolStep`, `callWorkflowVerifyStep` (`WF`); `callWorkflowRun` (`WR`) | `agentworkflow`, `dispatch`, MCP caller identity | Cohesive tool surface but already thin delegation to the actual workflow executor/launcher. `DispatchWrapper` is shared with task dispatch. No owner extraction is justified unless the goal is modular dispatch registration rather than moving business logic. |
| Subagent lifecycle | `subagent_spawn`, `subagent_status`, `subagent_cancel`, `subagent_role_audit` | `Subagent` R/W, `Store` R through recursion and sync-recovery helpers | `callSpawnSubagent`, `syncSubagentEnvelope`, `recoverSyncSummary`, `callSubagentStatus`, `callSubagentCancel`, `callSubagentRoleAudit` (`T`); shares `recursionBlocked` (`DX`) | `internal/subagent`, `store`, MCP identity, persisted message recovery/result envelopes | A real cohesive domain but not a first extraction. Spawn contains approval, sync polling, result recovery, and envelope policy; recursion guarding is shared with `task_execute`, and `Subagent` is also used by `skill_get`. First define those cross-domain seams so moving code does not duplicate trust/recovery policy. |
| Background jobs | `background_job`, `background_status`, `background_cancel` | `Background` R/W | `callBackgroundJob`, `callBackgroundStatus`, `callBackgroundCancel` (`T`) | `internal/background` | Clean but no meaningful transport-owned business logic: all three are narrow adapters to an existing service. A second owner would be redundant. |
| Role/task dispatch and reflex routing | `task_execute` | `Dispatch` R/W, `DispatchWrapper` R, `WorkflowLauncher` R/W, `Broker` R/W, `Store` R/W, `Plugins` W | `callExecuteTask`, `matchDispatchToAgentReflex` (`DX`); shares `recursionBlocked` (`DX`) | agentkit `broker`, `classify`, `dispatch`, `agent/reflexes`, `store`, `subagent` recursion semantics, plugin hooks | Highly entangled and not a first extraction. This one tool integrates identity, recursion, reflex selection, broker telemetry, workflow-vs-agent routing, and envelope wrapping. A future boundary should follow an already-cohesive dispatch service, not recreate these policies in a new selftools-only owner. |
| Executor handoff | `dispatch_executor` | `Executor` R/W | `callDispatchExecutor` (`DE`) | `dispatch`, `envelope`, MCP | Already delegates through the narrow `dispatch.Executor` interface. Keep as a transport adapter. |
| Sandboxed Python execution | `python_run` | `PythonPermChecker` R, `PythonDispatcher` R/W | `callRunPython` (`T`) | Package-level `RunPythonSandbox` in `self_tools_python.go`, `permission`, OS subprocess/pipe/resource-limit APIs, MCP | The heavy sandbox logic is already a package-level function behind two narrow interfaces; moving the one receiver handler would not improve cohesion. Keep the security boundary explicit and independently tested. |
| Agent self-scheduling | `schedule_create` | `Store` R/W | `callScheduleCreate`, `resolveSelfScheduleAgentID` (`SC`) | `store` schedule validation/next-run computation, MCP caller/session identity | Clean and self-contained, but only one tool. It is a reasonable later registration unit, not a high-value first owner extraction. |

### Presentation, discovery, and read surfaces

| Capability | Tool names | Fields read / reached | Receiver methods (file) | External dependencies | Shared state and extractability |
|---|---|---|---|---|---|
| Engine cross-app control | `engine_navigate`, `engine_refresh` | None | `callNavigateEngine`, `callRefreshEngine` (`T`) | `internal/crossapp`, fixed timeouts | Stateless and cohesive but tiny. There is no transport-owned state to isolate, and the handlers already call the cross-app API directly. |
| Card presentation | `card_show` | `PanelLookup` R and `TrustResolver` R indirectly | `callShowCard`, `resolveShowCardRenderTarget` (`T`); calls shared `resolvePanelAccess` (`P`) | `envelope` schema validation, turn source-ID validation, MCP context, panel trust | Extract only with the panel/signal domain. Its render-target gate intentionally reuses panel access policy, so a card-only owner would either depend back on the transport or duplicate a security-relevant check. Combined presentation ownership is a strong candidate. |
| Panel and mode signals | `panel_open`, `panel_close`, `signal_mode` | `PanelLookup` R, `TrustResolver` R, `PanelSignalSink` W | `callPanelOpen`, `callPanelClose`, `callSignalMode`, `resolvePanelAccess`, `emitPanelSignal` (`P`) | `dispatch` trust tiers, MCP caller/session identity, frontend stream sink | Strong only as a combined presentation owner with `card_show`. The five methods form one trust-and-signal policy, and absorbing card render-target resolution would remove rather than preserve the sole cross-domain helper call. |
| Tool discovery, description, and schema validation | `tool_validate`, `tool_describe`, `tool_list` | `SchemaLookup` R, `Inventory` R, `LearningRecaller` R | `callValidate`, `lookupToolSchema` (`V`); `callToolDescribe` (`D`); `callToolList`, `gatherInventory` (`L`); `RecallToolLearnings` (`T`) | MCP definitions/inventory, envelope validation, embedded examples, LLM tool definitions, `internal/learnings` | Moderately cohesive: three read-side dependencies meet at the discovery surface, and `RecallToolLearnings` only enriches `tool_describe` in production. There is no external chat-slot caller to preserve. A discovery facade is therefore viable, though it remains lower leverage than the first candidates because validation, inventory, and description are distinct operations. |
| Chat-history search/read | `chat_search`, `chat_get` | `Store` R | `callChatSearch`, `callChatGet` (`CS`) | `store`, SQL, regexp/UTF-8 handling, MCP caller/session context | Clean read-only boundary and already file-isolated. A possible later owner, but lower leverage than the first candidates. |
| Agent procedure lookup | `procedure_get` | `Store` R | `callProcedureGet` (`PR`) | `store`, MCP caller identity | Too small for a dedicated owner. Keep as a store-backed adapter or group only with a future coherent agent-capabilities read facade. |
| Caller identity | `whoami` | None | `executeWhoami` (`W`) | MCP caller identity, `internal/a2a` agent-card projection | Stateless single-purpose adapter; a dedicated owner would be ceremony. |
| Harness-reactive report declaration | `task_update_report` | `Store` R, `Reactions` R/W | `callTaskUpdateReport` (`TR`) | `selftools/reactions`, `store`, envelope marker result | Deliberately thin worked example: all behavior is configured/executed by the reaction engine. Keep the handler next to the reaction integration; no extra owner is warranted. |

## Shared state and cross-domain seams

These are the coupling points that a future extraction must preserve rather
than hiding behind duplicated helpers:

| Seam | Current consumers | Consequence |
|---|---|---|
| `Store` | Skills, agents, work project resolution, subagent recovery/recursion, task/reflex dispatch, chat history, procedures, reminders/pins, Glass-4 handoff, task-update reactions, scheduling | It is the broadest dependency but not evidence that the domains cohere. Candidate owners should receive narrow interfaces for the operations they actually need. |
| `Subagent` | Subagent lifecycle and fork-composed `skill_get` | A subagent owner cannot simply take exclusive ownership of the service field without preserving skill materialization's legitimate caller. |
| `DispatchWrapper` | `task_execute` and `workflow_run` | Envelope wrapping is shared output policy across two launch paths; it should remain one collaborator. |
| `resolvePanelAccess` plus `PanelLookup`/`TrustResolver` | Panel open/close and `card_show` render targets | This is a security-relevant shared gate and the strongest evidence for one combined presentation boundary rather than separate card and panel owners. |
| `recursionBlocked` | `subagent_spawn` and `task_execute` | Both create child sessions and intentionally share the same depth-one cap. Do not copy the check into two owners. |
| `resolveProjectIDFromSession` | Todo creation/listing and reminder/pin scoping | Extract as an injected/narrow project resolver or leave it at the transport seam; do not make either domain own the other. |
| MCP caller/session context | Skills, work, messaging, subagents, dispatch, panels, reminders, handoff, identity, scheduling | Identity is cross-cutting request context, not transport-owned mutable state. Owners must take `context.Context` unchanged. |
| Static tool definitions and schemas | `ListTools`, `tool_list`, `tool_describe`, `tool_validate` | Definitions remain the source of truth. A modular dispatcher must not create a second catalog or schema registry. |

Domain-local mutable state is much cleaner: `BuilderSessions` is builder-only;
`LearningRecorder`/`RememberCounters` are capture-only;
`LearningRecaller`/`RecallToolLearnings` are discovery-only in production; and
the concrete services behind `Background`, `Messaging`, `TodoStore`,
`ReminderEngine`, and `Reactions` each already own their own state. That
supports selective delegation, not a uniform one-type-per-tool-domain rewrite.

## Inventory reconciliation

The 31 fields reconcile as follows:

- Shared persistence: `Store` (1).
- Skills: `SkillVendor` (1).
- Agents: `AgentClassifier` (1).
- Work: `TodoStore`, `Work` (2).
- Messaging: `Messaging`, `Elicitation` (2).
- Subagents and background: `Subagent`, `Background` (2).
- Task dispatch: `Dispatch`, `DispatchWrapper`, `Broker`, `Plugins` (4).
- Workflow: `WorkflowLauncher`, `WorkflowRegistry`, `WorkflowExecutor` (3).
- Executor handoff: `Executor` (1).
- Python: `PythonPermChecker`, `PythonDispatcher` (2).
- Presentation: `PanelSignalSink`, `PanelLookup`, `TrustResolver` (3).
- Reminders: `ReminderEngine` (1).
- Discovery: `SchemaLookup`, `Inventory`, `LearningRecaller` (3).
- Learning capture: `LearningRecorder`, `RememberCounters` (2).
- Builder: `BuilderRegistry`, `BuilderSessions` (2).
- Harness reactions: `Reactions` (1).

The 82 receiver methods reconcile by domain: framework 2; skills 3; agents 5;
workflow 4; engine control 2; card presentation 2; discovery/validation 6;
builder 2; work tracking 11; installation 3; messaging 9; subagents 6;
background 3; task dispatch 2; executor handoff 1; chat history 2; procedures 1;
Python 1; panels/signals 5; reminders/pins 4; learning capture 1; Glass-4 handoff 2;
identity 1; reactions 1; scheduling 2; and the shared recursion helper 1.

The 68 `CallTool` names reconcile by domain: skills 3; agents 4; workflow 4;
engine 2; cards 1; discovery 3; builder 2; work 9; installation 3; messaging 9;
subagents 4; background 3; task dispatch 1; executor handoff 1; chat history 2;
procedures 1; Python 1; panels/signals 3; reminders/pins 3; learning 1;
Glass-4 handoff 2; identity 1; reactions 1; scheduling 1; scratchpad gate 3.

## Direction recommendation for AD-13

Adopt **selective delegation**, not a uniform “one owner per domain” split.
`SelfToolsTransport` should remain the MCP catalog/dispatch adapter and route
cohesive, policy-bearing domains to narrower collaborators when doing so moves
real behavior and state ownership. Tiny stateless handlers and handlers that
already delegate to an existing service should remain adapters; wrapping them
again would add layers without improving ownership.

This conclusion follows from the coupling map:

- Several domains have a clean, exclusive state cluster and shared invariants
  (work tracking, messaging, agent profiles).
- Several are already correctly delegated (`Background`, workflow callbacks,
  executor handoff, builder, reaction engine); another owner would be hollow.
- Several are load-bearing integration points with deliberate cross-domain
  seams (subagent/skill composition, task/reflex dispatch, card/panel trust).
  They should not be split until those seams have explicit
  shared interfaces.
- Single-tool domains such as identity, procedures, and scheduling do not
  justify a dedicated type merely to lower a metric.

Subject to architect approval, the strongest first candidates are:

1. **Messaging plus session handoff** — nine operations, one service, one
   timeout policy, and one directive-elicitation rule; no store/helper coupling.
2. **Todo/plan work tracking** — one persistence interface and one
   broadcast-after-mutation invariant; isolate the shared project resolver.
3. **Agent profile management/source resolution** — one entity and one
   management-class policy; use a narrow store interface.
4. **Combined card/panel presentation** — move the trust gate, render-target
   resolution, and panel-signal emission together so the current shared access
   check remains singular.

No extraction task should be created or dispatched from this map alone. AD-13
must first accept, reject, or modify this direction and the candidate order.

## Coverage observations

No production code changed, so this task adds no tests. A targeted textual
review of `internal/selftools/*_test.go` found notably thin direct handler
coverage in three nontrivial areas worth considering when AD-13 scopes later
work:

- Messaging has direct elicitation coverage for `message_send`, but no
  selftools-package behavioral tests for inbox/thread/ack/resolve/catch-up or
  the three session-handoff handlers.
- Background-job tests directly cover status polling, but not job creation or
  cancellation through this transport.
- Engine navigation/refresh have no direct selftools-package behavioral tests.

These are observations, not new tasks, and they do not alter the decomposition
recommendation.

## Maintenance rule

When a new first-party self-tool is added, update this map in the same change:
place the tool in exactly one capability row, record any new field/helper seam,
and decide whether its logic belongs in the transport adapter, an existing
capability owner, or a separately architected owner. Keep `CallTool`, the static
definitions, and this inventory in one-to-one agreement.
