// Package agent embeds Nanite in a Go application.
package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/hollis-labs/libs/agent/agentdir"
	"github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/libs/llm/core/llmcontracts"
	"github.com/hollis-labs/libs/security/policy"
	"github.com/hollis-labs/nanite/internal/slots"
)

var (
	// ErrBusy means a run is already active in the conversation.
	ErrBusy = errors.New("nanite: conversation is busy")
	// ErrNoConversation means the requested conversation does not exist.
	ErrNoConversation = errors.New("nanite: no conversation")
	// ErrNotImplemented means the engine does not yet implement the operation.
	ErrNotImplemented = errors.New("nanite: not implemented")
)

// Options configures an embedded agent.
type Options struct {
	Dir, DataDir, GlobalSkillsDir string
	Provider                      llmcontracts.Provider
	Gateway                       Gateway
	Store                         *store.Store
	Logger                        *slog.Logger
	Events                        EventSink
	Gate                          Gate
	PDP                           policy.PDP
	SlotSources                   []SlotSource
	SlotSelector                  SlotSelector
}

// Gateway configures the model gateway.
type Gateway struct {
	BaseURL string
	Headers map[string]string
	KeyRef  string
	Auth    string
}

// Agent owns one authored agent directory and its mutable host contributions.
type Agent struct {
	dir       string
	lock      *os.File
	closeOnce sync.Once
	closeErr  error
	slots     Slots
	tools     ToolSet
	skills    SkillSet
	notices   chan Notice
}

// Info describes the current definition loaded from disk.
type Info struct {
	Dir        string
	Definition agentdir.Snapshot
}

// Notice reports an asynchronous completion.
type Notice struct{ ID, ConversationID, Kind, Body string }

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
	if err := os.MkdirAll(data, 0700); err != nil {
		return nil, fmt.Errorf("create agent data directory: %w", err)
	}
	lock, err := os.OpenFile(filepath.Join(data, "agent.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open agent lock: %w", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("lock agent data directory %q: %w", data, err)
	}
	return &Agent{dir: def.Directory, lock: lock, slots: Slots{store: slots.NewStore()}, notices: make(chan Notice)}, nil
}

// Close releases this agent's resources and directory lock.
func (a *Agent) Close() error {
	a.closeOnce.Do(func() {
		close(a.notices)
		a.closeErr = a.lock.Close()
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
func (a *Agent) Notices() <-chan Notice { return a.notices }
