package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	llmtypes "github.com/hollis-labs/go-llm-types"
	conduit "github.com/hollis-labs/tesseract"

	embedcontracts "github.com/hollis-labs/go-embed-contracts"
	"github.com/hollis-labs/go-modelsdev/modelsdev"
	"github.com/hollis-labs/go-providers/provider"
	gosched "github.com/hollis-labs/go-scheduler"
	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/agent/builtin"
	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/background"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/config"
	"github.com/hollis-labs/nanite/internal/contextbroker"
	"github.com/hollis-labs/nanite/internal/coordination"
	"github.com/hollis-labs/nanite/internal/elicitation"
	envelope_render "github.com/hollis-labs/nanite/internal/executor/envelope_render"
	"github.com/hollis-labs/nanite/internal/filter"
	inspectsvc "github.com/hollis-labs/nanite/internal/inspector"
	"github.com/hollis-labs/nanite/internal/lifecycle"
	"github.com/hollis-labs/nanite/internal/loopdetect"
	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/memory"
	"github.com/hollis-labs/nanite/internal/messaging"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/plugin"
	adapterclaude "github.com/hollis-labs/nanite/internal/plugin/builtin/adapter-claude"
	adaptercodex "github.com/hollis-labs/nanite/internal/plugin/builtin/adapter-codex"
	adaptergemini "github.com/hollis-labs/nanite/internal/plugin/builtin/adapter-gemini"
	nanitenative "github.com/hollis-labs/nanite/internal/plugin/builtin/adapter-nanite-native"
	adapteropencode "github.com/hollis-labs/nanite/internal/plugin/builtin/adapter-opencode"
	"github.com/hollis-labs/nanite/internal/providercatalog"
	"github.com/hollis-labs/nanite/internal/recovery/broker"
	"github.com/hollis-labs/nanite/internal/recovery/orphansweep"
	"github.com/hollis-labs/nanite/internal/reminders"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
	"github.com/hollis-labs/nanite/internal/skill"
	"github.com/hollis-labs/nanite/internal/skillvendor"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
	"github.com/hollis-labs/nanite/internal/task"
	"github.com/hollis-labs/nanite/internal/tool"
	"github.com/hollis-labs/nanite/internal/tool/stash"
	"github.com/hollis-labs/nanite/internal/toolclient"
	"github.com/hollis-labs/nanite/internal/worker"
	"github.com/hollis-labs/nanite/internal/workflow"
	"github.com/hollis-labs/nanite/internal/workspace"
	"github.com/hollis-labs/nanite/internal/worktree"
	"github.com/hollis-labs/nanite/pkg/models"
)

// Container holds all service instances and shared subsystems. It is the
// single wiring point — created once in main.go and passed to the API layer.
type Container struct {
	Sessions SessionService
	Agents   AgentService
	// AgentConfig is the shared write path for managed file-backed agent
	// configs (GUI/API/CLI/MCP all route mutations through it).
	AgentConfig *AgentConfigService
	Skills      SkillService
	// SkillVendor is the content-addressed vendored skill store (internal/
	// skillvendor, TASKS/skills/03) that backs the explicit install/sync
	// pipeline (internal/skillinstall, TASKS/skills/04/05 --
	// docs/engineering/architecture/20-skills.md's "The model: DB is an
	// index, a vendored store is content"). Store itself is safe for
	// concurrent use (its own doc comment), but a *skillinstall.Installer
	// is NOT: its State()/Emit are single-call-scoped, mirroring internal/
	// plugin/install's own "build a fresh Installer per invocation"
	// convention (cmd/nanite/plugin_install_flow.go's buildInstaller).
	// Callers (internal/api/skills.go's install/sync handlers, cmd/nanite's
	// `nanite skill install/sync` subcommands) construct a fresh
	// &skillinstall.Installer{Vendor: SkillVendor, Index: Store} per call
	// rather than reusing one instance across concurrent requests. nil
	// when the vendor root failed to initialize at container-build time
	// (surfaced as a 503 by the API layer, not a container-boot failure).
	SkillVendor *skillvendor.Store
	Tools       ToolService
	Chat        ChatService
	Context     ContextService
	Streams     *StreamManager
	Events      EventEmitter
	Providers   *provider.Registry
	Commands    *chat.CommandRegistry
	Plugins     *plugin.Host
	MCP         *mcp.Manager

	// Messaging service — validates, persists, and fans out
	// agent-to-agent messages plus handoff state transitions.
	Messaging *messaging.Service

	// Subagent service — inline spawn / status / cancel for
	// primary-agent-dispatched child agents (T9).
	Subagent *subagent.Service

	// Background service — non-session-bound, async dispatch for
	// long-running work (P9 BackgroundJob, CW-20260420-0016). Result
	// envelopes ride the Messaging service back to the originating
	// session as channel=inbox notifications.
	Background *background.Service

	// Elicitation service — MCP elicitation/create mid-tool user prompts
	// (CW-20260420-0018, G4). Write tools that need user confirmation call
	// into this service; the service pushes an elicitation-prompt envelope
	// to the chat surface and blocks until the user responds or the timeout
	// fires. Nil-safe: tools auto-approve when not wired.
	Elicitation *elicitation.Service

	// Internal todo/plan system.
	Todos TodoService

	// Memory system (embedded Conduit).
	Conduit *conduit.Conduit
	Memory  *memory.Service
	// EmbeddingStatus is the resolved state of the embedder at container build
	// time: "active" | "disabled" | "missing_credentials" | "unreachable".
	// Surfaced by the settings API and consumed by the first-turn warning.
	EmbeddingStatus   string
	EmbeddingProvider string
	EmbeddingModel    string

	// Multi-agent orchestration.
	Coord               coordination.CoordStore
	DurableAgents       DurableAgentService
	DurableWake         DurableAgentWakeService
	DurableAgentRecipes DurableAgentRecipeService
	Tasks               task.Service
	Workers             *worker.Manager
	Worktrees           worktree.Manager

	// Engine is the go-scheduler schedule-tick engine (TASKS/scheduling/
	// 05-engine-wiring-and-full-replace.md) — the full replacement for the
	// old "durable-agent-wake-tick" 2-minute ticker and durable_wake.go's
	// wakeScheduleDue lookback heuristic. Typed as *gosched.Engine (the
	// external go-scheduler package) rather than *internal/scheduler.Engine
	// deliberately: internal/scheduler already imports internal/service
	// (RunnerAdapter decodes into service.DurableAgentWakeRequest/
	// WorkflowLaunchRequest/ToolResult), so a Container field typed against
	// internal/scheduler would create an import cycle. Constructed and
	// Start()ed in cmd/nanite/main.go (mirroring apps/hadron/cmd/hadrond/
	// main.go's construct-at-boot/Start-with-the-rest-of-the-daemon/
	// Stop-on-shutdown lifecycle) once its Store/Runner adapters
	// (internal/scheduler.StoreAdapter, .RunnerAdapter, .RetryingRunner)
	// are available — nil in any Container built directly via NewContainer
	// without that main.go wiring (e.g. most tests), matching
	// AgentCardGenerator/TaskManager's own existing "set post-hoc from
	// main.go, nil-checked at use" pattern on this struct. Exported here
	// (not buried unexported in main.go) so Engine.Status() is reachable
	// from wherever TASKS/scheduling/09-operator-http-api.md's HTTP surface
	// ends up living.
	Engine *gosched.Engine

	// Subsystems exposed for API handlers that need direct access.
	// These will shrink as more domain services are added.
	Store          *store.Store
	ToolClient     *toolclient.ToolClient
	ProcessTracker *chat.ProcessTracker
	Orchestrator   *chat.Orchestrator
	Activity       *chat.ActivityEmitter

	// AppConfig exposes the parsed nanite.yaml app config to handlers that
	// need it (artifact storage root, http caps, etc.). May be nil in
	// lightweight test setups — handlers must nil-check.
	AppConfig *config.TunablesConfig
	// WorkingDir is the project root for project-scoped discovery and
	// operator-managed config writes.
	WorkingDir string
	// ManagedConfigRoot is the on-disk config root used for file-backed
	// operator-managed agents and durable manifests.
	ManagedConfigRoot string

	// Utility provider/model for lightweight calls (autotitle, etc.).
	UtilityProvider string
	UtilityModel    string

	// ModelSelector resolves provider+model for operations (summarization, etc.).
	ModelSelector *provider.StaticModelSelector

	// Permissions is the per-invocation permission engine. nil = permissions disabled.
	Permissions *permission.Engine

	// PathGrants tracks session-scoped explicit-mention path grants for
	// the trust-agent permission redesign (CW-20260430-0009). Always
	// non-nil; per-session state lives inside.
	PathGrants *permission.PathGrants

	// Workflow run store and SSE broadcaster. nil = workflow system disabled.
	RunStore            *workflow.RunStore
	WorkflowBroadcaster *workflow.Broadcaster

	// AdapterRegistry holds registered CLIAgentAdapters for discovery and sandbox ops.
	AdapterRegistry *agent.AdapterRegistry

	// ProviderCatalog is the registry-backed provider/model dropdown
	// catalog (CW-20260526-0001). One entry per provider successfully
	// registered in cmd/nanite/main.go:initProviders. The API layer
	// enumerates this when serving /api/providers, replacing the
	// hand-maintained DB seed as the dropdown's source of truth.
	// nil-safe — when nil, handleListProviders degrades to the DB-only
	// shape so tests that don't wire a catalog still pass.
	ProviderCatalog *providercatalog.Catalog

	// Recovery is the in-process subagent recovery broker (Phase 8/9).
	// Exposed on the container so API handlers can route FE-driven
	// cancel_retry requests back to Broker.Cancel(sessionID, token).
	// nil-safe: when the broker isn't a *broker.Broker (test fakes
	// inject mocks that satisfy agent.RecoveryHooks but not *Broker),
	// the field is left nil and the cancel endpoint returns 503.
	Recovery *broker.Broker

	// Inspector is the I1 per-turn dev-mode aggregator (CW-20260426-0004).
	// nil when developer_mode is false.
	Inspector *inspectsvc.Service

	// LoopDetector is the I2 fingerprint-based loop detector (CW-20260420-0029).
	// Always non-nil; instantiated once at container boot.
	LoopDetector *loopdetect.Detector

	// ReminderEngine is the deterministic trigger engine for agent-set reminders
	// (J11, CW-20260426-0009). Always non-nil; per-session state is keyed
	// by sessionID inside the Engine.
	ReminderEngine *reminders.Engine

	// ReflexEngine is the FU-30 DB-backed agent reflex engine, constructed
	// inside NewContainer and also handed to NewChatService via
	// ChatServiceConfig.ReflexEngine — this field exists so main.go's own
	// later wiring can reuse the exact same instance instead of
	// constructing a second *reflexes.Engine. Loops/
	// 11-loop-event-predicate-trigger.md + 12-loop-run-tick-scheduled-
	// trigger.md integration fix: internal/loop's tick-resume bridge
	// (tick_resume.go) needs this same engine to evaluate resume_loop_run
	// reflexes from the scheduler's loop_run_tick job type. Always non-nil.
	ReflexEngine *reflexes.Engine

	// AgentCardGenerator builds A2A Agent Cards from workflow registry + boot profiles
	// (CW-20260814-0014). Serves /.well-known/agent-card.json.
	AgentCardGenerator *AgentCardGenerator

	// TaskManager routes A2A Task submissions to workflow/durable-agent execution
	// (CW-20260814-0015). Backs the JSON-RPC task methods (submit/get/cancel).
	TaskManager *TaskManager

	// TeamRunLauncher backs POST /api/teams/{id}/launch
	// (TASKS/teams/11-team-run-launch-api.md): resolves a saved Team's Team
	// Slots, compiles its phase sequence, and launches it as an ordinary
	// WorkflowRun (task 08's LaunchTeamRun). Set post-hoc from main.go, nil-
	// checked at use — same "constructed after NewContainer returns"
	// pattern as AgentCardGenerator/TaskManager, above: it depends on the
	// same workflowLauncher/workflowDefinitionsRegistry pair those two are
	// built from in main.go's own boot sequence.
	TeamRunLauncher *TeamRunLauncher

	// TeamRouting installs run-scoped semantic/coordinator-fallback reflexes
	// after TeamRunLauncher has produced the run and its resolved Team Slot
	// members. Set post-hoc from main.go immediately after TeamRunLauncher,
	// and nil-checked by the launch API before any run is created.
	TeamRouting *TeamRoutingService

	// stopModelCatalog cancels the model catalog background refresher.
	stopModelCatalog context.CancelFunc

	// subagentReaper sweeps subagent_runs for timed-out + orphan rows
	// (CW-20260512-0002 b/c). Started during container build; stopped
	// during Shutdown before the DB closes.
	subagentReaper *subagent.Reaper
	// stopSubagentReaper cancels the reaper's bound context (defense in
	// depth — Reaper.Stop alone is enough, but the cancel func unblocks
	// any in-flight ExecContext on shutdown).
	stopSubagentReaper context.CancelFunc

	// runtimeReaper sweeps agent_runtime for rows whose underlying
	// process has died — both pre-restart leftovers (one-shot startup
	// sweep) and mid-run process deaths (periodic). Started during
	// container build; stopped during Shutdown before the DB closes.
	// CW-20260518-0085.
	runtimeReaper *orphansweep.RuntimeReaper
	// stopRuntimeReaper cancels the runtime reaper's bound context.
	stopRuntimeReaper context.CancelFunc

	// shutdownOnce makes Shutdown safe for sequential and concurrent callers.
	shutdownOnce sync.Once
}

