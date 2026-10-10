package builders

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := storetest.New(t, context.Background(), dbPath)
	if err != nil {
		t.Fatalf("newTestStore: %v", err)
	}
	t.Cleanup(func() { s.Close(context.Background()) })
	return s
}

func TestAgentBuilder_FullFlow(t *testing.T) {
	s := newTestStore(t)
	before := builderBoundarySnapshot(t, s)
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
			if result.Status != "error" || result.Result != nil || result.Error != "create agent: "+store.ErrImmutableAgentProfile.Error() {
				t.Fatalf("final wire result = %+v, want immutable refusal", result)
			}
			if sm.Get(sessionKey) != nil {
				t.Fatal("failed build retained an active session")
			}
		}
	}

	if after := builderBoundarySnapshot(t, s); !reflect.DeepEqual(before, after) {
		t.Fatalf("builder changed history or authority: %#v -> %#v", before, after)
	}
}

func TestSkillBuilder_FullFlow(t *testing.T) {
	s := newTestStore(t)
	reg := DefaultRegistry(s)
	sm := NewSessionManager()
	sessionKey := "test-session-2"

	// Start the skill builder.
	out, startErr := HandleStartBuilder(reg, sm, sessionKey, map[string]any{
		"builder_name": "skill",
	})
	if startErr != nil {
		t.Fatalf("start builder: %v", startErr)
	}

	var start StartBuilderResult
	if decodeErr := json.Unmarshal([]byte(out), &start); decodeErr != nil {
		t.Fatalf("unmarshal start: %v", decodeErr)
	}
	if start.Builder != "skill" {
		t.Errorf("expected builder=skill, got %s", start.Builder)
	}
	// TASKS/skills/02: the "tool_bindings" step is dropped along with
	// store.Skill.ToolBindings — the skill builder now has 3 steps, not 4.
	if start.TotalSteps != 3 {
		t.Errorf("expected 3 steps, got %d", start.TotalSteps)
	}

	// Step through.
	steps := []struct {
		step  string
		value string
	}{
		{"name", "Code Review"},
		{"description", "Reviews code for quality and correctness"},
		{"category", "dev"},
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
		if decodeErr := json.Unmarshal([]byte(out), &result); decodeErr != nil {
			t.Fatalf("unmarshal step %s: %v", st.step, decodeErr)
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
	skill, err := s.GetSkillBySlug(context.Background(), "code-review")
	if err != nil {
		t.Fatalf("get skill by slug: %v", err)
	}
	if skill.Name != "Code Review" {
		t.Errorf("expected name=Code Review, got %s", skill.Name)
	}
	if skill.Category != "dev" {
		t.Errorf("expected category=dev, got %s", skill.Category)
	}
}

func TestBuilderRegistry_ListBuilders(t *testing.T) {
	s := newTestStore(t)
	reg := DefaultRegistry(s)

	names := reg.ListBuilders()
	if len(names) != 2 {
		t.Fatalf("expected 2 builders, got %d: %v", len(names), names)
	}

	// Should be sorted alphabetically.
	expected := []string{"agent", "skill"}
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
	if decodeErr := json.Unmarshal([]byte(out), &result); decodeErr != nil {
		t.Fatalf("unmarshal: %v", decodeErr)
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
	if decodeErr := json.Unmarshal([]byte(out), &result); decodeErr != nil {
		t.Fatalf("unmarshal: %v", decodeErr)
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
	before := builderBoundarySnapshot(t, s)
	if _, err := HandleStartBuilder(reg, sm, sessionKey, map[string]any{"builder_name": "agent"}); err != nil {
		t.Fatal(err)
	}
	steps := []struct{ step, value string }{
		{"name", "Default Model Agent"}, {"slug", ""}, {"system_prompt", "A test prompt."}, {"model", ""}, {"description", ""},
	}
	for i, step := range steps {
		out, err := HandleBuilderStep(reg, sm, sessionKey, map[string]any{"builder_name": "agent", "step_name": step.step, "value": step.value})
		if err != nil {
			t.Fatal(err)
		}
		var result StepResult
		if decodeErr := json.Unmarshal([]byte(out), &result); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if i < len(steps)-1 {
			if result.Status != "next" || result.NextStep != steps[i+1].step {
				t.Fatalf("interview step %s: %+v", step.step, result)
			}
			sess := sm.Get(sessionKey)
			if sess == nil || sess.Inputs[step.step] != step.value {
				t.Fatalf("interview lost submitted %s: %+v", step.step, sess)
			}
			if step.step == "model" && !strings.Contains(reg.Get("agent").StepByName("model").Prompt, "leave blank") {
				t.Fatal("blank model interview hint lost")
			}
		} else {
			if result.Status != "error" || result.Result != nil || result.Error != "create agent: "+store.ErrImmutableAgentProfile.Error() {
				t.Fatalf("blank/default path completed a retired creation: %+v", result)
			}
		}
	}
	if sm.Get(sessionKey) != nil {
		t.Fatal("refused build retained session")
	}
	if after := builderBoundarySnapshot(t, s); !reflect.DeepEqual(before, after) {
		t.Fatalf("builder changed history or authority: %#v -> %#v", before, after)
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

// builderBoundarySnapshot captures retained history and both authority partitions in the private DB.
func builderBoundarySnapshot(t *testing.T, st *store.Store) map[string][][]any {
	t.Helper()
	out := make(map[string][][]any)
	for _, query := range []struct{ table, sql string }{
		{"agent_profiles", "SELECT * FROM agent_profiles ORDER BY rowid"},
		{"agent_profile_revisions", "SELECT * FROM agent_profile_revisions ORDER BY rowid"},
		{"agent_procedures", "SELECT * FROM agent_procedures ORDER BY rowid"},
		{"agent_reflexes", "SELECT * FROM agent_reflexes ORDER BY rowid"},
		{"agent_tools", "SELECT * FROM agent_tools ORDER BY rowid"},
		{"agent_known_skills", "SELECT * FROM agent_known_skills ORDER BY rowid"},
		{"agent_definitions", "SELECT * FROM agent_definitions ORDER BY rowid"},
		{"agent_definition_resources", "SELECT * FROM agent_definition_resources ORDER BY rowid"},
		{"agent_definition_resource_refs", "SELECT * FROM agent_definition_resource_refs ORDER BY rowid"},
		{"agent_host_settings", "SELECT * FROM agent_host_settings ORDER BY rowid"},
		{"agent_actor_bindings", "SELECT * FROM agent_actor_bindings ORDER BY rowid"},
		{"actor_granted_tools", "SELECT * FROM actor_granted_tools ORDER BY rowid"},
		{"actor_known_skills", "SELECT * FROM actor_known_skills ORDER BY rowid"},
	} {
		rows, err := st.DB.QueryContext(t.Context(), query.sql)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		for rows.Next() {
			cells := make([]any, len(columns))
			refs := make([]any, len(columns))
			for i := range cells {
				refs[i] = &cells[i]
			}
			if err := rows.Scan(refs...); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			for i, cell := range cells {
				if raw, ok := cell.([]byte); ok {
					cells[i] = append([]byte(nil), raw...)
				}
			}
			out[query.table] = append(out[query.table], cells)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return out
}
