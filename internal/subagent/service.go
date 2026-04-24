package subagent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/hollis-labs/nanite/internal/messaging"
	"github.com/hollis-labs/nanite/internal/safego"
	"github.com/hollis-labs/nanite/internal/store"
)

// DefaultTimeoutSeconds bounds a runner when the caller doesn't set
// a timeout. 5 minutes matches the plan's §T9 spawn-request default.
const DefaultTimeoutSeconds = 300

// Typed errors returned by Approve and Reject so callers can
// distinguish rejection causes without string matching.
var (
	ErrNotPending      = errors.New("subagent: run is not in requested state")
	ErrApprovalExpired = errors.New("subagent: approval has expired")
)

// Runner executes the subagent's work and returns a structured
// Result. Implementations own the actual chat-engine invocation,
// agent role loading, and any MCP tool calls. The subagent package
// stays ignorant of the chat engine so this dependency lives in
// one place (the container wires the real runner; tests wire a
// stub).
//
// Runner MUST respect ctx cancellation — spawn.Cancel propagates
// via context.Cancel to trigger an in-flight runner to bail.
type Runner interface {
	Run(ctx context.Context, run *Run) (*Result, error)
}

// MessagePoster is the subset of messaging.Service that subagent
// needs to deliver a reply back to the parent. Narrow interface
// keeps this package testable without spinning up a whole messaging
// stack; *messaging.Service satisfies it structurally.
type MessagePoster interface {
	SendMessage(ctx context.Context, input messaging.SendInput) (*messaging.Message, error)
}

// ApprovalEmitter persists an envelope instance and pushes the envelope onto
// the parent session's live stream. Container-injected so the subagent package
// stays independent of the envelope + stream subsystems. Returns the generated
// envelope instance ID so Spawn can denormalize it onto the run row.
type ApprovalEmitter interface {
	Emit(ctx context.Context, sessionID, envelopeType string, payload []byte) (envelopeID string, err error)
}

// SettingsReader returns the per-user settings. Container-injected so the
// subagent package doesn't take a hard dep on the full Store.
type SettingsReader interface {
	GetUserSettings() (*store.UserSettings, error)
}

// Service coordinates the spawn → run → complete → reply flow.
// Safe for concurrent use.
type Service struct {
	db         *sql.DB
	runner     Runner
	poster     MessagePoster
	approver   ApprovalEmitter
	settings   SettingsReader
	streamSink SubagentStreamSink

	// cancelers holds a per-run context.CancelFunc keyed by runID so
	// Cancel(runID) can propagate cancellation into the in-flight
	// runner — not just flip the DB row. Spawn registers; execute's
	// defer clears; Cancel invokes-and-deletes. Mutex-guarded.
	cancelMu  sync.Mutex
	cancelers map[string]context.CancelFunc
}

// NewService constructs a Service. The db is used for subagent_runs
// CRUD. The runner is the injected LLM-execution dependency (nil =
// no spawn permitted — Spawn returns an error). The poster delivers
// the reply message on completion (nil = reply skipped, run result
// is still visible via Status). The approver emits approval envelopes
// onto the parent session's stream (nil = approval emission disabled;
// T10 will wire the real impl). The settings reader provides per-user
// settings for gating decisions (nil = only ModeInteractive triggers
// gating; T6 wires cfg.Store).
func NewService(db *sql.DB, runner Runner, poster MessagePoster, approver ApprovalEmitter, settings SettingsReader) *Service {
	return &Service{
		db:        db,
		runner:    runner,
		poster:    poster,
		approver:  approver,
		settings:  settings,
		cancelers: make(map[string]context.CancelFunc),
	}
}

// SetStreamSink wires (or unwires) the G-5 status-event sink. Pass nil
// to disable emission. Safe to call before any Spawn.
func (svc *Service) SetStreamSink(s SubagentStreamSink) { svc.streamSink = s }

// emitStatus marshals the run's current state into a JSON payload and
// hands it to the configured sink. summaryPreview is passed in because
// result.Summary is not persisted on the Run row — for the running
// transition pass "", for terminal transitions pass result.Summary
// (or "" on failure).
func (svc *Service) emitStatus(run *Run, summaryPreview string) {
	if svc.streamSink == nil {
		return
	}
	payload, err := json.Marshal(map[string]any{
		"run_id":           run.ID,
		"role":             run.Role,
		"status":           run.Status,
		"child_session_id": run.ChildSessionID,
		"error":            run.Error,
		"summary_preview":  truncate(summaryPreview, 80),
	})
	if err != nil {
		slog.Warn("subagent: marshal status payload", "err", err, "run_id", run.ID)
		return
	}
	svc.streamSink.SubagentStatusChanged(run.ParentSessionID, payload)
}

