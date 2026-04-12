// internal/service/install/adopt.go
package install

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/hollis-labs/nanite/internal/assets"
)

// adoptExisting fills in missing pieces when a project already has a
// .nanite/ directory (e.g., from a manual rename or a prior partial
// install). Unlike freshScaffold or migrateFromAgentrc, adopt does not
// archive anything — it assumes the .nanite/ content is the user's
// source of truth and only adds what's missing:
//   - symlinks into globalHome for roles/skills/commands (if missing)
//   - NANITE.md at project root (if missing)
//
// The underlying Scaffold* helpers are already idempotent: they skip
// existing files and symlinks, so re-running adopt on a completed project
// is a no-op.
//
// Follows the same Resolve → cleanup → persist → sync pattern as
// freshScaffold, so --reconfigure and adapter removal work correctly.
func (s *Service) adoptExisting(projectDir, globalHome string, opts InstallProjectOptions) (*InstallProjectReport, error) {
	src := ScaffoldSource{
		FrameworkVersion: assets.Version(),
		ProjectName:      filepath.Base(projectDir),
	}

	// ScaffoldNaniteDir is safe to call against an existing .nanite/:
	//   - config.yaml is only written if missing
	//   - agents/ is created via MkdirAll (no-op if present)
	//   - symlinks are only created if the link path doesn't exist
	if err := ScaffoldNaniteDir(projectDir, globalHome, src); err != nil {
		return nil, err
	}

	// ScaffoldNaniteMD preserves existing NANITE.md.
	if err := ScaffoldNaniteMD(projectDir, src); err != nil {
		return nil, err
	}

	cfgPath := filepath.Join(projectDir, ".nanite", "config.yaml")
	cfg, err := loadProjectConfig(cfgPath)
	if err != nil {
		return nil, err
	}

	resolved, previous, err := ResolveAdapters(cfg, projectDir, ResolveOpts{
		Flag:        opts.Adapters,
		NoAdapters:  opts.NoAdapters,
		Reconfigure: opts.Reconfigure,
		Interactive: opts.Interactive,
		Stdin:       opts.Stdin,
		Stdout:      opts.Stdout,
	})
	if err != nil {
		return nil, fmt.Errorf("resolve adapters: %w", err)
	}

	removed := setDifference(previous, resolved)
	cleanupReports, err := cleanupRemovedAdapters(projectDir, removed)
	if err != nil {
		return nil, fmt.Errorf("cleanup removed adapters: %w", err)
	}

	if err := persistAdapterList(cfgPath, resolved); err != nil {
		return nil, fmt.Errorf("persist adapter list: %w", err)
	}

	// BLG-20260412-002: back up any prior managed block before the adapter
	// sync re-renders it. Only files that actually contain a Nanite managed
	// section get snapshotted. If the backup fails, abort before touching
	// the live files — the AtomicWriteFile migration guarantees the
	// re-render itself can't leave a half-written target, but that only
	// matters when we have a known-good prior copy to compare against.
	refreshArchive, err := snapshotAdapterTargetsForRefresh(projectDir, time.Now())
	if err != nil {
		return nil, fmt.Errorf("refresh-snapshot adapter targets: %w", err)
	}

	if err := syncAdaptersForProject(projectDir, resolved); err != nil {
		return nil, fmt.Errorf("adapter sync: %w", err)
	}

	return &InstallProjectReport{
		Adopted:         true,
		Adapters:        resolved,
		AdapterCleanups: cleanupReports,
		ArchivePath:     refreshArchive,
	}, nil
}
