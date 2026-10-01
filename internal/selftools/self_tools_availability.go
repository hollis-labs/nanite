package selftools

import (
	"context"

	"github.com/hollis-labs/nanite/internal/mcp"
)

// selfToolNeeds maps each self tool whose handler depends on a collaborator
// to a check that the collaborator is wired. NewSelfToolsTransport leaves
// these collaborators nil; cmd/nanite wires them after the service container
// exists. A transport that never gets them — `nanite mcp` serving a bare
// store, with no NANITE_API_URL to forward to — answers every call to these
// tools with a "… not configured" error (CW-20261001-0017).
//
// Each check mirrors the guard at the top of the tool's handler. A tool
// absent from this table needs nothing beyond the store. Keep the two in
// step; TestHideUnwired_MatchesHandlerGuards fails when they drift.
var selfToolNeeds = map[string]func(*SelfToolsTransport) bool{
	"message_send":     hasMessaging,
	"message_inbox":    hasMessaging,
	"message_thread":   hasMessaging,
	"message_ack":      hasMessaging,
	"message_resolve":  hasMessaging,
	"message_catch_up": hasMessaging,
	"handoff_request":  hasMessaging,
	"handoff_approve":  hasMessaging,
	"handoff_reject":   hasMessaging,

	"todo_create":   hasWorkTracking,
	"todo_update":   hasWorkTracking,
	"todo_list":     hasWorkTracking,
	"plan_create":   hasWorkTracking,
	"plan_update":   hasWorkTracking,
	"plan_step_add": hasWorkTracking,
	"plan_list":     hasWorkTracking,
	"plan_get":      hasWorkTracking,
	"plan_delete":   hasWorkTracking,

	"subagent_spawn":      func(st *SelfToolsTransport) bool { return st.Subagent != nil },
	"subagent_status":     func(st *SelfToolsTransport) bool { return st.Subagent != nil },
	"subagent_cancel":     func(st *SelfToolsTransport) bool { return st.Subagent != nil },
	"subagent_role_audit": func(st *SelfToolsTransport) bool { return st.Subagent != nil },

	"background_job":    func(st *SelfToolsTransport) bool { return st.Background != nil },
	"background_status": func(st *SelfToolsTransport) bool { return st.Background != nil },
	"background_cancel": func(st *SelfToolsTransport) bool { return st.Background != nil },

	"workflow_execute_llm_step":  func(st *SelfToolsTransport) bool { return st.WorkflowExecutor != nil },
	"workflow_execute_tool_step": func(st *SelfToolsTransport) bool { return st.WorkflowExecutor != nil },
	"workflow_verify_step":       func(st *SelfToolsTransport) bool { return st.WorkflowExecutor != nil },
	"workflow_run":               func(st *SelfToolsTransport) bool { return st.WorkflowLauncher != nil },

	"task_execute":      func(st *SelfToolsTransport) bool { return st.Dispatch != nil },
	"dispatch_executor": func(st *SelfToolsTransport) bool { return st.Executor != nil },
	"skill_get":         func(st *SelfToolsTransport) bool { return st.SkillVendor != nil },
	"lesson_capture":    func(st *SelfToolsTransport) bool { return st.LearningRecorder != nil },

	// Served by the in-process chat loop, which intercepts them before they
	// reach any transport; CallTool only ever answers them with an error.
	"scratchpad_write": neverOnTransport,
	"scratchpad_read":  neverOnTransport,
	"scratchpad_clear": neverOnTransport,
}

func hasMessaging(st *SelfToolsTransport) bool {
	return st.MessagingTools != nil && st.MessagingTools.Service != nil
}

func hasWorkTracking(st *SelfToolsTransport) bool {
	return st.WorkTrackingTools != nil && st.WorkTrackingTools.Store != nil
}

func neverOnTransport(*SelfToolsTransport) bool { return false }

// toolWired reports whether name's handler has the collaborators it needs.
func (st *SelfToolsTransport) toolWired(name string) bool {
	need, ok := selfToolNeeds[name]
	return !ok || need(st)
}

// BareStoreTools returns the self tools a transport over a bare store lists
// with HideUnwired set: what `nanite mcp` advertises when it dispatches
// locally, with none of the harness's collaborators wired. Every check in
// selfToolNeeds reads a collaborator NewSelfToolsTransport leaves unwired,
// never the store itself, so no store is needed to compute it.
//
// A forwarding `nanite mcp` for a launch that is not a chat agent advertises
// exactly this set (CW-20261001-0188): such launches used to dispatch
// locally, and routing their calls through the live harness must not give
// them a tool they did not already have.
func BareStoreTools() []mcp.Tool {
	tools, _ := (&SelfToolsTransport{HideUnwired: true}).ListTools(context.Background())
	return tools
}

// HiddenTools names the tools ListTools leaves out because their
// collaborators are not wired. It is empty unless HideUnwired is set.
func (st *SelfToolsTransport) HiddenTools() []string {
	if !st.HideUnwired {
		return nil
	}
	var hidden []string
	for _, t := range selfToolDefinitions() {
		if !st.toolWired(t.Name) {
			hidden = append(hidden, t.Name)
		}
	}
	return hidden
}
