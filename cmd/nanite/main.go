package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/hollis-labs/go-envelopes"

	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/config"
	"github.com/hollis-labs/nanite/internal/coordination"
	"github.com/hollis-labs/nanite/internal/envelope"
	envelope_render "github.com/hollis-labs/nanite/internal/executor/envelope_render"
	"github.com/hollis-labs/nanite/internal/learnings"
	naniteotel "github.com/hollis-labs/nanite/internal/otel"
	"github.com/hollis-labs/nanite/internal/promptrouter"
	"github.com/hollis-labs/nanite/internal/providercatalog"
	"github.com/hollis-labs/nanite/internal/worktree"

	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/go-toolbroker/broker"
	"github.com/hollis-labs/nanite/internal/agentregistry"
	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/api"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/filter"
	"github.com/hollis-labs/nanite/internal/launcher"
	"github.com/hollis-labs/nanite/internal/lifecycle"
	nllmanthropic "github.com/hollis-labs/nanite/internal/llm/anthropic"
	nllmopenai "github.com/hollis-labs/nanite/internal/llm/openai"
	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/mcpserver"
	"github.com/hollis-labs/nanite/internal/plugin"
	_ "github.com/hollis-labs/nanite/internal/plugin/allplugins" // registers all built-in plugins
	"github.com/hollis-labs/nanite/internal/safego"
	"github.com/hollis-labs/nanite/internal/secrets"
	"github.com/hollis-labs/nanite/internal/server"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/slogx"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/toolclient"
	"github.com/hollis-labs/nanite/internal/truncate"
	"github.com/hollis-labs/nanite/internal/version"
	"github.com/hollis-labs/nanite/internal/workflowrunner"

	agentbroker "github.com/hollis-labs/agentkit/broker"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: %s <command>\n", brand.BinaryName)
		fmt.Fprintln(os.Stderr, "commands: serve, launch, chat, plugin, mcp, message, admin, path, version (framework-injection moved to `nanite-agent init`)")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "serve":
		cmdServe(os.Args[2:])
	case "launch":
		cmdLaunch(os.Args[2:])
	case "chat":
		cmdChat(os.Args[2:])
	case "plugin":
		cmdPlugin(os.Args[2:])
	case "mcp":
		cmdMCP(os.Args[2:])
	case "install":
		cmdInstall(os.Args[2:])
	case "message":
		cmdMessage(os.Args[2:])
	case "admin":
		cmdAdmin(os.Args[2:])
	case "path":
		cmdPath(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println(brand.BinaryName + " " + version.Full())
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func cmdServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	port := fs.Int("port", 8090, "HTTP listen port")
	// --db default is empty: an unset flag resolves the database path via
	// go-apppaths (CW-20260517-0061). A non-empty flag becomes an explicit
	// WithDBOverride. The retired "./nanite.db" CWD-relative default is the
	// data-loss failure mode this migration removes.
	dbFlag := fs.String("db", "", "SQLite database path (default: go-apppaths XDG layout — run `nanite path`)")
	dev := fs.Bool("dev", false, "Development mode (skip embedded SPA)")
	fs.Parse(args)

	resolvedDB := resolveDBPathWith(*dbFlag)
	dbPath := &resolvedDB

	// Resolve the go-apppaths layout once for the daemon's DB-sibling
	// directories (CW-20260517-0061). coordination/ and worktrees/ are
	// runtime STATE, so they anchor on StateDir — explicitly, NOT on
	// filepath.Dir(dbPath). Pre-migration they rode filepath.Dir(*dbPath)
	// (CWD); letting them silently follow the DB into the XDG data root
	// would be the same data-placement bug this migration removes.
	serveLayout, serveLayoutErr := config.ResolveLayout()
	if serveLayoutErr != nil {
		slog.Error("resolve app layout", "err", serveLayoutErr)
		os.Exit(1)
	}

	// Load app-level config first so the logging handler and OTel init
	// both observe the same settings. A missing or malformed config
	// falls back to safe defaults rather than aborting startup.
	appCfg, appCfgErr := config.LoadAppConfig("config/" + brand.ConfigFileName + ".yaml")
	if appCfgErr != nil {
		appCfg = config.DefaultAppConfig()
	}

	// Install the structured logging handler before anything else
	// emits a log record. All slog-based sites flow through the PII
	// redactor and JSON handler.
	_, logCloser, logErr := slogx.Init(slogx.Config{
		Format:    slogx.ParseFormat(appCfg.Logging.Format),
		Level:     slogx.ParseLevel(appCfg.Logging.Level),
		RedactPII: appCfg.Logging.RedactPII,
		AddSource: appCfg.Logging.AddSource,
	})
	if logErr != nil {
		slog.Warn("slog handler init failed, continuing with stdlib log", "err", logErr)
	} else {
		defer logCloser.Close()
	}

	// Load agentrc config (user-level + project-level, merged).
	cfg, cfgErr := config.Load()
	if cfgErr != nil {
		slog.Warn("failed to load agentrc config", "err", cfgErr)
	} else {
		name := cfg.Project.Name
		if name == "" {
			name = "(unnamed)"
		}
		slog.Info("config loaded", "project", name, "role", cfg.Role, "root", cfg.ProjectRoot())
	}

	if appCfgErr != nil {
		slog.Warn("failed to load app config, using defaults", "err", appCfgErr)
	}

	// Initialise OpenTelemetry tracing via the internal/otel wrapper.
	// The wrapper honours NANITE_OTEL_DISABLED=1 (env takes precedence)
	// and AppConfig.OTel.Disabled — either installs a no-op tracer
	// provider and returns a no-op shutdown.
	otelCtx := context.Background()
	otelShutdown, otelErr := naniteotel.Init(otelCtx, naniteotel.Config{
		ServiceName: brand.OTelService,
		Disabled:    appCfg.OTel.Disabled,
	})
	if otelErr != nil {
		slog.Warn("OTel init failed", "err", otelErr)
	} else {
		defer otelShutdown(otelCtx)
	}

	// Open store and run migrations.
	s, err := store.New(context.Background(), *dbPath)
	if err != nil {
		slogx.Fatal("failed to open store", "err", err)
	}
	defer s.Close()

	// Seed default data.
	if err := s.Seed(); err != nil {
		slogx.Fatal("failed to seed database", "err", err)
	}
	if err := s.SeedProviders(); err != nil {
		slogx.Fatal("failed to seed providers", "err", err)
	}

	// Load the canonical envelope catalog from go-envelopes (lib v0.1.0).
	// The lib's embedded manifest replaces nanite/config/envelopes.yaml as the
	// single source of truth for core type definitions and per-type schemas.
	envelopeCtx, envelopeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	envReg, err := envelopes.LoadCore(envelopeCtx)
	envelopeCancel()
	if err != nil {
		slogx.Fatal("envelope registry load failed", "err", err)
	}
	chat.SetEnvelopeRegistry(envReg)
	envelope.SetEnvelopeRegistry(envReg)

	// Mirror the registry's bare core type names into the chat-side
	// allowlist used by ParseEnvelopes. The registry is authoritative for
	// schema lookups; chat.registeredTypes stays as a wire-format index
	// until the orphan/namespacing story lets us collapse them (separate
	// catalog-cleanup task).
	coreTypes := envReg.Names()
	chat.InitCoreTypes(coreTypes)
	slog.Info("envelope registry loaded", "count", len(coreTypes), "source", "go-envelopes v0.1.0")

	// envelope.OrphanTypes is now empty — all five entries (giphy-modal;
	// kb-result, resolution-capture, ticket-form, ticket-confirmation) were
	// removed by the Phase 0 plugin cuts (15a-cut-giphy, 15c-cut-support-ticket).
	// RegisterOrphans is a no-op today; left in place (not deleted) since a
	// future orphan schema has an obvious place to register without
	// reintroducing this call site.
	if n, err := envelope.RegisterOrphans(envReg); err != nil {
		slog.Warn("envelope: partial orphan registration", "registered", n, "err", err)
	} else {
		slog.Info("envelope: orphan schemas registered", "count", n, "plugin_id", envelope.LegacyPluginID)
	}
	for _, bare := range envelope.OrphanTypes {
		chat.RegisterEnvelopeType(bare)
	}

	// Wire the plugin→chat envelope registrar hook (B.4). Without this, the
	// yaml-authoritative loader still records envelopes in the host side-map
	// but chat validation won't accept them until the plugin calls through.
	plugin.SetEnvelopeTypeRegistrar(chat.RegisterEnvelopeType)
	plugin.SetEnvelopeTypeUnregistrar(chat.UnregisterEnvelopeType)

	// B.11 strict envelope validation: cache developer_mode once at startup
	// rather than querying the settings store on every FilterPluginEnvelopes
	// call. Envelope filtering runs on every plugin command / event / MCP
	// response, so a DB hit per envelope would be avoidable latency. Changes
	// to developer_mode require a process restart to take effect in the
	// envelope validator — acceptable because this is a developer-tooling
	// knob, not a runtime-tunable behavior.
	envelopeValidatorDevMode := false
	if settings, err := s.GetUserSettings(); err != nil {
		slog.Warn("envelope validator dev mode: failed to read user settings at startup; defaulting to production-strict", "error", err)
	} else {
		envelopeValidatorDevMode = settings.DeveloperMode
	}
	plugin.SetEnvelopeValidatorDevModeFunc(func() bool { return envelopeValidatorDevMode })

	// Set up provider registry (API keys, CLI adapters).
	registry, cliAdapters, providerCatalog := initProviders(envelopeValidatorDevMode)

	slog.Info("app config loaded",
		"cli_active_throttle_seconds", appCfg.Presence.CLIActiveThrottleSeconds,
		"auto_detect_tools", len(appCfg.Artifacts.AutoDetectTools))

	// Configure output filters. Default: strip emoji from LLM responses.
	outputFilters := filter.NewChain()
	outputFilters.Add("no_emoji", filter.NoEmoji)
	slog.Info("output filters registered", "filters", outputFilters.Names())

	// Set up MCP manager, tool broker, and self-service tools. cfg may be
	// nil if the agentrc loader failed; initMCP falls back to the hardcoded
	// default allow-list in that case so dev tools still work for the
	// running user.
	mcpManager, tb, selfTools := initMCP(s, cfg, appCfg)

	// Set up activity emitter for external GUI event consumers.
	activity := chat.NewActivityEmitter("")

	// Resolve utility provider/model. user_settings.utility_* first, then
	// the default-resolver (user_settings.default_* → providers.default_
	// model). CW-20260526-0003: empty result on resolver error is accepted
	// here so the harness boots with utility wiring degraded rather than
	// crashing — the chat-service config still validates each utility call
	// site individually.
	utilityProvider, utilityModel := "", ""
	if settings, err := s.GetUserSettings(); err == nil {
		utilityProvider = settings.UtilityProvider
		utilityModel = settings.UtilityModel
	}
	if utilityProvider == "" || utilityModel == "" {
		if rp, rm, err := s.ResolveProviderAndModel(utilityProvider, utilityModel); err == nil {
			utilityProvider = rp
			utilityModel = rm
		}
	}

	// CLI process concurrency limit.
	maxCLIProcs := 10
	if maxProcs := os.Getenv(brand.Env("MAX_CLI_PROCESSES")); maxProcs != "" {
		if n, err := strconv.Atoi(maxProcs); err == nil && n >= 0 {
			maxCLIProcs = n
		}
	}

	// Create plugin host (before container so it can be wired as a dependency).
	logger := plugin.NewLogger(brand.ID + "-plugin")
	pluginHost := plugin.NewHost(nil, logger)
	pluginHost.SetStore(s)
	pluginHost.SetEnvelopeRegistry(envReg)
	pluginHost.SetMCPRegistrar(mcpManager)
	pluginHost.RegisterService("store", s)
	pluginHost.RegisterService("mcp", mcpManager)
	pluginHost.RegisterService("toolclient", tb)
	slog.Info("plugin host initialized")

	// --- Coordination store (Badger KV for multi-agent state) ---
	// Anchored on the go-apppaths StateDir (CW-20260517-0061) — runtime
	// state, not data, and explicitly NOT derived from filepath.Dir(dbPath).
	coordDir := filepath.Join(serveLayout.StateDir(), "coordination")
	coordStore, coordErr := coordination.NewBadgerStore(coordDir)
	if coordErr != nil {
		slog.Warn("coordination store failed to open, multi-agent features disabled", "err", coordErr)
		coordStore = nil
	} else {
		defer coordStore.Close()
		slog.Info("coordination store: badger initialized", "dir", coordDir)
	}
	var coord coordination.CoordStore
	if coordStore != nil {
		coord = coordStore
	} else {
		coord = coordination.NewNoopStore()
	}

	// --- Worktree manager (git worktree isolation for workers) ---
	// Anchored on the go-apppaths StateDir (CW-20260517-0061) — git
	// worktrees are ephemeral runtime state, explicitly NOT derived from
	// filepath.Dir(dbPath).
	wtBaseDir := filepath.Join(serveLayout.StateDir(), "worktrees")
	wtMgr, wtErr := worktree.NewManager(wtBaseDir)
	if wtErr != nil {
		slog.Warn("worktree manager init failed, worktree isolation disabled", "err", wtErr)
		wtMgr = worktree.NewNoopManager()
	} else {
		slog.Info("worktree manager initialized", "dir", wtBaseDir)
	}

	// CW-20260502-0005 / CW-20260509-0045 / CW-20260509-0046: agent-broker
	// instance. Constructed BEFORE NewContainer so it can be threaded
	// into ContainerConfig (chat-service upstream wire site) AND
	// installed onto selfTools.Broker (downstream scaffold) below. One
	// broker, two consumers — the deterministic v1 impl
	// (`broker.New()`) is concurrency-safe (pure function over
	// broker.Input). See chat_broker_dispatch.go for the load-bearing
	// boundary between the two wire sites.
	agentBrokerInstance := agentbroker.New()

	// --- S5 Phase C/F: shared directory registry ---
	//
	// Build the shared registry-primary registrar (FileBackedRegistrar +
	// DegradingRegistrar + LastKnownGoodCache) over the boot-profile
	// catalog root ONCE, here, BEFORE NewContainer. The SAME instance is
	// threaded into:
	//
	//   - ContainerConfig.AgentRegistry — so the GUI chat boot-profile
	//     launch path (driveBootSession) resolves its runtime binding
	//     registry-primary via launchplan.Build (Phase F);
	//   - the standalone `nanite launch` subcommand builds its own (it is
	//     a separate process), but uses the identical agentregistry.Build
	//     + launchplan.Build seam.
	//
	// agent-source registration (the D2 resolver handle) runs after the
	// HTTP port is known — see further below. agentregistry.Build does
	// pure local filesystem I/O and never fails the process (D1).
	agentRegistry := agentregistry.Build(resolveBootProfileCatalogPath(cfg), "", slog.Default())

	// --- Service container: single wiring point ---
	// apiBaseURL also backs the external workflow-engine wiring below
	// (CW-20260814-0003) — the same "point a subprocess at this live
	// harness" address CLI-launched agents use.
	apiBaseURL := fmt.Sprintf("http://127.0.0.1:%d", *port)
	container, err := service.NewContainer(service.ContainerConfig{
		Store:           s,
		Providers:       registry,
		MCP:             mcpManager,
		ToolClient:      tb,
		Plugins:         pluginHost,
		AppConfig:       appCfg,
		APIBaseURL:      apiBaseURL,
		Activity:        activity,
		OutputFilter:    outputFilters,
		UtilityProvider: utilityProvider,
		UtilityModel:    utilityModel,
		MaxCLIProcesses: maxCLIProcs,
		CoordStore:      coord,
		Worktrees:       wtMgr,
		CLIAdapters:     cliAdapters,
		ProviderCatalog: providerCatalog,
		AgentBroker:     agentBrokerInstance,
		// CW-20260512-0118 (SP-20260512-0010 W2): thread the dev-tools
		// allow-list onto the ContextClient so the per-session
		// SlotPermissions summary surfaces the baseline READ roots.
		// resolveDevToolsAllowedPaths is the same function initMCP uses
		// to populate the DevToolsTransport allow-list — calling it again
		// here keeps the rendered summary consistent with the gate
		// (no drift between what the agent reads and what the gate
		// enforces).
		DevToolsAllowedPaths: resolveDevToolsAllowedPaths(cfg),
		// S5 Phase F: thread the shared directory registrar so the GUI
		// chat boot-profile launch path resolves registry-primary via
		// launchplan.Build — the same seam the standalone launcher uses.
		AgentRegistry: agentRegistry,
		// CW-20260514-0047: thread the configured boot-profile catalog
		// path so the service container builds an in-memory registry of
		// compiled LaunchSpec entries. ResolvedBootProfileCatalogPath
		// returns an empty string when the field is unset, in which
		// case the registry constructor produces an inert (empty)
		// Registry and the dropdown surfaces only DB-seeded providers.
		BootProfileCatalogPath: resolveBootProfileCatalogPath(cfg),
		// Durable-agent recipe catalog files/dirs merge with built-ins at
		// startup through the app config seam used for product tunables.
		DurableAgentRecipeCatalogPaths: appCfg.Recipes.CatalogPaths,
	})
	if err != nil {
		slogx.Fatal("failed to create service container", "err", err)
	}

	// CW-20260813-0011: wire the StepExecutor implementation
	// (CW-20260813-0009) behind the workflow_execute_llm_step /
	// workflow_execute_tool_step / workflow_verify_step MCP callback
	// tools. container.Tools already satisfies service.ToolService;
	// registry (built by initProviders) already satisfies
	// service.WorkflowProviderResolver — no new adapters needed.
	//
	// CW-20260814-0001: also wire a WorkflowContextAssembler so
	// LLMStepRequest.EnableContextAssembly has something to resolve
	// against — reuses container.Sessions/Agents/Store/Context exactly as
	// generateResponse does, rather than a second context-assembly path.
	workflowContextAssembler := service.NewWorkflowContextAssembler(container.Sessions, container.Agents, container.Store, container.Context)
	workflowStepExecutor := service.NewWorkflowStepExecutor(container.Tools, registry, workflowContextAssembler)
	selfTools.WorkflowExecutor = workflowStepExecutor

	// CW-20260813-0014: wire the built-in engine (CW-20260813-0010) behind
	// the workflow_run self-tool and dispatch's RoleWorkflow outcome. The
	// same workflowStepExecutor instance above backs both the external-
	// engine MCP callbacks and this in-process launch path — "engines
	// only sequence, harness always executes" holds identically for both
	// (design doc). workflowDefinitionsRegistry is empty+inert when
	// workflow_definitions_path is unset.
	workflowDefinitionsRegistry, err := agentworkflow.LoadRegistryDir(resolveWorkflowDefinitionsPath(cfg))
	if err != nil {
		slogx.Fatal("failed to load workflow definitions registry", "err", err)
	}
	slog.Info("workflow definitions registry loaded",
		"path", resolveWorkflowDefinitionsPath(cfg), "count", len(workflowDefinitionsRegistry.Names()))
	workflowEngine := service.NewBuiltinWorkflowEngine(container.Store)
	workflowEngines := map[string]agentworkflow.WorkflowEngine{agentworkflow.EngineBuiltin: workflowEngine}

	// CW-20260814-0003: wire the external-engine invocation path — a
	// WorkflowDefinition with Engine: "langgraph"/"crewai" now reaches a
	// real subprocess via internal/workflowrunner.Launch instead of being
	// unregistered/unreachable. The runner scripts are embedded into this
	// binary (internal/workflowrunner's go:embed) and materialized under
	// the app's state dir so a Cerberus-deployed binary — which ships
	// alone, no repo checkout alongside it — can still locate them.
	// Materialization failure disables only the external engines (logged,
	// not fatal): the built-in engine and every workflow that doesn't
	// name an external engine are unaffected.
	workflowScriptsDir := filepath.Join(serveLayout.StateDir(), "workflow-runner-scripts")
	if scriptPaths, wsErr := workflowrunner.MaterializeScripts(workflowScriptsDir); wsErr != nil {
		slog.Warn("workflow-runner: failed to materialize external-engine scripts, langgraph/crewai engines unavailable",
			"dir", workflowScriptsDir, "err", wsErr)
	} else {
		externalBinPath := ""
		if exe, exeErr := os.Executable(); exeErr == nil {
			externalBinPath = launcher.ResolveBinaryPath(exe)
		} else {
			slog.Warn("workflow-runner: os.Executable failed, langgraph/crewai engines unavailable", "err", exeErr)
		}
		externalEngineSpecs := []struct {
			name       string
			scriptFile string
		}{
			{agentworkflow.EngineLangGraph, "langgraph_runner.py"},
			{agentworkflow.EngineCrewAI, "crewai_runner.py"},
			{agentworkflow.EngineGoogleADK, "google_adk_runner.py"},
			{agentworkflow.EngineAutoGen, "autogen_runner.py"},
			{agentworkflow.EngineLangChain, "langchain_runner.py"},
		}
		for _, spec := range externalEngineSpecs {
			scriptPath, ok := scriptPaths[spec.scriptFile]
			if !ok {
				continue
			}
			extEngine, extErr := service.NewExternalWorkflowEngine(service.ExternalWorkflowEngineConfig{
				EngineName:       spec.name,
				PythonPath:       "python3",
				ScriptPath:       scriptPath,
				NaniteBinaryPath: externalBinPath,
				DBPath:           *dbPath,
				APIBaseURL:       apiBaseURL,
			})
			if extErr != nil {
				slog.Warn("workflow-runner: failed to construct external engine, disabled",
					"engine", spec.name, "err", extErr)
				continue
			}
			workflowEngines[spec.name] = extEngine
		}
		slog.Info("workflow-runner: external engines registered",
			"dir", workflowScriptsDir, "engines", len(workflowEngines)-1)
	}

	workflowLauncher := service.NewWorkflowLauncher(workflowDefinitionsRegistry, workflowEngines, workflowStepExecutor, container.DurableAgents)
	selfTools.WorkflowLauncher = service.NewDispatchWorkflowLauncher(workflowLauncher)
	// CW-20260815-0022: same registry instance, wired separately so
	// callWorkflowRun can look up a named workflow's required inputs
	// ahead of Launch — see WorkflowRegistry's doc comment.
	selfTools.WorkflowRegistry = workflowDefinitionsRegistry

	// CW-20260814-0014: A2A Agent Card generator for /.well-known/agent-card.json
	// Uses the same workflow registry + boot profile registry to derive skills.
	container.AgentCardGenerator = service.NewAgentCardGenerator(
		workflowDefinitionsRegistry,
		container.BootProfiles,
		apiBaseURL,
		version.Full(),
	)

	// CW-20260814-0015, CW-20260814-0016: A2A TaskManager for JSON-RPC task methods.
	// Routes Task submissions to workflow launch or durable-agent wake.
	// container.DurableAgents is threaded through so CancelTask can reuse
	// the existing RequestStop primitive for instance-target tasks.
	container.TaskManager = service.NewTaskManager(
		s,
		workflowLauncher,
		container.DurableWake,
		container.DurableAgents,
		workflowDefinitionsRegistry,
		slog.Default(),
	)

	// Wire todo/plan store into the self-tools transport.
	selfTools.TodoStore = s
	selfTools.Messaging = container.Messaging
	selfTools.Subagent = container.Subagent
	selfTools.Background = container.Background
	selfTools.Work = container.Streams
	// Task 34: wire the shared managed-agent write path's classifier so
	// agent_create/agent_update gate on ManageClass.Editable() the same
	// way the REST API's requireMutableAgent does — container.AgentConfig
	// already carries the real writable-managed-roots configuration
	// (project .nanite / user nanite data dir), so this reuses it rather
	// than re-deriving a second, possibly-diverging classification.
	selfTools.AgentClassifier = container.AgentConfig
	// G4 (CW-20260420-0018): wire elicitation service so write tools
	// (e.g. message_send kind=directive) can request mid-call
	// user confirmation via elicitation/create.
	selfTools.Elicitation = container.Elicitation

	// CW-20260421-0010 (B3): wire the executeTask dispatch primitive.
	// Adapts subagent.Service.Spawn to dispatch.Spawner so the chat
	// agent's task_execute tool can drive role-based dispatch.
	if container.Subagent != nil {
		selfTools.Dispatch = service.NewDispatchSpawner(container.Subagent, s)
		// DispatchWrapper left nil — the transport falls back to
		// dispatch.DefaultEnvelopeWrapper when unset.
	}

	// CW-20260509-0046: install the upstream agent-broker instance on
	// the SelfToolsTransport's downstream scaffold seam. The same
	// instance was threaded into ContainerConfig.AgentBroker above —
	// chatServiceImpl now consults the broker BEFORE the chat-loop
	// entry (load-bearing decision); the downstream wire-up here
	// preserves the CW-20260502-0005 audit-into-event_log behavior on
	// the dispatch CALL (callExecuteTask). Sharing one broker is safe
	// — DeterministicBroker is stateless / concurrency-safe.
	selfTools.Broker = agentBrokerInstance

	// CW-20260429-0036 (B2 closing piece): wire the dispatch_executor
	// self-tool to the B3 in-process envelope_render executor pilot.
	// The chat-agent-facing capability bullet B5 added to the chat prompt
	// now resolves to a callable destination; the executor-handoff loop
	// closes (B5 cue → B4 surface narrowing → this tool → B3 executor →
	// returned envelope). Stateless executor — independent of the chat
	// service's route_dispatch instance is intentional (different lane:
	// chat service drives the upstream side-channel route hint, the
	// self-tool is the agent-initiated downstream invocation).
	selfTools.Executor = envelope_render.New()

	// CW-20260426-0006 (J8 v1): wire panel-control surface.
	//   - PanelSignalSink — push panel_signal SSE events on the originating session.
	//   - PanelLookup — enumerate plugin-registered panels for the access check.
	//   - TrustResolver — H1 gate for plugin-shipped panels (built-ins skip the gate).
	selfTools.PanelSignalSink = container.Streams
	selfTools.PanelLookup = func() []string {
		entries := pluginHost.GetPanels()
		ids := make([]string, len(entries))
		for i, e := range entries {
			ids[i] = e.ID
		}
		return ids
	}
	selfTools.TrustResolver = s
	// J11 (CW-20260426-0009): wire the reminder engine so RegisterTurnCount
	// calls from reminder_set hit the correct shared Engine instance.
	selfTools.ReminderEngine = container.ReminderEngine

	// D1 (CW-20260429-0009): wire the Vanta-backed learning recorder
	// + recaller used by lesson_capture and the lesson-recall slot
	// extension. memory.Service satisfies the learnings.LearningStore
	// interface; when it is nil (Conduit not initialised) both wires
	// stay nil and the self-tool returns a clear errorResult.
	if container.Memory != nil {
		selfTools.LearningRecorder = learnings.NewRecorder(container.Memory)
		selfTools.LearningRecaller = learnings.NewRecaller(container.Memory)
	}

	// Restore non-terminal tasks from SQLite snapshot into coordination store.
	if container.Tasks != nil {
		if err := container.Tasks.Restore(context.Background()); err != nil {
			slog.Warn("task restore", "err", err)
		}
	}

	// Clean up orphaned worktrees from previous runs.
	if container.Worktrees != nil {
		if cleaned, wtCleanErr := container.Worktrees.CleanupOrphaned(nil); wtCleanErr != nil {
			slog.Warn("worktree orphan cleanup", "err", wtCleanErr)
		} else if cleaned > 0 {
			slog.Info("worktree cleanup: removed orphaned worktrees", "count", cleaned)
		}
	}

	// Wire plugin host command registry from the container.
	pluginHost.SetCommandRegistry(container.Commands)
	pluginHost.SetEnvelopeConsumer(container.Streams)
	pluginHost.RegisterService("container", container)
	if container.Tasks != nil {
		pluginHost.RegisterService("tasks", container.Tasks)
	}

	// Create API layer.
	a := api.New(container)
	// Back POST /api/tools/call with the fully-wired self-tools transport
	// so a CLI-launched chat agent's `nanite mcp` subprocess can forward
	// self-tool calls into this running harness.
	a.SetSelfTools(selfTools)

	// --- S5 Phase C: agent-source handle registration ---
	//
	// agentRegistry was built earlier (before NewContainer) so the SAME
	// instance is shared by the chat service and this registration. Here
	// we register Nanite as an `agent-source` resolver HANDLE pointing
	// back at the loopback /api/tools/call endpoint (operation
	// agent_source_resolve). The directory holds the handle only — never
	// agent bodies (D2). A registration failure is logged and does NOT
	// crash startup: the registry is never mandatory (D1).
	loopbackToolsURL := fmt.Sprintf("http://127.0.0.1:%d/api/tools/call", *port)
	handleDir := filepath.Join(os.TempDir(), brand.ID+"-agent-source")
	if home, herr := os.UserHomeDir(); herr == nil {
		handleDir = filepath.Join(home, "."+brand.ID, "registry")
	}
	if err := agentregistry.RegisterAgentSource(agentRegistry, loopbackToolsURL, handleDir, slog.Default()); err != nil {
		slog.Warn("agent-source registration failed; continuing without directory handle", "err", err)
	}

	// Lifecycle manager for long-running daemon goroutines (cleanup,
	// snapshots, reapers). Owned by cmdServe; shut down on signal before
	// container.Shutdown so daemons stop referencing container state.
	daemonLifecycle := lifecycle.NewManager("cmd.nanite.daemons")

	// Shutdown handler. Uses context.Background() because cmdServe has no
	// parent ctx at this scope; the goroutine lives until the process exits.
	safego.Go(context.Background(), "cmd.nanite.signal-handler", func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		slog.Info("shutting down")
		if err := daemonLifecycle.Shutdown(10 * time.Second); err != nil {
			slog.Error("daemon lifecycle shutdown", "err", err)
		}
		container.Shutdown()
		os.Exit(0)
	})

	// Start periodic background workers (cleanup, snapshots, reapers).
	startBackgroundWorkers(daemonLifecycle, container)

	// Start HTTP server.
	srv := server.New(s, a, *port, *dev, pluginHost, appCfg.HTTP)

	// Discover, load plugins, and re-discover MCP tools.
	pluginsDir := discoverAndLoadPlugins(pluginHost, mcpManager, s)
	srv.SetPluginsDir(pluginsDir)

	if err := srv.ListenAndServe(); err != nil {
		slogx.Fatal("server error", "err", err)
	}
}

