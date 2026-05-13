package service

import (
	"context"
	"database/sql"
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
	agent, err := agents.GetAgentBySlug(slug)
	if err == nil {
		return agent, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w %q: %v", errRoleResolveFailed, slug, err)
	}
	// Unknown slug. Try the fallback before surfacing failure.
	fallback, fbErr := agents.GetAgentBySlug(fallbackRoleSlug)
	if fbErr != nil {
		// Fallback itself missing — the deployment is misconfigured;
		// surface the ORIGINAL slug so logs point at the user's request.
		return nil, fmt.Errorf("%w %q: %v (fallback %q also missing: %v)",
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

// errRoleResolveFailed is the sentinel for when GetAgentBySlug fails.
// Wrapped error preserves the slug + the underlying error.
var errRoleResolveFailed = errors.New("subagent runner: resolve role")

// agentSlugResolver is the narrow surface ChatRunner needs to look up
// an agent profile by slug. Satisfied by any AgentReader in production;
// lets test stubs implement only this method.
type agentSlugResolver interface {
	GetAgentBySlug(slug string) (*store.AgentProfile, error)
}

// sessionStoreForRunner is the narrow surface of *store.Store the
// runner needs. Lets tests inject without spinning up sqlite.
type sessionStoreForRunner interface {
	CreateSession(*store.Session) error
	GetSession(id string) (*store.Session, error)
	EnsureSessionAgent(sessionID, agentID, mode string, isPrimary bool) error
	CreateMessage(*store.Message) error
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
	parent, err := r.store.GetSession(run.ParentSessionID)
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
	if err := r.store.CreateSession(&store.Session{
		ID:          childID,
		WorkspaceID: parent.WorkspaceID,
		Provider:    provider,
		Model:       agent.DefaultModel,
		Title:       fmt.Sprintf("subagent: %s — %s", run.Role, truncatePrompt(run.Prompt, 60)),
	}); err != nil {
		return "", fmt.Errorf("create child session: %w", err)
	}
	// Bind the child session to the resolved agent so generateResponse's
	// ResolveForSession lookup finds it. Without this, chat_generate.go:88
	// returns "Failed to resolve agent".
	if err := r.store.EnsureSessionAgent(childID, agent.ID, "default", true); err != nil {
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
func (r *ChatRunner) Run(ctx context.Context, run *subagent.Run) (*subagent.Result, error) {
	agent, err := r.resolveRole(run.Role)
	if err != nil {
		return nil, err
	}
	childID, err := r.createChildSession(ctx, run, agent)
	if err != nil {
		return nil, err
	}
	if err := r.persistChild(ctx, run.ID, childID); err != nil {
		return nil, err
	}
	run.ChildSessionID = childID

	// Path-grant lineage: stamp (worker → parent) so the worker session's
	// dev_* lookups can fall through to grants the user explicitly issued
	// in the parent chat thread (e.g. the ~/Projects-apps/nanite mention
	// that prompted task_execute). Profile permissions stay
	// isolated; this only widens the session-scoped explicit-mention
	// grant store, which is naturally scoped to the conversation thread.
	// Cleared via defer so the entry's lifetime is exactly the worker's.
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
	userMsg := &store.Message{
		ID:        uuid.New().String(),
		SessionID: childID,
		Role:      "user",
		Content:   run.Prompt,
	}
	if err := r.store.CreateMessage(userMsg); err != nil {
		return nil, fmt.Errorf("create user message: %w", err)
	}

	// Drive one assistant turn. invokeChat closes the channel via its
	// defer (or the fake's equivalent), so drainCapture exits naturally.
	assistantMsgID := uuid.New().String()
	captureCh := make(chan chat.StreamEvent, 64)
	go r.invokeChat(ctx, childID, assistantMsgID, run.Prompt, captureCh)

	summary, envelope, counts, runErr := drainCapture(captureCh)
	if runErr != nil {
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
		return nil, fabErr
	}

	if summary == "" {
		summary = fmt.Sprintf("subagent %s completed without text response", run.Role)
	}
	return &subagent.Result{Summary: summary, ResultJSON: envelope}, nil
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
