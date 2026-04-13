// Package installcmd implements the framework-injection flow shared by the
// `nanite-agent init` entry point and the legacy `nanite install` deprecation
// shim. It is a thin wrapper over internal/service/install; no business logic
// lives here beyond flag parsing and output formatting.
package installcmd

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/internal/service/install"
	"github.com/mattn/go-isatty"
)

// Run parses args and executes the framework-injection flow. label is the
// program name used in user-visible output (e.g. "nanite-agent init").
// It does not return on error — it calls os.Exit with an appropriate code.
func Run(label string, args []string) {
	fs := flag.NewFlagSet(label, flag.ExitOnError)
	project := fs.String("project", "", "target project directory (omit to install to ~/.nanite/)")
	migrate := fs.Bool("migrate-from-agentrc", false, "archive existing .agentrc/ and migrate to .nanite/")
	archiveOnly := fs.Bool("archive-only", false, "archive .agentrc/ without scaffolding .nanite/ (for scratch dirs)")
	rollback := fs.Bool("rollback", false, "reverse the most recent migration for --project")
	resume := fs.Bool("resume", false, "continue a partial install from its state marker")
	restart := fs.Bool("restart", false, "reverse a partial install and run a fresh migration")
	refresh := fs.Bool("refresh", false, "re-extract embedded assets to ~/.nanite/, skipping user-modified files")
	force := fs.Bool("force", false, "overwrite user-modified files during --refresh")
	printDiff := fs.Bool("print-diff", false, "dry-run: show what would change (not yet implemented)")
	adapters := fs.String("adapters", "", "comma-separated list of CLI adapters to manage (claude, codex, gemini, opencode)")
	noAdapters := fs.Bool("no-adapters", false, "disable all CLI adapter management for this project")
	reconfigure := fs.Bool("reconfigure", false, "re-prompt for adapter selection even if config has adapters: set")
	fs.Parse(args)

	if *adapters != "" && *noAdapters {
		die(label, "flag conflict", fmt.Errorf("--adapters and --no-adapters are mutually exclusive"))
	}

	svc := install.New()

	// Home install (no --project, or --refresh).
	if *project == "" && !*refresh {
		home, err := os.UserHomeDir()
		if err != nil {
			die(label, "resolve home dir", err)
		}
		target := filepath.Join(home, ".nanite")
		report, err := svc.InstallHome(install.InstallHomeOptions{Target: target, Force: *force})
		if err != nil {
			die(label, "install home", err)
		}
		fmt.Printf("%s: created=%d unchanged=%d skipped=%d forced=%d\n",
			label, report.Created, report.Unchanged, report.Skipped, report.Forced)
		if len(report.SkippedFiles) > 0 {
			fmt.Println("  skipped (user-modified):")
			for _, f := range report.SkippedFiles {
				fmt.Printf("    %s\n", f)
			}
		}
		return
	}

	// --refresh re-extracts to ~/.nanite/ even when --project was passed.
	if *refresh {
		home, err := os.UserHomeDir()
		if err != nil {
			die(label, "resolve home dir", err)
		}
		target := filepath.Join(home, ".nanite")
		report, err := svc.InstallHome(install.InstallHomeOptions{Target: target, Force: *force})
		if err != nil {
			die(label, "refresh home", err)
		}
		fmt.Printf("%s refresh: created=%d unchanged=%d skipped=%d forced=%d\n",
			label, report.Created, report.Unchanged, report.Skipped, report.Forced)
		if len(report.SkippedFiles) > 0 {
			fmt.Println("  skipped (user-modified):")
			for _, f := range report.SkippedFiles {
				fmt.Printf("    %s\n", f)
			}
		}
		return
	}

	projectDir, err := resolveProjectDir(*project)
	if err != nil {
		die(label, "resolve project dir", err)
	}

	if *printDiff {
		fmt.Fprintf(os.Stderr, "%s: --print-diff not yet implemented\n", label)
		os.Exit(2)
	}

	if *rollback {
		if err := svc.Rollback(install.RollbackOptions{ProjectDir: projectDir}); err != nil {
			die(label, "rollback", err)
		}
		fmt.Printf("%s: rolled back %s\n", label, projectDir)
		return
	}

	if *resume {
		latest, err := latestArchiveFor(projectDir)
		if err != nil {
			die(label, "find latest archive", err)
		}
		if err := svc.Resume(install.ResumeOptions{
			ProjectDir:  projectDir,
			ArchivePath: latest,
			GlobalHome:  defaultGlobalHome(),
		}); err != nil {
			die(label, "resume", err)
		}
		fmt.Printf("%s: resumed %s\n", label, projectDir)
		return
	}

	if *restart {
		latest, err := latestArchiveFor(projectDir)
		if err != nil {
			die(label, "find latest archive", err)
		}
		if err := svc.Restart(install.RestartOptions{
			ProjectDir:  projectDir,
			ArchivePath: latest,
			GlobalHome:  defaultGlobalHome(),
		}); err != nil {
			die(label, "restart", err)
		}
		fmt.Printf("%s: restarted migration for %s\n", label, projectDir)
		return
	}

	opts := install.InstallProjectOptions{
		ProjectDir:         projectDir,
		GlobalHome:         defaultGlobalHome(),
		MigrateFromAgentrc: *migrate,
		ArchiveOnly:        *archiveOnly,
		Adapters:           *adapters,
		NoAdapters:         *noAdapters,
		Reconfigure:        *reconfigure,
		Interactive:        isStdinTTY(),
		Stdin:              os.Stdin,
		Stdout:             os.Stdout,
	}
	report, err := svc.InstallProject(opts)
	if err != nil {
		if errors.Is(err, install.ErrPartialInstall) {
			if !isStdinTTY() {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(3)
			}
			handlePartialInteractive(label, svc, projectDir, err)
			return
		}
		die(label, "install project", err)
	}

	switch {
	case report.FreshScaffold:
		fmt.Printf("%s: fresh scaffold in %s\n", label, projectDir)
	case report.Migrated:
		fmt.Printf("%s: migrated %s (archive: %s)\n", label, projectDir, report.ArchivePath)
	case report.Adopted:
		fmt.Printf("%s: adopted existing .nanite/ in %s\n", label, projectDir)
	case report.ArchiveOnly:
		fmt.Printf("%s: archived %s (archive: %s)\n", label, projectDir, report.ArchivePath)
	}

	if len(report.Adapters) > 0 {
		fmt.Printf("%s: adapters [%s] (%d enabled, %d disabled)\n",
			label,
			strings.Join(report.Adapters, ", "),
			len(report.Adapters),
			4-len(report.Adapters),
		)
	} else if !report.ArchiveOnly {
		fmt.Printf("%s: no CLI adapters enabled (run with --reconfigure to add them later)\n", label)
	}
	for _, cleanup := range report.AdapterCleanups {
		switch cleanup.Action {
		case "stripped":
			fmt.Printf("%s: removed managed section from %s (re-run with --reconfigure to add it back)\n",
				label, filepath.Base(cleanup.FilePath))
		case "deleted":
			fmt.Printf("%s: deleted %s (was managed-section-only)\n",
				label, filepath.Base(cleanup.FilePath))
		}
	}
}

