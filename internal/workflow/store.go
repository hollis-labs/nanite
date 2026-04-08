package workflow

import "sync"

// PipelineInfo holds metadata about a pipeline for display purposes.
type PipelineInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	StepCount   int    `json:"step_count"`
}

// RunRecord holds a pipeline run and its associated metadata.
type RunRecord struct {
	Pipeline PipelineInfo `json:"pipeline"`
	Run      *RunState    `json:"run"`
	Events   []Event      `json:"events"`
}

// RunStore is a thread-safe, fixed-capacity ring buffer of recent workflow runs.
type RunStore struct {
	mu   sync.RWMutex
	runs []*RunRecord
	cap  int
	idx  map[string]int // runID -> index in runs slice
}

// NewRunStore creates a RunStore with the given capacity. If cap <= 0, defaults to 50.
func NewRunStore(cap int) *RunStore {
	if cap <= 0 {
		cap = 50
	}
	return &RunStore{
		runs: make([]*RunRecord, 0, cap),
		cap:  cap,
		idx:  make(map[string]int),
	}
}

// Add inserts a new RunRecord. If at capacity, the oldest run is evicted and
// the index is rebuilt to reflect the updated positions.
func (s *RunStore) Add(info PipelineInfo, run *RunState) {
	s.mu.Lock()
	defer s.mu.Unlock()

	record := &RunRecord{
		Pipeline: info,
		Run:      run,
		Events:   []Event{},
	}

	if len(s.runs) < s.cap {
		s.idx[run.RunID] = len(s.runs)
		s.runs = append(s.runs, record)
		return
	}

	// At capacity — evict the oldest (index 0) by shifting left.
	delete(s.idx, s.runs[0].Run.RunID)
	s.runs = append(s.runs[1:], record)

	// Rebuild index after shift.
	for i, r := range s.runs {
		s.idx[r.Run.RunID] = i
	}
}

// Get returns the RunRecord for the given runID, or (nil, false) if not found.
func (s *RunStore) Get(runID string) (*RunRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	i, ok := s.idx[runID]
	if !ok {
		return nil, false
	}
	return s.runs[i], true
}

// List returns runs filtered by optional pipelineID and/or status, newest first.
// Empty string means "no filter" for that field.
func (s *RunStore) List(pipelineID, status string) []*RunRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*RunRecord, 0, len(s.runs))
	for i := len(s.runs) - 1; i >= 0; i-- {
		r := s.runs[i]
		if pipelineID != "" && r.Pipeline.ID != pipelineID {
			continue
		}
		if status != "" && string(r.Run.Status) != status {
			continue
		}
		result = append(result, r)
	}
	return result
}

// AppendEvent appends an event to the event log for the given runID.
// If the runID is not found, the call is a no-op.
func (s *RunStore) AppendEvent(runID string, event Event) {
	s.mu.Lock()
	defer s.mu.Unlock()

	i, ok := s.idx[runID]
	if !ok {
		return
	}
	s.runs[i].Events = append(s.runs[i].Events, event)
}
