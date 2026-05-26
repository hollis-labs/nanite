package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
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
	workspaceID := q.Get("workspace_id")
	if workspaceID == "" {
		a.errorResp(w, http.StatusBadRequest, "workspace_id query parameter is required")
		return
	}

	includeArchived := q.Get("include_archived") == "true"

	sessions, err := a.Services.Store.ListSessions(workspaceID, includeArchived)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, sessions)
}

func (a *API) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req CreateSessionRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.WorkspaceID == "" {
		a.errorResp(w, http.StatusBadRequest, "workspace_id is required")
		return
	}

	sess := &store.Session{
		WorkspaceID: req.WorkspaceID,
		ProjectID:   req.ProjectID,
		Model:       req.Model,
		Provider:    req.Provider,
	}
	if err := a.Services.Store.CreateSession(sess); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Resolve agent: request param → user settings default → file-default.
	agentID := req.AgentID
	if agentID == "" {
		if settings, err := a.Services.Store.GetUserSettings(); err == nil && settings.DefaultAgent != "" {
			agentID = settings.DefaultAgent
		}
	}
	if agentID == "" {
		agentID = "file-default"
	}

	// Assign the resolved agent as primary.
	if err := a.Services.Store.EnsureSessionAgent(sess.ID, agentID, "default", true); err != nil {
		// Log but don't fail — session was created successfully.
		_ = err
	}

	// Glass-4 (CW-20260502-0015): classify session intent for auto-handoff.
	// Deterministic-first per feedback.design_philosophy. Failure to classify
	// is non-fatal (intent stays NULL — no handoff flow for this session).
	if agent, err := a.Services.Store.GetAgent(agentID); err == nil {
		var sessionMode *store.Mode
		if m, err := a.Services.Store.GetSessionMode(sess.ID); err == nil {
			sessionMode = m
		}
		signals := service.SignalsFromSession(sess, agent, sessionMode)
		intent := service.ClassifySessionIntent(r.Context(), signals)
		if err := a.Services.Store.SetSessionIntent(sess.ID, intent); err != nil {
			slog.Warn("api: session intent classify+set failed (non-fatal)",
				"session_id", sess.ID, "intent", intent, "err", err)
		} else {
			slog.Info("api: session intent classified",
				"session_id", sess.ID, "intent", intent, "score", service.ScoreIntent(signals))
			intentVal := intent
			sess.Intent = &intentVal
		}
	}

	// Emit session creation event (fire-and-forget).
	if a.Services.Activity != nil {
		safego.Go(r.Context(), "api.sessions.activity.session-created", func() {
			a.Services.Activity.EmitSessionCreated(r.Context(), sess.ID, sess.WorkspaceID)
		})
	}

	a.jsonResp(w, http.StatusCreated, sess)
}

func (a *API) handleForkSession(w http.ResponseWriter, r *http.Request) {
	sourceID := r.PathValue("id")

	var req ForkSessionRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	overrides := &store.Session{
		Provider: req.Provider,
		Model:    req.Model,
	}
	// An explicit mode_id overrides the source session's mode on the fork;
	// empty falls through to the store default (inherit source's current_mode_id).
	if req.ModeID != "" {
		overrides.CurrentModeID = &req.ModeID
	}

	newSess, err := a.Services.Store.ForkSession(sourceID, overrides, req.IncludeMessages)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.jsonResp(w, http.StatusCreated, newSess)
}

func (a *API) handleGetSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, err := a.Services.Store.GetSession(id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "session not found")
		return
	}

	// Also return recent messages.
	messages, err := a.Services.Store.ListMessages(id, 50)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	lookup := buildEnvelopeLookup(a.Services.Store, messages)
	messages = injectEnvelopePriorResponses(messages, lookup)

	a.jsonResp(w, http.StatusOK, map[string]any{
		"session":          sess,
		"messages":         messages,
		"interrupted_turn": a.detectInterruptedTurn(id, sess),
	})
}

