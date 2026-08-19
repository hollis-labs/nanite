package dispatch

import "testing"

// TestDefaultChatToolSurface_ExcludesLensPrimitives is the canonical Phase 2
// graduation guard: the four lens primitives must be filtered out of the
// chat surface. If anything trips this test, B4 has regressed and the chat
// surface is not narrowed.
func TestDefaultChatToolSurface_ExcludesLensPrimitives(t *testing.T) {
	surface := DefaultChatToolSurface()
	for _, name := range []string{"tool_describe", "tool_validate", "lesson_capture", "card_show"} {
		if surface.Filter(name) {
			t.Errorf("DefaultChatToolSurface.Filter(%q) = true; want false (lens primitive must be excluded)", name)
		}
	}
}

// TestDefaultChatToolSurface_AllowsOtherTools confirms the filter is a
// narrow exclusion, not a deny-by-default. Sample tools from the chat
// agent's expected post-handoff surface should pass through.
func TestDefaultChatToolSurface_AllowsOtherTools(t *testing.T) {
	surface := DefaultChatToolSurface()
	for _, name := range []string{
		"dev_grep",
		"dev_glob",
		"dev_read",
		"web_fetch",
		"memory_recall",
		"knowledge_get",
		"task_create",
		"message_send",
		"panel_open",
		"dispatch_executor",
	} {
		if !surface.Filter(name) {
			t.Errorf("DefaultChatToolSurface.Filter(%q) = false; want true (non-lens tool must pass)", name)
		}
	}
}

// TestChatToolSurface_DoesNotReintroduceWorkerOnlyTools is a regression
// guard: the prior dispatch.ChatSurfaceWorkerOnlyTools deny-list was
// emptied and removed in chat_surface_v2 (Phase 3 Stage 1, 2026-05-02).
// The new ChatToolSurface symbol must NOT re-add the subagent-spawn
// primitives — that would re-introduce the regression chat_surface_v2
// fixed.
func TestChatToolSurface_DoesNotReintroduceWorkerOnlyTools(t *testing.T) {
	surface := DefaultChatToolSurface()
	// Names from the prior worker-only deny-list. These are subagent-spawn
	// primitives that chat_surface_v2 explicitly cleared from chat-surface
	// filtering. They MUST remain on the chat surface here.
	for _, name := range []string{
		"nanite_run_python",
		"nanite_spawn_subagent",
		"run_python",
		"spawn_subagent",
	} {
		if !surface.Filter(name) {
			t.Errorf("DefaultChatToolSurface.Filter(%q) = false; want true (chat_surface_v2 emptied this deny-list — do not re-add)", name)
		}
	}
}

// TestChatToolSurface_IncludedAdditionsOverrideExclusions verifies the
// override semantics: a tool present in both maps stays on the surface.
// Today the two maps are disjoint, but the override is documented so future
// callers can rely on it.
func TestChatToolSurface_IncludedAdditionsOverrideExclusions(t *testing.T) {
	surface := &ChatToolSurface{
		ExcludedTools:     map[string]struct{}{"foo": {}},
		IncludedAdditions: map[string]struct{}{"foo": {}},
	}
	if !surface.Filter("foo") {
		t.Errorf("Filter(foo) with both excluded+included = false; want true (IncludedAdditions wins)")
	}
}

// TestChatToolSurface_NilReceiverPasses confirms the nil-safe behavior:
// a nil receiver disables filtering entirely. Callers building a one-off
// surface can pass nil rather than constructing an empty struct.
func TestChatToolSurface_NilReceiverPasses(t *testing.T) {
	var surface *ChatToolSurface
	for _, name := range []string{"tool_describe", "card_show", "anything_at_all"} {
		if !surface.Filter(name) {
			t.Errorf("(*ChatToolSurface)(nil).Filter(%q) = false; want true (nil = no filtering)", name)
		}
	}
}

// TestChatToolSurface_EmptyMapsPass confirms a zero-value struct (no
// exclusions, no inclusions) passes every tool through.
func TestChatToolSurface_EmptyMapsPass(t *testing.T) {
	surface := &ChatToolSurface{
		ExcludedTools:     map[string]struct{}{},
		IncludedAdditions: map[string]struct{}{},
	}
	for _, name := range []string{"tool_describe", "card_show", "any_other_tool"} {
		if !surface.Filter(name) {
			t.Errorf("zero-config ChatToolSurface.Filter(%q) = false; want true", name)
		}
	}
}
