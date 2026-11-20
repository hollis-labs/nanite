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
	defaultAuthPoll    = 30 * time.Second
	maxAuthPoll        = 5 * time.Minute
	defaultAuthWaitMax = 24 * time.Hour
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

// Clock and stagger, replaced by tests.
var (
	authAfter        = time.After
	authNow          = time.Now
	authRetryStagger = 250 * time.Millisecond
)

// onAuthWaiting sees each grant request a token request found missing. It
// runs inside the token request, so it only records and hands off.
func (r *Runner) onAuthWaiting(w agent.AuthWaiting) {
	if r.opts.Agent.AutoApprove {
		return
	}
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
	if a.waits == nil {
		a.waits, a.watching = map[string]*authWait{}, map[string]bool{}
	}
	wait := a.waits[w.RequestID]
	if wait == nil {
		wait = &authWait{RequestID: w.RequestID, Audience: w.Audience, Scopes: slices.Clone(w.Scopes), For: w.For, Chats: map[string]string{}, Since: authNow().UTC()}
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
	r.goAuth(r.saveAuthWaits)
	if !known {
		r.announce(chatID, authWaitingActivity(w))
	}
	if start {
		r.watchAuth(w.RequestID)
	}
}

// onAuthGranted sees a conversation that waited get its token: its own
// later turn succeeded, so it is no longer told about the decision.
func (r *Runner) onAuthGranted(w agent.AuthWaiting) {
	if r.opts.Agent.OnAuthGranted != nil {
		r.opts.Agent.OnAuthGranted(w)
	}
	a := &r.auth
	a.mu.Lock()
	wait := a.waits[w.RequestID]
	if wait == nil {
		a.mu.Unlock()
		return
	}
	delete(wait.Chats, w.Conversation)
	if len(wait.Chats) == 0 {
		// Nobody is left waiting: stop watching.
		delete(a.waits, w.RequestID)
		delete(a.watching, w.RequestID)
	}
	a.mu.Unlock()
	r.goAuth(r.saveAuthWaits)
}

// goAuth runs fn in the background, tracked so Close waits for it; after
// Close it does nothing.
func (r *Runner) goAuth(fn func()) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.auth.wg.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.auth.wg.Done()
		fn()
	}()
}

func authWaitingActivity(w agent.AuthWaiting) chatstream.Event {
	return activity(agent.ActivityAuthWaiting, map[string]any{
		"request_id": w.RequestID, "status": w.Status, "tier": w.Tier,
		"audience": w.Audience, "scopes": w.Scopes, "for": w.For,
	})
}

// watchAuth polls a grant request until it is decided, gone or too old, or
// the runner closes: every AuthPoll at first, doubling up to five minutes.
func (r *Runner) watchAuth(id string) {
	ctx := r.noticeCtx
	if ctx == nil {
		return
	}
	r.goAuth(func() {
		wait := r.opts.AuthPoll
		if wait <= 0 {
			wait = defaultAuthPoll
		}
		limit := max(maxAuthPoll, wait)
		maxAge := r.opts.AuthWaitMax
		if maxAge <= 0 {
			maxAge = defaultAuthWaitMax
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-authAfter(wait):
			}
			since, ok := r.authSince(id)
			if !ok {
				return // decided some other way, or nobody waits any more
			}
			status, err := r.authStatus(ctx, id)
			status = normalizeAuthStatus(status)
			switch {
			case ctx.Err() != nil:
				return
			case errors.Is(err, agent.ErrAuthRequestGone), errors.Is(err, agent.ErrNoWarden):
				r.stopAuthWatch(ctx, id, err.Error())
				return
			case err != nil:
				r.logger().Warn("chatrunner: grant request lookup", "request", id, "error", err.Error())
			case authDecision(status):
				if err := r.AuthDecided(ctx, id, status); err != nil && ctx.Err() == nil {
					r.logger().Warn("chatrunner: grant request decided", "request", id, "error", err.Error())
				}
				return
			case status != "" && status != "pending":
				r.logger().Warn("chatrunner: unknown grant request status", "request", id, "status", status)
			}
			if authNow().Sub(since) > maxAge {
				r.stopAuthWatch(ctx, id, fmt.Sprintf("no decision after %s", maxAge))
				return
			}
			wait = min(wait*2, limit)
		}
	})
}

