package service

import (
	"context"
	"sort"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/dispatch"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/toolclient"
)

// TestApplyChatSurfaceFilter_DropsLensPrimitives is the unit assertion for
// the helper used by SelectForAgent: given the canonical Phase 2 surface,
// the four lens primitives are dropped and everything else passes.
func TestApplyChatSurfaceFilter_DropsLensPrimitives(t *testing.T) {
	tools := []llmtypes.ToolDefinition{
		{Name: "tool_describe"},
		{Name: "tool_validate"},
		{Name: "lesson_capture"},
		{Name: "card_show"},
		{Name: "dev_grep"},
		{Name: "tesseract_recall"},
		{Name: "tesseract_get"},
	}
	got := applyChatSurfaceFilter(tools, dispatch.DefaultChatToolSurface())
	wantNames := []string{"dev_grep", "tesseract_get", "tesseract_recall"}
	if names := sortedNames(got); !sliceEq(names, wantNames) {
		t.Errorf("applyChatSurfaceFilter names = %v, want %v", names, wantNames)
	}
}

// TestApplyChatSurfaceFilter_NilSurfacePassThrough — nil surface is no-op.
func TestApplyChatSurfaceFilter_NilSurfacePassThrough(t *testing.T) {
	tools := []llmtypes.ToolDefinition{
		{Name: "tool_describe"},
		{Name: "card_show"},
	}
	got := applyChatSurfaceFilter(tools, nil)
	if len(got) != 2 {
		t.Errorf("applyChatSurfaceFilter(nil) len = %d, want 2 (pass-through)", len(got))
	}
}

// TestApplyChatSurfaceFilter_EmptyInputSafe — empty input slice returns
// without error.
func TestApplyChatSurfaceFilter_EmptyInputSafe(t *testing.T) {
	got := applyChatSurfaceFilter(nil, dispatch.DefaultChatToolSurface())
	if len(got) != 0 {
		t.Errorf("applyChatSurfaceFilter(nil tools) len = %d, want 0", len(got))
	}
}

func TestHandleRequestTools_PreservesChatSurface(t *testing.T) {
	tc := toolclient.New(nil, nil, nil)
	tc.Builtins.RegisterBuiltins("fixture", []llmtypes.ToolDefinition{
		{Name: "tool_validate", Description: "Validate tool inputs."},
		{Name: "dev_read", Description: "Read a file."},
	})
	reader := newStubReader()
	reader.addAgent(&store.AgentProfile{ID: "chat-agent", Slug: chatRoleAgentSlug, Status: "active"})
	svc := NewToolService(tc, nil, reader)
	loaded, _, err := svc.HandleRequestTools(context.Background(), "chat-agent", map[string]any{
		"tool_names": []any{"tool_validate", "dev_read"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if containsToolNamed(loaded, "tool_validate") {
		t.Fatal("discovery bypassed the chat surface exclusion")
	}
	if !containsToolNamed(loaded, "dev_read") {
		t.Fatal("discovery lost the permitted file-read tool")
	}
}

// TestSelectForAgent_ChatRoleStripsLensPrimitives confirms the slug-keyed
// branch in SelectForAgent: an agent with slug "default" (chat-role) must
// not see the four lens primitives in its tool selection.
func TestSelectForAgent_ChatRoleStripsLensPrimitives(t *testing.T) {
	tc := buildToolClientWithLens(t)

	reader := newStubReader()
	reader.addAgent(&store.AgentProfile{
		ID:     "chat-agent-id",
		Slug:   chatRoleAgentSlug, // "default"
		Status: "active",
	})

	svc := NewToolService(tc, nil, reader).(*toolServiceImpl)
	sel, err := svc.SelectForAgent(context.Background(), "s1", "chat-agent-id", "render report card", "", 0)
	if err != nil {
		t.Fatalf("SelectForAgent: %v", err)
	}

	names := sortedNames(sel.Tools)
	for _, lens := range []string{"tool_describe", "tool_validate", "lesson_capture", "card_show"} {
		for _, n := range names {
			if n == lens {
				t.Errorf("chat-role surface unexpectedly carries %q (got %v)", lens, names)
			}
		}
	}
	// At least one of the non-lens companions must still be present so
	// we know the filter didn't accidentally clear everything.
	wantOne := map[string]bool{"dev_grep": true, "tesseract_recall": true, "tesseract_get": true}
	hit := false
	for _, n := range names {
		if wantOne[n] {
			hit = true
			break
		}
	}
	if !hit {
		t.Errorf("chat-role surface missing every expected non-lens companion (got %v)", names)
	}
}

// TestSelectForAgent_NonChatRoleKeepsLensPrimitives — Worker / Planner /
// executor / hint-selector profiles bypass the chat-surface filter and
// retain the lens primitives. This is the partner assertion to the chat
// test above; without it, a regression that filters every profile (not
// just chat) would slip through.
func TestSelectForAgent_NonChatRoleKeepsLensPrimitives(t *testing.T) {
	tc := buildToolClientWithLens(t)

	reader := newStubReader()
	for _, slug := range []string{"worker", "planner", "hint-selector", "mux-orchestrator", "envelope-renderer"} {
		t.Run(slug, func(t *testing.T) {
			id := "agent-" + slug
			reader.addAgent(&store.AgentProfile{
				ID:     id,
				Slug:   slug,
				Status: "active",
			})

			svc := NewToolService(tc, nil, reader).(*toolServiceImpl)
			sel, err := svc.SelectForAgent(context.Background(), "s1", id, "render report card", "", 0)
			if err != nil {
				t.Fatalf("SelectForAgent (%s): %v", slug, err)
			}
			names := sortedNames(sel.Tools)

			// Each non-chat profile must retain at least one lens primitive
			// the broker emitted. The broker may not return all four
			// (relevance ranking varies); the assertion is "≥1 lens
			// primitive passes through" — enough to prove the filter is
			// bypassed.
			lensFound := 0
			for _, n := range names {
				switch n {
				case "tool_describe", "tool_validate", "lesson_capture", "card_show":
					lensFound++
				}
			}
			if lensFound == 0 {
				t.Errorf("non-chat profile %q lost all lens primitives — chat-surface filter is leaking (got %v)", slug, names)
			}
		})
	}
}

// --- helpers ---

// buildToolClientWithLens wires a toolclient with the four lens primitives
// plus a couple of unrelated tools so there's a real set to filter. Tools
// are registered directly on the catalog (not as builtins) so they flow
// through SelectToolsAsProvider unchanged.
func buildToolClientWithLens(t *testing.T) *toolclient.ToolClient {
	t.Helper()
	cfg := toolclient.DefaultConfig()
	tc := toolclient.New(nil, nil, cfg)
	tc.RegisterTools([]llmtypes.ToolDefinition{
		{Name: "tool_describe", Description: "describe a tool's input schema (lens)"},
		{Name: "tool_validate", Description: "validate args against a tool's schema (lens)"},
		{Name: "lesson_capture", Description: "remember a one-sentence lesson (lens)"},
		{Name: "card_show", Description: "render an envelope card"},
		{Name: "dev_grep", Description: "search files for a pattern"},
		{Name: "tesseract_recall", Description: "recall durable memory entries"},
		{Name: "tesseract_get", Description: "fetch an entry by key"},
	})
	return tc
}

func sortedNames(tools []llmtypes.ToolDefinition) []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t.Name
	}
	sort.Strings(out)
	return out
}

func sliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
