package agent

import (
	"context"
	"errors"
	"fmt"
	"github.com/hollis-labs/libs/core/panics"
	"github.com/hollis-labs/nanite/internal/tools"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"syscall"

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
	Provider llmcontracts.Provider
	// Gateway optionally directs requests through a model gateway. Its zero value uses the provider endpoint.
	Gateway Gateway
	// Store defaults to SQLite at DataDir/nanite.db. The caller retains ownership of a supplied store.
	Store *store.Store
	// Logger defaults to slog.Default().
	Logger *slog.Logger
	// Events defaults to nil, with no external runtime event sink.
	Events EventSink
	// Gate defaults to DenyGate for tool calls that require approval.
	Gate Gate
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
	// PanicReporter also receives each panic the agent contains. Nanite
	// contains panics in host tools, slot sources, the selector, the
	// approval gate, the PDP, the event sink, provider streams and its own
	// goroutines, so they fail a tool call or a turn rather than the host
	// process. The error the host sees carries only a ref ("internal error
	// (ref pnc_...)"); the record with the redacted value and stack is
	// logged to Logger and handed to this reporter. Nil logs only.
	PanicReporter PanicReporter
}

// PanicReporter receives the record of a contained panic.
type PanicReporter = panics.Reporter

// PanicRecord is the redacted record of a contained panic.
type PanicRecord = panics.Record

// PanicRef returns the ref of a contained panic carried by err.
func PanicRef(err error) (string, bool) { return panics.Ref(err) }

// Gateway configures the model gateway.
type Gateway = provider.Gateway

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
	def, err := agentdir.Load(opts.Dir)
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
	a.engine, err = engine.New(engine.Config{
		Logger: opts.Logger, Events: opts.Events, Dir: def.Directory, GlobalSkillsDir: opts.GlobalSkillsDir, Builtins: builtins, Provider: opts.Provider, Store: db, Gate: opts.Gate, PDP: opts.PDP,
		ProviderConfig: provider.Config{Gateway: opts.Gateway, Secrets: opts.Secrets},
		Slots:          a.slots.store, SlotSources: slices.Clone(opts.SlotSources), SlotSelector: opts.SlotSelector,
		Tools: &a.tools.groups, Skills: &a.skills.groups, WorkspaceRoots: roots, PanicReporter: opts.PanicReporter,
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
