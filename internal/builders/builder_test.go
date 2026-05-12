package builders

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := store.New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("newTestStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestAgentBuilder_FullFlow(t *testing.T) {
	s := newTestStore(t)
	reg := DefaultRegistry(s)
	sm := NewSessionManager()
	sessionKey := "test-session-1"

	// Start the agent builder.
	out, err := HandleStartBuilder(reg, sm, sessionKey, map[string]any{
		"builder_name": "agent",
	})
	if err != nil {
		t.Fatalf("start builder: %v", err)
	}

	var start StartBuilderResult
	if err := json.Unmarshal([]byte(out), &start); err != nil {
		t.Fatalf("unmarshal start: %v", err)
	}
	if start.Builder != "agent" {
		t.Errorf("expected builder=agent, got %s", start.Builder)
	}
	if start.FirstStep != "name" {
		t.Errorf("expected first step=name, got %s", start.FirstStep)
	}
	if start.TotalSteps != 5 {
		t.Errorf("expected 5 steps, got %d", start.TotalSteps)
	}

	// Step through each field.
	steps := []struct {
		step  string
		value string
	}{
		{"name", "Test Agent"},
		{"slug", "test-agent"},
		{"system_prompt", "You are a helpful test agent."},
		{"model", "claude-sonnet-4-20250514"},
		{"description", "An agent for testing the builder flow."},
	}

	for i, st := range steps {
		out, err := HandleBuilderStep(reg, sm, sessionKey, map[string]any{
			"builder_name": "agent",
			"step_name":    st.step,
			"value":        st.value,
		})
		if err != nil {
			t.Fatalf("step %s: %v", st.step, err)
		}

		var result StepResult
		if err := json.Unmarshal([]byte(out), &result); err != nil {
			t.Fatalf("unmarshal step %s: %v", st.step, err)
		}

		if i < len(steps)-1 {
			if result.Status != "next" {
				t.Errorf("step %s: expected status=next, got %s", st.step, result.Status)
			}
			if result.NextStep != steps[i+1].step {
				t.Errorf("step %s: expected next=%s, got %s", st.step, steps[i+1].step, result.NextStep)
			}
		} else {
			// Last step should complete.
			if result.Status != "complete" {
				t.Errorf("step %s: expected status=complete, got %s (error: %s)", st.step, result.Status, result.Error)
			}
			if result.Result == nil {
				t.Fatal("expected non-nil result on completion")
			}
			if !strings.Contains(result.Result.Summary, "Test Agent") {
				t.Errorf("expected summary to contain agent name, got: %s", result.Result.Summary)
			}
		}
	}

	// Verify agent was created in the store.
	agent, err := s.GetAgentBySlug("test-agent")
	if err != nil {
		t.Fatalf("get agent by slug: %v", err)
	}
	if agent.Name != "Test Agent" {
		t.Errorf("expected name=Test Agent, got %s", agent.Name)
	}
	if agent.SystemPrompt != "You are a helpful test agent." {
		t.Errorf("unexpected system prompt: %s", agent.SystemPrompt)
	}
	if agent.DefaultModel != "claude-sonnet-4-20250514" {
		t.Errorf("unexpected model: %s", agent.DefaultModel)
	}
}

