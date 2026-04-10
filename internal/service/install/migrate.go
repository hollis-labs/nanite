// This file implements the migrate-from-agentrc branch of InstallProject.
// It archives the legacy .agentrc/ tree, carries over project-specific
// content into a freshly scaffolded .nanite/, and updates CLAUDE.md.
package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/internal/assets"
)

// migrateFromAgentrc is the migrate-from-agentrc branch of InstallProject.
// It archives the legacy .agentrc/ directory, carries over project-specific
// content into a freshly scaffolded .nanite/, and rewrites CLAUDE.md.
func (s *Service) migrateFromAgentrc(projectDir, globalHome string) (*InstallProjectReport, error) {
	basename := filepath.Base(projectDir)
	archiveBase, err := ExpandArchiveBase(archiveBaseOverride())
	if err != nil {
		return nil, err
	}

	ts := time.Now()
	archiveDir, err := ResolveArchiveDir(archiveBase, basename, ts)
	if err != nil {
		return nil, fmt.Errorf("resolve archive dir: %w", err)
	}
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir archive: %w", err)
	}

	state := NewState(basename, projectDir, archiveDir, assets.Version())
	statePath := filepath.Join(archiveDir, StateFileName)
	if err := WriteState(statePath, state); err != nil {
		return nil, fmt.Errorf("write initial state: %w", err)
	}

	// Snapshot all adapter target files (CLAUDE.md, AGENTS.md, GEMINI.md,
	// OPENCODE.md) so Rollback can restore them. Files that don't exist are
	// skipped — Rollback knows that means the installer created them.
	if err := snapshotAdapterTargets(projectDir, archiveDir); err != nil {
		return nil, fmt.Errorf("snapshot adapter targets: %w", err)
	}

	// Move .agentrc/ and .agentrc-legacy/ into our already-created archive
	// dir. We can't call ArchiveProjectAgentrc here because it would resolve
	// its own archive dir and the two would diverge.
	if err := moveAgentrcInto(projectDir, archiveDir); err != nil {
		return nil, err
	}
	state.MarkPhaseComplete(PhaseArchived)
	if err := WriteState(statePath, state); err != nil {
		return nil, fmt.Errorf("write state after archive: %w", err)
	}

	// Carry over project-specific content.
	if err := carryOverFromArchive(archiveDir, projectDir); err != nil {
		return nil, err
	}

	// Scaffold .nanite/ (config + symlinks + agents dir). This is idempotent
	// and preserves any files already carried over by carryOverFromArchive.
	src := ScaffoldSource{
		FrameworkVersion: assets.Version(),
		ProjectName:      basename,
	}
	if err := ScaffoldNaniteDir(projectDir, globalHome, src); err != nil {
		return nil, err
	}
	state.MarkPhaseComplete(PhaseScaffoldNaniteDir)
	if err := WriteState(statePath, state); err != nil {
		return nil, fmt.Errorf("write state after scaffold-nanite-dir: %w", err)
	}

	// Scaffold NANITE.md.
	if err := ScaffoldNaniteMD(projectDir, src); err != nil {
		return nil, err
	}
	state.MarkPhaseComplete(PhaseScaffoldNaniteMD)
	if err := WriteState(statePath, state); err != nil {
		return nil, fmt.Errorf("write state after scaffold-nanite-md: %w", err)
	}

	// CLAUDE.md surgery.
	managed := buildManagedSection(src)
	claudeReport, err := UpdateCLAUDEmd(
		filepath.Join(projectDir, "CLAUDE.md"),
		managed,
		&CLAUDESnapshotOpts{Dir: archiveDir},
	)
	if err != nil {
		return nil, err
	}
	state.MarkPhaseComplete(PhaseClaudeSync)
	if err := WriteState(statePath, state); err != nil {
		return nil, fmt.Errorf("write state after claude-sync: %w", err)
	}

	// Sync managed sections in all CLI target files via the built-in
	// adapter registry. This writes/updates managed sections in CLAUDE.md
	// (overwriting the install service's baseline content from above with
	// the claude adapter's agent listing if any agents are defined),
	// AGENTS.md, GEMINI.md, and OPENCODE.md.
	if err := syncAdaptersForProject(projectDir); err != nil {
		return nil, fmt.Errorf("adapter sync: %w", err)
	}
	state.MarkPhaseComplete(PhaseAdapterSync)
	state.MarkPhaseComplete(PhaseComplete)
	if err := WriteState(statePath, state); err != nil {
		return nil, fmt.Errorf("write final state: %w", err)
	}

	return &InstallProjectReport{
		Migrated:           true,
		ArchivePath:        archiveDir,
		CLAUDEUpdateReport: claudeReport,
	}, nil
}

