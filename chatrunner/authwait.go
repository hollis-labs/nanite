package chatrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	chatstream "github.com/hollis-labs/libs/ui/ui-go/chatstream"
	"github.com/hollis-labs/nanite/agent"
)

// NoticeKindAuth is the kind of the notice the runner posts when a Warden
// grant request a turn waited on is decided.
const NoticeKindAuth = "auth"

// authWaitsKey is the agent-wide host state holding the grant requests the
// runner watches, so a restart keeps watching.
const authWaitsKey = "auth.waits"

const (
	defaultAuthPoll = 30 * time.Second
	maxAuthPoll     = 5 * time.Minute
)

// AuthStatus looks up a Warden grant request's status. The default asks
// Warden (agent.Agent.AuthRequest); a host that learns decisions some other
// way (a push from Gatecrasher or Warden) calls Runner.AuthDecided instead
// and can leave this to the default.
type AuthStatus func(ctx context.Context, requestID string) (string, error)

// authWait is one grant request turns are waiting on.
type authWait struct {
	RequestID string   `json:"request_id"`
	Audience  string   `json:"audience"`
	Scopes    []string `json:"scopes,omitempty"`
	For       string   `json:"for,omitempty"`
	// Chats maps each waiting Nanite conversation to its chat.
	Chats map[string]string `json:"chats"`
	Since time.Time         `json:"since"`
}

// authWaits tracks the grant requests the runner watches.
type authWaits struct {
	mu       sync.Mutex
	waits    map[string]*authWait
	watching map[string]bool
	saveMu   sync.Mutex
	wg       sync.WaitGroup
}

// onAuthWaiting sees each grant request a token request found missing. It
// runs inside the token request, so it only records and hands off.
func (r *Runner) onAuthWaiting(w agent.AuthWaiting) {
	if r.opts.Agent.OnAuthWaiting != nil {
		r.opts.Agent.OnAuthWaiting(w)
	}
	if w.RequestID == "" || w.Conversation == "" {
		return
	}
	chatID := r.chatID(w.Conversation)
	if chatID == "" {
		return // not a conversation this runner serves
	}
	a := &r.auth
	a.mu.Lock()
	if r.isClosed() {
		a.mu.Unlock()
		return
	}
	if a.waits == nil {
		a.waits, a.watching = map[string]*authWait{}, map[string]bool{}
	}
	wait := a.waits[w.RequestID]
	if wait == nil {
		wait = &authWait{RequestID: w.RequestID, Audience: w.Audience, Scopes: slices.Clone(w.Scopes), For: w.For, Chats: map[string]string{}, Since: time.Now().UTC()}
		a.waits[w.RequestID] = wait
	}
	_, known := wait.Chats[w.Conversation]
	wait.Chats[w.Conversation] = chatID
	start := !a.watching[w.RequestID]
	a.watching[w.RequestID] = true
	a.mu.Unlock()
	if known && !start {
		return // already announced on this chat and watched
	}
	go r.saveAuthWaits()
	if !known {
		r.announce(chatID, authWaitingActivity(w))
	}
	if start {
		r.watchAuth(w.RequestID)
	}
}

func authWaitingActivity(w agent.AuthWaiting) chatstream.Event {
	return activity(agent.ActivityAuthWaiting, map[string]any{
		"request_id": w.RequestID, "status": w.Status, "tier": w.Tier,
		"audience": w.Audience, "scopes": w.Scopes, "for": w.For,
	})
}

// watchAuth polls a grant request until it is decided or the runner closes:
// every AuthPoll at first, doubling up to five minutes.
func (r *Runner) watchAuth(id string) {
	ctx := r.noticeCtx
	if ctx == nil {
		return
	}
	r.auth.wg.Add(1)
	go func() {
		defer r.auth.wg.Done()
		wait := r.opts.AuthPoll
		if wait <= 0 {
			wait = defaultAuthPoll
		}
		limit := max(maxAuthPoll, wait)
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			status, err := r.authStatus(ctx, id)
			switch {
			case err != nil:
				if ctx.Err() == nil {
					r.logger().Warn("chatrunner: grant request lookup", "request", id, "error", err.Error())
				}
			case status != "" && status != "pending":
				if err := r.AuthDecided(ctx, id, status); err != nil && ctx.Err() == nil {
					r.logger().Warn("chatrunner: grant request decided", "request", id, "error", err.Error())
				}
				return
			}
			if !r.authWatched(id) {
				return // decided some other way
			}
			wait = min(wait*2, limit)
		}
	}()
}

func (r *Runner) authStatus(ctx context.Context, id string) (string, error) {
	if r.opts.AuthStatus != nil {
		return r.opts.AuthStatus(ctx, id)
	}
	w, err := r.agent.AuthRequest(ctx, id)
	return w.Status, err
}