// initProviders constructs the provider.Registry plus the slice of go-providers
// CLI adapters threaded into the agent-runtime composition root (Phase 4c.1
// of agent-boot adoption). For claude, dev-mode wiring uses the go-providers
// DevPTY constructor which sets SkipPermissions=true in the adapter itself —
// CW-20260515-0003 dropped the legacy skipPermsAdapter wrapper that used to
// append --dangerously-skip-permissions externally.
func initProviders(devMode bool) (*provider.Registry, []provider.CLIAdapter, *providercatalog.Catalog) {
	registry := provider.NewRegistry()
	catalog := providercatalog.New()

	resolveKey := func(providerID string) string {
		return secrets.Get(secrets.ProviderKeyName(providerID))
	}

	// CW-20260526-0001: apiProvSpec carries the catalog metadata
	// (displayName, provID) inline with the registry registration so the
	// dropdown auto-surfaces every successfully-registered provider. A new
	// API provider is one literal here instead of (a) initProviders +
	// (b) seededProviders + (c) AllSeeded.
	type apiProvSpec struct {
		name, displayName, provID string
		create                    func() llmcontracts.Provider
		setKey                    func(llmcontracts.Provider, string)
	}
	apiProviders := []apiProvSpec{
		{"anthropic", "Anthropic", "anthropic-001",
			func() llmcontracts.Provider {
				ap := nllmanthropic.New()
				if v := os.Getenv("NANITE_PROVIDER_RATE_BUDGET_TPM"); v != "" {
					if n, err := strconv.Atoi(v); err == nil && n > 0 {
						ap.RateTracker.UpdateLimit(n)
						slog.Info("provider: rate-budget override applied via env",
							"provider", "anthropic", "tpm", n)
					}
				}
				return ap
			},
			func(p llmcontracts.Provider, k string) { p.(*nllmanthropic.Client).SetAPIKey(k) }},
		{"openai", "OpenAI", "openai-001",
			// CW-20260508-0012: SDK-backed wrapper (replaces deleted
			// go-providers HTTP openai client). Implements
			// llmcontracts.Provider; no rate-budget plumbing per spike
			// verdict (parity deferred to followup
			// followups.nanite.cw_20260508_0012.openai_rate_budget_parity).
			func() llmcontracts.Provider { return nllmopenai.New("", nil) },
			func(p llmcontracts.Provider, k string) { p.(*nllmopenai.Client).SetAPIKey(k) }},
	}

	var registeredAPI, missingAPI []string
	for _, spec := range apiProviders {
		key := resolveKey(spec.provID)
		if key != "" {
			p := spec.create()
			spec.setKey(p, key)
			registry.Register(spec.name, p)
			catalog.Add(providercatalog.Entry{
				Name:        spec.name,
				DisplayName: spec.displayName,
				RowID:       spec.provID,
			})
			slog.Info("provider registered (key from keychain)", "provider", spec.name)
			registeredAPI = append(registeredAPI, spec.name)
		} else {
			missingAPI = append(missingAPI, spec.name)
		}
	}

	if len(missingAPI) > 0 && len(registeredAPI) == 0 {
		slog.Warn("no API providers configured — chat will not work")
	}

	// Phase 4c.6 (CW-20260508-0002): provider.PTYBridge / SubprocessBridge
	// registry registrations deleted. CLI agents (claude / codex / opencode)
	// now spawn through internal/runtime/agent.Boot; the CLIAdapter slice
	// still feeds the agent runtime composition root via
	// ContainerConfig.CLIAdapters so the per-provider bootdir layouts can
	// resolve their adapter binaries.
	//
	// CW-20260508-0010: pruned gemini/copilot/aider/junie/kiro/qwen
	// registrations — they were never reached by any production code path
	// (factory.shouldUsePTY only matches claude/codex/opencode shapes).
	// Adapters remain in go-providers for other portfolio consumers
	// (agent-mux, clockwork-manifold).
	//
	// CW-20260515-0004: use the StreamingStdio constructor so claude
	// launches as a long-lived `-p --input-format stream-json
	// --output-format stream-json --verbose` process that reads NDJSON
	// `{"type":"user",...}` per-turn payloads from stdin and emits
	// stream-json events on stdout. ParseLineEvents in go-providers
	// (pty_claude_events.go) is built for exactly this NDJSON shape.
	//
	// History: CW-20260515-0003 swapped to NewClaudeAdapterPTY which
	// emitted bare-claude (TUI) argv. That fixed the c200 print-mode
	// crash but produced c202's silent hang — claude TUI ran fine in
	// the allocated PTY but its output is ANSI/screen redraws which
	// ParseLineEvents cannot decode. Sessions stayed state=running
	// forever with zero assistant deltas. StreamingStdio is the
	// "long-lived claude that the framework can talk to programmatically"
	// shape go-providers was designed for. Dev mode uses the matching
	// DevStreamingStdio constructor (sets SkipPermissions=true in the
	// adapter itself).
	var claudeAdapter provider.CLIAdapter = provider.NewClaudeAdapterStreamingStdio()
	if devMode {
		claudeAdapter = provider.NewClaudeAdapterDevStreamingStdio()
	}
	cliAdapters := []provider.CLIAdapter{
		claudeAdapter,
		provider.NewCodexAdapter(),
		provider.NewOpencodeAdapter(),
	}

	return registry, cliAdapters, catalog
}

