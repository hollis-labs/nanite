package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/dispatcher"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
)

// fallbackRoleSlug is the agent profile slug used when a requested role
// slug does not resolve. Mirrors the reflex-catalog pattern (CW-20260509-0050)
// where researcher / reviewer / documentor / strategist all fall back to
// the `worker` profile. Surfacing the fallback via slog.Warn so seeding
// drift is alertable without surprising the caller with a hard failure.
const fallbackRoleSlug = "worker"

// resolveRoleWithFallback looks up a slug through the resolver. If the
// slug is unknown (wraps sql.ErrNoRows), retries with fallbackRoleSlug
// and emits a structured warning. If the fallback also misses, the
// underlying error is returned wrapped with errRoleResolveFailed.
//
// caller identifies the runner emitting the warning (ChatRunner / BootRunner)
// so alerting can attribute the drift correctly.
func resolveRoleWithFallback(agents agentSlugResolver, slug, caller string) (*store.AgentProfile, error) {
	agent, err := agents.GetAgentBySlug(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, slug)
	if err == nil {
		return agent, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w %q: %w", errRoleResolveFailed, slug, err)
	}
	// Unknown slug. Try the fallback before surfacing failure.
	fallback, fbErr := agents.GetAgentBySlug(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, fallbackRoleSlug)
	if fbErr != nil {
		// Fallback itself missing — the deployment is misconfigured;
		// surface the ORIGINAL slug so logs point at the user's request.
		return nil, fmt.Errorf("%w %q: %w (fallback %q also missing: %w)",
			errRoleResolveFailed, slug, err, fallbackRoleSlug, fbErr)
	}
	slog.Warn("subagent: role slug not found, falling back",
		"requested_slug", slug,
		"fallback_slug", fallbackRoleSlug,
		"caller", caller,
	)
	return fallback, nil
}

// errStreamFailure is the sentinel returned by drainCapture when the
// child chat loop emits an error event. The actual error message is
// preserved in the wrapped error.
var errStreamFailure = errors.New("subagent: child chat loop emitted error event")

// errSubagentFabricationSuspected is the sentinel returned by ChatRunner.Run
// when the drained turn shows the fabrication-suspected pattern: the child
// invoked at least one tool, every tool_result emitted came back with
// IsError=true (or no successful tool roundtrips were observed), yet the
// child still produced non-empty assistant text. The text in that scenario
// is, by construction, not grounded in any successful tool call — most
// often it is training-data synthesis dressed up as analysis (c160 turn 16
// evidence, CW-20260512-0095). subagent.Service maps a non-nil run-error
// to subagent_runs.status = "failed", which is the acceptance criterion
// the parent agent can read to avoid downstream "I successfully set up X"
// claims when the subagent's tools never actually succeeded.
var errSubagentFabricationSuspected = errors.New("subagent: fabrication suspected — tools attempted but none succeeded, yet assistant produced non-empty text")

// errZeroOutput is the sentinel returned by ChatRunner.Run when the child
// turn drained cleanly (no error event) but produced *nothing* — no
// assistant text, no tool calls, and no envelope (CW-20260519-0067, audit
// §P5). This is the "planner subagent hangs the full timeout with zero
// output" pathology: the child loop's goroutine exited and closed the
// capture channel without ever emitting a delta, a tool_call, an envelope,
// or an error event. drainCapture returns (summary="", envelope="{}",
// counts={}, err=nil), and before this gate ChatRunner.Run applied the
// polite "completed without text response" fallback and returned a nil
// error — so subagent.execute stamped StatusCompleted on a run that did
// absolutely nothing for the entire budget.
//
// The output-presence gate (zeroOutputRun) converts that into this
// sentinel. It is joined with subagent.ErrStalled so the existing
// CW-20260519-0074 run-outcome classifier (classifyRunOutcome) routes it
// to StatusStalled — a run that went silent and never produced a
// deliverable is, by construction, stalled. No new status is invented:
// the 0074 taxonomy already has the right vocabulary.
var errZeroOutput = errors.New("subagent: child run produced no text, no tool calls, and no envelope")

// toolUsageCounts tallies tool_call and tool_result events partitioned by
// IsError so the fabrication-suspected detector can decide whether to mark
// a child run failed. Internal to drainCapture's contract.
type toolUsageCounts struct {
	calls          int // tool_call events seen
	resultsError   int // tool_result events with IsError=true
	resultsSuccess int // tool_result events with IsError=false
}

