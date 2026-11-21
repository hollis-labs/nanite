package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/authored"
	"github.com/hollis-labs/nanite/internal/recovery"
	"github.com/hollis-labs/nanite/internal/warden"
	"github.com/hollis-labs/nanite/toolregistry"

	ctxpkg "github.com/hollis-labs/libs/agent/core/context"
	"github.com/hollis-labs/libs/agent/core/toolresult"
	"github.com/hollis-labs/libs/agent/steer"
	"github.com/hollis-labs/libs/agent/trace"
	"github.com/hollis-labs/libs/core/panics"
	"github.com/hollis-labs/libs/llm/core/llmcontracts"
	"github.com/hollis-labs/libs/security/policy"
	"github.com/hollis-labs/libs/security/redact"
	"github.com/hollis-labs/nanite/internal/background"
	"github.com/hollis-labs/nanite/internal/definition"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/integrity"
	"github.com/hollis-labs/nanite/internal/mcpclient"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/provider"
	"github.com/hollis-labs/nanite/internal/provider/station"
	"github.com/hollis-labs/nanite/internal/skills"
	"github.com/hollis-labs/nanite/internal/slots"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
	"github.com/hollis-labs/nanite/internal/tools"
	transcriptlog "github.com/hollis-labs/nanite/internal/transcript"
)

var ErrBusy = errors.New("nanite: conversation is busy")

// ErrNoInterrupted means there is no interrupted turn to resume or discard.
var ErrNoInterrupted = errors.New("conversation has no interrupted turn")

type Config struct {
	DailyTokenBudget          int
	MaxOutputTokens, MaxSteps int
	AutoApprove               bool // Testing only; convert approval asks to allows, not denies.
	Events                    events.Sink
	Logger                    *slog.Logger
	Dir, GlobalSkillsDir      string
	// ToolPackageEnv names credential-like host variables tool packages may
	// pass through (authored.CheckToolEnv).
	ToolPackageEnv []string

	// ToolRegistry enables the tool_install builtin through a host-supplied registry adapter.
	// Nil disables registry installation. Local filesystem adapters are development-only.
	ToolRegistry *toolregistry.Client
	// ToolApprovalDir is host-owned storage outside Dir; empty uses user config.
	ToolApprovalDir string
	// Builtins limits which of Nanite's own tool groups are offered. Nil offers all.
	Builtins       tools.Builtins
	Provider       llmcontracts.Provider
	ProviderConfig provider.Config
	// Identity is identity.yaml (nil without one): the principal, Warden
	// minting for providers with auth warden, and MCP server auth.
	Identity *authored.Identity
	// OnAuthWaiting sees each Warden grant request Nanite files or finds
	// pending.
	OnAuthWaiting func(warden.Waiting)
	// OnAuthGranted sees a conversation that waited on a grant get its token.
	OnAuthGranted func(warden.Waiting)
	Store         *store.Store
	Gate          permission.Gate
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
	// SlotBudgets overrides the token budget of the slots it names. The
	// Universal slot (the base prompt) keeps its own budget.
	SlotBudgets map[slots.Name]int
	// SlotPlacement overrides where the slots it names go in a request (see
	// slots.DefaultPlacement). Universal, System and Agent always stay in the
	// system prefix.
	SlotPlacement map[slots.Name]slots.Placement
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
	// IdleTimeout is how long a model stream may be silent before the turn
	// fails as stalled; zero means DefaultIdleTimeout.
	IdleTimeout time.Duration
	// CompactAt, when positive, is the estimated request size in tokens
	// (system blocks, tool schemas and conversation together) at which the
	// conversation is compacted. Zero uses 80% of the runtime context window.
	CompactAt int
	// CompactKeepTokens, when positive, is about how much of the end of the
	// conversation compaction keeps whole, in estimated tokens: the recent
	// turns that fit, cut only where the person's message starts a turn, and
	// always the last turn. Zero keeps the last four messages.
	CompactKeepTokens int
	// CompactionPrompt replaces the summary prompt (DefaultCompactionPrompt).
	CompactionPrompt string
	// CompactionInputTokens bounds what one summary call is sent, in
	// estimated tokens; past it the span is sent as a clipped transcript
	// with its start left out. Zero uses DefaultCompactionInputTokens.
	CompactionInputTokens int
	// Steering, when set, runs libs agent/steer before each root turn: the
	// conversation's mode, work slots, reflexes and classifier.
	Steering *Steering
}

