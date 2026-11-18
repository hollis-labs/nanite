package chatrunner_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	chat "github.com/hollis-labs/libs/agent/chat"
	"github.com/hollis-labs/libs/agent/chat/memstore"
	chatstream "github.com/hollis-labs/libs/ui/ui-go/chatstream"
	streamhub "github.com/hollis-labs/libs/ui/ui-go/streamhub"
	"github.com/hollis-labs/nanite/chatrunner"
)

type grantStore struct {
	mu     sync.Mutex
	grants []chat.Grant
}

func (g *grantStore) Grant(_ context.Context, grant chat.Grant) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.grants = append(g.grants, grant)
	return nil
}

// TestBehindTheChatService runs the adapter behind the real Service: a turn
// that needs approval is answered with a standing grant through the Service,
// and the stream a client reads is complete.
func TestBehindTheChatService(t *testing.T) {
	h := newHost(t)
	grants := &grantStore{}
	svc, err := chat.NewService(chat.Config{Runner: h.runner, Grants: grants, Log: streamhub.NewMemoryLog(), Control: memstore.NewControl(),
		Agents: func(context.Context, string) (chat.Agent, error) {
			return chat.Agent{Owner: "tester", Scope: "chat-test", Kind: "embedded"}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close(context.Background()) })
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	view, err := svc.EnsureConversation(ctx, "desk")
	if err != nil {
		t.Fatal(err)
	}
	if view.RunnerRef != chatrunner.ConversationID(view.ConversationID) {
		t.Errorf("runner_ref %q, want %q", view.RunnerRef, chatrunner.ConversationID(view.ConversationID))
	}
	att, _, err := svc.Attach(ctx, view.ConversationID, chat.RoleController, chat.StreamCommitted)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AcquireLease(ctx, view.ConversationID, att.ID); err != nil {
		t.Fatal(err)
	}
	stream, err := svc.Events(ctx, view.ConversationID, att.ID, chat.After(0))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := svc.Submit(ctx, view.ConversationID, chat.Turn{Text: "call:write_note"}); err != nil {
		t.Fatal(err)
	}

	var seen []string
	outcomes := 0
	for {
		ev, err := stream.Next(ctx)
		if err != nil {
			t.Fatalf("stream after %v: %v", seen, err)
		}
		seen = append(seen, string(ev.Verb))
		switch {
		case ev.Verb == chatstream.VerbApprovalRequest:
			if ev.ExpiresAt == nil {
				t.Error("the approval has no expiry")
			}
			if err := svc.AnswerApproval(ctx, view.ConversationID, ev.ApprovalID, att.ID, chat.Decision{Allow: true, Scope: chat.ScopeAlways}); err != nil {
				t.Fatalf("AnswerApproval: %v", err)
			}
		case ev.Verb == chatstream.VerbActivity && ev.Kind == chat.ActivityApproval:
			outcomes++
		case ev.Verb == chatstream.VerbPartEnd && len(ev.Final) == 0:
			t.Errorf("part.end of %q has no final on the stream", ev.PartID)
		}
		if ev.IsTerminal() {
			if ev.Verb != chatstream.VerbRunFinish {
				t.Fatalf("the run ended with %s %s", ev.Verb, ev.Message)
			}
			break
		}
	}
	if outcomes != 1 {
		t.Errorf("chat.approval was published %d times, want once", outcomes)
	}
	if h.ran("write_note") != 1 {
		t.Errorf("the approved tool ran %d times", h.ran("write_note"))
	}
	grants.mu.Lock()
	defer grants.mu.Unlock()
	if len(grants.grants) != 1 || !strings.Contains(string(grants.grants[0].Descriptor), `"tool":"write_note"`) {
		t.Fatalf("grants handed to the host: %+v", grants.grants)
	}
}
