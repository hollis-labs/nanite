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
	st, err := storetest.New(t, ctx, filepath.Join(t.TempDir(), "selftools.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	transport := NewSelfToolsTransport(st)
	skill := &store.Skill{Name: "Wired skill", Slug: "wired-skill", Category: "probe"}
	if err := st.CreateSkill(ctx, skill); err != nil {
		t.Fatal(err)
	}
	agent := &store.AgentProfile{ID: "wired-agent", Slug: "wired-agent", Status: "active"}
	if err := st.CreateAgent(ctx, agent); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertAgentProcedure(ctx, store.AgentProcedure{AgentID: agent.ID, Name: "boot", Body: "wired procedure"}); err != nil {
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
	if got := call("procedure_get", map[string]any{"name": "boot", "agent_id": "forged"}); got != "wired procedure" {
		t.Fatal(got)
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
	row, err := st.GetHandoffStash(ctx, session.ID, stash.CacheKey)
	if err != nil || !strings.Contains(row.Payload, "wired next") {
		t.Fatalf("stash: %+v %v", row, err)
	}
}