// ContainerConfig holds all the external dependencies needed to construct
// a Container. Everything that main.go sets up before wiring goes here.
type ContainerConfig struct {
	Store      *store.Store
	Providers  *provider.Registry
	MCP        *mcp.Manager
	ToolClient *toolclient.ToolClient
	Plugins    *plugin.Host
	AppConfig  *config.TunablesConfig
	WorkingDir string
	// ManagedConfigRoot overrides the default project-local config root
	// used for operator-managed agents and durable manifests.
	ManagedConfigRoot string

	// APIBaseURL is the base URL the local HTTP API server listens on
	// (e.g. "http://127.0.0.1:8090"). Threaded into the agent-runtime
	// boot dir so a CLI-launched chat agent's `nanite mcp` subprocess
	// forwards self-tool calls back to this running harness. Empty leaves
	// CLI launches in local-only self-tool mode.
	APIBaseURL string

	// Optional subsystems — nil-safe.
	Activity     *chat.ActivityEmitter
	OutputFilter *filter.Chain

	// Coordination store for multi-agent orchestration. nil = disabled.
	CoordStore coordination.CoordStore

	// Worktree manager for worker filesystem isolation. nil = disabled.
	Worktrees worktree.Manager

	// Worker concurrency limit (0 = default 5).
	MaxConcurrentWorkers int

	// Utility provider/model — read from DB or env by main.go.
	UtilityProvider string
	UtilityModel    string

	// CLI process concurrency limit (0 = unlimited).
	MaxCLIProcesses int

	// CLIAdapters is the slice of go-providers CLI adapters the agent-runtime
	// composition root resolves by Name(). Threaded from main.go so the
	// dev-mode `--dangerously-skip-permissions` wrapping (and other
	// per-process customizations) is preserved. nil = the runtime has no
	// adapters and Boot fails for any provider; callers should populate
	// at least claude/codex/opencode.
	CLIAdapters []provider.CLIAdapter

	// ProviderCatalog is the registry-backed dropdown catalog
	// (CW-20260526-0001). nil-safe — when nil, handleListProviders falls
	// back to the DB-only shape so tests without explicit wiring work.
	ProviderCatalog *providercatalog.Catalog

	// DurableAgentRecipeCatalogPaths is the ordered set of local recipe
	// catalog files or directories loaded at startup. Configured recipes
	// override built-ins by ID; duplicate configured IDs are rejected.
	DurableAgentRecipeCatalogPaths []string

	// DevToolsAllowedPaths is the binary-scoped allow-list configured via
	// nanite.yaml `dev_tools_allowed_paths` (see cmd/nanite/main.go
	// resolveDevToolsAllowedPaths). Threaded onto the ContextClient so the
	// per-session SlotPermissions summary (CW-20260512-0118) surfaces the
	// baseline READ roots the agent operates against. Empty / nil leaves
	// the "workspace allow-list" section out of the rendered summary.
	DevToolsAllowedPaths []string
}

func newRuntimeAdapterRegistry() *agent.AdapterRegistry {
	reg := agent.NewAdapterRegistry()
	reg.Register(adapterclaude.New().Adapter())
	reg.Register(adaptercodex.New().Adapter())
	reg.Register(adaptergemini.New().Adapter())
	reg.Register(adapteropencode.New().Adapter())
	reg.Register(nanitenative.New().Adapter())
	return reg
}

