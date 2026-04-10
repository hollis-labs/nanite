// This file defines the public Service type and its InstallHome entry point,
// which extracts the embedded framework assets into the user's ~/.nanite
// directory. See scaffold.go for the package-level doc comment.
package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hollis-labs/nanite/internal/assets"
)

// ErrPartialInstall is returned when InstallProject detects a partial
// install (a matching archive dir with a non-complete state marker) and
// neither --resume nor --restart was requested.
var ErrPartialInstall = errors.New("partial install detected")

// Service is the install package's public entry point. All CLI and MCP
// surfaces call into a Service instance.
type Service struct{}

// New constructs a default Service.
func New() *Service {
	return &Service{}
}

// InstallHomeOptions controls InstallHome.
type InstallHomeOptions struct {
	// Target is the directory to extract to. Defaults to ~/.nanite if empty.
	Target string
	// Force overwrites user-modified files during extract.
	Force bool
}

// InstallHome extracts the embedded framework assets into the target
// directory (typically ~/.nanite). Skips user-modified files unless
// Force is set. Returns the extract report from assets.ExtractTo.
func (s *Service) InstallHome(opts InstallHomeOptions) (*assets.ExtractReport, error) {
	target := opts.Target
	if target == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve home dir: %w", err)
		}
		target = filepath.Join(home, ".nanite")
	}
	return assets.ExtractTo(target, assets.ExtractOptions{Force: opts.Force})
}

// InstallProjectOptions controls InstallProject.
type InstallProjectOptions struct {
	ProjectDir         string
	GlobalHome         string // defaults to ~/.nanite
	MigrateFromAgentrc bool
	ArchiveOnly        bool
}

// InstallProjectReport summarizes what InstallProject did.
type InstallProjectReport struct {
	FreshScaffold      bool
	Migrated           bool
	Adopted            bool
	ArchiveOnly        bool
	ArchivePath        string
	CLAUDEUpdateReport *CLAUDEUpdateReport
	Warnings           []string
}

// InstallProject scaffolds .nanite/, NANITE.md, and CLAUDE.md managed sections
// in projectDir. Dispatches to a branch based on detected state.
func (s *Service) InstallProject(opts InstallProjectOptions) (*InstallProjectReport, error) {
	if opts.ProjectDir == "" {
		return nil, errors.New("empty project dir")
	}
	projectDir, err := filepath.Abs(opts.ProjectDir)
	if err != nil {
		return nil, fmt.Errorf("resolve project dir: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(projectDir); err == nil {
		projectDir = resolved
	}

	globalHome := opts.GlobalHome
	if globalHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve home dir: %w", err)
		}
		globalHome = filepath.Join(home, ".nanite")
	}

	hasAgentrc := dirExists(filepath.Join(projectDir, ".agentrc"))
	hasNaniteDir := dirExists(filepath.Join(projectDir, ".nanite"))

	if hasAgentrc && hasNaniteDir {
		return nil, fmt.Errorf(".agentrc/ and .nanite/ both present in %s — please resolve manually", projectDir)
	}

	if opts.ArchiveOnly {
		if !hasAgentrc {
			return nil, fmt.Errorf("--archive-only requires .agentrc/ in %s", projectDir)
		}
		return s.archiveOnly(projectDir)
	}

	if opts.MigrateFromAgentrc {
		if !hasAgentrc {
			return nil, fmt.Errorf("--migrate-from-agentrc requires .agentrc/ in %s", projectDir)
		}
		return s.migrateFromAgentrc(projectDir, globalHome)
	}

	if hasAgentrc {
		return nil, fmt.Errorf(".agentrc/ present in %s — pass --migrate-from-agentrc to archive it, or --archive-only to archive without scaffolding", projectDir)
	}

	if hasNaniteDir {
		return s.adoptExisting(projectDir, globalHome)
	}

	// Partial install detection: if a matching archive dir exists with a
	// non-complete state marker, refuse to proceed and tell the caller to
	// use --resume or --restart.
	if !hasAgentrc && !hasNaniteDir {
		base, err := ExpandArchiveBase(archiveBaseOverride())
		if err == nil {
			if latest, _ := findLatestArchive(base, filepath.Base(projectDir)); latest != "" {
				if st, err := ReadState(filepath.Join(latest, StateFileName)); err == nil && st.Phase != PhaseComplete {
					return nil, fmt.Errorf("%w: partial install from %s at phase %q; rerun with --resume or --restart",
						ErrPartialInstall,
						st.StartedAt.Format("2006-01-02 15:04:05"),
						st.Phase,
					)
				}
			}
		}
	}

	return s.freshScaffold(projectDir, globalHome)
}

func (s *Service) freshScaffold(projectDir, globalHome string) (*InstallProjectReport, error) {
	src := ScaffoldSource{
		FrameworkVersion: assets.Version(),
		ProjectName:      filepath.Base(projectDir),
	}
	if err := ScaffoldNaniteDir(projectDir, globalHome, src); err != nil {
		return nil, err
	}
	if err := ScaffoldNaniteMD(projectDir, src); err != nil {
		return nil, err
	}
	managed := buildManagedSection(src)
	report, err := UpdateCLAUDEmd(filepath.Join(projectDir, "CLAUDE.md"), managed, nil)
	if err != nil {
		return nil, err
	}
	// Sync managed sections in all CLI target files via the built-in
	// adapter registry. For fresh scaffold the agents list is typically
	// empty, in which case the adapters short-circuit and only CLAUDE.md
	// (already written above) ends up with content.
	if err := syncAdaptersForProject(projectDir); err != nil {
		return nil, fmt.Errorf("adapter sync: %w", err)
	}
	return &InstallProjectReport{
		FreshScaffold:      true,
		CLAUDEUpdateReport: report,
	}, nil
}

func (s *Service) archiveOnly(projectDir string) (*InstallProjectReport, error) {
	basename := filepath.Base(projectDir)
	base, err := ExpandArchiveBase(archiveBaseOverride())
	if err != nil {
		return nil, err
	}
	archiveDir, err := ArchiveProjectAgentrc(projectDir, base, basename, time.Now())
	if err != nil {
		return nil, err
	}
	// Write a state marker so rollback/resume can find this project.
	state := NewState(basename, projectDir, archiveDir, assets.Version())
	state.MarkPhaseComplete(PhaseArchived)
	state.MarkPhaseComplete(PhaseComplete)
	if err := WriteState(filepath.Join(archiveDir, StateFileName), state); err != nil {
		return nil, fmt.Errorf("write state marker: %w", err)
	}
	return &InstallProjectReport{
		ArchiveOnly: true,
		ArchivePath: archiveDir,
	}, nil
}

// buildManagedSection returns the content that goes inside the
// <!-- nanite:start --> / <!-- nanite:end --> block in CLAUDE.md. The
// surrounding markers are added by agent.WriteManagedSection.
func buildManagedSection(src ScaffoldSource) string {
	_ = src // reserved for future templating
	return `## Nanite

Agent configuration for this project is managed by Nanite.

- Boot prompt: ` + "`NANITE.md`" + ` at the project root
- Agent config: ` + "`.nanite/config.yaml`" + `
- Per-agent context: ` + "`.nanite/agents/*.md`" + `

When the user says "Boot <agent>", look up the agent in .nanite/config.yaml
under ` + "`agents:`" + `, load each role file from ` + "`~/.nanite/roles/`" + `, load the listed
skills from ` + "`~/.nanite/skills/`" + `, and read the project context file from
.nanite/.

After context compaction, re-read NANITE.md and the active role/context files.
`
}

// dirExists reports whether path exists and is a directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