func (r *Runner) authStatus(ctx context.Context, id string) (string, error) {
	if r.opts.AuthStatus != nil {
		return r.opts.AuthStatus(ctx, id)
	}
	w, err := r.agent.AuthRequest(ctx, id)
	return w.Status, err
}

// authSince is when the runner started watching id, false when it no
// longer does.
func (r *Runner) authSince(id string) (time.Time, bool) {
	r.auth.mu.Lock()
	defer r.auth.mu.Unlock()
	w := r.auth.waits[id]
	if w == nil {
		return time.Time{}, false
	}
	return w.Since, true
}

// takeAuthWait removes and returns a watched request, nil when it is not
// watched (so of a decision and a stop racing, only one acts).
func (r *Runner) takeAuthWait(id string) *authWait {
	a := &r.auth
	a.mu.Lock()
	wait := a.waits[id]
	delete(a.waits, id)
	delete(a.watching, id)
	a.mu.Unlock()
	if wait != nil {
		r.saveAuthWaits()
	}
	return wait
}

// stopAuthWatch gives up on a request that will not be decided for this
// agent, and tells each chat that waited.
func (r *Runner) stopAuthWatch(ctx context.Context, id, reason string) {
	wait := r.takeAuthWait(id)
	if wait == nil {
		return
	}
	r.agent.AuthDecided(id, "withdrawn") // forget it: the next attempt files anew
	body := fmt.Sprintf("Stopped watching Warden grant request %s for %s: %s. Send your message again to file a new request.", id, authWhat(wait.Audience, wait.For), reason)
	if err := r.tellAuthChats(ctx, wait, func(string) string { return body }, false); err != nil && ctx.Err() == nil {
		r.logger().Warn("chatrunner: grant request stopped", "request", id, "error", err.Error())
	}
}

// AuthDecided records a Warden grant request's decision ("approved",
// "denied", "expired" or "withdrawn") and notifies each chat still waiting.
// Statuses are trimmed and case-insensitive; "granted" means "approved".
// On approval the notice starts a notice
// turn (with Options.NoticeTurns, while the chat has notice turns left) so
// the agent retries what needed the grant; otherwise it asks the owner to
// send the message again. A chat whose own later turn already got the token
// is not told. The runner calls it when its own polling sees a decision; a
// host that is pushed decisions calls it directly. Deciding a request the
// runner does not watch does nothing.
func (r *Runner) AuthDecided(ctx context.Context, requestID, status string) error {
	status = normalizeAuthStatus(status)
	if !authDecision(status) {
		return fmt.Errorf("chatrunner: %q is not a decision", status)
	}
	wait := r.takeAuthWait(requestID)
	if wait == nil {
		return nil
	}
	r.agent.AuthDecided(requestID, status)
	approved := status == "approved"
	what := authWhat(wait.Audience, wait.For)
	return r.tellAuthChats(ctx, wait, func(naniteID string) string {
		switch {
		case !approved:
			return fmt.Sprintf("Warden grant request %s for %s was %s. The work that needed it cannot continue until a new request is approved.", requestID, what, status)
		case r.noticeTurnAvailable(naniteID):
			return fmt.Sprintf("Warden approved grant request %s for %s. Continue the work that was waiting for it.", requestID, what)
		default:
			return fmt.Sprintf("Warden approved grant request %s for %s. Send your message again to continue.", requestID, what)
		}
	}, approved)
}

func normalizeAuthStatus(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "granted" {
		return "approved"
	}
	return status
}

