// fleet-install installs CI-built binaries described by a deploy.yaml that ships
// inside the release tarball. It is app-agnostic: every specific lives in deploy.yaml.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
)

// version is set with -ldflags "-X main.version=...".
var version = "dev"

const usage = `usage:
  fleet-install deploy <app> [--commit SHA | --run ID | --latest-green | --from-file TARBALL [--sums FILE]]
                [--repo owner/name] [--target ID] [--dry-run] [--accept-migration] [--force]
                [--restart-done] [--root DIR]
  fleet-install rollback <app> [--target ID] [--restart-done] [--root DIR]
  fleet-install status <app> [--target ID] [--root DIR]
  fleet-install list [--root DIR]
  fleet-install version`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, defaultDeps(), os.Args[1:]))
}

// run executes one command line and returns the process exit code.
func run(ctx context.Context, d *Deps, args []string) int {
	err := dispatch(ctx, d, args)
	if err == nil {
		return 0
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		fmt.Fprintf(d.Err, "fleet-install: %s\n", oneLine(ee.Msg))
		return ee.Code
	}
	fmt.Fprintf(d.Err, "fleet-install: %s\n", oneLine(err.Error()))
	return ExitFail
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// parseInterspersed parses flags that may follow positional arguments.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func dispatch(ctx context.Context, d *Deps, args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(d.Err, usage)
		return fail(ExitUsage, "missing command")
	}
	cmd, rest := args[0], args[1:]
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	root := fs.String("root", "", "state/cache directory override")
	target := fs.String("target", "", "target id")
	restartDone := fs.Bool("restart-done", false, "the operator ran the printed cerberus restart")
	var o DeployOpts
	if cmd == "deploy" {
		fs.StringVar(&o.Source.Commit, "commit", "", "commit SHA (needs a green build run)")
		fs.StringVar(&o.Source.Run, "run", "", "workflow run id")
		fs.BoolVar(&o.Source.Latest, "latest-green", false, "latest green build on the branch")
		fs.StringVar(&o.Source.FromFile, "from-file", "", "local tarball")
		fs.StringVar(&o.Source.Sums, "sums", "", "SHA256SUMS for --from-file")
		fs.StringVar(&o.Source.Repo, "repo", "", "GitHub repo owner/name (default: remembered from the last deploy)")
		fs.StringVar(&o.Source.Branch, "branch", "main", "branch for --latest-green")
		fs.StringVar(&o.Source.Workflow, "workflow", "", "build workflow file (default build.yml)")
		fs.StringVar(&o.Source.Artifact, "artifact-name", "", "artifact name pattern (default {app}-{sha12})")
		fs.StringVar(&o.Source.Platform, "platform", "", "os/arch of the tarball (default: this host)")
		fs.BoolVar(&o.DryRun, "dry-run", false, "print the steps, touch nothing")
		fs.BoolVar(&o.AcceptMigration, "accept-migration", false, "allow a build that migrates the database")
		fs.BoolVar(&o.Force, "force", false, "continue despite active work (sql_zero)")
	}
	pos, err := parseInterspersed(fs, rest)
	if err != nil {
		return fail(ExitUsage, "%v", err)
	}
	need := func(n int) error {
		if len(pos) != n {
			fmt.Fprintln(d.Err, usage)
			return fail(ExitUsage, "%s takes %d argument(s)", cmd, n)
		}
		return nil
	}
	switch cmd {
	case "version", "--version":
		d.say("fleet-install %s", version)
		return nil
	case "help", "-h", "--help":
		d.say("%s", usage)
		return nil
	case "list":
		if err := need(0); err != nil {
			return err
		}
		return d.List(*root)
	case "status":
		if err := need(1); err != nil {
			return err
		}
		return d.Status(ctx, pos[0], *target, *root)
	case "rollback":
		if err := need(1); err != nil {
			return err
		}
		return d.Rollback(ctx, pos[0], *target, *root, *restartDone)
	case "deploy":
		if err := need(1); err != nil {
			return err
		}
		n := 0
		for _, b := range []bool{o.Source.Commit != "", o.Source.Run != "", o.Source.Latest, o.Source.FromFile != ""} {
			if b {
				n++
			}
		}
		if n != 1 {
			return fail(ExitUsage, "give exactly one of --commit, --run, --latest-green, --from-file")
		}
		o.App, o.Target, o.Root, o.RestartDone = pos[0], *target, *root, *restartDone
		if !nameRe.MatchString(o.App) {
			return fail(ExitUsage, "invalid app name %q", o.App)
		}
		return d.Deploy(ctx, o)
	}
	fmt.Fprintln(d.Err, usage)
	return fail(ExitUsage, "unknown command %q", cmd)
}
