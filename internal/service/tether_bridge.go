package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	messaging "github.com/hollis-labs/libs/mesh/core/messaging"
	mailbox "github.com/hollis-labs/libs/mesh/core/messaging/mailbox"
	tether "github.com/hollis-labs/libs/mesh/core/tetherclient"

	"github.com/hollis-labs/nanite/internal/a2a"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/tetherbridge"
)

// tetherBridgeAddrEnv opts a Nanite process into Tether delivery (CW-20261022-0002).
// Its value is the Tether daemon address the client accepts (socket path or
// http URL); credentials come from the client's own TETHER_TOKEN / token-file
// resolution. Unset means the bridge is off and Nanite behaves as before.
const tetherBridgeAddrEnv = "NANITE_TETHER_ADDR"

// Tether mail enters the mailbox under Nanite's reserved external-sender
// participant (a2a.UserSentinel). The bridge cannot mint a sender: actor
// enrollment needs real issuer authority (AGENTS.md, Boundaries), and the
// mailbox refuses register_as. The real sender URN travels in the message
// body and metadata instead, so the agent can reply to it.
const tetherBridgeSenderSession = "tether"

// tetherInjector lands an accepted Tether delivery in the recipient's Nanite
// mailbox. Writing through mailbox.Service.SendMessage is deliberate: the
// existing messagingWakeReactor is then the one and only thing that decides
// whether to wake the session (message_wake_policy, reject-if-busy), for API
// and CLI sessions alike.
type tetherInjector struct {
	msgs *mailbox.Service
	chat *chatServiceImpl
}

func (i *tetherInjector) Busy(sessionID string) bool { return i.chat.IsGenerating(sessionID) }

func (i *tetherInjector) Inject(ctx context.Context, route tetherbridge.ActorRoute, env messaging.Envelope) (string, error) {
	subject, body := tetherEnvelopeText(env)
	meta, _ := json.Marshal(map[string]string{
		"source":             "tether",
		"tether_message_id":  env.ID,
		"tether_from":        env.From.URN(),
		"tether_in_reply_to": env.InReplyTo,
		"tether_thread_id":   env.ThreadID,
	})
	m, err := i.msgs.SendMessage(ctx, mailbox.SendInput{
		FromSessionID: tetherBridgeSenderSession,
		FromAgentID:   a2a.UserSentinel,
		ToSessionID:   route.SessionID,
		ToAgentID:     route.ProfileID,
		Subject:       subject,
		Body: fmt.Sprintf("[Tether message %s from %s]\n\n%s\n\nTo reply, send a Tether message to %s with in_reply_to=%s.",
			env.ID, env.From.URN(), body, env.From.URN(), env.ID),
		Metadata: string(meta),
	})
	if err != nil {
		return "", err
	}
	return m.ID, nil
}

// tetherEnvelopeText pulls a subject and body from a Tether payload, which is
// usually {"subject","body"} JSON but may be any content.
func tetherEnvelopeText(env messaging.Envelope) (subject, body string) {
	var p struct {
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	if json.Unmarshal(env.Payload, &p) == nil && (p.Body != "" || p.Subject != "") {
		return p.Subject, p.Body
	}
	return "", strings.TrimSpace(string(env.Payload))
}

// startTetherBridge starts the delivery loop when NANITE_TETHER_ADDR is set.
// It returns a stop func that cancels the loop and waits for it to exit; a
// nil stop means the bridge is off. A bad address is logged, not fatal: Tether
// being unreachable must not stop Nanite from serving.
func startTetherBridge(st *store.Store, msgs *mailbox.Service, chat *chatServiceImpl) (stop func()) {
	addr := os.Getenv(tetherBridgeAddrEnv)
	if addr == "" {
		return nil
	}
	client, err := tether.New(addr)
	if err != nil {
		slog.Error("tether bridge: client", "err", err)
		return nil
	}
	loop := &tetherbridge.Loop{
		Claimer:  client,
		Mail:     client.AsStore(),
		Store:    st,
		Actors:   st,
		Ledger:   st,
		Injector: &tetherInjector{msgs: msgs, chat: chat},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		loop.Run(ctx)
	}()
	slog.Info("service container: tether bridge started", "addr", addr)
	return func() {
		cancel()
		<-done
	}
}
