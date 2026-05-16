package launcher

import (
	"errors"
	"sync"

	agentsessions "github.com/hollis-labs/go-agent-sessions/agentsessions"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
	"github.com/hollis-labs/nanite/internal/store"
)

// memoryRuntimeStore is the in-memory RuntimeStore + StateSink used by a
// standalone launch when no *store.Store is supplied (tests, store-free
// operator runs). It records lifecycle rows and state transitions so the
// launch is observable in-process, but persists nothing to disk.
//
// It satisfies both runtimeagent.RuntimeStore (the row-persistence
// contract agent.Boot writes to) and agentsessions.StateSink (the
// runtime's per-session state-transition callback) so a single value
// covers both wiring points.
type memoryRuntimeStore struct {
	mu     sync.Mutex
	rows   map[string]*runtimeagent.RuntimeRow
	states map[string]agentsessions.State
}

func newMemoryRuntimeStore() *memoryRuntimeStore {
	return &memoryRuntimeStore{
		rows:   map[string]*runtimeagent.RuntimeRow{},
		states: map[string]agentsessions.State{},
	}
}

// --- runtimeagent.RuntimeStore ---

func (m *memoryRuntimeStore) CreateRuntimeRow(row *runtimeagent.RuntimeRow) error {
	if row == nil {
		return errors.New("launcher: memory runtime store: nil row")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.rows[row.ID]; exists {
		return errors.New("launcher: memory runtime store: duplicate runtime id " + row.ID)
	}
	cp := *row
	m.rows[row.ID] = &cp
	return nil
}

func (m *memoryRuntimeStore) MarkRuntimeFailed(id, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.rows[id]; ok {
		r.State = "failed"
	}
	return nil
}

func (m *memoryRuntimeStore) MarkRuntimeOrphaned(id, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.rows[id]; ok {
		r.State = "orphaned"
	}
	return nil
}

func (m *memoryRuntimeStore) SetProviderSessionID(id, providerSessionID string) error {
	// The standalone launcher does not resume from checkpoints, so the
	// provider session id is recorded only for in-process introspection.
	return nil
}

func (m *memoryRuntimeStore) GetCheckpoint(string) (*runtimeagent.RuntimeCheckpoint, error) {
	return nil, errors.New("launcher: memory runtime store: no checkpoints (standalone launch does not resume)")
}

func (m *memoryRuntimeStore) ListRunningRows() ([]*runtimeagent.RuntimeRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*runtimeagent.RuntimeRow
	for _, r := range m.rows {
		if r.State == "launching" || r.State == "running" {
			cp := *r
			out = append(out, &cp)
		}
	}
	return out, nil
}

// --- agentsessions.StateSink ---

func (m *memoryRuntimeStore) UpdateSessionState(id string, state agentsessions.State, pid int, exit *int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states[id] = state
	if r, ok := m.rows[id]; ok {
		r.State = string(state)
		r.PID = pid
	}
	return nil
}

// Compile-time assertions.
var (
	_ runtimeagent.RuntimeStore = (*memoryRuntimeStore)(nil)
	_ agentsessions.StateSink   = (*memoryRuntimeStore)(nil)
)

// storeRuntimeStore is the *store.Store-backed RuntimeStore used when
// Config.Store is set. It mirrors service.agentRuntimeStore so a
// standalone launch persists its runtime lifecycle row into the same
// agent_runtime table chat-spawned sessions use — an operator can then
// introspect / orphan-sweep a standalone launch identically.
type storeRuntimeStore struct {
	store *store.Store
}

func (s *storeRuntimeStore) CreateRuntimeRow(row *runtimeagent.RuntimeRow) error {
	if row == nil {
		return errors.New("launcher: store runtime store: nil row")
	}
	parent := ""
	if row.ParentSessionID != nil {
		parent = *row.ParentSessionID
	}
	return s.store.CreateAgentRuntimeRow(&store.AgentRuntimeRow{
		ID:              row.ID,
		AgentProfile:    row.AgentProfile,
		Provider:        row.Provider,
		Mode:            row.Mode,
		Workdir:         row.Workdir,
		State:           row.State,
		PID:             row.PID,
		ParentSessionID: parent,
		StartedAt:       row.StartedAt,
	})
}

func (s *storeRuntimeStore) MarkRuntimeFailed(id, reason string) error {
	return s.store.MarkAgentRuntimeFailed(id, reason)
}

func (s *storeRuntimeStore) MarkRuntimeOrphaned(id, reason string) error {
	return s.store.MarkAgentRuntimeOrphaned(id, reason)
}

func (s *storeRuntimeStore) SetProviderSessionID(id, providerSessionID string) error {
	return s.store.SetAgentRuntimeProviderSessionID(id, providerSessionID)
}

func (s *storeRuntimeStore) GetCheckpoint(id string) (*runtimeagent.RuntimeCheckpoint, error) {
	cp, err := s.store.GetAgentRuntimeCheckpoint(id)
	if err != nil {
		return nil, err
	}
	return &runtimeagent.RuntimeCheckpoint{
		ID:                cp.ID,
		ProviderSessionID: cp.ProviderSessionID,
		CapturedAt:        cp.CapturedAt,
	}, nil
}

func (s *storeRuntimeStore) ListRunningRows() ([]*runtimeagent.RuntimeRow, error) {
	rows, err := s.store.ListRunningAgentRuntimeRows()
	if err != nil {
		return nil, err
	}
	out := make([]*runtimeagent.RuntimeRow, 0, len(rows))
	for _, r := range rows {
		var parent *string
		if r.ParentSessionID != "" {
			p := r.ParentSessionID
			parent = &p
		}
		out = append(out, &runtimeagent.RuntimeRow{
			ID:              r.ID,
			AgentProfile:    r.AgentProfile,
			Provider:        r.Provider,
			Mode:            r.Mode,
			Workdir:         r.Workdir,
			State:           r.State,
			PID:             r.PID,
			ParentSessionID: parent,
			StartedAt:       r.StartedAt,
		})
	}
	return out, nil
}

// storeStateSink is the *store.Store-backed StateSink. It persists
// runtime state transitions to the agent_runtime table.
type storeStateSink struct {
	store *store.Store
}

func (s *storeStateSink) UpdateSessionState(id string, state agentsessions.State, pid int, exit *int) error {
	return s.store.SetAgentRuntimeState(id, string(state), pid)
}

var (
	_ runtimeagent.RuntimeStore = (*storeRuntimeStore)(nil)
	_ agentsessions.StateSink   = (*storeStateSink)(nil)
)
