package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/hollis-labs/libs/agent/trace"
	"github.com/hollis-labs/libs/core/panics"
	"github.com/hollis-labs/nanite/internal/agentlock"
	"github.com/hollis-labs/nanite/internal/authored"
	"github.com/hollis-labs/nanite/internal/tools"
	"github.com/hollis-labs/nanite/toolregistry"

	"github.com/hollis-labs/libs/agent/agentdir"
	"github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/libs/llm/core/llmcontracts"
	"github.com/hollis-labs/libs/security/policy"
	"github.com/hollis-labs/libs/security/secrets"
	"github.com/hollis-labs/nanite/internal/control"
	"github.com/hollis-labs/nanite/internal/engine"
	"github.com/hollis-labs/nanite/internal/provider"
	"github.com/hollis-labs/nanite/internal/recovery"
	"github.com/hollis-labs/nanite/internal/slots"
	nanitestore "github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
	"github.com/hollis-labs/nanite/internal/warden"
)

var (
	// ErrBusy means a run is already active in the conversation.
	ErrBusy = engine.ErrBusy
	// ErrNoInterrupted means there is no interrupted turn to resume or discard.
	ErrNoInterrupted = engine.ErrNoInterrupted
	// ErrNoConversation means the requested conversation does not exist.
	ErrNoConversation = nanitestore.ErrNoConversation
)

