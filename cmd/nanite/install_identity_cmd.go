package main

// Operator CLI for the install identity (offline, direct database access).

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/hollis-labs/nanite/internal/store"
)

const installIdentityUsage = `Usage: nanite install-identity [--db path] <command>

The install id is a random UUID minted once per database. It is the install part
of the Tether external key nanite/<install_id>/<host_settings_id>.

  show            print the install id
  regen [--force] mint a new install id

Use regen on a database that was cloned or restored onto a second install, BEFORE
any agent is enrolled from it. It refuses when any agent binding or actor instance
exists; --force overrides that. Agents already enrolled keep their stored URN and
their existing Tether registration; forcing does not rewrite either.`

func cmdInstallIdentity(args []string) {
	code := runInstallIdentity(args, os.Stdout, os.Stderr)
	if code != 0 {
		os.Exit(code)
	}
}

func runInstallIdentity(args []string, stdout, stderr io.Writer) int {
	dbFlag, rest := extractLeadingDBFlag(args)
	if len(rest) == 0 {
		fmt.Fprintln(stderr, installIdentityUsage)
		return 2
	}
	force := false
	for _, a := range rest[1:] {
		if a == "--force" && rest[0] == "regen" {
			force = true
			continue
		}
		fmt.Fprintf(stderr, "unexpected argument %q\n%s\n", a, installIdentityUsage)
		return 2
	}
	if rest[0] != "show" && rest[0] != "regen" {
		fmt.Fprintf(stderr, "unknown command %q\n%s\n", rest[0], installIdentityUsage)
		return 2
	}
	ctx := context.Background()
	s, err := store.New(ctx, resolveDBPathWith(dbFlag))
	if err != nil {
		fmt.Fprintf(stderr, "open store: %v\n", err)
		return 1
	}
	defer closeStoreBestEffort(ctx, s)

	switch rest[0] {
	case "show":
		ii, err := s.GetInstallIdentity(ctx)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, ii.InstallID)
	case "regen":
		ii, err := s.RegenerateInstallID(ctx, force, "cli")
		if errors.Is(err, store.ErrInstallHasEnrolledAgents) {
			fmt.Fprintf(stderr, "refusing to regenerate: %v\nAlready-enrolled agents keep their stored URN; regenerating now would change the key new enrollments use.\n", err)
			return 1
		}
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "install id regenerated\nprevious: %s\nnew:      %s\n", ii.PreviousInstallID, ii.InstallID)
	}
	return 0
}
