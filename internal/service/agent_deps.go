package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	agentsessions "github.com/hollis-labs/agentkit/agentsessions"
	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/go-providers/provider/events"
	"github.com/hollis-labs/go-sandbox/sandbox"
	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/recovery/broker"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
	"github.com/hollis-labs/nanite/internal/skillvendor"
	"github.com/hollis-labs/nanite/internal/store"
)

// AgentDepsConfig holds the inputs the chat-service composition root uses to
// build *runtimeagent.Dependencies. Construction is deliberately a separate
// step from chatServiceImpl wiring so the deps can be re-used by the subagent
// runner and (eventually) the background dispatcher without re-creating the
// shared wrapper session registry.
type AgentDepsConfig struct {
	Store           *store.Store
	PathGrants      *permission.PathGrants
	Streams         *StreamManager
	CLIAdapters     []provider.CLIAdapter
	WorkspacesRoot  string
	BinaryPath      string
	DBPath          string
	SandboxBaseProf sandbox.Profile
	Permissions     *permission.Engine

	// CLIWritableRoots is the directory allow-list a CLI-launch agent
	// (codex / claude) may write to beyond its boot dir. Threaded onto
	// Dependencies.CLIWritableRoots so the boot-dir layouts widen the
	// planted provider config (codex [sandbox_workspace_write], claude
	// permissions.additionalDirectories). Sourced from the same
	// dev_tools_allowed_paths config setting that scopes the in-process
	// dev_* tools (CW-20260518-0075).
	CLIWritableRoots []string

	// APIBaseURL is the base URL of the live nanite API server, threaded
	// into the boot dir's .mcp.json so a CLI-launched chat agent's
	// `nanite mcp` subprocess forwards self-tool calls back to the
	// running harness. Empty = subprocess runs self-tools locally.
	APIBaseURL string

	// MCP, when non-nil, wires the recovery broker's MCP-transport
	// remediation adapter (broker.MCPControl). The adapter forwards
	// RestartTransport to mcp.Manager.RestartStdioTransports so the broker
	// can recover from a wedged MCP stdio subprocess. Nil leaves
	// Dependencies.MCP unwired and the broker degrades to escalating
	// RemediationRefreshMCPTransport classifications as Permanent.
	MCP *mcp.Manager

	// Providers is the API provider registry the recovery broker's
	// credentials adapter consults to push refreshed keys onto cached
	// SDK clients. Optional — nil disables the credentials remediation
	// path entirely (Refresh returns an error and the broker escalates
	// to Permanent).
	Providers *provider.Registry

	// SkillVendor is the content-addressed vendored skill store
	// (internal/skillvendor.Store) — TASKS/skills/10 wires this onto
	// runtimeagent.Dependencies.SkillVendor so CLI-hosted agents can plant
	// their granted skills' vendored files into their native boot-dir
	// skill location. nil disables skill planting entirely (the container
	// already tolerates a nil skill vendor store elsewhere — see
	// container.go's own skillVendorErr handling).
	SkillVendor *skillvendor.Store
}

// AgentDepsBundle aggregates the artifacts BuildAgentDependencies returns.
// Distinct from a tuple return so callers can pluck the registry-bearing
// adapters they need without restructuring the signature each time a new
// composition-root adapter joins the broker wiring.
type AgentDepsBundle struct {
	// Deps is the composed runtime agent.Dependencies struct passed
	// into runtimeagent.Boot.
	Deps *runtimeagent.Dependencies

	// Manager is the single Nanite binding registry for wrapper-owned
	// sessions, shared by chat lookup, recovery, reaping, and shutdown.
	Manager *runtimeagent.SessionManager

	// Bridge is the agentEventBridge held by the chat service so
	// driveBootSession can bind per-session routers.
	Bridge *agentEventBridge

	// BootDirAdapter is the broker.BootDirOps adapter wired into the
	// recovery broker. The chat service calls Track / Untrack on it so
	// the broker has bootDir + Options on hand when a remediation fires.
	BootDirAdapter *agentBootDirAdapter

	// BootAdapter is the broker.AgentBoot adapter wired into the
	// recovery broker. The chat composition root installs a pre-boot
	// hook on it (CW-20260514-0049) so boot-profile-backed sessions
	// re-resolve via Registry.CompileFor under the fresh-catalog
	// policy at recovery-relaunch time. Normal launches do NOT touch
	// this adapter — they go through runtimeagent.Boot directly via
	// driveBootSession, keeping the resume vs normal-start split
	// structural.
	BootAdapter *agentBootAdapter
}

