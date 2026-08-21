package selftools

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/agentkit/broker"
	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/background"
	"github.com/hollis-labs/nanite/internal/builders"
	"github.com/hollis-labs/nanite/internal/classify"
	"github.com/hollis-labs/nanite/internal/crossapp"
	"github.com/hollis-labs/nanite/internal/dispatch"
	"github.com/hollis-labs/nanite/internal/envelope"
	"github.com/hollis-labs/nanite/internal/grounding"
	"github.com/hollis-labs/nanite/internal/learnings"
	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/messaging"
	"github.com/hollis-labs/nanite/internal/reminders"
	"github.com/hollis-labs/nanite/internal/selftools/reactions"
	"github.com/hollis-labs/nanite/internal/service/install"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
)

// WorkBroadcaster notifies connected UI clients that session-scoped todos or
// plans have changed so the Work drawer can refresh. Defined as a local
// interface to avoid importing the service package (which would be circular).
// CW-20260418-0044.
type WorkBroadcaster interface {
	BroadcastWorkChanged()
}

// PanelSignalSink is the narrow surface the panel-control tools use to push
// panel_signal stream events onto the originating chat session. The signature
// is a (sessionID, type, jsonPayload) triple rather than a chat.StreamEvent —
// the service layer adapts these arguments into a chat.StreamEvent so the mcp
// package stays free of the chat import (avoids the chat → toolclient → mcp
// import cycle). J8 v1 — CW-20260426-0006.
type PanelSignalSink interface {
	BroadcastPanelSignal(sessionID, signalType, jsonPayload string) int
}

// PanelLookup returns the IDs of plugin-shipped panels currently registered
// with the host. panel_open / panel_close use this to validate
// a panel_id outside the V1BuiltinPanelIDs set before applying H1 trust
// gating. Wired from main.go via a closure over plugin.Host.GetPanels — using
// a closure (rather than an interface) avoids importing the plugin package
// from internal/mcp (which would create a cycle through plugin → mcp →
// service → ...). J8 v1 — CW-20260426-0006.
type PanelLookup func() []string

// PanelTrustResolver mirrors dispatch.TrustResolver narrowly so the panel
// handlers can resolve H1 trust without importing dispatch directly into the
// transport. *store.Store already satisfies dispatch.TrustResolver; main.go
// adapts the same value through this interface.
type PanelTrustResolver = dispatch.TrustResolver

// TodoStoreInterface is the subset of store.Store needed by todo/plan MCP tools.
// Defined here to avoid circular imports with the service package. Uses raw
// store methods instead of the service layer's update structs.
type TodoStoreInterface interface {
	CreateTodo(t *store.Todo) error
	GetTodo(id string) (*store.Todo, error)
	ListTodos(f store.TodoFilter) ([]store.Todo, error)
	UpdateTodo(t *store.Todo) error
	UpdateTodoScope(id, scope, scopeID, projectID string) error
	DeleteTodo(id string) error

	CreatePlan(p *store.Plan) error
	GetPlan(id string) (*store.Plan, error)
	ListPlans(f store.PlanFilter) ([]store.Plan, error)
	UpdatePlan(p *store.Plan) error
	UpdatePlanStep(planID, stepID string, updates store.PlanStep) error
	AppendPlanSteps(planID string, steps []store.PlanStep) ([]store.PlanStep, error)
	DeletePlan(id string) error
}

// SelfToolsTransport provides self-service tools that let the agent
// create and manage its own skills, agent profiles, and workflows
// through the same store layer the API uses.
type SelfToolsTransport struct {
	Store           *store.Store
	BuilderRegistry *builders.Registry
	BuilderSessions *builders.SessionManager
	TodoStore       TodoStoreInterface // nil-safe; set after construction
	// Messaging is set post-construction from the container; nil-safe.
	Messaging *messaging.Service
	// Subagent is set post-construction from the container; nil-safe.
	Subagent *subagent.Service
	// Background is the P9 background-job dispatch service. Set post-
	// construction from the container; nil-safe (callers receive an
	// errorResult for the nanite_background_* tools when unset).
	// CW-20260420-0016.
	Background *background.Service
	// Work is set post-construction from the container; nil-safe. When set,
	// mutating todo/plan tools fire BroadcastWorkChanged after a successful
	// write so the Work drawer rehydrates. CW-20260418-0044.
	Work WorkBroadcaster

	// Dispatch is the executeTask dispatch primitive. Set post-
	// construction from the container; nil-safe (callers receive an
	// errorResult). The transport translates the task_execute
	// tool call to dispatch.ExecuteTask.
	// (CW-20260421-0010, B3)
	Dispatch dispatch.Spawner
	// DispatchWrapper turns a worker SpawnResult into a chat.Envelope.
	// Set post-construction; defaults to dispatch.DefaultEnvelopeWrapper{}
	// when the transport detects a configured Dispatch with no wrapper.
	DispatchWrapper dispatch.EnvelopeWrapper
	// WorkflowLauncher runs a named, defined workflow — the RoleWorkflow
	// dispatch outcome (CW-20260813-0014) — and backs both the
	// workflow_run self-tool and a reflex-routed task_execute call. Set
	// post-construction from the container; nil-safe (a RoleWorkflow
	// route without one returns dispatch.ErrNoWorkflowLauncher).
	WorkflowLauncher dispatch.WorkflowLauncher
	// WorkflowRegistry is the same *agentworkflow.Registry instance the
	// container wires into WorkflowLauncher, wired here separately so
	// callWorkflowRun can look up a named workflow's required inputs
	// (agentworkflow.RequiredInputs) and reject an under-specified call
	// with a concrete, actionable error before ever reaching the launcher
	// — rather than only after a step deep in the run fails to resolve a
	// missing {{input.<key>}} reference (CW-20260815-0022). Nil-safe: when
	// unset (e.g. tests that only stub WorkflowLauncher), the pre-check is
	// skipped and behavior is unchanged from before this field existed.
	WorkflowRegistry *agentworkflow.Registry

	// Broker is the agent-broker primitive consulted before dispatch
	// (CW-20260502-0005 scaffold). The no-op `broker.NewModeBroker()` impl
	// preserves current behavior; the deterministic v1 impl per
	// `decisions.nanite.architecture.agent_broker_v1` lands in a follow-up.
	// Nil-safe — when unset, callExecuteTask skips broker consultation
	// and dispatches via the legacy classifier path.
	Broker broker.Broker

	// Executor is the dispatch.Executor wired behind the dispatch_executor
	// self-tool (CW-20260429-0036, B2 closing piece — chat-agent-facing
	// destination for the executor-handoff capability bullet B5 added to the
	// chat prompt). v1 plumbs internal/executor/envelope_render — the B3
	// pilot — and dispatches "render_envelope" intents to it via
	// dispatch.DispatchExecutor (which handles unknown-intent routing).
	// Future LLM-driven executor profiles register as additional
	// dispatch.Executor implementations; the seam stays unchanged.
	// Nil-safe — when unset, dispatch_executor returns a clear errorResult
	// so a wiring miss is visible rather than silently dropped.
	Executor dispatch.Executor

	// The reflex catalog itself is no longer a Go-literal/YAML slice field
	// on this struct (formerly ReflexSet []promptrouter.Reflex, merged
	// builtin + ~/.nanite/reflexes/*.yaml user overrides at boot) —
	// TASKS/phase-4/03-migrate-promptrouter-to-reflexes.md retired
	// internal/promptrouter in full and moved its phrase catalog onto
	// DB-backed dispatch_to_agent agent_reflexes rows (seeds.go). Since
	// the DB is already authoritative here, callExecuteTask reads live
	// dispatch_to_agent rows directly off st.Store (see
	// matchDispatchToAgentReflex in self_tools_dispatch.go) instead of a
	// pre-merged in-memory slice — no separate load-and-merge boot step
	// needed. The ~/.nanite/reflexes/*.yaml user-override convention is
	// retired too; operators use the same agent_reflexes CRUD path
	// (internal/api/reflexes.go) as every other reflex.
	//
	// ReflexLogger (the former dedicated playbook_match_log writer via
	// store.LogReflexMatch) is retired by TASKS/reflex-taxonomy/
	// 06-unified-reflex-telemetry.md — matchDispatchToAgentReflex's
	// firings now go through the same unified reflexes.EmitFirings sink
	// (event_log) every other reflex firing does, via the Plugins field
	// below plus st.Store directly. See that task's Work Log for the "no
	// real reader of playbook_match_log" confirmation behind this
	// removal — the table/migrations are left in place, only the
	// now-fully-dead Go write path (this field, the ReflexMatchLogger
	// interface, store.LogReflexMatch/ReflexMatchLogEntry) is gone.

	// Plugins is the reflex-firing plugin observability hook pair
	// (EmitReflexFired/EmitReflexActionStaged) — TASKS/reflex-taxonomy/
	// 06-unified-reflex-telemetry.md, closing the "plugin hooks blind to
	// two of three paths" gap docs/engineering/architecture/
	// 10-reflex-action-taxonomy.md's Telemetry section names. Wired
	// post-construction from the same *plugin.Host instance
	// internal/service's container wires onto
	// internal/agent/reflexes.Engine.Plugins, so a dispatch_to_agent
	// reflex fired via THIS (task_execute self-tool) path emits the same
	// plugin hooks a reflex fired via the chat-turn-loop path or the
	// generic per-turn pass does. Nil-safe: reflexes.EmitFirings treats
	// a nil PluginHooks as "no plugin emission," matching every other
	// nil-safe field on this struct.
	Plugins reflexes.PluginHooks

	// PythonPermChecker is the permission engine used by python_run
	// to validate tool calls made from inside the Python sandbox.
	// Set post-construction; nil disables permission checks (all tool calls
	// from the sandbox are allowed — only appropriate for tests).
	// CW-20260420-0019 (D6).
	PythonPermChecker PythonPermissionChecker
	// PythonDispatcher routes tool calls from inside the Python sandbox
	// through the same dispatch path as normal tool calls.
	// Set post-construction; nil causes sandbox tool calls to error.
	// CW-20260420-0019 (D6).
	PythonDispatcher PythonToolDispatcher

	// GroundingRecaller is the pre-strategy memory recall step
	// (CW-20260419-0028, Phase 5 / E2). When non-nil and
	// NANITE_GROUNDING_ENABLED=true, callExecuteTask performs a memory
	// recall before dispatch classification and injects a "## Relevant
	// memories" block into the message when hits exceed the similarity
	// threshold. Set post-construction; nil means grounding is skipped
	// entirely (same effect as the gate being off).
	GroundingRecaller *grounding.Recaller
	// GroundingLogger persists consultation and outcome rows.
	// Set post-construction; nil disables grounding logging (the recall
	// step still runs when GroundingRecaller is set and the gate is on).
	// *store.Store satisfies grounding.ConsultationLogger.
	GroundingLogger grounding.ConsultationLogger

	// Elicitation is the G4 mid-call user-prompt service (CW-20260420-0018).
	// When set, write tools that need user confirmation (e.g. message_send
	// kind=directive) issue an elicitation/create request before proceeding.
	// Nil-safe: tools auto-approve when Elicitation is not wired.
	Elicitation mcp.ElicitationService

	// PanelSignalSink fans panel_open/panel_close/mode signals (J8 v1) onto the
	// originating chat session's stream. Nil-safe — when unwired, panel tools
	// still return their {opened: true} confirmation but the FE receives no
	// out-of-band signal. *service.StreamManager satisfies this.
	// CW-20260426-0006.
	PanelSignalSink PanelSignalSink

	// PanelLookup returns the IDs of plugin-shipped panels currently
	// registered with the plugin host. Used by callPanelOpen/callPanelClose
	// to validate panel IDs outside the V1 built-in set before applying H1
	// trust gating. Nil-safe — when unwired, only V1 built-in panel IDs are
	// addressable and plugin-shipped panel IDs return {reason: "unknown_panel"}.
	// CW-20260426-0006.
	PanelLookup PanelLookup

	// TrustResolver is the H1 trust resolver shared with the dispatch
	// subsystem. Used by callPanelOpen/callPanelClose to gate plugin-shipped
	// panel access on TrustTrusted. Nil-safe — when unset, plugin-shipped
	// panel access falls back to "untrusted" (the safe default).
	// CW-20260426-0006.
	TrustResolver PanelTrustResolver

	// ReminderEngine is the deterministic trigger engine for agent-set reminders
	// (J11, CW-20260426-0009). When set, reminder_set calls register the
	// creation turn with the engine so turn_count triggers compute correctly.
	// Nil-safe — without the engine, reminders are persisted but turn_count
	// triggers fall back to turn 0 as the creation baseline.
	ReminderEngine *reminders.Engine

	// SchemaLookup is the cross-server tool-schema registry used by
	// tool_validate (B1, CW-20260429-0006). When set, the validator can
	// resolve input schemas for tools published by ANY registered MCP
	// server, not just the self-tools. *mcp.Manager satisfies this.
	// Nil-safe — when unwired, tool_validate falls back to self-tool
	// schemas only and returns "unknown tool" for everything else.
	SchemaLookup ToolSchemaLookup

	// Inventory is the cross-server tool inventory used by
	// tool_list (CW-20260501-0001). When set, the discovery
	// primitive enumerates every registered MCP tool — self, memory,
	// dev, general, plugin — rather than only the in-process self
	// tools. *mcp.Manager satisfies this via GetAllToolsUnfiltered.
	// Nil-safe — when unwired, tool_list falls back to
	// selfToolDefinitions() (the legacy SP6 behavior).
	Inventory ToolInventoryLookup

	// LearningRecorder is the Vanta-backed write surface for the D1
	// lesson_capture self-tool (CW-20260429-0009). When unset, the
	// tool returns a clear errorResult on every call so a wiring miss
	// is visible rather than silently dropped.
	LearningRecorder *learnings.Recorder

	// LearningRecaller surfaces prior tool-use lessons during slot
	// assembly. Read by the chat layer via SelfToolsTransport's
	// RecallToolLearnings method (D1, CW-20260429-0009). Nil-safe —
	// when unwired, the lesson-recall slot extension is a no-op.
	LearningRecaller *learnings.Recaller

	// RememberCounters tracks per-session counts of lesson_capture
	// calls bucketed by scope (D1 telemetry requirement). Lazily
	// constructed by NewSelfToolsTransport so SnapshotSession works
	// without explicit wiring.
	RememberCounters *rememberSessionCounters

	// WorkflowExecutor is the agentworkflow.StepExecutor implementation
	// (internal/service/workflow_step_executor.go, CW-20260813-0009) that
	// the workflow_execute_llm_step / workflow_execute_tool_step /
	// workflow_verify_step self-tools delegate to — the MCP callback
	// surface an external workflow engine (LangGraph, CrewAI) or the
	// built-in engine uses to run real work through this same harness
	// (CW-20260813-0011). Nil-safe — when unwired, the three tools return
	// a clear errorResult.
	WorkflowExecutor agentworkflow.StepExecutor

	// AgentClassifier resolves an agent profile's ManageClass so
	// agent_create/agent_update can gate writes on ManageClass.Editable() —
	// the same rule internal/api/agent_capabilities.go's requireMutableAgent
	// enforces at the REST layer. Set post-construction from the container's
	// AgentConfigService (cmd/nanite/main.go), which already threads the
	// real writable-managed-roots configuration. Nil-safe: when unwired,
	// classifyAgent falls back to a zero-value agent.Classification (no
	// configured managed roots). That fallback still correctly rejects
	// internal/plugin-sourced profiles — the concrete threat this gate
	// exists to close (CW-... task 34) — it only loses precision
	// distinguishing "managed in a real root" from "external" for
	// file-backed profiles when no roots are configured.
	AgentClassifier AgentClassifier

	// Reactions is the harness-reactive self-tools' reaction engine
	// (internal/selftools/reactions, TASKS/harness-reactive-self-tools/
	// 03-reaction-engine-core.md). task_update_report
	// (self_tools_task_update_report.go, TASKS/harness-reactive-
	// self-tools/07-worked-example-task-update-report.md) is the first
	// and, as of this writing, only caller — any future harness-reactive
	// self-tool calls Fire the same way. Set post-construction from
	// main.go. Nil-safe: when unwired (e.g. a bare SelfToolsTransport in
	// a test harness), a harness-reactive self-tool's handler skips
	// firing reactions entirely rather than panicking, matching every
	// other nil-safe field on this struct.
	Reactions *reactions.Engine
}

