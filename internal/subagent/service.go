package subagent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/hollis-labs/nanite/internal/dispatch"
	"github.com/hollis-labs/nanite/internal/messaging"
	"github.com/hollis-labs/nanite/internal/safego"
	"github.com/hollis-labs/nanite/internal/store"
)

// DefaultTimeoutSeconds is the wall-clock BACKSTOP for a runner when
// the caller doesn't set a timeout and no operator override is in
// effect (see resolveDefaultTimeoutSeconds / the
// NANITE_SUBAGENT_DEFAULT_TIMEOUT_SECONDS env var).
//
// CW-20260519-0073: this is no longer the *governing* bound on a
// subagent run. The fixed 300s wall clock that this constant used to
// supply measured *elapsed time* — it guillotined a productive worker
// on its 22nd tool iteration exactly as readily as it caught a hung
// planner. The governing signal is now an *inactivity* timeout
// enforced inside the child chat loop (shouldStop Layer 2, scoped to
// subagent dispatch via subagentIdleTimeoutSeconds): a run that keeps
// emitting events resets its liveness clock and runs as long as the
// work needs; only genuine silence trips it. See the audit at
// CW-20260519-0072 §P0 for the Torque-parity rationale.
//
// 1800s (30 min) is deliberately generous: it is a pure backstop for
// the pathological case where the child loop somehow neither makes
// progress nor trips its own inactivity terminator.
//
// CW-20260517-0036: this constant is the *floor for an unconfigured
// deployment* only. An operator raises (or lowers) the backstop budget
// via NANITE_SUBAGENT_DEFAULT_TIMEOUT_SECONDS without recompiling —
// resolveDefaultTimeoutSeconds() reads it on every Spawn. Per-role
// control of the *governing* inactivity window already exists via the
// agent profile's constraints JSON (`idle_timeout_seconds`, parsed into
// chat.AgentConstraints and consumed by resolveIterationLimits); a
// per-role override of this wall-clock backstop would require wiring an
// agent-profile resolver into Spawn and is deliberately left out of
// this ticket as a larger refactor.
const DefaultTimeoutSeconds = 1800

// Subagent-run timeout validation range. Mirrors Torque's
// taskTimeoutOverride bounds (internal/runtime/agent/timeout.go:13-14):
// a value outside [60s, 7200s] is rejected and the caller falls back to
// the next priority tier. 60s is below any realistic agent orientation
// budget; 7200s (2h) is a generous ceiling for a single heavy run.
const (
	minTimeoutSeconds = 60
	maxTimeoutSeconds = 7200
)

// defaultTimeoutEnvVar is the operator knob for the wall-clock backstop
// budget applied to a subagent run that does not carry an explicit
// per-call timeout. Mirrors Torque's profile/env tiering. A value
// outside [minTimeoutSeconds, maxTimeoutSeconds] is ignored with a
// warning so a typo can't silently disable the backstop.
const defaultTimeoutEnvVar = "NANITE_SUBAGENT_DEFAULT_TIMEOUT_SECONDS"

// timeoutInRange reports whether secs is a usable subagent-run timeout.
func timeoutInRange(secs int) bool {
	return secs >= minTimeoutSeconds && secs <= maxTimeoutSeconds
}

// resolveDefaultTimeoutSeconds picks the wall-clock backstop budget for
// a subagent run that did not supply an explicit per-call timeout, in
// priority order (mirrors Torque resolveTimeout, timeout.go:35-43):
//
//  1. NANITE_SUBAGENT_DEFAULT_TIMEOUT_SECONDS when set and within
//     [minTimeoutSeconds, maxTimeoutSeconds]
//  2. DefaultTimeoutSeconds (the compiled-in 1800s floor)
//
// An explicit, in-range req.TimeoutSeconds still takes precedence over
// both — that check stays in Spawn, ahead of this call. An env value
// that is unparseable or out of range is ignored (with a warning) so a
// misconfiguration falls back safely rather than disabling the backstop.
func resolveDefaultTimeoutSeconds() int {
	raw := strings.TrimSpace(os.Getenv(defaultTimeoutEnvVar))
	if raw == "" {
		return DefaultTimeoutSeconds
	}
	secs, err := strconv.Atoi(raw)
	if err != nil {
		slog.Warn("subagent: ignoring non-integer timeout override env var",
			"env", defaultTimeoutEnvVar, "value", raw, "fallback_seconds", DefaultTimeoutSeconds)
		return DefaultTimeoutSeconds
	}
	if !timeoutInRange(secs) {
		slog.Warn("subagent: ignoring out-of-range timeout override env var",
			"env", defaultTimeoutEnvVar, "value", secs,
			"min", minTimeoutSeconds, "max", maxTimeoutSeconds,
			"fallback_seconds", DefaultTimeoutSeconds)
		return DefaultTimeoutSeconds
	}
	return secs
}