// NewContainer wires all services together and returns a ready Container.
func NewContainer(cfg ContainerConfig) (*Container, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("service.NewContainer: Store is required")
	}
	if cfg.Providers == nil {
		return nil, fmt.Errorf("service.NewContainer: Providers is required")
	}
	workingDir := cfg.WorkingDir
	if workingDir == "" {
		workingDir = "."
	}
	managedConfigRoot := cfg.ManagedConfigRoot
	if managedConfigRoot == "" {
		managedConfigRoot = filepath.Join(workingDir, ".nanite")
	}

	// --- Foundation (Wave 0) ---

	// Event emitter: fans out to activity + plugin sinks. Plugin dispatch and
	// direct chat work deliberately share one lifecycle/drain boundary.
	chatLifecycle := lifecycle.NewManager("service.chat")
	chatLifecycleCommitted := false
	defer func() {
		if !chatLifecycleCommitted {
			_ = chatLifecycle.Shutdown(time.Second)
		}
	}()
	var pluginSink PluginEventSink
	if cfg.Plugins != nil {
		pluginSink = cfg.Plugins
	}
	events := NewCompositeEmitter(cfg.Activity, pluginSink, chatLifecycle)

	// --- Domain services (Wave 1) ---

	sessions := NewSessionService(SessionServiceDeps{
		Sessions:    cfg.Store,
		Writer:      cfg.Store,
		Agents:      cfg.Store,
		AgentReader: cfg.Store,
		Settings:    cfg.Store,
		Events:      events,
	})

	// Adapter registry — adapters self-register via plugin loading.
	// For now, the registry is created and passed through; adapter plugins
	// will be wired when the plugin host supports adapter registration.
	adapterRegistry := newRuntimeAdapterRegistry()

	// Ensure ~/.nanite/agents/ exists on first run (J6, CW-20260421-0006).
	// Silently continue on error — a missing home dir is non-fatal at startup.
	if err := agent.EnsureHomeDirs(""); err != nil {
		slog.Warn("service container: ensure agent home dirs", "err", err)
	}

	// Discover agent definitions from the remaining tiers (CLI --agent flag,
	// currently unreachable, plus adapter-discovered). As of
	// TASKS/phase-2/06-cut-nanite-native-adapter-agent-sync.md every
	// registered CLIAgentAdapter (including nanite-native, the last one
	// that used to produce real results here) has a no-op Discover() — see
	// internal/agent/discovery.go's DiscoverOptions.Adapters doc comment
	// for why adapterRegistry is still passed through (it's also used below
	// for PopulateAllSandboxes/SyncAllProjectRoots). The project/user/plugin
	// directory-scan tiers were cut in full by TASKS/phase-1/08 — no
	// PluginsDir/HomeDir wiring is needed anymore.
	agentDefs, err := agent.Discover(agent.DiscoverOptions{
		WorkingDir: workingDir,
		Adapters:   adapterRegistry,
	})
	if err != nil {
		slog.Warn("service container: agent discovery", "err", err)
	}
	// CW-20260512-0111: append all internal agent profiles from
	// internal/agent/builtin/profiles/*.md. Each definition is stamped
	// Source="internal" so that the AutoIngestAgents pass writes
	// agent_profiles rows whose `source` column matches the Wave 2 cleanup
	// keep-list (agent_profiles WHERE source != 'internal' will be wiped
	// by CW-20260512-0112). Lower discovery priority than user / project
	// agents so user overrides by-slug still win.
	//
	// Per profiles.go contract: parse failures here are build-level bugs
	// (the files are embedded at compile time). Fail-fast at boot so an
	// unparseable internal profile surfaces immediately instead of being
	// silently absent from the agent registry.
	internalDefs, err := builtin.InternalProfiles()
	if err != nil {
		return nil, fmt.Errorf("service.NewContainer: load internal agent profiles: %w", err)
	}
	agentDefs = append(agentDefs, internalDefs...)
	// POC CW-20260420-0047: mux orchestrator agent profile.
	// MuxOrchestratorAgent returns (nil, nil) in non-devmode builds; guard
	// the nil-def case so we don't append a nil pointer to agentDefs.
	if muxDef, muxErr := builtin.MuxOrchestratorAgent(); muxErr != nil {
		slog.Warn("service container: built-in mux orchestrator agent", "err", muxErr)
	} else if muxDef != nil {
		agentDefs = append(agentDefs, muxDef)
	}
	slog.Info("service container: discovered file-based agents", "count", len(agentDefs))

	// Source classification roots: the project managed config root and the
	// user nanite data dir are writable-in-place; embedded internal and
	// plugin/vendor agents are read-only. Shared by the boot reconcile pass,
	// the AgentConfigService write contract, and the API editability gate.
	userDataDir := ""
	if home, herr := os.UserHomeDir(); herr == nil && home != "" {
		userDataDir = filepath.Join(home, ".nanite")
	}
	agentClassification := agent.NewClassification(managedConfigRoot, userDataDir)

	// Boot reconcile: durably stamp a UUID identity into writable managed
	// agent files (adopt the existing projection's id, else mint), so the
	// subsequent ingest uses it as the DB row PK and FK children resolve.
	// Idempotent — already-stamped files are skipped. Runs before ingest.
	ReconcileManagedAgentIDs(cfg.Store, agentDefs, agentClassification)

	// J7 (CW-20260421-0011): auto-ingest discovered agent definitions into DB.
	// File → parse → DB upsert. H1 trust: user/plugin sources → untrusted tier.
	// Built-in definitions (Source != "user"/"plugin") retain 'normal' tier.
	// Errors per-def are logged non-fatal via AutoIngestAgents.
	//
	// knownTools (CW-20260815-0013): built from the already-wired
	// ToolClient (main.go's initMCP + MCP AutoDiscover both run before
	// NewContainer is called) so AutoIngestAgents can flag a profile's
	// roleTools:/tools: entries that don't match any registered tool name.
	// nil when no ToolClient is wired — validation is skipped, not
	// treated as "nothing is known" (which would flag every entry).
	var knownTools map[string]bool
	if cfg.ToolClient != nil {
		catalog := cfg.ToolClient.ListTools()
		// request_tools has no registration anywhere in the builtin/MCP
		// catalog ToolClient.ListTools() draws from — it's a meta-tool
		// synthesized ad hoc by SelectForAgent (progressive discovery and
		// the always_included escape hatch, service/tool.go), with its
		// own dedicated execution path (ToolService.HandleRequestTools),
		// not routed through MCPManager/Builtins like an ordinary tool.
		// Append it here so known_tools' live-sync (below) sees it as a
		// real, permanently-available catalog entry instead of marking
		// its migration-seeded always_included row 'unavailable' on the
		// very first boot (TASKS/phase-4/05-wire-select-for-agent-to-
		// read-agent-tools.md — caught by that task's own zero-grant
		// escape-hatch test).
		catalog = append(catalog, toolclient.RequestToolsMetaTool())
		knownTools = make(map[string]bool, len(catalog))
		for _, t := range catalog {
			knownTools[t.Name] = true
		}

		// Phase 1 item 04 (TASKS/phase-1/04-add-known-tools-and-agent-tools-fk.md):
		// live-sync the known_tools global catalog against this same
		// builtins+MCP catalog, before AutoIngestAgents runs below (its
		// seedRoleToolsFromIngest call needs known_tools rows to already
		// exist so it can resolve roleTools: names to real grants).
		syncResult := SyncKnownTools(context.Background(), cfg.Store, catalog, cfg.ToolClient.IsBuiltinTool)
		slog.Info("service container: synced known_tools catalog",
			"upserted", syncResult.Upserted, "marked_unavailable", syncResult.MarkedUnavailable)
	}
	if n := AutoIngestAgents(cfg.Store, agentDefs, knownTools); n > 0 {
		slog.Info("service container: auto-ingested agents into DB", "count", n)
	}

	// Phase 1 item 04: one-time-per-agent carry-over of
	// tools:/tool_permissions:/role_tools:' CURRENT values into real
	// agent_tools grants. Runs after AutoIngestAgents so brand-new agents
	// ingested this same boot are covered too; see
	// BackfillAgentToolsFromLegacyColumns' doc comment for why this is
	// guarded to run at most once per agent, ever.
	if n, err := BackfillAgentToolsFromLegacyColumns(context.Background(), cfg.Store); err != nil {
		slog.Warn("service container: backfill agent_tools from legacy columns", "err", err)
	} else if n > 0 {
		slog.Info("service container: backfilled agent_tools from legacy columns", "grants", n)
	}

	agents := NewAgentService(AgentServiceConfig{
		Agents:   cfg.Store,
		Writers:  cfg.Store,
		Settings: cfg.Store,
		Events:   events,
	})

	// Shared managed-agent write service. GUI/API/CLI/MCP route all managed
	// config mutations through this one path (validate → atomic file write
	// → DB upsert/reindex → event). TASKS/adhoc/01-eliminate-file-based-
	// agent-runtime.md dropped the live in-memory-registry reload step
	// (AgentRegistryReloader/ReloadFileAgent/RemoveFileAgent) — agentServiceImpl
	// now reads straight from the DB on every call, so the row writeManaged
	// already upserted synchronously is immediately visible with no reload
	// needed.
	agentConfig := NewAgentConfigService(cfg.Store, agentClassification, managedConfigRoot, nil)

	// agent_permissions.go (newFileAgentPermissionResolver) and
	// ToolClient.PermissionResolver/GetPermissions/CheckPermission/
	// ToolPermissions/ParsePermissions were deleted outright by
	// TASKS/adhoc/02-remove-tool-permissions-collapse-to-agent-tools.md --
	// no agent ID is ever "file-<slug>"-shaped anymore (TASKS/adhoc/01), so
	// there was nothing left for that resolver to resolve, and every other
	// tool_permissions consumer had a real agent_tools-backed replacement.
	// agent_tools (+ the known_tools.always_included escape hatch) is now
	// the sole tool-permission gate everywhere, including inside
	// ToolClient.CallTool's own execution-time backstop.

	// Messaging service. Uses the AgentService as its resolver -- every
	// agent is DB-backed now (TASKS/adhoc/01-eliminate-file-based-agent-
	// runtime.md). Takes the SQLite-backed messaging Store plus the
	// underlying *sql.DB so
	// handoff transactions (which span session_handoffs +
	// session_agents) can run as a single txn.
	msgStore := messaging.NewSQLiteStore(cfg.Store.DB)
	// cfg.Store satisfies messaging.AgentRegistrar via its CreateAgent
	// method — enables T6 auto-register-on-first-send.
	messagingSvc := messaging.NewService(msgStore, cfg.Store.DB, agents, cfg.Store)
	// Wire the session_events writer into the composite emitter so
	// EmitPreCompact / EmitPostCompact persist context_pre_compact /
	// context_post_compact rows for P8 part C (CW-20260426-0002).
	events.WithSessionWriter(messagingSvc)
	slog.Info("service container: messaging service enabled")

	// Ensure ~/.nanite/skills/ exists on first run (J6, CW-20260421-0006).
	// Silently continue on error — a missing home dir is non-fatal at startup.
	if err := skill.EnsureHomeDirs(""); err != nil {
		slog.Warn("service container: ensure skill home dirs", "err", err)
	}

	// TASKS/skills/01: file-based skill discovery, boot-time builtin loading,
	// and the AutoIngestSkills boot pass are all cut in full — see
	// docs/engineering/architecture/20-skills.md's "Migration: clean slate,
	// no carried-forward content" section. skill.Discover/DiscoverOptions and
	// internal/skill/builtin no longer exist; there is no file-based skill
	// source to feed SkillServiceConfig.FileSkills until the install/sync
	// pipeline (TASKS/skills/04-05) lands.
	skills := NewSkillService(SkillServiceConfig{
		Skills:     cfg.Store,
		FileSkills: nil,
	})

	// TASKS/skills/05: the content-addressed vendored skill store backing
	// the explicit install/sync pipeline. Rooted at AppConfig.Skills.
	// VendorStorageDir, mirroring ArtifactsConfig.StorageDir's own
	// load/default/override pattern (cmd/nanite/main.go's initMCP). A
	// construction failure (e.g. an unwritable root) degrades to a nil
	// SkillVendor rather than failing container boot entirely -- install/
	// sync becomes unavailable (503 at the API layer) rather than taking
	// the whole service down over a skills-only storage path problem.
	skillVendorRoot := config.DefaultAppConfig().Skills.VendorStorageDir
	if cfg.AppConfig != nil && cfg.AppConfig.Skills.VendorStorageDir != "" {
		skillVendorRoot = cfg.AppConfig.Skills.VendorStorageDir
	}
	skillVendor, skillVendorErr := skillvendor.New(skillVendorRoot)
	if skillVendorErr != nil {
		slog.Warn("service container: skill vendor store init failed", "root", skillVendorRoot, "err", skillVendorErr)
		skillVendor = nil
	}

	// Internal todo/plan service (SQLite-backed, always available).
	todos := NewTodoService(TodoServiceConfig{
		Todos: cfg.Store,
		Plans: cfg.Store,
	})
	slog.Info("service container: todo/plan service enabled")

	// Task tracking service — requires coordination store.
	var tasks task.Service
	if cfg.CoordStore != nil && cfg.CoordStore.Available() {
		local := task.NewLocalBackend(cfg.CoordStore, &task.SQLiteSnapshot{DB: cfg.Store.DB})
		tasks = task.NewService(task.ServiceConfig{
			Local: local,
			Settings: func() string {
				us, err := cfg.Store.GetUserSettings(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */)
				if err != nil {
					return task.BackendLocal
				}
				return us.TaskBackend
			},
		})
		slog.Info("service container: task tracking enabled (badger-backed)")
	} else {
		slog.Info("service container: task tracking disabled (no coordination store)")
	}

	// Embedded Conduit instance for memory storage.
	var conduitInstance *conduit.Conduit
	var memorySvc *memory.Service
	var embeddingStatus string
	var embeddingProviderID, embeddingModel string
	{
		// CW-20260517-0061: Tesseract migrated to go-apppaths
		// (CW-20260517-0066) — its context.db moved to
		// ~/.local/share/tesseract/workspaces/default/main.db and its
		// records/ tree to ~/.local/state/tesseract/records. nanite resolves
		// those migrated paths directly via paths.Resolve("tesseract") and
		// passes them through the additive conduit.Config.DBPath/RecordsDir
		// override fields (added in the same Tesseract PR), so the embedded
		// memory store points at the migrated DB without relying on the
		// Phase 2 ~/.tesseract → XDG compat symlink.
		//
		// RootDir is still required by conduit.Config; it stays at the legacy
		// ~/.conduit dotdir purely as the base the library would derive from
		// when DBPath/RecordsDir are unset — here both ARE set, so RootDir is
		// inert for path derivation. (Pre-migration, RootDir-relative
		// derivation also pointed at the now-retired ~/.conduit/data/records,
		// which is exactly why the explicit override matters.) The ~/.conduit
		// dotdir evacuation is a separate follow-up.
		homeDir, _ := os.UserHomeDir()
		conduitRoot := filepath.Join(homeDir, ".conduit")

		var conduitDBPath, conduitRecordsDir string
		if tessLayout, tessLayoutErr := config.ResolveTesseractLayout(); tessLayoutErr != nil {
			slog.Warn("service container: resolve tesseract layout failed; embedded memory falls back to RootDir derivation",
				"err", tessLayoutErr)
		} else {
			conduitDBPath = tessLayout.MainDB()
			conduitRecordsDir = filepath.Join(tessLayout.StateDir(), "records")
			slog.Info("service container: tesseract memory paths resolved (go-apppaths)",
				"db", conduitDBPath, "records", conduitRecordsDir)
		}

		// Embedder selection: resolve from user settings via selectEmbedder.
		// No configured embedder = no-op (similarity recall unavailable).
		var embedder embedcontracts.Embedder
		us, usErr := cfg.Store.GetUserSettings(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */)
		if usErr != nil {
			slog.Warn("service container: user_settings read failed; embedder disabled", "err", usErr)
			embeddingStatus = EmbeddingStatusDisabled
		} else {
			embedder, embeddingModel, embeddingStatus = SelectEmbedder(
				context.Background(),
				EmbedderSettings{
					Mode:     us.EmbeddingMode,
					Provider: us.EmbeddingProvider,
					Model:    us.EmbeddingModel,
				},
				DefaultEmbedderSelectDeps(),
			)
			embeddingProviderID = us.EmbeddingProvider
		}
		slog.Info("service container: embedder status",
			"status", embeddingStatus,
			"provider", embeddingProviderID,
			"model", embeddingModel,
		)

		var conduitOpts []conduit.Option
		if embedder != nil {
			conduitOpts = append(conduitOpts, conduit.WithEmbedder(embedder))
			conduitOpts = append(conduitOpts, conduit.WithEmbeddingModel(embeddingModel))
		}
		// Bridge Conduit's printf-style logger callback into slog. Conduit
		// formats its own messages, so we emit them verbatim at Info level —
		// structured attrs aren't available on this callback boundary.
		conduitOpts = append(conduitOpts, conduit.WithLogger(func(format string, args ...any) {
			slog.Info("conduit: " + fmt.Sprintf(format, args...))
		}))

		var conduitErr error
		conduitInstance, conduitErr = conduit.Open(context.Background(), conduit.Config{
			RootDir:    conduitRoot,
			DBPath:     conduitDBPath,     // migrated tesseract context.db (empty → RootDir derivation)
			RecordsDir: conduitRecordsDir, // migrated tesseract records/ (empty → RootDir derivation)
		}, conduitOpts...)
		if conduitErr != nil {
			slog.Warn("service container: failed to open Conduit", "err", conduitErr)
		} else {
			memorySvc = memory.NewService(conduitInstance.MemoryStore())
			slog.Info("service container: memory service enabled (embedded Conduit)")
		}
	}

	var agentReader AgentReader = cfg.Store
	tools := NewToolService(cfg.ToolClient, cfg.MCP, agentReader)
	if impl, ok := tools.(*toolServiceImpl); ok {
		// C2 (CW-20260429-0008): wire the LLM-augmented repair pipeline.
		// The repair model is selectable via NANITE_REPAIR_MODEL; the
		// provider is picked from the user's utility provider (which
		// is what already runs cheap classifier / summarizer calls).
		// Nil-safe: when the provider is missing, the repair config is
		// left nil and Execute returns the C1 envelope directly.
		if rc := buildRepairConfig(cfg.Providers, cfg.Store, cfg.UtilityProvider); rc != nil {
			impl.SetRepairConfig(rc)
		}
	}

	// --- Orchestration (Wave 2) ---

	streams := NewStreamManager()
	if cfg.AppConfig != nil && cfg.AppConfig.Presence.CLIActiveThrottleSeconds > 0 {
		streams.CLIActiveThrottleInterval = time.Duration(cfg.AppConfig.Presence.CLIActiveThrottleSeconds) * time.Second
	}

	// T7: wire the messaging → SSE notification bridge. Now that
	// streams exists, every SendMessage broadcasts a
	// message_received StreamEvent into the target session's active
	// SSE stream. Nil-safe — when no stream is attached the broadcast
	// drops silently, which is the intended MVP behavior.
	messagingSvc.SetNotificationSink(&messagingStreamSink{streams: streams})

	// Trust-agent path grants (CW-20260430-0009). Session-scoped store
	// for explicit-mention auto-grants registered at user-message ingest.
	// Threaded onto the tool-execution context so dev_tools resolveAllowed
	// can fall back to it when the static AllowedPaths list rejects.
	// Constructed early so the ContextClient and the chat service share
	// the same instance — the ContextClient reads it via
	// AssembleSlotSources to render the SlotPermissions summary
	// (CW-20260512-0118), and dev_tools reads it via the per-call
	// context (permission.WithPathGrants) when enforcing the gate.
	pathGrants := permission.NewPathGrants()

	contextClient := chat.NewContextClient(cfg.Store)
	contextClient.PathGrants = pathGrants
	contextClient.DevToolsAllowedPaths = cfg.DevToolsAllowedPaths

	// CW-20260512-0116 (SP-20260512-0009 W6): wire the AGENTS.md walk-up
	// for SlotWorkspace. The cache is process-lifetime, concurrency-safe,
	// and keyed on (session_id, working_dir) with per-file mtime
	// invalidation. The resolver maps a session to its on-disk
	// working_dir via Store.GetProject (primary-key fetch) when ProjectID
	// is set. Phase 0 item 20 (retire workspaces): this used to also
	// guard on session.WorkspaceID == project.WorkspaceID to defend
	// against cross-workspace project leaks — both fields are gone now
	// that `projects` is a flat table with no workspace nesting, so the
	// guard is dropped along with them.
	//
	// This differs from internal/api/autocomplete.go::resolveRoot in
	// that an empty ProjectID returns empty (and the slot ships empty
	// via the assembly decider's skipped_no_content path) rather than
	// falling back to the first project + cwd. Autocomplete needs *some*
	// root to scan for completion candidates, so its fallbacks are a UX
	// safety net. SlotWorkspace explicitly represents "this session's
	// project conventions" — falling back to cwd or an arbitrary sibling
	// project would inject the wrong project's AGENTS.md into the prompt,
	// which is worse than empty.
	contextClient.WorkspaceCache = workspace.NewCache()
	contextClient.WorkingDirForSession = func(session *store.Session) (string, error) {
		if session == nil {
			return "", nil
		}
		if session.ProjectID == "" {
			return "", nil
		}
		// Primary-key fetch instead of the full project list — this
		// resolver fires on every AssembleSlotSources call (per turn),
		// so an O(N) scan would scale poorly as the project list grows.
		project, err := cfg.Store.GetProject(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, session.ProjectID)
		if err != nil {
			// "Project not found" is non-fatal for slot assembly: the
			// slot ships empty rather than failing the turn. Other
			// errors (DB unavailable etc.) bubble up so the caller can
			// log + fall back.
			if errors.Is(err, sql.ErrNoRows) {
				return "", nil
			}
			return "", err
		}
		if project == nil || project.RepoPath == "" {
			return "", nil
		}
		return project.RepoPath, nil
	}

	// --- ContextBroker: universal context retrieval ---
	{
		var sources []contextbroker.ContextSource

		// MemorySource — requires memory service (activation ranking by default).
		if memorySvc != nil {
			sources = append(sources, contextbroker.NewMemorySource(memorySvc))
		}

		// ConduitSource — requires MCP manager (calls Conduit tools).
		if cfg.MCP != nil {
			sources = append(sources, contextbroker.NewConduitSource(cfg.MCP))
		}

		// PCCSource — reads filesystem, always available.
		sources = append(sources, contextbroker.NewPCCSource(".nanite/pcc/global"))

		// SessionSource — reads message history, always available.
		sources = append(sources, contextbroker.NewSessionSource(func(sessionID string, limit int) ([]contextbroker.MessageSummary, error) {
			msgs, err := cfg.Store.ListMessages(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, sessionID, limit)
			if err != nil {
				return nil, err
			}
			out := make([]contextbroker.MessageSummary, len(msgs))
			for i, m := range msgs {
				out[i] = contextbroker.MessageSummary{Role: m.Role, Content: m.Content}
			}
			return out, nil
		}))

		broker := contextbroker.New(contextbroker.DefaultBudget(), sources...)
		contextClient.ContextBroker = broker
		slog.Info("service container: context broker enabled", "sources", len(sources))
	}

	// S3b tool-slot cache pipeline: stash manager + intent classifier. The
	// classifier's LLM fallback layer reuses the summarizer provider/model
	// unless UserSettings pins a different one.
	stashManager := stash.NewManager(stash.BuiltinCategorizer())
	overrideStore := newToolCacheOverrideStore()
	classifier := buildToolIntentClassifier(cfg.Providers, cfg.Store)

	// SP-20260512-0008 W2C (CW-20260512-0110): wire the artifact-store-
	// backed slot stasher so the Context Broker can substitute pointer
	// envelopes for oversized slot content. Errors here are non-fatal —
	// the decider's NopStasher fallback ships content inline if the
	// stasher can't be constructed.
	slotStasher, err := NewArtifactStasher(ArtifactStasherConfig{
		Store:     cfg.Store,
		AppConfig: cfg.AppConfig,
	})
	if err != nil {
		slog.Warn("service container: artifact stasher unavailable; oversized slots will ship inline",
			"err", err)
		slotStasher = nil
	}

	ctxService := NewContextService(ContextServiceConfig{
		Client:       contextClient,
		StashManager: stashManager,
		Classifier:   classifier,
		Overrides:    overrideStore,
		SettingsFunc: func() *store.UserSettings {
			us, err := cfg.Store.GetUserSettings(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */)
			if err != nil {
				return nil
			}
			return us
		},
		SlotStasher: slotStasher,
	})

	// Command registry.
	commands := chat.NewCommandRegistry()
	commands.RegisterServerCommands(cfg.Store, cfg.Providers)
	RegisterToolCacheCommand(commands, overrideStore)

	// Register file-based skills as slash commands.
	RegisterSkillCommands(commands, skills)

	// Process tracker.
	processTracker := chat.NewProcessTracker()
	if cfg.MaxCLIProcesses > 0 {
		processTracker.MaxProcesses = cfg.MaxCLIProcesses
	}
	// 0 = unlimited (leave at ProcessTracker's zero-value default)

	orchestrator := chat.NewOrchestrator(cfg.Providers, cfg.MCP)

	// Permission engine. Yolo mode when developer_mode=1 so dev-mode sessions
	// never hit approval prompts.
	permissions := permission.NewEngine(permission.ModeDefault, nil)
	if us, err := cfg.Store.GetUserSettings(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */); err == nil && us.DeveloperMode {
		permissions.SetMode(permission.ModeYolo)
	}

	// (pathGrants is constructed earlier and shared with ContextClient so the
	// SlotPermissions summary renders against the same grant store the
	// dev_tools gate consults. See CW-20260512-0118.)

	// Model catalog — fetches pricing and context-window data from models.dev.
	// After each successful fetch the OnRefresh hook pushes the data into the
	// pkg/models overlay so all callers of Pricing/MaxOutputFor/ContextWindowFor
	// automatically see live values without threading the catalog through the
	// stack, then materialises the same overlay values into the `models` DB
	// table (store.SyncModelsFromRegistry) so agents.model_id has a real,
	// current row to FK against — see Phase 1 #06.
	catalogCtx, stopCatalog := context.WithCancel(context.Background())
	modelCatalog := modelsdev.New(modelsdev.WithOnRefresh(func(c *modelsdev.Client) {
		syncCatalogToRegistry(c, cfg.Store)
	}))
	// Sync from disk cache immediately (warm cache path) so the registry is
	// enriched before accepting traffic even when no network fetch is needed.
	syncCatalogToRegistry(modelCatalog, cfg.Store)
	modelCatalog.StartRefresher(catalogCtx)

	// I1 (CW-20260426-0004): inspector service — dev-mode only.
	// Created unconditionally but only populated/queried when developer_mode=true.
	var inspectorSvc *inspectsvc.Service
	if us, err := cfg.Store.GetUserSettings(catalogCtx); err == nil && us.DeveloperMode {
		inspectorSvc = inspectsvc.NewService()
		slog.Info("service container: inspector service enabled (developer_mode=true)")
	}

	// I2 (CW-20260420-0029): loop detector — always-on, per-session windows.
	// Instantiated once at container boot; shared across all sessions.
	loopDetector := loopdetect.New()
	slog.Info("service container: loop detector enabled (I2, fingerprint-based)")

	// J11 (CW-20260426-0009): reminder engine — deterministic trigger evaluation.
	// A single engine is shared across sessions; per-session state lives inside
	// the engine (keyed by sessionID / reminderID). Always instantiated so the
	// SelfToolsTransport can register creation turns even before the first eval.
	reminderEngine := reminders.NewEngine(cfg.Store)
	slog.Info("service container: reminder engine enabled (J11, CW-20260426-0009)")

	// FU-30 reflex engine. Built before the chat service so per-turn
	// generation can evaluate DB-backed agent reflexes and inject just-in-time
	// reminders / forced tool choices. Plugin hooks (nil-safe) let plugins
	// rewrite reflex state/actions; the Halt executor marks the session
	// halted + logs the event when a reflex resolves to halt_session.
	reflexEngine := reflexes.NewEngine(cfg.Store, slog.Default())
	if cfg.Plugins != nil {
		reflexEngine.SetPluginHooks(cfg.Plugins)
	}
	reflexEngine.Executor.Halt = func(ctx context.Context, sessionID, reason string, evidence map[string]interface{}) error {
		// Outcome bookkeeping must survive cancellation of the halt operation it records.
		persistCtx := context.WithoutCancel(ctx)
		if err := cfg.Store.MarkSessionHalted(persistCtx, sessionID, reason); err != nil {
			return err
		}
		metaBlob, _ := json.Marshal(map[string]interface{}{
			"detector": "reflex",
			"reason":   reason,
			"evidence": evidence,
		})
		cfg.Store.LogEvent(persistCtx, sessionID, "session_halted", "reflex", "reflex-fired halt", string(metaBlob))
		return nil
	}
	// TASKS/scheduling/07-wire-add-schedule-reflex.md: closes
	// CW-20260819-0006's loop -- a firing action_kind='add_schedule'
	// reflex now genuinely inserts an agent_schedules row (via
	// newReflexScheduleHook/buildReflexAgentSchedule, internal/service/
	// reflex_schedule_hook.go) instead of only being staged in
	// AppliedActions/event_log. Wired here, alongside Executor.Halt above,
	// per docs/engineering/architecture/12-scheduling.md's "Producers" #3.
	reflexEngine.Executor.Schedule = NewReflexScheduleHook(cfg.Store)
	if n, err := reflexes.SeedBaseReflexes(context.Background(), cfg.Store, slog.Default()); err != nil {
		slog.Warn("service container: reflex base-seed", "err", err)
	} else if n > 0 {
		slog.Info("service container: seeded base reflexes", "count", n)
	}
	// CW-20260816-0023: Loom Curator/Weaver pilot reflex pair
	// (check_before_answer, capture_on_discovery). AgentID-scoped, so it
	// must run after agent.Discover + ReconcileManagedAgentIDs +
	// AutoIngestAgents (above, ~line 399-469) have resolved Curator's/
	// Weaver's real agent_profiles.id from .nanite/agents/*.md — this is
	// the same "earliest point the ID is known" boot spot
	// syncManagedDurableAgentConfig uses for CW-20260816-0021's schedule
	// seeding. A seed whose target isn't ingested yet is skipped with a
	// warning (not fatal) and picked up on a later boot once it is.
	if n, err := reflexes.SeedAgentReflexesBySlug(context.Background(), cfg.Store, reflexes.LoomPilotReflexSeeds(), slog.Default()); err != nil {
		slog.Warn("service container: loom pilot reflex seed", "err", err)
	} else if n > 0 {
		slog.Info("service container: seeded loom pilot reflexes", "count", n)
	}

	// Phase 4c.1 (CW-20260508-0002): construct *agent.Dependencies +
	// agentsessions.Manager once, after the core deps (store, pathGrants,
	// streams) exist. Threaded through ChatServiceConfig so HandleMessage
	// + subagent runner + future background dispatcher reuse the singleton.
	// CLIAdapters fallback covers older main.go versions until the slice is
	// populated; agent.Boot fails clean when no adapter matches.
	cliAdapters := cfg.CLIAdapters
	if len(cliAdapters) == 0 {
		cliAdapters = []provider.CLIAdapter{
			provider.NewClaudeAdapter(),
			provider.NewCodexAdapter(),
			provider.NewOpencodeAdapter(),
		}
	}
	agentDepsBundle, agentDepsErr := BuildAgentDependencies(AgentDepsConfig{
		Store:            cfg.Store,
		PathGrants:       pathGrants,
		Streams:          streams,
		CLIAdapters:      cliAdapters,
		DBPath:           cfg.Store.DBPath(catalogCtx),
		MCP:              cfg.MCP,
		Providers:        cfg.Providers,
		APIBaseURL:       cfg.APIBaseURL,
		CLIWritableRoots: cfg.DevToolsAllowedPaths,
		// TASKS/skills/10: threads the same vendored skill store
		// constructed above (skillVendor, possibly nil on init failure —
		// see its own comment) onto runtimeagent.Dependencies.SkillVendor
		// so CLI-hosted agents can plant their granted skills.
		SkillVendor: skillVendor,
	})
	if agentDepsErr != nil {
		stopCatalog()
		return nil, fmt.Errorf("service container: build agent dependencies: %w", agentDepsErr)
	}
	agentDeps := agentDepsBundle.Deps
	agentManager := agentDepsBundle.Manager
	agentBridge := agentDepsBundle.Bridge
	agentBootDir := agentDepsBundle.BootDirAdapter
	slog.Info("service container: agent runtime dependencies built",
		"adapters", len(cliAdapters),
		"workspaces_root", agentDeps.WorkspacesRoot)

	chatSvc := NewChatService(ChatServiceConfig{
		Sessions:           sessions,
		Agents:             agents,
		Tools:              tools,
		Streams:            streams,
		Context:            ctxService,
		Events:             events,
		Providers:          cfg.Providers,
		Store:              cfg.Store,
		Orchestrator:       orchestrator,
		AppConfig:          cfg.AppConfig,
		OutputFilter:       cfg.OutputFilter,
		Commands:           commands,
		PluginHost:         pluginSink,
		ProcessTracker:     processTracker,
		UtilityProvider:    cfg.UtilityProvider,
		UtilityModel:       cfg.UtilityModel,
		Permissions:        permissions,
		PathGrants:         pathGrants,
		Tasks:              tasks,
		EmbeddingStatus:    embeddingStatus,
		EmbeddingProvider:  embeddingProviderID,
		ResultCache:        buildResultCache(cfg.Store),
		ModelCatalog:       modelCatalog,
		SessionEventWriter: messagingSvc,
		SubagentInbox:      messagingSvc,
		DBPath:             cfg.Store.DBPath(catalogCtx),
		AdapterRegistry:    adapterRegistry,
		// I1 (CW-20260426-0004): inspector — nil when developer_mode=false.
		Inspector: inspectorSvc,
		// I2 (CW-20260420-0029): loop detector — always-on.
		LoopDetector: loopDetector,
		// J11 (CW-20260426-0009): reminder engine — always-on.
		ReminderEngine: reminderEngine,
		ReflexEngine:   reflexEngine,
		// Phase 4c.1 (CW-20260508-0002): agent-runtime composition root.
		AgentDeps:            agentDeps,
		AgentSessionsManager: agentManager,
		AgentEventBridge:     agentBridge,
		// Phase 9 (CW-20260510-0014): bootdir adapter for the recovery
		// broker. Chat-side callers Track / Untrack so the broker can
		// repopulate the session's sandbox dir / regenerate CLAUDE.md
		// during remediation.
		AgentBootDirAdapter: agentBootDir,
		// B2 (CW-20260429-0031): wire the B3 in-process envelope-render
		// executor pilot so the route-dispatch seam can hand off
		// non-chat-direct routes. nil-safe — when omitted, the route
		// classifier's hint stays purely informative.
		EnvelopeRenderExecutor: envelope_render.New(),
		Lifecycle:              chatLifecycle,
	})

	// G-3 + G-5: subagent service with the real chat-engine-backed runner.
	// Phase 4a (CW-20260508-0002): BootRunner is the entry point — CLI-provider
	// subagents (claude/codex/opencode etc.) Boot a fresh ModeSubagent process
	// and stream events through the agentEventBridge per-session router; HTTP-
	// provider subagents (anthropic/openai/gemini/etc.) delegate to the legacy
	// ChatRunner which still drives one chat-harness turn through
	// chatServiceImpl.generateResponse against a child session.
	chatSvcImpl, ok := chatSvc.(*chatServiceImpl)
	if !ok {
		stopCatalog()
		return nil, fmt.Errorf("service container: chatSvc is %T, expected *chatServiceImpl for ChatRunner", chatSvc)
	}

	// Phase 4c.8 (CW-20260508-0002): wire the SessionService archive hook to
	// chatSvc.CloseAgentSession so closing a chat session releases the
	// underlying long-lived runtime session immediately instead of waiting
	// for the IdleKill=15min supervisor timeout. Best-effort, nil-safe.
	if sessImpl, ok := sessions.(*sessionServiceImpl); ok {
		sessImpl.SetArchiveHook(chatSvcImpl.CloseAgentSession)
	}

	// Wire the recovery-broker replacement-session hook so the
	// observeSessionForRecovery goroutine adopts each broker-dispatched
	// replacement into chatServiceImpl.activeSessions. Without this, the
	// next user turn after a recoverable failure misses the replacement
	// (deleted from activeSessions during cleanup) and boots yet another
	// session, orphaning the broker's retry. Best-effort: a non-Broker
	// RecoveryHooks (mocks in tests) silently skips wiring.
	if broker, ok := agentDeps.Recovery.(*broker.Broker); ok {
		broker.SetReplacementSessionHook(func(sessionID string, sess *runtimeagent.Session) {
			chatSvcImpl.adoptReplacementSession(sessionID, sess)
		})

		// Phase 0 task 04 (decision log §19): wire the HTTP-provider
		// (bootdir-free) retry hook now that chatSvcImpl exists. Same
		// construction-order reason as the replacement-session hook
		// above — the adapter closes over RetryLastMessage, which only
		// exists once NewChatService has returned.
		broker.SetHTTPRetry(newRecoveryHTTPRetryAdapter(chatSvcImpl.RetryLastMessage))
	}

	// TASKS/phase-2/04-retire-boot-profile-catalog.md: the boot-profile
	// recovery pre-boot hook (chatSvcImpl.recoveryPreBootHook) that used
	// to install here has been removed along with the whole boot-profile
	// catalog — its entire body re-resolved a boot-profile-backed session
	// via Registry.CompileFor, and every other session type already
	// passed through unchanged. agentBootAdapter.SetPreBootHook remains a
	// real, general extension point (see its doc comment in
	// agent_deps.go) for a future resume-ID-threading use case; no
	// current caller needs it installed.
	legacyRunner := NewChatRunner(chatSvcImpl, agentReader, cfg.Store, cfg.Store.DB, pathGrants)
	subagentRunner := NewBootRunner(agentDeps, agentBridge, agentReader, cfg.Store, cfg.Store.DB, pathGrants, legacyRunner)
	approvalEmitter := NewApprovalEmitter(cfg.Store, streams)
	subagentSvc := subagent.NewService(cfg.Store.DB, subagentRunner, messagingSvc, approvalEmitter, cfg.Store)
	subagentSvc.SetStreamSink(&subagentStreamSink{streams: streams})
	// CW-20260520-0001 (Layer 2): react to a subagent completion by
	// possibly triggering a harness turn on the parent session per its
	// configured policy. chatSvcImpl already exists by this point (built
	// above for the ChatRunner wiring).
	subagentSvc.SetCompletionReactor(&subagentCompletionReactor{chat: chatSvcImpl})
	// CW-20260816-0065: react to a live A2A message send (message_send /
	// nanite_a2a_send / the nanite a2a CLI / the message_send HTTP route —
	// anything that reaches messaging.Service.SendMessage other than
	// kind=subagent_result, which stays on the SetCompletionReactor path
	// above) by possibly triggering a harness turn on the recipient
	// session per its resolved message-wake policy. chatSvcImpl already
	// exists by this point (built above for the ChatRunner wiring) — same
	// ordering SetCompletionReactor relies on just above.
	messagingSvc.SetWakeReactor(&messagingWakeReactor{chat: chatSvcImpl})
	// Copilot PR #258 review: wire the wake-reactor goroutine spawn onto
	// a tracked *lifecycle.Manager instead of the untracked safego.Go
	// default, so a burst of messages can't leave unbounded goroutines
	// running past process shutdown. Reuses chatSvcImpl's own manager
	// (same package, field access is intra-package) rather than
	// constructing a second one — these goroutines call back into
	// chatSvcImpl anyway, so draining them alongside chat's own
	// generateResponse goroutines on Shutdown is the right scope, not a
	// separate lifecycle. chatSvcImpl already exists by this point (same
	// ordering constraint as SetWakeReactor immediately above).
	messagingSvc.SetLifecycleManager(chatSvcImpl.lifecycle)
	// H1 (CW-20260421-0014): wire trust resolver + audit event logger.
	subagentSvc.SetTrustResolver(cfg.Store)
	subagentSvc.SetEventLogger(cfg.Store)
	// CW-20260516-0066: wire the recursion-depth cap. A caller that is
	// itself a subagent (appears as a child_session_id) is rejected
	// before it can spawn another — hard cap at depth 1.
	subagentSvc.SetParentageChecker(cfg.Store)
	// CW-20260519-0123: wire the Spawn-boundary fail-fast gate. Unknown
	// roles (no profile) and can_execute=false-outside-whitelist roles
	// are rejected with a structured config error instead of falling
	// through to the orphan reaper / inactivity stall path.
	subagentSvc.SetProfileResolver(cfg.Store)

	// CW-20260512-0002 (b)+(c): subagent reaper — background goroutine
	// sweeps subagent_runs for timed-out and orphan rows so a hung
	// runner doesn't leave the parent dispatch path blocked indefinitely.
	// Bound to a dedicated cancel func so Shutdown can stop it before
	// the DB closes; goroutine exits on ctx.Done OR Reaper.Stop.
	reaperCtx, stopReaper := context.WithCancel(context.Background())
	containerCommitted := false
	defer func() {
		if !containerCommitted {
			stopReaper()
		}
	}()
	subagentReaper := subagent.NewReaper(cfg.Store.DB, subagent.ReaperOptions{})
	subagentReaper.Start(reaperCtx)
	slog.Info("service container: subagent reaper started",
		"interval", subagent.DefaultReaperInterval.String(),
		"orphan_grace", subagent.DefaultReaperOrphanGrace.String(),
	)

	// CW-20260518-0085: agent_runtime orphan reaper — pairs with the
	// subagent reaper but operates on the agent_runtime table (where
	// agent.Boot persists per-spawn lifecycle rows). Without this:
	//   - After a service restart, every pre-restart row stays
	//     state=running with a now-dead pid forever (FE indicator from
	//     CW-20260518-0084 only catches sessions with message history;
	//     runtime-only subagent rows go untouched).
	//   - If a long-lived process dies mid-run without persisting a
	//     terminal state, the row hangs around in state=running too.
	// Startup sweep runs synchronously to reconcile pre-restart rows
	// before the chat layer starts serving requests; periodic reaper
	// then catches mid-run deaths on the configured interval.
	runtimeReaperCtx, stopRuntimeReaper := context.WithCancel(context.Background())
	defer func() {
		if !containerCommitted {
			stopRuntimeReaper()
		}
	}()
	runtimeReaper := orphansweep.NewRuntimeReaper(agentDeps, orphansweep.RuntimeReaperOptions{})
	// PR #213 review: bound the startup sweep to runtimeReaperCtx (so Shutdown
	// during container build can cancel it) and to a 30s wall clock (so a
	// stuck SQLite query cannot block boot indefinitely).
	startupSweepCtx, cancelStartupSweep := context.WithTimeout(runtimeReaperCtx, 30*time.Second)
	startupReconciled, startupSweepErr := runtimeReaper.SweepOnce(startupSweepCtx)
	cancelStartupSweep()
	if startupSweepErr != nil {
		slog.Warn("service container: agent_runtime startup sweep failed",
			"err", startupSweepErr,
		)
	} else {
		slog.Info("service container: agent_runtime startup sweep complete",
			"reconciled", startupReconciled,
		)
	}
	runtimeReaper.Start(runtimeReaperCtx)
	slog.Info("service container: agent_runtime reaper started",
		"interval", orphansweep.DefaultRuntimeReaperInterval.String(),
		"pid_zero_grace", orphansweep.DefaultRuntimeReaperPidZeroGrace.String(),
	)

	// G-4: register the subagent-spawn-approval typed response handler so
	// POST /api/envelopes/:id/respond dispatches to Approve/Reject.
	chat.RegisterResponseHandler("subagent-spawn-approval", chat.NewSubagentApprovalHandler(subagentSvc))

	slog.Info("service container: subagent service enabled (real chat-engine runner + status sink + approval handler + H1 trust)")

	// G1 (CW-20260420-0016): background-job service. Async, non-session-
	// bound dispatch for long-running tasks. Backend = PTY MVP (D2);
	// agent-mux swap (D3) is a wiring change behind the same Backend
	// interface. Result envelopes ride the messaging service back to the
	// originating session as channel=inbox notifications.
	backgroundSvc := background.NewService(background.NewPTYBackend(), messagingSvc)
	slog.Info("service container: background-job service enabled (PTY backend)")

	// G4 (CW-20260420-0018): elicitation service — MCP elicitation/create
	// mid-tool user prompts. The emitter persists an elicitation-prompt
	// envelope and streams it to the chat UI; the response handler routes
	// user responses back to the waiting tool call.
	elicitEmitter := NewElicitationEmitter(cfg.Store, streams)
	elicitSvc := elicitation.New(elicitEmitter, 0) // 0 → picks up env / default (5 min)
	chat.RegisterResponseHandler("elicitation-prompt", chat.NewElicitationResponseHandler(elicitSvc))
	slog.Info("service container: elicitation service enabled (G4, CW-20260420-0018)")

	// Phase 6: interactive-table row/column-action primitive. table-card
	// itself may still be emitted passively (card_show, no id, no
	// interactivity) — this handler only ever runs for the subset of
	// table-card responses that reach POST /api/envelopes/:id/respond,
	// which requires the emitting caller to have persisted an
	// EnvelopeInstance (the same requirement approval-card/
	// elicitation-prompt already have). See internal/chat/envelope_response_table.go.
	chat.RegisterResponseHandler("table-card", chat.NewTableCardActionHandler())

	durableAgents := NewDurableAgentServiceWithRuntime(cfg.Store, NewChatDurableAgentRuntimeController(chatSvc))
	durableWake := NewDurableAgentWakeService(cfg.Store, durableAgents)
	durableAgentRecipes, err := NewDurableAgentRecipeService(durableAgents, cfg.DurableAgentRecipeCatalogPaths...)
	if err != nil {
		stopCatalog()
		return nil, fmt.Errorf("service container: durable agent recipes: %w", err)
	}
	if err := SyncManagedDurableAgentConfigs(cfg.Store, managedConfigRoot); err != nil {
		stopCatalog()
		return nil, fmt.Errorf("service container: sync managed durable agents: %w", err)
	}

	// F5 follow-up (CW-20260420-0022): wire the HintDispatcher adapter
	// into ContextClient so NANITE_THINK_BLOCK_V2_ENABLED=true actually
	// fires v2 dynamic hints in production. Without this assignment the
	// production path falls through to v1 static hints (matching the
	// pre-Phase-7 behavior). Adapter is dispatch.Spawner-backed, slug
	// "hint-selector"; sub-millisecond cost when v2 is disabled because
	// the IsThinkBlockV2Enabled gate runs before the dispatcher is
	// consulted.
	hintSpawner := NewDispatchSpawner(subagentSvc, cfg.Store)
	contextClient.HintDispatcher = NewHintDispatchAdapter(hintSpawner)
	slog.Info("service container: hint dispatcher wired (F5 production wiring, CW-20260420-0022)")

	// Worker manager — requires ChatService for delegation.
	// Uses SetWorkers to break the circular dependency (ChatService <-> WorkerManager).
	var workers *worker.Manager
	if cfg.CoordStore != nil && cfg.CoordStore.Available() {
		workers = worker.NewManager(worker.ManagerConfig{
			MaxConcurrentWorkers: cfg.MaxConcurrentWorkers,
			Chat:                 chatSvc,
			Coord:                cfg.CoordStore,
			Tasks:                tasks,
			Worktrees:            cfg.Worktrees,
		})
		if impl, ok := chatSvc.(*chatServiceImpl); ok {
			impl.SetWorkers(workers)
		}
		slog.Info("service container: worker manager enabled")
	} else {
		slog.Info("service container: worker manager disabled (no coordination store)")
	}

	// Memory extraction hooks + agent tools.
	if memorySvc != nil && cfg.Plugins != nil {
		// Utility call provider/model resolves through the store so
		// user_settings → providers.default_model is honored
		// (CW-20260526-0003). If the chain is dry, the utilityCall stays
		// nil below and memory extraction falls through silently — same
		// behavior the unregistered-provider branch already produces.
		utilityProvider := cfg.UtilityProvider
		utilityModel := cfg.UtilityModel
		if utilityProvider == "" || utilityModel == "" {
			if rp, rm, err := cfg.Store.ResolveProviderAndModel(catalogCtx, utilityProvider, utilityModel); err == nil {
				utilityProvider = rp
				utilityModel = rm
			}
		}

		var utilityCall memory.UtilityCallFunc
		if prov, ok := cfg.Providers.Get(utilityProvider); ok {
			utilityCall = func(ctx context.Context, prompt string) (string, error) {
				msgs := []llmtypes.ChatMessage{{Role: "user", Content: prompt}}
				return prov.Complete(ctx, llmtypes.ChatRequest{
					SystemPrompt: "You are a memory extraction assistant. Follow instructions precisely.",
					Messages:     msgs,
					Model:        utilityModel,
				})
			}
		}

		extractor := memory.NewExtractor(memorySvc, utilityCall)

		// Register per-turn extraction hook (message.received).
		perTurnHook := extractor.PerTurnHook()
		if err := cfg.Plugins.RegisterEventHook(perTurnHook.EventTypes(), perTurnHook); err != nil {
			slog.Warn("service container: failed to register per-turn memory hook", "err", err)
		}

		// Register post-compaction extraction hook (context.compacted).
		postCompactHook := extractor.PostCompactHook()
		if err := cfg.Plugins.RegisterEventHook(postCompactHook.EventTypes(), postCompactHook); err != nil {
			slog.Warn("service container: failed to register post-compact memory hook", "err", err)
		}

		slog.Info("service container: memory extraction hooks registered")
	}

	// Memory tools (nanite_memory_save / nanite_memory_recall) were
	// removed in CW-20260508-0017 (Decision 3): the local SQLite memory
	// store is no longer agent-facing — Vanta is the canonical memory
	// substrate (vanta-primary-since: 2026-04-19). The underlying
	// memory.Service stays load-bearing for contextbroker.NewMemorySource
	// and the per-turn / post-compact
	// extractor hooks; only the agent-facing tool surface and the
	// `nanite-memory` MCP server registration are dropped here.

	// Model selector for operation-specific model resolution (e.g., cheap model for summarization).
	modelSelector := provider.NewStaticModelSelector(cfg.UtilityProvider, cfg.UtilityModel)

	// Workflow run store and SSE broadcaster — always enabled.
	runStore := workflow.NewRunStore(50)
	workflowBroadcaster := workflow.NewBroadcaster()
	slog.Info("service container: workflow engine enabled")

	slog.Info("service container: all services wired")

	container := &Container{
		Sessions:            sessions,
		Agents:              agents,
		Skills:              skills,
		SkillVendor:         skillVendor,
		Tools:               tools,
		Chat:                chatSvc,
		Context:             ctxService,
		Streams:             streams,
		Events:              events,
		Providers:           cfg.Providers,
		Commands:            commands,
		Plugins:             cfg.Plugins,
		MCP:                 cfg.MCP,
		Messaging:           messagingSvc,
		Subagent:            subagentSvc,
		Background:          backgroundSvc,
		Elicitation:         elicitSvc,
		Todos:               todos,
		Conduit:             conduitInstance,
		Memory:              memorySvc,
		EmbeddingStatus:     embeddingStatus,
		EmbeddingProvider:   embeddingProviderID,
		EmbeddingModel:      embeddingModel,
		Coord:               cfg.CoordStore,
		DurableAgents:       durableAgents,
		DurableWake:         durableWake,
		DurableAgentRecipes: durableAgentRecipes,
		Tasks:               tasks,
		Workers:             workers,
		Worktrees:           cfg.Worktrees,
		Store:               cfg.Store,
		ToolClient:          cfg.ToolClient,
		ProcessTracker:      processTracker,
		Orchestrator:        orchestrator,
		Activity:            cfg.Activity,
		UtilityProvider:     cfg.UtilityProvider,
		UtilityModel:        cfg.UtilityModel,
		ModelSelector:       modelSelector,
		Permissions:         permissions,
		PathGrants:          pathGrants,
		AdapterRegistry:     adapterRegistry,
		ProviderCatalog:     cfg.ProviderCatalog,
		Recovery:            recoveryBrokerOrNil(agentDeps),
		Inspector:           inspectorSvc,
		LoopDetector:        loopDetector,
		ReminderEngine:      reminderEngine,
		ReflexEngine:        reflexEngine,
		RunStore:            runStore,
		WorkflowBroadcaster: workflowBroadcaster,
		AppConfig:           cfg.AppConfig,
		WorkingDir:          workingDir,
		ManagedConfigRoot:   managedConfigRoot,
		AgentConfig:         agentConfig,
		stopModelCatalog:    stopCatalog,
		subagentReaper:      subagentReaper,
		stopSubagentReaper:  stopReaper,
		runtimeReaper:       runtimeReaper,
		stopRuntimeReaper:   stopRuntimeReaper,
	}
	containerCommitted = true
	chatLifecycleCommitted = true
	return container, nil
}