// AgentClassifier resolves the management class of an agent profile.
// Declared here (rather than referencing *service.AgentConfigService
// directly) because internal/service already imports internal/mcp —
// importing it back would create an import cycle. *service.AgentConfigService
// satisfies this interface structurally via its existing
// Classify(p *store.AgentProfile) agent.ManageClass method.
type AgentClassifier interface {
	Classify(p *store.AgentProfile) agent.ManageClass
}

// classifyAgent resolves p's ManageClass, preferring the wired
// AgentClassifier (root-aware, matches the REST API's classification
// exactly) and falling back to a zero-value agent.Classification when
// unwired. See the AgentClassifier field comment for what the fallback
// does and doesn't cover.
func (st *SelfToolsTransport) classifyAgent(a *store.AgentProfile) agent.ManageClass {
	if st.AgentClassifier != nil {
		return st.AgentClassifier.Classify(a)
	}
	var fallback agent.Classification
	return fallback.Classify(a.Source, a.SourceRef)
}

// agentNotEditableError formats the same rejection message shape used by
// internal/api/agents.go's writeNotManaged for a REST-layer editability
// rejection (CW-20260818, task 34), so a chat agent hitting this self-tool
// gate and an API client hitting requireMutableAgent see equivalent
// guidance for the same underlying rule.
func agentNotEditableError(slug string, class agent.ManageClass) string {
	msg := "agent is not a writable managed config"
	switch class {
	case agent.ManageClassInternal:
		msg = "agent is an embedded internal harness profile and is managed by Nanite, not editable here"
	case agent.ManageClassPlugin:
		msg = "agent is plugin/vendor-provided (read-only); copy it to the managed layer to edit"
	case agent.ManageClassExternal:
		msg = "agent is not in a writable managed location (read-only); copy it to the managed layer to edit"
	}
	return fmt.Sprintf("%s (slug=%q, manage_class=%s)", msg, slug, string(class))
}

// notifyWorkChanged fires a work_changed presence broadcast if a broadcaster
// is wired. Called after every successful mutating todo/plan tool call.
func (st *SelfToolsTransport) notifyWorkChanged() {
	if st.Work != nil {
		st.Work.BroadcastWorkChanged()
	}
}

// NewSelfToolsTransport creates a SelfToolsTransport backed by the given store.
func NewSelfToolsTransport(s *store.Store) *SelfToolsTransport {
	return &SelfToolsTransport{
		Store:            s,
		BuilderRegistry:  builders.DefaultRegistry(s),
		BuilderSessions:  builders.NewSessionManager(),
		RememberCounters: newRememberSessionCounters(),
	}
}

// RecallToolLearnings is the chat-layer slot extension for D1
// (CW-20260429-0009): it returns up to learnings.MaxRecallHints prior
// lessons captured for toolName. The slot assembler renders the result
// via learnings.SystemPromptBlock and prepends it to the system prompt
// for the turn. Nil-safe: when LearningRecaller is unwired the call
// returns nil so the assembler appends nothing.
func (st *SelfToolsTransport) RecallToolLearnings(ctx context.Context, userID, toolName string) []learnings.Hint {
	if st == nil || st.LearningRecaller == nil {
		return nil
	}
	return st.LearningRecaller.RecallByToolName(ctx, userID, toolName)
}

// ListTools returns all self-service tool definitions.
func (st *SelfToolsTransport) ListTools(_ context.Context) ([]mcp.Tool, error) {
	return selfToolDefinitions(), nil
}

// messageCallTimeout bounds every unary messaging tool call so a wedged store or
// slow subscriber can't hang the MCP handler forever. Subscription
// handlers use the parent ctx directly (lifetime-scoped) instead of this
// timeout — see callMessageSubscribe when it lands.
const messageCallTimeout = 30 * time.Second

// CallTool dispatches to the appropriate handler based on tool name.
// The request ctx is threaded to every handler; messaging handlers further
// wrap it with a 30s timeout so a wedged service call cannot block the
// MCP stream indefinitely (F06).
func (st *SelfToolsTransport) CallTool(ctx context.Context, name string, args map[string]any) (*mcp.ToolResult, error) {
	switch name {
	case "skill_create":
		return st.callCreateSkill(args)
	case "skill_list":
		return st.callListSkills(args)
	case "skill_update":
		return st.callUpdateSkill(args)
	case "skill_delete":
		return st.callDeleteSkill(args)
	case "agent_create":
		return st.callCreateAgent(args)
	case "agent_list":
		return st.callListAgents(args)
	case "agent_update":
		return st.callUpdateAgent(args)
	case agentSourceResolveToolName:
		return st.callAgentSourceResolve(args)
	case "workflow_execute_llm_step":
		return st.callWorkflowExecuteLLMStep(ctx, args)
	case "workflow_execute_tool_step":
		return st.callWorkflowExecuteToolStep(ctx, args)
	case "workflow_verify_step":
		return st.callWorkflowVerifyStep(ctx, args)
	case "workflow_run":
		return st.callWorkflowRun(ctx, args)
	case "engine_navigate":
		return st.callNavigateEngine(args)
	case "engine_refresh":
		return st.callRefreshEngine(args)
	case "card_show":
		return st.callShowCard(ctx, args)
	case "tool_validate":
		return st.callValidate(ctx, args)
	case "builder_start":
		return st.callStartBuilder(args)
	case "builder_step":
		return st.callBuilderStep(args)
	case "todo_create":
		return st.callTodoCreate(ctx, args)
	case "todo_update":
		return st.callTodoUpdate(args)
	case "todo_list":
		return st.callTodoList(ctx, args)
	case "plan_create":
		return st.callPlanCreate(ctx, args)
	case "plan_update":
		return st.callPlanUpdate(args)
	case "plan_step_add":
		return st.callPlanStepAdd(args)
	case "plan_list":
		return st.callPlanList(ctx, args)
	case "plan_get":
		return st.callPlanGet(args)
	case "plan_delete":
		return st.callPlanDelete(args)
	case "install_home":
		return st.callInstallHome(args)
	case "install_project":
		return st.callInstallProject(args)
	case "install_diff":
		return st.callInstallDiff(args)
	case "message_send":
		return st.callMessageSend(ctx, args)
	case "message_inbox":
		return st.callMessageInbox(ctx, args)
	case "message_thread":
		return st.callMessageThread(ctx, args)
	case "message_ack":
		return st.callMessageAck(ctx, args)
	case "message_resolve":
		return st.callMessageResolve(ctx, args)
	case "message_catch_up":
		return st.callMessageCatchUp(ctx, args)
	case "handoff_request":
		return st.callHandoffRequest(ctx, args)
	case "handoff_approve":
		return st.callHandoffApprove(ctx, args)
	case "handoff_reject":
		return st.callHandoffReject(ctx, args)
	case "subagent_spawn":
		return st.callSpawnSubagent(ctx, args)
	case "subagent_status":
		return st.callSubagentStatus(ctx, args)
	case "subagent_cancel":
		return st.callSubagentCancel(ctx, args)
	case "subagent_role_audit":
		return st.callSubagentRoleAudit(ctx, args)
	case "background_job":
		return st.callBackgroundJob(ctx, args)
	case "background_status":
		return st.callBackgroundStatus(ctx, args)
	case "background_cancel":
		return st.callBackgroundCancel(ctx, args)
	case "task_execute":
		return st.callExecuteTask(ctx, args)
	// --- Executor handoff (CW-20260429-0036, B2 closing piece) ---
	case "dispatch_executor":
		return st.callDispatchExecutor(ctx, args)
	case "chat_search":
		return st.callChatSearch(ctx, args)
	case "chat_get":
		return st.callChatGet(ctx, args)
	case "procedure_get":
		return st.callProcedureGet(ctx, args)
	case "python_run":
		return st.callRunPython(ctx, args)
	case "panel_open":
		return st.callPanelOpen(ctx, args)
	case "panel_close":
		return st.callPanelClose(ctx, args)
	case "signal_mode":
		return st.callSignalMode(ctx, args)
	// --- Reminders + Pin (J11, CW-20260426-0009) ---
	case "reminder_set":
		return st.callSetReminder(ctx, args)
	case "context_pin":
		return st.callPin(ctx, args)
	case "context_unpin":
		return st.callUnpin(ctx, args)
	// --- Discovery / introspection (CW-20260429-0005, A1) ---
	case "tool_describe":
		return st.callToolDescribe(ctx, args)
	// --- Cheap discovery primitive (SP6, CW-20260430-0006) ---
	case "tool_list":
		return st.callToolList(ctx, args)
	// --- Learning capture (CW-20260429-0009, D1) ---
	case "lesson_capture":
		return st.callRemember(ctx, args)
	// --- Self-handoff (Glass-4, CW-20260502-0015) ---
	case "handoff_stash":
		return st.callHandoffStash(ctx, args)
	case "handoff_pointers_expand":
		return st.callHandoffPointersExpand(ctx, args)
	// --- Self-discovery (A2A, CW-20260519-0069) ---
	case "whoami":
		return st.executeWhoami(ctx, args)
	// --- Harness-reactive self-tools worked example (TASKS/
	// harness-reactive-self-tools/07) ---
	case taskUpdateReportToolName:
		return st.callTaskUpdateReport(ctx, args)
	// --- Agent self-scheduling producer (TASKS/scheduling/08-agent-self-
	// tool.md) ---
	case scheduleCreateToolName:
		return st.callScheduleCreate(ctx, args)
	case "scratchpad_write", "scratchpad_read", "scratchpad_clear":
		// scratchpad_* are per-turn tools backed by the in-process chat
		// loop's loopState. They are dispatched by the chat-loop executor,
		// not this transport — so reaching them here means a caller without
		// a turn loop (the CLI-launch self-tools proxy, which has no
		// nanite-side loopState). Return an actionable error instead of a
		// bare "unknown tool" so the agent understands the tool is real but
		// out of scope for this dispatch path.
		return mcp.ErrorResult(fmt.Sprintf("%s is a per-turn tool backed by in-process chat-loop state; it is not available to CLI-launch sessions, which have no nanite-side turn loop. Use Vanta memory tools for state that must persist beyond a single call.", name)), nil
	default:
		return mcp.ErrorResult(fmt.Sprintf("unknown tool: %s", name)), nil
	}
}

