package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"

	messaging "github.com/hollis-labs/libs/mesh/core/messaging"
	mailbox "github.com/hollis-labs/libs/mesh/core/messaging/mailbox"
	tether "github.com/hollis-labs/libs/mesh/core/tetherclient"

	"github.com/hollis-labs/nanite/internal/a2a"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/tetherbridge"
	"github.com/hollis-labs/nanite/internal/tethermail"
)

// tetherBridgeAddrEnv opts a Nanite process into Tether delivery (CW-20261022-0002).
// Its value is the Tether daemon address the client accepts (socket path or
// http URL); credentials come from the client's own TETHER_TOKEN / token-file
// resolution. Unset means the bridge is off and Nanite behaves as before.
const tetherBridgeAddrEnv = "NANITE_TETHER_ADDR"

// tetherBridgeTokenFileEnv names a 0600 file holding the bridge's own Tether
// service token. Tether service tokens are longer than the client accepts in
// TETHER_TOKEN, so a file is the way to give the bridge its scoped identity.
const tetherBridgeTokenFileEnv = "NANITE_TETHER_TOKEN_FILE"

// tetherBridgeAllowEnv optionally restricts which Tether senders may wake an
// actor: a comma-separated list of URNs or prefixes ending in "*". Whatever it
// says, a sender must be verified by Tether.
const tetherBridgeAllowEnv = "NANITE_TETHER_ALLOW_FROM"

// Tether mail enters the mailbox under the reserved Tether participant
// (a2a.TetherSentinel), not as the user and not as an actor. The bridge cannot
// enroll an actor (AGENTS.md, Boundaries) and the mailbox refuses register_as.
// The real sender URN and Tether message id travel in the message metadata, and
// the body is rendered so sender text cannot pass as bridge-written framing.
const tetherBridgeSenderSession = "tether"

// tetherInjector lands an accepted Tether delivery in the recipient's Nanite
// mailbox. Writing through mailbox.Service.SendMessage is deliberate: the
// existing messagingWakeReactor is then the one and only thing that decides
// whether to wake the session (message_wake_policy, reject-if-busy), for API
// and CLI sessions alike.
//
// Inject is idempotent on the Tether message id: a mailbox row already carrying
// that id for the same slot is returned instead of writing a second one. That
// is what makes a crash or ledger failure between the mailbox write and the
// ledger write harmless.
type tetherInjector struct {
	db   *sql.DB
	msgs *mailbox.Service
	chat *chatServiceImpl
}

func (i *tetherInjector) Busy(sessionID string) bool { return i.chat.IsGenerating(sessionID) }

func (i *tetherInjector) Inject(ctx context.Context, route tetherbridge.ActorRoute, env messaging.Envelope) (string, error) {
	var existing string
	err := i.db.QueryRowContext(ctx,
		`SELECT id FROM agent_messages
		  WHERE to_session_id = ? AND to_agent_id = ? AND from_agent_id = ?
		    AND json_extract(metadata, '$.tether_message_id') = ?
		  LIMIT 1`,
		route.SessionID, route.ProfileID, a2a.TetherSentinel, env.ID).Scan(&existing)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("tether injector: look up existing delivery: %w", err)
	}

	from := oneLine(env.From.URN())
	meta, _ := json.Marshal(map[string]string{
		"source":             "tether",
		"tether_message_id":  env.ID,
		"tether_from":        from,
		"tether_principal":   oneLine(env.Metadata["from_principal"]),
		"tether_verified":    oneLine(env.Metadata["from_verification"]),
		"tether_in_reply_to": env.InReplyTo,
		"tether_thread_id":   env.ThreadID,
	})
	m, err := i.msgs.SendMessage(ctx, mailbox.SendInput{
		FromSessionID: tetherBridgeSenderSession,
		FromAgentID:   a2a.TetherSentinel,
		ToSessionID:   route.SessionID,
		ToAgentID:     route.ProfileID,
		Subject:       "Tether message from " + from,
		Body:          renderTetherBody(env, i.sessionFacts(ctx, route.SessionID)),
		Metadata:      string(meta),
	})
	if err != nil {
		return "", err
	}
	// The wake reactor only turns a mailbox row into a turn when the session's
	// message-wake policy is auto_summarize. A definition-bound session takes that
	// policy from its pinned definition, which does not opt in, so Tether mail for
	// an enrolled actor would sit unread. Mail for an actor is meant to be acted
	// on, so when the reactor will not wake the session the bridge does it.
	if i.chat.resolveMessageWakePolicy(ctx, route.SessionID) != chat.SubagentPolicyAutoSummarize {
		// A native turn offers tools only to a submitting credential with standing
		// (R1, re-checked on every dispatch). The bridge turn is submitted on behalf of
		// the actor, so the credential that enrolled it is the principal; the tools are
		// still only those the host's native authority grants. No enrollment, no
		// principal: the turn runs tool-free, as any turn without a credential does.
		wakeCtx := ctx
		var principal string
		if qerr := i.db.QueryRowContext(ctx,
			`SELECT e.principal FROM agent_actor_bindings b
			   JOIN agent_enrollments e ON e.host_settings_id = b.host_settings_id
			  WHERE b.actor_uri = ? AND b.enabled = 1 AND e.status = 'enrolled' LIMIT 1`, route.ProfileID).Scan(&principal); qerr == nil && principal != "" {
			wakeCtx = WithSubmittedBy(ctx, principal)
		}
		if _, werr := i.chat.TriggerMessageWake(wakeCtx, route.SessionID, m); werr != nil {
			slog.Warn("tether bridge: wake turn for delivered message failed", "session_id", route.SessionID, "message_id", m.ID, "err", werr)
		}
	}
	return m.ID, nil
}