// resolveDevToolsAllowedPaths returns the effective directory allow-list.
// It scopes the dev_* MCP tools AND, since CW-20260518-0075, the CLI-launch
// boot dirs (codex [sandbox_workspace_write] writable_roots, claude
// permissions.additionalDirectories) — both consume the value returned here.
//
// Trust-agent redesign (CW-20260430-0009):
//
//  1. If cfg.DevToolsAllowedPaths is non-nil, the user-supplied list is
//     used wholesale (matches the existing override semantics for
//     WritePaths/ProtectedPaths) — including the explicit empty list
//     `dev_tools_allowed_paths: []` which means "no implicit access".
//  2. Otherwise the default is project root only (cfg.ProjectRoot() if
//     set) — NOT cwd, NOT a hardcoded user-specific list. Wider scope
//     comes from explicit-mention grants registered into the per-session
//     PathGrants store at user-message ingest time (Q1-Q3 of the locked
//     design), or from cfg.dev_tools_allowed_paths.
//
// The cwd fallback that used to live here was dropped per Q4: cwd-as-
// baseline risks scope leak when the user starts a session in `~/`.
//
// Hardcoded user-specific defaults (`~/Projects-apps`, etc.) were
// removed by the SP5 hot-fix (commit 151fc7b) — see audit
// docs/audits/hardcoded-paths.md for the full disposition table.
func resolveDevToolsAllowedPaths(cfg *config.Config) []string {
	if cfg != nil && cfg.DevToolsAllowedPaths != nil {
		return cfg.ResolvedDevToolsAllowedPaths()
	}
	if cfg != nil {
		if root := cfg.ProjectRoot(); root != "" {
			return []string{root}
		}
	}
	return nil
}