// --- skill handlers ---

func (st *SelfToolsTransport) callCreateSkill(args map[string]any) (*mcp.ToolResult, error) {
	name, _ := args["name"].(string)
	slug, _ := args["slug"].(string)
	desc, _ := args["description"].(string)
	if name == "" || slug == "" || desc == "" {
		return mcp.ErrorResult("name, slug, and description are required"), nil
	}

	sk := &store.Skill{
		Name:         name,
		Slug:         slug,
		Description:  desc,
		Category:     strArg(args, "category", "custom"),
		ToolBindings: strArg(args, "tool_bindings", "[]"),
		InputSchema:  strArg(args, "input_schema", "{}"),
	}

	if err := st.Store.CreateSkill(sk); err != nil {
		return mcp.ErrorResult(fmt.Sprintf("create skill: %v", err)), nil
	}

	out, _ := json.Marshal(sk)
	return mcp.TextResult(fmt.Sprintf("Created skill %q (id=%s)\n%s", sk.Name, sk.ID, string(out))), nil
}

func (st *SelfToolsTransport) callListSkills(args map[string]any) (*mcp.ToolResult, error) {
	skills, err := st.Store.ListSkills()
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("list skills: %v", err)), nil
	}

	categoryFilter, _ := args["category"].(string)
	if categoryFilter != "" {
		filtered := make([]store.Skill, 0)
		for _, sk := range skills {
			if strings.EqualFold(sk.Category, categoryFilter) {
				filtered = append(filtered, sk)
			}
		}
		skills = filtered
	}

	if len(skills) == 0 {
		return mcp.TextResult("No skills found."), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d skill(s):\n\n", len(skills))
	for _, sk := range skills {
		fmt.Fprintf(&sb, "- %s (id=%s, slug=%s, category=%s)\n  %s\n",
			sk.Name, sk.ID, sk.Slug, sk.Category, sk.Description)
	}
	return mcp.TextResult(sb.String()), nil
}

func (st *SelfToolsTransport) callUpdateSkill(args map[string]any) (*mcp.ToolResult, error) {
	id := strArg(args, "id", "")
	if id == "" {
		return mcp.ErrorResult("id is required"), nil
	}

	sk, err := st.Store.GetSkill(id)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("get skill: %v", err)), nil
	}
	if sk == nil {
		return mcp.ErrorResult(fmt.Sprintf("skill %q not found", id)), nil
	}

	if v, ok := args["name"].(string); ok && v != "" {
		sk.Name = v
	}
	if v, ok := args["slug"].(string); ok && v != "" {
		sk.Slug = v
	}
	if v, ok := args["description"].(string); ok && v != "" {
		sk.Description = v
	}
	if v, ok := args["category"].(string); ok && v != "" {
		sk.Category = v
	}
	if v, ok := args["tool_bindings"].(string); ok && v != "" {
		sk.ToolBindings = v
	}
	if v, ok := args["input_schema"].(string); ok && v != "" {
		sk.InputSchema = v
	}

	if err := st.Store.UpdateSkill(sk); err != nil {
		return mcp.ErrorResult(fmt.Sprintf("update skill: %v", err)), nil
	}
	return mcp.TextResult(fmt.Sprintf("Updated skill %q (id=%s)", sk.Name, sk.ID)), nil
}

func (st *SelfToolsTransport) callDeleteSkill(args map[string]any) (*mcp.ToolResult, error) {
	id := strArg(args, "id", "")
	if id == "" {
		return mcp.ErrorResult("id is required"), nil
	}

	if err := st.Store.DeleteSkill(id); err != nil {
		return mcp.ErrorResult(fmt.Sprintf("delete skill: %v", err)), nil
	}
	return mcp.TextResult(fmt.Sprintf("Deleted skill %s", id)), nil
}

// --- agent handlers ---

func (st *SelfToolsTransport) callCreateAgent(args map[string]any) (*mcp.ToolResult, error) {
	name, _ := args["name"].(string)
	slug, _ := args["slug"].(string)
	prompt, _ := args["system_prompt"].(string)
	if name == "" || slug == "" || prompt == "" {
		return mcp.ErrorResult("name, slug, and system_prompt are required"), nil
	}

	// Task 34: creating an agent whose slug collides with an existing
	// non-editable (internal/plugin/external) profile must be rejected the
	// same way an update against one is — otherwise agent_create is a
	// second, unguarded path to the same overwrite-a-seed-profile gap
	// (the actual write below would currently hard-fail on the DB's
	// slug UNIQUE constraint with a raw SQL error; this check runs first
	// so the caller gets the same clear, classified rejection as
	// callUpdateAgent instead).
	if existing, err := st.Store.GetAgentBySlug(slug); err == nil && existing != nil {
		if class := st.classifyAgent(existing); !class.Editable() {
			return mcp.ErrorResult(agentNotEditableError(existing.Slug, class)), nil
		}
	}

	a := &store.AgentProfile{
		Name:         name,
		Slug:         slug,
		SystemPrompt: prompt,
		Description:  strArg(args, "description", ""),
		DefaultModel: strArg(args, "default_model", ""),
	}

	if err := st.Store.CreateAgent(a); err != nil {
		return mcp.ErrorResult(fmt.Sprintf("create agent: %v", err)), nil
	}

	out, _ := json.Marshal(a)
	return mcp.TextResult(fmt.Sprintf("Created agent %q (id=%s)\n%s", a.Name, a.ID, string(out))), nil
}

func (st *SelfToolsTransport) callListAgents(args map[string]any) (*mcp.ToolResult, error) {
	agents, err := st.Store.ListAgents()
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("list agents: %v", err)), nil
	}

	if len(agents) == 0 {
		return mcp.TextResult("No agent profiles found."), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d agent(s):\n\n", len(agents))
	for _, a := range agents {
		model := a.DefaultModel
		if model == "" {
			model = "(default)"
		}
		fmt.Fprintf(&sb, "- %s (id=%s, slug=%s, model=%s)\n  %s\n",
			a.Name, a.ID, a.Slug, model, a.Description)
	}
	return mcp.TextResult(sb.String()), nil
}

func (st *SelfToolsTransport) callUpdateAgent(args map[string]any) (*mcp.ToolResult, error) {
	id := strArg(args, "id", "")
	if id == "" {
		return mcp.ErrorResult("id is required"), nil
	}

	a, err := st.Store.GetAgent(id)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("get agent: %v", err)), nil
	}

	// Task 34: reject writes against non-editable (internal/plugin/external)
	// agent profiles before applying any field updates — matches
	// internal/api/agent_capabilities.go's requireMutableAgent gate at the
	// REST layer. Classify the target's *current* class (source/source_ref
	// as loaded, before any of the args below could mutate it).
	if class := st.classifyAgent(a); !class.Editable() {
		return mcp.ErrorResult(agentNotEditableError(a.Slug, class)), nil
	}

	if v, ok := args["name"].(string); ok && v != "" {
		a.Name = v
	}
	if v, ok := args["slug"].(string); ok && v != "" {
		a.Slug = v
	}
	if v, ok := args["system_prompt"].(string); ok && v != "" {
		a.SystemPrompt = v
	}
	if v, ok := args["description"].(string); ok && v != "" {
		a.Description = v
	}
	if v, ok := args["default_model"].(string); ok && v != "" {
		a.DefaultModel = v
	}

	if err := st.Store.UpdateAgent(a); err != nil {
		return mcp.ErrorResult(fmt.Sprintf("update agent: %v", err)), nil
	}
	return mcp.TextResult(fmt.Sprintf("Updated agent %q (id=%s)", a.Name, a.ID)), nil
}

// --- builder handlers ---

func (st *SelfToolsTransport) callStartBuilder(args map[string]any) (*mcp.ToolResult, error) {
	// Use a fixed session key — builders are per-transport, not per-chat-session.
	// The chat session ID would be better but isn't available here.
	sessionKey := "default"
	result, err := builders.HandleStartBuilder(st.BuilderRegistry, st.BuilderSessions, sessionKey, args)
	if err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}
	return mcp.TextResult(result), nil
}

func (st *SelfToolsTransport) callBuilderStep(args map[string]any) (*mcp.ToolResult, error) {
	sessionKey := "default"
	result, err := builders.HandleBuilderStep(st.BuilderRegistry, st.BuilderSessions, sessionKey, args)
	if err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}
	return mcp.TextResult(result), nil
}

// --- cross-app handlers ---