// renderTetherBody frames the message. The two framing lines are the bridge's;
// every line of the sender's text is prefixed "> " so a body that imitates the
// header still reads as quoted sender content.
func renderTetherBody(env messaging.Envelope, facts string) string {
	subject, body := tetherEnvelopeText(env)
	var b strings.Builder
	fmt.Fprintf(&b, "[Tether message %s from %s]\n", oneLine(env.ID), oneLine(env.From.URN()))
	if subject != "" {
		b.WriteString(quoteLines("subject: " + subject))
	}
	b.WriteString(quoteLines(body))
	fmt.Fprintf(&b, "[end of sender text. Reply with a Tether message to %s, in_reply_to=%s]",
		oneLine(env.From.URN()), oneLine(env.ID))
	if facts != "" {
		// Bridge-written facts: a model cannot know its own deployment, and a sender
		// may ask which runtime or model answered.
		fmt.Fprintf(&b, "\n[Nanite facts about this session: %s. Use these, not your own guess, if asked what you run on.]", facts)
	}
	return b.String()
}

func quoteLines(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		b.WriteString("> ")
		b.WriteString(strings.TrimRight(line, "\r"))
		b.WriteByte('\n')
	}
	return b.String()
}

func oneLine(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
}

// tetherSenderPolicy decides which Tether senders may wake a bound actor. A
// sender must carry Tether's own verification; when allow is non-empty it must
// also match an entry (exact URN, or a prefix ending in "*") by sender URN or
// verified principal.
func tetherSenderPolicy(allow []string) func(messaging.Envelope) error {
	return tetherSenderPolicyWith(allow, nil)
}

// tetherBridgeTrustUnverifiedEnv names exact sender URNs the bridge accepts even
// though Tether did not verify them (comma-separated, exact match only, empty by
// default). It exists for the operator's own address: the agent-mux operator UI
// (msg://user/agent-mux/operator) sends mail Tether marks with no verification.
// RISK: until Tether enforces sender identity, an unverified `from` is whatever
// the sender claimed, so a listed URN can be spoofed by anything able to send
// unverified mail on the local Tether. List only addresses whose spoofing you
// accept, and still pair it with NANITE_TETHER_ALLOW_FROM.
const tetherBridgeTrustUnverifiedEnv = "NANITE_TETHER_TRUST_UNVERIFIED_FROM"

// tetherSenderPolicyWith is tetherSenderPolicy plus the unverified-trust list.
func tetherSenderPolicyWith(allow, trustUnverified []string) func(messaging.Envelope) error {
	return func(env messaging.Envelope) error {
		if env.Metadata["from_verification"] != "verified" {
			trusted := false
			for _, u := range trustUnverified {
				if env.From.URN() == u {
					trusted = true
					break
				}
			}
			if !trusted {
				return fmt.Errorf("%w: sender %s is not verified by Tether", tetherbridge.ErrSenderRejected, oneLine(env.From.URN()))
			}
			slog.Info("tether bridge: accepting an unverified sender from the trust list", "from", oneLine(env.From.URN()), "message_id", env.ID)
		}
		if len(allow) == 0 {
			return nil
		}
		for _, id := range []string{env.From.URN(), env.Metadata["from_principal"]} {
			for _, pat := range allow {
				if id == pat || (strings.HasSuffix(pat, "*") && strings.HasPrefix(id, strings.TrimSuffix(pat, "*"))) {
					return nil
				}
			}
		}
		return fmt.Errorf("%w: sender %s is not on the allowlist", tetherbridge.ErrSenderRejected, oneLine(env.From.URN()))
	}
}