func TestSkillBuilder_FullFlow(t *testing.T) {
	s := newTestStore(t)
	reg := DefaultRegistry(s)
	sm := NewSessionManager()
	sessionKey := "test-session-2"

	// Start the skill builder.
	out, err := HandleStartBuilder(reg, sm, sessionKey, map[string]any{
		"builder_name": "skill",
	})
	if err != nil {
		t.Fatalf("start builder: %v", err)
	}

	var start StartBuilderResult
	if err := json.Unmarshal([]byte(out), &start); err != nil {
		t.Fatalf("unmarshal start: %v", err)
	}
	if start.Builder != "skill" {
		t.Errorf("expected builder=skill, got %s", start.Builder)
	}
	if start.TotalSteps != 4 {
		t.Errorf("expected 4 steps, got %d", start.TotalSteps)
	}

	// Step through.
	steps := []struct {
		step  string
		value string
	}{
		{"name", "Code Review"},
		{"description", "Reviews code for quality and correctness"},
		{"category", "dev"},
		{"tool_bindings", "dev_read, dev_grep"},
	}

	for i, st := range steps {
		out, err := HandleBuilderStep(reg, sm, sessionKey, map[string]any{
			"builder_name": "skill",
			"step_name":    st.step,
			"value":        st.value,
		})
		if err != nil {
			t.Fatalf("step %s: %v", st.step, err)
		}

		var result StepResult
		if err := json.Unmarshal([]byte(out), &result); err != nil {
			t.Fatalf("unmarshal step %s: %v", st.step, err)
		}

		if i < len(steps)-1 {
			if result.Status != "next" {
				t.Errorf("step %s: expected status=next, got %s", st.step, result.Status)
			}
		} else {
			if result.Status != "complete" {
				t.Errorf("step %s: expected status=complete, got %s (error: %s)", st.step, result.Status, result.Error)
			}
			if result.Result == nil {
				t.Fatal("expected non-nil result on completion")
			}
		}
	}

	// Verify skill was created.
	skill, err := s.GetSkillBySlug("code-review")
	if err != nil {
		t.Fatalf("get skill by slug: %v", err)
	}
	if skill.Name != "Code Review" {
		t.Errorf("expected name=Code Review, got %s", skill.Name)
	}
	if skill.Category != "dev" {
		t.Errorf("expected category=dev, got %s", skill.Category)
	}
	if !strings.Contains(skill.ToolBindings, "dev_read") {
		t.Errorf("expected tool_bindings to contain dev_read, got %s", skill.ToolBindings)
	}
}

func TestPromptTemplateBuilder_FullFlow(t *testing.T) {
	s := newTestStore(t)
	reg := DefaultRegistry(s)
	sm := NewSessionManager()
	sessionKey := "test-session-3"

	// Start the prompt template builder.
	out, err := HandleStartBuilder(reg, sm, sessionKey, map[string]any{
		"builder_name": "prompt_template",
	})
	if err != nil {
		t.Fatalf("start builder: %v", err)
	}

	var start StartBuilderResult
	if err := json.Unmarshal([]byte(out), &start); err != nil {
		t.Fatalf("unmarshal start: %v", err)
	}
	if start.Builder != "prompt_template" {
		t.Errorf("expected builder=prompt_template, got %s", start.Builder)
	}

	// Step through.
	steps := []struct {
		step  string
		value string
	}{
		{"name", "Safety Guardrails"},
		{"scope", "system"},
		{"template", "Always prioritize user safety. Never {{forbidden_action}}."},
		{"variables", "forbidden_action"},
		{"priority", "5"},
	}

	for i, st := range steps {
		out, err := HandleBuilderStep(reg, sm, sessionKey, map[string]any{
			"builder_name": "prompt_template",
			"step_name":    st.step,
			"value":        st.value,
		})
		if err != nil {
			t.Fatalf("step %s: %v", st.step, err)
		}

		var result StepResult
		if err := json.Unmarshal([]byte(out), &result); err != nil {
			t.Fatalf("unmarshal step %s: %v", st.step, err)
		}

		if i < len(steps)-1 {
			if result.Status != "next" {
				t.Errorf("step %s: expected status=next, got %s", st.step, result.Status)
			}
		} else {
			if result.Status != "complete" {
				t.Errorf("step %s: expected status=complete, got %s (error: %s)", st.step, result.Status, result.Error)
			}
		}
	}

	// Verify prompt template was created.
	pt, err := s.GetPromptTemplateBySlug("safety-guardrails")
	if err != nil {
		t.Fatalf("get prompt template by slug: %v", err)
	}
	if pt.Name != "Safety Guardrails" {
		t.Errorf("expected name=Safety Guardrails, got %s", pt.Name)
	}
	if pt.Scope != "system" {
		t.Errorf("expected scope=system, got %s", pt.Scope)
	}
	if pt.Priority != 5 {
		t.Errorf("expected priority=5, got %d", pt.Priority)
	}
}

func TestBuilderRegistry_ListBuilders(t *testing.T) {
	s := newTestStore(t)
	reg := DefaultRegistry(s)

	names := reg.ListBuilders()
	if len(names) != 3 {
		t.Fatalf("expected 3 builders, got %d: %v", len(names), names)
	}

	// Should be sorted alphabetically.
	expected := []string{"agent", "prompt_template", "skill"}
	for i, name := range names {
		if name != expected[i] {
			t.Errorf("builder[%d]: expected %s, got %s", i, expected[i], name)
		}
	}
}