// BuildAgentDependencies wires runtime dependencies plus the single wrapper
// session binding registry. Returns an AgentDepsBundle aggregating the composed
// Dependencies struct, the Manager instance (for daemon-bootstrap orphan
// sweep + Shutdown drain), the agentEventBridge (held by the chat service
// so driveBootSession can bind per-turn routers), and the per-adapter
// registry handles (BootDirAdapter today; MCP / Credentials siblings land
// alongside as their adapters wire in).
//
// The composition root is the single point that:
//
//   - constructs the shared Wrapper/ACP session manager
//   - wires the Store-backed RuntimeStore
//   - wires the AgentProfileResolver
//   - resolves the per-provider CLIAdapter
//   - threads the EventFanout / TypedEventCallback factories the chat service
//     binds per-session.
func BuildAgentDependencies(cfg AgentDepsConfig) (AgentDepsBundle, error) {
	if cfg.Store == nil {
		return AgentDepsBundle{}, errors.New("BuildAgentDependencies: Store is required")
	}
	if cfg.Streams == nil {
		return AgentDepsBundle{}, errors.New("BuildAgentDependencies: Streams is required")
	}

	binPath := cfg.BinaryPath
	if binPath == "" {
		exe, err := os.Executable()
		if err != nil {
			return AgentDepsBundle{}, fmt.Errorf("BuildAgentDependencies: resolve binary: %w", err)
		}
		if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
			binPath = resolved
		} else {
			binPath = exe
		}
	}

	workspacesRoot := cfg.WorkspacesRoot
	if workspacesRoot == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			workspacesRoot = filepath.Join(home, "."+brand.ID, "workspaces")
		}
	}

	dbPath := cfg.DBPath
	if dbPath == "" {
		dbPath = cfg.Store.DBPath(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */)
	}

	manager := runtimeagent.NewSessionManager()

	resolver := &agentProfileResolver{store: cfg.Store}
	runtimeStore := &agentRuntimeStore{store: cfg.Store}

	adapterIndex := indexAdapters(cfg.CLIAdapters)
	providerAdapter := func(name string) provider.CLIAdapter {
		if name == "" {
			return nil
		}
		// Strip nanite registry prefixes ("pty-", "sub-") so callers can
		// pass a session-side provider name verbatim. The adapter Name
		// itself is always the bare adapter (claude/codex/opencode/...).
		bare := stripRegistryPrefix(name)
		if a, ok := adapterIndex[bare]; ok {
			return a
		}
		return nil
	}

	telemetry := agentTelemetry{}

	bridge := &agentEventBridge{streams: cfg.Streams}
	approvalRequestSink := func(req *permission.ApprovalRequest) {
		if req == nil {
			return
		}
		payload, err := json.Marshal(chat.ApprovalRequestPayload{
			RequestID: req.ID,
			Tool:      req.ToolName,
			Input:     req.Input,
			Reason:    req.Reason,
		})
		if err != nil {
			return
		}
		_ = cfg.Streams.BroadcastSessionStreamEvent(req.SessionID, chat.StreamEvent{
			Type: "approval_request",
			Data: string(payload),
		})
	}

	deps := &runtimeagent.Dependencies{
		Agents:              resolver,
		Manager:             manager,
		Store:               runtimeStore,
		PathGrants:          cfg.PathGrants,
		EventFanout:         bridge.fanout,
		TypedEventCallback:  bridge.typedCallback,
		Permissions:         cfg.Permissions,
		ApprovalRequestSink: approvalRequestSink,
		ProviderAdapter:     providerAdapter,
		MCPConfig: runtimeagent.MCPConfig{
			BinaryPath: binPath,
			DBPath:     dbPath,
			ServerID:   brand.ID,
			APIBaseURL: cfg.APIBaseURL,
		},
		WorkspacesRoot:     workspacesRoot,
		CLIWritableRoots:   cfg.CLIWritableRoots,
		Telemetry:          telemetry,
		SandboxBaseProfile: cfg.SandboxBaseProf,
		// TASKS/skills/10: cfg.Store satisfies runtimeagent.SkillStore
		// (GetSkillBySlug + ListAgentKnownSkills) directly — no adapter
		// needed, same as several other Dependencies fields backed
		// straight by *store.Store elsewhere in this file.
		Skills: cfg.Store,
	}
	// cfg.SkillVendor is a *skillvendor.Store — a nil *skillvendor.Store
	// assigned directly into the SkillVendorReader interface field would
	// produce a non-nil interface wrapping a nil pointer (the classic Go
	// "typed nil" trap), which skillFilesForProvider's own `!= nil` guard
	// would then treat as "wired" and panic on the first ReadFiles call.
	// Guarding the assignment keeps deps.SkillVendor a genuine nil
	// interface when the container's skill vendor store construction
	// failed (container.go's own skillVendorErr fallback) — skill
	// planting degrades to a clean no-op in that case, matching how
	// install/sync already degrades to unavailable rather than crashing.
	if cfg.SkillVendor != nil {
		deps.SkillVendor = cfg.SkillVendor
	}
	// Orphan reconciliation consults the same registry chat and shutdown
	// use; no second liveness map can drift during recovery replacement.
	deps.LiveSessions = manager

	// BootDir adapter — satisfies broker.BootDirOps by re-running the
	// per-provider sandbox-dir population logic against the existing
	// boot dir. The chat service calls bootDirAdapter.Track right after
	// each successful runtimeagent.Boot so the broker has bootDir +
	// Options on hand when a Repopulate / RegenerateCLAUDEMD remediation
	// fires.
	bootDirAdapter, err := newAgentBootDirAdapter(deps)
	if err != nil {
		return AgentDepsBundle{}, fmt.Errorf("BuildAgentDependencies: bootdir adapter: %w", err)
	}

	// Construct the in-process recovery broker and wire it into deps.
	// AgentBoot is a closure over `deps` so the broker dispatches
	// replacement sessions through the same composition root. BootDir
	// is wired here (Phase 9 — CW-20260510-0014): Repopulate /
	// RegenerateCLAUDEMD re-run the per-provider sandbox-dir population
	// logic against the existing boot dir. MCP is wired here when
	// cfg.MCP is non-nil (Phase 9 — CW-20260510-0015); otherwise it
	// stays nil and RemediationRefreshMCPTransport classifications
	// surface as broker errors handled by the orchestration layer.
	// Credentials is wired here (Phase 9, CW-20260510-0016): re-reads
	// the OS keychain via internal/secrets and pushes the fresh key
	// onto the cached internal/llm/{anthropic,openai}.Client via
	// SetAPIKey. CLI providers (claude/codex/opencode) intentionally
	// error from Refresh because their auth lives outside nanite's
	// reach — see recoveryCredentialsAdapter.Refresh for the full
	// disposition.
	bootAdapter := &agentBootAdapter{deps: deps}
	brokerDeps := broker.Dependencies{
		AgentBoot: bootAdapter,
		BootDir:   bootDirAdapter,
		Store: &recoveryBrokerStore{
			store: cfg.Store,
		},
		Envelope: &recoveryEnvelopeSink{
			streams: cfg.Streams,
		},
		Credentials: newRecoveryCredentialsAdapter(resolver, cfg.Providers),
		// HTTPRetry is deliberately left unwired here (Phase 0 task 04):
		// the real adapter closes over chatServiceImpl.RetryLastMessage,
		// and chatServiceImpl doesn't exist yet at this point in
		// composition-root construction (NewChatService runs after
		// BuildAgentDependencies). The container wires it post-
		// construction via broker.SetHTTPRetry, mirroring how
		// SetReplacementSessionHook handles the identical ordering
		// problem for the CLI replacement-session adoption hook.
	}
	if cfg.MCP != nil {
		brokerDeps.MCP = &recoveryMCPAdapter{manager: cfg.MCP}
	}
	broker := broker.NewBroker(brokerDeps)
	deps.Recovery = broker

	return AgentDepsBundle{
		Deps:           deps,
		Manager:        manager,
		Bridge:         bridge,
		BootDirAdapter: bootDirAdapter,
		BootAdapter:    bootAdapter,
	}, nil
}