// resolveBootProfileCatalogPath resolves the configured boot-profile
// catalog root (CW-20260514-0047). Returns an empty string when the
// field is unset, which the registry constructor interprets as
// "no catalog" — the dropdown shows only DB-seeded providers and
// behavior is identical to before this feature. Tilde expansion is
// delegated to config.ResolvedBootProfileCatalogPath so the rules
// stay consistent with how every other path-shaped config field is
// handled.
func resolveBootProfileCatalogPath(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	return cfg.ResolvedBootProfileCatalogPath()
}

// resolveWorkflowDefinitionsPath resolves the configured workflow
// definitions directory (CW-20260813-0014). Returns an empty string when
// the field is unset, which agentworkflow.LoadRegistryDir interprets as
// "no directory" — an empty, inert Registry.
func resolveWorkflowDefinitionsPath(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	return cfg.ResolvedWorkflowDefinitionsPath()
}

// devAllowedSource returns a short string describing where the dev tools
// allow-list came from, purely for log observability when sessions hit a
// path-escape error.
func devAllowedSource(cfg *config.Config) string {
	if cfg != nil && cfg.DevToolsAllowedPaths != nil {
		return "config:dev_tools_allowed_paths"
	}
	if cfg != nil && cfg.ProjectRoot() != "" {
		return "default:project_root"
	}
	return "default:none"
}