// RefreshUtilitySettings updates the utility provider/model on the running
// container. Called by the settings API after a user changes preferences.
func (c *Container) RefreshUtilitySettings(prov, model string) {
	if prov != "" {
		c.UtilityProvider = prov
	}
	if model != "" {
		c.UtilityModel = model
	}
}

// containerShutdownMaxWait bounds admission to plugin unload and waiting for
// independent subsystem shutdowns. Once plugin unload begins it is joined
// before return, because the plugin API has no context-bounded shutdown form;
// a blocking plugin Unload may therefore extend total shutdown beyond this
// duration rather than being abandoned after teardown has begun.
const containerShutdownMaxWait = 10 * time.Second

// Shutdown performs graceful shutdown of all services. Independent subsystems
// run in parallel. Plugin unload is admitted only after Chat reports a
// successful lifecycle drain, and once admitted is joined before return.
func (c *Container) Shutdown() {
	c.shutdownOnce.Do(c.shutdown)
}

func (c *Container) shutdown() {
	c.shutdownWithMaxWait(containerShutdownMaxWait)
}

func (c *Container) shutdownWithMaxWait(maxWait time.Duration) {
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), maxWait)
	defer cancelShutdown()
	var wg sync.WaitGroup

	run := func(label string, fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				// A subsystem shutdown panicking should not abort the others.
				if r := recover(); r != nil {
					slog.Error("shutdown: subsystem panic", "label", label, "panic", r)
				}
			}()
			fn()
		}()
	}

	if c.stopModelCatalog != nil {
		c.stopModelCatalog()
	}

	// TASKS/scheduling/05-engine-wiring-and-full-replace.md: stop the
	// schedule engine's own tick goroutine before any of the subsystem
	// shutdowns below kick in — same "stop the thing that can fire new
	// work into a subsystem before that subsystem starts closing" ordering
	// already established for the reapers just below. Engine.Stop blocks
	// until the tick loop has actually exited (go-scheduler's own
	// documented contract), so by the time we proceed, no in-flight tick
	// can dispatch into a Runner (DurableWake/WorkflowLauncher/ToolService/
	// reflexes.Executor) whose own Shutdown/Close is about to run below.
	if c.Engine != nil {
		c.Engine.Stop()
	}

	// CW-20260512-0002 (b)+(c): stop the reaper goroutine before any of
	// the subsystem shutdowns below kick in. Reaper.Stop blocks until
	// the loop returns, so by the time we proceed, no concurrent reaper
	// UPDATEs will race the DB close.
	if c.stopSubagentReaper != nil {
		c.stopSubagentReaper()
	}
	if c.subagentReaper != nil {
		c.subagentReaper.Stop()
	}

	// CW-20260518-0085: same sequencing for the agent_runtime reaper.
	if c.stopRuntimeReaper != nil {
		c.stopRuntimeReaper()
	}
	if c.runtimeReaper != nil {
		c.runtimeReaper.Stop()
	}

	if c.Workers != nil {
		run("workers", func() {
			if err := c.Workers.Shutdown(maxWait); err != nil {
				slog.Warn("shutdown: workers", "err", err)
			}
		})
	}
	if c.Tasks != nil {
		run("tasks", func() {
			ctx, cancel := context.WithTimeout(context.Background(), maxWait)
			defer cancel()
			if err := c.Tasks.Snapshot(ctx); err != nil {
				slog.Warn("shutdown: task snapshot", "err", err)
			}
		})
	}
	if c.Coord != nil {
		run("coord", func() {
			if err := c.Coord.Close(); err != nil {
				slog.Warn("shutdown: coordination store close", "err", err)
			}
		})
	}
	if c.Conduit != nil {
		run("conduit", func() {
			if err := c.Conduit.Close(); err != nil {
				slog.Warn("shutdown: conduit close", "err", err)
			}
		})
	}
	if c.MCP != nil {
		run("mcp", func() { c.MCP.Close() })
	}
	if c.Messaging != nil {
		run("messaging", func() {
			if err := c.Messaging.Close(); err != nil {
				slog.Warn("shutdown: messaging close", "err", err)
			}
		})
	}

	// Plugin callbacks are owned by Chat's lifecycle. Chat runs separately
	// from the generic wait group so a container deadline can decline plugin
	// unload permanently if Chat is blocked or late. No goroutine waits on the
	// result and triggers unload after this method has returned.
	chatResult := make(chan error, 1)
	if c.Chat == nil {
		chatResult <- nil
	} else {
		go func() {
			var err error
			defer func() {
				if recovered := recover(); recovered != nil {
					err = fmt.Errorf("panic: %v", recovered)
				}
				chatResult <- err
			}()
			err = c.Chat.Shutdown()
		}()
	}

	chatDrained := false
	select {
	case err := <-chatResult:
		if err != nil {
			slog.Warn("shutdown: chat did not drain; skipping plugin unload", "err", err)
		} else {
			select {
			case <-shutdownCtx.Done():
				slog.Warn("shutdown: chat drain completed after deadline; skipping plugin unload", "timeout", maxWait.String())
			default:
				chatDrained = true
			}
		}
	case <-shutdownCtx.Done():
		slog.Warn("shutdown: chat timed out; skipping plugin unload", "timeout", maxWait.String())
	}

	if chatDrained && c.Plugins != nil {
		// The plugin host has no context-aware Shutdown contract. Once teardown
		// begins, join it rather than returning while Unload can still mutate
		// plugin state in the background.
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					slog.Error("shutdown: subsystem panic", "label", "plugins", "panic", recovered)
				}
			}()
			if err := c.Plugins.Shutdown(); err != nil {
				slog.Warn("shutdown: plugins", "err", err)
			}
		}()
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-shutdownCtx.Done():
		slog.Warn("shutdown: timeout — some subsystems may still be running", "timeout", maxWait.String())
	}
}

