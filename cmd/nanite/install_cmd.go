package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/service/install"
)

// cmdInstall is the entry point for `nanite install`. Thin wrapper over
// internal/service/install. Matches the cmdServe/cmdMCP pattern: manual
// flag parsing, direct service calls, os.Exit on error.
func cmdInstall(args []string) {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	project := fs.String("project", "", "target project directory (omit to install to ~/.nanite/)")
	migrate := fs.Bool("migrate-from-agentrc", false, "archive existing .agentrc/ and migrate to .nanite/")
	archiveOnly := fs.Bool("archive-only", false, "archive .agentrc/ without scaffolding .nanite/ (for scratch dirs)")
	rollback := fs.Bool("rollback", false, "reverse the most recent migration for --project")
	resume := fs.Bool("resume", false, "continue a partial install from its state marker")
	restart := fs.Bool("restart", false, "reverse a partial install and run a fresh migration")
	refresh := fs.Bool("refresh", false, "re-extract embedded assets to ~/.nanite/, skipping user-modified files")
	force := fs.Bool("force", false, "overwrite user-modified files during --refresh")
	printDiff := fs.Bool("print-diff", false, "dry-run: show what would change (not yet implemented)")
	fs.Parse(args)

	svc := install.New()

	// Home install (no --project, or --refresh).
	if *project == "" && !*refresh {
		home, err := os.UserHomeDir()
		if err != nil {
			installDie("resolve home dir", err)
		}
		target := filepath.Join(home, ".nanite")
		report, err := svc.InstallHome(install.InstallHomeOptions{Target: target, Force: *force})
		if err != nil {
			installDie("install home", err)
		}
		fmt.Printf("%s install: created=%d unchanged=%d skipped=%d forced=%d\n",
			brand.BinaryName, report.Created, report.Unchanged, report.Skipped, report.Forced)
		if len(report.SkippedFiles) > 0 {
			fmt.Println("  skipped (user-modified):")
			for _, f := range report.SkippedFiles {
				fmt.Printf("    %s\n", f)
			}
		}
		return
	}

	// --refresh re-extracts to ~/.nanite/ even when --project was passed.
	// Refresh takes precedence and re-extracts the home tree.
	if *refresh {
		home, err := os.UserHomeDir()
		if err != nil {
			installDie("resolve home dir", err)
		}
		target := filepath.Join(home, ".nanite")
		report, err := svc.InstallHome(install.InstallHomeOptions{Target: target, Force: *force})
		if err != nil {
			installDie("refresh home", err)
		}
		fmt.Printf("%s refresh: created=%d unchanged=%d skipped=%d forced=%d\n",
			brand.BinaryName, report.Created, report.Unchanged, report.Skipped, report.Forced)
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
		installDie("resolve project dir", err)
	}

	if *printDiff {
		fmt.Fprintf(os.Stderr, "%s: --print-diff not yet implemented\n", brand.BinaryName)
		os.Exit(2)
	}

	if *rollback {
		if err := svc.Rollback(install.RollbackOptions{ProjectDir: projectDir}); err != nil {
			installDie("rollback", err)
		}
		fmt.Printf("%s: rolled back %s\n", brand.BinaryName, projectDir)
		return
	}

	if *resume {
		latest, err := latestArchiveFor(projectDir)
		if err != nil {
			installDie("find latest archive", err)
		}
		if err := svc.Resume(install.ResumeOptions{
			ProjectDir:  projectDir,
			ArchivePath: latest,
			GlobalHome:  defaultGlobalHome(),
		}); err != nil {
			installDie("resume", err)
		}
		fmt.Printf("%s: resumed %s\n", brand.BinaryName, projectDir)
		return
	}

	if *restart {
		latest, err := latestArchiveFor(projectDir)
		if err != nil {
			installDie("find latest archive", err)
		}
		if err := svc.Restart(install.RestartOptions{
			ProjectDir:  projectDir,
			ArchivePath: latest,
			GlobalHome:  defaultGlobalHome(),
		}); err != nil {
			installDie("restart", err)
		}
		fmt.Printf("%s: restarted migration for %s\n", brand.BinaryName, projectDir)
		return
	}

	opts := install.InstallProjectOptions{
		ProjectDir:         projectDir,
		GlobalHome:         defaultGlobalHome(),
		MigrateFromAgentrc: *migrate,
		ArchiveOnly:        *archiveOnly,
	}
	report, err := svc.InstallProject(opts)
	if err != nil {
		// Partial install detection: in non-TTY, exit with code 3 so Phase
		// 5 dispatch harnesses can detect and retry with --resume.
		if errors.Is(err, install.ErrPartialInstall) {
			if !isStdinTTY() {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(3)
			}
			handlePartialInteractive(svc, projectDir, err)
			return
		}
		installDie("install project", err)
	}

	switch {
	case report.FreshScaffold:
		fmt.Printf("%s: fresh scaffold in %s\n", brand.BinaryName, projectDir)
	case report.Migrated:
		fmt.Printf("%s: migrated %s (archive: %s)\n", brand.BinaryName, projectDir, report.ArchivePath)
	case report.Adopted:
		fmt.Printf("%s: adopted existing .nanite/ in %s\n", brand.BinaryName, projectDir)
	case report.ArchiveOnly:
		fmt.Printf("%s: archived %s (archive: %s)\n", brand.BinaryName, projectDir, report.ArchivePath)
	}
}

// handlePartialInteractive prompts the user to resume/restart/cancel when
// InstallProject detects a partial install in an interactive terminal.
func handlePartialInteractive(svc *install.Service, projectDir string, detectedErr error) {
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
		installDie("find latest archive", err)
	}
	switch strings.ToLower(choice) {
	case "r":
		if err := svc.Resume(install.ResumeOptions{
			ProjectDir:  projectDir,
			ArchivePath: latest,
			GlobalHome:  defaultGlobalHome(),
		}); err != nil {
			installDie("resume", err)
		}
		fmt.Printf("%s: resumed %s\n", brand.BinaryName, projectDir)
	case "s":
		if err := svc.Restart(install.RestartOptions{
			ProjectDir:  projectDir,
			ArchivePath: latest,
			GlobalHome:  defaultGlobalHome(),
		}); err != nil {
			installDie("restart", err)
		}
		fmt.Printf("%s: restarted %s\n", brand.BinaryName, projectDir)
	default:
		fmt.Println("cancelled")
	}
}

// installDie prints an error and exits with code 1.
func installDie(action string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %s: %v\n", brand.BinaryName, action, err)
	os.Exit(1)
}

// isStdinTTY returns true if stdin is a terminal (character device).
func isStdinTTY() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

// resolveProjectDir expands "." to the current working directory and returns
// the absolute path.
func resolveProjectDir(project string) (string, error) {
	if project == "." || project == "" {
		return os.Getwd()
	}
	return filepath.Abs(project)
}

// defaultGlobalHome returns the default global install target (~/.nanite).
func defaultGlobalHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".nanite")
}

// latestArchiveFor returns the most recent archive dir under
// ~/Projects-apps/.archived (or NANITE_ARCHIVE_BASE) matching basename-*.
// Returns an error if no matching archive exists.
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