// initMCP sets up the MCP manager with built-in and user-configured servers,
// runs auto-discovery, and creates the tool broker. CW-20260420-0047.
//
// The mux-orchestrator transport and tool registration is gated behind the
// devmode build tag via registerMuxTransport (G5 — CW-20260421-0001).
// In production builds registerMuxTransport is a no-op and no mux_* tools
// appear in the tool surface.
func initMCP(s *store.Store, cfg *config.Config, appCfg *config.AppConfig) (*mcp.Manager, *toolclient.ToolClient, *mcp.SelfToolsTransport) {
	mcpManager := mcp.NewManager()

	devAllowed := resolveDevToolsAllowedPaths(cfg)
	slog.Info("dev tools allow-list", "paths", devAllowed, "source", devAllowedSource(cfg))

	// SP-20260512-0008 W2C (CW-20260512-0110): wire the artifact resolver
	// into dev_tools so dev_read(artifact_id=...) can resolve Context
	// Broker stash pointers to the on-disk artifact body. Resolver +
	// artifacts root mirror the API-layer download handler so both
	// surfaces address the same files.
	artifactsRoot := "data/artifacts"
	if appCfg != nil && appCfg.Artifacts.StorageDir != "" {
		artifactsRoot = appCfg.Artifacts.StorageDir
	}
	devTools := mcp.NewDevToolsTransport(devAllowed).WithArtifactResolver(
		mcp.NewStoreArtifactResolver(s), artifactsRoot,
	)
	// Builtin registrations use AddBuiltinServer (not the plain AddServer
	// + TierBuiltin pair) — it both registers the server and marks its
	// name as first-party-protected in the same call, so a fifth builtin
	// server only needs one new line here (internal/mcp/naming.go no
	// longer carries a second, hand-maintained name list to keep in sync).
	if err := mcpManager.AddBuiltinServer(mcp.DevServerName, devTools); err != nil {
		slog.Error("mcp: failed to register builtin server", "name", mcp.DevServerName, "err", err)
	}
	if err := mcpManager.AddBuiltinServer(mcp.GeneralServerName, mcp.NewGeneralToolsTransport()); err != nil {
		slog.Error("mcp: failed to register builtin server", "name", mcp.GeneralServerName, "err", err)
	}
	if err := mcpManager.AddBuiltinServer(mcp.CodeServerName, mcp.NewCodeExecTransport("")); err != nil {
		slog.Error("mcp: failed to register builtin server", "name", mcp.CodeServerName, "err", err)
	}
	selfTools := mcp.NewSelfToolsTransport(s)
	// E1 (CW-20260419-0027): wire reflex set + logger into the dispatch path.
	// LoadUserReflexes returns nil on a missing dir (not an error); merge with
	// builtins so user overrides with priority>=50 reliably beat built-ins.
	userReflexes, err := promptrouter.LoadUserReflexes("")
	if err != nil {
		slog.Warn("reflex: failed to load user overrides", "err", err)
	}
	selfTools.ReflexSet = promptrouter.MergeReflexes(promptrouter.BuiltinReflexes(), userReflexes)
	selfTools.ReflexLogger = s
	// B1 (CW-20260429-0006): wire the manager as the cross-server schema
	// registry so tool_validate can pre-flight check args for any
	// registered tool, not just self-tools.
	selfTools.SchemaLookup = mcpManager
	// CW-20260501-0001: wire the manager as the cross-server tool
	// inventory so tool_list enumerates every registered MCP tool
	// (self, dev, general, plugin) — not just the in-process self-tools.
	// Without this, sibling-server tools are invisible to the discovery
	// primitive.
	selfTools.Inventory = mcpManager
	if err := mcpManager.AddBuiltinServer(mcp.SelfServerName, selfTools); err != nil {
		slog.Error("mcp: failed to register builtin server", "name", mcp.SelfServerName, "err", err)
	}

	// CW-20260501-0005 sub-ticket 2: register Vanta MCP server when configured.
	// Vanta is the durable memory/knowledge/context substrate (vanta-primary-since
	// 2026-04-19). Trust tier defaults to plugin_http per docs/mcp-trust-model.md
	// (Vanta is the user's own infrastructure, not third-party). Tools added to
	// the chat surface in sub-ticket 3 (rollout); this scaffold only registers
	// the server so AutoDiscover picks up the tool list.
	registerVantaServer(mcpManager, cfg)

	loadPersistedMCPServers(s, mcpManager)
	// Both the MCPManager's broker and the ToolClient's Config must share
	// the same ruleset (CW-20260815-0011): constructing them from two
	// disconnected calls — one to the go-toolbroker library's own
	// DefaultRules() here, one to toolclient.New(..., nil) below (which
	// silently defaulted to library rules too) — meant Nanite's real
	// ruleset (toolclient.DefaultConfig / NaniteDefaultRules) was never
	// actually in effect for either broker instance.
	toolCfg := toolclient.DefaultConfig()
	mcpManager.Broker = broker.NewLocalBroker(nil, toolCfg.Rules)
	if diff, err := mcpManager.AutoDiscover(context.Background(), s); err != nil {
		slog.Warn("MCP auto-discovery failed", "err", err)
	} else {
		slog.Info("MCP auto-discovery",
			"total", diff.Total,
			"added", len(diff.Added),
			"removed", len(diff.Removed))
	}

	tb := toolclient.New(mcpManager, s, toolCfg)
	if mcpManager.Broker != nil {
		tb.LocalBroker = mcpManager.Broker
		slog.Info("toolclient: sharing MCPManager broker", "tool_summaries", len(mcpManager.Broker.AllTools()))
	}
	devToolDefs := mcp.DevToolProviderDefinitions()
	tb.Builtins.RegisterBuiltins("dev", devToolDefs)
	slog.Info("registered dev built-in tools", "count", len(devToolDefs))

	selfToolDefs := mcp.SelfToolProviderDefinitions()
	tb.Builtins.RegisterBuiltins("self-service", selfToolDefs)
	slog.Info("registered self-service built-in tools", "count", len(selfToolDefs))

	// Per-call description-render hook (CW-20260512-0105 / SP-20260512-0008
	// W1B). Opt-in: only the named adopters (task_execute, tool_list,
	// skill_list) get caller-specific descriptions; all other tools emit
	// their static registration-time string. See
	// internal/mcp/self_tools_describer.go for the renderer bodies.
	mcp.RegisterSelfToolDescribers(tb.Describers)
	slog.Info("registered self-tool describers", "count", tb.Describers.Count())

	// Register result-cache meta-tools (S4a). These let the LLM recall
	// truncated tool results via fetch_tool_result / search_tool_result.
	tb.Builtins.RegisterBuiltins("result-cache", []llmtypes.ToolDefinition{
		toolclient.FetchToolResultMetaTool(),
		toolclient.SearchToolResultMetaTool(),
	})

	return mcpManager, tb, selfTools
}