// Spawn inserts a subagent_runs row and (for sync/api/async MVP
// modes) dispatches the runner immediately. Returns the runID; sync
// mode blocks until the runner returns, async mode returns straight
// away and the caller can poll Status or watch for the reply
// message.
//
// Happy-path approval: the interactive approval envelope described
// in plan §D13 is DEFERRED. All three modes auto-approve for MVP —
// the distinction between them lives only in the reply-delivery
// channel (sync/api → chat, async → inbox) and blocking semantics
// (sync blocks, async/api return early).
func (svc *Service) Spawn(ctx context.Context, req SpawnRequest) (string, error) {
	if svc.runner == nil {
		return "", errors.New("subagent: no runner configured")
	}
	if req.ParentSessionID == "" {
		return "", errors.New("subagent: parent_session_id required")
	}
	if req.Role == "" {
		return "", errors.New("subagent: role required")
	}
	if req.Prompt == "" {
		return "", errors.New("subagent: prompt required")
	}
	mode := req.Mode
	if mode == "" {
		mode = ModeSync
	}
	switch mode {
	case ModeSync, ModeAsync, ModeAPI, ModeInteractive:
	default:
		return "", fmt.Errorf("subagent: invalid mode %q", mode)
	}
	timeout := req.TimeoutSeconds
	if timeout <= 0 {
		timeout = DefaultTimeoutSeconds
	}
	inputs := req.InputsJSON
	if inputs == "" {
		inputs = "{}"
	}

	run := &Run{
		ID:              uuid.New().String(),
		ParentSessionID: req.ParentSessionID,
		ParentAgentID:   req.ParentAgentID,
		Role:            req.Role,
		Prompt:          req.Prompt,
		Mode:            mode,
		InputsJSON:      inputs,
		TimeoutSeconds:  timeout,
		CreatedAt:       time.Now().UTC().Format(time.RFC3339Nano),
	}

	// Gate predicate: consult user settings + mode.
	gate := false
	if svc.settings != nil {
		us, err := svc.settings.GetUserSettings()
		if err != nil {
			return "", fmt.Errorf("load settings: %w", err)
		}
		gate = us.SubagentApprovalRequired || mode == ModeInteractive
	} else {
		// Nil settings = tests that don't care about gating; fall through to
		// ungated unless mode explicitly requests interactive approval.
		gate = mode == ModeInteractive
	}

	if gate {
		// Gated path: validate prerequisites + marshal payload BEFORE any DB
		// write so a misconfiguration (nil emitter) or marshal error doesn't
		// leave a row orphaned in 'requested'. Post-insert failures (Emit,
		// envelope-id persist) compensate by transitioning the run to
		// 'failed' with an error reason — we preserve the audit trail
		// rather than deleting.
		if svc.approver == nil {
			return "", fmt.Errorf("gated spawn requires approver but none configured")
		}
		payload, err := json.Marshal(map[string]any{
			"run_id":          run.ID,
			"role":            run.Role,
			"prompt":          run.Prompt,
			"mode":            run.Mode,
			"parent_agent_id": run.ParentAgentID,
			"timeout_seconds": run.TimeoutSeconds,
			"inputs_json":     run.InputsJSON,
			"risk_level":      "medium",
		})
		if err != nil {
			return "", fmt.Errorf("marshal approval payload: %w", err)
		}

		run.Status = StatusRequested
		if err := svc.insertRun(ctx, run); err != nil {
			return "", fmt.Errorf("insert run: %w", err)
		}

		envelopeID, err := svc.approver.Emit(ctx, run.ParentSessionID, "subagent-spawn-approval", payload)
		if err != nil {
			svc.markGatedSpawnFailed(run.ID, "approval envelope emission failed: "+err.Error())
			return "", fmt.Errorf("emit approval envelope: %w", err)
		}

		if _, uerr := svc.db.ExecContext(ctx,
			`UPDATE subagent_runs SET envelope_instance_id=? WHERE id=?`, envelopeID, run.ID); uerr != nil {
			svc.markGatedSpawnFailed(run.ID, "envelope id persist failed: "+uerr.Error())
			return "", fmt.Errorf("persist envelope id: %w", uerr)
		}
		run.EnvelopeInstanceID = envelopeID

		svc.emitStatus(run, "")
		return run.ID, nil
	}

	// Ungated path — existing behavior preserved exactly.
	// Auto-approve only when the gate predicate above is false
	// (that is, approval is not required and mode is not interactive).
	run.Status = StatusRunning
	run.StartedAt = run.CreatedAt
	if err := svc.insertRun(ctx, run); err != nil {
		return "", fmt.Errorf("insert run: %w", err)
	}

	// G-5: emit running event so the parent UI can render "subagent
	// spawned" before the runner does any work.
	svc.emitStatus(run, "")

	switch mode {
	case ModeSync:
		// Blocking: caller holds until the runner returns. The
		// parent's next turn can then reason over the reply. We
		// derive the runner ctx from the caller's ctx (so caller
		// cancellation propagates) but ALSO register the derived
		// CancelFunc so a Cancel(runID) from another goroutine
		// unblocks the in-flight runner.
		execCtx, execCancel := context.WithCancel(ctx)
		svc.cancelMu.Lock()
		svc.cancelers[run.ID] = execCancel
		svc.cancelMu.Unlock()
		svc.execute(execCtx, run, req.ParentAgentID)
	case ModeAsync, ModeAPI:
		// Non-blocking: fire-and-forget goroutine. The reply lands
		// in the parent session's inbox (async) or chat (api).
		// Background-derived ctx because the caller's request ctx
		// will likely be done by the time the runner finishes;
		// Cancel(runID) is the only intended cancellation path.
		runCtx, runCancel := context.WithCancel(context.Background())
		svc.cancelMu.Lock()
		svc.cancelers[run.ID] = runCancel
		svc.cancelMu.Unlock()
		safego.Go(context.Background(), "subagent.run", func() {
			svc.execute(runCtx, run, req.ParentAgentID)
		})
	}

	return run.ID, nil
}