// agentBootAdapter satisfies broker.AgentBoot by forwarding into
// agent.Boot with IsRelaunch=true so CreateRuntimeRow is skipped (the
// broker has already transitioned the runtime row via
// MarkAgentRuntimeRelaunching).
//
// CW-20260514-0049: PreBootHook is an optional pre-boot interceptor the
// chat composition root MAY install after construction. The hook
// receives a pointer to the agent.Options the broker assembled and may
// mutate it before agent.Boot fires. Hook errors abort the relaunch
// (returned verbatim to the broker; orchestration escalates to Permanent
// with an actionable reason). Empty hook (the current default — no
// caller installs one after TASKS/phase-2/04-retire-boot-profile-
// catalog.md removed the boot-profile-catalog resume-reresolve hook that
// used to be the sole populator) = no pre-boot intercept.
//
// The hook is also where resume-flavored fields (Mode=ModeResume,
// ResumeFromCheckpoint) MAY be threaded onto Options — this is the
// crash-recovery code path and the only structural entry point for
// resume IDs. Normal launches go through driveBootSession directly,
// which never touches resume fields, keeping the "never pass resume ID
// on normal launch" guarantee structural.
type agentBootAdapter struct {
	deps *runtimeagent.Dependencies

	mu          sync.Mutex
	preBootHook func(opts *runtimeagent.Options) error
}

// SetPreBootHook installs (or clears) the pre-boot interceptor. Safe for
// concurrent callers — the chat composition root installs it once at
// startup, but tests may rebind during a single chatServiceImpl lifetime.
// nil clears the hook so a future test can opt out of intercept.
func (a *agentBootAdapter) SetPreBootHook(hook func(opts *runtimeagent.Options) error) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.preBootHook = hook
	a.mu.Unlock()
}

func (a *agentBootAdapter) Boot(ctx context.Context, opts runtimeagent.Options) (*runtimeagent.Session, error) {
	a.mu.Lock()
	hook := a.preBootHook
	a.mu.Unlock()
	if hook != nil {
		if err := hook(&opts); err != nil {
			return nil, err
		}
	}
	opts.IsRelaunch = true
	return runtimeagent.Boot(ctx, a.deps, opts)
}

