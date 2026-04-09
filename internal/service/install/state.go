package install

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Phase names track progress through an install.
type Phase string

const (
	PhaseStarting          Phase = "starting"
	PhaseArchived          Phase = "archived"
	PhaseGlobalExtract     Phase = "global-extract-complete"
	PhaseScaffoldNaniteDir Phase = "scaffold-nanite-dir"
	PhaseScaffoldNaniteMD  Phase = "scaffold-nanite-md"
	PhaseClaudeSync        Phase = "claude-sync"
	PhaseAdapterSync       Phase = "adapter-sync"
	PhaseComplete          Phase = "complete"
)

// StateFileName is the name of the state marker file inside the archive dir.
const StateFileName = ".install-state.json"

// State is the persisted progress marker for a (possibly-multi-step) install.
type State struct {
	OriginalProjectBasename string    `json:"original_project_basename"`
	OriginalProjectPath     string    `json:"original_project_path"`
	StartedAt               time.Time `json:"started_at"`
	Phase                   Phase     `json:"phase"`
	CompletedPhases         []Phase   `json:"completed_phases"`
	ArchivePath             string    `json:"archive_path"`
	RollbackSnapshotPath    string    `json:"rollback_snapshot_path,omitempty"`
	NaniteVersion           string    `json:"nanite_version"`
}

// NewState constructs a fresh state starting at PhaseStarting.
func NewState(basename, projectPath, archivePath, naniteVersion string) *State {
	return &State{
		OriginalProjectBasename: basename,
		OriginalProjectPath:     projectPath,
		StartedAt:               time.Now().UTC(),
		Phase:                   PhaseStarting,
		CompletedPhases:         []Phase{},
		ArchivePath:             archivePath,
		RollbackSnapshotPath:    archivePath + "/rollback",
		NaniteVersion:           naniteVersion,
	}
}

// MarkPhaseComplete updates Phase and appends to CompletedPhases (idempotent).
func (s *State) MarkPhaseComplete(p Phase) {
	s.Phase = p
	for _, existing := range s.CompletedPhases {
		if existing == p {
			return
		}
	}
	s.CompletedPhases = append(s.CompletedPhases, p)
}

// WriteState serializes a State to the given path.
func WriteState(path string, s *State) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// ReadState reads and deserializes a State from the given path.
// Returns a *os.PathError wrapping fs.ErrNotExist if the file is missing.
func ReadState(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("unmarshal state: %w", err)
	}
	return &s, nil
}
