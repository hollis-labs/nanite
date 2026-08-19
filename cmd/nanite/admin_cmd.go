package main

// Admin CLI subcommands. Read-only operator surface against the prod store.

import (
	"fmt"
	"io"
	"os"

	"github.com/hollis-labs/nanite/internal/brand"
)

// cmdAdmin dispatches admin subcommands. An optional --db flag may appear
// before the subcommand name so operators can point at a non-default store
// without wrapping every subcommand in its own flag set (matches the
// `nanite message` convention).
func cmdAdmin(args []string) {
	usage := func(w io.Writer) {
		fmt.Fprintf(w, "usage: %s admin [--db path] <export-decision-tables>\n", brand.BinaryName)
	}

	if len(args) < 1 {
		usage(os.Stderr)
		os.Exit(1)
	}

	// An unset --db resolves via go-apppaths (CW-20260517-0061).
	dbFlag := ""
	remaining := args
	if len(args) >= 2 && args[0] == "--db" {
		dbFlag = args[1]
		remaining = args[2:]
	}
	dbPath := resolveDBPathWith(dbFlag)
	if len(remaining) < 1 {
		usage(os.Stderr)
		os.Exit(1)
	}

	sub := remaining[0]
	rest := remaining[1:]

	switch sub {
	case "export-decision-tables":
		// TASKS/phase-0/23-export-and-drop-decision-tables.md +
		// TASKS/phase-4/08-export-and-drop-agent-broker-decisions.md —
		// export strategy_decisions + broker_decisions +
		// agent_broker_decisions to event_log before their drop
		// migrations run. See admin_export_decisions.go.
		adminExportDecisionTables(dbPath, rest)
	default:
		fmt.Fprintf(os.Stderr, "unknown admin subcommand: %s\n", sub)
		usage(os.Stderr)
		os.Exit(1)
	}
}