// recoveryHTTPRetryAdapter satisfies broker.HTTPRetry for HTTP-provider
// (bootdir-free) chat sessions — Phase 0 task 04 / decision log §19's
// real HTTP-provider retry path.
//
// Deliberately narrow (mirrors agentBootAdapter's shape): the adapter
// holds only a bound method value, not the full ChatService interface,
// per deps.go's "keep the interface as narrow as AgentBoot's" guidance.
// retryLastMessage re-invokes the chat harness's existing "retry last
// message" mechanism — the same one a user's manual [Retry] click uses
// (chatServiceImpl.RetryLastMessage): reset the circuit breaker, find
// the session's last user message, dispatch a fresh generateResponse
// turn through the FULL harness (tools, slots, system prompt — no
// stripped-down reconstruction). That's a deliberate reuse decision:
// hand-rolling a second, narrower "resume this turn" code path here
// would duplicate stream registration, message persistence, and
// cancellation semantics RetryLastMessage already gets right, for no
// real benefit.
//
// Constructed post-NewChatService and wired via Broker.SetHTTPRetry
// (see BuildAgentDependencies's brokerDeps.HTTPRetry comment for why it
// can't be wired at NewBroker time).
type recoveryHTTPRetryAdapter struct {
	retryLastMessage func(ctx context.Context, sessionID string) (messageID string, err error)
}

// newRecoveryHTTPRetryAdapter builds the adapter from a bound method
// value (chatSvcImpl.RetryLastMessage in production; a scripted fake in
// tests).
func newRecoveryHTTPRetryAdapter(retryLastMessage func(ctx context.Context, sessionID string) (string, error)) *recoveryHTTPRetryAdapter {
	return &recoveryHTTPRetryAdapter{retryLastMessage: retryLastMessage}
}

func (a *recoveryHTTPRetryAdapter) Retry(ctx context.Context, ev *broker.FailureEvent) error {
	if a == nil || a.retryLastMessage == nil {
		return errors.New("recovery: http retry adapter not wired")
	}
	if ev == nil || ev.SessionID == "" {
		return errors.New("recovery: http retry: missing session id")
	}
	_, err := a.retryLastMessage(ctx, ev.SessionID)
	return err
}

// recoveryBrokerStore satisfies broker.BrokerStore against the store
// package. MarkRuntimeRelaunching sets state="launching" with the
// broker's audit reason; WriteBreadcrumb persists into
// nanite_recovery_breadcrumbs (migration 054).
type recoveryBrokerStore struct {
	store *store.Store
}

func (s *recoveryBrokerStore) MarkRuntimeRelaunching(sessionID, reason string) error {
	return s.store.MarkAgentRuntimeRelaunching(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, sessionID, reason)
}

func (s *recoveryBrokerStore) WriteBreadcrumb(b broker.Breadcrumb) error {
	return s.store.WriteRecoveryBreadcrumb(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, &store.RecoveryBreadcrumb{
		Timestamp:    b.Timestamp,
		SessionID:    b.SessionID,
		Class:        b.Class.String(),
		Cause:        b.Cause,
		Remediation:  b.Remediation.String(),
		Action:       b.Action.String(),
		Outcome:      b.Outcome.String(),
		AttemptCount: b.AttemptCount,
		DurationMs:   b.DurationFromFailure.Milliseconds(),
		Reason:       b.Reason,
	})
}

// mcpTransportRestarter is the narrow contract recoveryMCPAdapter needs
// from the host MCP manager. *mcp.Manager satisfies it via
// RestartStdioTransports. Defined as an interface (not a *mcp.Manager
// dependency) so unit tests can substitute a fake without spawning real
// stdio subprocesses.
type mcpTransportRestarter interface {
	RestartStdioTransports(ctx context.Context) error
}

// recoveryMCPAdapter satisfies broker.MCPControl. Phase 9 (CW-20260510-0015)
// wiring: the broker's RemediationRefreshMCPTransport action lands here
// when the classifier observes MCPTransport.Down on a failure event.
//
// The adapter cycles every stdio MCP transport the manager owns. The
// transports use lazy start() — Close reaps the (possibly-wedged)
// subprocess; the next ListTools / CallTool from any caller spawns a
// fresh subprocess in its place. Non-stdio transports (HTTP / plugin /
// builtin) are skipped inside the manager since they don't have a
// subprocess to wedge.
//
// The agent CLI's own .mcp.json-spawned subprocess is owned by the agent
// process (claude / codex / opencode), not by the host. The broker's
// follow-up dispatch (replacement session via AgentBoot) handles that
// side: a relaunched session re-reads .mcp.json and respawns its own
// MCP subprocess from scratch. This adapter handles only the host-side
// stdio transports the manager itself spawned.
//
// Idempotency: relies on *mcp.StdioTransport.Close being a no-op when
// the subprocess is already reaped. Back-to-back RestartTransport calls
// during a still-restarting state cycle the second-call's no-op closes
// without panicking.
//
// Bounded by ctx — RestartStdioTransports returns ctx.Err() between
// transports so the broker's 10s remediation timeout is honored.
type recoveryMCPAdapter struct {
	manager mcpTransportRestarter
}

