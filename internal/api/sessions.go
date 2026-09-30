package api

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	llmtypes "github.com/hollis-labs/go-llm-types"

	"github.com/hollis-labs/nanite/internal/chat"
	ctxpkg "github.com/hollis-labs/nanite/internal/context"
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
	lookup := buildEnvelopeLookup(a.Services.Store, messages)
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

// handleCompactSession runs the slot-aware compaction pipeline against the
// active conversation: drops dynamic context enrichment, summarizes the
// oldest messages via the configured summarizer, and strips tool-result
// blocks from older spans. The summary is persisted on the session and a
// slot_changed envelope is fanned out to any active chat stream so the UI
// can surface what just changed. The legacy 2000-char concat path and the
// destructive `messages.is_compacted` writes are gone.
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
	sessionID := r.PathValue("id")
	ctx := r.Context()

	session, err := a.Services.Sessions.Get(ctx, sessionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			a.errorResp(w, http.StatusNotFound, "session not found")
		} else {
			a.errorResp(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	agent, err := a.Services.Agents.ResolveForSession(ctx, sessionID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	settings, _ := a.Services.Store.GetUserSettings(ctx)
	windowSize := 0
	if settings != nil {
		windowSize = settings.ContextWindowTokens
	}

	result, err := a.Services.Context.AssembleSlots(ctx, session, agent, []llmtypes.ToolDefinition{}, "", windowSize, "")
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	summarizer := service.BuildSummarizer(a.Services.Providers, a.Services.Store, settings)
	mode := service.ClassifyCompactionMode(agent)
	pipeline := &ctxpkg.CompactionPipeline{
		Window:                result.Window,
		Estimator:             ctxpkg.DefaultEstimator{},
		Summarizer:            summarizer,
		Mode:                  mode,
		ConversationMessages:  result.Messages,
		SessionID:             sessionID,
		CompactionEventWriter: service.NewCompactionEventWriter(a.Services.Store),
	}

	tokensBefore := result.Window.UsedTokens()
	if a.Services.Events != nil {
		a.Services.Events.EmitPreCompact(ctx, sessionID, len(result.Messages), "manual")
	}
	cr, err := pipeline.RunForce(ctx)
	if err != nil {
		slog.Warn("api: compaction pipeline failed", "session_id", sessionID, "err", err)
		a.errorResp(w, http.StatusInternalServerError, fmt.Sprintf("compaction failed: %v", err))
		return
	}
	tokensAfter := result.Window.UsedTokens()
	tokensSaved := tokensBefore - tokensAfter
	if tokensSaved < 0 {
		tokensSaved = 0
	}

	summary := ""
	stages := []string{}
	if cr != nil {
		summary = cr.Summary
		stages = cr.StagesApplied
	}
	if summary == "" {
		// Stage 2 (summarize) is the only stage that produces a summary today.
		// When summarizer is unavailable Stage 2 skips, so derive a placeholder
		// from the manifest of stages that did fire so the session card can
		// still render something useful.
		if len(stages) > 0 {
			summary = fmt.Sprintf("Compaction applied %d stage(s); no LLM summary produced (summarizer unavailable).", len(stages))
		}
	}

	if a.Services.Events != nil {
		a.Services.Events.EmitPostCompact(ctx, sessionID, tokensSaved, stages)
	}

	if a.Services.Streams != nil {
		payload := chat.SlotChangedV1{
			Slot:         ctxpkg.SlotConversation,
			Change:       chat.SlotChangeSummarized,
			Reasoning:    fmt.Sprintf("/compact requested; %d stage(s) applied.", len(stages)),
			TokensBefore: tokensBefore,
			TokensAfter:  tokensAfter,
		}
		// Reuse the chat helper's marshaling+defaulting via a side channel:
		// we wrap the same emit logic by sending through a single-buffered
		// chan and forwarding to active streams.
		envCh := make(chan chat.StreamEvent, 1)
		if emitErr := chat.EmitSlotChangedEvent(envCh, payload); emitErr != nil {
			slog.Warn("api: slot_changed marshal failed", "session_id", sessionID, "err", emitErr)
		} else {
			evt := <-envCh
			a.Services.Streams.BroadcastSessionStreamEvent(sessionID, evt)
		}
	}

	a.jsonResp(w, http.StatusOK, map[string]any{
		"summary":        summary,
		"stages_applied": stages,
		"tokens_saved":   tokensSaved,
		"mode":           mode,
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
		lookup := buildEnvelopeLookup(a.Services.Store, page.Messages)
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
	lookup := buildEnvelopeLookup(a.Services.Store, page.Messages)
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