// startBackgroundWorkers launches periodic goroutines for cleanup, snapshots,
// and reapers on the supplied lifecycle manager. Each daemon's inner loop
// selects on ctx.Done() so Shutdown drains them deterministically.
func startBackgroundWorkers(lc *lifecycle.Manager, container *service.Container) {
	// Periodic cleanup of saved tool outputs.
	lc.Go("truncate-cleanup", func(ctx context.Context) {
		truncate.Cleanup()
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				truncate.Cleanup()
			}
		}
	})

	// Periodic task snapshot (flush Badger state to SQLite).
	if container.Tasks != nil {
		lc.Go("task-snapshot", func(ctx context.Context) {
			ticker := time.NewTicker(60 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if err := container.Tasks.Snapshot(ctx); err != nil {
						slog.Warn("task snapshot", "err", err)
					}
				}
			}
		})
	}

	// Periodic stale process reaper.
	lc.Go("stale-process-reaper", func(ctx context.Context) {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if killed := container.ProcessTracker.KillStale(5 * time.Minute); killed > 0 {
					slog.Info("stale process reaper: killed hung CLI processes", "count", killed)
				}
			}
		}
	})

	// Periodic stale worker reaper.
	if container.Workers != nil {
		lc.Go("stale-worker-reaper", func(ctx context.Context) {
			ticker := time.NewTicker(2 * time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if stale := container.Workers.ReapStale(60 * time.Second); len(stale) > 0 {
						slog.Info("stale worker reaper: reaped workers", "count", len(stale))
					}
				}
			}
		})
	}

	// Periodic A2A push notification delivery processor.
	// CW-20260814-0018: Best-effort push delivery with bounded retries (max 3,
	// exponential backoff). Deliveries are enqueued on TaskState transitions.
	if container.TaskManager != nil {
		lc.Go("a2a-push-delivery", func(ctx context.Context) {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if err := container.TaskManager.PushNotifier().ProcessPendingDeliveries(ctx); err != nil {
						slog.Warn("a2a push delivery: processing failed", "error", err)
					}
				}
			}
		})
	}

	// Periodic durable-agent wake tick.
	// CW-20260816-0005: RunDue fires class:process durable agent instances
	// (e.g. Atlas Curator's nightly schedule) whose agent_schedules entry is
	// due. It previously had no internal ticker and depended entirely on an
	// external caller hitting POST /api/durable-agent-wake/run-due. RunDue is
	// idempotent and cheap (a due-ness check per instance), so a 2-minute
	// interval — matching stale-worker-reaper's cadence — gives schedules a
	// tight enough resolution for both Atlas Curator today and Loom Curator's
	// planned lint+export tick, without adding meaningful load.
	lc.Go("durable-agent-wake-tick", func(ctx context.Context) {
		ticker := time.NewTicker(2 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				result, err := container.DurableWake.RunDue(ctx, service.DurableAgentWakeRunRequest{Now: time.Now()})
				if err != nil {
					slog.Warn("durable agent wake tick: run due failed", "error", err)
					continue
				}
				woken := 0
				for _, r := range result.Results {
					if !r.Skipped {
						woken++
					}
				}
				if woken > 0 {
					slog.Info("durable agent wake tick: woke durable agent instances", "count", woken)
				}
			}
		}
	})
}

