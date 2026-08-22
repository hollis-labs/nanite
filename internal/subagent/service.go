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

// DefaultHeartbeatSeconds is the cadence of "still running" progress
// pings emitted while a subagent's runner call is in flight
// (CW-20260519-0068). Without this, a run sits silent on the parent's
// SSE stream between the initial "running" dispatch event (Spawn,
// G-5) and the eventual terminal event — for a run approaching the
// 30-minute backstop that silence is indistinguishable from a hang.
// 30s is chosen to be well inside a human's patience for "is this
// still alive" while staying far below chat-turn-refresh noise
// thresholds.
const DefaultHeartbeatSeconds = 30

// Heartbeat cadence validation range and operator override. Mirrors the
// timeout knob's env-tiering shape above. A raw value of exactly "0"
// disables heartbeats outright (some deployments may prefer silence);
// anything else outside [minHeartbeatSeconds, maxHeartbeatSeconds] is
// rejected with a warning and falls back to DefaultHeartbeatSeconds so a
// typo can't accidentally spam (too low) or silence (too high) the
// signal.
const (
	minHeartbeatSeconds = 1
	maxHeartbeatSeconds = 300
)

// heartbeatSecondsEnvVar is the operator knob for the heartbeat cadence.
const heartbeatSecondsEnvVar = "NANITE_SUBAGENT_HEARTBEAT_SECONDS"

// heartbeatIntervalInRange reports whether secs is a usable heartbeat
// cadence (0 is handled separately by the caller as "disabled").
func heartbeatIntervalInRange(secs int) bool {
	return secs >= minHeartbeatSeconds && secs <= maxHeartbeatSeconds
}

// resolveHeartbeatInterval picks the "still running" ping cadence for a
// subagent run, in priority order:
//
//  1. NANITE_SUBAGENT_HEARTBEAT_SECONDS == "0" → heartbeats disabled
//     (returns 0).
//  2. NANITE_SUBAGENT_HEARTBEAT_SECONDS set to another in-range value →
//     that value.
//  3. unset, unparseable, or out of range → DefaultHeartbeatSeconds.
func resolveHeartbeatInterval() time.Duration {
	raw := strings.TrimSpace(os.Getenv(heartbeatSecondsEnvVar))
	if raw == "" {
		return DefaultHeartbeatSeconds * time.Second
	}
	secs, err := strconv.Atoi(raw)
	if err != nil {
		slog.Warn("subagent: ignoring non-integer heartbeat override env var",
			"env", heartbeatSecondsEnvVar, "value", raw, "fallback_seconds", DefaultHeartbeatSeconds)
		return DefaultHeartbeatSeconds * time.Second
	}
	if secs == 0 {
		return 0
	}
	if !heartbeatIntervalInRange(secs) {
		slog.Warn("subagent: ignoring out-of-range heartbeat override env var",
			"env", heartbeatSecondsEnvVar, "value", secs,
			"min", minHeartbeatSeconds, "max", maxHeartbeatSeconds,
			"fallback_seconds", DefaultHeartbeatSeconds)
		return DefaultHeartbeatSeconds * time.Second
	}
	return time.Duration(secs) * time.Second
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

	// ErrNoProfileForRole is returned by Spawn when role resolution
	// cannot find an agent_profiles row for the requested slug
	// (CW-20260519-0123). The c271 evidence pattern — an LLM calling
	// subagent_spawn with a role name like "system-architect" that has
	// no registered profile — used to fall through to the orphan path:
	// a row was inserted with status=running, the runner errored, the
	// reaper marked it `failed` 60s later with "timeout: orphan, no
	// child session". The fail-fast gate at the Spawn boundary catches
	// this BEFORE any DB row is inserted, before a timer is started,
	// and before the caller's ctx can deadline.
	ErrNoProfileForRole = errors.New("subagent: no agent profile registered for role")

	// ErrRoleNotExecutable is returned by Spawn when the resolved
	// profile carries can_execute=false AND the role slug is not in
	// the text-only producer whitelist (CW-20260519-0123). The c256
	// evidence pattern — a planner subagent with no tool surface that
	// hung 300s with zero output — used to fall through to the runner,
	// where the chat capture closed without `stream_end` and the
	// drainCapture loop returned empty. The fail-fast gate rejects
	// these at the Spawn boundary so the parent sees a clear config
	// error instead of a silent stall.
	//
	// The whitelist (textOnlyRoleSlugs) is for roles that are
	// deliberately can_execute=false because their dispatch surface
	// (e.g. PeerQuery / hint selection) is text-in / text-out by
	// design and does not need the executable tool path.
	ErrRoleNotExecutable = errors.New("subagent: agent profile is not executable and not in the text-only role whitelist")

	// ErrSpawnFanoutCapReached is returned by Spawn when the caller's
	// context deadline expires while waiting for an available slot in
	// the 3-slot fan-out semaphore (CW-20260816-0001). Distinct from a
	// genuine infrastructure timeout — this is a capacity signal: all
	// slots are occupied by other running subagents. The caller can
	// retry when a slot is freed, or the parent can sequence spawns
	// to respect the cap.
	ErrSpawnFanoutCapReached = errors.New("subagent: spawn fan-out capacity reached — all slots occupied")
)

