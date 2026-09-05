package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hollis-labs/go-agent-wrapper/activity"
	"github.com/hollis-labs/go-agent-wrapper/adapters"
	"github.com/hollis-labs/go-agent-wrapper/wrapper"
	runtimeevents "github.com/hollis-labs/go-runtime-events/runtimeevents"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/oklog/ulid/v2"
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

	// ResumeProviderSessionID, when non-empty, resumes the provider's prior
	// session (e.g. Claude `--resume <id>`) on this boot WITHOUT switching
	// lifecycle modes — so a long-lived CLI chat session can resume its real
	// provider context after a host restart while keeping the streaming
	// supervisor. Takes precedence over the ModeResume/checkpoint path.
	// CW-20260525-0001 Slice 3. Empty preserves prior behavior.
	ResumeProviderSessionID string

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

	// IsRelaunch, when true, signals that this Boot is a recovery
	// broker's replacement-session dispatch for a previously failed
	// session. The runtime row already exists in state="failed"; the
	// broker has transitioned it to state="launching" via
	// Store.MarkRuntimeRelaunching. Boot must SKIP CreateRuntimeRow
	// (it would conflict on the unique sessionID key) but otherwise
	// proceed normally — workspace + bootdir setup, manager.Start, etc.
	//
	// SessionID must be non-empty when IsRelaunch is true; the broker
	// always passes the original SessionID to preserve chat-history /
	// slot-state / path-grant lineage.
	IsRelaunch bool

	// BootPromptOverride, when non-empty, replaces the role-derived
	// system prompt that Layout.BootPrompt would otherwise compose.
	// CW-20260514-0048 (boot-profile-driven launches): the compiled
	// LaunchSpec carries a fully-rendered BootPrompt assembled from
	// the profile's slots; threading it onto the spawn via this
	// override keeps Layout.BootPrompt as the single hook so the
	// existing claude/codex/opencode paths converge on one source.
	//
	// Empty leaves the prior behavior intact — composeSystemPrompt
	// derives the prompt from profile + role + mode as it always has.
	BootPromptOverride string

	// DynamicContext carries the resolved output of this agent's
	// DB-configured cmd/http context resolvers (Phase 2 item 02,
	// TASKS/phase-2/02-port-forward-dynamic-resolver.md), keyed by slot
	// name. Populated by the caller (chat_boot_drive.go's
	// resolveAgentContextForBoot) via
	// internal/runtime/agent.ResolveContextBlocks BEFORE Boot is called
	// — resolution is launch-time, not something Boot itself performs.
	//
	// resolveBootPrompt appends each non-empty block as its own section
	// AFTER the role-derived (or BootPromptOverride-replaced) system
	// prompt, so a resolver's live-fetched data folds into the assembled
	// launch context regardless of which path produced the base prompt.
	// Nil/empty leaves the prior behavior unchanged.
	DynamicContext map[string]string

	// Provider overrides the bare adapter name resolved from
	// profile.DefaultProvider. CW-20260514-0053 (boot-profile-driven
	// launches): a compiled LaunchSpec carries spec.Provider (e.g.
	// "claude"), but the file-based default agent profile doesn't
	// declare DefaultProvider. Without this override, agent.Boot fell
	// through to bootdirLayoutFor("") and emitted "bootdir for provider
	// \"\" is not yet implemented" (c197 regression).
	//
	// Precedence: opts.Provider WHEN non-empty, else
	// profile.DefaultProvider. Bare adapter names ("claude", "codex",
	// "opencode"); CLI prefixes ("pty-claude" etc.) are normalized to
	// bare via normalizeProviderName before dispatch.
	//
	// Empty preserves legacy behavior — agent.Boot continues to rely
	// solely on profile.DefaultProvider as it did before.
	Provider string
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
	if o.IsRelaunch && o.SessionID == "" {
		return errors.New("agent.Boot: IsRelaunch requires Options.SessionID")
	}
	return nil
}

