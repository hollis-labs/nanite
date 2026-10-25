package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/agentpolicy"
)

// A host whose pinned definition asks for a read-only permission profile is
// refused by native execution. That refusal must not fail the whole agent list
// (GET /api/agents returned 500 on the agent-os database because of the
// Tachyon app-chat hosts); the host is listed as a marked stub, and GetAgent
// still refuses it.
func TestListAgentsListsUnsupportedHostAsMarkedStub(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	okPin, err := s.InstallAgentDefinition(ctx, definitionTestBytes("def:list-ok", "1", "Body."), nil)
	if err != nil {
		t.Fatal(err)
	}
	roData := strings.Replace(string(definitionTestBytes("def:list-readonly", "1", "Body.")), "profile: default", "profile: read-only", 1)
	roPin, err := s.InstallAgentDefinition(ctx, []byte(roData), nil)
	if err != nil {
		t.Fatal(err)
	}
	good, err := s.CreateAgentHostSettings(ctx, AgentHostSettings{Slug: "list-ok", Title: "Fine", DefinitionRef: okPin, Settings: NativeHostSettings{Version: "1", Runtime: "api"}, Enabled: true, Source: "explicit-authoring"})
	if err != nil {
		t.Fatal(err)
	}
	bad, err := s.CreateAgentHostSettings(ctx, AgentHostSettings{Slug: "list-readonly", Title: "Read only", DefinitionRef: roPin, Settings: NativeHostSettings{Version: "1", Runtime: "api"}, Enabled: true, Source: "explicit-authoring"})
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.ListAgents(ctx)
	if err != nil {
		t.Fatalf("one unsupported host failed the whole list: %v", err)
	}
	bySlug := map[string]AgentProfile{}
	for _, p := range got {
		bySlug[p.Slug] = p
	}
	if len(got) != 2 {
		t.Fatalf("want both hosts listed, got %d", len(got))
	}
	if p := bySlug["list-ok"]; p.ID != good.ID || p.UnsupportedSemantics != "" || p.Status != "active" {
		t.Errorf("supported host must be a normal entry: %+v", p)
	}
	stub := bySlug["list-readonly"]
	if stub.ID != bad.ID || stub.Status != "unsupported" || !strings.Contains(stub.UnsupportedSemantics, "not applied by native execution") {
		t.Errorf("unsupported host must be a marked stub: id=%s status=%q reason=%q", stub.ID, stub.Status, stub.UnsupportedSemantics)
	}
	if stub.DefinitionPolicy == nil || stub.NativeHost == nil {
		t.Error("stub must not carry nil policy/host pointers that callers dereference")
	}

	// A disabled unsupported host keeps the status every disabled filter keys on.
	if _, err := s.DisableAgentHostSettings(ctx, bad.ID, bad.Revision); err != nil {
		t.Fatal(err)
	}
	got, err = s.ListAgents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range got {
		if p.Slug == "list-readonly" && (p.Status != "disabled" || p.UnsupportedSemantics == "") {
			t.Errorf("disabled unsupported host: status=%q reason=%q", p.Status, p.UnsupportedSemantics)
		}
	}

	if _, err := s.GetAgent(ctx, bad.ID); !errors.Is(err, agentpolicy.ErrUnsupportedExecution) {
		t.Errorf("GetAgent must still refuse the unsupported host, got %v", err)
	}
}
