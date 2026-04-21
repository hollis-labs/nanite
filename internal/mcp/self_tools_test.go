package mcp

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	tmp := t.TempDir()
	dbPath := tmp + "/test.db"
	s, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close(); os.Remove(dbPath) })
	return s
}

func newSelfTools(t *testing.T) *SelfToolsTransport {
	t.Helper()
	s := newTestStore(t)
	return NewSelfToolsTransport(s)
}

// TestSelfToolsTransport_ListTools verifies all expected tools are returned.
func TestSelfToolsTransport_ListTools(t *testing.T) {
	st := newSelfTools(t)
	tools, err := st.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	expected := map[string]bool{
		"nanite_create_skill":     false,
		"nanite_list_skills":      false,
		"nanite_update_skill":     false,
		"nanite_delete_skill":     false,
		"nanite_create_agent":     false,
		"nanite_list_agents":      false,
		"nanite_update_agent":     false,
		"nanite_start_builder":    false,
		"nanite_builder_step":     false,
		"nanite_install_home":     false,
		"nanite_install_project":  false,
		"nanite_install_rollback": false,
		"nanite_install_diff":     false,
	}

	for _, tool := range tools {
		if _, ok := expected[tool.Name]; ok {
			expected[tool.Name] = true
		}
	}

	for name, found := range expected {
		if !found {
			t.Errorf("missing tool: %s", name)
		}
	}

	if len(tools) < len(expected) {
		t.Errorf("expected at least %d tools, got %d", len(expected), len(tools))
	}
}