type Engine struct {
	stopCtx     context.Context
	stopCancel  context.CancelFunc
	daily       dailyBudget
	stopped     bool
	stopDone    chan struct{}
	profileMu   sync.Mutex
	profiles    map[string]*turnProfile
	profileBase definition.Definition
	cfg         Config
	mu          sync.Mutex
	active      map[string]*Run
	// checking holds conversations an integrity check is reading, outside
	// e.mu; nothing may write their transcript meanwhile (see busyLocked).
	checking map[string]bool
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
	// steerers holds each root conversation's steering state.
	steerMu  sync.Mutex
	steerers map[string]*steering
	closed   bool
	scrub    *redact.Redactor
	runtime  *events.Runtime
	jobs     *background.Manager
	children subagent.Limit
	// leads limits subtasks that may start their own (a can_spawn variant
	// at depth 1), apart from children, so a council never starves itself.
	leads subagent.Limit
	// noticeSubs are the notice subscribers, by subscription id (notices.go).
	noticeSubs map[uint64]*noticeQueue
	noticeSeq  uint64
	// noticeQueueLimit bounds each subscriber's queue (NoticeQueueLimit).
	noticeQueueLimit int
	// noticeJobs marks background jobs a notice turn started (notices.go).
	noticeJobs map[string]bool
	// dispatched marks the child conversations a host started with Dispatch
	// (and that are still running), so their relayed asks say so.
	dispatched map[string]bool
	mcp        *mcpclient.Client
	cache      *toolresult.Cache
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
	// toolJobs holds a slot per running background tool-package job.
	toolJobs chan struct{}
	// testTurnHook, set only by tests, runs at the start of every turn.
	testTurnHook func()
}

