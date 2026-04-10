// This file implements Service.Resume and Service.Restart, which handle
// partial installs (interrupted migrations) by either continuing forward
// from wherever the state marker left off, or reversing the archive and
// starting fresh.
package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/hollis-labs/nanite/internal/assets"
)

// ResumeOptions controls Resume.
type ResumeOptions struct {
	ProjectDir  string
	ArchivePath string
	GlobalHome  string
}

// Resume continues a partial migration from wherever its state marker
// left off. Reads the marker, then runs each incomplete phase in sequence,
// updating the marker after each phase succeeds.
//
// If the marker is already at PhaseComplete, returns nil without doing
// any work.
func (s *Service) Resume(opts ResumeOptions) error {
	statePath := filepath.Join(opts.ArchivePath, StateFileName)
	state, err := ReadState(statePath)
	if err != nil {
		return fmt.Errorf("read state: %w", err)
	}
	if state.Phase == PhaseComplete {
		return nil
	}

	src := ScaffoldSource{
		FrameworkVersion: assets.Version(),
		ProjectName:      filepath.Base(opts.ProjectDir),
	}

	if !hasPhase(state, PhaseScaffoldNaniteDir) {
		if err := carryOverFromArchive(opts.ArchivePath, opts.ProjectDir); err != nil {
			return fmt.Errorf("carry over from archive: %w", err)
		}
		if err := ScaffoldNaniteDir(opts.ProjectDir, opts.GlobalHome, src); err != nil {
			return fmt.Errorf("scaffold .nanite dir: %w", err)
		}
		state.MarkPhaseComplete(PhaseScaffoldNaniteDir)
		if err := WriteState(statePath, state); err != nil {
			return fmt.Errorf("write state after scaffold-nanite-dir: %w", err)
		}
	}

	if !hasPhase(state, PhaseScaffoldNaniteMD) {
		if err := ScaffoldNaniteMD(opts.ProjectDir, src); err != nil {
			return fmt.Errorf("scaffold NANITE.md: %w", err)
		}
		state.MarkPhaseComplete(PhaseScaffoldNaniteMD)
		if err := WriteState(statePath, state); err != nil {
			return fmt.Errorf("write state after scaffold-nanite-md: %w", err)
		}
	}

	if !hasPhase(state, PhaseClaudeSync) || !hasPhase(state, PhaseAdapterSync) {
		// Replaces the legacy buildManagedSection + UpdateCLAUDEmd path.
		// Load whatever adapter list was persisted (or empty if the interrupt
		// happened before persistAdapterList ran), then re-persist and sync.
		cfgPath := filepath.Join(opts.ProjectDir, ".nanite", "config.yaml")
		cfg, err := loadProjectConfig(cfgPath)
		if err != nil {
			return fmt.Errorf("load project config: %w", err)
		}
		resolved, _, err := ResolveAdapters(cfg, opts.ProjectDir, ResolveOpts{
			Interactive: false,
		})
		if err != nil {
			return fmt.Errorf("resolve adapters: %w", err)
		}
		if err := persistAdapterList(cfgPath, resolved); err != nil {
			return fmt.Errorf("persist adapter list: %w", err)
		}
		state.MarkPhaseComplete(PhaseClaudeSync) // legacy phase name retained
		if err := WriteState(statePath, state); err != nil {
			return fmt.Errorf("write state after claude-sync: %w", err)
		}
		if err := syncAdaptersForProject(opts.ProjectDir, resolved); err != nil {
			return fmt.Errorf("adapter sync: %w", err)
		}
	}

	state.MarkPhaseComplete(PhaseAdapterSync)
	state.MarkPhaseComplete(PhaseComplete)
	if err := WriteState(statePath, state); err != nil {
		return fmt.Errorf("write final state: %w", err)
	}
	return nil
}

// RestartOptions controls Restart.
type RestartOptions struct {
	ProjectDir  string
	ArchivePath string
	GlobalHome  string
}

// Restart reverses a partial migration (moves .agentrc/ and
// .agentrc-legacy/ back from the archive, removes any partial .nanite/
// and NANITE.md, wipes the archive dir) and then runs a fresh
// migrate-from-agentrc via InstallProject.
func (s *Service) Restart(opts RestartOptions) error {
	// Move .agentrc/ and .agentrc-legacy/ back from the archive.
	for _, name := range []string{".agentrc", ".agentrc-legacy"} {
		src := filepath.Join(opts.ArchivePath, name)
		dst := filepath.Join(opts.ProjectDir, name)
		if _, err := os.Stat(src); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return fmt.Errorf("stat %s: %w", src, err)
		}
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("restore %s: %w", name, err)
		}
	}

	// Remove any partial .nanite/ and NANITE.md.
	if err := os.RemoveAll(filepath.Join(opts.ProjectDir, ".nanite")); err != nil {
		return fmt.Errorf("remove .nanite: %w", err)
	}
	if err := os.Remove(filepath.Join(opts.ProjectDir, "NANITE.md")); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove NANITE.md: %w", err)
	}

	// Wipe the archive dir.
	if err := os.RemoveAll(opts.ArchivePath); err != nil {
		return fmt.Errorf("remove archive dir: %w", err)
	}

	// Run a fresh migration.
	_, err := s.InstallProject(InstallProjectOptions{
		ProjectDir:         opts.ProjectDir,
		GlobalHome:         opts.GlobalHome,
		MigrateFromAgentrc: true,
	})
	return err
}

// hasPhase returns true if p is in s.CompletedPhases.
func hasPhase(s *State, p Phase) bool {
	for _, c := range s.CompletedPhases {
		if c == p {
			return true
		}
	}
	return false
}
