package engine

import (
	"context"
	"errors"
	"fmt"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/recovery"
	"log/slog"
	"maps"
	"runtime"
	"strings"
	"sync"
	"time"

	ctxpkg "github.com/hollis-labs/libs/agent/core/context"
	"github.com/hollis-labs/libs/agent/core/toolresult"
	"github.com/hollis-labs/libs/agent/trace"
	"github.com/hollis-labs/libs/core/panics"
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
	// Builtins limits which of Nanite's own tool groups are offered. Nil offers all.
	Builtins       tools.Builtins
	Provider       llmcontracts.Provider
	ProviderConfig provider.Config
	Store          *store.Store
	Gate           permission.Gate
	// AskWait bounds how long a subtask's relayed ask waits, in total, for
	// its root conversation to have a running turn; zero is DefaultAskWait.
	AskWait        time.Duration
	PDP            policy.PDP
	Slots          *slots.Store
	SlotSources    []slots.Source
	SlotSelector   slots.Selector
	Tools          *tools.Groups
	Skills         *skills.Groups
	WorkspaceRoots []string
	// Providers are named providers the host defines. They take precedence
	// over providers.yaml entries with the same name.
	Providers map[string]provider.Spec
	// TraceDir, when set, receives one JSONL file per day of turn trace
	// records (YYYY-MM-DD.jsonl).
	TraceDir string
	// TraceSink, when set, receives every turn's trace record.
	TraceSink func(context.Context, trace.Turn)
	// PanicReporter also receives each contained panic. The engine always
	// logs one; this adds the host's own record.
	PanicReporter panics.Reporter
	// SubtaskResultCap is the agent's subtask result cap in tokens. Zero uses
	// subagent.DefaultResultCap; a variant's result_cap overrides it.
	SubtaskResultCap int
	// SubtaskLimit is how many leads, and separately how many other
	// subtasks, run at once across the agent. Zero is subagent.DefaultLimit;
	// it is clamped to subagent.MaxLimit.
	SubtaskLimit int
	// TurnTokenBudget, when positive, is a token ceiling on one root turn
	// shared with every foreground subtask it starts, at any depth. Cached
	// tokens and estimated compaction summaries count. Reaching it stops
	// running subtasks mid-step and the turn at its next step.
	TurnTokenBudget int
}