// markGatedSpawnFailed transitions a just-inserted 'requested' run to
// 'failed' with an error reason when a post-insert step in gated Spawn
// (envelope Emit, envelope_instance_id persist) fails. Compensating
// update — uses Background ctx so it survives a cancelled caller, and
// matches on status=requested to avoid clobbering a concurrent
// Approve/Reject. Errors are swallowed: the caller is already returning
// an error to its own caller; double-reporting helps no one.
func (svc *Service) markGatedSpawnFailed(runID, reason string) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, _ = svc.db.ExecContext(context.Background(),
		`UPDATE subagent_runs
		   SET status=?, error=?, completed_at=?
		 WHERE id=? AND status=?`,
		StatusFailed, reason, now, runID, StatusRequested)
}

// Status returns the current Run row for runID, or ErrNotFound
// (wrapped sql.ErrNoRows) if no such run exists.
func (svc *Service) Status(ctx context.Context, runID string) (*Run, error) {
	row := svc.db.QueryRowContext(ctx, selectSQL+` WHERE id = ?`, runID)
	return scanRun(row)
}

// Cancel marks a run as cancelled and unblocks its runner. Ordering
// is load-bearing: on the success path we UPDATE the DB row to
// cancelled FIRST, then invoke the registered CancelFunc. This makes
// cancellation deterministic — a concurrent finalizeRun from the
// unblocked runner sees status = cancelled (not in the (running,
// requested, approved) guard set) and no-ops. finalizeRun's re-read
// branch then patches the in-memory Run so the G-5 terminal emit
// reports "cancelled" (not the "failed" that execute writes after
// ctx.Err()).
//
// CancelFunc invocation is deferred so the runner still unblocks even
// if the DB UPDATE errors (e.g., caller ctx times out). We'd rather
// return the error to the caller AND stop the runner than leak the
// goroutine while surfacing the DB failure. On that error path the
// row ends up "failed" via the runner's own finalizeRun (which finds
// status = running and writes cleanly), but the runner does not leak.
//
// Idempotent: a second Cancel finds the row already terminal and the
// map entry already cleared.
func (svc *Service) Cancel(ctx context.Context, runID string) error {
	defer func() {
		svc.cancelMu.Lock()
		if c, ok := svc.cancelers[runID]; ok {
			c()
			delete(svc.cancelers, runID)
		}
		svc.cancelMu.Unlock()
	}()

	_, err := svc.db.ExecContext(ctx,
		`UPDATE subagent_runs SET status = ? WHERE id = ? AND status IN (?,?,?)`,
		StatusCancelled, runID, StatusRequested, StatusApproved, StatusRunning,
	)
	if err != nil {
		return fmt.Errorf("cancel run: %w", err)
	}
	return nil
}