// Options configures an embedded agent.
type Options struct {
	// DailyTokenBudget caps total agent tokens per UTC day. Zero uses host.yaml.
	DailyTokenBudget int
	// MaxOutputTokens overrides runtime.max_output_tokens when positive.
	// Zero uses host.yaml; negative values are rejected.
	MaxOutputTokens int
	// MaxSteps overrides runtime.max_steps when positive. Zero uses
	// host.yaml (50 by default); negative values are rejected. A limit
	// permits one additional tool-less closing reply.
	MaxSteps int
	// Dir is the required authored agent directory.
	Dir string
	// DataDir defaults to .nanite inside Dir. When explicitly set, tool-package
	// approvals use DataDir/tool-approvals, which must resolve outside Dir.
	// Without an explicit DataDir, approvals use the OS user config directory.
	DataDir string
	// WorkDir is the agent's working directory, an existing absolute
	// directory: relative paths in the file tools resolve there first, then
	// in Dir, and bash and background jobs run there. Empty is the process's
	// working directory at Open. Set it when several agents in one process
	// each work in their own directory; the process's working directory is
	// never changed. MCP stdio servers are not affected: they start in the
	// process's working directory.
	WorkDir string
	// GlobalSkillsDir defaults to ~/.nanite/skills. Use "-" to disable it.
	GlobalSkillsDir string
	// ToolPackageEnv names credential-like host variables (provider key
	// variables, PATs, DSNs, credential URLs, or names containing KEY, TOKEN,
	// SECRET, AUTH, PASSWORD or CREDENTIAL) that tool packages may pass through
	// with env. Without it a
	// package that lists one does not load; it should ask for a Warden token
	// with needs instead.
	ToolPackageEnv []string

	// ToolRegistry enables the tool_install builtin through a host-supplied registry adapter.
	// Nil disables registry installation. Local filesystem adapters are development-only.
	ToolRegistry *toolregistry.Client
	// Provider defaults to the adapter selected by host.yaml and Gateway.
	// When set, it serves every use except one that providers.yaml points at
	// a provider in Providers.
	Provider llmcontracts.Provider
	// Gateway optionally directs requests through a model gateway. Its zero value uses the provider endpoint.
	Gateway Gateway
	// Providers are named providers the host defines, in addition to any in
	// the agent directory's providers.yaml; a host entry wins over a file
	// entry with the same name. providers.yaml's use section selects them.
	Providers []ProviderSpec
	// Store defaults to SQLite at DataDir/nanite.db. The caller retains ownership of a supplied store.
	Store *store.Store
	// Logger defaults to slog.Default().
	Logger *slog.Logger
	// Events defaults to nil, with no external runtime event sink.
	Events EventSink
	// Gate defaults to DenyGate for tool calls that require approval.
	Gate Gate
	// AutoApprove skips approval prompts and grant waits for testing only.
	// Explicit denies and package integrity checks still apply. Default false.
	AutoApprove bool
	// SubtaskAskWait bounds how long a subtask's approval or question waits,
	// in total, for its root conversation to have a running turn to be asked
	// on (a background subtask can ask between turns). It then ends denied,
	// "unanswered". Zero means 30 minutes.
	SubtaskAskWait time.Duration
	// PDP defaults to nil, with authorization based on the agent's authored rules.
	PDP policy.PDP
	// SlotSources defaults to an empty list. Sources contribute entries at the start of each turn.
	SlotSources []SlotSource
	// SlotSelector defaults to priority order with pointer and skip fallback to fit the slot budget.
	SlotSelector SlotSelector
	// Builtins names which of Nanite's own tool groups the agent is offered.
	// Nil means the default for the agent's kind: every group for a stable
	// or task agent, none for an embedded one, whose application decides.
	// Pass AllBuiltins() to give an embedded agent everything. An empty
	// slice offers none. A withheld tool is not sent to the model. Tools
	// that page through a long tool result are always offered.
	Builtins []BuiltinGroup
	// Secrets defaults to the standard secrets resolver.
	Secrets *secrets.Resolver
	// TraceFiles writes every turn's trace record to DataDir/traces as one
	// JSONL file per day (YYYY-MM-DD.jsonl). The CLI turns it on.
	TraceFiles bool
	// TraceSink, when set, receives every turn's trace record: one per turn,
	// however it ended, after the turn finishes. Error text in it is
	// redacted. A panic in the sink is contained.
	TraceSink func(context.Context, TraceRecord)
	// PanicReporter also receives each panic the agent contains. Nanite
	// contains panics in host tools, slot sources, the selector, the
	// approval gate, the PDP, the event sink, provider streams and its own
	// goroutines, so they fail a tool call or a turn rather than the host
	// process. The error the host sees carries only a ref ("internal error
	// (ref pnc_...)"); the record with the redacted value and stack is
	// logged to Logger and handed to this reporter. Nil logs only.
	PanicReporter PanicReporter
	// SubtaskResultCap is the size cap, in tokens, on the whole result a
	// subtask hands back. Zero means "use host.yaml's
	// runtime.subtask_result_cap, else the effective default of about
	// 5,900"; it cannot unset a host.yaml value. A variant's result_cap
	// overrides it, and either is clamped to what one tool result shows in
	// full (about 5,900 tokens). A larger result is stored for 30 days and
	// the parent gets a pointer it can read with fetch_tool_result.
	SubtaskResultCap int
	// SubtaskLimit is how many subtasks may run at once across the agent,
	// counting every conversation and background subtasks. Leads (can_spawn
	// variants) and other subtasks each get this many slots, so a lead never
	// competes with its own children. Zero means "use host.yaml's
	// runtime.subtask_limit, else 4"; at most 8. A foreground
	// subtask over the limit waits for a slot while its turn lasts; a
	// background one is refused and the model is told why.
	SubtaskLimit int
	// TurnTokenBudget, when positive, caps the tokens one turn may spend,
	// shared with every foreground subtask it starts at any depth, cached
	// tokens and (estimated) compaction summaries included. Reaching it stops
	// running subtasks mid-step and the turn at its next step, with a token
	// limit. Background subtasks have only their variant's budget; a resumed
	// turn starts a fresh budget. Zero means "use host.yaml's
	// runtime.turn_token_budget, else no shared budget": an embedder cannot
	// turn off a host.yaml budget through Options, only set its own.
	TurnTokenBudget int
	// IdleTimeout is how long a model stream may send nothing before its
	// turn fails as stalled. Zero means 90 seconds; negative is refused. A
	// stream that is busy but not passing anything on (the model writing a
	// long tool input, or thinking) sends keepalives, so this measures real
	// silence from the provider.
	IdleTimeout time.Duration
	// Steering, when set, runs libs agent/steer before each root turn: the
	// conversation's mode (set only through Conversation.SetMode, never by
	// the model), work slots, reflexes and classifier. Classifier results
	// are suggested, not applied, unless Steering.Policy sets a facet to auto.
	Steering *Steering
	// OnAuthWaiting sees each Warden grant request Nanite files, or finds
	// still pending, because the agent has no grant for a provider's or MCP
	// server's audience (identity.yaml). A host can show it as an activity
	// of kind ActivityAuthWaiting. The turn that needed the token fails with
	// an error naming the request; a later turn mints once it is approved.
	OnAuthWaiting func(AuthWaiting)
	// OnAuthGranted sees a conversation whose token request had found a
	// grant missing get its token: AuthWaiting.Conversation no longer waits
	// on AuthWaiting.RequestID (Status "granted").
	OnAuthGranted func(AuthWaiting)
	// SlotPlacement moves the slots it names between the system prefix and
	// the turn (see SlotInSystem and SlotInTurn). By default only
	// user_context is placed in the turn. Universal, System and Agent always
	// stay in the system prefix.
	SlotPlacement map[SlotName]SlotPlacement
	// CompactAt, when positive, is the request size in tokens at which the
	// conversation is compacted. The size counts everything a request
	// carries: system blocks, tool schemas and conversation. The provider's
	// reported input for the last request is checked against it too. Zero
	// uses 80% of host.yaml's runtime.context_window (200,000 when unset).
	CompactAt int
	// CompactKeepTokens, when positive, is about how much of the end of the
	// conversation compaction keeps whole, in estimated tokens: the recent
	// turns that fit, cut only where the person's message starts a turn,
	// and always the last turn. Zero keeps the last four messages.
	CompactKeepTokens int
	// CompactionPrompt replaces the system prompt of the call that writes a
	// compaction summary (DefaultCompactionPrompt when empty).
	CompactionPrompt string
	// CompactionInputTokens bounds what one summary call is sent, in
	// estimated tokens. A longer span is sent as a transcript with long
	// blocks clipped and its start left out. Zero means 100,000.
	CompactionInputTokens int
	// SlotBudgets overrides the token budget of the slots it names (sizes
	// are estimated at about 4 bytes a token). Budgets must be positive, and
	// the Universal slot cannot be named: its budget is set by the base
	// prompt. An entry over budget is replaced by its pointer, trimmed
	// with a marker, or, with no room left at all, left out; trims and
	// omissions are logged and reported in SlotsAssembled.
	SlotBudgets map[SlotName]int
}