// authDecision excludes pending and unknown statuses without consuming a watch.
func authDecision(status string) bool {
	switch status {
	case "approved", "denied", "expired", "withdrawn":
		return true
	}
	return false
}

// tellAuthChats posts a notice to each chat that waited, in a stable order,
// spacing retries out so many chats do not all start turns at once.
func (r *Runner) tellAuthChats(ctx context.Context, wait *authWait, body func(naniteID string) string, stagger bool) error {
	ids := make([]string, 0, len(wait.Chats))
	for id := range wait.Chats {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	var errs []error
	for i, naniteID := range ids {
		if stagger && i > 0 {
			select {
			case <-ctx.Done():
				return errors.Join(append(errs, ctx.Err())...)
			case <-authAfter(authRetryStagger):
			}
		}
		c, err := r.agent.Conversation(ctx, naniteID)
		if err == nil {
			err = c.PostNotice(ctx, NoticeKindAuth, body(naniteID))
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("conversation %s: %w", naniteID, err))
		}
	}
	return errors.Join(errs...)
}

// noticeTurnAvailable reports whether a notice on the conversation would
// start a notice turn now.
func (r *Runner) noticeTurnAvailable(naniteID string) bool {
	if !r.opts.NoticeTurns {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.noticeTurns[naniteID] < maxNoticeTurns
}

func authWhat(audience, label string) string {
	what := "audience " + audience
	if label != "" {
		what = label + " (" + what + ")"
	}
	return what
}

// saveAuthWaits writes the watched requests to host state, one save at a
// time, each with the latest set. After Close it writes nothing.
func (r *Runner) saveAuthWaits() {
	a := &r.auth
	a.saveMu.Lock()
	defer a.saveMu.Unlock()
	if r.isClosed() {
		return
	}
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

// loadAuthWaits restores the watched requests after a restart, merging with
// any registered meanwhile, and watches them again.
func (r *Runner) loadAuthWaits(ctx context.Context) {
	if r.opts.Agent.AutoApprove {
		return
	}
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
	if a.waits == nil {
		a.waits, a.watching = map[string]*authWait{}, map[string]bool{}
	}
	var start []string
	chats := map[string]string{}
	for id, w := range waits {
		if w == nil || w.RequestID != id || len(w.Chats) == 0 {
			continue
		}
		if cur := a.waits[id]; cur != nil {
			for naniteID, chatID := range w.Chats {
				if _, ok := cur.Chats[naniteID]; !ok {
					cur.Chats[naniteID] = chatID
				}
			}
		} else {
			a.waits[id] = w
		}
		for naniteID, chatID := range w.Chats {
			chats[naniteID] = chatID
		}
		if !a.watching[id] {
			a.watching[id] = true
			start = append(start, id)
		}
	}
	a.mu.Unlock()
	r.mu.Lock()
	for naniteID, chatID := range chats {
		if r.chats[naniteID] == "" {
			r.chats[naniteID] = chatID
		}
	}
	r.mu.Unlock()
	for _, id := range start {
		r.watchAuth(id)
	}
}

// authWaitingText is the run error of a turn that failed for a pending
// grant: what it waits on, and what happens next for this conversation.
func (r *Runner) authWaitingText(naniteID string, w agent.AuthWaiting) string {
	r.auth.mu.Lock()
	wait := r.auth.waits[w.RequestID]
	watched := false
	if wait != nil {
		if w.For == "" {
			w.For = wait.For
		}
		_, watched = wait.Chats[naniteID]
	}
	r.auth.mu.Unlock()
	next := "Send your message again once it is approved."
	switch {
	case watched && r.opts.NoticeTurns:
		next = "The agent continues once it is approved."
	case watched:
		next = "You will get a notice when it is decided; then send your message again."
	}
	return fmt.Sprintf("Waiting for approval of Warden grant request %s for %s. %s", w.RequestID, authWhat(w.Audience, w.For), next)
}