// detectInterruptedTurn reports whether the session has an in-flight turn whose
// backend agent is gone — the case where a service restart (deploy/reload)
// killed a turn mid-generation, leaving the GUI spinning forever with no
// indication anything went wrong (CW-20260518-0084).
//
// The signal is intentionally minimal and derived from existing state:
//
//   - The session's last persisted message is a `user` message. A completed
//     turn always ends with an `assistant` (or `tool`) row; a turn that started
//     but never produced a reply leaves the user message dangling.
//   - The process holds NO live in-memory stream for the session. During normal
//     generation the StreamManager always has a live stream for the message
//     being generated, so this is false for genuinely in-flight turns. After a
//     restart the StreamManager is a fresh empty instance, so a turn that was
//     generating at restart time reads as having no live stream.
//
// Both conditions together mean "a turn was dispatched, no reply landed, and
// nothing in this process is producing one" — i.e. the agent process is gone.
// Reconciling the dead agent_runtime rows is a separate task (CW-20260518-0085);
// this only surfaces the state so the FE can stop the endless spinner.
//
// Returns nil when the session is not in an interrupted state — the FE treats a
// null/absent field as "no interruption".
//
// PR #213 review hardening: this helper used to take the caller's messages
// slice and inspect `messages[len-1]`, which assumed the slice was the
// chronological tail. The current `handleGetSession` always passes the
// latest 50 (`ListMessages(id, 50)` is `ORDER BY created_at DESC LIMIT 50`
// reversed to ASC, so messages[-1] is in fact the absolute-latest message),
// but `ListMessagesPaginated` exists and a future endpoint passing a
// non-tail window would silently mis-trigger. Querying the store directly
// for the latest message eliminates the caller-slice dependency entirely.
func (a *API) detectInterruptedTurn(sessionID string, sess *store.Session) map[string]any {
	if sess == nil {
		return nil
	}
	// Only active sessions can have an in-flight turn; paused/archived ones
	// were deliberately put to rest.
	if sess.Status != "active" {
		return nil
	}
	// Probe the store directly for the chronologically-last message rather
	// than relying on a caller-supplied slice (ListMessages returns DESC then
	// reverses to ASC; with limit=1 the single returned element is the
	// absolute-latest row).
	tail, err := a.Services.Store.ListMessages(sessionID, 1)
	if err != nil || len(tail) == 0 {
		return nil
	}
	last := tail[len(tail)-1]
	if last.Role != "user" {
		return nil
	}
	// A live stream means this process is genuinely generating the reply —
	// not interrupted.
	if a.Services.Streams != nil && a.Services.Streams.HasLiveStreamForSession(sessionID) {
		return nil
	}
	return map[string]any{
		"interrupted":      true,
		"reason":           "service_restart",
		"last_message_id":  last.ID,
		"last_activity_at": last.CreatedAt,
	}
}

func (a *API) handleUpdateSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	existing, err := a.Services.Store.GetSession(id)
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
		if msgs, err := a.Services.Store.ListMessages(id, 1); err == nil && len(msgs) > 0 {
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

	if err := a.Services.Store.UpdateSession(existing); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Emit plugin event when session is archived via update.
	if req.Status != nil && *req.Status == "archived" && a.Services.Plugins != nil {
		safego.Go(r.Context(), "api.sessions.emit.session-archived-update", func() {
			a.Services.Plugins.EmitSessionArchived(id)
		})
	}

	a.jsonResp(w, http.StatusOK, existing)
}

func (a *API) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.Services.Store.ArchiveSession(id); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Kill any orphaned CLI processes for this session.
	if a.Services.ProcessTracker != nil {
		a.Services.ProcessTracker.KillSession(id)
	}

	// Broadcast session archived presence so UI updates immediately.
	a.Services.Streams.BroadcastSessionArchived(id)

	// Emit session ended event (fire-and-forget).
	if a.Services.Activity != nil {
		safego.Go(r.Context(), "api.sessions.activity.session-ended", func() {
			a.Services.Activity.EmitSessionEnded(r.Context(), id)
		})
	}

	// Emit plugin event: session archived.
	if a.Services.Plugins != nil {
		safego.Go(r.Context(), "api.sessions.emit.session-archived-delete", func() {
			a.Services.Plugins.EmitSessionArchived(id)
		})
	}

	a.jsonResp(w, http.StatusOK, map[string]string{"archived": id})
}

func (a *API) handleSwitchSessionMode(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")

	var req SwitchSessionModeRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Mode == "" {
		a.errorResp(w, http.StatusBadRequest, "mode is required")
		return
	}

	// Get the primary agent for this session.
	sa, err := a.Services.Store.GetSessionPrimaryAgent(sessionID)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "no primary agent for session")
		return
	}

	// Verify the mode exists for this agent.
	if _, err := a.Services.Store.GetAgentMode(sa.AgentID, req.Mode); err != nil {
		a.errorResp(w, http.StatusBadRequest, "unknown mode: "+req.Mode)
		return
	}

	previousMode := sa.Mode

	// Update the mode.
	if err := a.Services.Store.SetSessionAgentMode(sessionID, sa.AgentID, req.Mode); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Emit plugin event: mode changed.
	if a.Services.Plugins != nil {
		safego.Go(r.Context(), "api.sessions.emit.mode-changed", func() {
			a.Services.Plugins.EmitModeChanged(sessionID, previousMode, req.Mode)
		})
	}

	// Return updated session info.
	sess, err := a.Services.Store.GetSession(sessionID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.jsonResp(w, http.StatusOK, map[string]any{
		"session": sess,
		"mode":    req.Mode,
	})
}