func (a *recoveryMCPAdapter) RestartTransport(ctx context.Context, sessionID string) error {
	if a == nil || a.manager == nil {
		// Defensive: a fully-nil adapter would have been left out of
		// brokerDeps; this guards against a partially-constructed adapter
		// reaching the dispatch path.
		return errors.New("recovery: MCP adapter not wired")
	}
	if err := a.manager.RestartStdioTransports(ctx); err != nil {
		return fmt.Errorf("recovery: restart mcp transports: %w", err)
	}
	slog.Info("recovery: restarted mcp stdio transports", "session_id", sessionID)
	return nil
}

// stripRegistryPrefix drops the nanite registry-side prefix
// ("pty-claude" → "claude", "sub-codex" → "codex"). Callers that already
// pass the bare adapter name see no change.
//
// CW-20260514-0045: legacy bare "pty" (pre-CW-20260508-0002 default)
// normalizes to "claude" so dropdown-selected CLI providers still resolve
// to a registered adapter after the PTYBridge registry registrations
// were removed. Delegates to chat.NormalizeCLIProvider so the string-shape
// normalization call sites (chat_generate's ProviderAdapter lookup,
// bootdirLayoutFor, factory.go's normalizeProviderName, this) can't drift.
//
// Post-decision helper only (Phase 2 item 01,
// TASKS/phase-2/01-wire-runtime-kind-routing.md) — this is called from
// agentProfileResolver's ProviderAdapter closure ONLY once
// classifyNilProvider (service/chat.go) has already decided the turn
// routes CLI (primarily via agent_profiles.runtime_kind, not this
// prefix convention); its job here is deriving which CLI adapter to
// look up, not deciding CLI-vs-API.
func stripRegistryPrefix(name string) string {
	return chat.NormalizeCLIProvider(name)
}

// indexAdapters builds the providerName → CLIAdapter resolution map from
// the slice threaded through ContainerConfig.
func indexAdapters(in []provider.CLIAdapter) map[string]provider.CLIAdapter {
	out := make(map[string]provider.CLIAdapter, len(in))
	for _, a := range in {
		if a == nil {
			continue
		}
		out[a.Name()] = a
	}
	return out
}

// --- AgentProfiles adapter ---

// agentProfileResolver implements runtimeagent.AgentProfiles backed by the
// store. GetOrDefault is the only contract; Boot consults it to resolve the
// caller-supplied AgentProfile slug into a concrete row. An empty slug or an
// unknown slug falls back to a synthetic default profile (claude provider,
// no system prompt) so Boot can still spawn — the chat-service does not
// hand-craft profiles for ad-hoc sessions.
type agentProfileResolver struct {
	store *store.Store
}

func (r *agentProfileResolver) GetOrDefault(name string) (*store.AgentProfile, error) {
	if name != "" {
		if p, err := r.store.GetAgentBySlug(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, name); err == nil && p != nil {
			return p, nil
		}
		if p, err := r.store.GetAgent(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, name); err == nil && p != nil {
			return p, nil
		}
	}
	return &store.AgentProfile{
		ID:              "agent-runtime-default",
		Name:            "default",
		Slug:            "default",
		DefaultProvider: "claude",
	}, nil
}

// --- RuntimeStore adapter ---

// agentRuntimeStore implements runtimeagent.RuntimeStore against the store's
// agent_runtime table (migration 050).
type agentRuntimeStore struct {
	store *store.Store
}

func (s *agentRuntimeStore) CreateRuntimeRow(row *runtimeagent.RuntimeRow) error {
	if row == nil {
		return errors.New("agentRuntimeStore.CreateRuntimeRow: nil row")
	}
	parent := ""
	if row.ParentSessionID != nil {
		parent = *row.ParentSessionID
	}
	return s.store.CreateAgentRuntimeRow(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, &store.AgentRuntimeRow{
		ID:              row.ID,
		AgentProfile:    row.AgentProfile,
		Provider:        row.Provider,
		Mode:            row.Mode,
		Workdir:         row.Workdir,
		State:           row.State,
		PID:             row.PID,
		ParentSessionID: parent,
		MetaJSON:        marshalMeta(row.Meta),
		StartedAt:       row.StartedAt,
	})
}

func (s *agentRuntimeStore) MarkRuntimeFailed(id, reason string) error {
	return s.store.MarkAgentRuntimeFailed(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, id, reason)
}

// UpdateState persists the wrapper readiness and exit boundaries observed by
// Boot's lifecycle goroutine.
func (s *agentRuntimeStore) UpdateState(id, state string, pid int) error {
	return s.store.SetAgentRuntimeState(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, id, state, pid)
}

func (s *agentRuntimeStore) SetProviderSessionID(id, providerSessionID string) error {
	return s.store.SetAgentRuntimeProviderSessionID(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, id, providerSessionID)
}