func handlePartialInteractive(label string, svc *install.Service, projectDir string, detectedErr error) {
	fmt.Fprintln(os.Stderr, detectedErr)
	fmt.Println("How do you want to proceed?")
	fmt.Println("  [r] Resume from last completed phase")
	fmt.Println("  [s] Start over (restart)")
	fmt.Println("  [c] Cancel")
	fmt.Print("> ")
	var choice string
	fmt.Scanln(&choice)
	latest, err := latestArchiveFor(projectDir)
	if err != nil {
		die(label, "find latest archive", err)
	}
	switch strings.ToLower(choice) {
	case "r":
		if err := svc.Resume(install.ResumeOptions{
			ProjectDir:  projectDir,
			ArchivePath: latest,
			GlobalHome:  defaultGlobalHome(),
		}); err != nil {
			die(label, "resume", err)
		}
		fmt.Printf("%s: resumed %s\n", label, projectDir)
	case "s":
		if err := svc.Restart(install.RestartOptions{
			ProjectDir:  projectDir,
			ArchivePath: latest,
			GlobalHome:  defaultGlobalHome(),
		}); err != nil {
			die(label, "restart", err)
		}
		fmt.Printf("%s: restarted %s\n", label, projectDir)
	default:
		fmt.Println("cancelled")
	}
}

func die(label, action string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %s: %v\n", label, action, err)
	os.Exit(1)
}

func isStdinTTY() bool {
	return isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
}

func resolveProjectDir(project string) (string, error) {
	if project == "." || project == "" {
		return os.Getwd()
	}
	return filepath.Abs(project)
}

func defaultGlobalHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".nanite")
}

func latestArchiveFor(projectDir string) (string, error) {
	base := os.Getenv("NANITE_ARCHIVE_BASE")
	if base == "" {
		base = install.ArchiveBase
	}
	expanded, err := install.ExpandArchiveBase(base)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(expanded)
	if err != nil {
		return "", fmt.Errorf("read archive base %s: %w", expanded, err)
	}
	basename := filepath.Base(projectDir)
	prefix := basename + "-"
	var latest string
	var latestTime time.Time
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
			latest = filepath.Join(expanded, e.Name())
		}
	}
	if latest == "" {
		return "", fmt.Errorf("no archive found for %s", basename)
	}
	return latest, nil
}
