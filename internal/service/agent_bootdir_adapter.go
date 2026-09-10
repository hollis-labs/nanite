package service

import (
	"context"
	"errors"
	"fmt"
	"sync"

	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
)

// agentBootDirAdapter satisfies recovery.BootDirOps by re-running the
// per-provider sandbox-dir population logic that agent.Boot uses on
// initial setup. The adapter keeps an in-memory registry of
// sessionID → (bootDir, Options) populated by Track at boot time so
// Repopulate / RegenerateCLAUDEMD can rebuild the same SetupParams the
// original Boot composed without re-rolling a fresh $TMPDIR path.
//
// Lifecycle:
//
//   - Track is called by chat-side callers right after a successful
//     agent.Boot — both the initial boot and broker-dispatched
//     replacements (adoptReplacementSession). Track overwrites any
//     prior entry so a relaunched session's new bootDir / Options
//     replace the failed session's stale entry without an explicit
//     Untrack.
//   - Untrack is called when the chat session is archived (CloseAgentSession)
//     so the registry doesn't leak past the session's natural end.
//   - Repopulate / RegenerateCLAUDEMD are called by the recovery broker
//     during remediation. They look up the registered entry, recompute
//     the SetupParams via runtimeagent.ResolveBootdirParams, and
//     dispatch to the layout's Populate / RegenerateSystemPromptSlot.
//
// Concurrency: sync.Map keeps Track + lookups race-free. The adapter
// itself is stateless beyond the registry — Repopulate / RegenerateCLAUDEMD
// don't mutate the entry they read.
type agentBootDirAdapter struct {
	deps *runtimeagent.Dependencies

	// entries keyed by sessionID. Values are bootDirEntry.
	entries sync.Map
}

// bootDirEntry captures the sandbox-dir + the original Boot Options for
// a tracked session. Options carry agent profile slug, role, mode,
// workdir — everything ResolveBootdirParams needs to rebuild the same
// SetupParams the layout originally received.
type bootDirEntry struct {
	bootDir string
	opts    runtimeagent.Options
}

// newAgentBootDirAdapter returns an adapter bound to deps. Returns an
// error when deps is nil (callers in the composition root always have a
// non-nil deps; the check guards against future wiring drift).
func newAgentBootDirAdapter(deps *runtimeagent.Dependencies) (*agentBootDirAdapter, error) {
	if deps == nil {
		return nil, errors.New("agent_bootdir_adapter: deps is required")
	}
	return &agentBootDirAdapter{deps: deps}, nil
}

// Track registers (or overwrites) the bootDir + Options entry for
// sessionID. Called from chat-side callers right after a successful
// agent.Boot so the recovery broker has the data on hand when a
// remediation fires. Empty sessionID or empty bootDir is a no-op
// (defensive — agent.Boot guarantees both non-empty on success).
func (a *agentBootDirAdapter) Track(sessionID, bootDir string, opts runtimeagent.Options) {
	if a == nil {
		return
	}
	if sessionID == "" || bootDir == "" {
		return
	}
	// Always carry the sessionID on the stored Options so a future
	// ResolveBootdirParams re-uses the same id rather than generating a
	// new one.
	opts.SessionID = sessionID
	a.entries.Store(sessionID, bootDirEntry{bootDir: bootDir, opts: opts})
}

// Untrack removes the registry entry for sessionID. Called on chat
// session archive (CloseAgentSession) so the map doesn't leak past the
// natural session end. No-op when the entry is absent.
func (a *agentBootDirAdapter) Untrack(sessionID string) {
	if a == nil || sessionID == "" {
		return
	}
	a.entries.Delete(sessionID)
}

// Repopulate satisfies recovery.BootDirOps. Rewrites the full
// per-session sandbox dir (CLAUDE.md / agent-context.md /
// envelope-schema.md / .mcp.json + provider-specific files) by
// dispatching to the per-provider Layout.Populate against the existing
// bootDir. Idempotent — Layout.Populate uses fsutil.AtomicWriteFile +
// os.MkdirAll throughout.
//
// Returns an error when sessionID is unknown (no Track entry — broker
// is asking about a session this adapter never saw) or when the
// re-resolution / write step fails.
func (a *agentBootDirAdapter) Repopulate(ctx context.Context, sessionID string) error {
	if a == nil {
		return errors.New("agent_bootdir_adapter: nil receiver")
	}
	if sessionID == "" {
		return errors.New("agent_bootdir_adapter.Repopulate: empty sessionID")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	entry, ok := a.lookup(sessionID)
	if !ok {
		return fmt.Errorf("agent_bootdir_adapter.Repopulate: no entry for session %q", sessionID)
	}

	layout, params, err := runtimeagent.ResolveBootdirParams(a.deps, entry.opts, sessionID)
	if err != nil {
		return fmt.Errorf("agent_bootdir_adapter.Repopulate: resolve params: %w", err)
	}
	if _, err := layout.Populate(entry.bootDir, params); err != nil {
		return fmt.Errorf("agent_bootdir_adapter.Repopulate: populate %s: %w", entry.bootDir, err)
	}
	return nil
}

// RegenerateCLAUDEMD satisfies recovery.BootDirOps. Rewrites only the
// per-provider system-prompt-bearing slot (CLAUDE.md for claude,
// AGENTS.md for codex, agents/<slug>.md for opencode), leaving the rest
// of the sandbox intact.
//
// Returns an error when sessionID is unknown or when the re-resolution
// / write step fails.
func (a *agentBootDirAdapter) RegenerateCLAUDEMD(ctx context.Context, sessionID string) error {
	if a == nil {
		return errors.New("agent_bootdir_adapter: nil receiver")
	}
	if sessionID == "" {
		return errors.New("agent_bootdir_adapter.RegenerateCLAUDEMD: empty sessionID")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	entry, ok := a.lookup(sessionID)
	if !ok {
		return fmt.Errorf("agent_bootdir_adapter.RegenerateCLAUDEMD: no entry for session %q", sessionID)
	}

	layout, params, err := runtimeagent.ResolveBootdirParams(a.deps, entry.opts, sessionID)
	if err != nil {
		return fmt.Errorf("agent_bootdir_adapter.RegenerateCLAUDEMD: resolve params: %w", err)
	}
	if err := layout.RegenerateSystemPromptSlot(entry.bootDir, params); err != nil {
		return fmt.Errorf("agent_bootdir_adapter.RegenerateCLAUDEMD: regenerate %s: %w", entry.bootDir, err)
	}
	return nil
}

// lookup retrieves a tracked bootDirEntry by sessionID. Returns ok=false
// when the entry is absent or carries an unexpected value type — the
// callers convert that into a clear "no entry for session" error rather
// than panicking on a bad type assertion.
func (a *agentBootDirAdapter) lookup(sessionID string) (bootDirEntry, bool) {
	v, ok := a.entries.Load(sessionID)
	if !ok {
		return bootDirEntry{}, false
	}
	entry, ok := v.(bootDirEntry)
	if !ok {
		return bootDirEntry{}, false
	}
	return entry, true
}
