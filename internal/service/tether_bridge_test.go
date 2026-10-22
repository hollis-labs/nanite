package service

import (
	"encoding/json"
	"testing"

	messaging "github.com/hollis-labs/libs/mesh/core/messaging"
	mailbox "github.com/hollis-labs/libs/mesh/core/messaging/mailbox"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/store/mailboxadapter"
	"github.com/hollis-labs/nanite/internal/tetherbridge"
)

func TestTetherEnvelopeText(t *testing.T) {
	subj, body := tetherEnvelopeText(messaging.Envelope{Payload: json.RawMessage(`{"subject":"S","body":"B"}`)})
	if subj != "S" || body != "B" {
		t.Fatalf("got %q %q", subj, body)
	}
	_, body = tetherEnvelopeText(messaging.Envelope{Payload: json.RawMessage(`"plain"`)})
	if body != `"plain"` {
		t.Fatalf("raw fallback = %q", body)
	}
}

// The injector's job is to put Tether mail into the recipient's real Nanite
// mailbox under the (session, profile) slot the bridge resolved.
func TestTetherInjector_WritesRecipientMailbox(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	ctx := t.Context()
	actor := &store.AgentProfile{Name: "Tether actor", Slug: "tether-actor", SystemPrompt: "x"}
	if err := persistTestActor(ctx, st, actor); err != nil {
		t.Fatal(err)
	}
	svc := mailboxadapter.New(st).Service
	inj := &tetherInjector{msgs: svc}

	env := messaging.Envelope{
		ID:      "tether-m1",
		From:    messaging.Address{Kind: messaging.KindAgent, Authority: "agent-mux", ID: "sender"},
		Payload: json.RawMessage(`{"subject":"Hello","body":"please look"}`),
	}
	id, err := inj.Inject(ctx, tetherbridge.ActorRoute{SessionID: "sess-1", ProfileID: actor.ID}, env)
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	got, err := svc.Inbox(ctx, "sess-1", actor.ID, mailbox.InboxFilter{}, "sess-1", actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != id {
		t.Fatalf("inbox = %+v, want the injected message %s", got, id)
	}
}
