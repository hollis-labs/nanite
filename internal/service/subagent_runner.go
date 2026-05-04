package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
)

// errStreamFailure is the sentinel returned by drainCapture when the
// child chat loop emits an error event. The actual error message is
// preserved in the wrapped error.
var errStreamFailure = errors.New("subagent: child chat loop emitted error event")

// drainCapture consumes a chat.StreamEvent channel and assembles a
// summary string + envelope payload for subagent.Result. Pure logic,
// extracted from ChatRunner.Run so the parsing rules can be unit
// tested without spinning up generateResponse.
//
// Rules:
//   - "delta" events: append Content to the summary builder
//   - "plugin_envelope" events: overwrite envelope buffer with
//     Envelope field (last-wins)
//   - "error" / "structured_error" events: terminate drain, return
//     errStreamFailure wrapped with the error message
//   - "stream_end": capture evt.Envelope if non-empty (production
//     generateResponse attaches the terminal aggregated envelope JSON
//     there — chat_generate.go:837), then terminate drain successfully
//   - everything else (stream_start, status, tool_call, tool_result,
//     presence, etc.): ignored for capture purposes
//
// Returns summary, envelope (always valid JSON; "{}" when no envelope
// event was seen), and a non-nil error if the stream emitted an error
// event.
func drainCapture(ch <-chan chat.StreamEvent) (summary string, envelope string, err error) {
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
		case "error", "structured_error":
			msg := evt.Error
			if msg == "" {
				msg = "stream error event with no message"
			}
			return sb.String(), envelope, errors.Join(errStreamFailure, errors.New(msg))
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
			return sb.String(), envelope, nil
		}
	}

	// Channel closed without stream_end — treat as a clean drain.
	return sb.String(), envelope, nil
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

// invokeChat delegates to the test override or the real generateResponse.
func (r *ChatRunner) invokeChat(ctx context.Context, sessionID, msgID, prompt string, ch chan chat.StreamEvent) {
	if r.invoker != nil {
		r.invoker.generateResponse(ctx, sessionID, msgID, prompt, ch)
		return
	}
	r.chat.generateResponse(ctx, sessionID, msgID, prompt, ch)
}

// persistChild delegates to the test override or the real persistChildSessionID.
func (r *ChatRunner) persistChild(ctx context.Context, runID, childID string) error {
	if r.persistFn != nil {
		return r.persistFn(ctx, runID, childID)
	}
	return r.persistChildSessionID(ctx, runID, childID)
}

// resolveRole looks up the role slug in the agent registry. Wraps the
// underlying error with errRoleResolveFailed so callers can use
// errors.Is for classification.
func (r *ChatRunner) resolveRole(slug string) (*store.AgentProfile, error) {
	agent, err := r.agents.GetAgentBySlug(slug)
	if err != nil {
		return nil, fmt.Errorf("%w %q: %v", errRoleResolveFailed, slug, err)
	}
	return agent, nil
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
	// that prompted nanite_execute_task). Profile permissions stay
	// isolated; this only widens the session-scoped explicit-mention
	// grant store, which is naturally scoped to the conversation thread.
	// Cleared via defer so the entry's lifetime is exactly the worker's.
	r.pathGrants.RegisterLineage(childID, run.ParentSessionID)
	defer r.pathGrants.ClearLineage(childID)

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

	summary, envelope, runErr := drainCapture(captureCh)
	if runErr != nil {
		return nil, runErr
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