func (s *agentRuntimeStore) GetCheckpoint(id string) (*runtimeagent.RuntimeCheckpoint, error) {
	cp, err := s.store.GetAgentRuntimeCheckpoint(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, id)
	if err != nil {
		return nil, err
	}
	return &runtimeagent.RuntimeCheckpoint{
		ID:                cp.ID,
		ProviderSessionID: cp.ProviderSessionID,
		CapturedAt:        cp.CapturedAt,
	}, nil
}

func (s *agentRuntimeStore) ListRunningRows(ctx context.Context) ([]*runtimeagent.RuntimeRow, error) {
	rows, err := s.store.ListRunningAgentRuntimeRows(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*runtimeagent.RuntimeRow, 0, len(rows))
	for _, r := range rows {
		var parent *string
		if r.ParentSessionID != "" {
			p := r.ParentSessionID
			parent = &p
		}
		out = append(out, &runtimeagent.RuntimeRow{
			ID:              r.ID,
			AgentProfile:    r.AgentProfile,
			Provider:        r.Provider,
			Mode:            r.Mode,
			Workdir:         r.Workdir,
			State:           r.State,
			PID:             r.PID,
			ParentSessionID: parent,
			StartedAt:       r.StartedAt,
			UpdatedAt:       r.UpdatedAt,
			Meta:            r.MetaMap(),
		})
	}
	return out, nil
}

func (s *agentRuntimeStore) MarkRuntimeOrphaned(ctx context.Context, id, reason string) error {
	return s.store.MarkAgentRuntimeOrphaned(ctx, id, reason)
}

// LogEvent satisfies runtimeagent.RuntimeStore's postmortem-logging method
// by delegating straight to the store's shared event_log writer — the same
// sink chat_reflexes.go and recovery_pack_glue.go write through.
func (s *agentRuntimeStore) LogEvent(ctx context.Context, sessionID, eventType, category, detail, metadata string) {
	s.store.LogEvent(ctx, sessionID, eventType, category, detail, metadata)
}

// marshalMeta projects an arbitrary map into a JSON string suitable for
// agent_runtime.meta_json. nil / empty → "{}". Encoding errors collapse to
// "{}" so the runtime row is never refused on metadata-only failures.
func marshalMeta(m map[string]any) string {
	if len(m) == 0 {
		return "{}"
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// --- Telemetry ---

// agentTelemetry forwards PTY restart events to slog. The shape matches
// runtimeagent.Telemetry so the production root can drop in an OTEL-backed
// implementation without re-wiring the deps.
type agentTelemetry struct{}

func (agentTelemetry) RecordPTYRestart(sessionID string, attempt int, prevExit *agentsessions.ExitError) {
	exit := -1
	reason := ""
	if prevExit != nil {
		exit = prevExit.Code
		reason = prevExit.Error()
	}
	slog.Warn("agent_runtime: pty restart",
		"session_id", sessionID,
		"attempt", attempt,
		"prev_exit", exit,
		"reason", reason,
	)
}

// --- Event bridge (Phase 4c.2 + Phase 5) ---

// agentEventBridge translates lib-native event surfaces into nanite SSE
// chat.StreamEvent payloads, broadcasting to the chat session's stream.
//
// Two distinct surfaces feed the bridge:
//
//  1. EventFanout — chan llmtypes.StreamEvent (the legacy stream taxonomy
//     used by Provider.StreamChat). The chat-harness assembles deltas /
//     errors / usage onto this channel; the bridge translates each event
//     to chat.StreamEvent and forwards via streams.BroadcastSessionStreamEvent.
//  2. TypedEventCallback — provider.EventsCallback consuming the typed
//     events.Event taxonomy emitted by CLI adapters. ToolUse / ToolResult
//     drive the per-tool SSE pipeline (closes G-PTY-NO-TOOL-EVENTS).
//
// Per-session routers (Phase 4c.4) let driveBootSession redirect a turn's
// runtime stream events into a dedicated chat-harness consumer chan instead
// of broadcasting them as SSE. When no router is bound for a session id, the
// fanout falls back to the SSE-broadcast path used by spawned agents and
// background tasks.
type agentEventBridge struct {
	streams *StreamManager
	seq     atomic.Uint64
	routers sync.Map // sessionID -> *sessionRouter
}

// sessionRouter wraps a per-turn turnCh with a close-once guard so the bridge
// fanout goroutine, the chat-harness ctx-cancel watcher, and explicit
// SetPerSessionRouter(nil) calls can all race to release the chan without
// double-close panics.
//
// CW-20260824-0001: releasing the chan is not the only thing those goroutines
// race over — they also race the *senders*. `closed` is therefore guarded by
// mu, not by being atomic: every send takes mu for read and re-checks `closed`
// inside the lock, and closeOnce takes it for write. A `closed` field a caller
// can read without the lock is exactly the shape that produced the original
// check-then-act bug, so do not reintroduce one. Send only through send();
// never touch r.ch directly.
type sessionRouter struct {
	// mu orders sends against the close. Read-held for the (non-blocking)
	// send, write-held for the close.
	mu     sync.RWMutex
	ch     chan llmtypes.StreamEvent
	closed bool
}

// send delivers ev to the per-turn chan without ever blocking. It is a no-op
// once the router is closed, and it silently drops ev when the chan buffer is
// full — matching the pre-existing drop semantics at both call sites.
//
// Dropping on a full buffer is deliberate and load-bearing: the runtime's
// fanout goroutine must not stall behind a slow chat-harness consumer. Any
// change that lets this method block is a worse bug than the race it exists
// to close.
//
// Holding mu for read across the select is safe precisely because the select
// has a default arm: it completes in bounded time regardless of how the
// consumer behaves, so it cannot hold off closeOnce's write lock.
func (r *sessionRouter) send(ev llmtypes.StreamEvent) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return
	}
	select {
	case r.ch <- ev:
	default:
	}
}