type Engine struct {
	cfg      Config
	mu       sync.Mutex
	active   map[string]*Run
	previous map[string]map[string]string
	grants   map[string]map[string]bool
	// changed is closed and replaced each time a run starts or ends, so a
	// subtask waiting to relay an ask to its root conversation can wait for
	// the root's next run.
	changed chan struct{}
	// askSlots holds one open ask at a time per root conversation.
	askSlots map[string]chan struct{}
	// denials are the approvals a person refused a subtask, by child
	// conversation; the subtask's status reads them once it ends.
	denials map[string][]denial
	// subtaskCalls maps a child conversation to the root's tool call that
	// started its subtask chain, so a relayed ask can name it.
	subtaskCalls map[string]string
	// subtaskLinks maps a running child conversation to the turn that
	// started it, for the child's trace record.
	subtaskLinks map[string]subtaskLink
	// progressEvery is the least time between two progress events for
	// one subtask.
	progressEvery time.Duration
	// backgroundProgressEvery is the same for a background subtask.
	backgroundProgressEvery time.Duration
	closed                  bool
	scrub                   *redact.Redactor
	runtime                 *events.Runtime
	jobs                    *background.Manager
	children                subagent.Limit
	// leads limits subtasks that may start their own (a can_spawn variant
	// at depth 1), apart from children, so a council never starves itself.
	leads   subagent.Limit
	notices chan store.Notice
	mcp     *mcpclient.Client
	cache   *toolresult.Cache
	// subtaskResults stores over-cap subtask results in the same table as
	// cache, for subtaskResultTTL rather than an hour (result.go).
	subtaskResults *toolresult.Cache
	// subtasks holds each running subtask's result contract, by child
	// conversation id (result.go). Guarded by mu.
	subtasks map[string]*subtaskState
	// panics contains panics at every boundary where Nanite runs code it
	// does not own (host tools, slot sources, gates, provider streams) and
	// in every goroutine it starts. It is per engine: a library never sets
	// the process-wide default.
	panics *panics.Guard
	// traceMu serializes appends to the trace file.
	traceMu sync.Mutex
	// testTurnHook, set only by tests, runs at the start of every turn.
	testTurnHook func()
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
	guard := newGuard(cfg.Logger, scrub, cfg.PanicReporter)
	cfg.ProviderConfig.Panics = guard
	e := &Engine{cfg: cfg, scrub: scrub, panics: guard, runtime: events.NewRuntime(cfg.Events, cfg.Logger), notices: make(chan store.Notice, 64), mcp: mcpclient.New(cfg.Dir, cfg.ProviderConfig.Secrets, cfg.ProviderConfig.Redactor, cfg.Logger), cache: toolresult.New(cfg.Store.Results(), toolresult.Config{DefaultBudget: toolResultBudget,
		// A subtask result is already held under the budget (result.go); a
		// preview would break its JSON.
		Exempt: func(tool string) bool { return tool == subagent.ToolName }}),
		subtaskResults: toolresult.New(cfg.Store.Results(), toolresult.Config{TTL: subtaskResultTTL}), active: make(map[string]*Run),
		previous: make(map[string]map[string]string), grants: make(map[string]map[string]bool), children: subagent.NewLimit(cfg.SubtaskLimit), leads: subagent.NewLimit(cfg.SubtaskLimit),
		changed: make(chan struct{}), askSlots: make(map[string]chan struct{}), denials: make(map[string][]denial), subtaskCalls: make(map[string]string),
		subtaskLinks: make(map[string]subtaskLink), progressEvery: defaultProgressInterval, backgroundProgressEvery: defaultBackgroundProgressInterval}
	e.jobs = background.New(cfg.Store, scrub, e.publishNotice, cfg.Logger, guard)
	// Expired tool results, cached for an hour or stored subtask results for
	// 30 days, share one table; clear them once when the agent opens.
	if removed, err := e.cache.Purge(context.Background()); err != nil {
		cfg.Logger.Warn("purge expired tool results failed", "error", scrub.Text(err.Error()))
	} else if removed > 0 {
		cfg.Logger.Debug("purged expired tool results", "count", removed)
	}
	return e, nil
}

// newGuard builds the engine's panic guard. Panic values and stacks pass
// through the engine's redactor; each record is logged and handed to the
// host's reporter when it supplied one.
func newGuard(logger *slog.Logger, scrub *redact.Redactor, host panics.Reporter) *panics.Guard {
	log := panics.ReporterFunc(func(r panics.Record) {
		logger.Error("nanite: panic contained", "ref", r.Ref, "where", r.Where, "value", r.Value, "attrs", r.Attrs, "stack", r.Stack)
	})
	reporter := panics.Reporter(log)
	if host != nil {
		reporter = panics.Multi(log, host)
	}
	return panics.New(panics.Config{Reporter: reporter, Redact: scrub.Text})
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

// Discard abandons the conversation's interrupted turn. Tool calls the turn
// left unanswered get a result saying so, the turn is recorded as cancelled,
// and the conversation accepts new messages again.
func (e *Engine) Discard(ctx context.Context, id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return fmt.Errorf("nanite: agent is closed")
	}
	if e.active[id] != nil {
		return ErrBusy
	}
	turn, err := e.cfg.Store.Interrupted(ctx, id)
	if err != nil {
		return err
	}
	if turn == nil {
		return fmt.Errorf("conversation has no interrupted turn")
	}
	history, err := e.cfg.Store.History(ctx, id)
	if err != nil {
		return err
	}
	// A turn whose user message was never stored has nothing to repair and is still discarded.
	plan, _ := recovery.ResumePlan(history, *turn)
	journal, err := e.cfg.Store.OpenJournal(ctx, id)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if len(plan.Missing) > 0 {
		var parent *string
		if n := len(history); n > 0 {
			parent = &history[n-1].ID
		}
		err = journal.AppendMessage(store.Message{ID: store.NewID(now), TurnID: turn.ID, Role: "tool", Blocks: plan.Missing, CreatedAt: now}, parent)
	}
	if err == nil {
		err = journal.FinishTurn(store.Turn{ID: turn.ID, UserMessageID: turn.UserMessageID, Status: "cancelled", Error: "discarded after interruption"}, now)
	}
	return errors.Join(err, journal.Close())
}

