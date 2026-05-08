package agent

import (
	"context"
	"errors"
	"time"
)

// Mode is the lifecycle policy for an agent process spawned via Boot.
//
// Five flat modes; callers pick exactly one. Setup is identical across modes;
// only the lifecycle policy differs (when to fire the first turn, when to
// stop, whether to attach a supervisor, whether to enforce sandbox gates).
type Mode int

const (
	// ModeLongLived stays alive across turns. Default for chat sessions; the
	// PTY runtime is selected when the adapter advertises Caps.PTY=true.
	// Caller drives turns explicitly via Session.SendInput.
	ModeLongLived Mode = iota

	// ModeOneShot fires a single turn synchronously and stops on completion.
	// Used by short legacy paths and adapters without a stable long-lived
	// stdin protocol.
	ModeOneShot

	// ModeResume boots from a saved checkpoint, threading the prior
	// provider session-id through the runtime. Requires
	// Options.ResumeFromCheckpoint.
	ModeResume

	// ModeSubagent is a nested boot under a parent session. Path-grant
	// lineage is registered automatically; child stops when parent stops.
	// Requires Options.ParentSessionID.
	ModeSubagent

	// ModeBackground is fire-and-forget. The full gate stack (sandbox +
	// path grants + permission engine) applies by default; set
	// Options.WideOpen=true at audited callsites that need the legacy
	// privileged primitive (closes G-BG-PRIVILEGED structurally).
	ModeBackground
)

// String returns the canonical lower-case identifier persisted in the
// store.Session.Mode column.
func (m Mode) String() string {
	switch m {
	case ModeLongLived:
		return "long_lived"
	case ModeOneShot:
		return "one_shot"
	case ModeResume:
		return "resume"
	case ModeSubagent:
		return "subagent"
	case ModeBackground:
		return "background"
	default:
		return "unknown"
	}
}

// Options describes the caller's intent. All fields are optional unless
// flagged in Validate; sensible defaults are computed from the agent
// profile and Dependencies.
type Options struct {
	// Mode selects the lifecycle policy. Zero value is ModeLongLived.
	Mode Mode

	// AgentProfile is the agent identifier resolved against
	// Dependencies.Agents. Empty falls back to the default profile.
	AgentProfile string

	// SessionID is the chat-harness-supplied identifier. When empty
	// (most callers other than chat), Boot generates one.
	SessionID string

	// RunID disambiguates retries within a session. Used in the boot dir
	// name for forensic discoverability.
	RunID string

	// Workdir is the project directory the agent should reach via
	// per-provider mechanisms (claude --add-dir, opencode --dir, etc.).
	// Distinct from the boot dir and workspace dir.
	Workdir string

	// Role is the role identifier (orchestrator / reviewer / planner /
	// executor / null) feeding system-prompt assembly.
	Role string

	// ParentSessionID is required for ModeSubagent.
	ParentSessionID string

	// ResumeFromCheckpoint is required for ModeResume; the row id of a
	// stored session checkpoint.
	ResumeFromCheckpoint string

	// OneShotPrompt is the kickoff payload for ModeOneShot. When empty,
	// composeKickoff supplies a role-aware default.
	OneShotPrompt string

	// WideOpen elevates ModeBackground to the legacy privileged primitive
	// (no sandbox, no supervisor, no permission engine). Audited callsites
	// only; default behavior is full-gate enforcement.
	WideOpen bool

	// Env merges into the composed env the child receives. Profile-level
	// env wins over Options.Env where keys collide.
	Env map[string]string

	// SessionMeta is opaque metadata persisted on the session row for
	// downstream introspection (FE drawer, broker routing, etc.).
	SessionMeta map[string]any
}

// Validate enforces mode-specific invariants.
func (o Options) Validate() error {
	switch o.Mode {
	case ModeSubagent:
		if o.ParentSessionID == "" {
			return errors.New("agent.Boot: ModeSubagent requires Options.ParentSessionID")
		}
	case ModeResume:
		if o.ResumeFromCheckpoint == "" {
			return errors.New("agent.Boot: ModeResume requires Options.ResumeFromCheckpoint")
		}
	}
	return nil
}

// Session is the handle Boot returns to the caller. Lifecycle methods are
// thin wrappers around Dependencies.SessionsManager bound to this session's
// id; no caller should reach into the runtime lib directly.
type Session struct {
	ID           string
	Mode         Mode
	Provider     string
	BootDir      string
	WorkspaceDir string

	// Internal references for lifecycle methods; populated by Boot.
	deps     *Dependencies
	startedAt time.Time
}

// Boot resolves the agent profile, materializes the workspace and ephemeral
// boot dir, composes env + system prompt, selects a runtime (PTY for chat
// sessions with PTY-capable adapters; subprocess-per-turn elsewhere), wires
// supervisor + sandbox gates per Mode, persists the runtime row, and starts
// the runtime via Dependencies.SessionsManager.
//
// Mode-specific dispatch is documented per-Mode constant. The chat harness
// owns turn orchestration; Boot only owns process lifecycle.
//
// The body is implemented across Phase 3 (primitives) and Phase 4 (call-site
// migration). Phase 3a stubs Boot pending the SessionsManager composition
// root that Phase 4 wires.
func Boot(ctx context.Context, deps *Dependencies, opts Options) (*Session, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	if deps == nil {
		return nil, errors.New("agent.Boot: Dependencies is required")
	}
	return nil, errors.New("agent.Boot: not yet implemented (phase 3a — primitives only; Boot body lands in phase 3b)")
}