// discoverAndLoadPlugins finds plugins on disk, loads them and builtins,
// then re-runs MCP auto-discovery for any new servers plugins registered.
//
// CW-20260517-0061: pluginsDir is resolved by the shared resolvePluginsDir()
// helper (NANITE_PLUGINS_DIR override, else the repo-shipped ./plugins). It is
// no longer derived from filepath.Dir(dbPath) — plugins/ is a repo-shipped
// directory and must not follow the DB into the go-apppaths data root.
func discoverAndLoadPlugins(pluginHost *plugin.Host, mcpManager *mcp.Manager, s *store.Store) string {
	pluginsDir := resolvePluginsDir()
	if discovered, discErr := plugin.DiscoverPlugins(pluginsDir); discErr != nil {
		slog.Warn("plugin discovery failed", "err", discErr)
	} else {
		loaded, loadErrs := plugin.LoadDiscovered(pluginHost, discovered)
		for _, e := range loadErrs {
			slog.Warn("plugin load", "err", e)
		}
		slog.Info("plugins discovered", "discovered", len(discovered), "loaded", len(loaded))
	}
	if builtins, builtinErrs := plugin.LoadRegisteredBuiltins(pluginHost); len(builtins) > 0 {
		for _, e := range builtinErrs {
			slog.Warn("plugin builtin load", "err", e)
		}
		slog.Info("plugins: loaded builtins", "count", len(builtins))
	}

	// Re-discover tools after plugins (they may register new MCP servers).
	if postDiff, err := mcpManager.AutoDiscover(context.Background(), s); err != nil {
		slog.Warn("post-plugin MCP discovery failed", "err", err)
	} else if len(postDiff.Added) > 0 {
		slog.Info("post-plugin MCP discovery: new tools added", "count", len(postDiff.Added), "tools", postDiff.Added)
	}

	return pluginsDir
}