// textOnlyRoleSlugs enumerates can_execute=false role slugs that are
// nevertheless valid subagent-spawn targets (CW-20260519-0123). These
// roles produce text-only output through a dispatch surface that
// doesn't require an executable tool path — typically PeerQuery /
// classification / hint selection.
//
// Currently:
//   - hint-selector: think-block v2 affordance selector (CW-20260420-0022),
//     dispatched via the chat hint dispatch adapter; payload is JSON in,
//     JSON list out.
//
// Adding a role here is a deliberate signal: the role is intentionally
// non-executable AND someone explicitly dispatches it. Other
// can_execute=false profiles (analyst, planner, researcher) are NOT in
// this list — analyst is shape-compatible but not directly dispatched
// via subagent_spawn; planner's hang is the exact pathology this
// ticket retires; researcher carries a real read tool allow_list and
// is intended to evolve toward can_execute=true.
var textOnlyRoleSlugs = map[string]struct{}{
	"hint-selector": {},
}

// isTextOnlyRole reports whether slug is in textOnlyRoleSlugs.
func isTextOnlyRole(slug string) bool {
	_, ok := textOnlyRoleSlugs[slug]
	return ok
}

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
	GetUserSettings(ctx context.Context) (*store.UserSettings, error)
}

// TrustResolverIface is the subset of dispatch.TrustResolver that the
// subagent package depends on. *store.Store satisfies it via ResolveTrust.
type TrustResolverIface = dispatch.TrustResolver

// EventLogger persists audit events for trust-bypassed dispatches.
// *store.Store satisfies it; nil = audit logging skipped.
type EventLogger interface {
	LogEvent(ctx context.Context, sessionID, eventType, category, detail, metadata string)
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
	IsSubagentSession(ctx context.Context, sessionID string) (bool, error)
}

// ProfileResolver is the narrow surface the Spawn fail-fast gate uses
// to validate a role → profile at the Spawn boundary
// (CW-20260519-0123). *store.Store satisfies it via GetAgentBySlug.
//
// Defined as a narrow interface (rather than depending on *store.Store
// directly) so the gate can be unit-tested with a fake — the c271
// "no profile for role" pattern and the c256 "can_execute=false"
// pattern both want explicit test coverage without a real SQLite-
// backed agent_profiles table.
//
// Implementations MUST return an error wrapping sql.ErrNoRows when no
// profile exists for slug; the gate uses errors.Is(err, sql.ErrNoRows)
// to distinguish "missing profile" (return ErrNoProfileForRole) from
// "lookup failed" (return the underlying error). *store.Store's
// GetAgentBySlug already wraps with %w so this contract holds in
// production.
type ProfileResolver interface {
	GetAgentBySlug(ctx context.Context, slug string) (*store.AgentProfile, error)
}

// replyFromAgentID resolves the agent identity a subagent reply should be
// sent from (CW-20260815-0023). run.Role (e.g. "worker") is a role name,
// not the harness's agent-address format (a DB UUID or "file-<slug>") —
// passing it straight through as FromAgentID with RegisterAs="external"
// made messaging.Service.maybeAutoRegister try to INSERT a brand-new
// agent_profiles row with Slug=role. That collides with the UNIQUE
// constraint on agent_profiles.slug whenever a row already exists for that
// role — which it always does, since Spawn's GetAgentBySlug(req.Role) gate
// is exactly what validated the role exists in the first place; that row's
// real ID (e.g. "blt-worker-001") is never the same string as its slug
// ("worker"), so messaging's resolver lookup on the bare role misses and
// falls through to the doomed insert.
//
// Resolving to the real row's ID here means messaging's resolver finds it
// immediately and never attempts to auto-register — registerAs is empty
// because it's now irrelevant (auto-register is skipped entirely). Falls
// back to the bare role + "external" (the prior behavior) when no profile
// resolver is wired, which only happens in tests that don't exercise this
// path.
func (svc *Service) replyFromAgentID(role string) (id string, registerAs string) {
	if svc.profiles != nil {
		if profile, err := svc.profiles.GetAgentBySlug(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, role); err == nil && profile.ID != "" {
			return profile.ID, ""
		}
	}
	return role, "external"
}