// Record adds an exchange the host handled itself to the conversation's
// history: what the user said and the answer the host gave. No model is
// called. The next turn sees the exchange like any other.
func (e *Engine) Record(ctx context.Context, id, userText, answer string) error {
	if strings.TrimSpace(userText) == "" || strings.TrimSpace(answer) == "" {
		return fmt.Errorf("nanite: a recorded exchange needs both the user text and the answer")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return fmt.Errorf("nanite: agent is closed")
	}
	if e.active[id] != nil {
		return ErrBusy
	}
	if _, err := e.cfg.Store.Conversation(ctx, id); err != nil {
		return err
	}
	interrupted, err := e.cfg.Store.Interrupted(ctx, id)
	if err != nil {
		return err
	}
	if interrupted != nil {
		return fmt.Errorf("conversation has an interrupted turn; resume or discard it first")
	}
	history, err := e.cfg.Store.History(ctx, id)
	if err != nil {
		return err
	}
	journal, err := e.cfg.Store.OpenJournal(ctx, id)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	turn := store.Turn{ID: store.NewID(now), UserMessageID: store.NewID(now)}
	var parent *string
	if n := len(history); n > 0 {
		parent = &history[n-1].ID
	}
	text := func(s string) []llmtypes.ContentBlock {
		return []llmtypes.ContentBlock{{Type: "text", Text: e.scrub.Text(s)}}
	}
	reply := store.Message{ID: store.NewID(now), TurnID: turn.ID, Role: "assistant", Blocks: text(answer), CreatedAt: now}
	err = journal.StartTurn(turn, now)
	if err == nil {
		err = journal.AppendMessage(store.Message{ID: turn.UserMessageID, TurnID: turn.ID, Role: "user", Blocks: text(userText), CreatedAt: now}, parent)
	}
	if err == nil {
		err = journal.AppendMessage(reply, &turn.UserMessageID)
	}
	if err == nil {
		turn.Status, turn.StopReason = "completed", llmtypes.StopReasonEndTurn
		err = journal.FinishTurn(turn, now)
	}
	return errors.Join(err, journal.Close())
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
		// The host's event sink is telemetry: a panic in it is recorded and
		// the turn goes on.
		_ = e.panics.Call("event sink", func() error {
			e.runtime.Observe(eventCtx, id, r.id, event)
			return nil
		}, panics.With("conversation", id))
	}

	prev := maps.Clone(e.previous[id])
	state := e.cfg.Slots.Snapshot()
	e.active[id] = r
	e.signalLocked()
	hostTools, hostSkills := e.cfg.Tools.Snapshot(), e.cfg.Skills.Snapshot()
	e.panics.Go("turn", func() {
		e.execute(ctx, r, info, input, state, prev, hostTools, hostSkills, interrupted)
	}, func(pe *panics.Error) {
		// A panic after the guarded turn (while finalizing the run) must
		// still end the run, or Wait would block and the conversation would
		// stay busy.
		r.cancel()
		e.release(id, r, func() {
			r.finish(events.Result{}, pe, events.RunFailed{EventMeta: events.EventMeta{RunID: r.id}, Err: pe})
		})
	}, panics.With("conversation", id), panics.With("run", runID))
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

// toolResultBudget is how much of one tool result the model sees inline. A
// larger result is cached and shown as a preview the model pages through.
const toolResultBudget = 24 << 10

// provider returns the adapter and model for one use (chat, summarizer or
// subtask),
// and resolve does so for an explicit selection.
//
// A use without its own selection in providers.yaml falls back to chat, and
// chat without one uses host.yaml's runtime.provider and model. A provider
// the host injected (Config.Provider) serves every selection except one that
// names a provider the host itself defined (Config.Providers), so an agent
// directory cannot route a host's traffic around it. Otherwise a named
// provider comes from the host's definitions, then providers.yaml.
func (e *Engine) provider(def definition.Definition, use string) (llmcontracts.Provider, definition.Selection, error) {
	sel, ok := def.Use[use]
	if !ok {
		sel, ok = def.Use[definition.UseChat]
	}
	if !ok {
		sel = definition.Selection{Provider: def.Runtime.Provider, Model: def.Runtime.Model}
	}
	return e.resolve(def, use, sel)
}

