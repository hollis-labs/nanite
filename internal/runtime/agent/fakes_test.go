package agent

import (
	"errors"
	"sync"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/store"
)

// fakeAgentProfiles is the test substitute for the AgentProfiles
// dependency. Returns the configured profile or an error.
type fakeAgentProfiles struct {
	profile *store.AgentProfile
	err     error
}

func (f *fakeAgentProfiles) GetOrDefault(string) (*store.AgentProfile, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.profile, nil
}

// fakeRuntimeStore captures every interaction so tests can assert on the
// sequence of calls + persisted row state.
type fakeRuntimeStore struct {
	mu         sync.Mutex
	created    []*RuntimeRow
	failed     map[string]string
	orphaned   map[string]string
	provIDs    map[string]string
	states     []fakeStateUpdate
	checkpoint *RuntimeCheckpoint
	createErr  error
	listRows   []*RuntimeRow
	events     []fakeLoggedEvent
}

// fakeStateUpdate captures one UpdateState call for test assertions.
type fakeStateUpdate struct {
	ID    string
	State string
	PID   int
}

// fakeLoggedEvent captures one LogEvent call for test assertions.
type fakeLoggedEvent struct {
	SessionID string
	EventType string
	Category  string
	Detail    string
	Metadata  string
}

func newFakeRuntimeStore() *fakeRuntimeStore {
	return &fakeRuntimeStore{
		failed:   map[string]string{},
		orphaned: map[string]string{},
		provIDs:  map[string]string{},
	}
}

func (f *fakeRuntimeStore) CreateRuntimeRow(row *RuntimeRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return f.createErr
	}
	f.created = append(f.created, row)
	return nil
}

func (f *fakeRuntimeStore) MarkRuntimeFailed(id, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failed[id] = reason
	return nil
}

func (f *fakeRuntimeStore) UpdateState(id, state string, pid int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states = append(f.states, fakeStateUpdate{ID: id, State: state, PID: pid})
	return nil
}

func (f *fakeRuntimeStore) MarkRuntimeOrphaned(id, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.orphaned[id] = reason
	return nil
}

func (f *fakeRuntimeStore) SetProviderSessionID(id, providerSessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.provIDs[id] = providerSessionID
	return nil
}

func (f *fakeRuntimeStore) GetCheckpoint(string) (*RuntimeCheckpoint, error) {
	if f.checkpoint == nil {
		return nil, errors.New("fakeRuntimeStore: no checkpoint")
	}
	return f.checkpoint, nil
}

func (f *fakeRuntimeStore) ListRunningRows() ([]*RuntimeRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*RuntimeRow, len(f.listRows))
	copy(out, f.listRows)
	return out, nil
}

func (f *fakeRuntimeStore) LogEvent(sessionID, eventType, category, detail, metadata string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, fakeLoggedEvent{
		SessionID: sessionID,
		EventType: eventType,
		Category:  category,
		Detail:    detail,
		Metadata:  metadata,
	})
}

// fakeAdapter is a minimal CLIAdapter that produces predictable BuildArgs
// output and reports the binary as detected; never spawns real processes
// because the test code only exercises code paths up to (not including)
// runner.Run.
type fakeAdapter struct {
	name string
}

func (f *fakeAdapter) Name() string { return f.name }
func (f *fakeAdapter) BuildArgs(prompt, system, sessID string) []string {
	return []string{"--prompt", prompt}
}
func (f *fakeAdapter) ParseLine(line []byte) ([]llmtypes.StreamEvent, error) { return nil, nil }

// Detect returns /usr/bin/true on darwin/linux so AutoFireFirstTurn modes
// (Subagent / Background / OneShot) can run runner.Run end-to-end without
// hitting fork/exec failures. /usr/bin/true exits 0 immediately.
func (f *fakeAdapter) Detect() (string, bool) { return "/usr/bin/true", true }
