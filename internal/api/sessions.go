package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/hollis-labs/nanite/internal/safego"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

func (a *API) handleListSessions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	includeArchived := q.Get("include_archived") == "true"

	sessions, err := a.Services.Sessions.List(r.Context(), includeArchived)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, sessionsToView(sessions))
}

func (a *API) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req CreateSessionRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if req.SubagentRuntime != "" && !store.ValidSubagentRuntime(req.SubagentRuntime) {
		a.errorResp(w, http.StatusBadRequest, "subagent_runtime must be \"api\" or \"cli\"")
		return
	}

	// Validate even with no selection: the default profile resolves the
	// NANITE_HARNESS_* environment, and a bad value fails here, not every turn.
	meta, err := service.MergeHarnessSelection("", req.HarnessProfile, req.HarnessOverrides)
	if err == nil {
		err = service.ValidateHarnessSelection(a.Services.HarnessProfiles, meta)
	}
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	opts := service.CreateSessionOpts{
		ProjectID:       req.ProjectID,
		Model:           req.Model,
		Provider:        req.Provider,
		AgentID:         req.AgentID,
		SubagentRuntime: req.SubagentRuntime,
	}
	if req.HarnessProfile != "" || len(req.HarnessOverrides) > 0 {
		opts.Metadata = meta
	}
	// Create resolves the primary agent (request -> user-settings default ->
	// the real "default" row) and binds it best-effort.
	sess, err := a.Services.Sessions.Create(r.Context(), opts)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Emit session creation event (fire-and-forget).
	if a.Services.Activity != nil {
		safego.Go(r.Context(), "api.sessions.activity.session-created", func() {
			a.Services.Activity.EmitSessionCreated(r.Context(), sess.ID)
		})
	}

	a.jsonResp(w, http.StatusCreated, sessionToView(sess))
}

func (a *API) handleForkSession(w http.ResponseWriter, r *http.Request) {
	sourceID := r.PathValue("id")

	var req ForkSessionRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	newSess, err := a.Services.Sessions.Fork(r.Context(), sourceID, service.ForkOpts{
		IncludeMessages: req.IncludeMessages,
		Provider:        req.Provider,
		Model:           req.Model,
	})
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.jsonResp(w, http.StatusCreated, sessionToView(newSess))
}

func (a *API) handleGetSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, err := a.Services.Sessions.Get(r.Context(), id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "session not found")
		return
	}

	// Also return recent messages.
	messages, err := a.Services.Sessions.ListMessages(r.Context(), id, 50)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	lookup := a.Services.Sessions.EnvelopeLookup(r.Context(), messages)
	messages = injectEnvelopePriorResponses(messages, lookup)

	// A live stream means this process is genuinely generating the reply, so
	// the turn is not interrupted — see SessionService.DetectInterruptedTurn.
	hasLiveStream := a.Services.Streams != nil && a.Services.Streams.HasLiveStreamForSession(id)
	a.jsonResp(w, http.StatusOK, SessionDetailView{
		Session:         sessionToView(sess),
		Messages:        messagesToView(messages),
		InterruptedTurn: a.Services.Sessions.DetectInterruptedTurn(r.Context(), sess, hasLiveStream),
		ActiveMessageID: a.Services.Streams.ActiveMessageForSession(id),
	})
}

func (a *API) handleUpdateSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	existing, err := a.Services.Sessions.Get(r.Context(), id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "session not found")
		return
	}

	var req UpdateSessionRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if req.Title != nil {
		existing.Title = *req.Title
	}
	if req.CustomName != nil {
		existing.CustomName = *req.CustomName
	}
	if req.IsPinned != nil {
		existing.IsPinned = *req.IsPinned
	}
	if req.Provider != nil || req.Model != nil {
		if msgs, err := a.Services.Sessions.ListMessages(r.Context(), id, 1); err == nil && len(msgs) > 0 {
			if req.Provider != nil && *req.Provider != existing.Provider {
				a.errorResp(w, http.StatusBadRequest, "provider cannot be changed after the session has messages")
				return
			}
			if req.Model != nil && *req.Model != existing.Model {
				a.errorResp(w, http.StatusBadRequest, "model cannot be changed after the session has messages")
				return
			}
		}
	}
	if req.Model != nil {
		existing.Model = *req.Model
	}
	if req.Provider != nil {
		existing.Provider = *req.Provider
	}
	if req.Status != nil {
		switch *req.Status {
		case "active", "paused", "archived":
			existing.Status = *req.Status
		default:
			a.errorResp(w, http.StatusBadRequest, "status must be active, paused, or archived")
			return
		}
	}

	if err := a.Services.Sessions.Update(r.Context(), existing); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Emit plugin event when session is archived via update.
	if req.Status != nil && *req.Status == "archived" && a.Services.Plugins != nil {
		safego.Go(r.Context(), "api.sessions.emit.session-archived-update", func() {
			a.Services.Plugins.EmitSessionArchived(id)
		})
	}

	a.jsonResp(w, http.StatusOK, sessionToView(existing))
}

