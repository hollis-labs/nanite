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
	"github.com/hollis-labs/nanite/internal/tools"

	"github.com/hollis-labs/libs/agent/agentdir"
	"github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/libs/llm/core/llmcontracts"
	"github.com/hollis-labs/libs/security/policy"
	"github.com/hollis-labs/libs/security/secrets"
	"github.com/hollis-labs/nanite/internal/engine"
	"github.com/hollis-labs/nanite/internal/provider"
	"github.com/hollis-labs/nanite/internal/recovery"
	"github.com/hollis-labs/nanite/internal/slots"
	nanitestore "github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
)

var (
	// ErrBusy means a run is already active in the conversation.
	ErrBusy = engine.ErrBusy
	// ErrNoConversation means the requested conversation does not exist.
	ErrNoConversation = nanitestore.ErrNoConversation
)

// Options configures an embedded agent.
type Options struct {
	// Dir is the required authored agent directory.
	Dir string
	// DataDir defaults to .nanite inside Dir.
	DataDir string
	// GlobalSkillsDir defaults to ~/.nanite/skills. Use "-" to disable it.
	GlobalSkillsDir string
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
	engine    *engine.Engine
	dir       string
	lock      *os.File
	closeOnce sync.Once
	store     *nanitestore.Store
	closeErr  error
	slots     Slots
	tools     ToolSet
	skills    SkillSet
}

// Info describes the current definition loaded from disk.
type Info struct {
	// Dir is the absolute path to the authored agent directory.
	Dir string
	// Definition contains the authored agent files as of the last Info call.
	Definition agentdir.Snapshot
}

// Notice describes a completed job. ID identifies the notification, ConversationID
// selects the recipient, Kind identifies the job type, and Body is the display text.
type Notice = nanitestore.Notice

// Open validates the authored agent directory and takes its exclusive lock.
func Open(ctx context.Context, opts Options) (*Agent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if opts.Dir == "" {
		return nil, fmt.Errorf("nanite: agent directory required")
	}
	if opts.CompactAt < 0 {
		return nil, fmt.Errorf("nanite: CompactAt must not be negative")
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
	if opts.TurnTokenBudget < 0 {
		return nil, fmt.Errorf("nanite: TurnTokenBudget must not be negative")
	}
	def, err := agentdir.Load(opts.Dir)
	if err != nil {
		return nil, fmt.Errorf("load agent: %w", err)
	}
	subtasks, err := subtaskSettings(opts, def.Profile.Runtime)
	if err != nil {
		return nil, fmt.Errorf("load agent: %w", err)
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
	a := &Agent{dir: def.Directory, lock: lock, store: db, slots: Slots{store: slots.NewStore()}}
	roots := []string{def.Directory}
	if cwd, err := os.Getwd(); err == nil && cwd != def.Directory {
		roots = append([]string{cwd}, roots...)
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
		Logger: opts.Logger, Events: opts.Events, Dir: def.Directory, GlobalSkillsDir: opts.GlobalSkillsDir, Builtins: builtins, Provider: opts.Provider, Store: db, Gate: opts.Gate, AskWait: opts.SubtaskAskWait, PDP: opts.PDP,
		ProviderConfig: provider.Config{Gateway: opts.Gateway, Secrets: opts.Secrets}, Providers: providers,
		Slots: a.slots.store, SlotSources: slices.Clone(opts.SlotSources), SlotSelector: opts.SlotSelector,
		Tools: &a.tools.groups, Skills: &a.skills.groups, WorkspaceRoots: roots, PanicReporter: opts.PanicReporter,
		TraceSink: opts.TraceSink, TraceDir: traceDir, SubtaskResultCap: subtasks.resultCap, SubtaskLimit: subtasks.limit, TurnTokenBudget: subtasks.turnBudget, CompactAt: opts.CompactAt,
		SlotBudgets: maps.Clone(opts.SlotBudgets), SlotPlacement: maps.Clone(opts.SlotPlacement),
	})
	if err != nil {
		_ = db.Close()
		_ = lock.Close()
		return nil, err
	}
	return a, nil
}

// Close releases this agent's resources and directory lock.
func (a *Agent) Close() error {
	a.closeOnce.Do(func() {
		a.engine.Close()
		a.closeErr = errors.Join(a.store.Close(), a.lock.Close())
	})
	return a.closeErr
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

// Notices returns asynchronous job and subagent notices.
func (a *Agent) Notices() <-chan Notice { return a.engine.Notices() }

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