func TestBuilderStep_Validation(t *testing.T) {
	s := newTestStore(t)
	reg := DefaultRegistry(s)
	sm := NewSessionManager()

	// Test required field validation on agent builder.
	sessionKey := "test-validation-1"
	_, err := HandleStartBuilder(reg, sm, sessionKey, map[string]any{
		"builder_name": "agent",
	})
	if err != nil {
		t.Fatalf("start builder: %v", err)
	}

	// Try empty name (required).
	out, err := HandleBuilderStep(reg, sm, sessionKey, map[string]any{
		"builder_name": "agent",
		"step_name":    "name",
		"value":        "",
	})
	if err != nil {
		t.Fatalf("step name: %v", err)
	}

	var result StepResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Status != "error" {
		t.Errorf("expected status=error for empty required field, got %s", result.Status)
	}
	if !strings.Contains(result.Error, "required") {
		t.Errorf("expected error about required field, got: %s", result.Error)
	}

	// Test slug validation (invalid characters).
	// First provide a valid name.
	_, _ = HandleBuilderStep(reg, sm, sessionKey, map[string]any{
		"builder_name": "agent",
		"step_name":    "name",
		"value":        "Valid Name",
	})

	out, err = HandleBuilderStep(reg, sm, sessionKey, map[string]any{
		"builder_name": "agent",
		"step_name":    "slug",
		"value":        "INVALID SLUG!",
	})
	if err != nil {
		t.Fatalf("step slug: %v", err)
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Status != "error" {
		t.Errorf("expected status=error for invalid slug, got %s", result.Status)
	}
}

func TestStartBuilder_NoName(t *testing.T) {
	s := newTestStore(t)
	reg := DefaultRegistry(s)
	sm := NewSessionManager()

	// Calling with no builder_name should list available builders.
	out, err := HandleStartBuilder(reg, sm, "key", map[string]any{})
	if err != nil {
		t.Fatalf("start builder: %v", err)
	}
	if !strings.Contains(out, "agent") || !strings.Contains(out, "skill") {
		t.Errorf("expected available builders listed, got: %s", out)
	}
}

func TestStartBuilder_UnknownName(t *testing.T) {
	s := newTestStore(t)
	reg := DefaultRegistry(s)
	sm := NewSessionManager()

	_, err := HandleStartBuilder(reg, sm, "key", map[string]any{
		"builder_name": "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error for unknown builder")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("expected error to mention builder name, got: %v", err)
	}
}

func TestAgentBuilder_DefaultModel(t *testing.T) {
	s := newTestStore(t)
	reg := DefaultRegistry(s)
	sm := NewSessionManager()
	sessionKey := "test-default-model"

	_, _ = HandleStartBuilder(reg, sm, sessionKey, map[string]any{
		"builder_name": "agent",
	})

	// Walk through with empty model to test default.
	steps := []struct {
		step  string
		value string
	}{
		{"name", "Default Model Agent"},
		{"slug", ""}, // auto-generate
		{"system_prompt", "A test prompt."},
		{"model", ""}, // should default to claude-sonnet-4-20250514
		{"description", ""},
	}

	for _, st := range steps {
		_, err := HandleBuilderStep(reg, sm, sessionKey, map[string]any{
			"builder_name": "agent",
			"step_name":    st.step,
			"value":        st.value,
		})
		if err != nil {
			t.Fatalf("step %s: %v", st.step, err)
		}
	}

	agent, err := s.GetAgentBySlug("default-model-agent")
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if agent.DefaultModel != "claude-sonnet-4-20250514" {
		t.Errorf("expected default model, got %s", agent.DefaultModel)
	}
}

func TestNameToSlug(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Code Reviewer", "code-reviewer"},
		{"My Agent_v2", "my-agent-v2"},
		{"  spaces  ", "spaces"},
		{"UPPER CASE", "upper-case"},
		{"special!@#chars", "specialchars"},
		{"multi---hyphens", "multi-hyphens"},
	}

	for _, tt := range tests {
		got := nameToSlug(tt.input)
		if got != tt.want {
			t.Errorf("nameToSlug(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestBuilderStep_NoActiveSession(t *testing.T) {
	s := newTestStore(t)
	reg := DefaultRegistry(s)
	sm := NewSessionManager()

	_, err := HandleBuilderStep(reg, sm, "no-session", map[string]any{
		"builder_name": "agent",
		"step_name":    "name",
		"value":        "test",
	})
	if err == nil {
		t.Fatal("expected error when no session active")
	}
	if !strings.Contains(err.Error(), "builder_start") {
		t.Errorf("expected error to mention builder_start, got: %v", err)
	}
}
