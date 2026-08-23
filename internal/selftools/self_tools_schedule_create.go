package selftools

// schedule_create is TASKS/scheduling/08-agent-self-tool.md's agent-facing
// producer -- docs/engineering/architecture/12-scheduling.md's "Producers"
// section, item 2: "a new nanite_*-namespaced self-tool ... letting an
// agent schedule its own follow-up work." Per docs/tool-naming-
// convention.md's <concept>_<verb> rule, the nanite_* reservation is a
// namespace-collision defense against MCP-origin tools, not a literal
// naming requirement for self-tools (see that doc's own nanite_code_execute
// as the one real exception, and TASKS/harness-reactive-self-tools/
// 07-worked-example-task-update-report.md's identical reasoning for
// task_update_report) -- so this tool is named schedule_create, matching
// the concept_verb convention every other current self-tool follows.
// Checked against docs/engineering/GLOSSARY.md before landing: no existing
// "schedule_*" tool-name term is defined there to collide with.
//
// Scope, deliberately narrow for v1: this tool only ever produces a
// job_type=durable_agent_wake row -- the only job type with a real
// production dispatch path today (internal/store/agent_schedules.go's own
// InsertAgentSchedule default, and docs/engineering/architecture/
// 12-scheduling.md's "Runner adapter and job taxonomy" table). job_type
// is NOT an input field on this tool's schema at all -- letting an agent
// freely choose agent_workflow_run/command_run/reflex_dispatch as its own
// job_type would let it schedule arbitrary workflow launches, commands, or
// reflex applications against itself on a timer, a broader, unreviewed
// capability-surface question this task's own Context explicitly declines
// to open ("this task shouldn't silently expand into"). A future task that
// wants to open that up should do so deliberately, with its own review of
// the sandboxing/permission implications 12-scheduling.md's "What this
// session did not decide" already flags as open.
//
// max_retries/on_fail are similarly not agent-input fields: this handler
// hardcodes them (see scheduleCreateMaxRetries/scheduleCreateOnFail below)
// rather than exposing retry-policy knobs on day one, keeping the input
// surface to exactly what a "schedule my own follow-up" use case needs.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/store"
)

// scheduleCreateToolName is schedule_create's own registered name --
// shared between the tool definition and the CallTool dispatch case so
// both stay in lock-step by construction rather than by two independently
// typed string literals (mirrors taskUpdateReportToolName's pattern in
// self_tools_task_update_report.go).
const scheduleCreateToolName = "schedule_create"

// scheduleCreateMaxRetries/scheduleCreateOnFail are this tool's hardcoded
// retry-policy defaults for every row it inserts -- a deliberate,
// documented divergence from InsertAgentSchedule's own column defaults
// (MaxRetries=3, OnFail="retry", internal/store/agent_schedules.go).
//
// MaxRetries stays at the same budget (3) as every other producer's
// default -- no reason a self-authored follow-up needs a smaller retry
// budget than an operator- or YAML-authored one.
//
// OnFail diverges to "disable" instead of the "retry" column default.
// Per internal/scheduler/retrying_runner.go's applyOnFail (TASKS/
// scheduling/04-retry-backoff-on-fail-policy.md), on_fail="retry" is not
// itself a meaningful terminal action once max_retries is exhausted -- it
// falls through to the exact same non-disabling "log and keep going"
// behavior as on_fail="notify" (see that file's own doc comment: "'retry'
// is not itself a meaningful terminal action once max_retries is
// exhausted... resolves to behave identically to 'notify' at exhaustion:
// log/emit, do not disable"). That is a reasonable default for a
// human-reviewed, operator- or YAML-authored schedule, but a schedule an
// agent created for itself, with no human review in the loop, should not
// be allowed to keep silently failing forever on an "active" row nobody's
// watching. on_fail="disable" makes an exhausted self-schedule visibly
// terminal (status flips to "expired" via DisableSchedule/
// UpdateAgentScheduleStatus, discoverable via ListAgentSchedules/
// GetAgentSchedule) instead of indefinitely retryable-but-silently-broken.
const (
	scheduleCreateMaxRetries = 3
	scheduleCreateOnFail     = store.ScheduleOnFailDisable
)

// scheduleCreateNameMaxRunes bounds the auto-derived default Name (when the
// caller omits one) so a very long message doesn't produce an unwieldy
// schedule label.
const scheduleCreateNameMaxRunes = 60

