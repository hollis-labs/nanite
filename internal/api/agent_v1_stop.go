package api

import (
	"net/http"
	"sync"

	"github.com/hollis-labs/nanite/internal/safego"
	"github.com/hollis-labs/nanite/internal/service"
)

type agentV1StopResponse struct {
	SessionViewID string `json:"session_view_id"`
	// State is always "ended" on success.
	State string `json:"state"`
	// Stopped is false when the session had already ended (a repeat call).
	Stopped bool `json:"stopped"`
	// TurnCanceled reports that a running or queued turn was canceled by this
	// call. A turn that had already finished is left as it ended.
	TurnCanceled bool `json:"turn_canceled"`
}

// handleAgentV1StopSession ends a session: it cancels any running or queued
// turn, closes the live agent runtime (the archive hook, which stops the CLI
// process), and marks the session ended. It is idempotent: a second call
// reports stopped=false and changes nothing. An ended session refuses new
// turns with 409 invalid_state.
func (a *API) handleAgentV1StopSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := a.Services.Sessions.Get(r.Context(), id); err != nil {
		a.agentV1LookupError(w, err)
		return
	}
	if _, _, err := a.Services.CognitiveViews.Get(r.Context(), id); err != nil {
		a.agentV1LookupError(w, err)
		return
	}
	// One stop at a time per session, so exactly one caller reports
	// stopped=true however many race.
	defer a.lockStop(id)()
	// Hold the conversation gate too: turn admission takes it, so a turn is
	// either registered before the cancel below or sees the session ended.
	// A long operation that holds the gate (a compaction) makes the stop wait
	// until it finishes or the request is canceled.
	if locker, ok := a.Services.Chat.(service.ConversationLocker); ok {
		release, err := locker.LockConversation(r.Context(), id)
		if err != nil {
			a.agentV1Error(w, http.StatusServiceUnavailable, "session is busy; retry the stop")
			return
		}
		defer release()
	}
	sess, err := a.Services.Sessions.Get(r.Context(), id)
	if err != nil {
		a.agentV1LookupError(w, err)
		return
	}
	if sess.Status == "archived" {
		a.jsonResp(w, http.StatusOK, agentV1StopResponse{SessionViewID: id, State: "ended"})
		return
	}
	// Archive first: from here a new turn is refused and no runtime can start
	// (the service checks the row where it starts one). Then cancel whatever was
	// already running.
	if err = a.endSession(r, id); err != nil {
		a.agentV1Error(w, http.StatusInternalServerError, "failed to end session")
		return
	}
	out := agentV1StopResponse{SessionViewID: id, State: "ended", Stopped: true}
	if a.Services.Chat != nil {
		out.TurnCanceled = a.Services.Chat.CancelActiveGeneration(id)
	}
	a.jsonResp(w, http.StatusOK, out)
}

// lockStop serializes stops per session and frees the entry when the last
// holder leaves, so the table does not grow with every stopped session.
func (a *API) lockStop(id string) (unlock func()) {
	a.stopMu.Lock()
	if a.stopLocks == nil {
		a.stopLocks = map[string]*stopLock{}
	}
	l := a.stopLocks[id]
	if l == nil {
		l = &stopLock{}
		a.stopLocks[id] = l
	}
	l.refs++
	a.stopMu.Unlock()
	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		a.stopMu.Lock()
		if l.refs--; l.refs == 0 {
			delete(a.stopLocks, id)
		}
		a.stopMu.Unlock()
	}
}

type stopLock struct {
	mu   sync.Mutex
	refs int
}

// endSession archives a session and tears down everything attached to it. It
// is the shared body of DELETE /api/sessions/{id} and the v1 stop route.
func (a *API) endSession(r *http.Request, id string) error {
	if err := a.Services.Sessions.Archive(r.Context(), id); err != nil {
		return err
	}
	if a.Services.ProcessTracker != nil {
		a.Services.ProcessTracker.KillSession(id)
	}
	a.Services.Streams.BroadcastSessionArchived(id)
	if a.Services.Plugins != nil {
		safego.Go(r.Context(), "api.sessions.emit.session-archived-stop", func() {
			a.Services.Plugins.EmitSessionArchived(id)
		})
	}
	return nil
}
