// This file defines the public Service type and its InstallHome entry point,
// which extracts the embedded framework assets into the user's ~/.nanite
// directory. See scaffold.go for the package-level doc comment.
package install

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/hollis-labs/nanite/internal/assets"
)

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
// directory (typically ~/.nanite).
//
// BLG-20260412-002 staging flow:
//
//  1. If the target does not exist yet, extract directly into it — there is
//     nothing to protect and no atomicity concern.
//  2. If the target exists, extract into a sibling staging dir
//     (<target>.staging.<pid>-<nanos>) so a crash mid-extract cannot leave
//     the live install in a mixed-version state.
//  3. ExtractTo preserves user-modified files by default (it skips when
//     bytes differ). For the staging path we pre-seed the staging dir with
//     a copy of the current install so those "skip" decisions can still be
//     observed, then ExtractTo runs against the staging copy.
//  4. On success, os.Rename(target -> target.bak.<ts>) then
//     os.Rename(staging -> target). Both renames are single-directory
//     operations so they are atomic on POSIX.
//  5. On any failure the staging dir is removed (best effort) and the
//     live install is left untouched. If the pre-rename backup succeeds
//     but the staging rename fails, the backup is preserved for manual
//     recovery and a wrapped error is returned.
//
// Skips user-modified files unless Force is set. Returns the extract report
// from assets.ExtractTo.
func (s *Service) InstallHome(opts InstallHomeOptions) (*assets.ExtractReport, error) {
	if err := Preflight(); err != nil {
		return nil, err
	}
	target := opts.Target
	if target == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve home dir: %w", err)
		}
		target = filepath.Join(home, ".nanite")
	}

	// Fast path: no existing install → extract directly.
	if _, err := os.Stat(target); os.IsNotExist(err) {
		report, err := assets.ExtractTo(target, assets.ExtractOptions{Force: opts.Force})
		if err != nil {
			return nil, err
		}
		if _, err := EnsureRuntimeDirs(target); err != nil {
			return nil, fmt.Errorf("ensure runtime dirs: %w", err)
		}
		return report, nil
	} else if err != nil {
		return nil, fmt.Errorf("stat target %s: %w", target, err)
	}

	// Staging path: extract into a sibling dir, then atomic-rename.
	parent := filepath.Dir(target)
	basename := filepath.Base(target)
	ts := time.Now().UTC()
	staging := filepath.Join(parent,
		fmt.Sprintf("%s.staging.%d-%d", basename, os.Getpid(), ts.UnixNano()))

	// Seed staging with a snapshot of the current install so ExtractTo's
	// "skip user-modified files" semantics apply to the live set. copyTree
	// is best-effort on mode preservation and returns early on errors.
	if err := copyTree(target, staging); err != nil {
		_ = os.RemoveAll(staging)
		return nil, fmt.Errorf("seed staging dir: %w", err)
	}

	report, err := assets.ExtractTo(staging, assets.ExtractOptions{Force: opts.Force})
	if err != nil {
		_ = os.RemoveAll(staging)
		return nil, fmt.Errorf("extract into staging: %w", err)
	}

	// Swap: rename live → backup, then staging → live.
	backup := filepath.Join(parent,
		fmt.Sprintf("%s.bak.%s", basename, ts.Format("20060102-150405")+"."+strconv.Itoa(os.Getpid())))
	if err := os.Rename(target, backup); err != nil {
		_ = os.RemoveAll(staging)
		return nil, fmt.Errorf("move existing install aside (%s -> %s): %w", target, backup, err)
	}
	if err := os.Rename(staging, target); err != nil {
		// Best effort: try to put the original back. If that fails too,
		// surface both paths so the operator can recover by hand.
		if rbErr := os.Rename(backup, target); rbErr != nil {
			return nil, fmt.Errorf(
				"promote staging failed (%w); rollback of backup also failed (%v); "+
					"manual recovery: backup=%s staging=%s",
				err, rbErr, backup, staging,
			)
		}
		_ = os.RemoveAll(staging)
		return nil, fmt.Errorf("promote staging %s -> %s: %w", staging, target, err)
	}

	// Swap succeeded — remove the backup. Keep it around if removal fails;
	// it's recoverable state rather than a correctness issue.
	_ = os.RemoveAll(backup)

	// Provision runtime-state dirs and their READMEs in the live install.
	// This is best-effort after a successful swap; failures here should not
	// roll back the install but are still surfaced as errors.
	if _, err := EnsureRuntimeDirs(target); err != nil {
		return nil, fmt.Errorf("ensure runtime dirs: %w", err)
	}
	return report, nil
}