// scheduleCreateToolDefinition declares schedule_create: kind (cron |
// one_shot) and message are required; cron_expr is required exactly when
// kind="cron"; name is optional. Description follows the existing
// When-to-use / When-NOT-to-use / Required-context / Output-shape house
// style read off self_tools_whoami.go and self_tools_task_update_report.go
// before writing this one.
func scheduleCreateToolDefinition() mcp.Tool {
	return mcp.Tool{
		Name: scheduleCreateToolName,
		Description: "Schedule a durable follow-up wake for YOURSELF, so the harness re-invokes you later with a reminder prompt -- without you needing to stay running, or a human needing to remember to check back.\n\n" +
			"**When to use:** You want to be woken up again at a future time -- a recurring cron cadence, or as soon as the scheduler's next tick -- with a specific prompt describing what to pick up. Use for self-initiated follow-ups, e.g. \"check back on this in the morning\" or \"run this audit every day at 9am.\"\n\n" +
			"**When NOT to use:** This does NOT schedule a workflow launch, a raw command, or a reflex on a timer -- it only ever produces a durable_agent_wake job targeting your own durable-agent instance; job_type is fixed, not a field you can set. It does NOT support an arbitrary delayed one-time target: kind=\"one_shot\" fires on the scheduler engine's very next tick (effectively \"as soon as possible\"), not at a chosen future moment -- use kind=\"cron\" with a specific future cron expression for a delayed one-off wake. It cannot schedule a wake for a DIFFERENT agent -- the schedule is always scoped to whichever agent is making this call; there is no agent_id field to override that.\n\n" +
			"**Required context:** `kind` (\"cron\" or \"one_shot\") and `message` (the follow-up prompt you'll be woken up with -- write it as a note to your future self describing what to do). `cron_expr` is required when kind=\"cron\" (standard 5-field cron syntax, e.g. \"0 9 * * *\" for 9am daily; @hourly/@daily/@weekly/@monthly/@yearly/@every <duration> descriptors are also accepted) and must be omitted when kind=\"one_shot\". `name` is optional and defaults to a short label derived from `message`. Only callable from a real agent session (an in-process turn, or a session with a resolvable primary agent) -- there is no anonymous/unscoped caller.\n\n" +
			"**Output shape:** A short text confirmation naming the new schedule's id, kind, and computed next-fire time (RFC3339, UTC).",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"kind": map[string]any{
					"type":        "string",
					"enum":        []string{store.ScheduleKindCron, store.ScheduleKindOneShot},
					"description": "\"cron\" for a recurring wake on a cron cadence, or \"one_shot\" to fire on the scheduler engine's next tick (effectively immediately).",
				},
				"cron_expr": map[string]any{
					"type":        "string",
					"description": "Standard 5-field cron expression (minute hour day-of-month month day-of-week), e.g. \"0 9 * * *\". Also accepts @hourly/@daily/@weekly/@monthly/@yearly/@every <duration>. Required when kind=\"cron\"; must be omitted when kind=\"one_shot\".",
				},
				"message": map[string]any{
					"type":        "string",
					"description": "The follow-up prompt you'll be woken up with -- a note to your future self describing what to do when this fires.",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "Optional human-readable label for the schedule. Defaults to a short label derived from message.",
				},
			},
			"required": []string{"kind", "message"},
		},
	}
}

// callScheduleCreate is schedule_create's handler: validate kind/cron_expr/
// message, resolve the calling agent's own agent_id from ctx (never from an
// input field -- see resolveSelfScheduleAgentID), compute next_run via
// store.ComputeAgentScheduleNextRun (the same helper TASKS/scheduling/
// 02-store-adapter.md's Store adapter and 01's backfill use -- not a second
// hand-rolled cron-math implementation, per this repo's own standing
// instruction, restated in TASKS/ESCALATIONS.md's 2026-08-20 entry on
// task 05's minor finding), and insert the row scoped to that agent.
func (st *SelfToolsTransport) callScheduleCreate(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	kind := strArg(args, "kind", "")

	message := strings.TrimSpace(strArg(args, "message", ""))
	if message == "" {
		return mcp.ErrorResult("schedule_create: message is required"), nil
	}

	cronExpr := strings.TrimSpace(strArg(args, "cron_expr", ""))
	if kind == store.ScheduleKindOneShot {
		if cronExpr != "" {
			return mcp.ErrorResult("schedule_create: cron_expr must be omitted when kind=\"one_shot\" (one_shot fires on the scheduler engine's next tick; there is no delayed-target-time field in this schema today)"), nil
		}
	}

	name := strings.TrimSpace(strArg(args, "name", ""))
	if name == "" {
		name = defaultScheduleCreateName(message)
	}

	agentID, err := st.resolveSelfScheduleAgentID(ctx)
	if err != nil {
		return mcp.ErrorResult("schedule_create: " + err.Error()), nil
	}
	if st.Store == nil {
		return mcp.ErrorResult("schedule_create: store not configured"), nil
	}

	row := store.AgentSchedule{
		ID:           "self-sched-" + ulid.Make().String(),
		AgentID:      agentID,
		Name:         name,
		ScheduleKind: kind,
		ScheduleSpec: cronExpr,
		Body:         message,
		Status:       store.ScheduleStatusActive,
		CreatedBy:    "self:" + agentID,
		MaxRetries:   scheduleCreateMaxRetries,
		OnFail:       scheduleCreateOnFail,
		JobType:      store.ScheduleJobTypeDurableAgentWake,
	}
	if err := store.ValidateAgentSchedule(row); err != nil {
		return mcp.ErrorResult("schedule_create: " + err.Error()), nil
	}
	nextRun := store.ComputeAgentScheduleNextRun(kind, cronExpr, time.Now().UTC())
	if nextRun.IsZero() {
		return mcp.ErrorResult("schedule_create: could not compute next_run for the given kind/cron_expr"), nil
	}
	row.NextRun = nextRun.Format(time.RFC3339)
	if err := st.Store.InsertAgentSchedule(ctx, row); err != nil {
		return mcp.ErrorResult(fmt.Sprintf("schedule_create: %v", err)), nil
	}

	return mcp.TextResult(fmt.Sprintf(
		"Scheduled self follow-up %q (id=%s, kind=%s, next_run=%s)",
		name, row.ID, kind, row.NextRun,
	)), nil
}

