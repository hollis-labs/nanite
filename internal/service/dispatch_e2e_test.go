package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/dispatch"
	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
)

// TestB3_EndToEnd_ChatSurfaceAndEnvelopeOnlyDispatch is the load-bearing
// end-to-end gate for B3 (CW-20260421-0010). It exercises the full
// seam:
//
//  1. A user message arrives at a Chat-role harness agent.
//  2. The Chat agent's resolved tool surface is clamped to the static
//     set {todos, plans, scratchpad, peer_query, narration,
//     execute_task} — work-execution tools are filtered out at boot.
//  3. The Chat agent invokes nanite_execute_task with the user message.
//  4. ScopeTier classifies → AssignRole returns a Worker role.
//  5. dispatchSpawner spawns the Worker via subagent.Service (NOT the
//     Chat surface).
//  6. The Worker's output is captured and wrapped in an envelope.
//  7. The Chat agent receives ONLY the envelope JSON — the raw Worker
//     text is reachable solely via the structured envelope payload
//     (Data["summary"]).
//
// The test uses fakes for the agent reader, prompt-template reader,
// subagent service, and message store so it does not require a real
// SQLite + provider stack.
func TestB3_EndToEnd_ChatSurfaceAndEnvelopeOnlyDispatch(t *testing.T) {
	chatAgentID := "file-default"
	workerSlug := dispatch.WorkerRoleSlug
	workerProseOutput := "I edited foo.go on line 42 to fix the typo. Two tests now pass."

	// --- Step 1: Chat agent surface enforcement ---
	//
	// Simulate the broker output (a mixed bag of allowed + rejected
	// tools), then run it through the same EnforceChatSurface call
	// site that toolServiceImpl.SelectForAgent uses.
	mixedTools := []provider.ToolDefinition{
		{Name: "nanite_todo_create"},
		{Name: "nanite_plan_list"},
		{Name: "nanite_scratchpad_write"},
		{Name: "nanite_message_send"},
		{Name: "nanite_show_report"},
		{Name: "nanite_execute_task"},
		// Rejected — Chat agent must NOT see these.
		{Name: "dev_read"},
		{Name: "dev_write"},
		{Name: "shell_exec"},
		{Name: "web_fetch"},
		{Name: "mcp__engine__task_create"},
		{Name: "nanite_spawn_subagent"},
	}

	tplReader := &fakePromptTemplateReader{
		byAgent: map[string][]store.PromptTemplate{
			chatAgentID: {
				{ID: dispatch.ChatHarnessTemplateID, Slug: dispatch.ChatHarnessTemplateSlug},
			},
		},
	}

	adapter := &promptTemplateAdapter{r: tplReader}
	isChat, err := dispatch.IsChatRoleAgent(adapter, chatAgentID)
	if err != nil {
		t.Fatalf("IsChatRoleAgent err = %v", err)
	}
	if !isChat {
		t.Fatalf("agent %q expected to be chat-role; harness template was assigned", chatAgentID)
	}

	chatSurface := dispatch.EnforceChatSurface(mixedTools)
	chatNames := toolNamesOf(chatSurface)

	// 1a. Static surface tools survived.
	wantSurface := []string{
		"nanite_todo_create",
		"nanite_plan_list",
		"nanite_scratchpad_write",
		"nanite_message_send",
		"nanite_show_report",
		"nanite_execute_task",
	}
	for _, n := range wantSurface {
		if !contains_e2e(chatNames, n) {
			t.Errorf("chat surface missing required tool %q", n)
		}
	}

	// 1b. Work-execution tools rejected — verifies the contract
	// "Chat dispatches; it does not execute."
	rejected := []string{"dev_read", "dev_write", "shell_exec", "web_fetch", "mcp__engine__task_create", "nanite_spawn_subagent"}
	for _, n := range rejected {
		if contains_e2e(chatNames, n) {
			t.Errorf("chat surface leaked work-execution tool %q (must NOT be on Chat surface)", n)
		}
	}

	// --- Step 2-5: nanite_execute_task → dispatch → spawn → envelope ---
	//
	// Build a self-tools transport with a fake subagent service that
	// simulates Worker completion. The Worker emits both prose (the
	// "raw assistant text" we must NOT leak into Chat context) and a
	// terminal ResultJSON envelope payload.
	workerEnvelopeJSON := `{"kind":"envelope","version":1,"type":"report-card","title":"Edit complete","data":{"file":"foo.go","tests_passing":2}}`

	subagentSvc := &fakeSubagentSpawner{
		runID: "run-b3-e2e",
		runByID: map[string]*subagent.Run{
			"run-b3-e2e": {
				ID:              "run-b3-e2e",
				ParentSessionID: "session-b3",
				ChildSessionID:  "child-b3",
				Role:            workerSlug, // sanity: should be the worker slug, not file-default
				Status:          subagent.StatusCompleted,
				ResultJSON:      workerEnvelopeJSON,
			},
		},
	}
	msgs := &fakeMessageReader{
		bySession: map[string][]store.Message{
			"child-b3": {
				// Latest assistant message in the child session — this is
				// the prose we must NOT leak into Chat's context window.
				{Role: "assistant", Content: `{"text":"` + workerProseOutput + `"}`},
			},
		},
	}

	transport := mcp.NewSelfToolsTransport(nil)
	transport.Dispatch = NewDispatchSpawner(subagentSvc, msgs)
	// DispatchWrapper left nil — transport falls back to default.

	// --- Step 3: Chat agent calls nanite_execute_task ---
	res, err := transport.CallTool(context.Background(), "nanite_execute_task", map[string]any{
		"session_id":      "session-b3",
		"parent_agent_id": chatAgentID,
		"message":         "fix the typo in foo.go and make the tests pass",
	})
	if err != nil {
		t.Fatalf("CallTool returned error: %v", err)
	}
	if res == nil {
		t.Fatal("CallTool returned nil result")
	}

	// --- Step 4-5 verifications: spawned with Worker slug ---
	if subagentSvc.gotSpawn.Role != workerSlug {
		t.Errorf("spawn Role = %q, want %q (Chat must dispatch to Worker, not invoke its own surface)", subagentSvc.gotSpawn.Role, workerSlug)
	}
	if subagentSvc.gotSpawn.Mode != subagent.ModeSync {
		t.Errorf("spawn Mode = %q, want %q (capture contract)", subagentSvc.gotSpawn.Mode, subagent.ModeSync)
	}
	if subagentSvc.gotSpawn.ParentSessionID != "session-b3" {
		t.Errorf("ParentSessionID = %q, want session-b3", subagentSvc.gotSpawn.ParentSessionID)
	}
	if subagentSvc.gotSpawn.ParentAgentID != chatAgentID {
		t.Errorf("ParentAgentID = %q, want %q", subagentSvc.gotSpawn.ParentAgentID, chatAgentID)
	}

	// --- Step 7: tool result is envelope JSON; NOT raw worker text ---
	toolText := toolResultText(t, res)
	if toolText == "" {
		t.Fatal("CallTool result has no text content")
	}

	// 7a. The result must parse as a valid envelope.
	var env dispatch.Envelope
	if err := json.Unmarshal([]byte(toolText), &env); err != nil {
		t.Fatalf("tool result is not valid envelope JSON: %v\n  raw: %s", err, toolText)
	}
	if env.Type == "" || env.Kind != "envelope" {
		t.Errorf("envelope shape invalid: %+v", env)
	}

	// 7b. The worker's prose output (the "raw assistant text" the spec
	// says must never enter Chat's context) must NOT appear at the top
	// level of the envelope JSON. It is reachable only inside the
	// structured Data payload — which is the design intent. The Chat
	// agent gets the envelope; if a downstream consumer wants the
	// prose it must explicitly fetch it from the structured field.
	//
	// The acceptance criterion phrasing: "raw worker output is NOT in
	// Chat's recorded context". The recorded context here is what the
	// LLM would see — the tool result string. Since the worker emitted
	// a structured envelope, the wrapper preferred it (per
	// DefaultEnvelopeWrapper); the prose lives only in the child
	// session's transcript, not the parent's. Verify the parent-side
	// payload doesn't carry the prose verbatim.
	if strings.Contains(toolText, workerProseOutput) {
		t.Errorf("envelope JSON contains raw worker prose %q — Chat context would be polluted by un-wrapped worker output", workerProseOutput)
	}

	// 7c. Sanity: the envelope's structured data carries what the
	// worker emitted (the report-card payload), proving the seam
	// preserved the worker's intended communication.
	if env.Type != "report-card" {
		t.Errorf("envelope type = %q, want report-card (worker's emitted shape)", env.Type)
	}
	if env.Title != "Edit complete" {
		t.Errorf("envelope title = %q, want %q", env.Title, "Edit complete")
	}
}

// toolNamesOf extracts the Name field from a slice of tool definitions.
func toolNamesOf(tools []provider.ToolDefinition) []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t.Name
	}
	return out
}

// toolResultText extracts the text content from an mcp.ToolResult.
func toolResultText(t *testing.T, r *mcp.ToolResult) string {
	t.Helper()
	if r == nil {
		return ""
	}
	for _, c := range r.Content {
		if c.Type == "text" {
			return c.Text
		}
	}
	return ""
}

func contains_e2e(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