// TraceRecord is one turn's trace record. Its schema is documented in
// github.com/hollis-labs/libs/agent/trace.
type TraceRecord = trace.Turn

// PanicReporter receives the record of a contained panic.
type PanicReporter = panics.Reporter

// PanicRecord is the redacted record of a contained panic.
type PanicRecord = panics.Record

// PanicRef returns the ref of a contained panic carried by err.
func PanicRef(err error) (string, bool) { return panics.Ref(err) }

// Gateway configures the model gateway.
type Gateway = provider.Gateway

// ProviderSpec describes a named provider: its wire (anthropic, openai,
// openai-chat or openai-responses), base URL, key reference, auth scheme
// and headers. Any OpenAI-compatible server uses the openai-chat wire.
type ProviderSpec = provider.Spec

// Agent owns one authored agent directory and its mutable host contributions.
type Agent struct {
	publication *os.File
	control     *control.Server
	engine      *engine.Engine
	dir         string
	lock        *os.File
	closeOnce   sync.Once
	store       *nanitestore.Store
	closeErr    error
	slots       Slots
	tools       ToolSet
	skills      SkillSet
	// ensureMu serializes EnsureConversation's check-then-create.
	ensureMu sync.Mutex
}

// Info describes the current definition loaded from disk.
type Info struct {
	// Dir is the absolute path to the authored agent directory.
	Dir string
	// Definition contains the authored agent files as of the last Info call.
	Definition agentdir.Snapshot
}