func New(cfg Config) (*Engine, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.AutoApprove {
		cfg.Logger.Warn("nanite: auto mode is on; approvals and grant waits are skipped (testing only)")
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
	if cfg.Steering != nil {
		if err := cfg.Steering.validate(); err != nil {
			return nil, err
		}
	}
	guard := newGuard(cfg.Logger, scrub, cfg.PanicReporter)
	cfg.ProviderConfig.Panics = guard
	var mcpAuth *mcpclient.Auth
	if cfg.Identity != nil {
		src, err := authored.NewWardenSource(*cfg.Identity, cfg.ProviderConfig.Redactor, cfg.Logger, cfg.OnAuthWaiting, cfg.OnAuthGranted, cfg.AutoApprove)
		if err != nil {
			return nil, err
		}
		if src != nil {
			if err := src.UseWaiterStore(context.Background(), cfg.Store); err != nil {
				return nil, err
			}
		}
		cfg.ProviderConfig.Warden = src
		mcpAuth = &mcpclient.Auth{Principal: cfg.Identity.Principal, Warden: cfg.ProviderConfig.Warden, Servers: cfg.Identity.MCP}
	}
	e := &Engine{cfg: cfg, scrub: scrub, panics: guard, runtime: events.NewRuntime(cfg.Events, cfg.Logger), noticeSubs: make(map[uint64]*noticeQueue), noticeQueueLimit: NoticeQueueLimit, noticeJobs: make(map[string]bool), dispatched: make(map[string]bool), mcp: mcpclient.New(cfg.Dir, cfg.ProviderConfig.Secrets, cfg.ProviderConfig.Redactor, cfg.Logger, mcpAuth), cache: toolresult.New(cfg.Store.Results(), toolresult.Config{DefaultBudget: toolResultBudget,
		// A subtask result is already held under the budget (result.go); a
		// preview would break its JSON.
		Exempt: func(tool string) bool { return tool == subagent.ToolName }}),
		subtaskResults: toolresult.New(cfg.Store.Results(), toolresult.Config{TTL: subtaskResultTTL}), active: make(map[string]*Run), checking: make(map[string]bool),
		previous: make(map[string]map[string]string), grants: make(map[string]map[string]bool), children: subagent.NewLimit(cfg.SubtaskLimit), leads: subagent.NewLimit(cfg.SubtaskLimit),
		changed: make(chan struct{}), askSlots: make(map[string]chan struct{}), denials: make(map[string][]denial), subtaskCalls: make(map[string]string),
		subtaskLinks: make(map[string]subtaskLink), progressEvery: defaultProgressInterval, backgroundProgressEvery: defaultBackgroundProgressInterval,
		steerers: make(map[string]*steering), toolJobs: make(chan struct{}, MaxBackgroundToolJobs)}
	e.stopCtx, e.stopCancel = context.WithCancel(context.Background())
	e.profiles = make(map[string]*turnProfile)
	e.profileBase, err = definition.Load(cfg.Dir, cfg.GlobalSkillsDir)
	if err != nil {
		return nil, err
	}
	if err := e.loadDaily(); err != nil {
		return nil, err
	}
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
	// AgentDir is a composed profile directory for this turn and its descendants.
	AgentDir string
	// Headers are non-credential request headers for MCP HTTP and model gateways.
	Headers map[string]string
	Text    string
	Slots   []slots.Update
	// Source says what started the turn: empty for a person's message,
	// SourceNotice for a turn a host starts because a notice arrived,
	// SourceAuto for a turn the host sends on its own, or ErrandSource(m)
	// for a turn module m of the host asked for. Only a person's message is
	// classified, runs reflexes or reaches a front desk.
	Source string
	// Situation is what is in front of the person, for steering (the window
	// in front, the open windows, the layout). Input and Source are filled
	// in by the engine. Nil when the host has none or steering is off.
	Situation *steer.Situation
	// Steer is steering run ahead of this message by Engine.Steer; the turn
	// uses it instead of steering again. It must be for this conversation
	// and text, and is used once.
	Steer *PreparedSteer
}

// SourceNotice marks a turn started because a notice arrived. Its trace
// record has source "reflex": a notice turn is an automatic activation, which
// is what reflex records. Its user message is NoticeTurnText, set by the
// engine; the notice itself reaches the model only in the session slot.
const SourceNotice = "notice"

// NoticeTurnText is the user message of every notice turn.
const NoticeTurnText = "A notice arrived."

// SourceAuto marks a turn the host sent on its own (a trigger, a reflex of
// the host's, another agent), not a person's message. Its trace record has
// source "host".
const SourceAuto = "auto"

// SourceErrandPrefix starts the Source of a turn a host's module asked for:
// "errand:<module>". Its trace record has source "host".
const SourceErrandPrefix = "errand:"

// errandModule is a module name an errand source may carry.
var errandModule = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ErrandSource is the Input.Source of a turn module asked for. A module name
// is 1 to 64 letters, digits, '.', '_' or '-', starting with a letter or digit.
func ErrandSource(module string) (string, error) {
	if !errandModule.MatchString(module) {
		return "", fmt.Errorf("nanite: errand module %q is not a valid name (1-64 letters, digits, '.', '_' or '-')", module)
	}
	return SourceErrandPrefix + module, nil
}

// hostSource reports whether source is a turn the host or one of its modules
// started (SourceAuto or an errand).
func hostSource(source string) bool {
	return source == SourceAuto || strings.HasPrefix(source, SourceErrandPrefix)
}

func (e *Engine) Send(ctx context.Context, id string, input Input) (*Run, error) {
	if p := input.Steer; p != nil && (p.conversation != id || p.text != input.Text || input.Source != "") {
		return nil, errors.New("nanite: Input.Steer was prepared for another message")
	}
	return e.startRun(ctx, id, input, false)
}

func (e *Engine) Resume(ctx context.Context, id string) (*Run, error) {
	return e.startRun(ctx, id, Input{}, true)
}

// Discard abandons the conversation's interrupted turn. Tool calls the turn
// left unanswered get a result saying so, the turn is recorded as cancelled,
// and the conversation accepts new messages again.
func (e *Engine) Discard(ctx context.Context, id string) (err error) {
	// A check reported here is emitted once e.mu is released: a host's
	// event sink may call back into the engine.
	var checked *integrity.Report
	defer func() {
		if checked != nil {
			e.ObserveIntegrity(ctx, id, "discard", *checked)
		}
	}()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return fmt.Errorf("nanite: agent is closed")
	}
	if e.busyLocked(id) {
		return ErrBusy
	}
	turn, err := e.cfg.Store.Interrupted(ctx, id)
	if err != nil {
		return err
	}
	if turn == nil {
		// A torn transcript can leave its turn running, not interrupted:
		// say why nothing can be discarded rather than that nothing is there.
		if mark, err := e.cfg.Store.Setting(ctx, id, store.SettingIntegrity); err != nil {
			return err
		} else if mark != "" {
			report, err := e.validate(ctx, id)
			if checkRan(err) {
				checked = &report
			}
			if err != nil {
				return err
			}
		}
		return ErrNoInterrupted
	}
	history, err := e.cfg.Store.History(ctx, id)
	if err != nil {
		return err
	}
	// A turn whose user message was never stored has nothing to repair and is still discarded.
	plan, _ := recovery.ResumePlan(history, *turn)
	journal, err := e.cfg.Store.OpenJournal(ctx, id)
	if errors.Is(err, transcriptlog.ErrTorn) {
		// The transcript refuses writes until its tail is repaired: say why
		// with the findings, not the bare byte-layer error.
		report, gateErr := e.validate(ctx, id)
		if checkRan(gateErr) {
			checked = &report
		}
		if gateErr != nil {
			return gateErr
		}
	}
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
	// A blocked history takes no new exchanges either.
	if err := e.runGate(ctx, id, false); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return fmt.Errorf("nanite: agent is closed")
	}
	if e.busyLocked(id) {
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
	switch input.Source {
	case "":
	case SourceNotice:
		if input.Text != "" {
			return nil, fmt.Errorf("a notice turn's text is set by the engine; leave Input.Text empty")
		}
		input.Text = NoticeTurnText
	case SourceAuto:
		if strings.TrimSpace(input.Text) == "" {
			return nil, fmt.Errorf("an auto turn needs text")
		}
	default:
		module, ok := strings.CutPrefix(input.Source, SourceErrandPrefix)
		if !ok {
			return nil, fmt.Errorf("unknown turn source %q", input.Source)
		}
		if _, err := ErrandSource(module); err != nil {
			return nil, err
		}
		if strings.TrimSpace(input.Text) == "" {
			return nil, fmt.Errorf("an errand turn needs text")
		}
	}
	info, err := e.cfg.Store.Conversation(ctx, id)
	if err != nil {
		return nil, err
	}
	// Before anything reads the history or assembles context: never run on
	// a history an integrity check found unsafe.
	if err := e.runGate(ctx, id, resume); err != nil {
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
		return nil, ErrNoInterrupted
	}
	if interrupted != nil {
		input.AgentDir, input.Headers = interrupted.AgentDir, interrupted.Headers
		// Persisted empty selections mean the Open profile and no turn headers,
		// even when the caller tries to resume from another profile's context.
		ctx = context.WithValue(ctx, profileKey{}, (*turnProfile)(nil))
		ctx = station.WithHeaders(ctx, nil)
	}
	ctx, err = e.prepareProfile(ctx, &input)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, fmt.Errorf("nanite: agent is closed")
	}
	if e.stopped {
		return nil, ErrStopped
	}
	if err := e.checkDaily(); err != nil {
		return nil, err
	}
	if e.busyLocked(id) {
		return nil, ErrBusy
	}
	// Validation performs filesystem I/O without the engine lock. Confirm
	// that nobody resolved or replaced the interrupted turn in the meantime.
	current, err := e.cfg.Store.Interrupted(ctx, id)
	if err != nil {
		return nil, err
	}
	if (current == nil) != (interrupted == nil) || current != nil && current.ID != interrupted.ID {
		return nil, fmt.Errorf("conversation's interrupted turn changed while validating its profile")
	}
	runID := store.NewID(time.Now())
	if interrupted != nil {
		runID = interrupted.ID
	}

	r, ctx := e.newRunLocked(warden.WithConversation(ctx, id), id, runID)
	prev := maps.Clone(e.previous[id])
	state := e.cfg.Slots.Snapshot()
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

