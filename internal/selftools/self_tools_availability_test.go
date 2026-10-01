package selftools

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/subagent"
)

func listedNames(t *testing.T, st *SelfToolsTransport) []string {
	t.Helper()
	tools, err := st.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.Name
	}
	return names
}

// TestHideUnwired_MatchesHandlerGuards pins selfToolNeeds to the handlers:
// every tool it names exists, is hidden on a bare transport, and answers a
// call there with its handler's own error rather than "unknown tool"
// (CW-20261001-0017). Calls are safe here: each of these handlers returns
// before doing anything when its collaborator is nil.
func TestHideUnwired_MatchesHandlerGuards(t *testing.T) {
	st := newSelfTools(t)
	st.HideUnwired = true
	listed := listedNames(t, st)
	hidden := st.HiddenTools()

	catalog := map[string]bool{}
	for _, tool := range selfToolDefinitions() {
		catalog[tool.Name] = true
	}
	for name := range selfToolNeeds {
		if !catalog[name] {
			t.Errorf("selfToolNeeds names %q, which is not a self tool", name)
			continue
		}
		if slices.Contains(listed, name) {
			t.Errorf("%q is listed on a bare transport", name)
		}
		if !slices.Contains(hidden, name) {
			t.Errorf("%q missing from HiddenTools", name)
		}
		res, err := st.CallTool(context.Background(), name, map[string]any{})
		if err != nil {
			t.Errorf("%q: CallTool error: %v", name, err)
			continue
		}
		text := ""
		if len(res.Content) > 0 {
			text = res.Content[0].Text
		}
		if !res.IsError || text == "" || strings.Contains(text, "unknown tool") {
			t.Errorf("%q on a bare transport = IsError %v %q, want its handler's own error", name, res.IsError, text)
		}
	}
	if len(listed)+len(hidden) != len(catalog) {
		t.Errorf("listed %d + hidden %d != catalog %d", len(listed), len(hidden), len(catalog))
	}
}

// Without HideUnwired the transport lists its whole catalog, as the live
// harness and the NANITE_API_URL proxy rely on.
func TestListTools_DefaultIsFullCatalog(t *testing.T) {
	st := newSelfTools(t)
	if got, want := len(listedNames(t, st)), len(selfToolDefinitions()); got != want {
		t.Fatalf("listed %d tools, want the full %d", got, want)
	}
	if hidden := st.HiddenTools(); hidden != nil {
		t.Fatalf("HiddenTools = %v, want none without HideUnwired", hidden)
	}
}

// The filter reads collaborator presence on every call, so wiring one
// brings its tools back without anything else changing.
func TestListTools_WiringACollaboratorListsItsTools(t *testing.T) {
	st := newSelfTools(t)
	st.HideUnwired = true
	if slices.Contains(listedNames(t, st), "subagent_spawn") {
		t.Fatal("subagent_spawn listed before the subagent service is wired")
	}
	st.Subagent = &subagent.Service{}
	listed := listedNames(t, st)
	for _, name := range []string{"subagent_spawn", "subagent_status", "subagent_cancel", "subagent_role_audit"} {
		if !slices.Contains(listed, name) {
			t.Errorf("%q not listed once the subagent service is wired", name)
		}
	}
	if slices.Contains(listed, "message_inbox") {
		t.Error("message_inbox listed while messaging is still unwired")
	}
}