// loadPersistedMCPServers loads user-configured MCP servers from the database
// and registers them with the MCP manager.
func loadPersistedMCPServers(s *store.Store, m *mcp.Manager) {
	servers, err := s.ListMCPServers()
	if err != nil {
		slog.Warn("failed to load persisted MCP servers", "err", err)
		return
	}

	for _, cfg := range servers {
		if !cfg.Enabled {
			continue
		}
		switch cfg.TransportType {
		case "stdio":
			var args []string
			if cfg.Args != "" && cfg.Args != "[]" {
				json.Unmarshal([]byte(cfg.Args), &args)
			}
			var envVars []string
			if cfg.Env != "" && cfg.Env != "[]" {
				json.Unmarshal([]byte(cfg.Env), &envVars)
			}
			var envAllowlist []string
			if cfg.EnvAllowlist != "" && cfg.EnvAllowlist != "[]" {
				if err := json.Unmarshal([]byte(cfg.EnvAllowlist), &envAllowlist); err != nil {
					slog.Warn("mcp: malformed env_allowlist json — ignoring",
						"name", cfg.Name, "err", err)
					envAllowlist = nil
				}
			}
			if err := m.AddStdioServer(cfg.Name, cfg.Command, args, envVars, envAllowlist, mcp.TrustTier(cfg.TrustTier)); err != nil {
				slog.Warn("mcp: failed to register persisted stdio server", "name", cfg.Name, "err", err)
			}
		case "sse":
			if err := m.AddHTTPServer(cfg.Name, cfg.URL, mcp.TrustTier(cfg.TrustTier)); err != nil {
				slog.Warn("mcp: failed to register persisted http server", "name", cfg.Name, "err", err)
			}
		default:
			slog.Warn("mcp: unknown transport type, skipping", "transport", cfg.TransportType, "server", cfg.Name)
		}
	}

	if len(servers) > 0 {
		slog.Info("mcp: loaded user-configured servers from database", "count", len(servers))
	}
}

// registerVantaServer registers the Vanta MCP HTTP server when configured.
// Reads cfg.Vanta and the NANITE_VANTA_TOKEN environment variable; the env
// var overrides the YAML token so the secret can stay out of config files.
//
// Trust tier defaults to plugin_http (Vanta is the user's own infrastructure,
// per docs/mcp-trust-model.md and the orchestrator decision in
// CW-20260501-0005). Override with cfg.Vanta.TrustTier if needed.
//
// When cfg is nil or cfg.Vanta.URL is empty, the function is a no-op — Vanta
// integration is opt-in. CW-20260501-0005 sub-ticket 2.
func registerVantaServer(m *mcp.Manager, cfg *config.Config) {
	if cfg == nil || strings.TrimSpace(cfg.Vanta.URL) == "" {
		return
	}

	serverName := strings.TrimSpace(cfg.Vanta.ServerName)
	if serverName == "" {
		serverName = "vanta"
	}

	// Resolve tier. Default plugin_http per the trust model: Vanta is the
	// user's own infrastructure (not third-party HTTP), but it's HTTP-based
	// and outside the in-process builtin set, so plugin_http is the natural
	// fit. Operators who run an in-process Vanta build can override to
	// builtin via cfg.Vanta.TrustTier.
	tier := mcp.TierPluginHTTP
	if t := strings.TrimSpace(cfg.Vanta.TrustTier); t != "" {
		tier = mcp.TrustTier(t)
	}

	// Token: env var wins over YAML so the secret stays out of config files.
	token := strings.TrimSpace(os.Getenv(brand.Env("VANTA_TOKEN")))
	if token == "" {
		token = strings.TrimSpace(cfg.Vanta.Token)
	}

	headers := map[string]string{}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}

	var err error
	if len(headers) > 0 {
		err = m.AddHTTPServerWithHeaders(serverName, cfg.Vanta.URL, headers, tier)
	} else {
		err = m.AddHTTPServer(serverName, cfg.Vanta.URL, tier)
	}
	if err != nil {
		slog.Warn("mcp: failed to register vanta server",
			"name", serverName, "url", cfg.Vanta.URL, "err", err)
		return
	}
	slog.Info("mcp: registered vanta server",
		"name", serverName, "tier", string(tier), "auth", token != "")
}

// cmdMCP dispatches MCP subcommands: serve (default), import, export.
func cmdMCP(args []string) {
	// Check for subcommand before falling through to serve mode.
	if len(args) > 0 {
		switch args[0] {
		case "import":
			mcpImport(args[1:])
			return
		case "export":
			mcpExport(args[1:])
			return
		}
	}

	// Default: run the MCP server on stdio.
	cmdMCPServe(args)
}

// cmdMCPServe runs the Nanite MCP server on stdio. Claude CLI (or any MCP client)
// spawns this as a subprocess and communicates via JSON-RPC on stdin/stdout.
func cmdMCPServe(args []string) {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	// --db default is empty: an unset flag resolves via go-apppaths
	// (CW-20260517-0061). A non-empty flag becomes an explicit WithDBOverride.
	dbFlag := fs.String("db", "", "SQLite database path (default: go-apppaths XDG layout — run `nanite path`)")
	sessionID := fs.String("session", "", "Session ID")
	fs.Parse(args)

	dbPathStr := resolveDBPathWith(*dbFlag)
	dbPath := &dbPathStr

	s, err := store.New(context.Background(), *dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s mcp: open db: %v\n", brand.BinaryName, err)
		os.Exit(1)
	}
	defer s.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	// Reuse the same allow-list resolution path as the main server so the
	// stdio MCP entry point honours config.dev_tools_allowed_paths and
	// falls back to project root + cwd when the field is unset (per
	// CW-20260430-0009 — no hardcoded user-specific defaults). Failures to
	// load the agentrc config fall back to the same default ladder — the
	// stdio path is invoked by external clients (Claude CLI), so degrading
	// gracefully matters more than aborting.
	cfg, _ := config.Load()
	allowedPaths := resolveDevToolsAllowedPaths(cfg)
	// SP-20260512-0008 W2C (CW-20260512-0110): load app config so
	// dev_read(artifact_id=...) resolves stash pointers against the
	// same artifacts root the main server uses. Falls back to the
	// default ("data/artifacts") if the config can't be loaded.
	stdioAppCfg, _ := config.LoadAppConfig("config/" + brand.ConfigFileName + ".yaml")
	if stdioAppCfg == nil {
		stdioAppCfg = config.DefaultAppConfig()
	}
	artifactsRoot := stdioAppCfg.Artifacts.StorageDir
	if artifactsRoot == "" {
		artifactsRoot = "data/artifacts"
	}
	// NANITE_API_URL is planted into the boot dir's .mcp.json by a
	// CLI-launch composition root. When present, self-tool calls forward
	// to that live harness instead of dispatching against this
	// subprocess's bare store (Option A — see internal/api/tools_call.go).
	apiURL := os.Getenv("NANITE_API_URL")
	// NANITE_MCP_TOOL_ALLOWLIST is planted into .mcp.json by
	// internal/workflowrunner's renderMCPJSON for a workflow-runner
	// subprocess ONLY — CLI-launched coding agents' renderMCPJSON
	// (internal/runtime/agent/sandbox_content_mcp.go) never sets it, so
	// they see the unrestricted catalog exactly as before
	// (CW-20260814-0006).
	toolAllowlist := parseToolAllowlist(os.Getenv(workflowrunner.ToolAllowlistEnvVar))
	srv := mcpserver.New(s, *sessionID, allowedPaths, artifactsRoot, apiURL, toolAllowlist)
	if err := srv.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "%s mcp: %v\n", brand.BinaryName, err)
		os.Exit(1)
	}
}

// parseToolAllowlist splits a comma-separated NANITE_MCP_TOOL_ALLOWLIST
// value into tool names, trimming whitespace and dropping empties. Returns
// nil (unrestricted) for an unset/blank env var.
func parseToolAllowlist(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	names := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			names = append(names, p)
		}
	}
	return names
}
