package selftools

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	tmp := t.TempDir()
	dbPath := tmp + "/test.db"
	s, err := store.New(context.Background(), dbPath)
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
		"skill_list":      false,
		"skill_delete":    false,
		"agent_create":    false,
		"agent_list":      false,
		"agent_update":    false,
		"builder_start":   false,
		"builder_step":    false,
		"install_home":    false,
		"install_project": false,
		"install_diff":    false,
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

// TASKS/skills/01: TestSelfToolsTransport_CreateSkill (the skill_create
// round-trip test) is deleted along with skill_create itself — see
// docs/engineering/architecture/20-skills.md's "Scope: skills are authored
// packages only" section. skill_list is still exercised below, seeded via
// store.CreateSkill directly instead of the now-deleted self-tool.

// TestSelfToolsTransport_ListSkills verifies filtering by category.
func TestSelfToolsTransport_ListSkills(t *testing.T) {
	st := newSelfTools(t)
	ctx := context.Background()

	// Seed two skills in different categories directly through the store
	// (skill_create no longer exists as a self-tool).
	if err := st.Store.CreateSkill(&store.Skill{
		Name: "Skill A", Slug: "skill-a", Description: "cat-x skill", Category: "cat-x",
	}); err != nil {
		t.Fatalf("seed skill-a: %v", err)
	}
	if err := st.Store.CreateSkill(&store.Skill{
		Name: "Skill B", Slug: "skill-b", Description: "cat-y skill", Category: "cat-y",
	}); err != nil {
		t.Fatalf("seed skill-b: %v", err)
	}

	// List all.
	allResult, _ := st.CallTool(ctx, "skill_list", map[string]any{})
	if !strings.Contains(allResult.Content[0].Text, "Skill A") || !strings.Contains(allResult.Content[0].Text, "Skill B") {
		t.Errorf("expected both skills, got: %s", allResult.Content[0].Text)
	}

	// Filter by cat-x.
	filteredResult, _ := st.CallTool(ctx, "skill_list", map[string]any{"category": "cat-x"})
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

	result, err := st.CallTool(ctx, "agent_create", map[string]any{
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
	listResult, err := st.CallTool(ctx, "agent_list", map[string]any{})
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

// TASKS/skills/01: TestSelfToolsTransport_CreateSkill_MissingFields (the
// skill_create input-validation test) is deleted along with skill_create
// itself. skill_delete is still exercised below, seeded via
// store.CreateSkill directly instead of the now-deleted self-tool.

// TestSelfToolsTransport_DeleteSkill tests skill deletion.
func TestSelfToolsTransport_DeleteSkill(t *testing.T) {
	st := newSelfTools(t)
	ctx := context.Background()

	// Seed a skill directly through the store (skill_create no longer
	// exists as a self-tool).
	seed := &store.Skill{Name: "To Delete", Slug: "to-delete", Description: "Will be deleted"}
	if err := st.Store.CreateSkill(seed); err != nil {
		t.Fatalf("seed skill: %v", err)
	}
	skillID := seed.ID
	if skillID == "" {
		t.Fatal("expected CreateSkill to populate an ID")
	}

	// Delete it.
	delResult, _ := st.CallTool(ctx, "skill_delete", map[string]any{"id": skillID})
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
	r, err := st.CallTool(ctx, "todo_create", map[string]any{
		"title": "wire test", "scope": "session", "scope_id": "sess-wire",
	})
	if err != nil || r.IsError {
		t.Fatalf("todo_create failed: %v / %s", err, r.Content[0].Text)
	}
	if b.calls != 1 {
		t.Fatalf("expected 1 broadcast after todo_create, got %d", b.calls)
	}

	// List is read-only — no broadcast.
	if _, err := st.CallTool(ctx, "todo_list", map[string]any{"scope": "session"}); err != nil {
		t.Fatal(err)
	}
	if b.calls != 1 {
		t.Fatalf("expected 1 broadcast after read-only list, got %d", b.calls)
	}

	// Create plan → 2 broadcasts.
	if _, err := st.CallTool(ctx, "plan_create", map[string]any{
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
	if _, err := st.CallTool(ctx, "plan_delete", map[string]any{"id": plans[0].ID}); err != nil {
		t.Fatal(err)
	}
	if b.calls != 3 {
		t.Fatalf("expected 3 broadcasts after plan_delete, got %d", b.calls)
	}
}

// TestSelfToolsTransport_TodoListEmitsEnvelope verifies that todo_list
// emits a list-card envelope (Phase 6 composition — the standalone
// `todo-list` type was retired, TASKS/phase-6/01-rebuild-todo-list-as-composition.md)
// carrying a `data_source` pointer with the scope/scope_id the caller
// filtered on, so the frontend's live todo composition can lazy-fetch
// correctly. CW-20260418-0045.
func TestSelfToolsTransport_TodoListEmitsEnvelope(t *testing.T) {
	st := newSelfTools(t)
	st.TodoStore = st.Store
	ctx := context.Background()

	// With scope: envelope must appear and carry scope + scope_id.
	r, err := st.CallTool(ctx, "todo_list", map[string]any{
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
	if !strings.Contains(body, `"type":"list-card"`) {
		t.Fatalf("expected list-card envelope type, got: %s", body)
	}
	if !strings.Contains(body, `"kind":"todos"`) {
		t.Fatalf("envelope missing data_source.kind, got: %s", body)
	}
	if !strings.Contains(body, `"scope":"session"`) || !strings.Contains(body, `"scope_id":"sess-env"`) {
		t.Fatalf("envelope missing scope coordinates: %s", body)
	}
	if !strings.Contains(body, `"title":"Session Todos"`) {
		t.Fatalf("envelope missing title: %s", body)
	}

	// Without scope: no envelope (would render an un-scoped card).
	r2, _ := st.CallTool(ctx, "todo_list", map[string]any{})
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
	ctx := mcp.WithSessionID(context.Background(), "ctx-session-xyz")

	r, err := st.CallTool(ctx, "plan_create", map[string]any{
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

	r, _ := st.CallTool(context.Background(), "plan_create", map[string]any{
		"title": "no scope_id",
		"scope": "session",
	})
	if !r.IsError {
		t.Fatalf("expected error result when scope_id is missing and ctx has no session")
	}
}

// TestSelfToolsTransport_PlanStepAdd_HappyPath verifies appending steps via
// the MCP tool surface returns success, preserves existing step IDs, and
// emits the appended-step JSON. CW-20260430-0001 (SP1).
func TestSelfToolsTransport_PlanStepAdd_HappyPath(t *testing.T) {
	st := newSelfTools(t)
	st.TodoStore = st.Store
	ctx := context.Background()

	// Seed a plan with one step via plan_create.
	createRes, err := st.CallTool(ctx, "plan_create", map[string]any{
		"title":    "step-add target",
		"scope":    "workspace",
		"scope_id": "",
		"steps":    `[{"id":"s1","title":"first","status":"pending"}]`,
	})
	if err != nil || createRes.IsError {
		t.Fatalf("plan_create failed: %v / %s", err, createRes.Content[0].Text)
	}
	plans, _ := st.Store.ListPlans(store.PlanFilter{Scope: "workspace"})
	if len(plans) != 1 {
		t.Fatalf("expected 1 plan, got %d", len(plans))
	}
	planID := plans[0].ID

	// Append two new steps.
	addRes, err := st.CallTool(ctx, "plan_step_add", map[string]any{
		"plan_id": planID,
		"steps":   `[{"title":"second"},{"title":"third","depends_on":["s2"]}]`,
	})
	if err != nil {
		t.Fatalf("plan_step_add: %v", err)
	}
	if addRes.IsError {
		t.Fatalf("unexpected error: %s", addRes.Content[0].Text)
	}
	body := addRes.Content[0].Text
	if !strings.Contains(body, "Appended 2 step(s)") {
		t.Errorf("expected 'Appended 2 step(s)' in result, got: %s", body)
	}
	if !strings.Contains(body, `"appended_count":2`) {
		t.Errorf("expected appended_count in JSON payload, got: %s", body)
	}

	// Existing step is preserved; new steps appended at the tail with s2/s3 ids.
	got, err := st.Store.GetPlan(planID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	gotSteps, _ := got.ParsePlanSteps()
	if len(gotSteps) != 3 {
		t.Fatalf("expected 3 steps after append, got %d", len(gotSteps))
	}
	if gotSteps[0].ID != "s1" || gotSteps[0].Title != "first" {
		t.Errorf("existing step mutated: %+v", gotSteps[0])
	}
	if gotSteps[1].ID != "s2" || gotSteps[1].Title != "second" {
		t.Errorf("second step wrong: %+v", gotSteps[1])
	}
	if gotSteps[2].ID != "s3" || gotSteps[2].Title != "third" {
		t.Errorf("third step wrong: %+v", gotSteps[2])
	}
}

// TestSelfToolsTransport_PlanStepAdd_PlanIDNotFound verifies an unknown
// plan_id returns a structured error result rather than a panic or silent
// no-op. CW-20260430-0001 (SP1).
func TestSelfToolsTransport_PlanStepAdd_PlanIDNotFound(t *testing.T) {
	st := newSelfTools(t)
	st.TodoStore = st.Store
	ctx := context.Background()

	r, err := st.CallTool(ctx, "plan_step_add", map[string]any{
		"plan_id": "no-such-plan",
		"steps":   `[{"title":"orphan step"}]`,
	})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !r.IsError {
		t.Fatal("expected IsError=true for missing plan_id")
	}
	if !strings.Contains(r.Content[0].Text, "append plan steps") {
		t.Errorf("expected handler error prefix, got: %s", r.Content[0].Text)
	}
}

// TestSelfToolDefinitions_PlanStepAddPresent verifies the new tool ships in
// selfToolDefinitions(). CW-20260430-0001 (SP1).
func TestSelfToolDefinitions_PlanStepAddPresent(t *testing.T) {
	defs := selfToolDefinitions()
	for _, d := range defs {
		if d.Name == "plan_step_add" {
			// Sanity-check Bucket-2 description sections.
			for _, want := range []string{"When to use", "When NOT to use", "Output shape", "Cross-references"} {
				if !strings.Contains(d.Description, want) {
					t.Errorf("plan_step_add description missing %q", want)
				}
			}
			return
		}
	}
	t.Fatal("plan_step_add missing from selfToolDefinitions()")
}

func TestSelfToolDefinitions_ScratchpadToolsPresent(t *testing.T) {
	defs := selfToolDefinitions()
	names := make(map[string]bool, len(defs))
	for _, d := range defs {
		names[d.Name] = true
	}
	for _, want := range []string{
		"scratchpad_write",
		"scratchpad_read",
		"scratchpad_clear",
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
		case "scratchpad_write", "scratchpad_read", "scratchpad_clear":
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

func TestScratchpadWriteSchema_ValueHasExplicitTypes(t *testing.T) {
	defs := selfToolDefinitions()
	for _, d := range defs {
		if d.Name != "scratchpad_write" {
			continue
		}
		props, _ := d.InputSchema["properties"].(map[string]any)
		valueSchema, _ := props["value"].(map[string]any)
		anyOf, _ := valueSchema["anyOf"].([]map[string]any)
		if len(anyOf) == 0 {
			t.Fatal("scratchpad_write.value schema missing anyOf")
		}
		wantTypes := map[string]bool{
			"string":  false,
			"number":  false,
			"integer": false,
			"boolean": false,
		}
		for _, schema := range anyOf {
			typ, _ := schema["type"].(string)
			if _, ok := wantTypes[typ]; ok {
				wantTypes[typ] = true
			}
		}
		for typ, ok := range wantTypes {
			if !ok {
				t.Fatalf("scratchpad_write.value schema missing %q variant", typ)
			}
		}
		return
	}
	t.Fatal("scratchpad_write not found")
}

func TestExtractStoredAssistantText(t *testing.T) {
	raw := `{"v":1,"text":"hello from child","tier":"tool"}`
	if got := extractStoredAssistantText(raw); got != "hello from child" {
		t.Fatalf("extractStoredAssistantText() = %q, want %q", got, "hello from child")
	}
	if got := extractStoredAssistantText("not json"); got != "" {
		t.Fatalf("extractStoredAssistantText(invalid) = %q, want empty", got)
	}
}

func TestExtractLiteralSubagentOutput_FileReadPrompt(t *testing.T) {
	prompt := "Read the first 5 lines of /tmp/README.md and return the content."
	text := "I'll read the file for you.\n\n```text\nline 1\nline 2\n```\n\nSummary follows."
	got, ok := extractLiteralSubagentOutput(prompt, text)
	if !ok {
		t.Fatal("expected literal extraction to succeed")
	}
	want := "First 5 lines of `README.md`:\n\n1. `line 1`\n2. `line 2`"
	if got != want {
		t.Fatalf("extractLiteralSubagentOutput() = %q, want %q", got, want)
	}
}

func TestExtractLiteralSubagentOutput_NumberedList(t *testing.T) {
	prompt := "Read the first 5 lines of /tmp/README.md and return the content."
	text := "Here are the first 5 lines:\n\n1. `# NANITE`\n2. (empty line)\n3. `Nanite...`\n4. (empty line)\n5. `It provides...`\n\nSummary follows."
	got, ok := extractLiteralSubagentOutput(prompt, text)
	if !ok {
		t.Fatal("expected numbered-list literal extraction to succeed")
	}
	want := "First 5 lines of `README.md`:\n\n1. `# NANITE`\n2. (blank line)\n3. `Nanite...`\n4. (blank line)\n5. `It provides...`"
	if got != want {
		t.Fatalf("extractLiteralSubagentOutput() = %q, want %q", got, want)
	}
}

func TestExtractLiteralSubagentOutput_NonLiteralPrompt(t *testing.T) {
	prompt := "Summarize /tmp/README.md in two bullets."
	text := "```text\nline 1\nline 2\n```"
	if got, ok := extractLiteralSubagentOutput(prompt, text); ok || got != "" {
		t.Fatalf("expected no literal extraction, got %q ok=%v", got, ok)
	}
}

// TestSelfToolsTransport_PlanCRUD exercises the full plan lifecycle via the
// self-service tools (create → list → get → delete).
func TestSelfToolsTransport_PlanCRUD(t *testing.T) {
	st := newSelfTools(t)
	st.TodoStore = st.Store
	ctx := context.Background()

	createResult, err := st.CallTool(ctx, "plan_create", map[string]any{
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

	listResult, _ := st.CallTool(ctx, "plan_list", map[string]any{
		"scope":    "session",
		"scope_id": "sess-1",
	})
	if listResult.IsError || !strings.Contains(listResult.Content[0].Text, "UAT Plan") {
		t.Fatalf("plan_list did not surface plan: %s", listResult.Content[0].Text)
	}

	getResult, _ := st.CallTool(ctx, "plan_get", map[string]any{"id": planID})
	if getResult.IsError || !strings.Contains(getResult.Content[0].Text, planID) {
		t.Fatalf("plan_get did not return plan: %s", getResult.Content[0].Text)
	}

	delResult, _ := st.CallTool(ctx, "plan_delete", map[string]any{"id": planID})
	if delResult.IsError {
		t.Fatalf("plan_delete failed: %s", delResult.Content[0].Text)
	}

	plans, _ = st.Store.ListPlans(store.PlanFilter{Scope: "session", ScopeID: "sess-1"})
	if len(plans) != 0 {
		t.Fatalf("expected 0 plans after delete, got %d", len(plans))
	}
}