// resolveSelfScheduleAgentID resolves the CALLING agent's own agent_id from
// ctx -- the enforcement point that makes it impossible to schedule a job
// against a different agent's agent_id via this tool (there is no agent_id
// input field for a caller to even attempt to supply; the schema declared
// above has none).
//
// Two resolution paths, tried in order, both already-established
// identity-from-ctx mechanisms in this codebase -- neither invented for
// this task:
//
//  1. mcp.CallerProfileFromContext -- the H1 trust-gate stamp
//     (chat_tool_executor.go's executeToolBatch) set for every tool call
//     made from the in-process chat-loop turn executor. This is the same
//     mechanism self_tools_whoami.go's executeWhoami relies on as "the
//     calling agent's identity."
//
//  2. mcp.SessionIDFromContext -> store.GetSessionPrimaryAgent -- a
//     fallback for the path whoami's own mechanism does NOT cover: a
//     CLI-launched durable agent's `nanite mcp` subprocess forwards self-
//     tool calls through internal/mcpserver/self_proxy.go to
//     POST /api/tools/call (internal/api/tools_call.go's handleSelfToolCall),
//     which stamps only mcp.WithSessionID(ctx, req.SessionID) -- no caller-
//     profile field exists on that request shape at all (confirmed by
//     reading tools_call.go/self_proxy.go directly before writing this
//     function). A durable agent asking to schedule its own wake is
//     exactly the primary expected caller of this tool, so relying solely
//     on path (1) would silently exclude the main use case. session_agents
//     already carries an authoritative "primary agent for this session"
//     row for every durable-agent session (durable_agents.go's
//     selectOrCreateLaunchSession calls EnsureSessionAgent(sess.ID,
//     inst.ProfileID, "default", true) at session-creation time), and
//     store.GetSessionPrimaryAgent is the existing, widely-used accessor
//     for it (internal/service/agent.go's resolveBinding,
//     internal/chat/commands_builtin.go, internal/api/agents.go,
//     internal/api/frontend_readiness.go all already read it the same
//     way) -- not a new resolution path invented for this task.
//
// A caller with neither an H1 stamp nor a session ID (e.g. a bare unit
// test ctx, or some future transport this codebase doesn't have yet) gets
// a clear rejection rather than an empty/blank agent_id silently reaching
// InsertAgentSchedule.
func (st *SelfToolsTransport) resolveSelfScheduleAgentID(ctx context.Context) (string, error) {
	if id := mcp.CallerProfileFromContext(ctx); id != "" {
		return id, nil
	}
	sessionID := mcp.SessionIDFromContext(ctx)
	if sessionID == "" {
		return "", errors.New("no calling-agent identity in context (neither an H1 caller profile nor a session id is stamped) -- not callable outside a real agent session")
	}
	if st.Store == nil {
		return "", errors.New("store not configured")
	}
	sa, err := st.Store.GetSessionPrimaryAgent(ctx, sessionID)
	if err != nil {
		return "", fmt.Errorf("resolve calling agent from session %s: %w", sessionID, err)
	}
	if sa.AgentID == "" {
		return "", fmt.Errorf("session %s has no primary agent", sessionID)
	}
	return sa.AgentID, nil
}

// defaultScheduleCreateName derives a short label from message when the
// caller omits name -- a rune-safe truncation (message may contain
// multi-byte characters) rather than a byte-index slice.
func defaultScheduleCreateName(message string) string {
	const prefix = "Self-scheduled follow-up: "
	r := []rune(message)
	if len(r) > scheduleCreateNameMaxRunes {
		return prefix + string(r[:scheduleCreateNameMaxRunes]) + "…"
	}
	return prefix + message
}