// Open validates the authored agent directory and takes its exclusive lock.
func Open(ctx context.Context, opts Options) (*Agent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if opts.Dir == "" {
		return nil, fmt.Errorf("nanite: agent directory required")
	}
	if opts.WorkDir != "" {
		if !filepath.IsAbs(opts.WorkDir) {
			return nil, fmt.Errorf("nanite: WorkDir %q must be absolute", opts.WorkDir)
		}
		if info, err := os.Stat(opts.WorkDir); err != nil || !info.IsDir() {
			return nil, fmt.Errorf("nanite: WorkDir %q must be an existing directory", opts.WorkDir)
		}
	}
	if opts.MaxOutputTokens < 0 || opts.MaxSteps < 0 {
		return nil, fmt.Errorf("nanite: MaxOutputTokens and MaxSteps must not be negative")
	}
	if opts.CompactAt < 0 {
		return nil, fmt.Errorf("nanite: CompactAt must not be negative")
	}
	if opts.DailyTokenBudget < 0 {
		return nil, fmt.Errorf("nanite: DailyTokenBudget must not be negative")
	}
	if opts.CompactKeepTokens < 0 || opts.CompactionInputTokens < 0 {
		return nil, fmt.Errorf("nanite: CompactKeepTokens and CompactionInputTokens must not be negative")
	}
	for name, size := range opts.SlotBudgets {
		switch {
		case !slices.Contains(SlotNames(), name):
			return nil, fmt.Errorf("nanite: SlotBudgets names unknown slot %q", name)
		case name == SlotUniversal:
			return nil, fmt.Errorf("nanite: the universal slot's budget is fixed by the base prompt")
		case size <= 0:
			return nil, fmt.Errorf("nanite: slot %s budget must be positive", name)
		}
	}
	if opts.SubtaskResultCap < 0 {
		return nil, fmt.Errorf("nanite: SubtaskResultCap must not be negative")
	}
	if opts.SubtaskLimit < 0 || opts.SubtaskLimit > subagent.MaxLimit {
		return nil, fmt.Errorf("nanite: SubtaskLimit must be between 0 and %d", subagent.MaxLimit)
	}
	if opts.IdleTimeout < 0 {
		return nil, fmt.Errorf("nanite: IdleTimeout must not be negative")
	}
	if opts.TurnTokenBudget < 0 {
		return nil, fmt.Errorf("nanite: TurnTokenBudget must not be negative")
	}
	publication, err := agentlock.Publication(opts.Dir, true)
	if err != nil {
		return nil, err
	}
	keepPublication := false
	defer func() {
		if !keepPublication {
			_ = publication.Close()
		}
	}()
	def, err := agentdir.Load(opts.Dir)
	if err != nil {
		return nil, fmt.Errorf("load agent: %w", err)
	}
	if opts.DailyTokenBudget == 0 {
		opts.DailyTokenBudget = def.Profile.Runtime.DailyTokenBudget
	}
	subtasks, err := subtaskSettings(opts, def.Profile.Runtime)
	if err != nil {
		return nil, fmt.Errorf("load agent: %w", err)
	}
	approvalDir := ""
	if opts.DataDir != "" {
		approvalDir, err = filepath.Abs(filepath.Join(opts.DataDir, "tool-approvals"))
		if err != nil {
			return nil, err
		}
	}
	rt, err := runtimeSettings(opts, def.Profile.Runtime)
	if err != nil {
		return nil, err
	}
	data := opts.DataDir
	if data == "" {
		data = filepath.Join(def.Directory, ".nanite")
	}
	if err := nanitestore.PreparePrivateDir(data); err != nil {
		return nil, fmt.Errorf("create agent data directory: %w", err)
	}
	traceDir := ""
	if opts.TraceFiles {
		traceDir = filepath.Join(data, "traces")
	}
	lock, err := os.OpenFile(filepath.Join(data, "agent.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600) // #nosec G304 -- The caller selects the private agent state directory.
	if err != nil {
		return nil, fmt.Errorf("open agent lock: %w", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil { // #nosec G115 -- File.Fd carries a Unix file descriptor.
		_ = lock.Close()
		return nil, fmt.Errorf("lock agent data directory %q: %w", data, err)
	}
	if err := lock.Chmod(0600); err != nil {
		_ = lock.Close()
		return nil, err
	}
	db, err := nanitestore.Open(ctx, data, opts.Store)
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	if err := recovery.Recover(ctx, db); err != nil {
		_ = db.Close()
		_ = lock.Close()
		return nil, fmt.Errorf("recover agent: %w", err)
	}
	a := &Agent{dir: def.Directory, publication: publication, lock: lock, store: db, slots: Slots{store: slots.NewStore()}}
	roots := []string{def.Directory}
	work := opts.WorkDir
	if work == "" {
		work, _ = os.Getwd()
	}
	if work != "" && filepath.Clean(work) != def.Directory {
		roots = append([]string{filepath.Clean(work)}, roots...)
	}
	offered := opts.Builtins
	if offered == nil && def.Profile.Kind == agentdir.Embedded {
		// An application decides what its embedded agent can do: no built-in
		// group unless the host names it.
		offered = []BuiltinGroup{}
	}
	builtins, err := tools.NewBuiltins(offered)
	if err != nil {
		_ = db.Close()
		_ = lock.Close()
		return nil, err
	}
	var identity *authored.Identity
	if id, ok, err := authored.LoadIdentity(def.Directory); err != nil {
		_ = db.Close()
		_ = lock.Close()
		return nil, fmt.Errorf("load agent: %w", err)
	} else if ok {
		identity = &id
	}
	var onWaiting, onGranted func(warden.Waiting)
	if opts.OnAuthWaiting != nil {
		onWaiting = func(w warden.Waiting) { opts.OnAuthWaiting(AuthWaiting(w)) }
	}
	if opts.OnAuthGranted != nil {
		onGranted = func(w warden.Waiting) { opts.OnAuthGranted(AuthWaiting(w)) }
	}
	providers := make(map[string]provider.Spec, len(opts.Providers))
	for _, spec := range opts.Providers {
		if err := spec.Validate(); err != nil {
			_ = db.Close()
			_ = lock.Close()
			return nil, err
		}
		providers[spec.Name] = spec
	}
	a.engine, err = engine.New(engine.Config{
		Logger: opts.Logger, Events: opts.Events, Dir: def.Directory, GlobalSkillsDir: opts.GlobalSkillsDir, ToolPackageEnv: opts.ToolPackageEnv, ToolRegistry: opts.ToolRegistry, ToolApprovalDir: approvalDir, Builtins: builtins, Provider: opts.Provider, Store: db, Gate: opts.Gate, AutoApprove: opts.AutoApprove, AskWait: opts.SubtaskAskWait, PDP: opts.PDP,
		ProviderConfig: provider.Config{Gateway: opts.Gateway, Secrets: opts.Secrets}, Providers: providers, Identity: identity, OnAuthWaiting: onWaiting, OnAuthGranted: onGranted,
		Slots: a.slots.store, SlotSources: slices.Clone(opts.SlotSources), SlotSelector: opts.SlotSelector,
		Tools: &a.tools.groups, Skills: &a.skills.groups, WorkspaceRoots: roots, PanicReporter: opts.PanicReporter,
		DailyTokenBudget: opts.DailyTokenBudget, MaxOutputTokens: opts.MaxOutputTokens, MaxSteps: opts.MaxSteps,
		TraceSink: opts.TraceSink, TraceDir: traceDir, SubtaskResultCap: subtasks.resultCap, SubtaskLimit: subtasks.limit, TurnTokenBudget: subtasks.turnBudget, IdleTimeout: rt.idleTimeout, CompactAt: rt.compactAt, CompactKeepTokens: rt.keepTokens, CompactionPrompt: rt.prompt, CompactionInputTokens: rt.inputTokens, Steering: opts.Steering,
		SlotBudgets: maps.Clone(opts.SlotBudgets), SlotPlacement: maps.Clone(opts.SlotPlacement),
	})
	if err != nil {
		_ = db.Close()
		_ = lock.Close()
		return nil, err
	}
	a.control, err = control.Start(data, a.StopAll)
	if err != nil {
		logger := opts.Logger
		if logger == nil {
			logger = slog.Default()
		}
		logger.Warn("agent CLI control unavailable; embedding StopAll remains available", "error", err)
	}
	keepPublication = true
	return a, nil
}

// Close releases this agent's resources and directory lock.
func (a *Agent) Close() error {
	a.closeOnce.Do(func() {
		controlErr := a.control.Close()
		a.engine.Close()
		a.closeErr = errors.Join(controlErr, a.store.Close(), a.lock.Close(), a.publication.Close())
	})
	return a.closeErr
}

// UseDesk names the providers.yaml use for the front desk's planner. Like
// every use, it falls back to chat when the file does not select it.
const UseDesk = authored.UseDesk

// Complete makes one model request outside any conversation: the provider
// and model providers.yaml selects for use (UseDesk, or "chat",
// "summarizer", "subtask"), a system prompt and one user message, at most
// maxTokens out (default 2048). It is for host helpers such as a planner;
// it runs no tools and records nothing.
func (a *Agent) Complete(ctx context.Context, use, system, prompt string, maxTokens int) (string, error) {
	return a.engine.Complete(ctx, use, system, prompt, maxTokens)
}

// Info re-reads the authored files and reports the current definition.
func (a *Agent) Info(ctx context.Context) (Info, error) {
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	def, err := agentdir.Load(a.dir)
	if err != nil {
		return Info{}, err
	}
	return Info{Dir: a.dir, Definition: def}, nil
}

// Slots returns the host slot writer collection.
func (a *Agent) Slots() *Slots { return &a.slots }

// SlotBudget is the token budget of the named slot, so a host can size what
// it writes there. Zero means the slot has no fixed budget.
func (a *Agent) SlotBudget(name SlotName) int { return a.engine.SlotBudgets()[name] }

// Tools returns the host tool groups.
func (a *Agent) Tools() *ToolSet { return &a.tools }

// Skills returns the host skill groups.
func (a *Agent) Skills() *SkillSet { return &a.skills }

// SubscribeNotices returns every notice posted from now on (a background
// job or subtask finishing, a relayed ask waiting for a turn, a host's
// PostNotice), in order and without loss, until ctx ends or the agent
// closes; then the channel is closed. Each subscriber gets every notice. A
// notice also waits in the store for its conversation's next turn.
func (a *Agent) SubscribeNotices(ctx context.Context) <-chan NoticePosted {
	return a.engine.SubscribeNotices(ctx)
}

// RebuildIndex reconstructs the SQLite conversation projection from transcripts.
func (a *Agent) RebuildIndex(ctx context.Context) error {
	return a.engine.Maintain(ctx, a.store.RebuildIndex)
}

// subtaskConfig is the agent's subtask settings after precedence.
type subtaskConfig struct{ limit, turnBudget, resultCap int }

// subtaskSettings resolves the subtask settings: a non-zero Option wins, then
// host.yaml's runtime value, then the engine's default (zero). host.yaml is
// read when the agent opens. A host.yaml subtask_limit above the most the
// agent runs at once is refused, not clamped.
// runtimeConfig is the compaction and stream settings an agent runs with.
type runtimeConfig struct {
	compactAt, keepTokens, inputTokens int
	prompt                             string
	idleTimeout                        time.Duration
}

// runtimeSettings resolves the compaction and stream settings: a non-zero
// Option wins over host.yaml's runtime key, which is read when the agent
// opens.
func runtimeSettings(opts Options, runtime agentdir.Runtime) (runtimeConfig, error) {
	idle, err := runtime.IdleTimeoutDuration()
	if err != nil {
		return runtimeConfig{}, fmt.Errorf("host.yaml runtime: %w", err)
	}
	if opts.IdleTimeout != 0 {
		idle = opts.IdleTimeout
	}
	pick := func(option, file int) int {
		if option != 0 {
			return option
		}
		return file
	}
	prompt := opts.CompactionPrompt
	if prompt == "" {
		prompt = runtime.CompactPrompt
	}
	return runtimeConfig{
		compactAt:   pick(opts.CompactAt, runtime.CompactAt),
		keepTokens:  pick(opts.CompactKeepTokens, runtime.CompactKeepTokens),
		inputTokens: pick(opts.CompactionInputTokens, runtime.CompactInputTokens),
		prompt:      prompt,
		idleTimeout: idle,
	}, nil
}

func subtaskSettings(opts Options, runtime agentdir.Runtime) (subtaskConfig, error) {
	if err := subagent.CheckLimit(runtime.SubtaskLimit); err != nil {
		return subtaskConfig{}, fmt.Errorf("host.yaml runtime: %w", err)
	}
	pick := func(option, file int) int {
		if option != 0 {
			return option
		}
		return file
	}
	return subtaskConfig{
		limit:      pick(opts.SubtaskLimit, runtime.SubtaskLimit),
		turnBudget: pick(opts.TurnTokenBudget, runtime.TurnTokenBudget),
		resultCap:  pick(opts.SubtaskResultCap, runtime.SubtaskResultCap),
	}, nil
}

// ActivityAuthWaiting is the activity kind for an AuthWaiting.
const ActivityAuthWaiting = warden.AuthWaitingActivity

// AuthWaiting is a Warden grant request Nanite filed, or found pending, for
// an audience and scopes the agent has no grant for. Nothing in it is secret.
type AuthWaiting struct {
	RequestID string   `json:"request_id"`
	Status    string   `json:"status"`
	Tier      string   `json:"tier,omitempty"`
	Audience  string   `json:"audience"`
	Scopes    []string `json:"scopes,omitempty"`
	// Conversation is the conversation whose turn needed the grant ("" when
	// the need came from outside a turn).
	Conversation string `json:"conversation,omitempty"`
	// For names what needed it: "provider <name>" or "MCP server <name>".
	For string `json:"for,omitempty"`
}

// ErrNoWarden means the agent has no Warden identity (identity.yaml).
var ErrNoWarden = errors.New("nanite: the agent has no warden identity")

// AuthRequest looks up a Warden grant request's current status: "pending",
// "approved", "denied", "expired" or another Warden status.
func (a *Agent) AuthRequest(ctx context.Context, id string) (AuthWaiting, error) {
	src := a.engine.Warden()
	if src == nil {
		return AuthWaiting{}, ErrNoWarden
	}
	w, err := src.Request(ctx, id)
	return AuthWaiting(w), err
}

// AuthDecided tells the agent a grant request has been decided with status.
// On approval the next token request looks it up at once instead of after
// the recheck interval; on a refusal ("denied", "expired", "withdrawn", ...)
// the request is forgotten and the next token request files a new one.
func (a *Agent) AuthDecided(id, status string) {
	if src := a.engine.Warden(); src != nil {
		src.Decided(id, status)
	}
}

// ErrAuthRequestGone means Warden no longer shows a grant request to the
// agent (not found or forbidden): it will never be decided for it.
var ErrAuthRequestGone = warden.ErrRequestGone

// IsAuthWaiting reports whether err is a turn failing because a Warden grant
// is still pending, and returns the request.
func IsAuthWaiting(err error) (AuthWaiting, bool) {
	w, ok := warden.AsMissingGrant(err)
	return AuthWaiting(w), ok
}

// ErrReenroll means the agent's Warden parent credential can no longer be
// used: a token request's answer was lost, the rotated credential could not
// be saved, or Warden refused it. Whoever enrolled the agent places a new
// credential and removes the <credential_file>.inflight marker.
var ErrReenroll = warden.ErrReenroll

// DefaultCompactionPrompt is the system prompt of the call that writes a
// compaction summary when Options.CompactionPrompt is empty.
const DefaultCompactionPrompt = engine.DefaultCompactionPrompt