// Session is the handle Boot returns to the caller. Lifecycle methods are
// thin wrappers around the *wrapper.Wrapper Boot constructed for this
// session's id (manager.go); no caller should reach into
// go-agent-wrapper/agentkit directly. Public shape (exported fields) is
// unchanged from the pre-migration version — internal/recovery/broker and
// every other existing caller compile against it unmodified.
type Session struct {
	ID           string
	Mode         Mode
	Provider     string
	BootDir      string
	WorkspaceDir string

	deps      *Dependencies
	startedAt time.Time
	// hadLineage records whether Boot registered a PathGrants lineage for
	// this session — Stop unwinds it.
	hadLineage bool

	// wr is the wrapper.Wrapper instance driving this session's runtime.
	// SendInput/Stop (manager.go) call directly into it. Safe to use once
	// runDone's owning goroutine has observed wrapper.Wrapper.Run reach
	// runtimeevents.KindSessionReady — Boot blocks until that point (or a
	// pre-ready failure) before ever returning a *Session, so every method
	// below can assume wr is ready.
	//
	wr    *wrapper.Wrapper
	isACP bool

	// runDone closes once the background goroutine Boot started observes
	// wr.Run(runCtx) return (clean exit or error alike) — safe for any
	// number of concurrent Wait callers, mirroring the pre-migration
	// agentsessions.Manager's own sessionResult.done broadcast pattern.
	// runErr is written by that same goroutine strictly before the close
	// (Go's channel-close-as-broadcast memory-model guarantee), so any
	// reader that first observes runDone closed may then safely read
	// runErr without further synchronization.
	runDone chan struct{}
	runErr  error
	// runCancel is reserved for SessionManager's pre-ready/adoption shutdown
	// boundary. Ordinary Stop deliberately does not cancel it; see manager.go.
	runCancel context.CancelFunc
}

// expandUserHome replaces a leading "~" or "~/" in path with the
// current user's home directory and returns the result. Paths that
// don't start with "~" are returned unchanged. The "~user" form is
// NOT supported (uncommon; punt to a future ticket if it shows up) —
// such paths flow through verbatim so the caller can decide.
//
// Returns an error only when "~" is present but os.UserHomeDir fails
// (e.g. $HOME unset and /etc/passwd unreadable on darwin). Existing
// callers see the error wrapped with the original raw path so logs
// reflect what the operator authored.
func expandUserHome(path string) (string, error) {
	if path == "" || path[0] != '~' {
		return path, nil
	}
	if path != "~" && !strings.HasPrefix(path, "~/") {
		// "~user" or "~ something" — out of scope; preserve verbatim.
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, path[2:]), nil
}

// effectiveProvider centralizes the precedence rule for the bare
// adapter name agent.Boot dispatches on. CW-20260514-0053: when a
// boot-profile-driven launch threads spec.Provider through
// Options.Provider, that override wins over the agent profile's
// DefaultProvider. Empty Options.Provider preserves legacy behavior.
//
// nil profile is the file-default fallback case; the caller already
// guards against it by the time we reach the six dispatch sites that
// consume the return value.
func effectiveProvider(opts Options, profile *store.AgentProfile) string {
	if opts.Provider != "" {
		return opts.Provider
	}
	if profile != nil {
		return profile.DefaultProvider
	}
	return ""
}

