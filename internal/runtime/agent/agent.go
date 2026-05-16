package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	agentsessions "github.com/hollis-labs/go-agent-sessions/agentsessions"
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

	// ExtraArgs is appended verbatim to the spawned process's argv
	// (after the adapter's BuildArgs output). CW-20260514-0048: lets
	// a boot-profile-driven launch thread LaunchSpec.Args into the
	// runtime without forking agent.Boot. The runtime lib already
	// supports the surface as agentsessions.StartOptions.ExtraArgs.
	//
	// Default empty preserves the pre-CW-20260514-0048 behavior. The
	// runtime does NOT template-substitute these — callers pre-resolve
	// any placeholders before passing the slice.
	ExtraArgs []string

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
// thin wrappers around Dependencies.SessionsManager bound to this session's
// id; no caller should reach into the runtime lib directly.
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
// supervisor + sandbox gates per Mode, persists the runtime row, and starts
// the runtime via Dependencies.SessionsManager.
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
	if deps.SessionsManager == nil {
		return nil, errors.New("agent.Boot: Dependencies.SessionsManager is required")
	}
	if deps.Agents == nil {
		return nil, errors.New("agent.Boot: Dependencies.Agents is required")
	}
	if deps.ProviderAdapter == nil {
		return nil, errors.New("agent.Boot: Dependencies.ProviderAdapter is required")
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

	layout, params := composeBootdirParams(deps, opts, profile, sessID)
	bootDir, err := layout.Setup(params)
	if err != nil {
		return nil, fmt.Errorf("agent.Boot: bootdir setup: %w", err)
	}

	// Anything past this point that fails must clean the boot dir to avoid
	// leaking $TMPDIR entries.
	cleanup := func(failure error) (*Session, error) {
		_ = os.RemoveAll(bootDir)
		return nil, failure
	}

	envMap := composeEnv(profile, opts)
	envMap = layout.AmendEnv(envMap, bootDir)

	spawnWorkdir := layout.SpawnWorkdir(bootDir, opts.Workdir)

	providerName := effectiveProvider(opts, profile)
	adapter := deps.ProviderAdapter(providerName)
	if adapter == nil {
		return cleanup(fmt.Errorf("agent.Boot: no adapter registered for provider %q", providerName))
	}

	runtimeCfg := runtimeConfigForAdapter(adapter, providerName, opts.Mode)
	runtimeCfg.ID = sessID
	runtimeCfg.Kind = "cli"

	rt, err := agentsessions.NewFromAdapter(runtimeCfg)
	if err != nil {
		return cleanup(fmt.Errorf("agent.Boot: build runtime: %w", err))
	}

	// PathGrants lineage for nested subagents. nil-safe: ModeSubagent
	// validation already enforced ParentSessionID non-empty.
	hadLineage := false
	if opts.Mode == ModeSubagent && deps.PathGrants != nil {
		deps.PathGrants.RegisterLineage(sessID, opts.ParentSessionID)
		hadLineage = true
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
			if hadLineage && deps.PathGrants != nil {
				deps.PathGrants.ClearLineage(sessID)
			}
			return cleanup(fmt.Errorf("agent.Boot: persist runtime row: %w", err))
		}
	}

	var sessionIDPreset string
	if opts.Mode == ModeResume && opts.ResumeFromCheckpoint != "" {
		cp, err := deps.Store.GetCheckpoint(opts.ResumeFromCheckpoint)
		if err != nil {
			if hadLineage && deps.PathGrants != nil {
				deps.PathGrants.ClearLineage(sessID)
			}
			_ = deps.Store.MarkRuntimeFailed(sessID, err.Error())
			return cleanup(fmt.Errorf("agent.Boot: load checkpoint: %w", err))
		}
		if cp != nil {
			sessionIDPreset = cp.ProviderSessionID
		}
	}

	var supervisor *agentsessions.SupervisorOptions
	if opts.Mode == ModeLongLived && runtimeCfg.Caps.PTY {
		telem := deps.Telemetry
		if telem == nil {
			telem = noopTelemetry{}
		}
		recovery := deps.Recovery
		supervisor = &agentsessions.SupervisorOptions{
			IdleKill:          15 * time.Minute,
			RestartOnCrash:    2,
			MaxRestartBackoff: 30 * time.Second,
			WatchdogTimeout:   0,
			OnRestart: func(attempt int, prevExit *agentsessions.ExitError) {
				telem.RecordPTYRestart(sessID, attempt, prevExit)
				if recovery != nil {
					recovery.OnRestart(sessID, attempt, prevExit)
				}
			},
		}
	}

	sandboxProfile := buildSandboxProfile(deps.SandboxBaseProfile, opts, ws.Root, bootDir)

	onSessionID := func(id string) {
		_ = deps.Store.SetProviderSessionID(sessID, id)
	}

	var eventFanout chan<- agentsessionsStreamEventChan
	_ = eventFanout // type alias bridge — see below
	var typedCallback = func() interface{} {
		if deps.TypedEventCallback != nil {
			return deps.TypedEventCallback(sessID)
		}
		return nil
	}()
	_ = typedCallback

	// First-turn payload: ModeOneShot can override with OneShotPrompt; all
	// others use the kickoff convention pointing at the planted boot.md.
	firstTurn := composeKickoff(opts.Role, sessID, opts.ParentSessionID)
	if opts.Mode == ModeOneShot && opts.OneShotPrompt != "" {
		firstTurn = opts.OneShotPrompt
	}

	bootPrompt := layout.BootPrompt(profile, opts)
	firstTurnPayload := []byte(firstTurn)

	// CW-20260516-0007: the streaming-stdio runtime treats the child's
	// stdin strictly as NDJSON. Two adjustments for that runtime:
	//
	//  1. Suppress the raw boot-prompt-on-stdin write. claudeLayout's
	//     BootMode is "stdin" (correct for the PTY TUI), which makes the
	//     runtime write BootPrompt verbatim at spawn. For streaming-stdio
	//     that markdown text crashes claude's JSON parser (c207/c208).
	//     The boot prompt's content is now planted into CLAUDE.md (see
	//     composeBootdirParams → resolveBootPrompt → claudeLayout), which
	//     claude auto-discovers from its cwd — so dropping the stdin
	//     write loses nothing.
	//  2. NDJSON-frame FirstTurnPayload so AutoFireFirstTurn modes
	//     (one-shot / subagent / background) deliver a parseable kickoff.
	if shouldUseStreamingStdio(providerName, opts.Mode) {
		bootPrompt = ""
		if framed, ferr := streamingStdioUserFrame(firstTurn); ferr == nil {
			firstTurnPayload = framed
		}
	}

	startOpts := agentsessions.StartOptions{
		Workdir:           spawnWorkdir,
		WorkspaceDir:      ws.Root,
		LogPath:           ws.LogPath,
		BootPrompt:        bootPrompt,
		BootMode:          layout.BootMode(),
		Env:               envMapToSlice(envMap),
		Profile:           sandboxProfile,
		SessionIDPreset:   sessionIDPreset,
		OnSessionID:       onSessionID,
		Supervisor:        supervisor,
		ResourceLimits:    nil,
		AutoFireFirstTurn: shouldAutoFireFirstTurn(opts.Mode),
		FirstTurnPayload:  firstTurnPayload,
		AttachEnabled:     true,
		ExtraArgs:         append([]string(nil), opts.ExtraArgs...),
	}
	if deps.EventFanout != nil {
		startOpts.EventFanout = deps.EventFanout(sessID)
	}
	if deps.TypedEventCallback != nil {
		startOpts.TypedEventCallback = deps.TypedEventCallback(sessID)
	}

	sessionMeta := metaToStringMap(opts.SessionMeta)
	if err := deps.SessionsManager.Start(ctx, agentsessions.StartRequest{
		ID:          sessID,
		Runtime:     rt,
		Options:     startOpts,
		SessionMeta: sessionMeta,
	}); err != nil {
		if hadLineage && deps.PathGrants != nil {
			deps.PathGrants.ClearLineage(sessID)
		}
		_ = deps.Store.MarkRuntimeFailed(sessID, err.Error())
		return cleanup(fmt.Errorf("agent.Boot: SessionsManager.Start: %w", err))
	}

	sess := &Session{
		ID:           sessID,
		Mode:         opts.Mode,
		Provider:     providerName,
		BootDir:      bootDir,
		WorkspaceDir: ws.Root,
		deps:         deps,
		startedAt:    time.Now(),
		hadLineage:   hadLineage,
	}

	// Mode-specific drive. AutoFireFirstTurn handles the kickoff for
	// ModeOneShot / ModeSubagent / ModeBackground; ModeLongLived and
	// ModeResume callers drive turns externally.
	if opts.Mode == ModeOneShot && !shouldAutoFireFirstTurn(opts.Mode) {
		// Defensive: shouldAutoFireFirstTurn(ModeOneShot) is true today,
		// but the manual SendInput path remains here for future modes
		// where AutoFireFirstTurn is false but the caller's intent is a
		// single immediate turn.
		// firstTurnPayload is the streaming-stdio-framed form when the
		// runtime requires it (CW-20260516-0007); identical to
		// []byte(firstTurn) for non-streaming runtimes.
		if err := deps.SessionsManager.SendInput(sessID, firstTurnPayload); err != nil {
			_ = sess.Stop(context.Background())
			return nil, fmt.Errorf("agent.Boot: ModeOneShot SendInput: %w", err)
		}
	}

	return sess, nil
}

// agentsessionsStreamEventChan is an unused alias retained as a placeholder
// for clarity around the EventFanout shape; deps.EventFanout returns the
// real chan<- llmtypes.StreamEvent. Kept un-exported.
type agentsessionsStreamEventChan = struct{}

// envMapToSlice flattens the composed env map into the KEY=VALUE slice
// agentsessions.StartOptions.Env requires. Sorted keys for deterministic
// ordering (helps test assertions and child-process debug output).
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

// metaToStringMap projects the any-typed Options.SessionMeta into the
// string-typed map agentsessions.StartRequest.SessionMeta requires.
// Non-string values are rendered with fmt.Sprintf("%v", v).
func metaToStringMap(meta map[string]any) map[string]string {
	if len(meta) == 0 {
		return nil
	}
	out := make(map[string]string, len(meta))
	for k, v := range meta {
		switch tv := v.(type) {
		case string:
			out[k] = tv
		default:
			out[k] = fmt.Sprintf("%v", v)
		}
	}
	return out
}

// newSessionID generates a session id when the caller doesn't supply one.
// ULID is monotonic-time prefixed so logs sort naturally.
func newSessionID() string {
	return ulid.Make().String()
}
