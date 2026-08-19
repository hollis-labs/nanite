package api

import (
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
	"github.com/hollis-labs/nanite/internal/recovery"
	"github.com/hollis-labs/nanite/internal/safego"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

func (a *API) handleListSessions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	includeArchived := q.Get("include_archived") == "true"

	sessions, err := a.Services.Store.ListSessions(includeArchived)
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

	sess := &store.Session{
		ProjectID: req.ProjectID,
		Model:     req.Model,
		Provider:  req.Provider,
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

	// Emit session creation event (fire-and-forget).
	if a.Services.Activity != nil {
		safego.Go(r.Context(), "api.sessions.activity.session-created", func() {
			a.Services.Activity.EmitSessionCreated(r.Context(), sess.ID)
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
	// A live stream means this process is genuinely generating the reply —
	// not interrupted. The pure "dangling user turn + no live stream" decision
	// lives in internal/recovery (interrupted-turn detection, the fourth of
	// the four recovery mechanisms); this method's job is just the store/
	// stream lookups that feed it.
	hasLiveStream := a.Services.Streams != nil && a.Services.Streams.HasLiveStreamForSession(sessionID)
	result := recovery.DetectInterruptedTurn(last.Role, last.ID, last.CreatedAt, hasLiveStream)
	if result != nil {
		a.logInterruptedTurnDetected(sessionID, last, result)
	}
	return result
}

// interruptedTurnDetectedMeta is the structured event_log.metadata payload
// for event_type="interrupted_turn_detected" — the session/turn context
// that triggered the heuristic, not a bare event-type string. Mirrors the
// shape convention chat_reflexes.go's "reflex_action" write established
// (docs/engineering/architecture/06-session-lifecycle-and-recovery.md:
// "extend event_log logging to all four [recovery mechanisms]").
type interruptedTurnDetectedMeta struct {
	SessionID       string `json:"session_id"`
	LastMessageID   string `json:"last_message_id"`
	LastMessageRole string `json:"last_message_role"`
	LastActivityAt  string `json:"last_activity_at"`
	Reason          string `json:"reason"`
}

// logInterruptedTurnDetected writes the event_log postmortem row for a real
// interrupted-turn detection firing (a GET /sessions/{id} that finds a
// dangling unanswered user turn with no live stream, per
// DetectInterruptedTurn above). Best-effort — a.Services.Store.LogEvent
// already swallows its own DB errors; this only degrades to a skipped
// write if Store is nil (never true in production wiring).
func (a *API) logInterruptedTurnDetected(sessionID string, last store.Message, result map[string]any) {
	if a.Services.Store == nil {
		return
	}
	reason, _ := result["reason"].(string)
	meta := interruptedTurnDetectedMeta{
		SessionID:       sessionID,
		LastMessageID:   last.ID,
		LastMessageRole: last.Role,
		LastActivityAt:  last.CreatedAt,
		Reason:          reason,
	}
	blob, err := json.Marshal(meta)
	if err != nil {
		blob = []byte("{}")
	}
	a.Services.Store.LogEvent(sessionID, "interrupted_turn_detected", "recovery",
		fmt.Sprintf("interrupted turn detected: last message %s (%s) has no reply and no live stream", last.ID, last.Role),
		string(blob))
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

	settings, _ := a.Services.Store.GetUserSettings()
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
		Window:               result.Window,
		Estimator:            ctxpkg.DefaultEstimator{},
		Summarizer:           summarizer,
		Mode:                 mode,
		ConversationMessages: result.Messages,
		SessionID:            sessionID,
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