// spawnFanoutCap is the maximum number of Spawn invocations that may
// have their runner executing concurrently. FIFO ordering is preserved
// by the buffered-channel semaphore below.
// fanout cap; knob target: CW-20260419-0001 publisher config
const spawnFanoutCap = 3

// Typed errors returned by Approve and Reject so callers can
// distinguish rejection causes without string matching.
var (
	ErrNotPending      = errors.New("subagent: run is not in requested state")
	ErrApprovalExpired = errors.New("subagent: approval has expired")

	// ErrRecursionBlocked is returned by Spawn when the caller is itself
	// a subagent (has a parent). The recursion-depth cap (CW-20260516-0066)
	// is a hard cap at depth 1: only a depth-0 progenitor — a root /
	// user-facing session with NO parent — may spawn session-creating
	// subagents. A worker that received a task must do the work itself
	// instead of re-dispatching it to a fresh child. This prevents the
	// fork-bomb chains observed on 2026-05-16 (session c226).
	ErrRecursionBlocked = errors.New("subagent recursion blocked: only a root agent may spawn subagents; this agent has a parent — do the work yourself")

	// ErrStalled is the cross-package classification signal for a run
	// that ended because the provider-stream inactivity watchdog fired
	// (CW-20260517-0036) — the provider held the stream open but emitted
	// nothing for the full inactivity window. The Runner wraps this into
	// the error it returns (via errors.Join) when it sees the structured
	// `cause:"stalled"` error event, so `execute`'s outcome classifier
	// can errors.Is it and stamp StatusStalled rather than the generic
	// StatusFailed. Genuine provider errors and crashes do NOT carry it.
	ErrStalled = errors.New("subagent: run stalled — provider stream inactivity timeout")
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

// TrustResolverIface is the subset of dispatch.TrustResolver that the
// subagent package depends on. *store.Store satisfies it via ResolveTrust.
type TrustResolverIface = dispatch.TrustResolver

// EventLogger persists audit events for trust-bypassed dispatches.
// *store.Store satisfies it; nil = audit logging skipped.
type EventLogger interface {
	LogEvent(sessionID, eventType, category, detail, metadata string)
}

// ParentageChecker reports whether a session is itself a spawned
// subagent (has a parent). *store.Store satisfies it via
// IsSubagentSession. Defined as a narrow interface (rather than taking
// *store.Store directly) so the recursion cap can be unit-tested with a
// fake and is not coupled to the concrete *store.Store type.
//
// Used by the recursion-depth cap (CW-20260516-0066): a caller that is
// itself a subagent is rejected before it can spawn another.
type ParentageChecker interface {
	IsSubagentSession(sessionID string) (bool, error)
}

// Service coordinates the spawn → run → complete → reply flow.
// Safe for concurrent use.
type Service struct {
	db           *sql.DB
	runner       Runner
	poster       MessagePoster
	approver     ApprovalEmitter
	settings     SettingsReader
	streamSink   SubagentStreamSink
	trustResolver TrustResolverIface
	eventLogger  EventLogger
	parentage    ParentageChecker

	// cancelers holds a per-run context.CancelFunc keyed by runID so
	// Cancel(runID) can propagate cancellation into the in-flight
	// runner — not just flip the DB row. Spawn registers; execute's
	// defer clears; Cancel invokes-and-deletes. Mutex-guarded.
	cancelMu  sync.Mutex
	cancelers map[string]context.CancelFunc

	// spawnSem is a buffered-channel semaphore that bounds the number of
	// Spawn invocations with an in-flight runner to spawnFanoutCap.
	// Sends acquire a slot; receives release it. FIFO ordering is a
	// property of Go channel scheduling under normal load.
	spawnSem chan struct{}
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
		spawnSem:  make(chan struct{}, spawnFanoutCap),
	}
}