func (st *SelfToolsTransport) callNavigateEngine(args map[string]any) (*mcp.ToolResult, error) {
	page, _ := args["page"].(string)
	if page == "" {
		return mcp.ErrorResult("page is required"), nil
	}

	// Build params map from all optional arguments.
	params := make(map[string]string)
	for _, key := range []string{"id", "project_id", "status", "priority", "sort"} {
		if v, ok := args[key].(string); ok && v != "" {
			params[key] = v
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := crossapp.NavigateEngine(ctx, page, params); err != nil {
		return mcp.TextResult(fmt.Sprintf("Navigation failed — Engine may be offline: %v", err)), nil
	}

	// CW-20260419-0013 (user-reported via c17): the LLM-coaching trailer
	// ("Tell the user what you navigated to in one sentence. Do NOT call
	// any more tools.") leaked into the user-visible chat surface. Tool
	// result now reports only what happened; the tool description already
	// instructs the LLM how to behave.
	msg := fmt.Sprintf("Navigated Engine GUI to %s", page)
	if len(params) > 0 {
		msg += fmt.Sprintf(" (filters: %v)", params)
	}
	return mcp.TextResult(msg), nil
}

func (st *SelfToolsTransport) callRefreshEngine(args map[string]any) (*mcp.ToolResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := crossapp.RefreshEngine(ctx); err != nil {
		return mcp.TextResult(fmt.Sprintf("Refresh failed — Engine may be offline: %v", err)), nil
	}
	// CW-20260419-0013: LLM-coaching trailer removed (see callNavigateEngine).
	return mcp.TextResult("Engine GUI data refreshed."), nil
}

// --- envelope injection handlers ---

// buildShowEnvelope assembles the envelope JSON shape emitted by
// card_show. It propagates the optional `target` and `mode` args
// onto top-level envelope fields so the FE's applyEnvelopePanelEffects can
// route drawer-visibility from a tool result (CW-20260428-0007 / J8 v1).
// Empty values are omitted so unknown-mode/unknown-target cases stay silent.
//
// renderTarget and renderTargetBlocked are resolved upstream by the caller
// (callShowCard's trust gate) and stamped here so the wire shape stays in
// one place. Callers without render-target plumbing pass empty strings.
func buildShowEnvelope(envType string, data map[string]any, args map[string]any, renderTarget, renderTargetBlocked string) map[string]any {
	env := map[string]any{
		"kind":    "envelope",
		"version": 1,
		"type":    envType,
		"data":    data,
	}
	if target, _ := args["target"].(string); target != "" {
		env["target"] = target
	}
	if mode, _ := args["mode"].(string); mode != "" {
		env["mode"] = mode
	}
	if renderTarget != "" {
		env["render_target"] = renderTarget
	}
	if renderTargetBlocked != "" {
		env["render_target_blocked"] = renderTargetBlocked
	}
	return env
}

// groundedShowCardTypes are the envelope types whose payload is prose the
// agent renders from tool output. We require the `sources` arg for these
// so the agent has to declare what it grounded the content on. Other
// passive renderables (metric-card, table-card, etc.) carry structured
// values that don't need this gate.
var groundedShowCardTypes = map[string]bool{
	"report-card":     true,
	"document-viewer": true,
}

// callShowCard is the generic envelope-emission tool for the v1 passive-
// renderable allow-list (CW-20260428-0019, A3 — Collab UI v1). It replaces
// the per-type document_show / report_show tools that existed pre-A3.
//
// The agent supplies the envelope `type` (any value in
// envelope.PassiveRenderableTypes) plus a `data` object that this handler
// validates against the per-type JSON Schema before stamping into the
// envelope. target/mode propagation reuses buildShowEnvelope (Gap A).
//
// For envelope types whose body is agent-authored prose grounded in tool
// output (report-card, document-viewer), `sources` is required and
// validated via parseSourcesArg, then post-stamped onto data after schema
// validation — the schemas don't currently model `sources` (and use
// additionalProperties:false), so we add the field after the schema check
// rather than relaxing the schemas.
//
// A2 — CW-20260428-0008. The handler resolves the envelope's render-target
// here:
//   - If the agent passed an explicit `render_target`, gate it through the
//     same V1BuiltinPanelIDs / resolvePanelAccess trust check that protects
//     panel_signal emission. Untrusted plugin-panel routing is dropped to
//     inline + a render_target_blocked hint.
//   - Otherwise read the per-type schema's `default_render_target` (built-
//     in panel IDs only by convention) and stamp that.
//
// ctx is used by the trust resolver; passing context.Background() in tests
// without TrustResolver/PanelLookup wired skips plugin-panel access (the
// gate falls back to "untrusted" so the deny path is still exercised).
func (st *SelfToolsTransport) callShowCard(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	envType, _ := args["type"].(string)
	if envType == "" {
		return mcp.ErrorResult("type is required: pass an envelope type from " + strings.Join(envelope.PassiveRenderableTypes, ", ")), nil
	}
	if !envelope.IsPassiveRenderable(envType) {
		return mcp.ErrorResult(fmt.Sprintf(
			"envelope type %q is not addressable through card_show. Allow-list (v1): %s. "+
				"Decision-flow envelopes (approval-card, proposal-card, confirmation-card), "+
				"runtime-emitted envelopes (chat-loop-terminated, elicitation-prompt, subagent-spawn-approval), "+
				"and plugin-shipped envelopes (giphy-modal) have their own emission paths.",
			envType, strings.Join(envelope.PassiveRenderableTypes, ", "))), nil
	}

	data, ok := args["data"].(map[string]any)
	if !ok || data == nil {
		return mcp.ErrorResult("data is required and must be a JSON object matching the per-type schema"), nil
	}

	if err := envelope.ValidateData(envType, data); err != nil {
		return mcp.ErrorResult(fmt.Sprintf("data does not match the %q schema: %v", envType, err)), nil
	}

	if groundedShowCardTypes[envType] {
		// CW-20260419-0022 (UAT c19): prose-bearing cards must declare what
		// tool_use_ids ground their content. Shallow shape check first.
		sources, sourcesErr := parseSourcesArg(args)
		if sourcesErr != nil {
			return mcp.ErrorResult(sourcesErr.Error()), nil
		}
		// CW-20260429-0024: deep check — every cited tool_use_id must be a
		// real ID observed in this turn's tool_calls (set stamped by
		// service.executeToolBatch). Skipped automatically when the set is
		// nil (test / subagent paths).
		if turnErr := validateSourcesAgainstTurn(ctx, sources); turnErr != nil {
			return mcp.ErrorResult(turnErr.Error()), nil
		}
		data["sources"] = sources
	}

	if envType == "report-card" {
		if _, has := data["generated_at"]; !has {
			data["generated_at"] = time.Now().Format(time.RFC3339)
		}
	}

	renderTarget, renderTargetBlocked := st.resolveShowCardRenderTarget(ctx, envType, args)

	envJSON, _ := json.Marshal(buildShowEnvelope(envType, data, args, renderTarget, renderTargetBlocked))

	label := envType
	if title, _ := data["title"].(string); title != "" {
		label = fmt.Sprintf("%s: %s", envType, title)
	}
	result := fmt.Sprintf("%s\n<!--ENVELOPE_DATA:%s:ENVELOPE_DATA-->", label, string(envJSON))
	return mcp.TextResult(result), nil
}

// resolveShowCardRenderTarget decides where the envelope should render. The
// rules (A2 — CW-20260428-0008):
//
//   - Empty agent arg + no schema default → ("", "") inline.
//   - Empty agent arg + schema default → (default, "") — schema defaults
//     are limited to built-in IDs by convention so they bypass the gate.
//   - Explicit agent value pointing at a built-in panel → (id, "") allowed.
//   - Explicit agent value pointing at a plugin panel:
//   - trust gate passes → (id, "") allowed.
//   - trust gate fails  → ("", reason). Render falls back to inline and
//     the FE surfaces the blocked-reason as a debug pill.
//
// The agent can pass render_target="" explicitly to force-inline a card
// whose schema would otherwise route to a drawer; the empty string is
// distinguishable from a missing arg here only because we read the raw
// args map, but in practice both flow through the same "no override"
// branch — that's the intended behavior.
func (st *SelfToolsTransport) resolveShowCardRenderTarget(ctx context.Context, envType string, args map[string]any) (string, string) {
	override, hasOverride := args["render_target"].(string)
	if !hasOverride || override == "" {
		return envelope.DefaultRenderTarget(envType), ""
	}
	if V1BuiltinPanelIDs[override] {
		return override, ""
	}
	allowed, reason := st.resolvePanelAccess(ctx, override)
	if allowed {
		return override, ""
	}
	return "", reason
}

// --- todo/plan handlers ---

func (st *SelfToolsTransport) callTodoCreate(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if st.TodoStore == nil {
		return mcp.ErrorResult("todo service not available"), nil
	}
	title, _ := args["title"].(string)
	if title == "" {
		return mcp.ErrorResult("title is required"), nil
	}
	scope := strArg(args, "scope", store.TodoScopeSession)
	switch scope {
	case store.TodoScopeTurn, store.TodoScopeSession, store.TodoScopeProject:
	default:
		return mcp.ErrorResult(fmt.Sprintf("scope must be one of turn, session, project (got %q)", scope)), nil
	}

	// D1 (CW-20260428-0014): resolve scope_id and project_id from the current
	// session when the agent omits them. The LLM has no way to know its
	// session_id or project_id; the chat tool executor stamps them on ctx.
	scopeID := strArg(args, "scope_id", "")
	projectID := strArg(args, "project_id", "")
	sessionID := mcp.SessionIDFromContext(ctx)

	switch scope {
	case store.TodoScopeProject:
		if projectID == "" {
			projectID = st.resolveProjectIDFromSession(sessionID)
		}
		if projectID == "" {
			return mcp.ErrorResult("scope=project requires project_id (current session has no project)"), nil
		}
		if scopeID == "" {
			scopeID = projectID
		}
	default: // turn, session
		if scopeID == "" {
			if sessionID == "" {
				return mcp.ErrorResult(fmt.Sprintf("scope_id is required for scope %q (no current session in context)", scope)), nil
			}
			scopeID = sessionID
		}
	}

	t := &store.Todo{
		Title:       title,
		Scope:       scope,
		ScopeID:     scopeID,
		ProjectID:   projectID,
		Priority:    strArg(args, "priority", "medium"),
		Description: strArg(args, "description", ""),
		ParentID:    strArg(args, "parent_id", ""),
		Labels:      strArg(args, "labels", "[]"),
		CreatedBy:   "agent",
	}

	if err := st.TodoStore.CreateTodo(t); err != nil {
		return mcp.ErrorResult(fmt.Sprintf("create todo: %v", err)), nil
	}

	st.notifyWorkChanged()
	out, _ := json.Marshal(t)
	return mcp.TextResult(fmt.Sprintf("Created todo %q (id=%s, scope=%s)\n%s", t.Title, t.ID, t.Scope, string(out))), nil
}

// resolveProjectIDFromSession looks up the project_id for the given session.
// Returns "" when sessionID is empty, the lookup fails, or the session has no
// project. Used by self-tools to auto-fill project_id when the LLM omits it.
func (st *SelfToolsTransport) resolveProjectIDFromSession(sessionID string) string {
	if sessionID == "" || st.Store == nil {
		return ""
	}
	sess, err := st.Store.GetSession(sessionID)
	if err != nil || sess == nil {
		return ""
	}
	return sess.ProjectID
}

func (st *SelfToolsTransport) callTodoUpdate(args map[string]any) (*mcp.ToolResult, error) {
	if st.TodoStore == nil {
		return mcp.ErrorResult("todo service not available"), nil
	}
	id := strArg(args, "id", "")
	if id == "" {
		return mcp.ErrorResult("id is required"), nil
	}

	t, err := st.TodoStore.GetTodo(id)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("get todo: %v", err)), nil
	}

	if v, ok := args["title"].(string); ok && v != "" {
		t.Title = v
	}
	if v, ok := args["description"].(string); ok && v != "" {
		t.Description = v
	}
	if v, ok := args["status"].(string); ok && v != "" {
		t.Status = v
	}
	if v, ok := args["priority"].(string); ok && v != "" {
		t.Priority = v
	}
	if v, ok := args["labels"].(string); ok && v != "" {
		t.Labels = v
	}

	if err := st.TodoStore.UpdateTodo(t); err != nil {
		return mcp.ErrorResult(fmt.Sprintf("update todo: %v", err)), nil
	}

	st.notifyWorkChanged()
	return mcp.TextResult(fmt.Sprintf("Updated todo %q (id=%s, status=%s, priority=%s)", t.Title, t.ID, t.Status, t.Priority)), nil
}