// Service coordinates the spawn → run → complete → reply flow.
// Safe for concurrent use.
type Service struct {
	db            *sql.DB
	runner        Runner
	poster        MessagePoster
	approver      ApprovalEmitter
	settings      SettingsReader
	streamSink    SubagentStreamSink
	reactor       CompletionReactor
	trustResolver TrustResolverIface
	eventLogger   EventLogger
	parentage     ParentageChecker
	profiles      ProfileResolver

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

// SetProfileResolver wires the Spawn-boundary fail-fast gate
// (CW-20260519-0123). Call before any Spawn. When nil, the gate is
// disabled — Spawn does NOT validate role → profile and the prior
// behavior (rely on the runner's fallback / orphan reaper) is
// preserved. Production wiring always sets this so unknown roles and
// can_execute=false-outside-whitelist roles fail fast with a
// structured config error; tests opt in explicitly so existing
// service_test.go cases that pass synthetic roles like
// "file-summarizer" continue to drive an EchoRunner end-to-end.
func (svc *Service) SetProfileResolver(p ProfileResolver) { svc.profiles = p }

// SetStreamSink wires (or unwires) the G-5 status-event sink. Pass nil
// to disable emission. Safe to call before any Spawn.
func (svc *Service) SetStreamSink(s SubagentStreamSink) { svc.streamSink = s }

// SetCompletionReactor wires (or unwires) the CW-20260520-0001 Layer-2
// harness reaction hook. Pass nil to disable — completions still post
// their subagent_result message, they just don't get a proactive
// harness-triggered turn. Safe to call before any Spawn.
func (svc *Service) SetCompletionReactor(r CompletionReactor) { svc.reactor = r }

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

// stampActivity persists a fresh last_activity_at for runID so the
// OUTER periodic reaper sweep (reaper.go SweepOnce) can tell a
// genuinely silent run from one that's still making progress
// (CW-20260816-0004). Guarded to status='running' — a heartbeat tick
// that lands after the row already went terminal (a race with
// finalizeRun) must not resurrect a stale 'running' read.
//
// Uses a short context derived from context.Background, not the
// caller's runCtx: the write must still land even if runCtx is on the
// verge of its own deadline, and it must not be cancelled by the same
// timeout it exists to make survivable. Mirrors persistRetryCheckpoint's
// context discipline.
func (svc *Service) stampActivity(runID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := svc.db.ExecContext(ctx,
		`UPDATE subagent_runs SET last_activity_at = ? WHERE id = ? AND status = ?`,
		time.Now().UTC().Format(time.RFC3339Nano), runID, StatusRunning,
	); err != nil {
		slog.Warn("subagent: stamp activity", "err", err, "run_id", runID)
	}
}

// emitHeartbeat marshals a non-terminal "still running" progress ping
// for run and hands it to the sink (CW-20260519-0068). It is
// distinguished from emitStatus's dispatch/terminal transitions by
// heartbeat:true so a consumer renders it as an in-place update rather
// than a new status transition.
//
// Also stamps last_activity_at (CW-20260816-0004) — this ticker is
// Nanite's only cheap, already-live activity signal for a subagent run
// in flight, and the reaper now keys its inactivity branch off this
// column instead of pure elapsed time from started_at. A run whose
// streamSink is unwired (so this ticker never starts at all — see
// startHeartbeat) leaves last_activity_at empty forever; the reaper's
// COALESCE falls back to started_at in that case, i.e. the pre-fix
// elapsed-time behavior, not a crash.
//
// Only fields that startHeartbeat's caller guarantees are stable for
// the lifetime of the in-flight runner.Run call are read here (id,
// role, parent_session_id, retry_count, max_retries) — run.ChildSessionID
// is deliberately excluded because both Runner implementations
// (ChatRunner, BootRunner) write it from the runner's own goroutine
// partway through Run; reading it here without synchronization would
// race that write.
func (svc *Service) emitHeartbeat(run *Run, elapsed time.Duration) {
	svc.stampActivity(run.ID)
	if svc.streamSink == nil {
		return
	}
	payload, err := json.Marshal(map[string]any{
		"run_id":          run.ID,
		"role":            run.Role,
		"status":          StatusRunning,
		"heartbeat":       true,
		"elapsed_seconds": int(elapsed.Seconds()),
		"attempt":         run.RetryCount + 1,
		"max_retries":     run.MaxRetries,
	})
	if err != nil {
		slog.Warn("subagent: marshal heartbeat payload", "err", err, "run_id", run.ID)
		return
	}
	svc.streamSink.SubagentStatusChanged(run.ParentSessionID, payload)
}

// startHeartbeat launches a background ticker that calls emitHeartbeat
// for run every resolveHeartbeatInterval() until either the returned
// stop func is called or runCtx is done, whichever comes first.
//
// Callers MUST call the returned stop func exactly once, immediately
// after the guarded runner.Run call returns and before mutating any run
// field the heartbeat reads — otherwise a late tick can race those
// writes. A nil streamSink or a resolved interval of 0 (heartbeats
// disabled) makes this a no-op that still returns a safe stop func.
func (svc *Service) startHeartbeat(runCtx context.Context, run *Run) (stop func()) {
	interval := resolveHeartbeatInterval()
	if interval <= 0 || svc.streamSink == nil {
		return func() {}
	}
	stopCh := make(chan struct{})
	started := time.Now()
	safego.Go(runCtx, "subagent.heartbeat", func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-runCtx.Done():
				return
			case <-ticker.C:
				svc.emitHeartbeat(run, time.Since(started))
			}
		}
	})
	var once sync.Once
	return func() { once.Do(func() { close(stopCh) }) }
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

	// CW-20260519-0123: fail-fast role → profile resolution at the Spawn
	// boundary. Two distinct pathologies retired here:
	//
	//   - No-profile (c271, e.g. role="system-architect"): the LLM
	//     supplies a role name with no registered agent_profiles row.
	//     Before this gate, Spawn inserted a row with status=running,
	//     handed the runner an unresolvable slug, and the orphan reaper
	//     marked the row `failed` 60s later with "timeout: orphan, no
	//     child session" — the parent's spawn call returned "context
	//     deadline exceeded" after the wait, with zero structured signal
	//     about the cause.
	//   - Non-executable (c256, e.g. role="planner"): the profile exists
	//     but has can_execute=false, no tool surface. The runner drove a
	//     chat turn against it; the capture closed without `stream_end`
	//     and drainCapture returned empty. Wave-1 made this fail FAST as
	//     `stalled` (CW-20260519-0067), but the dispatch still misroutes.
	//
	// Gate semantics:
	//   - Profile missing: return ErrNoProfileForRole + role context. No
	//     DB write, no timer, no orphan row.
	//   - Profile present + CanExecute=false + slug NOT in
	//     textOnlyRoleSlugs (currently {"hint-selector"}): return
	//     ErrRoleNotExecutable. The whitelist preserves the legitimate
	//     PeerQuery dispatch path.
	//   - Resolver not wired (svc.profiles == nil): skip the gate
	//     entirely. Tests opt in via SetProfileResolver; production
	//     container always wires it.
	//
	// Errors wrap the sentinels so callers can errors.Is them; the MCP
	// transport layer (callSpawnSubagent) maps them to ErrorKindConfig
	// so the parent envelope distinguishes config faults from internal
	// failures.
	if svc.profiles != nil {
		profile, lookupErr := svc.profiles.GetAgentBySlug(ctx, req.Role)
		switch {
		case lookupErr == nil:
			if !profile.CanExecute && !isTextOnlyRole(req.Role) {
				return "", fmt.Errorf("%w: %q (slug=%q, can_execute=false, not in text-only whitelist)",
					ErrRoleNotExecutable, req.Role, profile.Slug)
			}
		case errors.Is(lookupErr, sql.ErrNoRows):
			return "", fmt.Errorf("%w %q", ErrNoProfileForRole, req.Role)
		default:
			// Lookup itself failed (DB transient / closed / etc.). Surface
			// the underlying error rather than masking it as a config issue
			// — the caller's retry policy may differ for transient faults.
			return "", fmt.Errorf("subagent: resolve role %q: %w", req.Role, lookupErr)
		}
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
		isChild, perr := svc.parentage.IsSubagentSession(ctx, req.ParentSessionID)
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
				// Outcome bookkeeping must survive cancellation of the rejected spawn it records.
				svc.eventLogger.LogEvent(context.WithoutCancel(ctx), req.ParentSessionID,
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

	// CW-20260519-0075 — checkpoint/resume + retry lifecycle (audit §P6).
	// Resolve the retry budget + on_fail policy in priority order:
	//   1. explicit per-call value when valid
	//   2. compiled-in defaults (DefaultMaxRetries = 3, DefaultOnFail = retry)
	// An explicit negative MaxRetries is normalized to zero (single-shot).
	// An unrecognized OnFail is rejected outright — fat-fingering a routing
	// policy must not silently default to retry.
	maxRetries := req.MaxRetries
	if maxRetries < 0 {
		maxRetries = 0
	} else if maxRetries == 0 {
		maxRetries = DefaultMaxRetries
	}
	onFail := req.OnFail
	if onFail == "" {
		onFail = DefaultOnFail
	}
	if !IsValidOnFail(onFail) {
		return "", fmt.Errorf("subagent: invalid on_fail %q (want retry|block|escalate)", onFail)
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
		MaxRetries:      maxRetries,
		OnFail:          onFail,
		AttemptsJSON:    "[]",
	}

	// H1 trust resolution: consult the agent's trust tier before any
	// approval-gate logic. Tier determines whether to refuse, gate, or
	// bypass the approval envelope. Phase 0 item 20 (retire workspaces):
	// resolution is no longer workspace-scoped — workspace_role_trust
	// (the override layer) is retired in full, operator-confirmed
	// 2026-08-18.
	trust := dispatch.TrustNormal
	if svc.trustResolver != nil && req.AgentProfileID != "" {
		t, terr := svc.trustResolver.ResolveTrust(ctx, req.AgentProfileID)
		if terr != nil {
			// Fail closed: treat resolve error as normal (require approval).
			slog.Warn("subagent: trust resolve error; defaulting to normal", "err", terr,
				"agent_profile_id", req.AgentProfileID)
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
			"agent_profile_id": req.AgentProfileID,
			"role":             req.Role,
		})
		if svc.eventLogger != nil {
			svc.eventLogger.LogEvent(ctx, req.ParentSessionID,
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
			us, err := svc.settings.GetUserSettings(ctx)
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
		fromAgentID, registerAs := svc.replyFromAgentID(run.Role)
		// CW-20260815-0027: resolve run.ParentAgentID to its real
		// agent_profiles.ID rather than passing a bare slug — see
		// replyToAgentID's doc comment for why that fails ValidateAgentID.
		toAgentID := svc.replyToAgentID(run.ParentAgentID)
		_, _ = svc.poster.SendMessage(ctx, messaging.SendInput{
			FromSessionID: run.ParentSessionID,
			FromAgentID:   fromAgentID,
			ToSessionID:   run.ParentSessionID,
			ToAgentID:     toAgentID,
			Channel:       messaging.ChannelChat,
			Kind:          messaging.KindReply,
			Body:          body,
			Type:          messaging.TypeMessage,
			RegisterAs:    registerAs,
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
	us, err := svc.settings.GetUserSettings(ctx)
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
// available or ctx is cancelled. Returns ErrSpawnFanoutCapReached if
// ctx.Done() fires while genuinely waiting for capacity (all 3 slots
// occupied), ctx.Err() for other cancellation reasons, or nil on
// successful acquisition.
func (svc *Service) acquireSpawnSlot(ctx context.Context) error {
	select {
	case svc.spawnSem <- struct{}{}:
		return nil
	case <-ctx.Done():
		// Context cancelled while waiting. Distinguish between "timed
		// out waiting for a slot" (at-capacity) vs "cancelled for
		// another reason" by attempting a non-blocking acquisition.
		// If the semaphore is full, we were genuinely at capacity.
		select {
		case svc.spawnSem <- struct{}{}:
			// A slot became available between ctx.Done() and this check.
			// Return it immediately and report the original cancellation.
			<-svc.spawnSem
			return ctx.Err()
		default:
			// Semaphore is still full — we timed out while waiting for
			// capacity. Return the distinguishable error sentinel.
			return ErrSpawnFanoutCapReached
		}
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
// CW-20260519-0073: the `context.WithTimeout` inside the attempt
// loop is now a generous wall-clock BACKSTOP
// (DefaultTimeoutSeconds = 1800s), not the governing bound. The
// real liveness signal is the child chat loop's inactivity
// terminator (shouldStop Layer 2, scoped to subagent dispatch).
//
// CW-20260519-0075 (audit §P6) — checkpoint/resume + retry lifecycle.
// execute is now a retry loop: on a retriable terminal outcome
// (over_budget / stalled / failed-with-OnFail=retry) and a remaining
// retry budget the prior attempt's state is folded into AttemptsJSON
// and the runner is invoked again. The child session is preserved
// across attempts so the chat path's message history carries forward
// (the runner skips createChildSession when ChildSessionID is set);
// an over_budget resume picks up the conversation rather than discards
// it. fabrication-suspected, cancelled, rejected, and any state that
// IsRetriableStatus rejects are never retried regardless of OnFail.
// max_retries (default 3) caps the chain length. Each attempt gets a
// fresh wall-clock budget — execute does not amortize the backstop
// across retries (a wedged provider on attempt 1 should not eat
// attempt 2's budget). Cancellation observed mid-chain bails out
// without further retries (the loop checks ctx + the persisted status
// before each iteration).
func (svc *Service) execute(ctx context.Context, run *Run, parentAgentID string) {
	// Clear the per-run cancel registration on exit so a late Cancel
	// call after terminal state is a cheap no-op (no stale func held,
	// no double-invocation).
	defer func() {
		svc.cancelMu.Lock()
		delete(svc.cancelers, run.ID)
		svc.cancelMu.Unlock()
	}()

	var (
		result *Result
		runErr error
	)
	for {
		// Each attempt gets its own bounded run context. The runner's
		// inactivity timeout governs liveness inside this window
		// (CW-20260519-0073).
		runCtx, cancel := context.WithTimeout(ctx, time.Duration(run.TimeoutSeconds)*time.Second)
		stopHeartbeat := svc.startHeartbeat(runCtx, run)
		result, runErr = svc.runner.Run(runCtx, run)
		stopHeartbeat()

		now := time.Now().UTC().Format(time.RFC3339Nano)
		if runErr != nil {
			// classifyRunOutcome MUST see runCtx with its real
			// post-runner Err() — that's how it tells "wall-clock
			// backstop fired" from "runner returned an error of its
			// own". Cancelling runCtx before this call would falsely
			// always report ctx.Err()==Canceled and the classifier
			// would misroute a genuine runner error to stalled /
			// over_budget.
			run.Status = classifyRunOutcome(runErr, result, runCtx)
			run.Error = runErr.Error()
			if result != nil {
				run.ResultJSON = structuredResultJSON(result)
			}
		} else {
			run.Status = StatusCompleted
			run.Error = ""
			if result != nil {
				run.ResultJSON = structuredResultJSON(result)
			}
		}
		// Now safe to release the per-attempt context — classification
		// has already captured what it needed from runCtx.
		cancel()
		run.CompletedAt = now

		// Retry-or-finalize decision.
		if !svc.shouldRetry(ctx, run, runErr) {
			break
		}

		// Fold the just-finished attempt into AttemptsJSON so the audit
		// trail preserves every iteration's outcome. The top-level
		// columns will be overwritten by the next attempt — capture
		// happens BEFORE that overwrite.
		run.AttemptsJSON = appendAttempt(run.AttemptsJSON, attemptRecord{
			Status:      run.Status,
			Error:       run.Error,
			ResultJSON:  run.ResultJSON,
			StartedAt:   run.StartedAt,
			CompletedAt: run.CompletedAt,
		})
		run.RetryCount++
		// Persist progress so a process crash mid-chain still leaves
		// the row coherent (status reverts to running, attempts_json
		// + retry_count carry the prior history). The next attempt
		// will overwrite the terminal fields.
		if err := svc.persistRetryCheckpoint(ctx, run); err != nil {
			slog.Warn("subagent: persist retry checkpoint", "err", err, "run_id", run.ID,
				"retry_count", run.RetryCount)
		}
		// Reset the attempt-local fields so the next iteration writes
		// fresh state instead of carrying the prior attempt's residue.
		run.Status = StatusRunning
		run.Error = ""
		run.ResultJSON = "{}"
		run.CompletedAt = ""

		slog.Info("subagent: retrying run",
			"run_id", run.ID,
			"role", run.Role,
			"retry_count", run.RetryCount,
			"max_retries", run.MaxRetries,
			"reason_status", lastAttemptStatus(run.AttemptsJSON),
		)
	}

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
	// CW-20260815-0023: resolve run.Role to its real agent_profiles.ID
	// rather than passing the bare role string — see replyFromAgentID's
	// doc comment for why that collided with the agent_profiles.slug
	// UNIQUE constraint on every reply.
	fromAgentID, registerAs := svc.replyFromAgentID(run.Role)
	// CW-20260815-0027: resolve parentAgentID to its real agent_profiles.ID
	// rather than passing a bare slug — see replyToAgentID's doc comment
	// for why that fails ValidateAgentID.
	toAgentID := svc.replyToAgentID(parentAgentID)
	// CW-20260512-0019: Kind=subagent_result (not the generic KindReply)
	// so chat_generate.go's turn-start injection and the harness-reaction
	// layer (CW-20260520-0001) can query for this specifically. Emitted
	// unconditionally for every mode — sync/interactive completions
	// already surface in the same turn's tool result, but a durable,
	// queryable agent_messages row also prevents the c271 failure mode
	// (a later turn re-deriving/hallucinating a completion it can no
	// longer see in its own context window).
	msg, err := svc.poster.SendMessage(finalCtx, messaging.SendInput{
		FromSessionID: run.ParentSessionID,
		FromAgentID:   fromAgentID, // the subagent is the sender
		ToSessionID:   run.ParentSessionID,
		ToAgentID:     toAgentID,
		Channel:       replyChannel,
		Kind:          messaging.KindSubagentResult,
		Body:          summary,
		PayloadJSON:   resultPayload,
		Type:          messaging.TypeMessage,
		RegisterAs:    registerAs,
	})
	if err != nil {
		slog.Warn("subagent: reply delivery", "err", err, "run_id", run.ID)
		return
	}
	// CW-20260520-0001 (Layer 2): let the harness react to this
	// completion (e.g. proactively trigger a summarizing turn on the
	// parent session per its configured policy). Only meaningful for
	// modes where the parent's turn has already ended (async/api) — for
	// sync/interactive the parent turn is still active, so the reactor's
	// own busy-check naturally no-ops. Fire-and-forget in its own
	// goroutine so a slow/misbehaving reactor never blocks finalizeRun's
	// caller.
	if svc.reactor != nil {
		runCopy := run
		msgID := msg.ID
		safego.Go(context.Background(), "subagent.completion-reactor", func() {
			svc.reactor.ReactToCompletion(context.Background(), runCopy, msgID)
		})
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
	attempts := r.AttemptsJSON
	if attempts == "" {
		attempts = "[]"
	}
	_, err := svc.db.ExecContext(ctx,
		`INSERT INTO subagent_runs (id, parent_session_id, child_session_id, role, prompt,
		                            mode, status, inputs_json, result_json, error,
		                            timeout_seconds, created_at, started_at, completed_at,
		                            parent_agent_id, envelope_instance_id,
		                            approved_at, approved_by,
		                            rejected_at, rejection_reason,
		                            provider,
		                            retry_count, max_retries, on_fail, attempts_json)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.ParentSessionID, r.ChildSessionID, r.Role, r.Prompt,
		r.Mode, r.Status, r.InputsJSON, r.ResultJSON, r.Error,
		r.TimeoutSeconds, r.CreatedAt, r.StartedAt, r.CompletedAt,
		r.ParentAgentID, r.EnvelopeInstanceID,
		r.ApprovedAt, r.ApprovedBy,
		r.RejectedAt, r.RejectionReason,
		r.Provider,
		r.RetryCount, r.MaxRetries, r.OnFail, attempts,
	)
	return err
}

// finalizeRun updates the terminal fields of a run (status, result,
// error, completed_at). Guarded so a concurrent Cancel that flipped
// the row to 'cancelled' wins the race — without this, a runner
// that was cancelled mid-flight would have its cancellation
// overwritten by the completed/failed terminal state this function
// wants to write.
//
// CW-20260519-0075: also persists retry_count + attempts_json so the
// chain audit trail survives the final UPDATE. AttemptsJSON is
// recorded incrementally by persistRetryCheckpoint between attempts;
// this just makes sure the final row reflects the full history.
func (svc *Service) finalizeRun(ctx context.Context, r *Run) error {
	attempts := r.AttemptsJSON
	if attempts == "" {
		attempts = "[]"
	}
	res, err := svc.db.ExecContext(ctx,
		`UPDATE subagent_runs
		   SET status = ?, result_json = ?, error = ?, completed_at = ?,
		       retry_count = ?, attempts_json = ?
		 WHERE id = ? AND status IN (?, ?, ?)`,
		r.Status, r.ResultJSON, r.Error, r.CompletedAt,
		r.RetryCount, attempts,
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

// shouldRetry decides whether execute's loop should run another
// iteration (CW-20260519-0075, audit §P6). The retry decision is
// gated by:
//
//   - the outcome's retriability (IsRetriableStatus + on_fail policy)
//   - remaining budget (retry_count < max_retries)
//   - a concurrent Cancel observed via either ctx or the persisted
//     row status (a Cancel issued between attempts must NOT trigger
//     another retry — that would resurrect a cancelled run).
//
// Unretriable outcomes never retry regardless of budget:
//   - fabrication-suspected (the LLM produced unreliable text;
//     re-running won't reliably fix that)
//   - cancelled, rejected (operator decisions)
//
// over_budget always retries within budget regardless of on_fail —
// it is not a routing-on-fail case, it is a "productive run hit the
// wall clock" case that the audit explicitly wants resumed rather
// than discarded. on_fail=block / escalate only suppresses retry for
// the failed / stalled buckets.
func (svc *Service) shouldRetry(ctx context.Context, run *Run, runErr error) bool {
	// Cancellation observed via ctx (parent cancelled the run): bail
	// without retrying. The runner's own ctx already cancelled; the
	// row should land in its final state.
	if ctx != nil && ctx.Err() != nil {
		return false
	}

	if run.RetryCount >= run.MaxRetries {
		return false
	}

	if !IsRetriableStatus(run.Status) {
		return false
	}

	// Fabrication-suspected: never auto-retry. The fabrication detector
	// trips precisely when the model produced ungrounded text; re-running
	// is not a fix and may compound the problem. The error wraps the
	// sentinel via errors.Is on the runner side, but the package boundary
	// here cannot import the runner's sentinel — encode the test as a
	// substring match on the canonical error prefix the runner emits
	// (subagent_runner.go errSubagentFabricationSuspected). The substring
	// is stable across the runner; if it changes a test catches the drift.
	if runErr != nil && strings.Contains(runErr.Error(), "fabrication suspected") {
		return false
	}

	// on_fail routing applies to the failed/stalled buckets. over_budget
	// is the resume case and is always retriable within budget.
	if run.Status == StatusFailed || run.Status == StatusStalled {
		switch run.OnFail {
		case OnFailBlock, OnFailEscalate:
			return false
		}
	}

	// Concurrent Cancel: re-read the row's status before committing to
	// another attempt. A Cancel that landed between attempts updates the
	// row to cancelled; we must observe that and abort the chain rather
	// than the next runner.Run resurrecting it. Use a short-bounded
	// background ctx so a cancelled parent ctx doesn't prevent the read.
	checkCtx, checkCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer checkCancel()
	var persistedStatus string
	if err := svc.db.QueryRowContext(checkCtx,
		`SELECT status FROM subagent_runs WHERE id = ?`, run.ID,
	).Scan(&persistedStatus); err == nil {
		if persistedStatus == StatusCancelled || persistedStatus == StatusRejected {
			return false
		}
	}
	// On a DB error, conservatively allow the retry — the next attempt's
	// finalize will still observe the cancel via finalizeRun's guarded
	// UPDATE. The whole row check is defense-in-depth.

	return true
}

// attemptRecord is the per-attempt audit detail folded into Run.AttemptsJSON
// before each retry. Mirrors the run row's terminal-field shape so
// dashboards can render an attempt the same way as a final row. Only the
// fields that change attempt-to-attempt are recorded; static fields
// (role, prompt, mode, etc.) live once on the row.
type attemptRecord struct {
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
	ResultJSON  string `json:"result_json,omitempty"`
	StartedAt   string `json:"started_at,omitempty"`
	CompletedAt string `json:"completed_at,omitempty"`
}

// appendAttempt parses the existing AttemptsJSON array, appends the new
// entry, and returns the marshalled result. A malformed prior value is
// recovered to an empty array (a single corrupt attempts column must not
// poison the rest of the chain — the loss is one entry of audit, not
// run correctness). Returns "[]" on a marshal failure (effectively
// impossible for a fixed-shape struct of strings).
func appendAttempt(prior string, rec attemptRecord) string {
	var attempts []attemptRecord
	if prior != "" && prior != "[]" {
		if err := json.Unmarshal([]byte(prior), &attempts); err != nil {
			slog.Warn("subagent: attempts_json malformed; rebuilding from this attempt",
				"prior_len", len(prior), "err", err)
			attempts = nil
		}
	}
	attempts = append(attempts, rec)
	out, err := json.Marshal(attempts)
	if err != nil {
		return "[]"
	}
	return string(out)
}

// lastAttemptStatus reads the last entry's status from an AttemptsJSON
// blob for structured logging. Returns the empty string on any parse
// problem — the log line is informational and must not fail the run.
func lastAttemptStatus(attemptsJSON string) string {
	if attemptsJSON == "" || attemptsJSON == "[]" {
		return ""
	}
	var attempts []attemptRecord
	if err := json.Unmarshal([]byte(attemptsJSON), &attempts); err != nil {
		return ""
	}
	if len(attempts) == 0 {
		return ""
	}
	return attempts[len(attempts)-1].Status
}

// persistRetryCheckpoint writes the in-between-attempts state to the row
// so a process crash mid-chain leaves coherent persistence: the row
// status reverts to `running` (so the reaper can still sweep it),
// retry_count + attempts_json carry the prior history, and the result/
// error/completed_at columns are reset so the next attempt's UPDATE
// (via finalizeRun) writes fresh state. Uses a 5s timeout on a fresh
// background ctx so a cancelled parent doesn't prevent the write.
//
// This persistence is best-effort: a failure here is logged but does not
// abort the retry chain. The in-memory Run state is the source of truth
// inside execute's loop; the DB row is the eventual surface that a
// crash-recovery read would consult.
func (svc *Service) persistRetryCheckpoint(parentCtx context.Context, run *Run) error {
	attempts := run.AttemptsJSON
	if attempts == "" {
		attempts = "[]"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := svc.db.ExecContext(ctx,
		`UPDATE subagent_runs
		   SET status = ?, retry_count = ?, attempts_json = ?,
		       error = '', result_json = '{}', completed_at = ''
		 WHERE id = ? AND status NOT IN (?, ?)`,
		StatusRunning, run.RetryCount, attempts,
		run.ID, StatusCancelled, StatusRejected,
	)
	return err
}

// selectSQL is the canonical SELECT clause for subagent_runs rows.
const selectSQL = `SELECT id, parent_session_id, child_session_id, role, prompt,
	mode, status, inputs_json, result_json, error,
	timeout_seconds, created_at, started_at, completed_at,
	parent_agent_id, envelope_instance_id, approved_at, approved_by,
	rejected_at, rejection_reason, provider,
	retry_count, max_retries, on_fail, attempts_json
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
		&r.RetryCount, &r.MaxRetries, &r.OnFail, &r.AttemptsJSON,
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