// drainCapture consumes a chat.StreamEvent channel and assembles a
// summary string + envelope payload for subagent.Result. Pure logic,
// extracted from ChatRunner.Run so the parsing rules can be unit
// tested without spinning up generateResponse.
//
// Rules:
//   - "delta" events: append Content to the summary builder
//   - "plugin_envelope" events: overwrite envelope buffer with
//     Envelope field (last-wins)
//   - "tool_call" events: increment counts.calls (fabrication-detector
//     input — CW-20260512-0095)
//   - "tool_result" events: increment counts.resultsError when IsError
//     is true, counts.resultsSuccess otherwise. The request_tools
//     meta-tool emits tool_result events with IsError=false even on
//     outcomes the LLM should treat as "no useful answer" (reflection
//     prompt, hard halt, empty load); the detector intentionally counts
//     those as successful roundtrips — they're real harness interactions,
//     not fabrication risk.
//   - "error" / "structured_error" events: terminate drain, return
//     errStreamFailure wrapped with the error message
//   - "stream_end": capture evt.Envelope if non-empty (production
//     generateResponse attaches the terminal aggregated envelope JSON
//     there — chat_generate.go:837), then terminate drain successfully
//   - everything else (stream_start, status, presence, etc.): ignored
//     for capture purposes
//
// Returns summary, envelope (always valid JSON; "{}" when no envelope
// event was seen), tool-usage counts (for the caller's fabrication
// detector — load-bearing for CW-20260512-0095), and a non-nil error
// if the stream emitted an error event.
func drainCapture(ch <-chan chat.StreamEvent) (summary string, envelope string, counts toolUsageCounts, err error) {
	var sb strings.Builder
	envelope = "{}"

	for evt := range ch {
		switch evt.Type {
		case "delta":
			sb.WriteString(evt.Content)
		case "plugin_envelope":
			if evt.Envelope != "" {
				envelope = evt.Envelope
			}
		case "tool_call":
			counts.calls++
		case "tool_result":
			if evt.IsError {
				counts.resultsError++
			} else {
				counts.resultsSuccess++
			}
		case "error", "structured_error":
			msg := evt.Error
			if msg == "" {
				msg = "stream error event with no message"
			}
			// CW-20260519-0074 — run status taxonomy. The provider-stream
			// inactivity watchdog (CW-20260517-0036) emits its terminal
			// error event with a structured `cause:"stalled"` detail
			// (chat_generate.go:1530). Join subagent.ErrStalled so the
			// run-outcome classifier in subagent.execute can errors.Is it
			// and stamp StatusStalled instead of the generic StatusFailed.
			// A genuine provider error / crash carries no `stalled` cause
			// and so does not get the sentinel.
			if isStalledErrorEvent(evt) {
				return sb.String(), envelope, counts,
					errors.Join(errStreamFailure, subagent.ErrStalled, errors.New(msg))
			}
			return sb.String(), envelope, counts, errors.Join(errStreamFailure, errors.New(msg))
		case "stream_end":
			// Production generateResponse attaches the final aggregated
			// envelope JSON to stream_end.Envelope. Mid-stream
			// plugin_envelope delivery additionally depends on
			// StreamManager routing that isn't guaranteed here — prefer
			// the terminal payload when present so ResultJSON reflects
			// the actual turn output rather than falling back to "{}".
			if evt.Envelope != "" {
				envelope = evt.Envelope
			}
			return sb.String(), envelope, counts, nil
		}
	}

	// Channel closed without stream_end — treat as a clean drain.
	return sb.String(), envelope, counts, nil
}

// isStalledErrorEvent reports whether a stream error event was produced
// by the provider-stream inactivity watchdog (CW-20260517-0036) rather
// than by a provider-emitted error or a crash. The watchdog tags its
// ErrorEvent's structured details with `cause:"stalled"`
// (chat_generate.go:1530); a genuine provider error carries no such
// detail. Drives the StatusStalled-vs-StatusFailed split in the
// CW-20260519-0074 run-outcome classifier.
//
// The check is defensive: it tolerates a nil StructuredError (the event
// constructor always sets one for the stalled path, but a future event
// shape change shouldn't panic the drain) and a missing/non-string
// `cause`.
func isStalledErrorEvent(evt chat.StreamEvent) bool {
	if evt.StructuredError == nil {
		return false
	}
	cause, ok := evt.StructuredError.Details["cause"].(string)
	return ok && cause == "stalled"
}

// detectFabrication returns a non-nil error when the drained turn matches
// the fabrication-suspected pattern: the child attempted at least one tool
// call AND no tool_result came back successful AND the assistant text is
// non-empty. The intent is to convert a "subagent fabricated a polished
// analysis because the data tools all failed" turn into a failed subagent
// run the parent can detect via subagent_runs.status = "failed" instead of
// being handed the fabricated text as authoritative output.
//
// Returns nil in three cases:
//
//   - the child made no tool calls at all (text-only reply — no fabrication
//     signal here; the parent's own reply may still be wrong but that is a
//     separate problem for the parent-side reporting layer, CW-20260512-0096);
//   - at least one tool_result came back successful (the text is at least
//     partially grounded in real tool output — the universal Refusal rules
//     govern whether that grounding is sufficient);
//   - the summary is empty (the empty-summary fallback in ChatRunner.Run
//     turns this into a "completed without text response" surface that the
//     parent will not mistake for grounded analysis).
//
// The returned error wraps errSubagentFabricationSuspected so callers can
// match with errors.Is, and carries a structured reason string for
// operators inspecting subagent_runs.error.
func detectFabrication(summary string, counts toolUsageCounts) error {
	if counts.calls == 0 {
		return nil
	}
	if counts.resultsSuccess > 0 {
		return nil
	}
	if strings.TrimSpace(summary) == "" {
		return nil
	}
	return fmt.Errorf("%w: tool_calls=%d, tool_results_error=%d, tool_results_success=%d, assistant_text_chars=%d",
		errSubagentFabricationSuspected,
		counts.calls, counts.resultsError, counts.resultsSuccess, len(summary))
}

