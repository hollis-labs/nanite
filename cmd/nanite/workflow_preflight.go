package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/hollis-labs/nanite/internal/workflowhost"
)

func cmdWorkflow(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] != "preflight" {
		return fmt.Errorf("usage: nanite workflow preflight --db <offline-copy-path>")
	}
	flags := flag.NewFlagSet("workflow preflight", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("db", "", "stable offline SQLite copy (stopped-service DB + WAL, or sqlite3 .backup)")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if *path == "" || flags.NArg() != 0 {
		return fmt.Errorf("usage: nanite workflow preflight --db <offline-copy-path>")
	}
	return workflowhost.PreflightWorkflowStorageCopy(ctx, *path, stdout)
}