// Boot resolves the agent profile, materializes the workspace and ephemeral
// boot dir, composes env + system prompt, selects a runtime (PTY for chat
// sessions with PTY-capable adapters; subprocess-per-turn elsewhere), wires
// sandbox gates per Mode, persists the runtime row, and starts the runtime
// via go-agent-wrapper. Native and ACP launches deliberately share this one
// lifecycle path: only boot-profile compilation, sandbox inputs, recovery
// persistence, and UI event translation remain Nanite-owned.
//
// Mode-specific dispatch is documented per-Mode constant. The chat harness
// owns turn orchestration; Boot only owns process lifecycle.
func Boot(ctx context.Context, deps *Dependencies, opts Options) (*Session, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	if deps == nil {
		return nil, errors.New("agent.Boot: Dependencies is required")
	}
	if deps.Manager == nil {
		return nil, errors.New("agent.Boot: Dependencies.Manager is required")
	}
	if deps.Agents == nil {
		return nil, errors.New("agent.Boot: Dependencies.Agents is required")
	}
	if deps.Store == nil {
		return nil, errors.New("agent.Boot: Dependencies.Store is required")
	}

	profile, err := deps.Agents.GetOrDefault(opts.AgentProfile)
	if err != nil {
		return nil, fmt.Errorf("agent.Boot: resolve profile: %w", err)
	}
	if profile == nil {
		return nil, errors.New("agent.Boot: profile resolution returned nil")
	}

	sessID := opts.SessionID
	if sessID == "" {
		sessID = newSessionID()
	}
	opts.SessionID = sessID

	// CW-20260514-0054: ensure the project workdir exists before any
	// subprocess is spawned. Boot profiles (and future call sites) can
	// declare a workdir that doesn't exist yet — e.g. claude-smoke.yaml
	// points at /tmp/nanite-smoke-workdir. Without this, claude exits 1
	// within ~700ms when --add-dir <missing-path> fails, the runtime
	// retries twice, and the session lands in state=failed with
	// restart_exhausted (c198). Idempotent: existing dirs are a no-op.
	// Empty Workdir keeps legacy behavior unchanged.
	//
	// Round-1 review: boot-profile YAML preserves leading "~" verbatim
	// (the compiler does not expand) — see internal/bootprofile tests
	// asserting spec.Workdir == "~/Projects-apps/nanite". MkdirAll on
	// a raw "~/..." would create a literal "./~/..." dir wherever
	// nanite is running, AND every downstream consumer (layout
	// SpawnWorkdir, runtime row, recovery adapter) would see the same
	// unexpanded path. Expand at the Boot boundary and overwrite
	// opts.Workdir so the rest of this function and the persisted row
	// observe the absolute path.
	if opts.Workdir != "" {
		expanded, err := expandUserHome(opts.Workdir)
		if err != nil {
			return nil, fmt.Errorf("agent.Boot: expand workdir %q: %w", opts.Workdir, err)
		}
		opts.Workdir = expanded
		if err := os.MkdirAll(opts.Workdir, 0o755); err != nil {
			return nil, fmt.Errorf("agent.Boot: ensure workdir %q: %w", opts.Workdir, err)
		}
	}

	ws, err := workspaceCreate(deps.WorkspacesRoot, sessID, opts)
	if err != nil {
		return nil, err
	}

	// PathGrants lineage for nested subagents. nil-safe: ModeSubagent
	// validation already enforced ParentSessionID non-empty.
	hadLineage := false
	if opts.Mode == ModeSubagent && deps.PathGrants != nil {
		deps.PathGrants.RegisterLineage(sessID, opts.ParentSessionID)
		hadLineage = true
	}

	providerName := effectiveProvider(opts, profile)
	isACP := useACPProtocol(profile)
	bootDir := ""
	spawnWorkdir := opts.Workdir
	if spawnWorkdir == "" {
		spawnWorkdir = ws.Root
	}
	envMap := composeEnv(profile, opts)

	// ACP agents consume their system prompt over session/new|load and do
	// not consume Nanite's native provider boot files. Native agents retain
	// the existing boot-profile/layout compilation unchanged.
	if !isACP {
		layout, params := composeBootdirParams(deps, opts, profile, sessID)
		bootDir, err = layout.Setup(params)
		if err != nil {
			if hadLineage && deps.PathGrants != nil {
				deps.PathGrants.ClearLineage(sessID)
			}
			return nil, fmt.Errorf("agent.Boot: bootdir setup: %w", err)
		}
		envMap = layout.AmendEnv(envMap, bootDir)
		spawnWorkdir = layout.SpawnWorkdir(bootDir, opts.Workdir)
	}

	// Anything past this point that fails must clean the boot dir to avoid
	// leaking $TMPDIR entries.
	cleanup := func(failure error) (*Session, error) {
		if hadLineage && deps.PathGrants != nil {
			deps.PathGrants.ClearLineage(sessID)
		}
		_ = os.RemoveAll(bootDir)
		return nil, failure
	}

	var selectedAdapter adapters.Adapter
	if isACP {
		factory := deps.ACPAdapterFactory
		if factory == nil {
			factory = newACPAdapter
		}
		selectedAdapter, err = factory(providerName, effectiveACPTransport(profile))
	} else {
		if deps.ProviderAdapter == nil {
			return cleanup(errors.New("agent.Boot: Dependencies.ProviderAdapter is required for native protocol"))
		}
		cli := deps.ProviderAdapter(providerName)
		if cli == nil {
			return cleanup(fmt.Errorf("agent.Boot: no adapter registered for provider %q", providerName))
		}
		selectedAdapter, err = selectNativeAdapter(providerName, opts.Mode, cli)
	}
	if err != nil {
		return cleanup(fmt.Errorf("agent.Boot: select wrapper adapter: %w", err))
	}

	parentPtr := (*string)(nil)
	if opts.ParentSessionID != "" {
		parentPtr = &opts.ParentSessionID
	}
	// Skip CreateRuntimeRow on relaunch — the broker's
	// Store.MarkRuntimeRelaunching has already transitioned the
	// existing row from failed -> launching, and a second
	// CreateRuntimeRow would conflict on the unique sessionID key.
	if !opts.IsRelaunch {
		if err := deps.Store.CreateRuntimeRow(&RuntimeRow{
			ID:              sessID,
			AgentProfile:    opts.AgentProfile,
			Provider:        providerName,
			Mode:            opts.Mode.String(),
			Workdir:         spawnWorkdir,
			State:           "launching",
			ParentSessionID: parentPtr,
			StartedAt:       time.Now(),
			Meta:            opts.SessionMeta,
		}); err != nil {
			return cleanup(fmt.Errorf("agent.Boot: persist runtime row: %w", err))
		}
	}

	var sessionIDPreset string
	switch {
	case opts.ResumeProviderSessionID != "":
		// CW-20260525-0001 Slice 3: direct provider-session resume on a
		// long-lived boot (no mode switch — keeps the streaming supervisor).
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

	sandboxProfile := buildSandboxProfile(deps.SandboxBaseProfile, opts, ws.Root, bootDir)

	onSessionID := func(id string) {
		_ = deps.Store.SetProviderSessionID(sessID, id)
	}

	// Native agents point at Nanite's planted boot file. ACP agents receive
	// equivalent boot content directly because they do not consume those
	// provider-specific files.
	firstTurn := composeKickoff(opts.Role, sessID, opts.ParentSessionID)
	if isACP {
		firstTurn = composeKickoffRaw(opts)
	}
	if opts.Mode == ModeOneShot && opts.OneShotPrompt != "" {
		firstTurn = opts.OneShotPrompt
	}

	firstTurnPayload := []byte(firstTurn)

	// CW-20260516-0007: the streaming-stdio runtime treats the child's
	// stdin strictly as NDJSON — NDJSON-frame FirstTurnPayload so
	// AutoFireFirstTurn modes (one-shot / subagent / background) deliver a
	// parseable kickoff.
	//
	// The raw-boot-prompt-on-stdin suppression this comment used to also
	// describe (points 1-2 of the pre-migration three-point note) is moot
	// post-migration: wrapper.Config has no BootPrompt/BootMode field at
	// all (confirmed non-load-bearing — this task's own folded-in
	// escalation finding 3 — claude's planted CLAUDE.md is already
	// auto-discovered via cwd, per claudeLayout.SpawnWorkdir), so there is
	// no raw-boot-prompt-on-stdin write path left to suppress.
	if !isACP && shouldUseStreamingStdio(providerName, opts.Mode) {
		if framed, ferr := streamingStdioUserFrame(firstTurn); ferr == nil {
			firstTurnPayload = framed
		}
	}

	var canonicalSink runtimeevents.Sink
	var ownedCanonicalSink io.Closer
	if deps.RuntimeEventSink != nil {
		canonicalSink = deps.RuntimeEventSink(sessID)
	}
	if canonicalSink == nil {
		fileSink, openErr := runtimeevents.OpenFileSink(filepath.Join(ws.LogDir, "runtime-events.jsonl"))
		if openErr != nil {
			_ = deps.Store.MarkRuntimeFailed(sessID, openErr.Error())
			return cleanup(fmt.Errorf("agent.Boot: open normalized runtime event journal: %w", openErr))
		}
		canonicalSink = fileSink
		ownedCanonicalSink = fileSink
	}
	sink := &runtimeEventSink{acp: isACP, canonical: canonicalSink}
	if deps.EventFanout != nil {
		sink.fanout = deps.EventFanout(sessID)
	}
	if deps.TypedEventCallback != nil {
		sink.typedCB = deps.TypedEventCallback(sessID)
	}
	readyCh := make(chan struct{})

	wr, err := wrapper.New(wrapper.Config{
		App:      "nanite",
		Adapter:  selectedAdapter,
		Activity: activity.NewBridge(sink),
		Workdir:  spawnWorkdir,
		Environment: wrapper.ChildEnvironment{
			Mode: wrapper.EnvironmentReplace,
			Set:  envMapToSlice(envMap),
		},
		BootDir:         bootDir,
		SessionID:       sessID,
		WorkspaceDir:    ws.Root,
		LogPath:         ws.LogPath,
		SandboxProfile:  sandboxProfile,
		SessionIDPreset: sessionIDPreset,
		SystemPrompt:    ResolveSystemPrompt(opts.Role, profile, opts.Mode, opts.BootPromptOverride, opts.DynamicContext),
		ACPManager:      deps.Manager.ACPManager(),
		ACPBestEffortPermissionRequestResponder: bestEffortPermissionResponder(
			sessID, deps.Permissions, deps.ApprovalRequestSink,
		),
		OnSessionID:       onSessionID,
		AutoFireFirstTurn: shouldAutoFireFirstTurn(opts.Mode),
		FirstTurnPayload:  string(firstTurnPayload),
	})
	if err != nil {
		if ownedCanonicalSink != nil {
			_ = ownedCanonicalSink.Close()
		}
		if hadLineage && deps.PathGrants != nil {
			deps.PathGrants.ClearLineage(sessID)
		}
		_ = deps.Store.MarkRuntimeFailed(sessID, err.Error())
		return cleanup(fmt.Errorf("agent.Boot: wrapper.New: %w", err))
	}

	runCtx, runCancel := context.WithCancel(context.Background())
	sess := &Session{
		ID:           sessID,
		Mode:         opts.Mode,
		Provider:     providerName,
		BootDir:      bootDir,
		WorkspaceDir: ws.Root,
		deps:         deps,
		startedAt:    time.Now(),
		hadLineage:   hadLineage,
		wr:           wr,
		isACP:        isACP,
		runDone:      make(chan struct{}),
		runCancel:    runCancel,
	}
	if err := deps.Manager.AdmitLaunch(sess); err != nil {
		runCancel()
		if ownedCanonicalSink != nil {
			_ = ownedCanonicalSink.Close()
		}
		_ = deps.Store.MarkRuntimeFailed(sessID, err.Error())
		return cleanup(fmt.Errorf("agent.Boot: admit session launch: %w", err))
	}
	var registrationErr error
	sink.onReady = func() {
		// Registration is part of the synchronous readiness observation so a
		// fast process exit cannot race Boot into publishing a dead handle.
		// Recovery replacements are adopted by the service with Adopt so it can
		// still capture and stop a live predecessor; pre-storing here would
		// overwrite that only reference before adoption.
		registrationErr = deps.Manager.RegisterReady(sessID, sess, opts.IsRelaunch)
		close(readyCh)
	}

	// wrapper.Wrapper.Run owns the full start-wait-emit-exit lifecycle and
	// blocks until the session exits, so run it on a background
	// goroutine detached from ctx (a long-lived ModeLongLived chat session
	// must outlive the request-scoped ctx a caller passes into Boot).
	// sess.wr becomes usable for SendInput/Stop the moment Wrapper.Run
	// emits runtimeevents.KindSessionReady — sink.onReady (closing
	// readyCh) mirrors that exact point, so Boot blocks below until it
	// fires (or Run exits first, or the caller's ctx is canceled) before
	// returning sess to its own caller.
	//
	// runCancel is deliberately NOT called from Session.Stop (manager.go)
	// — only here, deferred, strictly after wr.Run has already returned
	// and computed sess.runErr. Wrapper.Run's own tail end falls back to
	// ctx.Err() whenever the underlying session.Wait() reports a nil
	// error (true for every clean stop, e.g. agentkit's adapterSession.
	// Wait always returns a nil error) — canceling runCtx from Stop
	// while Run's own session.Wait()/ctx.Err() check is still in flight
	// races into an *avoidable* context.Canceled on an otherwise-clean
	// cooperative stop. wr.Stop's own ctx parameter (caller-bounded) is
	// already the correct, sufficient interrupt mechanism — see
	// manager.go's Stop.
	go func() {
		defer deps.Manager.DiscardPending(sess)
		defer runCancel()
		defer close(sess.runDone)
		if ownedCanonicalSink != nil {
			defer func() { _ = ownedCanonicalSink.Close() }()
		}
		sess.runErr = recoveryCompatibleWrapperError(wr.Run(runCtx), isACP)
		state := "done"
		if sess.runErr != nil {
			state = "failed"
		}
		_ = deps.Store.UpdateState(sessID, state, 0)
	}()

	select {
	case <-readyCh:
		if registrationErr != nil {
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = sess.Stop(stopCtx)
			stopCancel()
			<-sess.runDone
			_ = deps.Store.MarkRuntimeFailed(sessID, registrationErr.Error())
			return cleanup(fmt.Errorf("agent.Boot: register ready session: %w", registrationErr))
		}
		_ = deps.Store.UpdateState(sessID, "running", 0)
	case <-sess.runDone:
		runCancel()
		if hadLineage && deps.PathGrants != nil {
			deps.PathGrants.ClearLineage(sessID)
		}
		failErr := sess.runErr
		if failErr == nil {
			failErr = errors.New("agent.Boot: wrapper.Run exited before session became ready")
		}
		_ = deps.Store.MarkRuntimeFailed(sessID, failErr.Error())
		return cleanup(fmt.Errorf("agent.Boot: wrapper.Run: %w", failErr))
	case <-ctx.Done():
		runCancel()
		<-sess.runDone // block until the background goroutine observes cancellation
		if hadLineage && deps.PathGrants != nil {
			deps.PathGrants.ClearLineage(sessID)
		}
		// MarkRuntimeFailed sets state="failed" AND persists a reason in
		// one write, superseding whatever bare UpdateState the background
		// goroutine above already wrote (state alone, no reason) — mirrors
		// the <-sess.runDone branch's own MarkRuntimeFailed call above so
		// every Boot abort path leaves a forensic reason, not just a state.
		_ = deps.Store.MarkRuntimeFailed(sessID, "agent.Boot: caller ctx canceled: "+ctx.Err().Error())
		return cleanup(ctx.Err())
	}

	// Mode-specific drive. AutoFireFirstTurn (wired into wrapper.Config
	// above) handles the kickoff for ModeOneShot / ModeSubagent /
	// ModeBackground; ModeLongLived and ModeResume callers drive turns
	// externally via Session.SendInput.
	return sess, nil
}

// envMapToSlice flattens the composed env map into a sorted KEY=VALUE
// slice for wrapper.ChildEnvironment. Sorting keeps validation, tests, and
// child-process diagnostics deterministic.
func envMapToSlice(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(env))
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}

// newSessionID generates a session id when the caller doesn't supply one.
// ULID is monotonic-time prefixed so logs sort naturally.
func newSessionID() string {
	return ulid.Make().String()
}