// buildResultCache creates a ResultCache from UserSettings or defaults.
func buildResultCache(s *store.Store) *tool.ResultCache {
	cfg := tool.ResultCacheConfig{}
	if us, err := s.GetUserSettings(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */); err == nil {
		cfg.SoftTruncBytes = us.ToolResultSoftTruncBytes
		cfg.HardCapBytes = us.ToolResultHardCapBytes
		cfg.CacheTTLSeconds = us.ToolResultCacheTTLSeconds
	}
	return tool.NewResultCache(s.DB, cfg)
}

// modelsDevProviderAllowlist restricts syncCatalogToRegistry's merge to the
// models.dev provider keys Nanite's own provider adapters actually call
// (see seededProviders in internal/store/seed.go). Verified against a real
// models.dev disk cache (2026-08-17 fetch, 190 providers) while building
// Phase 1 #06: models.dev's catalog is deliberately provider-agnostic
// (ADR-001, docs/decisions/ADR-001-models-catalog-sync.md) and
// modelsdev.CatalogInput's maps are keyed by bare model ID with no
// provider dimension at all — several reseller/gateway providers
// (confirmed real entries: qihang-ai, 302ai, jiekou, nano-gpt, helicone,
// llmgateway, abacus) mirror "claude-sonnet-4-5-20250929" verbatim at
// different (often discounted) pricing and context limits. Sorted
// alphabetically after "anthropic", the last one processed silently wins
// and clobbers the real vendor's numbers for every caller of
// models.Pricing/MaxOutputFor/ContextWindowFor — and, since this task
// added a DB-write path fed from the same overlay, the models table too.
// ADR-001's provider-agnostic design is not being reopened here (still
// Option C, still one shared merge); this only narrows which of
// models.dev's ~190 providers are allowed to contribute to that merge.
var modelsDevProviderAllowlist = map[string]bool{
	"anthropic": true,
	"openai":    true,
}