// copyTree recursively copies src to dst, creating dst if needed. Files
// are copied with their existing mode; directories are created with 0o755.
// Symlinks are recreated as symlinks (not followed) so the staging dir
// mirrors the live tree's link structure.
func copyTree(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("mkdir dst: %w", err)
	}
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)

		// Symlinks: recreate rather than follow.
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return fmt.Errorf("readlink %s: %w", path, err)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("mkdir parent of %s: %w", target, err)
			}
			if err := os.Symlink(link, target); err != nil {
				return fmt.Errorf("symlink %s -> %s: %w", target, link, err)
			}
			return nil
		}

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
		if err := os.WriteFile(target, data, info.Mode()); err != nil { //nolint:forbidigo // staging seed copy, atomic rename applied at tree level
			return fmt.Errorf("write %s: %w", target, err)
		}
		return nil
	})
}

// InstallProjectOptions controls InstallProject.
type InstallProjectOptions struct {
	ProjectDir string
	GlobalHome string // defaults to ~/.nanite

	// Adapter selection options (new in 2026-04-09 design).
	Adapters    string // value of --adapters flag, comma-separated, "" = unset
	NoAdapters  bool   // --no-adapters flag
	Reconfigure bool   // --reconfigure flag

	// Interactive controls whether to prompt the user for adapter
	// selection. Set by the CLI based on isStdinTTY().
	Interactive bool
	Stdin       io.Reader // injection for prompts (defaults to os.Stdin)
	Stdout      io.Writer // injection for prompts (defaults to os.Stdout)
}

// normalized returns a copy of opts with Stdin/Stdout defaulted to
// os.Stdin/os.Stdout when nil. Prevents bufio.NewReader(nil) panics
// for library consumers that set Interactive=true but leave the IO
// streams unset.
func (opts InstallProjectOptions) normalized() InstallProjectOptions {
	if opts.Stdin == nil {
		opts.Stdin = os.Stdin
	}
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	return opts
}

// InstallProjectReport summarizes what InstallProject did.
type InstallProjectReport struct {
	FreshScaffold bool
	Adopted       bool
	ArchivePath   string
	Warnings []string

	// Adapter selection results (new in 2026-04-09 design).
	Adapters        []string        // resolved adapter list (may be empty)
	AdapterCleanups []CleanupReport // sections that were stripped/deleted
}

// InstallProject scaffolds .nanite/, NANITE.md, and CLAUDE.md managed sections
// in projectDir. Dispatches to a branch based on detected state.
func (s *Service) InstallProject(opts InstallProjectOptions) (*InstallProjectReport, error) {
	if err := Preflight(); err != nil {
		return nil, err
	}
	opts = opts.normalized()
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

	if hasAgentrc {
		return nil, fmt.Errorf(".agentrc/ present in %s — legacy agentrc projects are no longer handled by nanite install", projectDir)
	}

	if hasNaniteDir {
		return s.adoptExisting(projectDir, globalHome, opts)
	}

	return s.freshScaffold(projectDir, globalHome, opts)
}

func (s *Service) freshScaffold(projectDir, globalHome string, opts InstallProjectOptions) (*InstallProjectReport, error) {
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

	if err := syncAdaptersForProject(projectDir, resolved); err != nil {
		return nil, fmt.Errorf("adapter sync: %w", err)
	}

	return &InstallProjectReport{
		FreshScaffold:   true,
		Adapters:        resolved,
		AdapterCleanups: cleanupReports,
	}, nil
}

// setDifference returns elements present in `a` but not in `b`.
func setDifference(a, b []string) []string {
	bSet := make(map[string]bool, len(b))
	for _, x := range b {
		bSet[x] = true
	}
	var out []string
	for _, x := range a {
		if !bSet[x] {
			out = append(out, x)
		}
	}
	return out
}

// dirExists reports whether path exists and is a directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