// handleGetSessionMode returns the resolved session-level *store.Mode (B1,
// CW-20260428-0009). 200 with `null` body means the session has no
// session-mode pointer set — the legacy agent-scoped AgentMode is the active
// mode for that session's prompts.
func (a *API) handleGetSessionMode(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	mode, err := a.Services.Store.GetSessionMode(sessionID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, mode)
}

// handleSetSessionMode points a session at a specific mode by slug or mode_id
// (B1, CW-20260428-0009). Empty body or {slug:"", mode_id:""} clears the
// pointer. Returns the resolved mode (or null when cleared).
func (a *API) handleSetSessionMode(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")

	var req SetSessionModeRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	// Verify session exists up front so we don't silently no-op a clear on a
	// missing session.
	if _, err := a.Services.Store.GetSession(sessionID); err != nil {
		a.errorResp(w, http.StatusNotFound, "session not found")
		return
	}

	// Resolve target mode ID.
	modeID := req.ModeID
	if modeID == "" && req.Slug != "" {
		m, err := a.Services.Store.GetModeBySlug(req.Slug)
		if err != nil {
			a.errorResp(w, http.StatusInternalServerError, err.Error())
			return
		}
		if m == nil {
			a.errorResp(w, http.StatusBadRequest, "unknown mode slug: "+req.Slug)
			return
		}
		modeID = m.ID
	}

	if modeID == "" {
		// Clear path.
		if err := a.Services.Store.ClearSessionMode(sessionID); err != nil {
			a.errorResp(w, http.StatusInternalServerError, err.Error())
			return
		}
		// F1 (CW-20260429-0001): broadcast cross-tab so a second tab open on
		// the same session updates its mode chip without manual refetch.
		// Empty mode_id/mode_slug signal a clear (default chat mode).
		if a.Services.Streams != nil {
			a.Services.Streams.BroadcastSessionModeChanged(sessionID, "", "")
		}
		a.jsonResp(w, http.StatusOK, nil)
		return
	}

	// Verify mode_id resolves before writing the FK.
	if req.ModeID != "" {
		m, err := a.Services.Store.GetMode(modeID)
		if err != nil {
			a.errorResp(w, http.StatusInternalServerError, err.Error())
			return
		}
		if m == nil {
			a.errorResp(w, http.StatusBadRequest, "unknown mode_id: "+modeID)
			return
		}
	}

	if err := a.Services.Store.SetSessionMode(sessionID, modeID); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	resolved, err := a.Services.Store.GetSessionMode(sessionID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// F1 (CW-20260429-0001): broadcast the resolved mode so other tabs on
	// the same session pick up the change without polling. Reuses the
	// presence pipe — same channel as session_archived / work_changed.
	if a.Services.Streams != nil && resolved != nil {
		a.Services.Streams.BroadcastSessionModeChanged(sessionID, resolved.ID, resolved.Slug)
	}

	a.jsonResp(w, http.StatusOK, resolved)
}

// handleSetSessionAutoSwitch sets the per-session auto-switch override for
// classifier mode suggestions (F2, CW-20260429-0002).
//
//	PATCH /api/sessions/{id}/auto-switch
//	{ "override": true | false | null }
//
// `null` clears the override (session inherits user_settings.mode_auto_switch_pref).
// `true` forces ON for this session (does NOT bypass first-use prompt).
// `false` forces OFF for this session.
//
// Returns the persisted override on the session row.
func (a *API) handleSetSessionAutoSwitch(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")

	// Verify session exists up front so we don't silently no-op on a missing
	// session — same shape as handleSetSessionMode.
	if _, err := a.Services.Store.GetSession(sessionID); err != nil {
		a.errorResp(w, http.StatusNotFound, "session not found")
		return
	}

	// `encoding/json` decodes both `{}` (field absent) and `{"override": null}`
	// into a nil pointer, so a plain `*bool` field can't distinguish absent
	// from null. Decode into a raw map first, require the `override` key, then
	// unmarshal the value — clients that omit it get a 400 instead of a silent
	// override-clear (PR #93 Copilot feedback).
	var raw map[string]json.RawMessage
	if err := a.decode(r, &raw); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	rawOverride, present := raw["override"]
	if !present {
		a.errorResp(w, http.StatusBadRequest, "override field is required (use null to clear)")
		return
	}
	var override *bool
	if !bytes.Equal(bytes.TrimSpace(rawOverride), []byte("null")) {
		var b bool
		if err := json.Unmarshal(rawOverride, &b); err != nil {
			a.errorResp(w, http.StatusBadRequest, "override must be true, false, or null")
			return
		}
		override = &b
	}

	if err := a.Services.Store.SetSessionAutoSwitchOverride(sessionID, override); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.jsonResp(w, http.StatusOK, SessionAutoSwitchResponse{Override: override})
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

	agent, _, err := a.Services.Agents.ResolveForSession(ctx, sessionID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	var workspace *store.Workspace
	if session.WorkspaceID != "" {
		workspace, _ = a.Services.Store.GetWorkspace(session.WorkspaceID)
	}

	settings, _ := a.Services.Store.GetUserSettings()
	windowSize := 0
	if settings != nil {
		windowSize = settings.ContextWindowTokens
	}

	// B1 (CW-20260428-0009): manual /compact path doesn't need the session-
	// mode addendum (compaction operates on the existing window, not on a
	// new turn). Pass nil sessionMode — same as we pass nil AgentMode here.
	result, err := a.Services.Context.AssembleSlots(ctx, session, agent, nil, workspace, []llmtypes.ToolDefinition{}, "", windowSize, nil, "")
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	summarizer := service.BuildSummarizer(a.Services.Providers, a.Services.Store, settings)
	mode := service.ClassifyCompactionMode(agent)
	pipeline := &ctxpkg.CompactionPipeline{
		Window:               result.Window,
		Estimator:            ctxpkg.DefaultEstimator{},
		Summarizer:           summarizer,
		Mode:                 mode,
		ConversationMessages: result.Messages,
		// P7 HandoffStash: no loopState in HTTP path; empty scratchpad snapshot.
		SessionID:          sessionID,
		StashWriter:        service.NewStashWriter(a.Services.Store),
		ScratchpadSnapshot: map[string]any{},
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

	if err := a.Services.Store.UpdateSessionCompaction(sessionID, summary); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
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
		// Reuse the chat helper's marshalling+defaulting via a side channel:
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
		page, err := a.Services.Store.ListMessagesAroundID(sessionID, around, before, after)
		if err != nil {
			a.errorResp(w, http.StatusInternalServerError, err.Error())
			return
		}
		lookup := buildEnvelopeLookup(a.Services.Store, page.Messages)
		page.Messages = injectEnvelopePriorResponses(page.Messages, lookup)
		a.jsonResp(w, http.StatusOK, page)
		return
	}

	// Offset-based pagination.
	offset := 0
	if o := q.Get("offset"); o != "" {
		if n, err := strconv.Atoi(o); err == nil && n >= 0 {
			offset = n
		}
	}

	page, err := a.Services.Store.ListMessagesPaginated(sessionID, limit, offset)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	lookup := buildEnvelopeLookup(a.Services.Store, page.Messages)
	page.Messages = injectEnvelopePriorResponses(page.Messages, lookup)
	a.jsonResp(w, http.StatusOK, page)
}

func (a *API) handleListSessionPluginEnvelopes(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	insts, err := a.Services.Store.ListEnvelopeInstancesBySession(sessionID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	out := make([]chat.Envelope, 0, len(insts))
	for _, inst := range insts {
		if inst.RespondedAt != nil {
			continue
		}
		if inst.EnvelopeType != "subagent-spawn-approval" && inst.EnvelopeType != "elicitation-prompt" {
			continue
		}
		var data map[string]any
		if err := json.Unmarshal([]byte(inst.EnvelopeJSON), &data); err != nil {
			slog.Warn("api: skip malformed plugin-envelope rehydrate row",
				"session_id", sessionID,
				"envelope_id", inst.ID,
				"type", inst.EnvelopeType,
				"err", err,
			)
			continue
		}
		out = append(out, chat.Envelope{
			Kind:         "envelope",
			Version:      1,
			Type:         inst.EnvelopeType,
			ID:           inst.ID,
			Data:         data,
			DisplayClass: string(service.EnvelopeDisplayClassActionRequired),
		})
	}
	a.jsonResp(w, http.StatusOK, out)
}