// partialResult assembles a *subagent.Result from whatever drainCapture
// accumulated before the run ended in error (CW-20260519-0071, audit §P2).
//
// The error path used to discard the accumulated `summary`/`envelope`/
// `counts` entirely — `if runErr != nil { return nil, runErr }` — so a
// subagent guillotined mid-productive-work (e.g. cut by the wall-clock
// backstop after writing real files) left no structured trace of what it
// got done. subagent_runs.status was `failed` and result_json sat at its
// insert-time default. This helper lets ChatRunner.Run return the partial
// result *alongside* the error so subagent.execute can persist it on the
// StatusFailed branch.
//
// The ResultJSON it produces is a structured "partial" envelope that
// records: which envelope (if any) the child emitted, the accumulated
// assistant text, and the tool-usage counts (how many tool calls were
// made and how many succeeded/failed — a proxy for "how much real work
// happened before the cut"). It deliberately does NOT carry the error
// itself: capture is additive, the error stays intact on its own path.
//
// Returns nil when nothing was accumulated (no summary, no envelope, no
// tool activity) — there is no partial work worth persisting, and a nil
// result keeps subagent.execute's existing "leave result_json at default"
// behavior for genuinely empty failures.
func partialResult(summary, envelope string, counts toolUsageCounts) *subagent.Result {
	hasEnvelope := envelope != "" && envelope != "{}"
	hasActivity := counts.calls > 0 || counts.resultsSuccess > 0 || counts.resultsError > 0
	if strings.TrimSpace(summary) == "" && !hasEnvelope && !hasActivity {
		return nil
	}

	// envelopeRaw is the child's structured envelope JSON (last-wins from
	// drainCapture). Embedded as a raw message so a real envelope is not
	// double-encoded; falls back to {} when none was emitted.
	envelopeRaw := json.RawMessage("{}")
	if hasEnvelope && json.Valid([]byte(envelope)) {
		envelopeRaw = json.RawMessage(envelope)
	}

	payload, err := json.Marshal(struct {
		Partial  bool            `json:"partial"`
		Summary  string          `json:"summary"`
		Envelope json.RawMessage `json:"envelope"`
		Tools    struct {
			Calls          int `json:"calls"`
			ResultsSuccess int `json:"results_success"`
			ResultsError   int `json:"results_error"`
		} `json:"tools"`
	}{
		Partial:  true,
		Summary:  summary,
		Envelope: envelopeRaw,
		Tools: struct {
			Calls          int `json:"calls"`
			ResultsSuccess int `json:"results_success"`
			ResultsError   int `json:"results_error"`
		}{
			Calls:          counts.calls,
			ResultsSuccess: counts.resultsSuccess,
			ResultsError:   counts.resultsError,
		},
	})
	if err != nil {
		// Marshalling a fixed-shape struct of strings/ints/RawMessage
		// effectively cannot fail; fall back to summary-only capture.
		return &subagent.Result{Summary: summary}
	}
	return &subagent.Result{Summary: summary, ResultJSON: string(payload)}
}

// zeroOutputRun reports whether a *successfully drained* child turn (no
// error event) produced no usable output at all: no assistant text, no
// tool calls, and no structured envelope (CW-20260519-0067, audit §P5).
//
// This is the output-presence gate. It is deliberately a lightweight
// presence check, not a semantic deliverable validator — it asks only
// "did the child emit anything?", not "is what it emitted any good?".
//
// The genuine "completed with a real but text-light result" case is NOT
// a zero-output run and must still land as StatusCompleted:
//   - a child that made tool calls (counts.calls > 0) did real work even
//     if it emitted no closing prose;
//   - a child that produced a structured envelope (envelope != "{}")
//     delivered a card/result even with empty assistant text.
//
// Only the all-three-empty case — silent for the whole budget — trips
// the gate. The summary check trims whitespace so a child that emitted
// only blank deltas is still treated as silent.
func zeroOutputRun(summary, envelope string, counts toolUsageCounts) bool {
	hasText := strings.TrimSpace(summary) != ""
	hasEnvelope := envelope != "" && envelope != "{}"
	hasToolActivity := counts.calls > 0 ||
		counts.resultsSuccess > 0 || counts.resultsError > 0
	return !hasText && !hasEnvelope && !hasToolActivity
}

// errRoleResolveFailed is the sentinel for when GetAgentBySlug fails.
// Wrapped error preserves the slug + the underlying error.
var errRoleResolveFailed = errors.New("subagent runner: resolve role")

// agentSlugResolver is the narrow surface ChatRunner needs to look up
// an agent profile by slug. Satisfied by any AgentReader in production;
// lets test stubs implement only this method.
type agentSlugResolver interface {
	GetAgentBySlug(ctx context.Context, slug string) (*store.AgentProfile, error)
}

// sessionStoreForRunner is the narrow surface of *store.Store the
// runner needs. Lets tests inject without spinning up sqlite.
type sessionStoreForRunner interface {
	CreateSession(context.Context, *store.Session) error
	GetSession(ctx context.Context, id string) (*store.Session, error)
	EnsureSessionAgent(ctx context.Context, sessionID, agentID, mode string, isPrimary bool) error
	CreateMessage(context.Context, *store.Message) error
}

