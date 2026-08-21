package agent

// agent_acp.go — TASKS/agent-host-acp/11-nanite-per-agent-protocol-
// transport-config.md: bootACP is agent.Boot's ACP-protocol branch,
// dispatched by factory.go's useACPProtocol before the native bootdir/
// wrapper.Wrapper path runs at all. See acp_session.go's package doc for
// why this drives acp.Client directly instead of routing through
// wrapper.Wrapper.Run.

import (
	"context"
	"fmt"
	"time"

	"github.com/hollis-labs/go-agent-wrapper/acp"
	"github.com/hollis-labs/nanite/internal/store"
)

// bootACP is agent.Boot's ACP-protocol counterpart to the native
// wrapper.Wrapper path below it in agent.go. Mirrors that path's shared
// bookkeeping (RuntimeRow persistence, PathGrants lineage cleanup on
// failure, live-session tracking, AutoFireFirstTurn) but deliberately
// skips two native-only pieces:
//
//   - No boot-dir planting (composeBootdirParams/layout.Setup) — neither
//     task 09's opencodeacp nor task 10's copilotacp native adapter has
//     been shown to consume a planted CLAUDE.md/config.toml the way the
//     native CLI runtimes do; Session.BootDir is left "" (Stop's
//     os.RemoveAll(bootDir) already no-ops on empty).
//   - No provider-session-id capture via a callback — acp.LaunchParams has
//     no OnSessionID-equivalent hook (the interface only exposes
//     SessionIDPreset, a caller->implementation direction), and acp.Client
//     doesn't expose the assigned session id back to the caller either.
//     ModeResume's checkpoint-driven SessionIDPreset flow (below) still
//     works; a session BOOTED under ACP does not yet get its own
//     provider-session-id persisted for a LATER resume. Flagged as a real,
//     confirmed gap in the current acp.Client interface shape (not
//     something Nanite can work around without reaching past the
//     interface into a concrete Client type) — a natural follow-up for
//     whichever task next touches libs/go-agent-wrapper's acp package.
func bootACP(ctx context.Context, deps *Dependencies, opts Options, profile *store.AgentProfile, providerName, sessID string, ws *Workspace, hadLineage bool) (*Session, error) {
	cleanup := func(failure error) (*Session, error) {
		if hadLineage && deps.PathGrants != nil {
			deps.PathGrants.ClearLineage(sessID)
		}
		return nil, failure
	}

	client, err := newACPClient(providerName, effectiveACPTransport(profile))
	if err != nil {
		return cleanup(fmt.Errorf("agent.Boot: %w", err))
	}

	cwd := opts.Workdir
	if cwd == "" {
		cwd = ws.Root
	}

	parentPtr := (*string)(nil)
	if opts.ParentSessionID != "" {
		parentPtr = &opts.ParentSessionID
	}
	// Skip CreateRuntimeRow on relaunch, mirroring the native path's
	// identical IsRelaunch guard immediately below it in agent.go's Boot.
	if !opts.IsRelaunch {
		if err := deps.Store.CreateRuntimeRow(&RuntimeRow{
			ID:              sessID,
			AgentProfile:    opts.AgentProfile,
			Provider:        providerName,
			Mode:            opts.Mode.String(),
			Workdir:         cwd,
			State:           "launching",
			ParentSessionID: parentPtr,
			StartedAt:       time.Now(),
			Meta:            opts.SessionMeta,
		}); err != nil {
			return cleanup(fmt.Errorf("agent.Boot: persist runtime row: %w", err))
		}
	}

	// SessionIDPreset resolution mirrors agent.go Boot's identical native-
	// path block (ResumeProviderSessionID wins outright; otherwise a
	// ModeResume checkpoint's ProviderSessionID, when one exists) —
	// duplicated rather than extracted into a shared helper to keep this
	// task's diff to the already-reviewed native path minimal.
	var sessionIDPreset string
	switch {
	case opts.ResumeProviderSessionID != "":
		sessionIDPreset = opts.ResumeProviderSessionID
	case opts.Mode == ModeResume && opts.ResumeFromCheckpoint != "":
		cp, err := deps.Store.GetCheckpoint(opts.ResumeFromCheckpoint)
		if err != nil {
			_ = deps.Store.MarkRuntimeFailed(sessID, err.Error())
			return cleanup(fmt.Errorf("agent.Boot: load checkpoint: %w", err))
		}
		if cp != nil {
			sessionIDPreset = cp.ProviderSessionID
		}
	}

	systemPrompt := ResolveSystemPrompt(opts.Role, profile, opts.Mode, opts.BootPromptOverride, opts.DynamicContext)
	envMap := composeEnv(profile, opts)

	sink := &acpSession{client: client}
	if deps.EventFanout != nil {
		sink.fanout = deps.EventFanout(sessID)
	}
	if deps.TypedEventCallback != nil {
		sink.typedCB = deps.TypedEventCallback(sessID)
	}

	// acp.Client.Launch performs the full initialize/session/new (or
	// session/load) handshake synchronously and returns only once the
	// session is ready to accept a Prompt (see acp.Client's own doc
	// comment) — unlike the native path's wrapper.Wrapper.Run, there is no
	// separate async "wait for KindSessionReady" step needed here; Launch
	// returning nil IS the readiness signal.
	if err := client.Launch(ctx, acp.LaunchParams{
		Cwd:             cwd,
		Env:             envMapToSlice(envMap),
		SystemPrompt:    systemPrompt,
		SessionIDPreset: sessionIDPreset,
	}); err != nil {
		_ = deps.Store.MarkRuntimeFailed(sessID, err.Error())
		return cleanup(fmt.Errorf("agent.Boot: acp launch: %w", err))
	}

	sess := &Session{
		ID:           sessID,
		Mode:         opts.Mode,
		Provider:     providerName,
		BootDir:      "",
		WorkspaceDir: ws.Root,
		deps:         deps,
		startedAt:    time.Now(),
		hadLineage:   hadLineage,
		acp:          sink,
		runDone:      make(chan struct{}),
	}

	// Mirrors the native path's own runDone-owning goroutine (agent.go's
	// Boot): drains for the life of the session, then marks the runtime
	// row done and untracks it once the ACP Client's Events channel
	// closes (Stop's explicit Close, or the underlying process exiting on
	// its own — see acp_session.go's drain doc comment).
	//
	// TASKS/agent-host-acp/11 Finding 2 fix: sess.runErr is set from
	// sink.processExitErr strictly after drain(...) returns (so
	// handleProcessExited, called synchronously from within drain's own
	// loop, has already run) and strictly before runDone closes — the
	// same write-before-close ordering agent.go's native path uses for
	// wr.Run's error, so Session.Wait's happens-before argument holds
	// identically for both backends. nil (the common case — clean exit,
	// intentional Stop, or copilotacp's Client, which never emits
	// KindProcessExited at all) leaves Wait() returning nil, exactly like
	// before this fix; a genuine unprompted crash now reaches Wait() as a
	// non-nil error internal/recovery/broker's classifier can act on,
	// instead of always looking like a clean exit.
	go func() {
		defer close(sess.runDone)
		sink.drain(context.Background())
		sess.runErr = sink.processExitErr
		deps.untrackLiveSession(sessID)
		_ = deps.Store.UpdateState(sessID, "done", 0)
	}()

	_ = deps.Store.UpdateState(sessID, "running", 0)
	deps.trackLiveSession(sessID, sess)

	// First-turn payload: mirrors agent.go Boot's own composeKickoff call
	// EXCEPT it uses composeKickoffRaw (embeds boot content directly)
	// rather than composeKickoff's "Boot @./boot.md" file-reference
	// convention — there is no planted boot.md for an ACP session to
	// resolve that reference against (see this function's own doc
	// comment on skipping boot-dir planting). ModeOneShot's
	// OneShotPrompt override still takes precedence, same as native.
	firstTurn := composeKickoffRaw(opts)
	if opts.Mode == ModeOneShot && opts.OneShotPrompt != "" {
		firstTurn = opts.OneShotPrompt
	}
	if shouldAutoFireFirstTurn(opts.Mode) {
		go func() {
			_ = sess.SendInput([]byte(firstTurn))
		}()
	}

	return sess, nil
}