func (st *SelfToolsTransport) callTodoList(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if st.TodoStore == nil {
		return mcp.ErrorResult("todo service not available"), nil
	}

	// D1 (CW-20260428-0014): resolve scope_id / project_id from ctx when the
	// agent omits them so the emitted list-card envelope carries the real
	// IDs. Otherwise the live todo composition lazy-fetches with empty
	// filters and the drawer renders nothing. Listing itself still works
	// fine with an empty filter, so this is best-effort — no error path.
	scopeArg := strArg(args, "scope", "")
	scopeIDArg := strArg(args, "scope_id", "")
	projectIDArg := strArg(args, "project_id", "")
	sessionID := mcp.SessionIDFromContext(ctx)
	switch scopeArg {
	case store.TodoScopeSession, store.TodoScopeTurn:
		if scopeIDArg == "" && sessionID != "" {
			scopeIDArg = sessionID
		}
	case store.TodoScopeProject:
		if projectIDArg == "" {
			projectIDArg = st.resolveProjectIDFromSession(sessionID)
		}
		if scopeIDArg == "" {
			scopeIDArg = projectIDArg
		}
	}

	f := store.TodoFilter{
		Scope:     scopeArg,
		ScopeID:   scopeIDArg,
		ProjectID: projectIDArg,
		Status:    strArg(args, "status", ""),
		Priority:  strArg(args, "priority", ""),
	}

	todos, err := st.TodoStore.ListTodos(f)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("list todos: %v", err)), nil
	}

	var sb strings.Builder
	if len(todos) == 0 {
		sb.WriteString("No todos found matching filters.")
	} else {
		fmt.Fprintf(&sb, "Found %d todo(s):\n\n", len(todos))
		for _, t := range todos {
			fmt.Fprintf(&sb, "- [%s] %s (id=%s, priority=%s, scope=%s/%s)\n",
				t.Status, t.Title, t.ID, t.Priority, t.Scope, t.ScopeID)
			if t.Description != "" {
				fmt.Fprintf(&sb, "  %s\n", t.Description)
			}
		}
	}

	// Emit a list-card envelope (with a live `data_source` pointer) so the
	// UI renders an interactive card. This is the Phase 6 composition that
	// replaced the standalone `todo-list` envelope type
	// (TASKS/phase-6/01-rebuild-todo-list-as-composition.md) — the frontend
	// ListCard component detects data_source.kind == "todos" and
	// lazy-fetches /api/todos by scope + scope_id itself, so the envelope
	// only needs to carry the filter coordinates, not the items themselves.
	// `items` still carries an empty placeholder array to satisfy
	// list-card's schema (`items` is required); the frontend does not
	// trust it when data_source is set. We only attach the envelope when
	// scope is present; an empty scope would render an un-scoped card that
	// matches every session. CW-20260418-0045.
	if f.Scope != "" {
		title := strArg(args, "title", "Todos")
		envJSON, _ := json.Marshal(map[string]any{
			"kind":    "envelope",
			"version": 1,
			"type":    "list-card",
			"data": map[string]any{
				"title": title,
				"items": []any{},
				"data_source": map[string]any{
					"kind":     "todos",
					"scope":    f.Scope,
					"scope_id": f.ScopeID,
				},
			},
		})
		fmt.Fprintf(&sb, "\n<!--ENVELOPE_DATA:%s:ENVELOPE_DATA-->", string(envJSON))
	}

	return mcp.TextResult(sb.String()), nil
}

func (st *SelfToolsTransport) callPlanCreate(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if st.TodoStore == nil {
		return mcp.ErrorResult("todo service not available"), nil
	}
	title, _ := args["title"].(string)
	scope, _ := args["scope"].(string)
	if title == "" || scope == "" {
		return mcp.ErrorResult("title and scope are required"), nil
	}

	// CW-20260418 (c7 scope_id fix): same auto-fill as callTodoCreate.
	scopeID := strArg(args, "scope_id", "")
	if scope != "workspace" && scopeID == "" {
		if sid := mcp.SessionIDFromContext(ctx); sid != "" {
			scopeID = sid
		} else {
			return mcp.ErrorResult(fmt.Sprintf("scope_id is required for scope %q (no current session in context)", scope)), nil
		}
	}

	p := &store.Plan{
		Title:       title,
		Scope:       scope,
		ScopeID:     scopeID,
		Description: strArg(args, "description", ""),
		Steps:       strArg(args, "steps", "[]"),
		CreatedBy:   "agent",
	}

	if err := st.TodoStore.CreatePlan(p); err != nil {
		return mcp.ErrorResult(fmt.Sprintf("create plan: %v", err)), nil
	}

	st.notifyWorkChanged()
	out, _ := json.Marshal(p)
	return mcp.TextResult(fmt.Sprintf("Created plan %q (id=%s, scope=%s)\n%s", p.Title, p.ID, p.Scope, string(out))), nil
}

func (st *SelfToolsTransport) callPlanUpdate(args map[string]any) (*mcp.ToolResult, error) {
	if st.TodoStore == nil {
		return mcp.ErrorResult("todo service not available"), nil
	}
	id := strArg(args, "id", "")
	if id == "" {
		return mcp.ErrorResult("id is required"), nil
	}

	// If step_id is provided, update just that step.
	stepID, _ := args["step_id"].(string)
	if stepID != "" {
		stepUpdates := store.PlanStep{
			Status: strArg(args, "status", ""),
			Notes:  strArg(args, "notes", ""),
		}
		if err := st.TodoStore.UpdatePlanStep(id, stepID, stepUpdates); err != nil {
			return mcp.ErrorResult(fmt.Sprintf("update plan step: %v", err)), nil
		}
		st.notifyWorkChanged()
		return mcp.TextResult(fmt.Sprintf("Updated step %s in plan %s", stepID, id)), nil
	}

	// Otherwise update plan-level fields.
	p, err := st.TodoStore.GetPlan(id)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("get plan: %v", err)), nil
	}

	if v, ok := args["title"].(string); ok && v != "" {
		p.Title = v
	}
	if v, ok := args["status"].(string); ok && v != "" {
		p.Status = v
	}

	if err := st.TodoStore.UpdatePlan(p); err != nil {
		return mcp.ErrorResult(fmt.Sprintf("update plan: %v", err)), nil
	}

	st.notifyWorkChanged()
	return mcp.TextResult(fmt.Sprintf("Updated plan %q (id=%s, status=%s)", p.Title, p.ID, p.Status)), nil
}

// callPlanStepAdd appends one or more steps to an existing plan without
// re-creating it. CW-20260430-0001 (SP1) — closes the c120 workaround.
func (st *SelfToolsTransport) callPlanStepAdd(args map[string]any) (*mcp.ToolResult, error) {
	if st.TodoStore == nil {
		return mcp.ErrorResult("todo service not available"), nil
	}
	planID := strArg(args, "plan_id", "")
	if planID == "" {
		return mcp.ErrorResult("plan_id is required"), nil
	}

	stepsArg, ok := args["steps"]
	if !ok || stepsArg == nil {
		return mcp.ErrorResult("steps is required (JSON array string or array)"), nil
	}

	var raw []byte
	switch v := stepsArg.(type) {
	case string:
		if v == "" {
			return mcp.ErrorResult("steps is required (JSON array string or array)"), nil
		}
		raw = []byte(v)
	default:
		// Allow callers that already deserialise the array.
		marshaled, err := json.Marshal(v)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("steps must be a JSON array: %v", err)), nil
		}
		raw = marshaled
	}

	var newSteps []store.PlanStep
	if err := json.Unmarshal(raw, &newSteps); err != nil {
		return mcp.ErrorResult(fmt.Sprintf("parse steps: %v", err)), nil
	}
	if len(newSteps) == 0 {
		return mcp.ErrorResult("steps must contain at least one step"), nil
	}

	appended, err := st.TodoStore.AppendPlanSteps(planID, newSteps)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("append plan steps: %v", err)), nil
	}

	st.notifyWorkChanged()

	out, _ := json.Marshal(map[string]any{
		"plan_id":        planID,
		"appended":       appended,
		"appended_count": len(appended),
	})
	return mcp.TextResult(fmt.Sprintf("Appended %d step(s) to plan %s\n%s", len(appended), planID, string(out))), nil
}

func (st *SelfToolsTransport) callPlanList(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if st.TodoStore == nil {
		return mcp.ErrorResult("todo service not available"), nil
	}

	// CW-20260418 (c7 scope_id fix): auto-fill session scope_id from ctx.
	scopeArg := strArg(args, "scope", "")
	scopeIDArg := strArg(args, "scope_id", "")
	if scopeArg == "session" && scopeIDArg == "" {
		if sid := mcp.SessionIDFromContext(ctx); sid != "" {
			scopeIDArg = sid
		}
	}

	f := store.PlanFilter{
		Scope:   scopeArg,
		ScopeID: scopeIDArg,
		Status:  strArg(args, "status", ""),
	}

	plans, err := st.TodoStore.ListPlans(f)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("list plans: %v", err)), nil
	}

	if len(plans) == 0 {
		return mcp.TextResult("No plans found matching filters."), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d plan(s):\n\n", len(plans))
	for _, p := range plans {
		steps, _ := p.ParsePlanSteps()
		fmt.Fprintf(&sb, "- [%s] %s (id=%s, scope=%s/%s, steps=%d)\n",
			p.Status, p.Title, p.ID, p.Scope, p.ScopeID, len(steps))
		if p.Description != "" {
			fmt.Fprintf(&sb, "  %s\n", p.Description)
		}
	}
	return mcp.TextResult(sb.String()), nil
}

func (st *SelfToolsTransport) callPlanGet(args map[string]any) (*mcp.ToolResult, error) {
	if st.TodoStore == nil {
		return mcp.ErrorResult("todo service not available"), nil
	}
	id := strArg(args, "id", "")
	if id == "" {
		return mcp.ErrorResult("id is required"), nil
	}

	p, err := st.TodoStore.GetPlan(id)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("get plan: %v", err)), nil
	}

	out, _ := json.Marshal(p)
	return mcp.TextResult(string(out)), nil
}

func (st *SelfToolsTransport) callPlanDelete(args map[string]any) (*mcp.ToolResult, error) {
	if st.TodoStore == nil {
		return mcp.ErrorResult("todo service not available"), nil
	}
	id := strArg(args, "id", "")
	if id == "" {
		return mcp.ErrorResult("id is required"), nil
	}

	if err := st.TodoStore.DeletePlan(id); err != nil {
		return mcp.ErrorResult(fmt.Sprintf("delete plan: %v", err)), nil
	}
	st.notifyWorkChanged()
	return mcp.TextResult(fmt.Sprintf("Deleted plan %s", id)), nil
}

// --- install handlers ---

func (st *SelfToolsTransport) callInstallHome(args map[string]any) (*mcp.ToolResult, error) {
	force, _ := args["force"].(bool)
	svc := install.New()
	report, err := svc.InstallHome(install.InstallHomeOptions{Force: force})
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("install home: %v", err)), nil
	}
	return mcp.TextResult(fmt.Sprintf("install home: created=%d unchanged=%d skipped=%d forced=%d",
		report.Created, report.Unchanged, report.Skipped, report.Forced)), nil
}

func (st *SelfToolsTransport) callInstallProject(args map[string]any) (*mcp.ToolResult, error) {
	projectDir, _ := args["project_dir"].(string)
	if projectDir == "" {
		return mcp.ErrorResult("project_dir is required"), nil
	}
	svc := install.New()
	report, err := svc.InstallProject(install.InstallProjectOptions{
		ProjectDir: projectDir,
	})
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("install project: %v", err)), nil
	}

	summary := fmt.Sprintf("install project %s:", projectDir)
	switch {
	case report.FreshScaffold:
		summary += " fresh scaffold"
	case report.Adopted:
		summary += " adopted existing"
	}
	if len(report.Warnings) > 0 {
		summary += "\nwarnings:\n  - " + strings.Join(report.Warnings, "\n  - ")
	}
	return mcp.TextResult(summary), nil
}

func (st *SelfToolsTransport) callInstallDiff(args map[string]any) (*mcp.ToolResult, error) {
	// TODO(Plan A Task 15+): implement dry-run mode in internal/service/install
	// that returns an action list without mutating state.
	_ = args
	return mcp.TextResult("install diff not yet implemented"), nil
}

// --- Messaging handlers ---
//
// The message_subscribe tool is intentionally not registered here: it
// requires streaming support in mcp-go or a custom server-side handler,
// which is deferred to a follow-up task. See Task 10 notes.