// chatInvoker is the narrow surface the runner needs from
// chatServiceImpl. Lets tests swap in a fake without exporting
// generateResponse. Satisfied structurally by *chatServiceImpl.
type chatInvoker interface {
	generateResponse(ctx context.Context, sessionID, msgID, prompt string, ch chan chat.StreamEvent)
}

// ChatRunner implements subagent.Runner by driving a single assistant
// turn through chatServiceImpl.generateResponse against a freshly
// created persisted child session. Lives in package service for
// unexported access to chatServiceImpl.
type ChatRunner struct {
	chat   *chatServiceImpl
	agents agentSlugResolver
	store  sessionStoreForRunner
	db     *sql.DB

	// pathGrants is the session-scoped grant store. The runner stamps a
	// (childSession → parentSession) lineage entry at spawn time and
	// clears it via defer at spawn-finish so dev_* lookups against the
	// worker session can fall through to the parent's explicit-mention
	// grants. nil-safe — RegisterLineage / ClearLineage no-op when the
	// store is unset (tests that don't exercise lineage leave it nil).
	pathGrants *permission.PathGrants

	// Test-only override hooks. Production wiring leaves these nil; the
	// runner falls back to r.chat.generateResponse and the real DB UPDATE.
	invoker   chatInvoker
	persistFn func(ctx context.Context, runID, childID string) error
}

// NewChatRunner constructs a runner. All deps are required in production;
// tests can leave fields unset when they don't exercise that code path.
// pathGrants is the session-scoped grant store used to register the
// (worker → parent) lineage at spawn time so the worker's dev_* tool
// calls can resolve paths the user explicitly granted in the parent
// chat. nil-safe.
func NewChatRunner(c *chatServiceImpl, agents agentSlugResolver, st sessionStoreForRunner, db *sql.DB, pathGrants *permission.PathGrants) *ChatRunner {
	return &ChatRunner{chat: c, agents: agents, store: st, db: db, pathGrants: pathGrants}
}

// invokeChat delegates to the test override or routes through the
// single dispatcher door (CW-20260512-0121 / SP-20260512-0011). The
// dispatcher stamps CallerSubagent on ctx so the runner's request_build
// telemetry reports caller=subagent — the cross-call-site slot-shape
// invariant the consolidation ticket cashes in.
//
// Test override path: r.invoker exists for tests that want to stub out
// generateResponse entirely (subagent_runner_test.go fakeChatService,
// _lineage_test.go chatInvokerFunc). When set, it short-circuits the
// dispatcher — those tests assert on the runner's behavior, not on
// the dispatcher's CallerType plumbing (the dispatcher path is
// covered by internal/dispatcher tests + this file's new
// subagent_runner_dispatcher_test.go).
func (r *ChatRunner) invokeChat(ctx context.Context, sessionID, msgID, prompt string, ch chan chat.StreamEvent) {
	if r.invoker != nil {
		r.invoker.generateResponse(ctx, sessionID, msgID, prompt, ch)
		return
	}
	if err := r.chat.dispatcher.Run(ctx, dispatcher.Request{
		SessionID:      sessionID,
		AssistantMsgID: msgID,
		UserContent:    prompt,
		CallerType:     dispatcher.CallerSubagent,
	}, ch); err != nil {
		slog.Error("subagent: dispatcher.Run rejected subagent request",
			"session_id", sessionID,
			"assistant_msg_id", msgID,
			"err", err,
		)
		// Dispatcher rejected pre-runner; close ch so the drain
		// goroutine in ChatRunner.Run terminates rather than blocking
		// indefinitely. Mirrors the dispatcher-rejection close in
		// chat.launchGeneration.
		close(ch)
	}
}

// persistChild delegates to the test override or the real persistChildSessionID.
func (r *ChatRunner) persistChild(ctx context.Context, runID, childID string) error {
	if r.persistFn != nil {
		return r.persistFn(ctx, runID, childID)
	}
	return r.persistChildSessionID(ctx, runID, childID)
}

// resolveRole looks up the role slug in the agent registry, falling back
// to the `worker` profile when the slug is unknown (sql.ErrNoRows). Other
// errors wrap with errRoleResolveFailed so callers can use errors.Is for
// classification. See resolveRoleWithFallback.
func (r *ChatRunner) resolveRole(slug string) (*store.AgentProfile, error) {
	return resolveRoleWithFallback(r.agents, slug, "ChatRunner")
}