// syncCatalogToRegistry builds a CatalogInput from the models.dev client and
// pushes it into the pkg/models overlay so all callers of Pricing,
// MaxOutputFor, and ContextWindowFor see live values without the catalog being
// threaded through the call stack. It then upserts the same, now-current
// values into the `models` DB table via st.SyncModelsFromRegistry so the
// table isn't just fed once by the boot-time seed (Phase 1 #06 — see
// internal/store/models.go for why this must run after SyncFromCatalog,
// not independently of it).
//
// st may be nil in tests that construct a bare *modelsdev.Client without a
// store; the DB-write half is skipped in that case and only the in-memory
// overlay is updated.
func syncCatalogToRegistry(c *modelsdev.Client, st *store.Store) {
	refs := c.List()
	if len(refs) == 0 {
		return
	}
	input := models.CatalogInput{
		ContextWindows:  make(map[string]int, len(refs)),
		MaxOutputTokens: make(map[string]int, len(refs)),
		InputPricing:    make(map[string]float64, len(refs)),
		OutputPricing:   make(map[string]float64, len(refs)),
	}
	for _, ref := range refs {
		if ref.ID == "" {
			continue
		}
		if !modelsDevProviderAllowlist[ref.ProviderID] {
			continue
		}
		if ref.Limit.ContextWindow > 0 {
			input.ContextWindows[ref.ID] = ref.Limit.ContextWindow
		}
		if ref.Limit.MaxOutputTokens > 0 {
			input.MaxOutputTokens[ref.ID] = ref.Limit.MaxOutputTokens
		}
		if ref.Cost.Input > 0 {
			input.InputPricing[ref.ID] = ref.Cost.Input
		}
		if ref.Cost.Output > 0 {
			input.OutputPricing[ref.ID] = ref.Cost.Output
		}
	}
	models.SyncFromCatalog(input)

	if st == nil {
		return
	}
	if n, err := st.SyncModelsFromRegistry(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */); err != nil {
		slog.Warn("service container: models table sync from catalog failed", "err", err)
	} else {
		slog.Debug("service container: models table synced from catalog", "rows", n)
	}
}

// recoveryBrokerOrNil resolves the *broker.Broker on the agent
// dependencies, or returns nil when the wired recovery hooks are not a
// concrete *Broker (test fakes register interface-only mocks). The
// API recovery-cancel endpoint is no-op when nil — there is nothing
// to cancel against.
func recoveryBrokerOrNil(deps *runtimeagent.Dependencies) *broker.Broker {
	if deps == nil {
		return nil
	}
	broker, _ := deps.Recovery.(*broker.Broker)
	return broker
}