// redactAddr drops credentials and query strings before an address is logged.
func redactAddr(addr string) string {
	u, err := url.Parse(addr)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return addr // a socket path; nothing to strip
	}
	return u.Scheme + "://" + u.Host + u.Path
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
func startTetherBridge(st *store.Store, msgs *mailbox.Service, chat *chatServiceImpl, waker tetherbridge.Waker) (stop func()) {
	addr := os.Getenv(tetherBridgeAddrEnv)
	if addr == "" {
		return nil
	}
	client, err := tether.New(addr, tetherBridgeClientOptions(os.Getenv(tetherBridgeTokenFileEnv))...)
	if err != nil {
		slog.Error("tether bridge: client", "addr", redactAddr(addr), "err", strings.ReplaceAll(err.Error(), addr, redactAddr(addr)))
		return nil
	}
	// Mail is read through the non-destructive list + watch client (never the
	// destructive Inbox); the libs client still carries the claim/receipt calls.
	var mailOpts []tethermail.Option
	if tf := strings.TrimSpace(os.Getenv(tetherBridgeTokenFileEnv)); tf != "" {
		mailOpts = append(mailOpts, tethermail.WithTokenFile(tf))
	}
	mail, err := tethermail.New(addr, mailOpts...)
	if err != nil {
		slog.Error("tether bridge: mail client", "addr", redactAddr(addr), "err", strings.ReplaceAll(err.Error(), addr, redactAddr(addr)))
		return nil
	}
	loop := &tetherbridge.Loop{
		Claimer:     client,
		Mail:        tetherMailSource{mail},
		Cursors:     st,
		Waker:       waker,
		Store:       st,
		Actors:      st,
		Ledger:      st,
		Injector:    &tetherInjector{db: st.DB, msgs: msgs, chat: chat},
		AllowSender: tetherSenderPolicyWith(splitList(os.Getenv(tetherBridgeAllowEnv)), splitList(os.Getenv(tetherBridgeTrustUnverifiedEnv))),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		loop.Run(ctx) // returns after its subscription goroutines have exited
	}()
	slog.Info("service container: tether bridge started", "addr", redactAddr(addr))
	return func() {
		cancel()
		<-done
	}
}

// tetherBridgeClientOptions adds the token file when one is configured; with
// none, the client resolves credentials as before.
func tetherBridgeClientOptions(tokenFile string) []tether.Option {
	if tokenFile = strings.TrimSpace(tokenFile); tokenFile == "" {
		return nil
	}
	return []tether.Option{tether.WithTokenFile(tokenFile)}
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// tetherMailSource adapts the tethermail client to the bridge's MailSource.
type tetherMailSource struct{ c *tethermail.Client }

func (m tetherMailSource) List(ctx context.Context, to string, unreadOnly bool, limit, offset int) ([]messaging.Envelope, int, error) {
	return m.c.List(ctx, to, tethermail.ListOptions{UnreadOnly: unreadOnly, Limit: limit, Offset: offset})
}

func (m tetherMailSource) Watch(ctx context.Context, to string, afterSeq int64) (<-chan tetherbridge.Event, error) {
	in, err := m.c.Watch(ctx, to, afterSeq, tethermail.WatchOptions{})
	if err != nil {
		return nil, err
	}
	out := make(chan tetherbridge.Event)
	go func() {
		defer close(out)
		for ev := range in {
			select {
			case out <- tetherbridge.Event{Kind: string(ev.Kind), Seq: ev.Seq, Env: ev.Env}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

// sessionFacts states the session's actual provider and model (and the host's
// runtime), from Nanite's own records, for the delivery framing.
func (i *tetherInjector) sessionFacts(ctx context.Context, sessionID string) string {
	var provider, model string
	if err := i.db.QueryRowContext(ctx, `SELECT COALESCE(provider,''), COALESCE(model,'') FROM sessions WHERE id = ?`, sessionID).Scan(&provider, &model); err != nil {
		return ""
	}
	if provider == "" && model == "" {
		return ""
	}
	return fmt.Sprintf("provider=%s, model=%s", oneLine(provider), oneLine(model))
}