// createChildSession builds a persisted child session row bound to the
// resolved agent's provider/model defaults. Workspace inherits from
// the parent session. The child session is bound to the agent via
// EnsureSessionAgent so generateResponse's ResolveForSession lookup
// (chat_generate.go:88) finds the row.
//
// Provider resolution order:
//  1. run.Provider (from SpawnRequest.Provider) when non-empty — caller override
//  2. agent.DefaultProvider — agent profile default
//  3. parent.Provider — inherit the parent session's provider so a subagent
//     spawned from an HTTP/provider-backed session does not silently fall back
//     to the user's global default (for example pty).
func (r *ChatRunner) createChildSession(ctx context.Context, run *subagent.Run, agent *store.AgentProfile) (string, error) {
	parent, err := r.store.GetSession(ctx, run.ParentSessionID)
	if err != nil {
		return "", fmt.Errorf("get parent session: %w", err)
	}
	// Prefer the per-spawn provider override; fall back to agent profile default.
	provider := run.Provider
	if provider == "" {
		provider = agent.DefaultProvider
	}
	if provider == "" {
		provider = parent.Provider
	}
	childID := uuid.New().String()
	if err := r.store.CreateSession(ctx, &store.Session{
		ID:       childID,
		Provider: provider,
		Model:    agent.DefaultModel,
		Title:    fmt.Sprintf("subagent: %s — %s", run.Role, truncatePrompt(run.Prompt, 60)),
	}); err != nil {
		return "", fmt.Errorf("create child session: %w", err)
	}
	// Bind the child session to the resolved agent so generateResponse's
	// ResolveForSession lookup finds it. Without this, chat_generate.go:88
	// returns "Failed to resolve agent".
	if err := r.store.EnsureSessionAgent(ctx, childID, agent.ID, "default", true); err != nil {
		return "", fmt.Errorf("bind child session to agent: %w", err)
	}
	return childID, nil
}

