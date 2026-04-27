package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/classify"
)

// ExecuteTaskArgs carries the input to the executeTask dispatch primitive.
type ExecuteTaskArgs struct {
	// SessionID is the parent (Chat) session that initiated dispatch.
	SessionID string
	// ParentAgentID is the Chat agent's ID — recorded as the "from" on
	// the reply message the spawned role posts back.
	ParentAgentID string
	// Message is the user-shaped prompt to hand to the spawned role.
	// The dispatch primitive does NOT reshape this — pre-dispatch
	// rewriting is the caller's concern (harness prompt instructions).
	Message string
	// Provider is an optional provider override for the spawned role's
	// session. Empty string means use the role profile's default.
	Provider string
	// TimeoutSeconds caps the spawned role's wall time. Zero falls back
	// to the subagent service's default.
	TimeoutSeconds int
	// RoleOverride forces a specific role assignment regardless of the
	// classifier output. Test seam; production callers leave it
	// RoleInvalid to use AssignRole.
	RoleOverride Role
}

// SpawnRequest mirrors the shape subagent.Service.Spawn accepts. Defined
// here as a narrow value type so the dispatch package does not import
// internal/subagent — the wiring layer (internal/service) translates to
// the concrete subagent type.
type SpawnRequest struct {
	ParentSessionID string
	ParentAgentID   string
	Role            string
	Prompt          string
	Mode            string
	Provider        string
	TimeoutSeconds  int
}

// SpawnResult is the worker output captured by the dispatch primitive.
// Summary is the assistant text; EnvelopeJSON, when non-empty, is the
// full envelope payload the worker emitted via the renderer tools.
type SpawnResult struct {
	Summary      string
	EnvelopeJSON string
}

// Spawner is the narrow surface ExecuteTask uses to launch a role agent.
// The wiring layer adapts internal/subagent.Service to this interface so
// the dispatch package stays independent of the spawn machinery.
//
// Spawn must run the role to completion (or honor ctx cancellation) and
// return the captured assistant output. Async modes are out of scope for
// the executeTask happy path — the primitive needs the result to wrap
// in an envelope before returning to Chat.
type Spawner interface {
	Spawn(ctx context.Context, req SpawnRequest) (*SpawnResult, error)
}

// EnvelopeWrapper wraps a worker/planner Result as the envelope returned
// to the Chat agent. The envelope pipeline (internal/chat) is the actual
// owner of envelope construction — this is a thin adapter so the dispatch
// primitive stays narrow.
type EnvelopeWrapper interface {
	// Wrap turns a SpawnResult into a chat.Envelope ready for relay.
	// Implementations may parse the existing fenced-block envelope JSON
	// the worker emitted, or synthesize a default report-card envelope
	// when the worker only emitted prose.
	Wrap(role Role, result *SpawnResult) (chat.Envelope, error)
}

// ErrNoSpawner is returned by ExecuteTask when no Spawner is configured.
// Indicates a wiring bug — production callers always wire one.
var ErrNoSpawner = errors.New("dispatch: no spawner configured")

// ErrNoWrapper is returned by ExecuteTask when no EnvelopeWrapper is
// configured. Indicates a wiring bug.
var ErrNoWrapper = errors.New("dispatch: no envelope wrapper configured")

