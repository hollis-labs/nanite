package agent

import (
	"testing"

	"github.com/hollis-labs/nanite/internal/provider/fake"
)

// Host state outlives a restart, is kept apart per conversation and from
// the agent-wide value, and "" removes it.
func TestHostStateOutlivesARestart(t *testing.T) {
	dir := agentDirOfKind(t, "kind: embedded\nlifetime: application\napplication_id: host-state-test\n")
	open := func() *Agent {
		a, err := Open(t.Context(), Options{Dir: dir, GlobalSkillsDir: "-", Provider: fake.New()})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	a := open()
	one, err := a.NewConversation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	two, err := a.NewConversation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []error{
		one.SetHostState(t.Context(), "desk", "one's"),
		two.SetHostState(t.Context(), "desk", "two's"),
		two.SetHostState(t.Context(), "other", "x"),
		a.SetHostState(t.Context(), "desk", "agent-wide"),
	} {
		if step != nil {
			t.Fatal(step)
		}
	}
	if err := one.SetHostState(t.Context(), "", "x"); err == nil {
		t.Fatal("an empty key was accepted")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}

	a = open()
	defer func() { _ = a.Close() }()
	all, err := a.ConversationHostStates(t.Context(), "desk")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[one.ID()] != "one's" || all[two.ID()] != "two's" {
		t.Fatalf("conversation states: %v", all)
	}
	if v, err := a.HostState(t.Context(), "desk"); err != nil || v != "agent-wide" {
		t.Fatalf("agent-wide: %q %v", v, err)
	}
	c, err := a.Conversation(t.Context(), two.ID())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetHostState(t.Context(), "desk", ""); err != nil {
		t.Fatal(err)
	}
	if v, err := c.HostState(t.Context(), "desk"); err != nil || v != "" {
		t.Fatalf("after removal: %q %v", v, err)
	}
	if v, _ := c.HostState(t.Context(), "other"); v != "x" {
		t.Fatalf("another key: %q", v)
	}
}
