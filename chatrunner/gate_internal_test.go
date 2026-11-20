package chatrunner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	chat "github.com/hollis-labs/libs/agent/chat"
	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/internal/provider/fake"
)

func openRunner(t *testing.T, tweaks ...func(*Options)) *Runner {
	t.Helper()
	dir := t.TempDir()
	definition := "---\nschema_version: \"2\"\ndefinition_id: def:chat-test\nrevision: \"1\"\nname: chat-test\ndescription: Chat test.\nbehavior:\n  purpose: Test.\nrequirements: {}\nharness_profile:\n  permissions:\n    profile: default\ncontinuity:\n  mode: durable\n---\nTest instructions.\n"
	profile := "kind: embedded\nlifetime: application\napplication_id: chat-test\nruntime:\n  provider: anthropic\n  model: test-model\n"
	for name, text := range map[string]string{"agent.md": definition, "host.yaml": profile} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	opts := Options{Agent: agent.Options{Dir: dir, Provider: fake.New(fake.Text("ok")), GlobalSkillsDir: "-"}}
	for _, tweak := range tweaks {
		tweak(&opts)
	}
	r, err := Open(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

// A Nanite conversation keeps its chat conversation between turns, so a
// background subtask's call evaluated then still gets the chat's ID.
func TestChatIDBetweenTurns(t *testing.T) {
	r := openRunner(t)
	if _, err := r.conversation(t.Context(), "conv_between"); err != nil {
		t.Fatal(err)
	}
	if r.activeRun(ConversationID("conv_between")) != nil {
		t.Fatal("a run is active")
	}
	if got := r.chatID(ConversationID("conv_between")); got != "conv_between" {
		t.Fatalf("chatID between turns = %q", got)
	}
	if got := r.chatID("chat-unknown"); got != "" {
		t.Fatalf("chatID of a conversation never served = %q", got)
	}
}

// A controller's answer is a person's (Human); an expiry the Service issued
// is not.
func TestGateMarksHumanAnswers(t *testing.T) {
	for _, tc := range []struct {
		name  string
		d     chat.Decision
		human bool
	}{
		{"controller deny", chat.Decision{}, true},
		{"controller allow", chat.Decision{Allow: true}, true},
		{"expired", chat.Decision{Reason: chat.ReasonExpired}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := openRunner(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			x := &run{runner: r, nanite: "chat-c1", turn: chat.Turn{ConversationID: "c1"}, pending: map[string]*pending{}, asked: map[string]bool{}, ctx: ctx, stop: cancel}
			r.mu.Lock()
			r.active["chat-c1"] = x
			r.mu.Unlock()
			got := make(chan agent.PermissionDecision, 1)
			go func() {
				d, _ := gate{r}.Decide(ctx, agent.PermissionRequest{ID: "call-1", ConversationID: "chat-c1", Tool: "write_note", Input: json.RawMessage(`{}`)})
				got <- d
			}()
			p := x.approval("call-1")
			<-p.settled
			p.answer <- tc.d
			if d := <-got; d.Human != tc.human || d.Allow != tc.d.Allow {
				t.Fatalf("decision %+v, want human %v", d, tc.human)
			}
		})
	}
}
