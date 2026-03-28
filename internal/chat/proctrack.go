package chat

import (
	"log"
	"os"
	"sync"
	"time"
)

// ProcessTracker tracks CLI processes spawned by PTY/subprocess bridges,
// keyed by session ID. When a session is archived or the engine shuts down,
// tracked processes are killed.
type ProcessTracker struct {
	mu        sync.Mutex
	processes map[string][]*trackedProcess // sessionID → running processes
}

type trackedProcess struct {
	process      *os.Process
	startedAt    time.Time
	lastActivity time.Time // last time output was received from this process
}

// ProcessHealth describes the health status of a tracked process.
type ProcessHealth struct {
	SessionID    string        `json:"session_id"`
	PID          int           `json:"pid"`
	Uptime       time.Duration `json:"uptime"`
	IdleDuration time.Duration `json:"idle_duration"`
	IsStale      bool          `json:"is_stale"`
}

// NewProcessTracker creates a new process tracker.
func NewProcessTracker() *ProcessTracker {
	return &ProcessTracker{
		processes: make(map[string][]*trackedProcess),
	}
}

// Track registers a process under the given session ID.
func (pt *ProcessTracker) Track(sessionID string, proc *os.Process) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	now := time.Now()
	pt.processes[sessionID] = append(pt.processes[sessionID], &trackedProcess{
		process:      proc,
		startedAt:    now,
		lastActivity: now,
	})
}

// Untrack removes a specific process from tracking (called when process exits normally).
func (pt *ProcessTracker) Untrack(sessionID string, proc *os.Process) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	procs := pt.processes[sessionID]
	for i, tp := range procs {
		if tp.process.Pid == proc.Pid {
			pt.processes[sessionID] = append(procs[:i], procs[i+1:]...)
			break
		}
	}
	if len(pt.processes[sessionID]) == 0 {
		delete(pt.processes, sessionID)
	}
}

// KillSession kills all processes tracked for a session.
func (pt *ProcessTracker) KillSession(sessionID string) int {
	pt.mu.Lock()
	procs := pt.processes[sessionID]
	delete(pt.processes, sessionID)
	pt.mu.Unlock()

	killed := 0
	for _, tp := range procs {
		if err := tp.process.Kill(); err != nil {
			// Process may have already exited — that's fine.
			if !isProcessDone(err) {
				log.Printf("proctrack: failed to kill pid %d for session %s: %v", tp.process.Pid, sessionID, err)
			}
		} else {
			killed++
			log.Printf("proctrack: killed orphan pid %d for session %s (ran %s)", tp.process.Pid, sessionID, time.Since(tp.startedAt).Round(time.Second))
		}
	}
	return killed
}

// KillAll kills all tracked processes (used during engine shutdown).
func (pt *ProcessTracker) KillAll() int {
	pt.mu.Lock()
	all := make(map[string][]*trackedProcess, len(pt.processes))
	for k, v := range pt.processes {
		all[k] = v
	}
	pt.processes = make(map[string][]*trackedProcess)
	pt.mu.Unlock()

	killed := 0
	for sessionID, procs := range all {
		for _, tp := range procs {
			if err := tp.process.Kill(); err != nil {
				if !isProcessDone(err) {
					log.Printf("proctrack: shutdown kill failed for pid %d (session %s): %v", tp.process.Pid, sessionID, err)
				}
			} else {
				killed++
			}
		}
	}
	if killed > 0 {
		log.Printf("proctrack: shutdown killed %d orphan processes", killed)
	}
	return killed
}

// ActiveSessions returns session IDs with running processes.
func (pt *ProcessTracker) ActiveSessions() []string {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	ids := make([]string, 0, len(pt.processes))
	for id := range pt.processes {
		ids = append(ids, id)
	}
	return ids
}

// Count returns the total number of tracked processes across all sessions.
func (pt *ProcessTracker) Count() int {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	n := 0
	for _, procs := range pt.processes {
		n += len(procs)
	}
	return n
}

// Touch updates the last activity time for a process (called when output is received).
func (pt *ProcessTracker) Touch(sessionID string, pid int) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	for _, tp := range pt.processes[sessionID] {
		if tp.process.Pid == pid {
			tp.lastActivity = time.Now()
			return
		}
	}
}

// HealthCheck returns the health status of all tracked processes.
// A process is considered stale if it has produced no output for longer than staleThreshold.
func (pt *ProcessTracker) HealthCheck(staleThreshold time.Duration) []ProcessHealth {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	now := time.Now()
	var results []ProcessHealth
	for sessionID, procs := range pt.processes {
		for _, tp := range procs {
			idle := now.Sub(tp.lastActivity)
			results = append(results, ProcessHealth{
				SessionID:    sessionID,
				PID:          tp.process.Pid,
				Uptime:       now.Sub(tp.startedAt).Round(time.Second),
				IdleDuration: idle.Round(time.Second),
				IsStale:      idle > staleThreshold,
			})
		}
	}
	return results
}

// KillStale kills processes that have been idle longer than staleThreshold.
// Returns the number of processes killed.
func (pt *ProcessTracker) KillStale(staleThreshold time.Duration) int {
	pt.mu.Lock()
	now := time.Now()
	type staleEntry struct {
		sessionID string
		tp        *trackedProcess
	}
	var stale []staleEntry
	for sessionID, procs := range pt.processes {
		for _, tp := range procs {
			if now.Sub(tp.lastActivity) > staleThreshold {
				stale = append(stale, staleEntry{sessionID, tp})
			}
		}
	}
	pt.mu.Unlock()

	killed := 0
	for _, entry := range stale {
		if err := entry.tp.process.Kill(); err != nil {
			if !isProcessDone(err) {
				log.Printf("proctrack: failed to kill stale pid %d for session %s: %v",
					entry.tp.process.Pid, entry.sessionID, err)
			}
		} else {
			killed++
			log.Printf("proctrack: killed stale pid %d for session %s (idle %s)",
				entry.tp.process.Pid, entry.sessionID,
				now.Sub(entry.tp.lastActivity).Round(time.Second))
		}
		// Remove from tracking.
		pt.Untrack(entry.sessionID, entry.tp.process)
	}
	return killed
}

// isProcessDone returns true if the error indicates the process has already exited.
func isProcessDone(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return msg == "os: process already finished" ||
		msg == "os: process already released" ||
		msg == "wait: no child processes"
}