// TestSelfToolsTransport_CreateSkill tests the round-trip create and verify.
func TestSelfToolsTransport_CreateSkill(t *testing.T) {
	st := newSelfTools(t)
	ctx := context.Background()

	// Create a skill.
	result, err := st.CallTool(ctx, "nanite_create_skill", map[string]any{
		"name":        "Test Skill",
		"slug":        "test-skill",
		"description": "A test skill for unit testing",
		"category":    "testing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, "Created skill") {
		t.Errorf("expected creation confirmation, got: %s", result.Content[0].Text)
	}

	// List skills and verify it appears.
	listResult, err := st.CallTool(ctx, "nanite_list_skills", map[string]any{
		"category": "testing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if listResult.IsError {
		t.Fatalf("unexpected error: %s", listResult.Content[0].Text)
	}
	if !strings.Contains(listResult.Content[0].Text, "Test Skill") {
		t.Errorf("expected skill in list, got: %s", listResult.Content[0].Text)
	}
	if !strings.Contains(listResult.Content[0].Text, "test-skill") {
		t.Errorf("expected slug in list, got: %s", listResult.Content[0].Text)
	}
}

// TestSelfToolsTransport_ListSkills verifies filtering by category.
func TestSelfToolsTransport_ListSkills(t *testing.T) {
	st := newSelfTools(t)
	ctx := context.Background()

	// Create two skills in different categories.
	st.CallTool(ctx, "nanite_create_skill", map[string]any{
		"name": "Skill A", "slug": "skill-a", "description": "cat-x skill", "category": "cat-x",
	})
	st.CallTool(ctx, "nanite_create_skill", map[string]any{
		"name": "Skill B", "slug": "skill-b", "description": "cat-y skill", "category": "cat-y",
	})

	// List all.
	allResult, _ := st.CallTool(ctx, "nanite_list_skills", map[string]any{})
	if !strings.Contains(allResult.Content[0].Text, "Skill A") || !strings.Contains(allResult.Content[0].Text, "Skill B") {
		t.Errorf("expected both skills, got: %s", allResult.Content[0].Text)
	}

	// Filter by cat-x.
	filteredResult, _ := st.CallTool(ctx, "nanite_list_skills", map[string]any{"category": "cat-x"})
	if !strings.Contains(filteredResult.Content[0].Text, "Skill A") {
		t.Errorf("expected Skill A, got: %s", filteredResult.Content[0].Text)
	}
	if strings.Contains(filteredResult.Content[0].Text, "Skill B") {
		t.Errorf("did not expect Skill B, got: %s", filteredResult.Content[0].Text)
	}
}

// TestSelfToolsTransport_CreateAgent tests agent creation round-trip.
func TestSelfToolsTransport_CreateAgent(t *testing.T) {
	st := newSelfTools(t)
	ctx := context.Background()

	result, err := st.CallTool(ctx, "nanite_create_agent", map[string]any{
		"name":          "Test Agent",
		"slug":          "test-agent",
		"system_prompt": "You are a helpful test agent.",
		"description":   "For testing purposes",
		"default_model": "claude-3-haiku",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, "Created agent") {
		t.Errorf("expected creation confirmation, got: %s", result.Content[0].Text)
	}

	// List agents and verify.
	listResult, err := st.CallTool(ctx, "nanite_list_agents", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if listResult.IsError {
		t.Fatalf("unexpected error: %s", listResult.Content[0].Text)
	}
	if !strings.Contains(listResult.Content[0].Text, "Test Agent") {
		t.Errorf("expected agent in list, got: %s", listResult.Content[0].Text)
	}
	if !strings.Contains(listResult.Content[0].Text, "test-agent") {
		t.Errorf("expected slug in list, got: %s", listResult.Content[0].Text)
	}
}

// TestSelfToolsTransport_CreateSkill_MissingFields verifies validation.
func TestSelfToolsTransport_CreateSkill_MissingFields(t *testing.T) {
	st := newSelfTools(t)
	ctx := context.Background()

	result, _ := st.CallTool(ctx, "nanite_create_skill", map[string]any{
		"name": "Only Name",
	})
	if !result.IsError {
		t.Fatal("expected error for missing required fields")
	}
}

// TestSelfToolsTransport_DeleteSkill tests skill deletion.
func TestSelfToolsTransport_DeleteSkill(t *testing.T) {
	st := newSelfTools(t)
	ctx := context.Background()

	// Create a skill first.
	createResult, _ := st.CallTool(ctx, "nanite_create_skill", map[string]any{
		"name": "To Delete", "slug": "to-delete", "description": "Will be deleted",
	})
	if createResult.IsError {
		t.Fatalf("create failed: %s", createResult.Content[0].Text)
	}

	// Extract the ID from the list.
	skills, _ := st.Store.ListSkills()
	var skillID string
	for _, sk := range skills {
		if sk.Slug == "to-delete" {
			skillID = sk.ID
			break
		}
	}
	if skillID == "" {
		t.Fatal("could not find created skill")
	}

	// Delete it.
	delResult, _ := st.CallTool(ctx, "nanite_delete_skill", map[string]any{"id": skillID})
	if delResult.IsError {
		t.Fatalf("delete failed: %s", delResult.Content[0].Text)
	}

	// Verify it's gone.
	sk, _ := st.Store.GetSkill(skillID)
	if sk != nil {
		t.Error("skill should have been deleted")
	}
}

// TestSelfToolsTransport_UnknownTool verifies unknown tool returns error.
func TestSelfToolsTransport_UnknownTool(t *testing.T) {
	st := newSelfTools(t)
	result, _ := st.CallTool(context.Background(), "nonexistent_tool", map[string]any{})
	if !result.IsError {
		t.Fatal("expected error for unknown tool")
	}
}

// countingBroadcaster tracks BroadcastWorkChanged calls for tests.
type countingBroadcaster struct{ calls int }

func (b *countingBroadcaster) BroadcastWorkChanged() { b.calls++ }

// TestSelfToolsTransport_WorkBroadcast verifies that mutating todo/plan
// tools fire BroadcastWorkChanged on success, and that read-only tools do
// not. CW-20260418-0044.
func TestSelfToolsTransport_WorkBroadcast(t *testing.T) {
	st := newSelfTools(t)
	st.TodoStore = st.Store
	b := &countingBroadcaster{}
	st.Work = b
	ctx := context.Background()

	// Create todo → 1 broadcast.
	r, err := st.CallTool(ctx, "nanite_todo_create", map[string]any{
		"title": "wire test", "scope": "session", "scope_id": "sess-wire",
	})
	if err != nil || r.IsError {
		t.Fatalf("todo_create failed: %v / %s", err, r.Content[0].Text)
	}
	if b.calls != 1 {
		t.Fatalf("expected 1 broadcast after todo_create, got %d", b.calls)
	}

	// List is read-only — no broadcast.
	if _, err := st.CallTool(ctx, "nanite_todo_list", map[string]any{"scope": "session"}); err != nil {
		t.Fatal(err)
	}
	if b.calls != 1 {
		t.Fatalf("expected 1 broadcast after read-only list, got %d", b.calls)
	}

	// Create plan → 2 broadcasts.
	if _, err := st.CallTool(ctx, "nanite_plan_create", map[string]any{
		"title": "wire plan", "scope": "session", "scope_id": "sess-wire",
	}); err != nil {
		t.Fatal(err)
	}
	if b.calls != 2 {
		t.Fatalf("expected 2 broadcasts after plan_create, got %d", b.calls)
	}

	plans, _ := st.Store.ListPlans(store.PlanFilter{Scope: "session", ScopeID: "sess-wire"})
	if len(plans) != 1 {
		t.Fatalf("expected 1 plan, got %d", len(plans))
	}

	// Delete plan → 3 broadcasts.
	if _, err := st.CallTool(ctx, "nanite_plan_delete", map[string]any{"id": plans[0].ID}); err != nil {
		t.Fatal(err)
	}
	if b.calls != 3 {
		t.Fatalf("expected 3 broadcasts after plan_delete, got %d", b.calls)
	}
}

// TestSelfToolsTransport_TodoListEmitsEnvelope verifies that nanite_todo_list
// emits a todo-list envelope carrying the scope/scope_id the caller filtered
// on, so TodoListCard can lazy-fetch correctly. CW-20260418-0045.
func TestSelfToolsTransport_TodoListEmitsEnvelope(t *testing.T) {
	st := newSelfTools(t)
	st.TodoStore = st.Store
	ctx := context.Background()

	// With scope: envelope must appear and carry scope + scope_id.
	r, err := st.CallTool(ctx, "nanite_todo_list", map[string]any{
		"scope":    "session",
		"scope_id": "sess-env",
		"title":    "Session Todos",
	})
	if err != nil || r.IsError {
		t.Fatalf("todo_list failed: %v / %s", err, r.Content[0].Text)
	}
	body := r.Content[0].Text
	if !strings.Contains(body, "<!--ENVELOPE_DATA:") {
		t.Fatalf("expected envelope marker in output, got: %s", body)
	}
	if !strings.Contains(body, `"type":"todo-list"`) {
		t.Fatalf("expected todo-list envelope type, got: %s", body)
	}
	if !strings.Contains(body, `"scope":"session"`) || !strings.Contains(body, `"scope_id":"sess-env"`) {
		t.Fatalf("envelope missing scope coordinates: %s", body)
	}
	if !strings.Contains(body, `"title":"Session Todos"`) {
		t.Fatalf("envelope missing title: %s", body)
	}

	// Without scope: no envelope (would render an un-scoped card).
	r2, _ := st.CallTool(ctx, "nanite_todo_list", map[string]any{})
	if strings.Contains(r2.Content[0].Text, "<!--ENVELOPE_DATA:") {
		t.Fatalf("expected no envelope when scope is empty, got: %s", r2.Content[0].Text)
	}
}

// TestSelfToolsTransport_PlanCreate_AutoFillsSessionIDFromCtx verifies the
// CW-20260418 c7 fix: when the agent omits scope_id and ctx carries a
// session id, the handler fills it in automatically so the record lands
// with the real session UUID (not empty string, which the Work drawer
// filter never matches).
func TestSelfToolsTransport_PlanCreate_AutoFillsSessionIDFromCtx(t *testing.T) {
	st := newSelfTools(t)
	st.TodoStore = st.Store
	ctx := WithSessionID(context.Background(), "ctx-session-xyz")

	r, err := st.CallTool(ctx, "nanite_plan_create", map[string]any{
		"title": "ctx autofill",
		"scope": "session",
		// scope_id intentionally omitted
	})
	if err != nil || r.IsError {
		t.Fatalf("plan_create failed: %v / %s", err, r.Content[0].Text)
	}
	plans, _ := st.Store.ListPlans(store.PlanFilter{Scope: "session", ScopeID: "ctx-session-xyz"})
	if len(plans) != 1 {
		t.Fatalf("expected 1 plan with ctx scope_id, got %d", len(plans))
	}
}

// TestSelfToolsTransport_PlanCreate_ErrorsWithoutSessionID verifies that when
// scope_id is omitted AND ctx has no session id, plan_create errors out
// rather than silently writing an empty scope_id.
func TestSelfToolsTransport_PlanCreate_ErrorsWithoutSessionID(t *testing.T) {
	st := newSelfTools(t)
	st.TodoStore = st.Store

	r, _ := st.CallTool(context.Background(), "nanite_plan_create", map[string]any{
		"title": "no scope_id",
		"scope": "session",
	})
	if !r.IsError {
		t.Fatalf("expected error result when scope_id is missing and ctx has no session")
	}
}

func TestSelfToolDefinitions_ScratchpadToolsPresent(t *testing.T) {
	defs := selfToolDefinitions()
	names := make(map[string]bool, len(defs))
	for _, d := range defs {
		names[d.Name] = true
	}
	for _, want := range []string{
		"nanite_scratchpad_write",
		"nanite_scratchpad_read",
		"nanite_scratchpad_clear",
	} {
		if !names[want] {
			t.Errorf("tool %q missing from selfToolDefinitions()", want)
		}
	}
}

func TestScratchpadToolDescriptions_RequiredSections(t *testing.T) {
	defs := selfToolDefinitions()
	for _, d := range defs {
		switch d.Name {
		case "nanite_scratchpad_write", "nanite_scratchpad_read", "nanite_scratchpad_clear":
			if !strings.Contains(d.Description, "When to use") {
				t.Errorf("%s description missing 'When to use' section", d.Name)
			}
			if !strings.Contains(d.Description, "When NOT to use") {
				t.Errorf("%s description missing 'When NOT to use' section", d.Name)
			}
			if !strings.Contains(d.Description, "Output shape") {
				t.Errorf("%s description missing 'Output shape' section", d.Name)
			}
		}
	}
}

// TestSelfToolsTransport_PlanCRUD exercises the full plan lifecycle via the
// self-service tools (create → list → get → delete).
func TestSelfToolsTransport_PlanCRUD(t *testing.T) {
	st := newSelfTools(t)
	st.TodoStore = st.Store
	ctx := context.Background()

	createResult, err := st.CallTool(ctx, "nanite_plan_create", map[string]any{
		"title":       "UAT Plan",
		"scope":       "session",
		"scope_id":    "sess-1",
		"description": "plan created for UAT",
	})
	if err != nil || createResult.IsError {
		t.Fatalf("plan_create failed: %v / %s", err, createResult.Content[0].Text)
	}

	plans, err := st.Store.ListPlans(store.PlanFilter{Scope: "session", ScopeID: "sess-1"})
	if err != nil || len(plans) != 1 {
		t.Fatalf("expected 1 plan after create, got %d (err=%v)", len(plans), err)
	}
	planID := plans[0].ID

	listResult, _ := st.CallTool(ctx, "nanite_plan_list", map[string]any{
		"scope":    "session",
		"scope_id": "sess-1",
	})
	if listResult.IsError || !strings.Contains(listResult.Content[0].Text, "UAT Plan") {
		t.Fatalf("plan_list did not surface plan: %s", listResult.Content[0].Text)
	}

	getResult, _ := st.CallTool(ctx, "nanite_plan_get", map[string]any{"id": planID})
	if getResult.IsError || !strings.Contains(getResult.Content[0].Text, planID) {
		t.Fatalf("plan_get did not return plan: %s", getResult.Content[0].Text)
	}

	delResult, _ := st.CallTool(ctx, "nanite_plan_delete", map[string]any{"id": planID})
	if delResult.IsError {
		t.Fatalf("plan_delete failed: %s", delResult.Content[0].Text)
	}

	plans, _ = st.Store.ListPlans(store.PlanFilter{Scope: "session", ScopeID: "sess-1"})
	if len(plans) != 0 {
		t.Fatalf("expected 0 plans after delete, got %d", len(plans))
	}
}