func (r *Runner) authWatched(id string) bool {
	r.auth.mu.Lock()
	defer r.auth.mu.Unlock()
	return r.auth.waits[id] != nil
}

// AuthDecided records a Warden grant request's decision ("approved" or
// "granted", or a refusal such as "denied" or "expired") and tells each chat
// that waited on it with a notice: on approval the notice starts a notice
// turn (with Options.NoticeTurns) so the agent retries what needed the
// grant. The runner calls it when its own polling sees a decision; a host
// that is pushed decisions calls it directly. Deciding a request the runner
// does not watch does nothing.
func (r *Runner) AuthDecided(ctx context.Context, requestID, status string) error {
	if status == "" || status == "pending" {
		return fmt.Errorf("chatrunner: %q is not a decision", status)
	}
	a := &r.auth
	a.mu.Lock()
	wait := a.waits[requestID]
	delete(a.waits, requestID)
	delete(a.watching, requestID)
	a.mu.Unlock()
	if wait == nil {
		return nil
	}
	r.saveAuthWaits()
	approved := status == "approved" || status == "granted"
	if approved {
		r.agent.AuthDecided(requestID)
	}
	body := authDecisionText(wait, status, approved)
	var errs []error
	for naniteID := range wait.Chats {
		c, err := r.agent.Conversation(ctx, naniteID)
		if err == nil {
			err = c.PostNotice(ctx, NoticeKindAuth, body)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("conversation %s: %w", naniteID, err))
		}
	}
	return errors.Join(errs...)
}

func authDecisionText(w *authWait, status string, approved bool) string {
	what := "audience " + w.Audience
	if w.For != "" {
		what = w.For + " (" + what + ")"
	}
	if approved {
		return fmt.Sprintf("Warden approved grant request %s for %s. Continue the work that was waiting for it.", w.RequestID, what)
	}
	return fmt.Sprintf("Warden grant request %s for %s was %s. The work that needed it cannot continue until a new request is approved.", w.RequestID, what, status)
}

// saveAuthWaits writes the watched requests to host state, one save at a
// time, each with the latest set.
func (r *Runner) saveAuthWaits() {
	a := &r.auth
	a.saveMu.Lock()
	defer a.saveMu.Unlock()
	a.mu.Lock()
	var value string
	if len(a.waits) > 0 {
		raw, err := json.Marshal(a.waits)
		if err != nil {
			a.mu.Unlock()
			return
		}
		value = string(raw)
	}
	a.mu.Unlock()
	if err := r.agent.SetHostState(context.Background(), authWaitsKey, value); err != nil {
		r.logger().Warn("chatrunner: save grant requests", "error", err.Error())
	}
}

// loadAuthWaits restores the watched requests after a restart and watches
// them again.
func (r *Runner) loadAuthWaits(ctx context.Context) {
	raw, err := r.agent.HostState(ctx, authWaitsKey)
	if err != nil {
		r.logger().Warn("chatrunner: load grant requests", "error", err.Error())
		return
	}
	if strings.TrimSpace(raw) == "" {
		return
	}
	var waits map[string]*authWait
	if err := json.Unmarshal([]byte(raw), &waits); err != nil {
		r.logger().Warn("chatrunner: load grant requests", "error", err.Error())
		return
	}
	a := &r.auth
	a.mu.Lock()
	a.waits, a.watching = map[string]*authWait{}, map[string]bool{}
	var ids []string
	for id, w := range waits {
		if w == nil || w.RequestID != id || len(w.Chats) == 0 {
			continue
		}
		a.waits[id], a.watching[id] = w, true
		ids = append(ids, id)
	}
	a.mu.Unlock()
	r.mu.Lock()
	for _, id := range ids {
		for naniteID, chatID := range waits[id].Chats {
			if r.chats[naniteID] == "" {
				r.chats[naniteID] = chatID
			}
		}
	}
	r.mu.Unlock()
	for _, id := range ids {
		r.watchAuth(id)
	}
}

// authFor fills in what needed a grant request from the watched requests,
// when the failure carried only its text.
func (r *Runner) authFor(w agent.AuthWaiting) agent.AuthWaiting {
	if w.For != "" {
		return w
	}
	r.auth.mu.Lock()
	defer r.auth.mu.Unlock()
	if wait := r.auth.waits[w.RequestID]; wait != nil {
		w.For = wait.For
	}
	return w
}

// authWaitingText is the turn's error message when it failed for a pending
// grant.
func authWaitingText(w agent.AuthWaiting) string {
	what := "audience " + w.Audience
	if w.For != "" {
		what = w.For + " (" + what + ")"
	}
	return fmt.Sprintf("Waiting for approval of Warden grant request %s for %s. The agent continues once it is approved.", w.RequestID, what)
}
