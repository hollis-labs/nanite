// Package installcmd implements the framework-injection flow shared by the
// `nanite-agent init` entry point and the legacy `nanite install` deprecation
// shim. It is a thin wrapper over internal/service/install; no business logic
// lives here beyond flag parsing and output formatting.
package installcmd

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/nanite/internal/service/install"
	"github.com/mattn/go-isatty"
)

// Run parses args and executes the framework-injection flow. label is the
// program name used in user-visible output (e.g. "nanite-agent init").
// It does not return on error — it calls os.Exit with an appropriate code.
func Run(label string, args []string) {
	fs := flag.NewFlagSet(label, flag.ExitOnError)
	project := fs.String("project", "", "target project directory (omit to install to ~/.nanite/)")
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

	opts := install.InstallProjectOptions{
		ProjectDir:  projectDir,
		GlobalHome:  defaultGlobalHome(),
		Adapters:    *adapters,
		NoAdapters:  *noAdapters,
		Reconfigure: *reconfigure,
		Interactive: isStdinTTY(),
		Stdin:       os.Stdin,
		Stdout:      os.Stdout,
	}
	report, err := svc.InstallProject(opts)
	if err != nil {
		die(label, "install project", err)
	}

	switch {
	case report.FreshScaffold:
		fmt.Printf("%s: fresh scaffold in %s\n", label, projectDir)
	case report.Adopted:
		fmt.Printf("%s: adopted existing .nanite/ in %s\n", label, projectDir)
	}

	if len(report.Adapters) > 0 {
		fmt.Printf("%s: adapters [%s] (%d enabled, %d disabled)\n",
			label,
			strings.Join(report.Adapters, ", "),
			len(report.Adapters),
			4-len(report.Adapters),
		)
	} else {
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
