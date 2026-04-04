package service

import (
	"fmt"
	"log"
	"time"

	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/agent/builtin"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/skill"
	skillbuiltin "github.com/hollis-labs/nanite/internal/skill/builtin"
	"github.com/hollis-labs/nanite/internal/config"
	"github.com/hollis-labs/nanite/internal/filter"
	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/provider"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/toolclient"
)

// Container holds all service instances and shared subsystems. It is the
// single wiring point — created once in main.go and passed to the API layer.
type Container struct {
	Sessions  SessionService
	Agents    AgentService
	Skills    SkillService
	Tools     ToolService
	Chat      ChatService
	Context   ContextService
	Streams   *StreamManager
	Events    EventEmitter
	Providers *provider.Registry
	Commands  *chat.CommandRegistry
	Plugins   *plugin.Host
	MCP       *mcp.Manager

	// Subsystems exposed for API handlers that need direct access.
	// These will shrink as more domain services are added.
	Store          *store.Store
	ToolClient     *toolclient.ToolClient
	ProcessTracker *chat.ProcessTracker
	Orchestrator   *chat.Orchestrator
	Activity       *chat.ActivityEmitter

	// Utility provider/model for lightweight calls (autotitle, etc.).
	UtilityProvider string
	UtilityModel    string

	// ModelSelector resolves provider+model for operations (summarization, etc.).
	ModelSelector *provider.StaticModelSelector

	// Permissions is the per-invocation permission engine. nil = permissions disabled.
	Permissions *permission.Engine
}

// ContainerConfig holds all the external dependencies needed to construct
// a Container. Everything that main.go sets up before wiring goes here.
type ContainerConfig struct {
	Store     *store.Store
	Providers *provider.Registry
	MCP       *mcp.Manager
	ToolClient *toolclient.ToolClient
	Plugins   *plugin.Host
	AppConfig *config.AppConfig

	// Optional subsystems — nil-safe.
	Activity     *chat.ActivityEmitter
	OutputFilter *filter.Chain

	// Utility provider/model — read from DB or env by main.go.
	UtilityProvider string
	UtilityModel    string

	// CLI process concurrency limit (0 = unlimited).
	MaxCLIProcesses int
}

