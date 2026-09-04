package workflowapi

import "sync"

// RunStore keeps a bounded, process-local compatibility history for clients
// still reading runs created by the retired workflow API engine.
type RunStore struct {
	mu   sync.RWMutex
	runs []*RunRecord
	cap  int
	idx  map[string]int
}

// NewRunStore creates a RunStore with the given capacity. Non-positive
// capacities use the API's historical default of 50 records.
func NewRunStore(capacity int) *RunStore {
	if capacity <= 0 {
		capacity = 50
	}
	return &RunStore{
		runs: make([]*RunRecord, 0, capacity),
		cap:  capacity,
		idx:  make(map[string]int),
	}
}

// Add inserts a new run. At capacity, it evicts the oldest run.
func (s *RunStore) Add(info PipelineInfo, run *RunState) {
	s.mu.Lock()
	defer s.mu.Unlock()

	record := &RunRecord{Pipeline: info, Run: run, Events: []Event{}}
	if len(s.runs) < s.cap {
		s.idx[run.RunID] = len(s.runs)
		s.runs = append(s.runs, record)
		return
	}

	delete(s.idx, s.runs[0].Run.RunID)
	s.runs = append(s.runs[1:], record)
	for i, existing := range s.runs {
		s.idx[existing.Run.RunID] = i
	}
}

// Get returns the record for runID.
func (s *RunStore) Get(runID string) (*RunRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	i, ok := s.idx[runID]
	if !ok {
		return nil, false
	}
	return s.runs[i], true
}

// List returns runs filtered by optional pipeline and status, newest first.
func (s *RunStore) List(pipelineID, status string) []*RunRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*RunRecord, 0, len(s.runs))
	for i := len(s.runs) - 1; i >= 0; i-- {
		record := s.runs[i]
		if pipelineID != "" && record.Pipeline.ID != pipelineID {
			continue
		}
		if status != "" && string(record.Run.Status) != status {
			continue
		}
		result = append(result, record)
	}
	return result
}

// SetStatus updates a process-local run's compatibility status.
func (s *RunStore) SetStatus(runID string, status RunStatus) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	i, ok := s.idx[runID]
	if !ok {
		return false
	}
	s.runs[i].Run.Status = status
	return true
}

// AppendEvent appends an event when runID is present.
func (s *RunStore) AppendEvent(runID string, event Event) {
	s.mu.Lock()
	defer s.mu.Unlock()

	i, ok := s.idx[runID]
	if !ok {
		return
	}
	s.runs[i].Events = append(s.runs[i].Events, event)
}