func (a *API) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// Archive closes the session's live agent runtime (the onArchive hook)
	// and emits session-end to activity and plugins.
	if err := a.Services.Sessions.Archive(r.Context(), id); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Kill any orphaned CLI processes for this session.
	if a.Services.ProcessTracker != nil {
		a.Services.ProcessTracker.KillSession(id)
	}

	// Broadcast session archived presence so UI updates immediately.
	a.Services.Streams.BroadcastSessionArchived(id)

	// Emit plugin event: session archived.
	if a.Services.Plugins != nil {
		safego.Go(r.Context(), "api.sessions.emit.session-archived-delete", func() {
			a.Services.Plugins.EmitSessionArchived(id)
		})
	}

	a.jsonResp(w, http.StatusOK, map[string]string{"archived": id})
}

// handleRebootSessionAgent reboots a single chat session's runtime agent
// (CW-20260516-0057). POST /api/sessions/{id}/agent/reboot.
//
// The next user turn cold-boots a fresh agent process + boot dir from the
// current binary; other sessions are untouched. Use it to pick up a freshly
// deployed binary or boot-dir change, or to recover one wedged agent,
// without the coarse all-sessions restart of nanite-api-service.
//
// Responses:
//   - 200 {rebooted, status} — reboot done, or status="no_active_agent"
//     when there was no live agent to stop (next turn boots fresh anyway).
//   - 404 — unknown session id.
//   - 409 — a turn is in flight for the session; retry once it settles.
func (a *API) handleRebootSessionAgent(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	ctx := r.Context()

	if _, err := a.Services.Sessions.Get(ctx, sessionID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			a.errorResp(w, http.StatusNotFound, "session not found")
		} else {
			a.errorResp(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	if a.Services.Chat == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "chat service not wired")
		return
	}

	result, err := a.Services.Chat.RebootSessionAgent(ctx, sessionID)
	if err != nil {
		if errors.Is(err, service.ErrSessionBusy) {
			a.errorResp(w, http.StatusConflict, err.Error())
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, result)
}

// handleRecoverSession evicts the session's live runtime so the next turn
// cold-boots into auto-recovery (recovery pack + provider resume) — the
// explicit user-triggered counterpart to the daemon-restart auto path, and
// distinct from a clean Reboot. CW-20260525-0001 Slice 2.
// POST /api/sessions/{id}/recover.
func (a *API) handleRecoverSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	ctx := r.Context()

	if _, err := a.Services.Sessions.Get(ctx, sessionID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			a.errorResp(w, http.StatusNotFound, "session not found")
		} else {
			a.errorResp(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	if a.Services.Chat == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "chat service not wired")
		return
	}

	result, err := a.Services.Chat.RecoverSession(ctx, sessionID)
	if err != nil {
		if errors.Is(err, service.ErrSessionBusy) {
			a.errorResp(w, http.StatusConflict, err.Error())
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	// The next turn plants the recovery pack / resumes the provider session.
	a.jsonResp(w, http.StatusOK, map[string]any{
		"recovered": result.Rebooted,
		"status":    result.Status,
		"note":      "recovery armed — the next message will resume prior context",
	})
}

func (a *API) handleCompactSession(w http.ResponseWriter, r *http.Request) {
	result, err := a.Services.CompactSession(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, service.ErrCompactionSessionNotFound) {
			a.errorResp(w, http.StatusNotFound, "session not found")
		} else {
			a.errorResp(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]any{
		"summary":        result.Summary,
		"stages_applied": result.StagesApplied,
		"tokens_saved":   result.TokensSaved,
		"mode":           result.Mode,
	})
}

func (a *API) handleListSessionMessages(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	q := r.URL.Query()

	limit := 50
	if l := q.Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}

	// "around" param: return a window centered on a specific message ID.
	if around := q.Get("around"); around != "" {
		before := 25
		after := 25
		if b := q.Get("before"); b != "" {
			if n, err := strconv.Atoi(b); err == nil && n >= 0 {
				before = n
			}
		}
		if af := q.Get("after"); af != "" {
			if n, err := strconv.Atoi(af); err == nil && n >= 0 {
				after = n
			}
		}
		page, err := a.Services.Sessions.ListMessagesAround(r.Context(), sessionID, around, before, after)
		if err != nil {
			a.errorResp(w, http.StatusInternalServerError, err.Error())
			return
		}
		lookup := a.Services.Sessions.EnvelopeLookup(r.Context(), page.Messages)
		page.Messages = injectEnvelopePriorResponses(page.Messages, lookup)
		a.jsonResp(w, http.StatusOK, messagePageToView(page))
		return
	}

	// Offset-based pagination.
	offset := 0
	if o := q.Get("offset"); o != "" {
		if n, err := strconv.Atoi(o); err == nil && n >= 0 {
			offset = n
		}
	}

	page, err := a.Services.Sessions.ListMessagesPage(r.Context(), sessionID, limit, offset)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	lookup := a.Services.Sessions.EnvelopeLookup(r.Context(), page.Messages)
	page.Messages = injectEnvelopePriorResponses(page.Messages, lookup)
	a.jsonResp(w, http.StatusOK, messagePageToView(page))
}

func (a *API) handleListSessionPluginEnvelopes(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	out, err := a.Services.Sessions.ListPendingEnvelopes(r.Context(), sessionID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, out)
}
