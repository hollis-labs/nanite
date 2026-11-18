package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"sync"
	"time"

	ctxpkg "github.com/hollis-labs/libs/agent/core/context"
	"github.com/hollis-labs/libs/agent/core/toolresult"
	"github.com/hollis-labs/libs/llm/core/llmcontracts"
	"github.com/hollis-labs/libs/security/policy"
	"github.com/hollis-labs/libs/security/redact"
	"github.com/hollis-labs/nanite/internal/background"
	"github.com/hollis-labs/nanite/internal/definition"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/mcpclient"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/provider"
	"github.com/hollis-labs/nanite/internal/skills"
	"github.com/hollis-labs/nanite/internal/slots"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
	"github.com/hollis-labs/nanite/internal/tools"
)

var ErrBusy = errors.New("nanite: conversation is busy")

type Config struct {
	Events               events.Sink
	Logger               *slog.Logger
	Dir, GlobalSkillsDir string
	Provider             llmcontracts.Provider
	ProviderConfig       provider.Config
	Store                *store.Store
	Gate                 permission.Gate
	PDP                  policy.PDP
	Slots                *slots.Store
	SlotSources          []slots.Source
	SlotSelector         slots.Selector
	Tools                *tools.Groups
	Skills               *skills.Groups
	WorkspaceRoots       []string
}

type Engine struct {
	cfg      Config
	mu       sync.Mutex
	active   map[string]*Run
	previous map[string]map[string]string
	grants   map[string]map[string]bool
	closed   bool
	scrub    *redact.Redactor
	runtime  *events.Runtime
	jobs     *background.Manager
	children subagent.Limit
	notices  chan store.Notice
	mcp      *mcpclient.Client
	cache    *toolresult.Cache
}

func New(cfg Config) (*Engine, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Gate == nil {
		cfg.Gate = permission.DenyGate
	}
	if cfg.SlotSelector == nil {
		cfg.SlotSelector = slots.DefaultSelector(nil)
	}
	cfg.ProviderConfig.Redactor = redact.NewScope()
	scrub, err := redact.New(redact.WithScope(cfg.ProviderConfig.Redactor))
	if err != nil {
		return nil, err
	}
	e := &Engine{cfg: cfg, scrub: scrub, runtime: events.NewRuntime(cfg.Events, cfg.Logger), notices: make(chan store.Notice, 64), mcp: mcpclient.New(cfg.Dir, cfg.ProviderConfig.Secrets, cfg.ProviderConfig.Redactor, cfg.Logger), cache: toolresult.New(cfg.Store.Results(), toolresult.Config{}), active: make(map[string]*Run),
		previous: make(map[string]map[string]string), grants: make(map[string]map[string]bool), children: subagent.NewLimit()}
	e.jobs = background.New(cfg.Store, scrub, e.publishNotice, cfg.Logger)
	return e, nil
}

type Input struct {
	Text  string
	Slots []slots.Update
}

func (e *Engine) Send(ctx context.Context, id string, input Input) (*Run, error) {
	return e.startRun(ctx, id, input, false)
}

func (e *Engine) Resume(ctx context.Context, id string) (*Run, error) {
	return e.startRun(ctx, id, Input{}, true)
}

func (e *Engine) startRun(ctx context.Context, id string, input Input, resume bool) (*Run, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, fmt.Errorf("nanite: agent is closed")
	}
	if e.active[id] != nil {
		return nil, ErrBusy
	}

	info, err := e.cfg.Store.Conversation(ctx, id)
	if err != nil {
		return nil, err
	}
	interrupted, err := e.cfg.Store.Interrupted(ctx, id)
	if err != nil {
		return nil, err
	}
	if interrupted != nil && !resume {
		return nil, fmt.Errorf("conversation has an interrupted turn; resume it before sending")
	}
	if interrupted == nil && resume {
		return nil, fmt.Errorf("conversation has no interrupted turn")
	}
	runID := store.NewID(time.Now())
	if interrupted != nil {
		runID = interrupted.ID
	}

	ctx, cancel := context.WithCancel(ctx)
	r := &Run{id: runID, events: make(chan events.Event, 65), done: make(chan struct{}), cancel: cancel}
	r.observe = func(eventCtx context.Context, event events.Event) {
		e.runtime.Observe(eventCtx, id, r.id, event)
	}

	prev := maps.Clone(e.previous[id])
	state := e.cfg.Slots.Snapshot()
	e.active[id] = r
	go e.execute(ctx, r, info, input, state, prev, e.cfg.Tools.Snapshot(), e.cfg.Skills.Snapshot(), interrupted)
	return r, nil
}

