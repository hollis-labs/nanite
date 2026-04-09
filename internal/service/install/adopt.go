// internal/service/install/adopt.go
package install

import (
	"path/filepath"

	"github.com/hollis-labs/nanite/internal/assets"
)

// adoptExisting fills in missing pieces when a project already has a
// .nanite/ directory (e.g., from a manual rename or a prior partial
// install). Unlike freshScaffold or migrateFromAgentrc, adopt does not
// archive anything — it assumes the .nanite/ content is the user's
// source of truth and only adds what's missing:
//   - symlinks into globalHome for roles/skills/commands (if missing)
//   - NANITE.md at project root (if missing)
//   - CLAUDE.md managed section (created or updated, existing content
//     outside the section preserved)
//
// The underlying Scaffold* helpers are already idempotent: they skip
// existing files and symlinks, so re-running adopt on a completed project
// is a no-op.
func (s *Service) adoptExisting(projectDir, globalHome string) (*InstallProjectReport, error) {
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

	// UpdateCLAUDEmd creates or updates the managed section.
	managed := buildManagedSection(src)
	claudeReport, err := UpdateCLAUDEmd(
		filepath.Join(projectDir, "CLAUDE.md"),
		managed,
		nil, // no snapshot dir for adopt (no archive involved)
	)
	if err != nil {
		return nil, err
	}

	return &InstallProjectReport{
		Adopted:            true,
		CLAUDEUpdateReport: claudeReport,
	}, nil
}