// persistChildSessionID writes the child session id back onto the
// subagent_runs row so Status / inspection see it.
func (r *ChatRunner) persistChildSessionID(ctx context.Context, runID, childID string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE subagent_runs SET child_session_id = ? WHERE id = ?`,
		childID, runID,
	)
	if err != nil {
		return fmt.Errorf("persist child_session_id: %w", err)
	}
	return nil
}

// Run executes one assistant turn for the subagent run. It resolves the
// role to an agent profile, creates a child session (bound to the agent),
// persists the child session ID, then drives generateResponse in a
// goroutine and drains the resulting stream into a Result.
//
// CW-20260519-0075 (audit §P6) — checkpoint/resume + retry. When the
// caller hands us a Run whose ChildSessionID is already populated (this
// is a retry / resume attempt produced by execute's loop), skip the
// createChildSession + persistChildSessionID + lineage-registration work
// and reuse the prior session. The child's existing message history is
// the resume substrate: a fresh user message is appended below and
// generateResponse picks up the full conversation, so the LLM continues
// where it left off rather than restarting from scratch. This is the
// "checkpoint a productive over_budget run and resume it" half of the
// audit.
func (r *ChatRunner) Run(ctx context.Context, run *subagent.Run) (*subagent.Result, error) {
	agent, err := r.resolveRole(run.Role)
	if err != nil {
		return nil, err
	}
	isResume := run.ChildSessionID != ""
	var childID string
	if isResume {
		childID = run.ChildSessionID
	} else {
		childID, err = r.createChildSession(ctx, run, agent)
		if err != nil {
			return nil, err
		}
		if err := r.persistChild(ctx, run.ID, childID); err != nil {
			return nil, err
		}
		run.ChildSessionID = childID
	}

	// Path-grant lineage: stamp (worker → parent) so the worker session's
	// dev_* lookups can fall through to grants the user explicitly issued
	// in the parent chat thread (e.g. the ~/Projects-apps/nanite mention
	// that prompted task_execute). Profile permissions stay
	// isolated; this only widens the session-scoped explicit-mention
	// grant store, which is naturally scoped to the conversation thread.
	// Cleared via defer so the entry's lifetime is exactly the worker's.
	//
	// CW-20260519-0075: on a retry/resume the lineage is re-registered
	// for the same childID (RegisterLineage is keyed by childID, so a
	// second call overwrites the same entry harmlessly). The defer still
	// clears at the end of this attempt, but execute's loop re-enters
	// Run for the next attempt and re-registers — no leak, no stale
	// pointer.
	r.pathGrants.RegisterLineage(childID, run.ParentSessionID)
	defer r.pathGrants.ClearLineage(childID)

	// CW-20260512-0119 (SP-20260512-0010 W3): explicitly forward the
	// parent's effective denies into the child's effective permission
	// set. Mirrors opencode's `deriveSubagentSessionPermission`
	// (agent/subagent-permissions.ts:24-32, issue #26514) — the bug
	// class where a child silently bypasses a parent-level Plan-mode
	// deny because naive inheritance reads only the session's own rules.
	//
	// Parent rules source: the parent session's previously-derived
	// RuleSet (set by the runner when the parent was itself a subagent
	// — composes across a Parent → Child → Grandchild chain). When the
	// parent has no derived rules registered (top-level chat session),
	// parentRules is nil and DeriveSubagentRuleSet treats it as empty.
	//
	// Subagent rules source: the spawned agent's profile rules. Today
	// no agent profile carries a per-profile permission.RuleSet
	// (file-based agents may set a frontmatter `permissions` block in
	// the future; DB-backed agents have no schema column for this yet).
	// When subagentRules is nil the derivation is parent-only-forwarding,
	// which is the H1 protection the ticket requires. As soon as agent
	// profile rules are wired, the same derivation pass picks them up.
	//
	// Resolve(): parent rules MUST already be in canonical absolute form
	// (W4 contract — RuleSet.Resolve is called by whoever populates
	// derivedRules). The subagent's own rules are resolved against the
	// child session's working_dir inside DeriveSubagentRuleSet, so
	// `./` patterns in a future agent-profile RuleSet anchor at the
	// child's effective working_dir rather than the parent's.
	r.registerSubagentDerivedRules(childID, run.ParentSessionID, agent)

	// Persist the prompt as a user message on the child session.
	// generateResponse's context assembly loads the provider message
	// list via ListMessages (assembleTurnContext → chat_generate.go);
	// the userContent parameter is only used for tool selection,
	// filters, and auto-title. Without this row the provider sees an
	// empty conversation and ignores run.Prompt. Mirrors the pattern
	// in chat.go:234 (HandleMessage) and delegation.go:109.
	//
	// CW-20260519-0075 (audit §P6): on a retry/resume attempt the child
	// session already carries the prior turn's messages. Posting the
	// original Prompt verbatim would replay an unrelated turn the LLM
	// just saw — confusing and wasteful. Instead append a short
	// continuation directive that tells the model to keep going from
	// where it left off (the prior turn's assistant text is still in
	// the history, so context is intact). This is the "checkpoint and
	// resume" continuation prompt.
	userPrompt := run.Prompt
	if isResume {
		userPrompt = resumeContinuationPrompt(run)
	}
	userMsg := &store.Message{
		ID:        uuid.New().String(),
		SessionID: childID,
		Role:      "user",
		Content:   userPrompt,
	}
	if err := r.store.CreateMessage(ctx, userMsg); err != nil {
		return nil, fmt.Errorf("create user message: %w", err)
	}

	// Phase 4 task 04 (docs/engineering/architecture/04-harness.md,
	// "Run-another-agent surfaces, unified"): build the shared,
	// surface-agnostic AgentRunRequest describing this dispatch.
	// invokeChat's own dispatcher.Request construction is unchanged
	// (it has its own test-override surface, subagent_runner_test.go /
	// _lineage_test.go) — runReq exists so this Run call's outcome can
	// later be reported through the same shared shape delegation and
	// durable wake use, without touching invokeChat's contract.
	runReq := dispatcher.AgentRunRequest{
		CallerType:      dispatcher.CallerSubagent,
		Completion:      dispatcher.CompletionAsyncCapture,
		TargetSessionID: childID,
		Prompt:          userPrompt,
	}

	// Drive one assistant turn. invokeChat closes the channel via its
	// defer (or the fake's equivalent), so drainCapture exits naturally.
	assistantMsgID := uuid.New().String()
	captureCh := make(chan chat.StreamEvent, 64)
	go r.invokeChat(ctx, childID, assistantMsgID, userPrompt, captureCh)

	summary, envelope, counts, runErr := drainCapture(captureCh)
	if runErr != nil {
		// Phase 4 task 04: derive (never drive) the shared
		// AgentRunResult from drainCapture's already-computed tuple —
		// LogOutcome is purely additive reporting; detectFabrication /
		// zeroOutputRun / partialResult below are completely untouched
		// and remain the sole source of truth for run.Status via
		// subagent.execute's classifyRunOutcome.
		outcome := dispatcher.AgentRunResult{
			CallerType:         runReq.CallerType,
			Completion:         runReq.Completion,
			TargetSessionID:    childID,
			Content:            summary,
			Envelope:           envelope,
			ToolCalls:          counts.calls,
			ToolResultsSuccess: counts.resultsSuccess,
			ToolResultsError:   counts.resultsError,
			Status:             dispatcher.RunStatusFailed,
			Err:                runErr,
		}
		if errors.Is(runErr, subagent.ErrStalled) {
			outcome.Status = dispatcher.RunStatusStalled
		}
		dispatcher.LogOutcome(outcome)

		// CW-20260519-0071 (audit §P2): partial-result capture. A
		// subagent guillotined mid-productive-work (deadline cancels
		// the in-flight provider stream → drainCapture returns
		// errStreamFailure) has often done many real tool iterations
		// — files written, etc. Returning (nil, runErr) here discarded
		// the accumulated summary/envelope/counts, so the run row had
		// status=failed and result_json at its insert-time default:
		// orphaned side-effects with no record of what got done.
		//
		// Return the partial result ALONGSIDE the error. The error is
		// unchanged — subagent.execute still stamps StatusFailed — but
		// it can now also persist result_json from this partial trace.
		// partialResult returns nil when nothing was accumulated, which
		// preserves the prior "leave result_json at default" behavior
		// for genuinely empty failures.
		if partial := partialResult(summary, envelope, counts); partial != nil {
			slog.Info("subagent: capturing partial result on error path",
				"run_id", run.ID,
				"child_session_id", childID,
				"role", run.Role,
				"tool_calls", counts.calls,
				"tool_results_success", counts.resultsSuccess,
				"tool_results_error", counts.resultsError,
				"assistant_text_chars", len(summary),
			)
			return partial, runErr
		}
		return nil, runErr
	}

	// CW-20260512-0095: fabrication-suspected detection. When the child
	// invoked at least one tool, no tool_result came back successful, and
	// the assistant still produced non-empty text, fail the run rather
	// than hand the (likely fabricated) text back to the parent. The
	// universal Refusal rules (CW-20260512-0100) teach the model not to
	// fabricate; this is the runtime backstop for when it does anyway.
	// The error message captures the counts so the operator inspecting
	// subagent_runs.error sees the evidence shape.
	if fabErr := detectFabrication(summary, counts); fabErr != nil {
		slog.Warn("subagent: fabrication suspected; failing run",
			"run_id", run.ID,
			"child_session_id", childID,
			"role", run.Role,
			"tool_calls", counts.calls,
			"tool_results_error", counts.resultsError,
			"tool_results_success", counts.resultsSuccess,
			"assistant_text_chars", len(summary),
		)
		// Phase 4 task 04: normalized reporting only — fabErr (and thus
		// subagent_runs.status) is unchanged by this call.
		dispatcher.LogOutcome(dispatcher.AgentRunResult{
			CallerType:         runReq.CallerType,
			Completion:         runReq.Completion,
			TargetSessionID:    childID,
			Content:            summary,
			Envelope:           envelope,
			ToolCalls:          counts.calls,
			ToolResultsSuccess: counts.resultsSuccess,
			ToolResultsError:   counts.resultsError,
			Status:             dispatcher.RunStatusFabricationSuspected,
			Err:                fabErr,
		})
		// CW-20260519-0071: capture the partial trace here too. The
		// run is still failed (fabErr unchanged), but persisting the
		// suspect text + tool counts in result_json lets an operator
		// inspecting subagent_runs see exactly what the child produced
		// and which tools it attempted before the detector tripped.
		if partial := partialResult(summary, envelope, counts); partial != nil {
			return partial, fabErr
		}
		return nil, fabErr
	}

	// CW-20260519-0067 (audit §P5): output-presence gate. drainCapture
	// returned a nil error, but "no error event" is NOT the same as "the
	// child produced a usable result". The pathological case — a planner
	// subagent that hangs the full budget, emits no deltas, makes zero
	// tool calls, and produces no envelope, then has its child loop exit
	// and close the capture channel without an error event — drains
	// cleanly as (summary="", envelope="{}", counts={}, err=nil). Before
	// this gate that sailed through the polite "completed without text
	// response" fallback below and subagent.execute stamped
	// StatusCompleted: a 300s no-op recorded as a success (a telemetry
	// bug — the parent agent reads status to decide whether to trust the
	// run).
	//
	// Gate it: a run that emitted no text AND no tool calls AND no
	// envelope is not a completion. Return errZeroOutput joined with
	// subagent.ErrStalled — the run went silent — so the existing
	// CW-20260519-0074 classifyRunOutcome routes it to StatusStalled. No
	// new status is invented; the 0074 taxonomy already has the word for
	// "the run went silent and never produced a deliverable".
	//
	// A genuine text-light success is explicitly NOT gated: a child that
	// made tool calls or emitted an envelope (zeroOutputRun returns false)
	// still falls through to the empty-summary fallback and StatusCompleted.
	if zeroOutputRun(summary, envelope, counts) {
		slog.Warn("subagent: zero-output run gated — no text, no tool calls, no envelope",
			"run_id", run.ID,
			"child_session_id", childID,
			"role", run.Role,
		)
		dispatcher.LogOutcome(dispatcher.AgentRunResult{
			CallerType:      runReq.CallerType,
			Completion:      runReq.Completion,
			TargetSessionID: childID,
			Status:          dispatcher.RunStatusStalled,
			Err:             errZeroOutput,
		})
		return nil, errors.Join(errZeroOutput, subagent.ErrStalled)
	}

	if summary == "" {
		// A text-light but non-empty run (made tool calls and/or emitted
		// an envelope) reaches here: zeroOutputRun returned false. The
		// child did real work but produced no closing prose — surface a
		// neutral summary and keep StatusCompleted.
		summary = fmt.Sprintf("subagent %s completed without text response", run.Role)
	}
	dispatcher.LogOutcome(dispatcher.AgentRunResult{
		CallerType:         runReq.CallerType,
		Completion:         runReq.Completion,
		TargetSessionID:    childID,
		Content:            summary,
		Envelope:           envelope,
		ToolCalls:          counts.calls,
		ToolResultsSuccess: counts.resultsSuccess,
		ToolResultsError:   counts.resultsError,
		Status:             dispatcher.RunStatusCompleted,
	})
	return &subagent.Result{Summary: summary, ResultJSON: envelope}, nil
}

// resumeContinuationPrompt produces the user-turn text appended to the
// child session at the start of a retry / resume attempt
// (CW-20260519-0075, audit §P6). The prior attempt's prompt + assistant
// text + tool transcript are already in the child's message history;
// posting the original prompt verbatim would have the LLM repeat its
// prior reasoning. A continuation directive instead asks the model to
// pick up where it left off.
//
// The directive is intentionally short and shape-stable so callers
// (tests, observers) can match against it. It branches on the prior
// terminal state recorded in AttemptsJSON so an over_budget resume and
// a failed/stalled retry get slightly different copy:
//   - over_budget → "continue the prior work; the wall clock cut you off"
//   - stalled     → "the provider stream went silent; try again"
//   - failed      → "the prior attempt errored; recover and continue"
//   - default     → generic continuation
//
// The original prompt is included verbatim as a reminder so the model
// can re-anchor if its working state has been compacted out of the
// usable context window.
func resumeContinuationPrompt(run *subagent.Run) string {
	last := ""
	if run.AttemptsJSON != "" && run.AttemptsJSON != "[]" {
		var attempts []struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal([]byte(run.AttemptsJSON), &attempts); err == nil && len(attempts) > 0 {
			last = attempts[len(attempts)-1].Status
		}
	}
	prefix := "[continuation] The prior attempt did not finish; resume from where you left off."
	switch last {
	case subagent.StatusOverBudget:
		prefix = "[continuation] Your prior attempt hit the wall-clock backstop while still making progress. Pick up the work from where you left off and aim to finish in this turn."
	case subagent.StatusStalled:
		prefix = "[continuation] Your prior attempt stalled (the provider stream went silent). Recover and continue the original task."
	case subagent.StatusFailed:
		prefix = "[continuation] Your prior attempt failed mid-way. Recover from the error and continue the original task."
	}
	return fmt.Sprintf("%s\n\nOriginal request was: %s", prefix, run.Prompt)
}

// truncatePrompt shortens s to at most n bytes, appending an ellipsis
// if trimmed. Named to avoid collision with the package-level truncate
// import alias from internal/truncate used elsewhere in this package.
func truncatePrompt(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// registerSubagentDerivedRules computes the spawned child session's
// effective permission RuleSet via DeriveSubagentRuleSet and stores it
// against the child session id so renderers (W2 SlotPermissions) and
// the runtime gate can read the forwarded denies (CW-20260512-0119,
// SP-20260512-0010 W3).
//
// Inputs:
//
//   - childID: the freshly created child session id.
//   - parentSessionID: the parent session this subagent was spawned from.
//     Used to look up any RuleSet previously derived for the parent (a
//     subagent spawning a grandchild composes naturally through this).
//   - agent: the spawned agent's profile. Carries the (currently
//     unwired) per-profile RuleSet — when agent profiles eventually grow
//     a permission.RuleSet column, this is the point at which it gets
//     read.
//
// nil-safe: when pathGrants is nil, the call is a no-op; the runner's
// downstream consumers (renderer, gate) will read empty.
//
// When neither the parent nor the subagent contribute any rules the
// derived RuleSet is empty. We still register it so the renderer can
// distinguish "this session has no rules registered" from "this session
// has an empty derived set" — both render the same content today (no
// "## Path access" rule section) but a future debug surface may want to
// tell them apart.
func (r *ChatRunner) registerSubagentDerivedRules(childID, parentSessionID string, agent *store.AgentProfile) {
	if r.pathGrants == nil || childID == "" {
		return
	}

	// Parent rules: the previously-derived effective set for the parent
	// session. nil when the parent is a top-level chat session that
	// hasn't had a RuleSet registered. The derivation propagates naturally
	// across Parent → Child → Grandchild because the parent's
	// LookupDerivedRules return value was itself the output of a previous
	// DeriveSubagentRuleSet call.
	parentRules := r.pathGrants.LookupDerivedRules(parentSessionID)

	// Subagent rules: the profile-level RuleSet for the spawned agent.
	// Not wired today — agent profiles don't carry a permission.RuleSet
	// field. When that lands (file-based agents may grow a frontmatter
	// `permissions:` block; DB-backed agents would need a schema
	// column), populate subagentRules from the agent definition here.
	//
	// For now this leaves subagentRules nil — the derivation reduces to
	// "forward all parent denies into the child", which is the H1
	// protection the ticket calls out as the load-bearing requirement.
	var subagentRules *permission.RuleSet
	_ = agent // reserved for future profile-rules wiring

	// Child working_dir for resolving any `./` patterns in the subagent's
	// future profile rules. Read from PathGrants.BestSessionDir which
	// walks the lineage chain when the child's own bucket is empty
	// (CW-20260504-0003) — gives us the most-specific dir the user has
	// signalled intent toward. May be empty when no path mentions have
	// been registered; that's fine because subagentRules is nil today
	// (Resolve fast-paths an empty subagent ruleset).
	childWorkingDir := r.pathGrants.BestSessionDir(childID)

	derived, err := permission.DeriveSubagentRuleSet(permission.DerivationInput{
		Parent:             parentRules,
		Subagent:           subagentRules,
		SubagentWorkingDir: childWorkingDir,
	})
	if err != nil {
		// Resolve failures (workspace-relative pattern escape, missing
		// working_dir for a `./` rule) surface as warnings rather than
		// failing the spawn — the child runs without the forwarded
		// denies and the dev_* gate (CW-20260512-0095) still backstops
		// fabrication detection. Failing the spawn here would couple
		// permission-rule misconfiguration to subagent dispatch
		// availability, which is the wrong blast radius.
		slog.Warn("subagent: derive permission ruleset failed; child runs without forwarded denies",
			"err", err,
			"child_session_id", childID,
			"parent_session_id", parentSessionID,
		)
		return
	}
	if derived == nil {
		return
	}

	r.pathGrants.RegisterDerivedRules(childID, derived)
}
