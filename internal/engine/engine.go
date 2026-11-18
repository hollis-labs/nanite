package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	ctxpkg "github.com/hollis-labs/libs/agent/core/context"
	"github.com/hollis-labs/libs/agent/core/toolresult"
	"github.com/hollis-labs/libs/llm/core/llmcontracts"
	"github.com/hollis-labs/libs/security/policy"
	"github.com/hollis-labs/libs/security/redact"
	"github.com/hollis-labs/nanite/internal/definition"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/provider"
	"github.com/hollis-labs/nanite/internal/skills"
	"github.com/hollis-labs/nanite/internal/slots"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/tools"
)

var ErrBusy = errors.New("nanite: conversation is busy")

type Config struct {
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
	return &Engine{cfg: cfg, scrub: scrub, cache: toolresult.New(cfg.Store.Results(), toolresult.Config{}), active: make(map[string]*Run),
		previous: make(map[string]map[string]string), grants: make(map[string]map[string]bool)}, nil
}

type Input struct {
	Text  string
	Slots []slots.Update
}

func (e *Engine) Send(ctx context.Context, id string, input Input) (*Run, error) {
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
	ctx, cancel := context.WithCancel(ctx)
	r := &Run{id: store.NewID(time.Now()), events: make(chan events.Event, 65), done: make(chan struct{}), cancel: cancel}
	prev := maps.Clone(e.previous[id])
	state := e.cfg.Slots.Snapshot().Copy()
	e.active[id] = r
	go e.execute(ctx, r, info, input, state, prev, e.cfg.Tools.Snapshot(), e.cfg.Skills.Listing())
	return r, nil
}

func (e *Engine) Close() {
	e.mu.Lock()
	e.closed = true
	runs := make([]*Run, 0, len(e.active))
	for _, r := range e.active {
		runs = append(runs, r)
		r.Cancel()
	}
	e.mu.Unlock()
	for _, r := range runs {
		<-r.done
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

func (e *Engine) assemble(ctx context.Context, input Input, info slots.TurnInfo, def definition.Definition, state *slots.Store, prev map[string]string, skillListing string) (slots.Assembled, error) {
	mode := def.Snapshot.Profile.Permissions.Mode
	if mode == "" {
		mode = "default"
	}
	for _, u := range []slots.Update{
		{Slot: slots.Universal, Entry: slots.Entry{Owner: "engine", Key: "rules", Content: "Use the available tools when needed. Respect the workspace and tool permissions."}},
		{Slot: slots.Agent, Entry: slots.Entry{Owner: "file", Key: "instructions", Content: def.Snapshot.Definition.Body}},
		{Slot: slots.Permissions, Entry: slots.Entry{Owner: "engine", Key: "mode", Content: "Permission mode: " + mode}},
	} {
		if err := state.Put(u.Slot, u.Entry); err != nil {
			return slots.Assembled{}, err
		}
	}
	if err := state.Put(slots.Skills, slots.Entry{Owner: "engine", Key: "host-skills", Content: skillListing}); err != nil {
		return slots.Assembled{}, err
	}
	var authored strings.Builder
	for _, name := range slices.Sorted(maps.Keys(def.Skills)) {
		ref := def.Skills[name]
		fmt.Fprintf(&authored, "- %s: %s\n", ref.Name, ref.Description)
	}
	if authored.Len() > 0 {
		if err := state.Put(slots.Skills, slots.Entry{Owner: "file", Key: "authored-skills", Content: strings.TrimSuffix(authored.String(), "\n")}); err != nil {
			return slots.Assembled{}, err
		}
	}
	for _, file := range def.Context {
		if err := state.Put(slots.Context, slots.Entry{Owner: "file", Key: file.Name, Content: file.Text}); err != nil {
			return slots.Assembled{}, err
		}
	}
	for _, source := range e.cfg.SlotSources {
		owner := source.Name()
		if owner == "engine" || owner == "file" || owner == "" {
			return slots.Assembled{}, fmt.Errorf("invalid slot source owner %q", owner)
		}
		updates, err := source.Slots(ctx, info)
		if err != nil {
			return slots.Assembled{}, fmt.Errorf("slot source %s: %w", owner, err)
		}
		for _, u := range updates {
			u.Entry.Owner = owner
			if err := state.Put(u.Slot, u.Entry); err != nil {
				return slots.Assembled{}, err
			}
		}
	}
	for _, u := range input.Slots {
		if !slots.HostWritable(u.Slot) || u.Entry.Owner == "engine" || u.Entry.Owner == "file" {
			return slots.Assembled{}, fmt.Errorf("turn override for %q uses a reserved slot or owner", u.Slot)
		}
		if err := state.Put(u.Slot, u.Entry); err != nil {
			return slots.Assembled{}, err
		}
	}
	snap := state.Snapshot()
	budgets := make(map[slots.Name]int)
	for name, size := range ctxpkg.DefaultBudgets() {
		if size > 0 {
			budgets[slots.Name(name)] = size
		}
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