// moveAgentrcInto moves projectDir/.agentrc and .agentrc-legacy into
// archiveDir without reinvoking ArchiveProjectAgentrc's dir resolution logic.
func moveAgentrcInto(projectDir, archiveDir string) error {
	if err := moveIfExists(
		filepath.Join(projectDir, ".agentrc"),
		filepath.Join(archiveDir, ".agentrc"),
	); err != nil {
		return fmt.Errorf("move .agentrc: %w", err)
	}
	if err := moveIfExists(
		filepath.Join(projectDir, ".agentrc-legacy"),
		filepath.Join(archiveDir, ".agentrc-legacy"),
	); err != nil {
		return fmt.Errorf("move .agentrc-legacy: %w", err)
	}
	return nil
}

// archiveBaseOverride returns the archive base directory, honoring the
// NANITE_ARCHIVE_BASE env var override used in tests.
func archiveBaseOverride() string {
	if v := os.Getenv("NANITE_ARCHIVE_BASE"); v != "" {
		return v
	}
	return ArchiveBase
}

// carryOverFromArchive copies project-specific content from the archived
// .agentrc/ into the fresh .nanite/ directory:
//   - agents/* (recursive)
//   - boot-prompt.md (if present)
//   - config.yaml (with agentrc_version → nanite_version rename)
func carryOverFromArchive(archiveDir, projectDir string) error {
	srcAgentrc := filepath.Join(archiveDir, ".agentrc")
	dstNanite := filepath.Join(projectDir, ".nanite")
	if err := os.MkdirAll(dstNanite, 0o755); err != nil {
		return fmt.Errorf("mkdir .nanite: %w", err)
	}

	// agents/
	srcAgents := filepath.Join(srcAgentrc, "agents")
	if _, err := os.Stat(srcAgents); err == nil {
		if err := copyDir(srcAgents, filepath.Join(dstNanite, "agents")); err != nil {
			return fmt.Errorf("copy agents: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat agents: %w", err)
	}

	// boot-prompt.md
	if data, err := os.ReadFile(filepath.Join(srcAgentrc, "boot-prompt.md")); err == nil {
		if err := os.WriteFile(filepath.Join(dstNanite, "boot-prompt.md"), data, 0o644); err != nil {
			return fmt.Errorf("copy boot-prompt: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read boot-prompt: %w", err)
	}

	// config.yaml (with field rename)
	if data, err := os.ReadFile(filepath.Join(srcAgentrc, "config.yaml")); err == nil {
		renamed := renameConfigFields(data)
		if err := os.WriteFile(filepath.Join(dstNanite, "config.yaml"), renamed, 0o644); err != nil {
			return fmt.Errorf("copy config: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read config: %w", err)
	}

	return nil
}

// copyDir recursively copies src to dst, preserving file modes.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(target), err)
		}
		if err := os.WriteFile(target, data, info.Mode()); err != nil {
			return fmt.Errorf("write %s: %w", target, err)
		}
		return nil
	})
}

// renameConfigFields applies the agentrc → nanite field renames to the
// config.yaml byte slice. Line-based replace preserves comments and
// formatting; suitable for the one field we care about right now. YAML
// round-trip is deferred until we need to rename nested fields.
func renameConfigFields(data []byte) []byte {
	return []byte(strings.ReplaceAll(string(data), "agentrc_version:", "nanite_version:"))
}
