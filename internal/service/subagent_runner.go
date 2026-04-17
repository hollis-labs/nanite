package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/hollis-labs/nanite/internal/chat"
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
//   - "stream_end": terminate drain successfully
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
}

// NewChatRunner constructs a runner. All deps are required in production;
// tests can leave fields unset when they don't exercise that code path.
func NewChatRunner(c *chatServiceImpl, agents agentSlugResolver, st sessionStoreForRunner, db *sql.DB) *ChatRunner {
	return &ChatRunner{chat: c, agents: agents, store: st, db: db}
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
// the parent session. Agent-session binding (session_agents row) is
// deferred to Task 8 when generateResponse is wired.
func (r *ChatRunner) createChildSession(ctx context.Context, run *subagent.Run, agent *store.AgentProfile) (string, error) {
	parent, err := r.store.GetSession(run.ParentSessionID)
	if err != nil {
		return "", fmt.Errorf("get parent session: %w", err)
	}
	childID := uuid.New().String()
	if err := r.store.CreateSession(&store.Session{
		ID:          childID,
		WorkspaceID: parent.WorkspaceID,
		Provider:    agent.DefaultProvider,
		Model:       agent.DefaultModel,
		Title:       fmt.Sprintf("subagent: %s — %s", run.Role, truncatePrompt(run.Prompt, 60)),
	}); err != nil {
		return "", fmt.Errorf("create child session: %w", err)
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

// Run is the skeleton for the runner interface. Task 8 finishes this
// function by launching generateResponse, draining via drainCapture,
// and assembling the Result. Role resolution is live so
// TestChatRunner_ResolveRoleFails passes; the chat invocation returns
// a sentinel error.
func (r *ChatRunner) Run(ctx context.Context, run *subagent.Run) (*subagent.Result, error) {
	agent, err := r.resolveRole(run.Role)
	if err != nil {
		return nil, err
	}
	childID, err := r.createChildSession(ctx, run, agent)
	if err != nil {
		return nil, err
	}
	if err := r.persistChildSessionID(ctx, run.ID, childID); err != nil {
		return nil, err
	}
	run.ChildSessionID = childID
	return nil, errors.New("subagent runner: chat invocation not yet wired (Task 8)")
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