func (e *Engine) resolve(def definition.Definition, use string, sel definition.Selection) (llmcontracts.Provider, definition.Selection, error) {
	hostSpec, hostDefined := e.cfg.Providers[sel.Provider]
	if e.cfg.Provider != nil && !hostDefined {
		return e.cfg.Provider, sel, nil
	}
	if sel.Provider == provider.BuiltinAnthropic || sel.Provider == provider.BuiltinOpenAI {
		cfg := e.cfg.ProviderConfig
		cfg.Provider = sel.Provider
		p, err := provider.New(cfg)
		return p, sel, err
	}
	spec, ok := hostSpec, hostDefined
	if !ok {
		spec, ok = def.Providers[sel.Provider]
	}
	if !ok {
		return nil, sel, fmt.Errorf("providers: %s names unknown provider %q", use, sel.Provider)
	}
	spec.Name = sel.Provider
	p, err := provider.NewNamed(spec, e.cfg.ProviderConfig)
	return p, sel, err
}

func (e *Engine) assemble(ctx context.Context, input Input, info slots.TurnInfo, def definition.Definition, state slots.Snapshot, prev map[string]string, skillListing, rolePrompt, mode string, warnings []string, pending []store.Notice) (slots.Assembled, error) {
	permissions := "Permission mode: " + mode + ". File tools are confined to the workspace roots."
	if len(e.cfg.WorkspaceRoots) > 0 {
		permissions += " Workspace roots: " + strings.Join(e.cfg.WorkspaceRoots, ", ") + "."
	}
	permissions += " Approved Bash commands run with the user’s privileges and can access paths outside those roots."
	environment := "Model: " + def.Runtime.Model + ". Platform: " + runtime.GOOS + "/" + runtime.GOARCH + ". Today's date: " + time.Now().Format("2006-01-02") + "."
	if len(e.cfg.WorkspaceRoots) > 0 {
		environment = "Working directory: " + e.cfg.WorkspaceRoots[0] + ". Relative paths resolve there. Agent directory: " + e.cfg.Dir + ". " + environment
	}
	updates := []slots.Update{
		{Slot: slots.System, Entry: slots.Entry{Owner: "engine", Key: "environment", Content: environment}},
		{Slot: slots.Universal, Entry: slots.Entry{Owner: "engine", Key: "rules", Content: basePrompt}},
		{Slot: slots.Agent, Entry: slots.Entry{Owner: "file", Key: "instructions", Content: def.Snapshot.Definition.Body}},
		{Slot: slots.Permissions, Entry: slots.Entry{Owner: "engine", Key: "mode", Content: permissions}},
		{Slot: slots.Skills, Entry: slots.Entry{Owner: "engine", Key: "available-skills", Content: skillListing}},
	}

	if rolePrompt != "" {
		updates = append(updates, slots.Update{Slot: slots.Mode, Entry: slots.Entry{Owner: "engine", Key: "subtask-variant", Content: rolePrompt, Priority: 100}})
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
		var owner string
		var sourceUpdates []slots.Update
		err := e.panics.Call("slot source", func() (err error) {
			owner = source.Name()
			if owner == "engine" || owner == "file" || owner == "" {
				return fmt.Errorf("invalid slot source owner %q", owner)
			}
			sourceUpdates, err = source.Slots(ctx, info)
			return err
		}, panics.With("conversation", info.ConversationID))
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
	budgets[slots.Universal] = universalBudget
	var plan slots.Plan
	err = e.panics.Call("slot selector", func() (err error) {
		plan, err = e.cfg.SlotSelector.Select(ctx, info, snap, budgets)
		return err
	}, panics.With("conversation", info.ConversationID))
	if err != nil {
		return slots.Assembled{}, err
	}
	return slots.Assemble(ctx, snap, plan, prev, def.Runtime.ContextWindow, nil, budgets)
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
