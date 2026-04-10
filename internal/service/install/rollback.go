// This file implements Service.Rollback, which reverses a previous
// migration by restoring CLAUDE.md from the pre-edit snapshot, removing
// .nanite/ and NANITE.md, and moving the archived .agentrc/ content back
// into the project directory.
package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RollbackOptions controls Rollback.
type RollbackOptions struct {
	// ProjectDir is the project to roll back. Resolved to absolute path.
	ProjectDir string
	// ArchivePath is the explicit archive directory to restore from. If
	// empty, Rollback finds the most recent matching archive under
	// ~/Projects-apps/.archived (or NANITE_ARCHIVE_BASE).
	ArchivePath string
}

// Rollback reverses a migration by restoring CLAUDE.md from the pre-edit
// snapshot, removing .nanite/ and NANITE.md, and moving the archived
// .agentrc/ and .agentrc-legacy/ directories back into the project.
//
// Rollback refuses to run if the archive directory is missing a state
// marker — that's the evidence that a prior install recorded this dir as
// its archive location. Without it, we don't know the archive contents
// are ours to move.
func (s *Service) Rollback(opts RollbackOptions) error {
	if opts.ProjectDir == "" {
		return errors.New("empty project dir")
	}
	projectDir, err := filepath.Abs(opts.ProjectDir)
	if err != nil {
		return fmt.Errorf("resolve project dir: %w", err)
	}
	basename := filepath.Base(projectDir)

	archiveDir := opts.ArchivePath
	if archiveDir == "" {
		base, err := ExpandArchiveBase(archiveBaseOverride())
		if err != nil {
			return err
		}
		latest, err := findLatestArchive(base, basename)
		if err != nil {
			return fmt.Errorf("find latest archive: %w", err)
		}
		if latest == "" {
			return fmt.Errorf("no archive found for %s under %s", basename, base)
		}
		archiveDir = latest
	}

	// Sanity: state marker must exist.
	statePath := filepath.Join(archiveDir, StateFileName)
	if _, err := os.Stat(statePath); err != nil {
		return fmt.Errorf("state marker missing at %s — refusing to roll back without evidence of a prior install", statePath)
	}

	// Restore each CLI target file (CLAUDE.md, AGENTS.md, GEMINI.md,
	// OPENCODE.md) from its pre-edit snapshot. If the snapshot is
	// missing, the installer created the file from scratch — remove
	// it instead of restoring.
	for _, name := range adapterTargetFiles {
		preEdit := filepath.Join(archiveDir, name+".pre-edit")
		dst := filepath.Join(projectDir, name)
		if data, err := os.ReadFile(preEdit); err == nil {
			if err := os.WriteFile(dst, data, 0o644); err != nil {
				return fmt.Errorf("restore %s: %w", name, err)
			}
		} else if errors.Is(err, fs.ErrNotExist) {
			// No snapshot — file was created by installer. Remove it.
			if err := os.Remove(dst); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("remove installer-created %s: %w", name, err)
			}
		} else {
			return fmt.Errorf("read %s.pre-edit: %w", name, err)
		}
	}

	// Remove .nanite/ and NANITE.md from project.
	if err := os.RemoveAll(filepath.Join(projectDir, ".nanite")); err != nil {
		return fmt.Errorf("remove .nanite: %w", err)
	}
	if err := os.Remove(filepath.Join(projectDir, "NANITE.md")); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove NANITE.md: %w", err)
	}

	// Restore .agentrc/ and .agentrc-legacy/ from archive.
	for _, name := range []string{".agentrc", ".agentrc-legacy"} {
		src := filepath.Join(archiveDir, name)
		dst := filepath.Join(projectDir, name)
		if _, err := os.Stat(src); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return fmt.Errorf("stat %s: %w", src, err)
		}
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("restore %s: %w", name, err)
		}
	}

	return nil
}

// findLatestArchive returns the most recent archive dir matching basename-*
// under base, by mtime. Returns "" (no error) if no matches.
func findLatestArchive(base, basename string) (string, error) {
	entries, err := os.ReadDir(base)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var latest string
	var latestTime time.Time
	prefix := basename + "-"
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(latestTime) {
			latestTime = info.ModTime()
			latest = filepath.Join(base, e.Name())
		}
	}
	return latest, nil
}
