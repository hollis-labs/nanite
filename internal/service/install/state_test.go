package install

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestState_WriteRead(t *testing.T) {
	dir := t.TempDir()
	s := &State{
		OriginalProjectBasename: "hadron",
		OriginalProjectPath:     "/Users/foo/hadron",
		StartedAt:               time.Date(2026, 4, 9, 14, 30, 22, 0, time.UTC),
		Phase:                   PhaseArchived,
		CompletedPhases:         []Phase{PhaseStarting, PhaseArchived},
		ArchivePath:             dir,
		NaniteVersion:           "2.3.0",
	}
	path := filepath.Join(dir, StateFileName)
	if err := WriteState(path, s); err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	got, err := ReadState(path)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if got.Phase != PhaseArchived {
		t.Errorf("Phase = %q, want %q", got.Phase, PhaseArchived)
	}
	if len(got.CompletedPhases) != 2 {
		t.Errorf("CompletedPhases len = %d, want 2", len(got.CompletedPhases))
	}
	if got.OriginalProjectBasename != "hadron" {
		t.Errorf("OriginalProjectBasename = %q, want hadron", got.OriginalProjectBasename)
	}
}

func TestState_ReadMissing(t *testing.T) {
	_, err := ReadState(filepath.Join(t.TempDir(), "nope.json"))
	if !os.IsNotExist(err) {
		t.Errorf("expected os.IsNotExist, got %v", err)
	}
}

func TestState_MarkPhaseComplete(t *testing.T) {
	s := NewState("hadron", "/path/to/hadron", "/archive", "2.3.0")
	s.MarkPhaseComplete(PhaseScaffoldNaniteDir)
	if s.Phase != PhaseScaffoldNaniteDir {
		t.Errorf("Phase = %q, want %q", s.Phase, PhaseScaffoldNaniteDir)
	}
	contains := false
	for _, p := range s.CompletedPhases {
		if p == PhaseScaffoldNaniteDir {
			contains = true
			break
		}
	}
	if !contains {
		t.Error("CompletedPhases missing PhaseScaffoldNaniteDir")
	}

	// Idempotency: calling MarkPhaseComplete a second time with the same phase
	// should not duplicate the entry in CompletedPhases.
	s.MarkPhaseComplete(PhaseScaffoldNaniteDir)
	if len(s.CompletedPhases) != 1 {
		t.Errorf("CompletedPhases len after duplicate call = %d, want 1", len(s.CompletedPhases))
	}

	// A different phase should append.
	s.MarkPhaseComplete(PhaseClaudeSync)
	if len(s.CompletedPhases) != 2 {
		t.Errorf("CompletedPhases len after second distinct phase = %d, want 2", len(s.CompletedPhases))
	}
	if s.Phase != PhaseClaudeSync {
		t.Errorf("Phase = %q, want %q", s.Phase, PhaseClaudeSync)
	}
}