// closeOnce releases the per-turn chan. Idempotent, and mutually exclusive
// with in-flight send() calls — a send can no longer observe an open router
// and then hand its event to an already-closed chan.
func (r *sessionRouter) closeOnce() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed {
		r.closed = true
		close(r.ch)
	}
}

func (b *agentEventBridge) nextEventID() uint64 {
	return b.seq.Add(1)
}

// SetPerSessionRouter binds (or unbinds) a per-turn chan that the fanout
// goroutine forwards runtime events to. Passing nil unbinds + closes the
// previously bound chan. The bridge owns the close lifecycle so callers don't
// race against in-flight sends.
//
// Phase 4c.4: driveBootSession binds turnCh before SendInput; the bridge
// unbinds + closes when EventDone or EventError flows through, or when the
// chat-harness explicitly clears the router on ctx cancel.
func (b *agentEventBridge) SetPerSessionRouter(sessionID string, ch chan llmtypes.StreamEvent) {
	if ch == nil {
		if v, ok := b.routers.LoadAndDelete(sessionID); ok {
			v.(*sessionRouter).closeOnce()
		}
		return
	}
	router := &sessionRouter{ch: ch}
	if prev, loaded := b.routers.Swap(sessionID, router); loaded {
		// Phase 4c.7 (CW-20260508-0002): session takeover detected. The
		// prior turn's Done hadn't arrived yet (or its ctx-cancel watcher
		// hadn't run) when the chat-harness bound a fresh turnCh — most
		// likely a user-driven retry / new message before the prior
		// generateResponse drained.
		//
		// Conservative semantics: release the stale router (close-once
		// terminates the prior streamLoop), then let the new turn proceed.
		// The Boot'd CLI process is still running; its mid-turn output may
		// interleave with the new turn's response.
		//
		// Known limitation: claude-code's PTY surface doesn't expose a
		// mid-turn interrupt today, so we can't tell the agent to abort
		// the prior turn before delivering new input. Phase 5+ adds
		// Session.Interrupt(ctx) once go-agent-sessions surfaces a
		// non-blocking interrupt (follow-up ticket).
		slog.Warn("agent_event_bridge: session takeover — closing stale per-turn chan",
			"session_id", sessionID)
		prev.(*sessionRouter).closeOnce()
	}
}

// fanout returns the per-session StreamEvent channel. Closes naturally when
// the runtime drains its EventFanout at session-stop; the bridge goroutine
// exits at that point. Buffer is sized for typical burst rates from
// provider.StreamChat (deltas at ~10-30 Hz under load).
//
// When a router is bound for sessionID via SetPerSessionRouter, runtime
// events forward to the bound turnCh instead of broadcasting SSE. Done /
// Error close the turnCh and unbind the router so subsequent inter-turn
// events fall back to SSE broadcast.
func (b *agentEventBridge) fanout(sessionID string) chan<- llmtypes.StreamEvent {
	out := make(chan llmtypes.StreamEvent, 64)
	go func() {
		for ev := range out {
			if v, ok := b.routers.Load(sessionID); ok {
				router := v.(*sessionRouter)
				// send drops on a full buffer to avoid stalling the runtime
				// (the chat-harness consumer is expected to keep up) and is
				// a no-op once the router is closed. Neither case is
				// actionable here, which is why send reports nothing.
				router.send(ev)
				if ev.Type == llmtypes.EventDone || ev.Type == llmtypes.EventError {
					b.routers.CompareAndDelete(sessionID, router)
					router.closeOnce()
				}
				continue
			}
			translated, ok := b.translateStreamEvent(ev)
			if !ok {
				continue
			}
			b.streams.BroadcastSessionStreamEvent(sessionID, translated)
		}
	}()
	return out
}