// NewContainer wires all services together and returns a ready Container.
func NewContainer(cfg ContainerConfig) (*Container, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("service.NewContainer: Store is required")
	}
	if cfg.Providers == nil {
		return nil, fmt.Errorf("service.NewContainer: Providers is required")
	}

	// --- Foundation (Wave 0) ---

	// Event emitter: fans out to activity + plugin sinks.
	var pluginSink PluginEventSink
	if cfg.Plugins != nil {
		pluginSink = cfg.Plugins
	}
	events := NewCompositeEmitter(cfg.Activity, pluginSink)

	// --- Domain services (Wave 1) ---

	sessions := NewSessionService(SessionServiceDeps{
		Sessions: cfg.Store,
		Writer:   cfg.Store,
		Agents:   cfg.Store,
		Settings: cfg.Store,
		Events:   events,
	})

	// Discover file-based agent definitions from all 6 priority locations.
	agentDefs, err := agent.Discover(agent.DiscoverOptions{
		WorkingDir: ".",
		PluginsDir: "plugins",
	})
	if err != nil {
		log.Printf("service container: agent discovery: %v", err)
	}
	// Append built-in default agent as lowest priority.
	if defaultDef, defErr := builtin.DefaultAgent(); defErr == nil {
		agentDefs = append(agentDefs, defaultDef)
	} else {
		log.Printf("service container: built-in default agent: %v", defErr)
	}
	log.Printf("service container: discovered %d file-based agents", len(agentDefs))

	agents := NewAgentService(AgentServiceConfig{
		Agents:     cfg.Store,
		Writers:    cfg.Store,
		Settings:   cfg.Store,
		Events:     events,
		FileAgents: agentDefs,
	})

	// Discover file-based skill definitions from all 5 priority locations.
	skillDefs, err := skill.Discover(skill.DiscoverOptions{
		WorkingDir: ".",
		PluginsDir: "plugins",
	})
	if err != nil {
		log.Printf("service container: skill discovery: %v", err)
	}
	// Append built-in skills as lowest priority.
	if builtinDefs, bErr := skillbuiltin.BuiltinSkills(); bErr == nil {
		skillDefs = append(skillDefs, builtinDefs...)
	} else {
		log.Printf("service container: built-in skills: %v", bErr)
	}
	log.Printf("service container: discovered %d file-based skills", len(skillDefs))

	skills := NewSkillService(SkillServiceConfig{
		Skills:     cfg.Store,
		FileSkills: skillDefs,
	})

	var agentReader AgentReader = cfg.Store
	tools := NewToolService(cfg.ToolClient, cfg.MCP, agentReader)
	if impl, ok := tools.(*toolServiceImpl); ok {
		impl.SetDecisionLogger(cfg.Store)
	}

	// --- Orchestration (Wave 2) ---

	streams := NewStreamManager()
	if cfg.AppConfig != nil && cfg.AppConfig.Presence.CLIActiveThrottleSeconds > 0 {
		streams.CLIActiveThrottleInterval = time.Duration(cfg.AppConfig.Presence.CLIActiveThrottleSeconds) * time.Second
	}

	contextClient := chat.NewContextClient(cfg.Store)
	ctxService := NewContextService(ContextServiceConfig{Client: contextClient})

	// Command registry.
	commands := chat.NewCommandRegistry()
	commands.RegisterServerCommands(cfg.Store, cfg.Providers)

	// Register file-based skills as slash commands.
	RegisterSkillCommands(commands, skills)

	// Process tracker.
	processTracker := chat.NewProcessTracker()
	if cfg.MaxCLIProcesses > 0 {
		processTracker.MaxProcesses = cfg.MaxCLIProcesses
	}
	// 0 = unlimited (leave at ProcessTracker's zero-value default)

	orchestrator := chat.NewOrchestrator(cfg.Providers, cfg.MCP)

	// Permission engine with default mode. Rules loaded from project/user config at runtime.
	permissions := permission.NewEngine(permission.ModeDefault, nil)

	chatSvc := NewChatService(ChatServiceConfig{
		Sessions:       sessions,
		Agents:         agents,
		Tools:          tools,
		Streams:        streams,
		Context:        ctxService,
		Events:         events,
		Providers:      cfg.Providers,
		Store:          cfg.Store,
		Orchestrator:   orchestrator,
		AppConfig:      cfg.AppConfig,
		OutputFilter:   cfg.OutputFilter,
		Commands:       commands,
		PluginHost:     pluginSink,
		ProcessTracker: processTracker,
		UtilityProvider: cfg.UtilityProvider,
		UtilityModel:    cfg.UtilityModel,
		Permissions:     permissions,
	})

	// Model selector for operation-specific model resolution (e.g., cheap model for summarization).
	modelSelector := provider.NewStaticModelSelector(cfg.UtilityProvider, cfg.UtilityModel)

	log.Println("service container: all services wired")

	return &Container{
		Sessions:        sessions,
		Agents:          agents,
		Skills:          skills,
		Tools:           tools,
		Chat:            chatSvc,
		Context:         ctxService,
		Streams:         streams,
		Events:          events,
		Providers:       cfg.Providers,
		Commands:        commands,
		Plugins:         cfg.Plugins,
		MCP:             cfg.MCP,
		Store:           cfg.Store,
		ToolClient:      cfg.ToolClient,
		ProcessTracker:  processTracker,
		Orchestrator:    orchestrator,
		Activity:        cfg.Activity,
		UtilityProvider: cfg.UtilityProvider,
		UtilityModel:    cfg.UtilityModel,
		ModelSelector:   modelSelector,
		Permissions:     permissions,
	}, nil
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

// Shutdown performs graceful shutdown of all services.
func (c *Container) Shutdown() {
	c.Chat.Shutdown()
	if c.MCP != nil {
		c.MCP.Close()
	}
}
