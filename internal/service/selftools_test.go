package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

func TestSelfToolsReadServiceWiring(t *testing.T) {
	ctx := t.Context()
	st, openErr := storetest.New(t, ctx, filepath.Join(t.TempDir(), "selftools.db"))
	if openErr != nil {
		t.Fatal(openErr)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	transport := NewSelfToolsTransport(st)
	skill := &store.Skill{Name: "Wired skill", Slug: "wired-skill", Category: "probe"}
	if err := st.CreateSkill(ctx, skill); err != nil {
		t.Fatal(err)
	}
	agent := &store.AgentProfile{ID: "wired-agent", Name: "Wired actor", Slug: "wired-agent", Status: "active"}
	if err := persistTestActor(ctx, st, agent); err != nil {
		t.Fatal(err)
	}
	session := &store.Session{ID: "wired-session", Title: "Wired session"}
	if err := st.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateMessage(ctx, &store.Message{SessionID: session.ID, Role: "assistant", Content: "wired reply"}); err != nil {
		t.Fatal(err)
	}
	ctx = mcp.WithCallerProfile(mcp.WithSessionID(ctx, session.ID), agent.ID)
	call := func(name string, args map[string]any) string {
		t.Helper()
		result, err := transport.CallTool(ctx, name, args)
		if err != nil || result == nil || result.IsError {
			t.Fatalf("%s: %+v, %v", name, result, err)
		}
		return result.Content[0].Text
	}
	if got := call("skill_list", map[string]any{"category": "probe"}); !strings.Contains(got, skill.Name) {
		t.Fatal(got)
	}
	// Legacy procedure rows are not a runtime fallback for immutable SOPs.
	result, procedureErr := transport.CallTool(ctx, "procedure_get", map[string]any{"name": "boot", "agent_id": "forged"})
	if procedureErr == nil && (result == nil || !result.IsError) {
		t.Fatalf("retired procedure lookup: %+v %v", result, procedureErr)
	}
	for _, target := range []string{session.ID, session.ShortCode} {
		if got := call("chat_get", map[string]any{"target": target}); !strings.Contains(got, "wired reply") {
			t.Fatal(got)
		}
	}
	var stash struct {
		CacheKey string `json:"cache_key"`
	}
	if err := json.Unmarshal([]byte(call("handoff_stash", map[string]any{"session_intent": "wired intent", "next_step_anchor": "wired next"})), &stash); err != nil {
		t.Fatal(err)
	}
	if got := call("handoff_pointers_expand", map[string]any{"cache_key": stash.CacheKey}); !strings.Contains(got, "wired next") {
		t.Fatal(got)
	}
	row, err := st.GetHandoffStash(ctx, session.ID, stash.CacheKey)
	if err != nil || !strings.Contains(row.Payload, "wired next") {
		t.Fatalf("stash: %+v %v", row, err)
	}
}

func TestSelfToolsWriteServiceWiring(t *testing.T) {
	ctx := t.Context()
	st, openErr := storetest.New(t, ctx, filepath.Join(t.TempDir(), "writes.db"))
	if openErr != nil {
		t.Fatal(openErr)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	transport := NewSelfToolsTransport(st)
	project := &store.Project{ID: "write-project", Name: "Write project"}
	if err := st.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	session := &store.Session{ID: "write-session", Title: "Write session", ProjectID: project.ID}
	if err := st.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	agent := &store.AgentProfile{ID: "write-agent", Name: "Write actor", Slug: "write-agent", Status: "active"}
	if err := persistTestActor(ctx, st, agent); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureSessionAgent(ctx, session.ID, agent.ID, "default", true); err != nil {
		t.Fatal(err)
	}
	ctx = mcp.WithSessionID(ctx, session.ID) // schedule falls back to the primary binding.
	call := func(name string, args map[string]any) string {
		t.Helper()
		result, err := transport.CallTool(ctx, name, args)
		if err != nil || result == nil || result.IsError {
			t.Fatalf("%s: %+v %v", name, result, err)
		}
		return result.Content[0].Text
	}
	call("schedule_create", map[string]any{"name": "wired schedule", "message": "wake me", "kind": "one_shot"})
	schedules, err := st.ListAgentSchedules(ctx, agent.ID)
	if err != nil || len(schedules) != 1 || schedules[0].NextRun == "" || !strings.HasPrefix(schedules[0].ID, "self-sched-") {
		t.Fatalf("schedules: %+v %v", schedules, err)
	}
}

func TestSelfToolsTodoUpdateListedAndCallable(t *testing.T) {
	ctx := t.Context()
	st, openErr := storetest.New(t, ctx, filepath.Join(t.TempDir(), "todo-update.db"))
	if openErr != nil {
		t.Fatal(openErr)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	transport := NewSelfToolsTransport(st)
	transport.HideUnwired = true
	if transport.WorkTrackingTools.Store != nil || transport.WorkTrackingTools.Updater == nil {
		t.Fatal("expected production service-only todo updater wiring")
	}
	tools, err := transport.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	listed := false
	for _, tool := range tools {
		if tool.Name == "todo_update" {
			listed = true
		}
	}
	if !listed {
		t.Fatal("todo_update missing from production transport discovery")
	}
	row := &store.Todo{Title: "Original", Scope: "session", ScopeID: "fixture"}
	if createErr := st.CreateTodo(ctx, row); createErr != nil {
		t.Fatal(createErr)
	}
	result, err := transport.CallTool(ctx, "todo_update", map[string]any{"id": row.ID, "title": "Updated"})
	if err != nil || result == nil || result.IsError {
		t.Fatalf("todo_update: %+v %v", result, err)
	}
	var updated store.Todo
	if decodeErr := json.Unmarshal([]byte(result.Content[0].Text), &updated); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	persisted, err := st.GetTodo(ctx, row.ID)
	if err != nil || updated.Title != "Updated" || persisted.Title != "Updated" {
		t.Fatalf("update not applied: %+v %+v %v", updated, persisted, err)
	}
}