// SetTrustResolver wires the H1 trust resolver. Call before any Spawn.
// When nil, Spawn falls back to TrustNormal for every spawn.
func (svc *Service) SetTrustResolver(r TrustResolverIface) { svc.trustResolver = r }

// SetEventLogger wires the audit event logger. Call before any Spawn.
// When nil, trust_dispatch audit rows are skipped (non-fatal).
func (svc *Service) SetEventLogger(l EventLogger) { svc.eventLogger = l }

// SetParentageChecker wires the recursion-depth cap's parentage check.
// Call before any Spawn. When nil, the depth cap is disabled — Spawn
// behaves as before (no parentage enforcement). Production wiring
// always sets this so the cap is in force; tests opt in explicitly.
func (svc *Service) SetParentageChecker(p ParentageChecker) { svc.parentage = p }

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
	// CW-20260516-0066: hard subagent recursion-depth cap. Only a
	// depth-0 progenitor (a root / user-facing session with no parent)
	// may spawn a session-creating subagent. If the caller's session is
	// itself a subagent — i.e. it appears as a child_session_id on some
	// subagent_runs row — reject the spawn outright. This is checked
	// BEFORE any DB write so a rejected recursive spawn leaves no
	// orphaned run row. When no ParentageChecker is wired the cap is
	// disabled (tests / direct invocations); production always wires it.
	if svc.parentage != nil {
		isChild, perr := svc.parentage.IsSubagentSession(req.ParentSessionID)
		if perr != nil {
			// Fail closed: if we cannot determine parentage we refuse the
			// spawn rather than risk an unbounded recursive chain. A spawn
			// that genuinely needed to happen will be retried by the root.
			return "", fmt.Errorf("subagent recursion check failed: %w", perr)
		}
		if isChild {
			if svc.eventLogger != nil {
				meta, _ := json.Marshal(map[string]any{
					"parent_session_id": req.ParentSessionID,
					"role":              req.Role,
					"mode":              mode,
				})
				svc.eventLogger.LogEvent(
					req.ParentSessionID,
					"subagent_recursion_blocked",
					"trust",
					fmt.Sprintf("rejected spawn of role=%s: caller is itself a subagent", req.Role),
					string(meta),
				)
			}
			return "", ErrRecursionBlocked
		}
	}

	// CW-20260517-0036 — resolve the wall-clock backstop budget in
	// priority order (mirrors Torque resolveTimeout):
	//   1. an explicit, in-range req.TimeoutSeconds (the per-call tool
	//      arg) — kept first so an operator/author override always wins;
	//   2. NANITE_SUBAGENT_DEFAULT_TIMEOUT_SECONDS (env) when in range;
	//   3. the compiled-in DefaultTimeoutSeconds floor.
	// An explicit per-call value outside [60s, 7200s] is rejected and
	// the run falls through to the env/default tier — a fat-fingered
	// `timeout_seconds: 5` can't disable the backstop, and a runaway
	// `timeout_seconds: 999999` can't extend it past the 2h ceiling.
	timeout := req.TimeoutSeconds
	if timeout > 0 && !timeoutInRange(timeout) {
		slog.Warn("subagent: explicit timeout_seconds out of range; falling back to resolved default",
			"requested_seconds", timeout, "min", minTimeoutSeconds, "max", maxTimeoutSeconds,
			"role", req.Role)
		timeout = 0
	}
	if timeout <= 0 {
		timeout = resolveDefaultTimeoutSeconds()
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
		Provider:        req.Provider,
		CreatedAt:       time.Now().UTC().Format(time.RFC3339Nano),
	}

	// H1 trust resolution: consult the workspace-scoped trust tier before
	// any approval-gate logic. Tier determines whether to refuse, gate, or
	// bypass the approval envelope.
	trust := dispatch.TrustNormal
	if svc.trustResolver != nil && req.WorkspaceID != "" && req.AgentProfileID != "" {
		t, terr := svc.trustResolver.ResolveTrust(ctx, req.WorkspaceID, req.AgentProfileID)
		if terr != nil {
			// Fail closed: treat resolve error as normal (require approval).
			slog.Warn("subagent: trust resolve error; defaulting to normal", "err", terr,
				"workspace_id", req.WorkspaceID, "agent_profile_id", req.AgentProfileID)
		} else {
			trust = t
		}
	}

	// Untrusted: refuse outright. ErrUntrustedRole must NOT be bypassed.
	if trust == dispatch.TrustUntrusted {
		return "", dispatch.ErrUntrustedRole
	}

	// Trusted: bypass approval, write audit log, proceed to ungated path.
	if trust == dispatch.TrustTrusted {
		meta, _ := json.Marshal(map[string]any{
			"workspace_id":     req.WorkspaceID,
			"agent_profile_id": req.AgentProfileID,
			"role":             req.Role,
		})
		if svc.eventLogger != nil {
			svc.eventLogger.LogEvent(
				req.ParentSessionID,
				"trust_dispatch",
				"trust",
				fmt.Sprintf("role=%s tier=trusted bypass=approval", req.Role),
				string(meta),
			)
		}
	} else {
		// TrustNormal path: evaluate the approval gate predicate.
		gate := false
		if svc.settings != nil {
			us, err := svc.settings.GetUserSettings()
			if err != nil {
				return "", fmt.Errorf("load settings: %w", err)
			}
			gate = (us.SubagentApprovalRequired && !us.DeveloperMode) || mode == ModeInteractive
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
				"provider":        run.Provider,
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
	}

	// Ungated path — reached when:
	//   a) trust == TrustTrusted (approval bypassed, audit written above), or
	//   b) trust == TrustNormal and approval gate evaluated to false.
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
		// Blocking: caller holds until the runner returns. Use a
		// background-derived ctx so the caller's short tool-call
		// timeout (30s) doesn't cancel the child runner — same
		// pattern as async mode. Cancel(runID) is still wired.
		//
		// Acquire the fan-out semaphore before executing. The
		// caller's ctx governs the wait; if it cancels while
		// queued we return cleanly without consuming a slot.
		if err := svc.acquireSpawnSlot(ctx); err != nil {
			return "", err
		}
		execCtx, execCancel := context.WithCancel(context.Background())
		svc.cancelMu.Lock()
		svc.cancelers[run.ID] = execCancel
		svc.cancelMu.Unlock()
		svc.executeWithSlot(execCtx, run, req.ParentAgentID)
	case ModeAsync, ModeAPI:
		// Non-blocking: fire-and-forget goroutine. The reply lands
		// in the parent session's inbox (async) or chat (api).
		// Background-derived ctx because the caller's request ctx
		// will likely be done by the time the runner finishes;
		// Cancel(runID) is the only intended cancellation path.
		//
		// Acquire the fan-out semaphore before launching the
		// goroutine. The caller's ctx governs the wait so a
		// queued async spawn can be cancelled before it starts.
		if err := svc.acquireSpawnSlot(ctx); err != nil {
			return "", err
		}
		runCtx, runCancel := context.WithCancel(context.Background())
		svc.cancelMu.Lock()
		svc.cancelers[run.ID] = runCancel
		svc.cancelMu.Unlock()
		safego.Go(context.Background(), "subagent.run", func() {
			svc.executeWithSlot(runCtx, run, req.ParentAgentID)
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

// acquireSpawnSlot blocks until a slot in the fan-out semaphore is
// available or ctx is cancelled. Returns ctx.Err() if the wait is
// interrupted, nil on successful acquisition.
func (svc *Service) acquireSpawnSlot(ctx context.Context) error {
	select {
	case svc.spawnSem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// releaseSpawnSlot returns a previously-acquired slot to the semaphore.
func (svc *Service) releaseSpawnSlot() { <-svc.spawnSem }

// executeWithSlot wraps execute with a deferred semaphore release so
// the slot is returned exactly once regardless of how execute exits
// (success, error, or cancellation).
func (svc *Service) executeWithSlot(ctx context.Context, run *Run, parentAgentID string) {
	defer svc.releaseSpawnSlot()
	svc.execute(ctx, run, parentAgentID)
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
//
// CW-20260519-0073: the `context.WithTimeout` below is now a
// generous wall-clock BACKSTOP (DefaultTimeoutSeconds = 1800s), not
// the governing bound. The real liveness signal is the child chat
// loop's inactivity terminator (shouldStop Layer 2, scoped to
// subagent dispatch). The old fixed 300s deadline measured elapsed
// time and cancelled productive-but-slow runs mid-stream; the
// inactivity timeout measures *silence* instead, so a worker that
// keeps emitting events is never reaped. This backstop only fires
// for the pathological case where the child loop neither progresses
// nor trips its own inactivity terminator.
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
		// CW-20260519-0074 (audit §P3) — run status taxonomy. The error
		// branch no longer collapses every non-success into `failed`.
		// classifyRunOutcome inspects the error and the progress signal
		// (tool-call count, carried in the partial Result's result_json)
		// to pick between `stalled`, `over_budget`, and `failed`.
		run.Status = classifyRunOutcome(runErr, result, runCtx)
		run.Error = runErr.Error()
		// CW-20260519-0071 (audit §P2): partial-result capture on the
		// non-success branch. A subagent guillotined mid-productive-work
		// (wall-clock backstop cancels the provider stream) has often
		// done real file-writing work; the runner now returns that
		// partial Result *alongside* the error. Persist result_json so
		// the run row carries a structured trace of what the killed
		// run accomplished instead of leaving result_json at its
		// insert-time default. The error itself is untouched above —
		// the status now carries a diagnostic signal but the error
		// string is still recorded; this is additive capture, not error
		// suppression. result == nil (genuinely empty failure, or a
		// runner that returns nil on error) leaves result_json as-is.
		if result != nil {
			run.ResultJSON = structuredResultJSON(result)
		}
	} else {
		run.Status = StatusCompleted
		if result != nil {
			run.ResultJSON = structuredResultJSON(result)
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

	// CW-20260519-0066: subagent → parent envelope hop. A subagent that
	// produced a structured envelope (a plan-review / approval / proposal
	// card) had it stranded on the child session's message row — it never
	// reached the parent session's transcript or the operator's GUI, so
	// the operator could not see or act on the card. Lift any envelope(s)
	// the child emitted onto the PARENT session's stream + persist them as
	// EnvelopeInstance rows on the parent, mirroring the G-4 approval-card
	// path (Emit = CreateEnvelopeInstance + plugin_envelope broadcast).
	// Zero envelopes = no-op; one or many are each re-emitted.
	svc.liftResultEnvelopes(finalCtx, run, result)

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
		// CW-20260519-0074: report the classified outcome, not a blanket
		// "failed" — run.Status was set by classifyRunOutcome above and
		// distinguishes over_budget / stalled from a genuine failure.
		summary = fmt.Sprintf("subagent %s ended (%s): %v", run.ID, run.Status, runErr)
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

// classifyRunOutcome maps a non-nil runner error to a terminal run
// status (CW-20260519-0074, audit §P3). Before this change `execute`
// had exactly one error branch — any non-nil runErr → StatusFailed —
// so a context.DeadlineExceeded on a *productive* run was recorded
// identically to a genuine crash and the status carried no diagnostic
// signal. The taxonomy now splits the non-success outcomes:
//
//   - StatusStalled — the provider-stream inactivity watchdog fired
//     (CW-20260517-0036): the Runner wraps subagent.ErrStalled into the
//     error. A stall is by definition "no progress", so it is also the
//     bucket for a wall-clock backstop that fired with zero tool calls
//     (the pathological "child loop neither progressed nor tripped its
//     own inactivity terminator" case from the audit §2.2).
//   - StatusOverBudget — the wall-clock backstop deadline / cancellation
//     fired (runCtx.Err() != nil) AND the run was making progress
//     (non-zero tool calls in the partial result). This is NOT a
//     failure: it must not burn a retry budget or fire on_fail (mirrors
//     Torque's `canceled`-vs-`failed` split).
//   - StatusFailed — everything else: a genuine provider error, a crash,
//     or the fabrication-detector trip.
//
// runCtx is the run's context — its Err() distinguishes "the backstop
// deadline cut a live run" from "the runner returned an error on its
// own". result is the partial Result the Runner returns alongside the
// error (CW-20260519-0071); it carries the tool-call count used as the
// progress signal. Either argument may be nil.
func classifyRunOutcome(runErr error, result *Result, runCtx context.Context) string {
	if runErr == nil {
		// Defensive: callers only invoke this on the error branch.
		return StatusCompleted
	}

	progressed := runMadeProgress(result)

	// A stalled stream is, by construction, "no progress" — the provider
	// emitted nothing for the whole inactivity window. Classify it
	// stalled regardless of any tool calls made in earlier iterations:
	// the run still ended because it went silent.
	if errors.Is(runErr, ErrStalled) {
		return StatusStalled
	}

	// The wall-clock backstop fired (or the run was cancelled): the run
	// context is done. A run that made real progress before the deadline
	// is over_budget, not failed — it should not burn retry budget. A
	// deadline that fired with zero progress is a silent/stuck run with
	// no productive trace: classify it stalled (it never made progress
	// and never tripped its own inactivity terminator).
	if runCtx != nil && runCtx.Err() != nil {
		if progressed {
			return StatusOverBudget
		}
		return StatusStalled
	}

	// No stall sentinel, run context still live: a genuine provider
	// error, a crash, or the fabrication-detector trip. `failed` is now
	// reserved for exactly these.
	return StatusFailed
}

// runMadeProgress reports whether a partial Result shows the run did
// real work before it ended — the progress signal for classifyRunOutcome.
//
// The signal source is the partial result_json the Runner assembles on
// the error path (CW-20260519-0071): a `{"partial":true,...,"tools":
// {"calls":N,"results_success":N,"results_error":N}}` blob. A non-zero
// tool-call (or tool-result) count means the child ran productive
// iterations. This avoids re-plumbing the counts through a dedicated
// channel — the partial result already carries them.
//
// Returns false when result is nil, has no result_json, or the blob
// records no tool activity.
func runMadeProgress(result *Result) bool {
	if result == nil || result.ResultJSON == "" {
		return false
	}
	var parsed struct {
		Tools struct {
			Calls          int `json:"calls"`
			ResultsSuccess int `json:"results_success"`
			ResultsError   int `json:"results_error"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(result.ResultJSON), &parsed); err != nil {
		return false
	}
	return parsed.Tools.Calls > 0 ||
		parsed.Tools.ResultsSuccess > 0 ||
		parsed.Tools.ResultsError > 0
}

// structuredResultJSON resolves the JSON persisted to subagent_runs.result_json.
//
// CW-20260516-0060: a runner whose child emits no structured envelope hands
// back Result.ResultJSON == "{}" (drainCapture/drainBootSession seed the
// envelope buffer with "{}" and only overwrite it on a plugin_envelope /
// stream_end payload — and BootRunner's PTY surface emits none today). The
// old code persisted that "{}" verbatim, so the run row carried no trace of
// the subagent's actual output. When the runner produced a real structured
// payload it is used verbatim; otherwise the summary is wrapped into a
// structured object so the row is never an empty {}.
func structuredResultJSON(result *Result) string {
	if rj := result.ResultJSON; rj != "" && rj != "{}" {
		return rj
	}
	payload, err := json.Marshal(struct {
		Summary string `json:"summary"`
	}{Summary: result.Summary})
	if err != nil {
		return "{}"
	}
	return string(payload)
}

// liftedEnvelope is a single structured envelope recovered from a
// completed subagent's Result, reduced to the (type, data) pair the
// ApprovalEmitter.Emit contract needs. Emit wraps `data` as the
// envelope's `data` blob and stamps the type; the child's outer
// kind/version envelope frame is dropped because Emit rebuilds it.
type liftedEnvelope struct {
	Type string
	Data json.RawMessage
}

// extractLiftableEnvelopes pulls every structured envelope out of a
// subagent Result's ResultJSON so execute() can re-emit them onto the
// parent session (CW-20260519-0066).
//
// Two ResultJSON shapes are recognized — the runner produces different
// shapes on the success vs partial-capture paths:
//
//   - Success path (subagent_runner.go ChatRunner.Run): ResultJSON is the
//     child's terminal envelope JSON verbatim, i.e. the envelope wire
//     shape {"kind":"envelope","version":1,"type":"plan-review",
//     "data":{...}}. drainCapture accumulates it last-wins, so there is
//     at most one envelope on this path.
//   - Partial-capture path (CW-20260519-0071 partialResult): ResultJSON is
//     {"partial":true,"summary":...,"envelope":{...},"tools":{...}} — the
//     child's envelope nested under the `envelope` key. A subagent cut
//     mid-task may still have emitted a review/approval card worth
//     surfacing, so this path is lifted too.
//
// Returns nil for a nil result, an empty/`{}` ResultJSON, a parse
// failure, or an envelope blob with no `type` (a structured result that
// is not an envelope — e.g. structuredResultJSON's {"summary":...}
// wrapper). The function never errors: a malformed child envelope is a
// dropped card, not a failed run.
func extractLiftableEnvelopes(result *Result) []liftedEnvelope {
	if result == nil {
		return nil
	}
	raw := strings.TrimSpace(result.ResultJSON)
	if raw == "" || raw == "{}" {
		return nil
	}

	// Decode loosely: both recognized shapes are JSON objects. A
	// partial-capture blob carries `partial:true` + a nested `envelope`;
	// a success-path blob is the envelope itself (kind/type/data).
	var obj struct {
		Partial  bool            `json:"partial"`
		Envelope json.RawMessage `json:"envelope"`
		Type     string          `json:"type"`
		Data     json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		slog.Warn("subagent: result envelope not valid JSON; not lifted to parent",
			"err", err)
		return nil
	}

	// Partial-capture shape: the child's envelope is nested. Recurse on
	// the nested blob so the same type/data extraction applies.
	if obj.Partial && len(obj.Envelope) > 0 {
		nested := strings.TrimSpace(string(obj.Envelope))
		if nested == "" || nested == "{}" {
			return nil
		}
		return extractLiftableEnvelopes(&Result{ResultJSON: nested})
	}

	// Success-path shape: the blob IS the envelope. A blob with no `type`
	// is some other structured result (e.g. the {"summary":...} wrapper)
	// — there is no card to surface.
	if obj.Type == "" {
		return nil
	}
	data := obj.Data
	if len(data) == 0 {
		data = json.RawMessage("{}")
	}
	return []liftedEnvelope{{Type: obj.Type, Data: data}}
}

// liftResultEnvelopes re-emits every structured envelope a completed
// subagent produced onto the PARENT session's stream + persists each as
// an EnvelopeInstance row on the parent (CW-20260519-0066).
//
// Before this hop existed, a subagent-produced plan-review / approval /
// proposal card was persisted only on the subagent's own message row and
// never reached the parent transcript or the operator's GUI — the
// operator could not see or act on the card. svc.approver.Emit is the
// same persist+broadcast primitive G-4 uses for spawn-approval cards
// (CreateEnvelopeInstance + a plugin_envelope StreamEvent), so the lifted
// envelope renders on BOTH the live SSE stream and the persisted/reload
// path.
//
// Zero envelopes (the common case) is a silent no-op. One or many are
// each emitted in order. A nil approver (tests / minimal wiring) skips
// emission. An Emit failure is logged and the remaining envelopes are
// still attempted — a dropped card must not abort the run's bookkeeping.
func (svc *Service) liftResultEnvelopes(ctx context.Context, run *Run, result *Result) {
	if svc.approver == nil || run.ParentSessionID == "" {
		return
	}
	envs := extractLiftableEnvelopes(result)
	if len(envs) == 0 {
		return
	}
	for _, env := range envs {
		id, err := svc.approver.Emit(ctx, run.ParentSessionID, env.Type, env.Data)
		if err != nil {
			slog.Warn("subagent: lift child envelope to parent failed",
				"err", err, "run_id", run.ID,
				"parent_session_id", run.ParentSessionID,
				"envelope_type", env.Type)
			continue
		}
		slog.Info("subagent: lifted child envelope to parent session",
			"run_id", run.ID,
			"child_session_id", run.ChildSessionID,
			"parent_session_id", run.ParentSessionID,
			"envelope_type", env.Type,
			"envelope_instance_id", id)
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
		                            rejected_at, rejection_reason,
		                            provider)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.ParentSessionID, r.ChildSessionID, r.Role, r.Prompt,
		r.Mode, r.Status, r.InputsJSON, r.ResultJSON, r.Error,
		r.TimeoutSeconds, r.CreatedAt, r.StartedAt, r.CompletedAt,
		r.ParentAgentID, r.EnvelopeInstanceID,
		r.ApprovedAt, r.ApprovedBy,
		r.RejectedAt, r.RejectionReason,
		r.Provider,
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
	rejected_at, rejection_reason, provider
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
		&r.Provider,
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