// translateStreamEvent maps a llmtypes.StreamEvent to a chat.StreamEvent
// suitable for SSE broadcast. Returns (zero, false) when the event has no
// useful FE projection (e.g. EventSessionID is informational only — the
// chat service handles session-id persistence elsewhere).
func (b *agentEventBridge) translateStreamEvent(ev llmtypes.StreamEvent) (chat.StreamEvent, bool) {
	switch ev.Type {
	case llmtypes.EventDelta:
		return chat.StreamEvent{
			EventID: b.nextEventID(),
			Type:    "delta",
			Content: ev.Content,
		}, true
	case llmtypes.EventDone:
		return chat.StreamEvent{
			EventID: b.nextEventID(),
			Type:    "stream_end",
		}, true
	case llmtypes.EventError:
		return chat.StreamEvent{
			EventID: b.nextEventID(),
			Type:    "error",
			Error:   ev.Error,
		}, true
	case llmtypes.EventUsage:
		// Usage rows feed the cost ledger upstream; surface as a stream_end
		// piggyback when present, otherwise drop.
		return chat.StreamEvent{}, false
	case llmtypes.EventThinking:
		if ev.ThinkingBlock == nil {
			return chat.StreamEvent{}, false
		}
		return chat.StreamEvent{
			EventID: b.nextEventID(),
			Type:    "delta",
			Content: ev.ThinkingBlock.Thinking,
			Phase:   "thinking",
		}, true
	case llmtypes.EventToolUse:
		// Tool invocations also surface via TypedEventCallback (richer
		// per-tool SSE); skip here to avoid double-emission.
		return chat.StreamEvent{}, false
	case llmtypes.EventSessionID:
		// Provider session id propagates through Boot's OnSessionID path.
		return chat.StreamEvent{}, false
	default:
		return chat.StreamEvent{}, false
	}
}

// typedCallback returns the per-session callback Boot wires onto
// StartOptions.TypedEventCallback. Drives the per-tool SSE pipeline that
// closes G-PTY-NO-TOOL-EVENTS.
func (b *agentEventBridge) typedCallback(sessionID string) provider.EventsCallback {
	return func(e events.Event) {
		switch evt := e.(type) {
		case events.ToolUse:
			b.streams.BroadcastSessionStreamEvent(sessionID, chat.StreamEvent{
				EventID: b.nextEventID(),
				Type:    "tool_call",
				Tool:    evt.Name,
				ToolID:  evt.ID,
				Detail:  toolDetailFromArgs(evt.Args),
			})
			// CW-20260519-0051: surface a one-line info-card envelope when
			// the CLI agent invokes a structured-input built-in (e.g.
			// AskUserQuestion) whose host-side round-trip isn't wired for
			// CLI-launch sessions. The tool_call SSE above keeps the
			// inspector/devtools view of the call; this emission gives the
			// operator a visible signal that answers won't round-trip, so
			// the previously-silent empty-answer failure mode stops.
			// typedCallback fires only for CLI runtimes, so the gate is
			// the tool-name match (not a separate provider check).
			if isCLIStructuredInputUnsupported(evt.Name) {
				b.emitCLIStructuredInputFallback(sessionID, evt.Name, evt.ID)
			}
		case events.ToolResult:
			b.streams.BroadcastSessionStreamEvent(sessionID, chat.StreamEvent{
				EventID: b.nextEventID(),
				Type:    "tool_result",
				ToolID:  evt.ID,
				Summary: evt.ContentPreview,
				Error: func() string {
					if evt.IsError {
						return evt.ContentPreview
					}
					return ""
				}(),
			})
		case events.Thinking:
			b.streams.BroadcastSessionStreamEvent(sessionID, chat.StreamEvent{
				EventID: b.nextEventID(),
				Type:    "delta",
				Content: evt.Text,
				Phase:   "thinking",
			})
		case events.Error:
			msg := evt.Message
			if msg == "" && evt.Err != nil {
				msg = evt.Err.Error()
			}
			b.streams.BroadcastSessionStreamEvent(sessionID, chat.StreamEvent{
				EventID: b.nextEventID(),
				Type:    "error",
				Error:   msg,
			})
		case events.SubagentSpawn:
			// Synthesized status event for FE awareness; no payload
			// schema yet, so we use the Detail field to surface the tool.
			b.streams.BroadcastSessionStreamEvent(sessionID, chat.StreamEvent{
				EventID: b.nextEventID(),
				Type:    "status",
				Detail:  "subagent: " + evt.Tool,
			})
		case events.Heartbeat:
			// Optional: surface as a no-op presence ping. Drop for now;
			// the FE stalled-stream watchdog uses other signals.
		case events.SessionID:
			// Provider session id flows via Boot.OnSessionID; drop here.
		case events.Delta:
			// Lib-native delta is redundant with llmtypes.StreamEvent
			// EventDelta (which the EventFanout bridge already forwards).
			// Drop to avoid double-emission.
		case events.Usage, events.Done, events.SubprocessStderr:
			// Usage rows feed cost-ledger upstream; Done is a turn boundary;
			// SubprocessStderr is an observability surface, not a chat event.
		}
	}
}

// toolDetailFromArgs picks a short human-readable detail string from a tool
// args map. Today's heuristic: prefer "command" / "path" / "name" / "url"
// fields when present; fall back to the empty string.
func toolDetailFromArgs(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	for _, key := range []string{"command", "path", "file_path", "name", "url", "query"} {
		if v, ok := args[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// _ context.Context retained so the EventSink's ctx parameter is part of
// the package's compile graph even when no in-tree caller threads it
// further (the lib's Manager.emit is the canonical caller).
var _ = context.TODO