func (st *SelfToolsTransport) callMessageSend(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if st.Messaging == nil {
		return mcp.ErrorResult("messaging service not configured"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, messageCallTimeout)
	defer cancel()

	kind := strArg(args, "kind", "")
	body := strArg(args, "body", "")
	msgType := strArg(args, "type", "")

	// G4 elicitation pilot (CW-20260420-0018, D5): directive messages broadcast
	// instructions to all recipients and carry elevated blast radius. Require
	// explicit user confirmation before sending when elicitation is wired.
	// Directive is a `type` value (see message_send InputSchema), not kind.
	if msgType == "directive" && st.Elicitation != nil {
		fromSessionID := strArg(args, "from_session_id", "")
		fromAgentID := strArg(args, "from_agent_id", "")
		elicitResp, err := mcp.ElicitUserInput(ctx, st.Elicitation, fromSessionID, fromAgentID, "",
			mcp.ElicitationCreateParams{
				Message: fmt.Sprintf("Send directive to %s? Body: %q", strArg(args, "to_agent_id", ""), body),
				RequestedSchema: &mcp.ElicitationRequestedSchema{
					Type:        "boolean",
					Title:       "Confirm directive send",
					Description: "Directive messages instruct recipient agents to take action. Confirm to proceed.",
				},
			})
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("elicitation: %v", err)), nil
		}
		if elicitResp.Action != "accept" {
			return mcp.TextResult(fmt.Sprintf("directive send aborted by user (action=%s)", elicitResp.Action)), nil
		}
	}

	msg := messaging.SendInput{
		FromSessionID: strArg(args, "from_session_id", ""),
		FromAgentID:   strArg(args, "from_agent_id", ""),
		ToSessionID:   strArg(args, "to_session_id", ""),
		ToAgentID:     strArg(args, "to_agent_id", ""),
		Channel:       strArg(args, "channel", ""),
		Kind:          kind,
		PayloadJSON:   strArg(args, "payload_json", ""),
		Subject:       strArg(args, "subject", ""),
		Body:          body,
		Type:          msgType,
		ReplyTo:       strArg(args, "reply_to", ""),
		RegisterAs:    strArg(args, "register_as", ""),
	}
	out, err := st.Messaging.SendMessage(ctx, msg)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("message send: %v", err)), nil
	}
	return mcp.TextResult(fmt.Sprintf("sent: %s", out.ID)), nil
}

func (st *SelfToolsTransport) callMessageInbox(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if st.Messaging == nil {
		return mcp.ErrorResult("messaging service not configured"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, messageCallTimeout)
	defer cancel()
	// MVP caller identity: the MCP boundary has no out-of-band caller
	// channel yet, so the same (session_id, agent_id) args serve as both
	// target and caller. Real caller-identity-from-ctx is a post-MVP
	// upgrade; the service still enforces the match, which catches a
	// misconfigured caller that passes different values.
	sessionID := strArg(args, "session_id", "")
	agentID := strArg(args, "agent_id", "")
	inbox, err := st.Messaging.Inbox(
		ctx,
		sessionID,
		agentID,
		messaging.InboxFilter{
			Status:  strArg(args, "status", ""),
			Channel: strArg(args, "channel", ""),
			Kind:    strArg(args, "kind", ""),
		},
		sessionID,
		agentID,
	)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("message inbox: %v", err)), nil
	}
	if inbox == nil {
		inbox = []messaging.Message{}
	}
	data, err := json.Marshal(inbox)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("messaging inbox marshal: %v", err)), nil
	}
	return mcp.TextResult(string(data)), nil
}

func (st *SelfToolsTransport) callMessageThread(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if st.Messaging == nil {
		return mcp.ErrorResult("messaging service not configured"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, messageCallTimeout)
	defer cancel()
	messages, err := st.Messaging.Thread(
		ctx,
		strArg(args, "thread_id", ""),
		strArg(args, "session_id", ""),
		strArg(args, "agent_id", ""),
	)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("message thread: %v", err)), nil
	}
	if messages == nil {
		messages = []messaging.Message{}
	}
	data, err := json.Marshal(messages)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("messaging thread marshal: %v", err)), nil
	}
	return mcp.TextResult(string(data)), nil
}

func (st *SelfToolsTransport) callMessageAck(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if st.Messaging == nil {
		return mcp.ErrorResult("messaging service not configured"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, messageCallTimeout)
	defer cancel()
	if err := st.Messaging.Ack(
		ctx,
		strArg(args, "session_id", ""),
		strArg(args, "agent_id", ""),
		strArg(args, "message_id", ""),
	); err != nil {
		return mcp.ErrorResult(fmt.Sprintf("message ack: %v", err)), nil
	}
	return mcp.TextResult("acked"), nil
}

func (st *SelfToolsTransport) callMessageResolve(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if st.Messaging == nil {
		return mcp.ErrorResult("messaging service not configured"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, messageCallTimeout)
	defer cancel()
	if err := st.Messaging.Resolve(
		ctx,
		strArg(args, "session_id", ""),
		strArg(args, "agent_id", ""),
		strArg(args, "message_id", ""),
	); err != nil {
		return mcp.ErrorResult(fmt.Sprintf("message resolve: %v", err)), nil
	}
	return mcp.TextResult("resolved"), nil
}

func (st *SelfToolsTransport) callMessageCatchUp(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if st.Messaging == nil {
		return mcp.ErrorResult("messaging service not configured"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, messageCallTimeout)
	defer cancel()
	// intArg does not clamp; non-positive values are passed through to
	// the service/store layers, which apply default-20 + store cap.
	limit := mcp.IntArg(args, "limit", 20)
	messages, err := st.Messaging.RecentForSession(
		ctx,
		strArg(args, "session_id", ""),
		limit,
	)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("message catch_up: %v", err)), nil
	}
	if messages == nil {
		messages = []messaging.Message{}
	}
	data, err := json.Marshal(messages)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("messaging catch_up marshal: %v", err)), nil
	}
	return mcp.TextResult(string(data)), nil
}

func (st *SelfToolsTransport) callHandoffRequest(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if st.Messaging == nil {
		return mcp.ErrorResult("messaging service not configured"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, messageCallTimeout)
	defer cancel()
	// Session id is harness-assigned: prefer the dispatch context over an
	// agent-supplied arg, which a CLI-launch agent cannot reliably know.
	sessionID := mcp.SessionIDFromContext(ctx)
	if sessionID == "" {
		sessionID = strArg(args, "session_id", "")
	}
	id, err := st.Messaging.RequestHandoff(
		ctx,
		sessionID,
		strArg(args, "from_agent_id", ""),
		strArg(args, "to_agent_id", ""),
		strArg(args, "requested_by", ""),
	)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("handoff request: %v", err)), nil
	}
	return mcp.TextResult("handoff requested: " + id), nil
}

