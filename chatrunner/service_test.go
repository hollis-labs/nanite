package chatrunner_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	chat "github.com/hollis-labs/libs/agent/chat"
	"github.com/hollis-labs/libs/agent/chat/memstore"
	chatstream "github.com/hollis-labs/libs/ui/ui-go/chatstream"
	streamhub "github.com/hollis-labs/libs/ui/ui-go/streamhub"
	"github.com/hollis-labs/nanite/agent"
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

// TestSubtaskApprovalBehindTheChatService: a subtask's write needs approval;
// it reaches the chat as one approval.request attributed to the subtask and
// attached to the root's subtask call. It takes only a once answer: "always"
// would be kept against the root chat and reach the root and the subtask's
// siblings, and the chat Service has no scope for one subtask. So a second
// subtask is asked again, and no standing grant is stored.
func TestSubtaskApprovalBehindTheChatService(t *testing.T) {
	h := newHost(t, agent.BuiltinSubtasks)
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

	turn := func(text string) (requests []chatstream.Event, subtaskDone []map[string]any) {
		t.Helper()
		if _, err := svc.Submit(ctx, view.ConversationID, chat.Turn{Text: text}); err != nil {
			t.Fatal(err)
		}
		for {
			ev, err := stream.Next(ctx)
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case ev.Verb == chatstream.VerbApprovalRequest:
				requests = append(requests, ev)
				for _, scope := range []chat.Scope{chat.ScopeAlways, chat.ScopeConversation} {
					if err := svc.AnswerApproval(ctx, view.ConversationID, ev.ApprovalID, att.ID, chat.Decision{Allow: true, Scope: scope}); !errors.Is(err, chat.ErrScopeUnavailable) {
						t.Fatalf("answer with scope %s: %v, want ErrScopeUnavailable", scope, err)
					}
				}
				if err := svc.AnswerApproval(ctx, view.ConversationID, ev.ApprovalID, att.ID, chat.Decision{Allow: true, Scope: chat.ScopeOnce}); err != nil {
					t.Fatalf("AnswerApproval: %v", err)
				}
			case ev.Verb == chatstream.VerbActivity && ev.Kind == chatrunner.ActivitySubagent:
				var v map[string]any
				_ = json.Unmarshal(ev.Value, &v)
				if v["state"] == "done" {
					subtaskDone = append(subtaskDone, v)
				}
			}
			if ev.IsTerminal() {
				if ev.Verb != chatstream.VerbRunFinish {
					t.Fatalf("the run ended with %s %s", ev.Verb, ev.Message)
				}
				return requests, subtaskDone
			}
		}
	}

	requests, done := turn("subtask:worker:call:write_note")
	if len(requests) != 1 {
		t.Fatalf("approval requests %d, want 1", len(requests))
	}
	var descriptor struct {
		Description, Tool string
		GrantScope        string `json:"grant_scope"`
		Standing          *bool
		Subtask           *agent.SubtaskOrigin
	}
	if err := json.Unmarshal(requests[0].Descriptor, &descriptor); err != nil {
		t.Fatal(err)
	}
	if descriptor.Tool != "write_note" || descriptor.Subtask == nil || descriptor.Subtask.Variant != "worker" ||
		descriptor.Subtask.Goal != "call:write_note" || !strings.HasPrefix(descriptor.Description, `subtask "call:write_note" wants write_note`) ||
		!strings.HasSuffix(descriptor.Description, "(this call only)") || descriptor.GrantScope != "once" ||
		descriptor.Standing == nil || *descriptor.Standing {
		t.Fatalf("descriptor %+v", descriptor)
	}
	if requests[0].CallID != "call-sub" {
		t.Fatalf("the card's call ID is %q, want the root's subtask call", requests[0].CallID)
	}
	// The test brain never calls return_result, so the subtask is partial.
	if len(done) != 1 || done[0]["status"] != "partial" || h.ran("write_note") != 1 {
		t.Fatalf("subtask %+v, write_note ran %d", done, h.ran("write_note"))
	}

	// A once answer covered only that call.
	requests, done = turn("subtask:worker:call:write_note")
	if len(requests) != 1 || len(done) != 1 || done[0]["status"] != "partial" || h.ran("write_note") != 2 {
		t.Fatalf("second subtask asked %d times, done %+v, write_note ran %d", len(requests), done, h.ran("write_note"))
	}
	grants.mu.Lock()
	defer grants.mu.Unlock()
	if len(grants.grants) != 0 {
		t.Fatalf("a subtask's card stored standing grants %+v", grants.grants)
	}
}
