package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	messaging "github.com/hollis-labs/libs/mesh/core/messaging"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/tethermail"
)

// TetherSender sends Tether mail on behalf of the enrolled actor attached to a
// session (CW-20261024-0021, MVP push). The caller's session comes from the MCP
// context, never from an argument, so an agent can only send as its own actor.
// It uses the bridge's service identity (NANITE_TETHER_TOKEN_FILE), so no
// per-agent Tether credential is needed; whether Tether accepts that identity
// sending as a substrate=nanite actor is a Tether contract question (observe
// mode today). Every send carries an Idempotency-Key.
//
// NOT A CONTRACT (Tether agent, 2026-10-29): in observe mode a service token may
// send with from = a substrate=nanite actor; it is recorded and attributed to the
// bridge token, not enforced. When Tether enforces sender identity
// (CW-20261024-0023), a shared token sending as many actors will need a scoped
// send-as grant for nanite actors; Tether is adding that to 0023's requirements.
type TetherSender struct {
	st   *store.Store
	mail *tethermail.Client
}

// newTetherSender returns nil unless NANITE_TETHER_ADDR is set (the bridge is on).
func newTetherSender(st *store.Store) *TetherSender {
	addr := os.Getenv(tetherBridgeAddrEnv)
	if addr == "" {
		return nil
	}
	var opts []tethermail.Option
	if tf := strings.TrimSpace(os.Getenv(tetherBridgeTokenFileEnv)); tf != "" {
		opts = append(opts, tethermail.WithTokenFile(tf))
	}
	c, err := tethermail.New(addr, opts...)
	if err != nil {
		return nil
	}
	return &TetherSender{st: st, mail: c}
}

// SendAsSessionActor sends a Tether message from the session's actor. It
// returns the Tether message id.
func (t *TetherSender) SendAsSessionActor(ctx context.Context, sessionID, to, subject, body, inReplyTo, threadID, idemKey string) (string, error) {
	if t == nil {
		return "", errors.New("tether_send: Tether is not configured on this server")
	}
	if strings.TrimSpace(sessionID) == "" {
		return "", errors.New("tether_send: no session in context")
	}
	from, err := t.st.ActorURNForSession(ctx, sessionID)
	if err != nil {
		return "", err
	}
	if from == "" {
		return "", errors.New("tether_send: this session has no enrolled Tether actor; enroll the agent first")
	}
	toAddr, err := messaging.ParseURN(to)
	if err != nil {
		return "", fmt.Errorf("tether_send: bad recipient %q: %w", to, err)
	}
	fromAddr, err := messaging.ParseURN(from)
	if err != nil {
		return "", fmt.Errorf("tether_send: bad actor urn: %w", err)
	}
	if strings.TrimSpace(body) == "" {
		return "", errors.New("tether_send: body is required")
	}
	payload, _ := json.Marshal(map[string]string{"subject": subject, "body": body})
	kind := messaging.Kind("request")
	if inReplyTo != "" {
		kind = messaging.Kind("response")
	}
	if idemKey == "" {
		// No key from the model: every call is its own send. A content-only key
		// would silently drop a repeated "ack" to the same recipient while the tool
		// said "sent". The key still protects the HTTP retry of THIS call (it is
		// fixed before Send), and a model that wants cross-call dedupe passes its own.
		var nonce [8]byte
		_, _ = rand.Read(nonce[:])
		h := sha256.Sum256([]byte(strings.Join([]string{sessionID, to, string(kind), threadID, inReplyTo, subject, body, hex.EncodeToString(nonce[:])}, "\x00")))
		idemKey = "nanite-" + hex.EncodeToString(h[:16])
	}
	out, err := t.mail.Send(ctx, messaging.Envelope{Kind: kind, From: fromAddr, To: toAddr, ThreadID: threadID, InReplyTo: inReplyTo, Payload: payload}, idemKey)
	if err != nil {
		return "", err
	}
	return out.ID, nil
}