func (st *SelfToolsTransport) callHandoffApprove(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if st.Messaging == nil {
		return mcp.ErrorResult("messaging service not configured"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, messageCallTimeout)
	defer cancel()
	if err := st.Messaging.ApproveHandoff(ctx, strArg(args, "handoff_id", "")); err != nil {
		return mcp.ErrorResult(fmt.Sprintf("handoff approve: %v", err)), nil
	}
	return mcp.TextResult("approved"), nil
}

func (st *SelfToolsTransport) callHandoffReject(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if st.Messaging == nil {
		return mcp.ErrorResult("messaging service not configured"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, messageCallTimeout)
	defer cancel()
	if err := st.Messaging.RejectHandoff(
		ctx,
		strArg(args, "handoff_id", ""),
		strArg(args, "reason", ""),
	); err != nil {
		return mcp.ErrorResult(fmt.Sprintf("handoff reject: %v", err)), nil
	}
	return mcp.TextResult("rejected"), nil
}

// --- subagent handlers (T9) ---

// callSpawnSubagent handles subagent_spawn. Returns a ResultEnvelope
// (success boolean + structured result/error) JSON-encoded into the
// ToolResult text content. The envelope shape is the
// CW-20260512-0122 structural fix for the c160 turn-18 reproduction:
// when the subagent fails (timeout / denied / internal / empty reply),
// the parent reads success=false and the structured error.kind +
// error.message, instead of receiving a textResult that is
// indistinguishable from a fabricated success.
//
// All three modes auto-approve for MVP; interactive approval is a
// follow-up (T9.2).
//
//   - sync: blocks on the subagent run; the envelope's Success flag is
//     derived from the terminal Run.Status and the recovered
//     assistant text. ToolResult.IsError mirrors !Success so the
//     existing tool-error UI surfaces failures alongside the
//     structured envelope.
//   - async / api: returns immediately with an envelope reporting
//     success=true and the run_id in Result. The parent polls
//     subagent_status or the inbox for the eventual reply (which
//     itself carries no envelope today — async failure surfaces
//     through subagent_status, not the spawn return value).
//   - spawn-stage errors: pre-run rejections (untrusted role, missing
//     fields, etc.) return success=false with error.kind=denied or
//     error.kind=internal depending on the cause.
//
// IsError is set on the ToolResult only when success=false so the
// chat-tool executor's error path lights up alongside the envelope
// body — defense in depth for callers that read IsError as a
// shortcut for "did this tool succeed". The envelope JSON is the
// authoritative shape; IsError is a redundant mirror.
//
// Per CW-20260512-0122 sharp edges: the envelope MUST NOT fabricate
// tool_use_ids (same trust class as SP-20260512-0007). Context fields
// are populated only from authoritative run state (status, role,
// run_id). Pre-launch: the prior textResult(summary) shape is gone
// (feedback_no_compat_shims). Callers parsing the old shape must
// migrate to the envelope.
func (st *SelfToolsTransport) callSpawnSubagent(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if st.Subagent == nil {
		// Wiring miss — no run was ever created, so no run_id. Returns
		// success=false; the universal slot rule tells the LLM to
		// acknowledge the failure rather than narrate success.
		env := subagent.NewFailureEnvelope("", subagent.ErrorKindInternal,
			"subagent service not configured", nil)
		return envelopeResult(env), nil
	}

	// CW-20260516-0066: hard subagent recursion-depth cap. subagent_spawn
	// creates a new tracked child session — only a depth-0 progenitor (a
	// root / user-facing session with no parent) may invoke it. If the
	// caller's session is itself a subagent, reject before any run row is
	// created. The caller identity comes from the ctx, not the
	// LLM-supplied parent_session_id arg. Kind=denied so the parent reads
	// this as a deliberate trust refusal, not an internal fault.
	if blocked, err := st.recursionBlocked(ctx); err != nil {
		env := subagent.NewFailureEnvelope("", subagent.ErrorKindInternal, err.Error(), nil)
		return envelopeResult(env), nil
	} else if blocked {
		env := subagent.NewFailureEnvelope("", subagent.ErrorKindDenied,
			subagentRecursionBlockedMsg, nil)
		return envelopeResult(env), nil
	}

	ctx, cancel := context.WithTimeout(ctx, messageCallTimeout)
	defer cancel()
	// Caller identity is authoritative from the ctx, not the LLM-supplied
	// parent_session_id arg — consistent with the recursion cap above. A
	// forged arg must not be able to attach the run row / reply to a
	// different session. The arg is a fallback only for ctx-less paths.
	parentSessionID := strArg(args, "parent_session_id", "")
	if ctxSID := mcp.SessionIDFromContext(ctx); ctxSID != "" {
		parentSessionID = ctxSID
	}
	// CW-20260516-0058 / CW-20260815 (emit-react postmortem): ParentAgentID
	// gates the reply-delivery block in subagent.Service.execute — an empty
	// or unresolvable value silently drops the completion reply (Layer 1 of
	// the emit→react mechanism never fires, with only a slog.Warn to show
	// for it). The LLM cannot reliably know its own agent_profiles.ID (it
	// has no introspective access to it) and, when the tool schema asks for
	// one anyway, will confidently supply a plausible-looking wrong value
	// (its own slug/name, e.g. "orchestrator") rather than leave the field
	// empty — which is worse than not supplying it, because that value then
	// fails ValidateAgentID and the whole reply is dropped. Caller identity
	// is authoritative from ctx, exactly like parentSessionID above; the
	// arg is a fallback only for ctx-less paths (tests, etc.), never an
	// override of a real ctx-derived identity.
	parentAgentID := strArg(args, "parent_agent_id", "")
	if apID := mcp.CallerProfileFromContext(ctx); apID != "" {
		parentAgentID = apID
	}
	req := subagent.SpawnRequest{
		ParentSessionID: parentSessionID,
		ParentAgentID:   parentAgentID,
		Role:            strArg(args, "role", ""),
		Prompt:          strArg(args, "prompt", ""),
		Mode:            strArg(args, "mode", "sync"),
		InputsJSON:      strArg(args, "inputs_json", ""),
		TimeoutSeconds:  mcp.IntArg(args, "timeout_seconds", 0),
		Provider:        strArg(args, "provider", ""),
	}
	id, err := st.Subagent.Spawn(ctx, req)
	if err != nil {
		// Spawn-stage failures (validation, untrusted role, missing
		// fields, settings load) — no run row exists. Kind selection:
		//   - denied: deliberate trust refusal (untrusted role tier)
		//   - config: role has no registered profile, or profile is
		//     can_execute=false and not in the text-only whitelist
		//     (CW-20260519-0123). Fixable by registering the missing
		//     profile or routing to a known role — distinct from an
		//     internal fault so the parent can act.
		//   - at_capacity: fan-out semaphore full; caller's context
		//     deadline expired while waiting for a slot
		//     (CW-20260816-0001). Retryable when slots free up.
		//   - internal: everything else (catch-all)
		//
		// For config-kind envelopes, populate `reason` (PR #214 review
		// fix item 5) with the stable ConfigReason* discriminator so
		// downstream consumers don't have to parse the message string
		// to branch on no-profile vs not-executable.
		kind := subagent.ErrorKindInternal
		ctx := map[string]any{"role": req.Role}
		switch {
		case errors.Is(err, dispatch.ErrUntrustedRole):
			kind = subagent.ErrorKindDenied
		case errors.Is(err, subagent.ErrNoProfileForRole):
			kind = subagent.ErrorKindConfig
			ctx["reason"] = subagent.ConfigReasonNoProfile
		case errors.Is(err, subagent.ErrRoleNotExecutable):
			kind = subagent.ErrorKindConfig
			ctx["reason"] = subagent.ConfigReasonNotExecutable
		case errors.Is(err, subagent.ErrSpawnFanoutCapReached):
			kind = subagent.ErrorKindAtCapacity
		}
		env := subagent.NewFailureEnvelope("", kind,
			fmt.Sprintf("spawn subagent: %v", err),
			ctx)
		return envelopeResult(env), nil
	}
	if req.Mode == "" || req.Mode == subagent.ModeSync {
		env := st.syncSubagentEnvelope(ctx, id)
		return envelopeResult(env), nil
	}
	// async / api: spawn ack-only. The parent receives the reply
	// asynchronously (inbox for async, chat for api). Success=true
	// here means "spawn accepted"; whether the run itself succeeds is
	// observable via subagent_status.
	env := subagent.NewSuccessEnvelope(id, "")
	return envelopeResult(env), nil
}

// syncSubagentEnvelope blocks until the subagent run is terminal,
// then builds a ResultEnvelope from the Run row and the recovered
// assistant text. Called by callSpawnSubagent's sync path.
//
// The recovered summary is the child session's last assistant text
// (the same prose previously returned verbatim by the pre-envelope
// textResult path). When the run completed but produced no
// recoverable text, EnvelopeFromRun maps that to ErrorKindEmptyReply
// so the parent knows there is no reply to surface — the c160
// turn-18 fabrication-class regression target.
//
// CW-20260816-0002: Spawn's ModeSync branch (Spawn's own switch,
// several hundred lines above this file's boundary — see
// internal/subagent/service.go) already blocks synchronously on
// executeWithSlot and does not return the run_id to this function's
// caller until the run is terminal. So by the time syncSubagentEnvelope
// runs, the run is (bar a vanishingly rare race) already done — this
// function's job is really just "fetch the terminal Run row and build
// an envelope from it," not "wait for completion."
//
// The bug this fixes: the ctx passed in here is the SAME ctx used for
// the (already-completed, possibly long) Spawn call above it. If that
// ctx's deadline fired at any point during Spawn's block — entirely
// plausible for a multi-minute run — it is already expired by the time
// we get here, even though Spawn itself ignores it (executeWithSlot
// runs on a background-derived ctx, not the caller's). Status(ctx, ...)
// then fails immediately with "context deadline exceeded" despite the
// answer sitting right there in the DB, and the caller incorrectly
// narrates total failure for a run that actually succeeded.
//
// Fix: on a ctx-cancelled Status failure, retry once with a short,
// fresh background context before giving up — cheap, and recovers the
// common case where the real terminal result was simply blocked by an
// unrelated expired ctx rather than genuinely still in progress. The
// polling loop below is kept as defensive handling for the rare case
// where Status is somehow reached before the run is terminal (e.g. a
// future caller of this function that doesn't share Spawn's blocking
// behavior) — it is not what the fix actually depends on.
func (st *SelfToolsTransport) syncSubagentEnvelope(ctx context.Context, runID string) subagent.ResultEnvelope {
	const pollInterval = 500 * time.Millisecond
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		run, err := st.Subagent.Status(ctx, runID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return subagent.NewFailureEnvelope(runID, subagent.ErrorKindInternal,
					"subagent run not found after spawn", nil)
			}
			if ctx.Err() != nil {
				// The caller's ctx is cancelled/expired — not necessarily
				// because the run is still running. Re-check with a fresh
				// context before concluding that; Spawn's own block means
				// the row is very likely already terminal.
				freshCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				freshRun, freshErr := st.Subagent.Status(freshCtx, runID)
				cancel()
				if freshErr == nil && freshRun != nil && freshRun.Status != subagent.StatusRunning {
					run, err = freshRun, nil
				} else {
					return subagent.NewFailureEnvelope(runID, subagent.ErrorKindTimeout,
						fmt.Sprintf("sync wait expired (run may still be executing): %v", ctx.Err()),
						map[string]any{
							"run_id": runID,
							"hint":   "call subagent_status to check final outcome",
						})
				}
			} else {
				// Genuine DB error (e.g., closed connection, corrupted row).
				return subagent.NewFailureEnvelope(runID, subagent.ErrorKindInternal,
					fmt.Sprintf("status lookup failed: %v", err), nil)
			}
		}
		if run == nil {
			return subagent.NewFailureEnvelope(runID, subagent.ErrorKindInternal,
				"subagent run not found after spawn", nil)
		}

		// Ready for EnvelopeFromRun once the run is out of StatusRunning.
		// This is deliberately broader than IsTerminalStatus: besides the
		// terminal set (completed, failed, over_budget, stalled,
		// cancelled, rejected), EnvelopeFromRun also has an explicit,
		// load-bearing case for StatusRequested/StatusApproved — an
		// approval-gated spawn acks success immediately rather than
		// waiting for a human, and must not be treated as "still
		// running" here (that regressed a real approval-gating test:
		// waiting on ctx.Done() below would misreport a pending-approval
		// spawn as ErrorKindTimeout instead of the intended ack).
		if run.Status != subagent.StatusRunning {
			summary, recoverErr := st.recoverSyncSummary(run)
			if recoverErr != nil {
				// Summary recovery failed due to an actual error (e.g. ListMessages
				// returned a DB error, store closed). This is an internal failure,
				// NOT an empty-reply case — routing through EnvelopeFromRun with
				// summary="" would emit ErrorKindEmptyReply ("completed but
				// returned no assistant text"), which is misleading when the real
				// cause is a backend fault. Surface the underlying error so the
				// parent's failure handling reflects the actual issue.
				return subagent.NewFailureEnvelope(run.ID, subagent.ErrorKindInternal,
					fmt.Sprintf("summary recovery failed: %v", recoverErr),
					map[string]any{
						"role":   run.Role,
						"status": run.Status,
					})
			}
			return subagent.EnvelopeFromRun(run, summary)
		}

		// Only StatusRunning reaches here (requested/approved and the
		// terminal set are handled above). Wait for next poll or context
		// cancellation.
		select {
		case <-ctx.Done():
			// Context cancelled during wait. The run is still in progress
			// (status = run.Status). Return ErrorKindTimeout with current
			// status so the parent knows the run did not fail — it's just
			// not done within the sync wait budget.
			return subagent.NewFailureEnvelope(runID, subagent.ErrorKindTimeout,
				fmt.Sprintf("sync wait cancelled (run status: %s, may still be executing): %v", run.Status, ctx.Err()),
				map[string]any{
					"run_id": runID,
					"status": run.Status,
					"hint":   "call subagent_status to check final outcome",
				})
		case <-ticker.C:
			// Poll again.
			continue
		}
	}
}

// recoverSyncSummary scans the child session's assistant messages for
// the most recent text content. Returns (summary, nil) on success
// (including the "no assistant text exists" empty-string case), or
// ("", err) when the underlying store lookup itself fails. The
// caller (syncSubagentEnvelope) discriminates between these two cases
// so a DB error is reported as ErrorKindInternal, not the
// misleading ErrorKindEmptyReply that the prior eat-the-error
// signature produced.
//
// Round-1 fix (Copilot #3): pre-round-1 this helper returned plain
// string and swallowed ListMessages errors. The caller had no way to
// distinguish "store closed" from "no text" — both routed through
// EnvelopeFromRun with summary="" and emitted ErrorKindEmptyReply.
func (st *SelfToolsTransport) recoverSyncSummary(run *subagent.Run) (string, error) {
	if run == nil || run.ChildSessionID == "" {
		// No child session to scan — legitimately empty, not an error.
		return "", nil
	}
	msgs, err := st.Store.ListMessages(run.ChildSessionID, 20)
	if err != nil {
		// Store lookup failure — surface to caller so the envelope can
		// emit ErrorKindInternal instead of the misleading
		// ErrorKindEmptyReply ("completed but returned no assistant text").
		return "", fmt.Errorf("list messages: %w", err)
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "assistant" || msgs[i].Content == "" {
			continue
		}
		if text := extractStoredAssistantText(msgs[i].Content); text != "" {
			if literal, ok := extractLiteralSubagentOutput(run.Prompt, text); ok {
				return literal, nil
			}
			return text, nil
		}
		return msgs[i].Content, nil
	}
	return "", nil
}

// envelopeResult marshals a ResultEnvelope into a ToolResult. IsError
// mirrors !Success so the chat-tool executor's existing error path
// surfaces failures in parallel with the structured envelope body —
// belt-and-braces against callers that read IsError as a shortcut.
func envelopeResult(env subagent.ResultEnvelope) *mcp.ToolResult {
	return &mcp.ToolResult{
		Content: []mcp.ToolContent{{Type: "text", Text: subagent.MarshalEnvelope(env)}},
		IsError: !env.Success,
	}
}

func extractStoredAssistantText(content string) string {
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return ""
	}
	return payload.Text
}

func extractLiteralSubagentOutput(prompt, text string) (string, bool) {
	if !looksLikeLiteralContentPrompt(prompt) {
		return "", false
	}
	block, ok := extractSingleFencedCodeBlock(text)
	if ok {
		return formatLiteralFileReadReply(prompt, block)
	}
	list, ok := extractLeadingNumberedList(text)
	if !ok {
		return "", false
	}
	return formatLiteralFileReadReply(prompt, list)
}

func looksLikeLiteralContentPrompt(prompt string) bool {
	p := strings.ToLower(prompt)
	if !strings.Contains(p, "/") {
		return false
	}
	if !strings.Contains(p, "read") {
		return false
	}
	return strings.Contains(p, "line") || strings.Contains(p, "return the content")
}

func extractSingleFencedCodeBlock(text string) (string, bool) {
	start := strings.Index(text, "```")
	if start < 0 {
		return "", false
	}
	endRel := strings.Index(text[start+3:], "```")
	if endRel < 0 {
		return "", false
	}
	end := start + 3 + endRel + 3
	if strings.Contains(text[end:], "```") {
		return "", false
	}
	return strings.TrimSpace(text[start:end]), true
}

func extractLeadingNumberedList(text string) (string, bool) {
	lines := strings.Split(text, "\n")
	var out []string
	collecting := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if collecting {
				break
			}
			continue
		}
		if isNumberedListLine(trimmed) {
			collecting = true
			out = append(out, trimmed)
			continue
		}
		if collecting {
			break
		}
	}
	if len(out) < 2 {
		return "", false
	}
	return strings.Join(out, "\n"), true
}