// newRunLocked makes a run for conversation id and makes it the active one.
// The caller holds e.mu and has checked the conversation is free.
func (e *Engine) newRunLocked(ctx context.Context, id, runID string) (*Run, context.Context) {
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
	e.active[id] = r
	e.signalLocked()
	return r, ctx
}

func (e *Engine) Close() {
	e.mu.Lock()
	e.closed = true
	e.stopCancel()
	for id, q := range e.noticeSubs {
		q.stop()
		delete(e.noticeSubs, id)
	}
	runs := make([]*Run, 0, len(e.active))
	for _, r := range e.active {
		runs = append(runs, r)
		r.Cancel()
	}
	e.mu.Unlock()
	e.steerMu.Lock()
	clear(e.steerers)
	e.steerMu.Unlock()
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
func (e *Engine) provider(def definition.Definition, use string) (llmcontracts.Provider, authored.Selection, error) {
	sel, ok := def.Use[use]
	if !ok {
		sel, ok = def.Use[authored.UseChat]
	}
	if !ok {
		sel = authored.Selection{Provider: def.Runtime.Provider, Model: def.Runtime.Model}
	}
	return e.resolve(def, use, sel)
}

func (e *Engine) resolve(def definition.Definition, use string, sel authored.Selection) (llmcontracts.Provider, authored.Selection, error) {
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
		environment = "Working directory: " + e.cfg.WorkspaceRoots[0] + ". Relative paths resolve there. Agent directory: " + e.profileDir(ctx) + ". " + environment
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
		entry := slots.Entry{Owner: "engine", Key: "notice-" + notice.ID, Priority: 1, Content: notice.Kind + ": " + notice.Body}
		if notice.Kind == mailNoticeKind {
			// A mail turn that showed nothing would spend a delivery attempt
			// unseen; its pointer always has room.
			entry.Pointer, entry.Reserve = mailPointer(notice.Body), true
		}
		updates = append(updates, slots.Update{Slot: slots.Session, Entry: entry})
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
	budgets := e.SlotBudgets()
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

// SlotBudgets returns the token budget of each slot: the library defaults,
// the base prompt's own Universal budget, then the host's overrides. Zero
// means the slot has no fixed budget.
func (e *Engine) SlotBudgets() map[slots.Name]int {
	budgets := make(map[slots.Name]int)
	for name, size := range ctxpkg.DefaultBudgets() {
		budgets[slots.Name(name)] = size
	}
	budgets[slots.Universal] = universalBudget
	for name, size := range e.cfg.SlotBudgets {
		if name != slots.Universal && size > 0 {
			budgets[name] = size
		}
	}
	return budgets
}

// placement is where a slot's content goes in a request.
func (e *Engine) placement(name slots.Name) slots.Placement {
	if name == slots.Universal || name == slots.System || name == slots.Agent {
		return slots.InSystem
	}
	if p, ok := e.cfg.SlotPlacement[name]; ok {
		return p
	}
	return slots.DefaultPlacement(name)
}

// TurnContextBlock is the Name of the text block that carries a turn's
// in-turn slots in front of the user's message.
const TurnContextBlock = slots.TurnContextBlock

// turnContext renders the slots placed in the turn that changed since the
// last turn as one text block for the front of the user's message, or
// reports false when there are none. An unchanged slot is already in the
// history, with the message it last changed for, so it is not repeated.
func (e *Engine) turnContext(assembled slots.Assembled) (llmtypes.ContentBlock, bool) {
	var b strings.Builder
	for _, block := range assembled.Blocks {
		name := slots.Name(block.SlotName)
		if e.placement(name) != slots.InTurn || strings.TrimSpace(block.Content) == "" || !assembled.Report[name].Changed {
			continue
		}
		fmt.Fprintf(&b, "<%s>\n%s\n</%s>\n", block.SlotName, block.Content, block.SlotName)
	}
	if b.Len() == 0 {
		return llmtypes.ContentBlock{}, false
	}
	return llmtypes.ContentBlock{Type: "text", Name: TurnContextBlock, Text: e.scrub.Text(strings.TrimSuffix(b.String(), "\n"))}, true
}

// forgetTurnContext marks the conversation's in-turn slots as changed, so
// the next turn sends them again. It is called after a compaction replaced
// older messages with a summary: an unchanged in-turn slot lived in one of
// those messages and would otherwise be gone until it changed.
func (e *Engine) forgetTurnContext(conversation string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	hashes := maps.Clone(e.previous[conversation])
	for name := range hashes {
		if e.placement(slots.Name(name)) == slots.InTurn {
			delete(hashes, name)
		}
	}
	e.previous[conversation] = hashes
}

// Warden is the agent's Warden token source, nil without identity.yaml's
// warden section.
func (e *Engine) Warden() *warden.Source { return e.cfg.ProviderConfig.Warden }

// DefaultIdleTimeout is how long a model stream may be silent before its turn
// fails as stalled, when Config.IdleTimeout is zero.
const DefaultIdleTimeout = 90 * time.Second

func (e *Engine) idleTimeout() time.Duration {
	if e.cfg.IdleTimeout > 0 {
		return e.cfg.IdleTimeout
	}
	return DefaultIdleTimeout
}