func (e *Engine) Close() {
	e.mu.Lock()
	e.closed = true
	close(e.notices)
	runs := make([]*Run, 0, len(e.active))
	for _, r := range e.active {
		runs = append(runs, r)
		r.Cancel()
	}
	e.mu.Unlock()
	e.jobs.Close()
	for _, r := range runs {
		<-r.done
	}
	if err := e.mcp.Close(); err != nil {
		e.cfg.Logger.Warn("MCP connections did not close cleanly")
	}

}

func (e *Engine) provider(def definition.Definition) (llmcontracts.Provider, error) {
	if e.cfg.Provider != nil {
		return e.cfg.Provider, nil
	}
	cfg := e.cfg.ProviderConfig
	cfg.Provider = def.Runtime.Provider
	return provider.New(cfg)
}

func (e *Engine) assemble(ctx context.Context, input Input, info slots.TurnInfo, def definition.Definition, state slots.Snapshot, prev map[string]string, skillListing string, warnings []string, pending []store.Notice) (slots.Assembled, error) {
	mode := def.Snapshot.Profile.Permissions.Mode
	if mode == "" {
		mode = "default"
	}
	updates := []slots.Update{
		{Slot: slots.Universal, Entry: slots.Entry{Owner: "engine", Key: "rules", Content: "Use the available tools when needed. Respect the workspace and tool permissions."}},
		{Slot: slots.Agent, Entry: slots.Entry{Owner: "file", Key: "instructions", Content: def.Snapshot.Definition.Body}},
		{Slot: slots.Permissions, Entry: slots.Entry{Owner: "engine", Key: "mode", Content: "Permission mode: " + mode + ". File tools are confined to the workspace roots. Approved Bash commands run with the user’s privileges and can access paths outside those roots."}},
		{Slot: slots.Skills, Entry: slots.Entry{Owner: "engine", Key: "available-skills", Content: skillListing}},
	}

	if prompt := subagent.Prompt(info.Role); prompt != "" {
		updates = append(updates, slots.Update{Slot: slots.Mode, Entry: slots.Entry{Owner: "engine", Key: "subagent-role", Content: prompt, Priority: 100}})
	}

	for _, notice := range pending {
		updates = append(updates, slots.Update{Slot: slots.Session, Entry: slots.Entry{Owner: "engine", Key: "notice-" + notice.ID, Priority: 1, Content: notice.Kind + ": " + notice.Body}})
	}

	for i, warning := range warnings {
		updates = append(updates, slots.Update{Slot: slots.Session, Entry: slots.Entry{Owner: "engine", Key: fmt.Sprintf("mcp-warning-%04d", i), Content: warning}})
	}
	for _, file := range def.Context {
		updates = append(updates, slots.Update{Slot: slots.Context, Entry: slots.Entry{Owner: "file", Key: file.Name, Content: file.Text}})
	}
	for _, source := range e.cfg.SlotSources {
		owner := source.Name()
		if owner == "engine" || owner == "file" || owner == "" {
			return slots.Assembled{}, fmt.Errorf("invalid slot source owner %q", owner)
		}
		sourceUpdates, err := source.Slots(ctx, info)
		if err != nil {
			return slots.Assembled{}, fmt.Errorf("slot source %s: %w", owner, err)
		}
		for _, u := range sourceUpdates {
			u.Entry.Owner = owner
			updates = append(updates, u)
		}
	}
	for _, u := range input.Slots {
		if !slots.HostWritable(u.Slot) || u.Entry.Owner == "engine" || u.Entry.Owner == "file" {
			return slots.Assembled{}, fmt.Errorf("turn override for %q uses a reserved slot or owner", u.Slot)
		}
		updates = append(updates, u)
	}
	snap, err := state.With(updates...)
	if err != nil {
		return slots.Assembled{}, err
	}
	budgets := make(map[slots.Name]int)
	for name, size := range ctxpkg.DefaultBudgets() {
		budgets[slots.Name(name)] = size
	}
	plan, err := e.cfg.SlotSelector.Select(ctx, info, snap, budgets)
	if err != nil {
		return slots.Assembled{}, err
	}
	return slots.Assemble(ctx, snap, plan, prev, def.Runtime.ContextWindow, nil)
}

func (e *Engine) Maintain(ctx context.Context, fn func(context.Context) error) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return fmt.Errorf("nanite: agent is closed")
	}
	if len(e.active) > 0 {
		return ErrBusy
	}
	return fn(ctx)
}