func isNumberedListLine(line string) bool {
	if len(line) < 3 || line[1] != '.' || line[2] != ' ' {
		return false
	}
	return line[0] >= '0' && line[0] <= '9'
}

func formatLiteralFileReadReply(prompt, literal string) (string, bool) {
	lines, ok := normalizeLiteralLines(literal)
	if !ok || len(lines) == 0 {
		return "", false
	}
	pathLabel := "requested file"
	if path := extractPromptPath(prompt); path != "" {
		pathLabel = filepath.Base(path)
	}
	var b strings.Builder
	if n, ok := extractRequestedLineCount(prompt); ok {
		fmt.Fprintf(&b, "First %d lines of `%s`:\n\n", n, pathLabel)
	} else {
		fmt.Fprintf(&b, "Requested content from `%s`:\n\n", pathLabel)
	}
	for i, line := range lines {
		rendered := "(blank line)"
		if strings.TrimSpace(line) != "" {
			rendered = fmt.Sprintf("`%s`", line)
		}
		fmt.Fprintf(&b, "%d. %s", i+1, rendered)
		if i < len(lines)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String(), true
}

func normalizeLiteralLines(literal string) ([]string, bool) {
	literal = strings.TrimSpace(literal)
	if strings.HasPrefix(literal, "```") {
		body := literal[3:]
		if idx := strings.IndexByte(body, '\n'); idx >= 0 {
			body = body[idx+1:]
		}
		if end := strings.LastIndex(body, "```"); end >= 0 {
			body = body[:end]
		}
		body = strings.TrimRight(body, "\n")
		return strings.Split(body, "\n"), true
	}
	lines := strings.Split(literal, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !isNumberedListLine(trimmed) {
			return nil, false
		}
		item := strings.TrimSpace(trimmed[3:])
		switch item {
		case "(empty line)", "(blank line)":
			out = append(out, "")
		default:
			out = append(out, strings.Trim(item, "`"))
		}
	}
	return out, len(out) > 0
}

var (
	promptPathPattern       = regexp.MustCompile(`/[^\s` + "`" + `]+`)
	promptFirstLinesPattern = regexp.MustCompile(`(?i)first\s+(\d+)\s+lines?`)
)

func extractPromptPath(prompt string) string {
	return promptPathPattern.FindString(prompt)
}

func extractRequestedLineCount(prompt string) (int, bool) {
	m := promptFirstLinesPattern.FindStringSubmatch(prompt)
	if len(m) != 2 {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

func (st *SelfToolsTransport) callSubagentStatus(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if st.Subagent == nil {
		return mcp.ErrorResult("subagent service not configured"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, messageCallTimeout)
	defer cancel()
	run, err := st.Subagent.Status(ctx, strArg(args, "run_id", ""))
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("subagent status: %v", err)), nil
	}
	data, err := json.Marshal(run)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("subagent status marshal: %v", err)), nil
	}
	return mcp.TextResult(string(data)), nil
}

func (st *SelfToolsTransport) callSubagentCancel(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if st.Subagent == nil {
		return mcp.ErrorResult("subagent service not configured"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, messageCallTimeout)
	defer cancel()
	if err := st.Subagent.Cancel(ctx, strArg(args, "run_id", "")); err != nil {
		return mcp.ErrorResult(fmt.Sprintf("subagent cancel: %v", err)), nil
	}
	return mcp.TextResult("cancelled"), nil
}

// callSubagentRoleAudit returns a structured report of role slugs that
// produced orphan-reaped or config-error subagent runs
// (CW-20260519-0123 scope item 3). The report drives the
// fail-fast → audit → create operator workflow: roles appearing here
// without a registered profile are candidates for Phase-6 profile
// creation; roles appearing with a profile but with
// can_execute=false are candidates for either the text-only
// whitelist or tool-surface addition.
func (st *SelfToolsTransport) callSubagentRoleAudit(ctx context.Context, _ map[string]any) (*mcp.ToolResult, error) {
	if st.Subagent == nil {
		return mcp.ErrorResult("subagent service not configured"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, messageCallTimeout)
	defer cancel()
	entries, err := st.Subagent.AuditUnknownRoles(ctx)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("subagent role audit: %v", err)), nil
	}
	// Normalize a nil slice to an empty array so the JSON shape is
	// stable for callers (entries=[] rather than entries=null).
	if entries == nil {
		entries = []subagent.RoleAuditEntry{}
	}
	payload, err := json.Marshal(map[string]any{"entries": entries})
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("subagent role audit marshal: %v", err)), nil
	}
	return mcp.TextResult(string(payload)), nil
}

// --- background-job handlers (CW-20260420-0016) ---
//
// background_job dispatches the P3 PatternBackground gate
// before delegating to background.Service. Async by definition —
// returns immediately with a job_id; the result envelope arrives
// via messaging when the backend completes.

func (st *SelfToolsTransport) callBackgroundJob(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if st.Background == nil {
		return mcp.ErrorResult("background service not configured"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, messageCallTimeout)
	defer cancel()

	req := background.JobRequest{
		Agent:                strArg(args, "agent", ""),
		Task:                 strArg(args, "task", ""),
		OriginatingSessionID: strArg(args, "originating_session_id", ""),
		OriginatingAgentID:   strArg(args, "originating_agent_id", ""),
		Budget: background.JobBudget{
			WallClockSeconds: mcp.IntArg(args, "wall_clock_seconds", 0),
			MaxOutputBytes:   mcp.IntArg(args, "max_output_bytes", 0),
		},
	}
	// The MCP boundary always asserts PatternBackground here — the tool
	// is the gate's user-facing surface, so any caller invoking it has
	// classified the request as background-shaped (or is misusing the
	// tool). The Service-layer gate is still the authoritative check;
	// this just makes the boundary obvious to readers.
	id, err := st.Background.Submit(ctx, classify.PatternBackground, req)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("background submit: %v", err)), nil
	}
	out := map[string]any{"job_id": id}
	data, _ := json.Marshal(out)
	return mcp.TextResult(string(data)), nil
}

func (st *SelfToolsTransport) callBackgroundStatus(_ context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if st.Background == nil {
		return mcp.ErrorResult("background service not configured"), nil
	}
	jobID := strArg(args, "job_id", "")
	if jobID == "" {
		return mcp.ErrorResult("job_id is required"), nil
	}
	res, err := st.Background.Result(jobID)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("background status: %v", err)), nil
	}
	data, err := json.Marshal(res)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("background status marshal: %v", err)), nil
	}
	return mcp.TextResult(string(data)), nil
}

func (st *SelfToolsTransport) callBackgroundCancel(_ context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if st.Background == nil {
		return mcp.ErrorResult("background service not configured"), nil
	}
	jobID := strArg(args, "job_id", "")
	if jobID == "" {
		return mcp.ErrorResult("job_id is required"), nil
	}
	if err := st.Background.Cancel(jobID); err != nil {
		return mcp.ErrorResult(fmt.Sprintf("background cancel: %v", err)), nil
	}
	return mcp.TextResult("cancelled"), nil
}

// --- helpers ---

func strArg(args map[string]any, key, def string) string {
	v, ok := args[key].(string)
	if !ok || v == "" {
		return def
	}
	return v
}

// parseSourcesArg decodes the `sources` argument for card_show when
// type is report-card or document-viewer (the prose-bearing card types).
// Shape: JSON array of objects with at least a `tool_use_id` or `tool_name`
// field. Enforces presence + non-empty + basic per-entry shape — this is
// the cheap grounding check (CW-20260419-0022). The deep "tool_use_id was
// actually invoked this turn" check is layered on by validateSourcesAgainstTurn
// (CW-20260429-0024).
func parseSourcesArg(args map[string]any) ([]map[string]any, error) {
	raw, ok := args["sources"].(string)
	if !ok || raw == "" {
		return nil, fmt.Errorf("sources is required: pass a JSON array of objects like [{\"tool_use_id\":\"...\",\"tool_name\":\"...\"}] citing the tool calls whose results ground this card. If you did not fetch the data, do NOT render the card — respond in plain text instead")
	}
	var sources []map[string]any
	if err := json.Unmarshal([]byte(raw), &sources); err != nil {
		return nil, fmt.Errorf("invalid sources JSON: %v", err)
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf("sources must contain at least one entry — cite the tool calls whose results ground this card")
	}
	for i, s := range sources {
		id, _ := s["tool_use_id"].(string)
		name, _ := s["tool_name"].(string)
		if id == "" && name == "" {
			return nil, fmt.Errorf("sources[%d] must include tool_use_id or tool_name", i)
		}
	}
	return sources, nil
}

// validateSourcesAgainstTurn rejects any source whose tool_use_id is not in
// the set stamped onto ctx by service.executeToolBatch. The set being nil
// (subagent / test paths that don't stamp the context) skips the check —
// that's the explicit fallback contract for TurnToolUseIDsFromContext.
//
// This is the deep grounding check (CW-20260429-0024). The shallow shape
// check still runs in parseSourcesArg; this only fires when we have a
// known-good "what did this turn actually call" set to compare against.
//
// Per-entry rule: if a source carries tool_use_id, that ID must be in the
// turn set. If a source carries only tool_name (no tool_use_id), it's
// allowed through here — name-only validation is a separate concern (the
// ticket calls out tool_name registry validation as out of scope).
func validateSourcesAgainstTurn(ctx context.Context, sources []map[string]any) error {
	turnIDs := mcp.TurnToolUseIDsFromContext(ctx)
	if turnIDs == nil {
		// No ctx stamping — preserve existing behavior (tests, subagents).
		return nil
	}
	known := make(map[string]struct{}, len(turnIDs))
	for _, id := range turnIDs {
		known[id] = struct{}{}
	}
	for i, s := range sources {
		id, _ := s["tool_use_id"].(string)
		if id == "" {
			// tool_name-only entry; out of scope for this check.
			continue
		}
		if _, ok := known[id]; !ok {
			return fmt.Errorf(
				"source[%d].tool_use_id %q is not from this turn — known tool_use_ids: [%s]. "+
					"Build the sources array from real tool_use_ids you observed in this turn's tool_results; do not fabricate IDs",
				i, id, strings.Join(turnIDs, ", "))
		}
	}
	return nil
}

// --- python_run handler (CW-20260420-0019, D6) ---

// callRunPython handles the python_run self-tool. Runs Python code
// in an isolated subprocess sandbox with a dual-FD tool-call channel that
// routes through the permission engine on every tool invocation.
//
// Reach for the calling agent (Chat, Planner, Worker, or a custom agent
// profile) is governed by the agent profile's tool permissions and the
// dev-mode gate. There is no hard-coded role-based restriction at this
// dispatch site; the previous "Worker/Planner-only" framing was removed
// when the chat-surface enforcement was lifted.
func (st *SelfToolsTransport) callRunPython(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	code := strArg(args, "code", "")
	if code == "" {
		return mcp.ErrorResult("code is required"), nil
	}

	// Parse optional args object.
	var scriptArgs map[string]any
	if raw, ok := args["args"].(map[string]any); ok {
		scriptArgs = raw
	}

	timeLimitSec := mcp.IntArg(args, "time_limit_seconds", pythonSandboxDefaultTimeLimitSec)
	memLimitMB := mcp.IntArg(args, "memory_limit_mb", pythonSandboxDefaultMemLimitMB)

	// Resolve session ID from arg or context.
	sessionID := strArg(args, "session_id", "")
	if sessionID == "" {
		sessionID = mcp.SessionIDFromContext(ctx)
	}
	if sessionID == "" {
		sessionID = "ptc-default"
	}

	result, err := RunPythonSandbox(
		ctx,
		sessionID,
		code,
		scriptArgs,
		timeLimitSec,
		memLimitMB,
		st.PythonPermChecker,
		st.PythonDispatcher,
	)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("python sandbox: %v", err)), nil
	}

	out, jsonErr := json.Marshal(result)
	if jsonErr != nil {
		return mcp.ErrorResult(fmt.Sprintf("python sandbox: marshal result: %v", jsonErr)), nil
	}
	return mcp.TextResult(string(out)), nil
}