// Reject transitions a requested run to rejected, posts a reply message to
// the parent agent summarizing the decision, and returns. Returns
// ErrNotPending if the run is not in requested state. reason may be empty.
func (svc *Service) Reject(ctx context.Context, runID, reason string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := svc.db.ExecContext(ctx,
		`UPDATE subagent_runs
		   SET status=?, rejected_at=?, rejection_reason=?
		 WHERE id=? AND status=?`,
		StatusRejected, now, reason, runID, StatusRequested)
	if err != nil {
		return fmt.Errorf("reject run: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotPending
	}

	run, err := svc.Status(ctx, runID)
	if err != nil {
		return err
	}
	svc.emitStatus(run, "")

	if svc.poster != nil && run.ParentAgentID != "" {
		body := "Subagent spawn rejected"
		if reason != "" {
			body = body + ": " + reason
		}
		_, _ = svc.poster.SendMessage(ctx, messaging.SendInput{
			FromSessionID: run.ParentSessionID,
			FromAgentID:   run.Role,
			ToSessionID:   run.ParentSessionID,
			ToAgentID:     run.ParentAgentID,
			Channel:       messaging.ChannelChat,
			Kind:          messaging.KindReply,
			Body:          body,
			Type:          messaging.TypeMessage,
			RegisterAs:    "external",
		})
	}
	return nil
}

// expireIfStale checks whether runID is in 'requested' and older than
// UserSettings.SubagentApprovalTimeoutSeconds. If so, transitions to
// 'rejected' with the sentinel reason and returns (true, nil). Otherwise
// returns (false, nil). Any DB error surfaces as (false, err).
func (svc *Service) expireIfStale(ctx context.Context, runID string) (bool, error) {
	if svc.settings == nil {
		return false, nil // no settings = gating off = nothing to expire
	}
	us, err := svc.settings.GetUserSettings()
	if err != nil {
		return false, fmt.Errorf("load settings: %w", err)
	}
	timeout := us.SubagentApprovalTimeoutSeconds
	if timeout <= 0 {
		timeout = 86400
	}

	var status, createdAt string
	err = svc.db.QueryRowContext(ctx,
		`SELECT status, created_at FROM subagent_runs WHERE id=?`, runID).Scan(&status, &createdAt)
	if err != nil {
		return false, err
	}
	if status != StatusRequested {
		return false, nil
	}

	t, perr := time.Parse(time.RFC3339Nano, createdAt)
	if perr != nil {
		return false, nil
	}
	if time.Since(t) <= time.Duration(timeout)*time.Second {
		return false, nil
	}

	if err := svc.Reject(ctx, runID, "approval timed out"); err != nil {
		return false, err
	}
	return true, nil
}

// Approve transitions a requested run directly to running (recording the
// approval audit fields) and launches its runner. Returns ErrNotPending if
// the run is in a terminal or non-requested state, ErrApprovalExpired if
// the run has exceeded the stale timeout.
//
// The schema reserves a distinct 'approved' state, but the MVP collapses
// requested→approved→running into a single UPDATE so UI/ops consumers
// keying off status=running observe the run as soon as it is dispatched.
// approved_at/approved_by remain the audit witnesses. approved_by is left
// empty here — there is no authenticated caller identity plumbed to this
// method yet; a future change will populate it from the response-endpoint
// auth context rather than hard-coding a sentinel.
func (svc *Service) Approve(ctx context.Context, runID string) error {
	if expired, err := svc.expireIfStale(ctx, runID); err != nil {
		return err
	} else if expired {
		return ErrApprovalExpired
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := svc.db.ExecContext(ctx,
		`UPDATE subagent_runs
		   SET status=?, approved_at=?, approved_by=?, started_at=?
		 WHERE id=? AND status=?`,
		StatusRunning, now, "", now, runID, StatusRequested)
	if err != nil {
		return fmt.Errorf("approve run: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotPending
	}

	run, err := svc.Status(ctx, runID)
	if err != nil {
		return err
	}
	svc.emitStatus(run, "")

	// Launch runner with independent ctx + registered cancel, matching the
	// async branch pattern in Spawn.
	runCtx, runCancel := context.WithCancel(context.Background())
	svc.cancelMu.Lock()
	svc.cancelers[run.ID] = runCancel
	svc.cancelMu.Unlock()
	safego.Go(context.Background(), "subagent.run", func() {
		svc.execute(runCtx, run, run.ParentAgentID)
	})
	return nil
}

// execute runs the runner, writes the result back to subagent_runs,
// and (if a poster is configured) delivers a reply message to the
// parent session. Separate from Spawn so async callers can launch
// it as a goroutine.
//
// Ctx discipline: the runner itself receives `runCtx` (parent + the
// run's wall-time cap) so cancellation propagates properly. But the
// finalize+reply bookkeeping uses a FRESH context derived from
// context.Background — if the caller's request is canceled
// mid-execute in sync mode, we still want to persist the terminal
// state and post the reply. Dropping those because the caller gave
// up would leave the run stuck in 'running' and silently drop the
// subagent's work.
func (svc *Service) execute(ctx context.Context, run *Run, parentAgentID string) {
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(run.TimeoutSeconds)*time.Second)
	defer cancel()

	// Clear the per-run cancel registration on exit so a late Cancel
	// call after terminal state is a cheap no-op (no stale func held,
	// no double-invocation).
	defer func() {
		svc.cancelMu.Lock()
		delete(svc.cancelers, run.ID)
		svc.cancelMu.Unlock()
	}()

	result, runErr := svc.runner.Run(runCtx, run)

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if runErr != nil {
		run.Status = StatusFailed
		run.Error = runErr.Error()
	} else {
		run.Status = StatusCompleted
		if result != nil {
			run.ResultJSON = result.ResultJSON
			if run.ResultJSON == "" {
				run.ResultJSON = "{}"
			}
		}
	}
	run.CompletedAt = now

	// Finalize + reply under their own bounded background ctx so a
	// canceled parent request (sync mode) still gets the
	// bookkeeping written. 30s is ample for a single UPDATE +
	// messaging.Send against local SQLite.
	finalCtx, finalCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer finalCancel()

	if err := svc.finalizeRun(finalCtx, run); err != nil {
		slog.Warn("subagent: finalize run", "err", err, "run_id", run.ID)
	}

	// G-5: emit terminal event after finalizeRun commits, before the
	// reply post. Parent UI sees "subagent done, posting reply..." if
	// reply delivery is slow.
	terminalPreview := ""
	if result != nil {
		terminalPreview = result.Summary
	}
	svc.emitStatus(run, terminalPreview)

	if svc.poster == nil || parentAgentID == "" {
		return
	}
	// Reply-delivery channel: sync + api stay in-chat, async lands
	// in the inbox so the parent doesn't need to be actively
	// listening.
	replyChannel := messaging.ChannelChat
	if run.Mode == ModeAsync {
		replyChannel = messaging.ChannelInbox
	}
	summary := ""
	if result != nil {
		summary = result.Summary
	}
	if runErr != nil {
		summary = fmt.Sprintf("subagent %s failed: %v", run.ID, runErr)
	}
	resultPayload := run.ResultJSON
	if resultPayload == "" {
		resultPayload = "{}"
	}
	if _, err := svc.poster.SendMessage(finalCtx, messaging.SendInput{
		FromSessionID: run.ParentSessionID,
		FromAgentID:   run.Role, // the subagent is the sender
		ToSessionID:   run.ParentSessionID,
		ToAgentID:     parentAgentID,
		Channel:       replyChannel,
		Kind:          messaging.KindReply,
		Body:          summary,
		PayloadJSON:   resultPayload,
		Type:          messaging.TypeMessage,
		// The subagent is likely newly-seen from messaging's
		// perspective — stamp it as a CLI / internal auto-register
		// candidate. Clean-break: we don't add a new provenance
		// kind, 'external' is fine.
		RegisterAs: "external",
	}); err != nil {
		slog.Warn("subagent: reply delivery", "err", err, "run_id", run.ID)
	}
}

// insertRun persists a freshly-created run.
func (svc *Service) insertRun(ctx context.Context, r *Run) error {
	_, err := svc.db.ExecContext(ctx,
		`INSERT INTO subagent_runs (id, parent_session_id, child_session_id, role, prompt,
		                            mode, status, inputs_json, result_json, error,
		                            timeout_seconds, created_at, started_at, completed_at,
		                            parent_agent_id, envelope_instance_id,
		                            approved_at, approved_by,
		                            rejected_at, rejection_reason)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.ParentSessionID, r.ChildSessionID, r.Role, r.Prompt,
		r.Mode, r.Status, r.InputsJSON, r.ResultJSON, r.Error,
		r.TimeoutSeconds, r.CreatedAt, r.StartedAt, r.CompletedAt,
		r.ParentAgentID, r.EnvelopeInstanceID,
		r.ApprovedAt, r.ApprovedBy,
		r.RejectedAt, r.RejectionReason,
	)
	return err
}

// finalizeRun updates the terminal fields of a run (status, result,
// error, completed_at). Guarded so a concurrent Cancel that flipped
// the row to 'cancelled' wins the race — without this, a runner
// that was cancelled mid-flight would have its cancellation
// overwritten by the completed/failed terminal state this function
// wants to write.
func (svc *Service) finalizeRun(ctx context.Context, r *Run) error {
	res, err := svc.db.ExecContext(ctx,
		`UPDATE subagent_runs
		   SET status = ?, result_json = ?, error = ?, completed_at = ?
		 WHERE id = ? AND status IN (?, ?, ?)`,
		r.Status, r.ResultJSON, r.Error, r.CompletedAt,
		r.ID, StatusRunning, StatusRequested, StatusApproved,
	)
	if err != nil {
		return err
	}
	// Zero rows updated means a concurrent Cancel (or another
	// terminal transition) already wrote a terminal state. That's
	// fine — the run is settled. Re-read the authoritative status so
	// the in-memory struct reflects DB truth (otherwise the G-5
	// terminal event would report "failed" on a cancel-path run).
	if n, _ := res.RowsAffected(); n == 0 {
		slog.Info("subagent: finalize skipped; run already terminal",
			"run_id", r.ID, "intended_status", r.Status)
		var actual string
		if err := svc.db.QueryRowContext(ctx,
			`SELECT status FROM subagent_runs WHERE id = ?`, r.ID,
		).Scan(&actual); err == nil {
			r.Status = actual
			if actual == StatusCancelled {
				// The runner returned ctx.Err() after Cancel fired;
				// the "context canceled" Error is an artifact of the
				// cancellation, not a real failure.
				r.Error = ""
			}
		}
	}
	return nil
}

// selectSQL is the canonical SELECT clause for subagent_runs rows.
const selectSQL = `SELECT id, parent_session_id, child_session_id, role, prompt,
	mode, status, inputs_json, result_json, error,
	timeout_seconds, created_at, started_at, completed_at,
	parent_agent_id, envelope_instance_id, approved_at, approved_by,
	rejected_at, rejection_reason
	FROM subagent_runs`

func scanRun(row interface{ Scan(...any) error }) (*Run, error) {
	var r Run
	if err := row.Scan(
		&r.ID, &r.ParentSessionID, &r.ChildSessionID, &r.Role, &r.Prompt,
		&r.Mode, &r.Status, &r.InputsJSON, &r.ResultJSON, &r.Error,
		&r.TimeoutSeconds, &r.CreatedAt, &r.StartedAt, &r.CompletedAt,
		&r.ParentAgentID, &r.EnvelopeInstanceID,
		&r.ApprovedAt, &r.ApprovedBy,
		&r.RejectedAt, &r.RejectionReason,
	); err != nil {
		return nil, err
	}
	return &r, nil
}

// EchoRunner is the stub runner used until the real chat-engine
// runner lands. Returns a synthetic Result containing a short
// summary of the prompt. Good enough to exercise the Spawn flow in
// tests + a manual dogfood smoke without a provider roundtrip.
type EchoRunner struct{}

// Run reflects the prompt back as a summary and wraps it in a
// trivial result JSON payload.
func (EchoRunner) Run(_ context.Context, run *Run) (*Result, error) {
	summary := fmt.Sprintf("echo-runner: role=%s prompt=%q", run.Role, truncate(run.Prompt, 120))
	payload, _ := json.Marshal(map[string]string{
		"echo_role":   run.Role,
		"echo_prompt": run.Prompt,
	})
	return &Result{Summary: summary, ResultJSON: string(payload)}, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