// ExecuteTask is the dispatch primitive. The Chat agent invokes it (via
// the nanite_execute_task tool) when its harness prompt determines the
// request needs to be handed to a Worker or Planner. The primitive:
//
//  1. Classifies the message into ScopeTier + ExecutionPattern.
//  2. Maps the classification to a role + agent slug via AssignRole.
//  3. Spawns the role agent with the resolved surface (the spawned
//     profile owns its own permissions — Chat's surface does not leak).
//  4. Captures the role's output.
//  5. Wraps the output in an envelope.
//  6. Returns the envelope. Raw worker output never leaves this function
//     in a form the Chat agent can read — the envelope is the only
//     conduit.
//
// On any failure the primitive returns an error; the caller is
// responsible for surfacing the failure to the user (typically via an
// error-report envelope).
func ExecuteTask(ctx context.Context, spawner Spawner, wrapper EnvelopeWrapper, args ExecuteTaskArgs) (chat.Envelope, error) {
	if spawner == nil {
		return chat.Envelope{}, ErrNoSpawner
	}
	if wrapper == nil {
		return chat.Envelope{}, ErrNoWrapper
	}
	if strings.TrimSpace(args.Message) == "" {
		return chat.Envelope{}, errors.New("dispatch: message is required")
	}
	if args.SessionID == "" {
		return chat.Envelope{}, errors.New("dispatch: session_id is required")
	}

	// 1. Classify.
	tier, pattern := classify.Classify(classify.IntentSignals{
		Message:         args.Message,
		MessageTokenEst: estimateTokens(args.Message),
	})

	// 2. Assign role.
	assignment := AssignRole(tier, pattern)
	if args.RoleOverride.IsValid() {
		// Test seam — preserves the spawned slug from AssignRole so the
		// override can change the role flag without forcing the caller to
		// also pick a slug.
		assignment.Role = args.RoleOverride
	}

	// 3. Spawn.
	mode := assignment.Mode
	// ExecuteTask must capture the result, so async dispatch is forced
	// to sync at the seam. The classification-driven mode is preserved
	// in the assignment for telemetry / future async-aware callers.
	if mode == "async" {
		mode = "sync"
	}

	result, err := spawner.Spawn(ctx, SpawnRequest{
		ParentSessionID: args.SessionID,
		ParentAgentID:   args.ParentAgentID,
		Role:            assignment.AgentSlug,
		Prompt:          args.Message,
		Mode:            mode,
		Provider:        args.Provider,
		TimeoutSeconds:  args.TimeoutSeconds,
	})
	if err != nil {
		return chat.Envelope{}, fmt.Errorf("dispatch: spawn %s: %w", assignment.Role.String(), err)
	}
	if result == nil {
		return chat.Envelope{}, fmt.Errorf("dispatch: spawn %s returned nil result", assignment.Role.String())
	}

	// 4 + 5. Wrap.
	env, err := wrapper.Wrap(assignment.Role, result)
	if err != nil {
		return chat.Envelope{}, fmt.Errorf("dispatch: wrap envelope: %w", err)
	}

	return env, nil
}

// estimateTokens is the same coarse byte-to-token heuristic
// internal/context uses. Inlined here to avoid pulling internal/context
// into dispatch for one helper.
func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	n := len(s) / 4
	if n == 0 {
		return 1 // floor-of-1 so classifyTier's est>0 branches fire
	}
	return n
}

// DefaultEnvelopeWrapper is a minimal EnvelopeWrapper that:
//   - Parses the worker's emitted envelope JSON when present.
//   - Falls back to a synthesized report-card envelope when the worker
//     only emitted prose.
//
// Production wiring may swap in a richer wrapper that integrates with
// the full envelope pipeline (Stage 1 detection, Stage 3 TLDR). For B3
// the default keeps the seam end-to-end testable without pulling in the
// detection layer.
type DefaultEnvelopeWrapper struct{}

// Wrap implements EnvelopeWrapper.
func (DefaultEnvelopeWrapper) Wrap(role Role, result *SpawnResult) (chat.Envelope, error) {
	if result == nil {
		return chat.Envelope{}, errors.New("dispatch: nil result")
	}

	// If the worker emitted a structured envelope, prefer it verbatim.
	if env, ok := tryUnmarshalEnvelope(result.EnvelopeJSON); ok {
		return env, nil
	}

	// Otherwise synthesize a report-card envelope around the summary.
	title := fmt.Sprintf("%s result", titleCase(role.String()))
	return chat.Envelope{
		Kind:    "envelope",
		Version: 1,
		Type:    "report-card",
		Title:   title,
		Data: map[string]any{
			"role":    role.String(),
			"summary": result.Summary,
		},
	}, nil
}

// tryUnmarshalEnvelope attempts to parse raw as a chat.Envelope. Returns
// (env, true) on a clean parse with non-empty Type; otherwise (zero,
// false). Worker envelope JSON in subagent.Result.ResultJSON sometimes
// arrives as `{}` (no envelope emitted) — that round-trips cleanly to
// an empty Envelope which is not what callers want, so we treat
// missing-Type as "not really an envelope."
func tryUnmarshalEnvelope(raw string) (chat.Envelope, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" {
		return chat.Envelope{}, false
	}
	var env chat.Envelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return chat.Envelope{}, false
	}
	if env.Type == "" {
		return chat.Envelope{}, false
	}
	if env.Kind == "" {
		env.Kind = "envelope"
	}
	if env.Version == 0 {
		env.Version = 1
	}
	return env, true
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
