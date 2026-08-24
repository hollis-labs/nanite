package main

// Admin subcommand: export-decision-tables.
//
// TASKS/phase-0/23-export-and-drop-decision-tables.md: `strategy_decisions`
// (the strategy planner's decision log — writer removed by
// TASKS/phase-0/11-cut-strategy-planner.md) and `broker_decisions` (the
// tool broker's decision log — writer removed by the same task) are both
// retired. Every historical row must land in `event_log` before
// internal/store/migrations/111_drop_broker_and_strategy_decisions.sql
// drops the two tables — this command is that export step.
//
// TASKS/phase-4/08-export-and-drop-agent-broker-decisions.md: once the
// Agent Broker's writer is retired (TASKS/phase-4/02-dispatch-to-agent-
// reflex-action-kind-and-broker-migration.md deletes chat_broker_dispatch.go
// in full), `agent_broker_decisions` (migration 058) has zero live writers
// and joins the same export-then-drop treatment this file already gives
// `strategy_decisions`/`broker_decisions`. Reuses this exact mechanism
// rather than inventing a new one, per that task's explicit instruction —
// this is the only change needed: one more entry in
// decisionTablesToExport below, plus the drop migration
// (120_drop_agent_broker_decisions.sql).
//
// Deliberately does NOT go through store.New(): store.New applies every
// pending goose migration unconditionally (internal/store/store.go's
// migrate()), so if the drop migration has already landed in this binary,
// store.New would drop the table(s) before this command's own code ever
// runs. This command opens the SQLite file directly (the same opener
// store.New uses, minus the migrate() call) so the export works
// regardless of build/deploy order — run it against a database BEFORE
// deploying a build that contains the drop migration.
//
// Usage:
//
//	nanite admin export-decision-tables [--db path] [--dry-run] [--force]

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hollis-labs/go-sqlite/sqlitekit"
	"github.com/hollis-labs/nanite/internal/store"
)

// decisionTableExport pairs a source table with the event_log event_type
// / category its rows are exported under, plus which column supplies the
// short human-readable event_log.detail value.
type decisionTableExport struct {
	table        string
	eventType    string
	category     string
	detailColumn string
}

var decisionTablesToExport = []decisionTableExport{
	{table: "strategy_decisions", eventType: "strategy_decision_export", category: "strategy", detailColumn: "approach"},
	{table: "broker_decisions", eventType: "broker_decision_export", category: "tool_broker", detailColumn: "intent"},
	{table: "agent_broker_decisions", eventType: "agent_broker_decision_export", category: "agent_broker", detailColumn: "decision"},
}

// adminExportDecisionTables is the entry point for
//
//	nanite admin export-decision-tables [--db path] [--dry-run] [--force]
//
// For each table in decisionTablesToExport: reads every row (generic
// column-name → value map, independent of the exact column set so schema
// drift can't silently drop a field), JSON-encodes the full row into
// event_log.metadata, and writes it via Store.LogEvent. After writing,
// re-counts event_log rows for that event_type and compares against the
// number of source rows read — any mismatch is a hard failure (non-zero
// exit), so a partial/failed export is loud rather than silently
// preceding the drop migration.
func adminExportDecisionTables(dbPath string, args []string) {
	dryRun := false
	force := false
	for _, a := range args {
		switch a {
		case "--dry-run":
			dryRun = true
		case "--force":
			force = true
		default:
			fmt.Fprintf(os.Stderr, "admin export-decision-tables: unknown flag %q\n", a)
			os.Exit(1)
		}
	}

	absPath, err := filepath.Abs(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "admin export-decision-tables: resolve db path: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()
	db, err := sqlitekit.OpenSingle(ctx, absPath, sqlitekit.OpenOptions{
		Options:         sqlitekit.WriterOptions(),
		CreateParentDir: false,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "admin export-decision-tables: open db %s: %v\n", absPath, err)
		os.Exit(1)
	}
	defer func() {
		_ = db.Close() // The CLI export result owns the outcome; database close is process-exit cleanup.
	}()

	s := &store.Store{DB: db}

	if !force {
		var already int
		err := db.QueryRow(
			`SELECT COUNT(*) FROM event_log WHERE event_type IN ('strategy_decision_export', 'broker_decision_export', 'agent_broker_decision_export')`,
		).Scan(&already)
		if err == nil && already > 0 {
			fmt.Fprintf(os.Stderr,
				"admin export-decision-tables: event_log already has %d exported decision row(s) — refusing to re-export (pass --force to re-run anyway; this WILL create duplicates)\n",
				already)
			os.Exit(1)
		}
	}

	exitCode := 0
	for _, spec := range decisionTablesToExport {
		exists, err := tableExistsForExport(db, spec.table)
		if err != nil {
			fmt.Fprintf(os.Stderr, "admin export-decision-tables: check table %q: %v\n", spec.table, err)
			exitCode = 1
			continue
		}
		if !exists {
			fmt.Printf("%s: table does not exist (already dropped?) — skipping\n", spec.table)
			continue
		}

		rows, err := readTableRowsForExport(db, spec.table)
		if err != nil {
			fmt.Fprintf(os.Stderr, "admin export-decision-tables: read %q: %v\n", spec.table, err)
			exitCode = 1
			continue
		}

		written := 0
		for _, row := range rows {
			metaJSON, err := json.Marshal(row)
			if err != nil {
				fmt.Fprintf(os.Stderr, "admin export-decision-tables: marshal row from %q: %v\n", spec.table, err)
				exitCode = 1
				continue
			}
			sessionID, _ := row["session_id"].(string)
			detail, _ := row[spec.detailColumn].(string)
			if dryRun {
				written++
				continue
			}
			s.LogEvent(ctx, sessionID, spec.eventType, spec.category, detail, string(metaJSON))
			written++
		}

		if !dryRun {
			var afterCount int
			if err := db.QueryRow(`SELECT COUNT(*) FROM event_log WHERE event_type = ?`, spec.eventType).Scan(&afterCount); err != nil {
				fmt.Fprintf(os.Stderr, "admin export-decision-tables: verify %q: %v\n", spec.eventType, err)
				exitCode = 1
			} else if afterCount != len(rows) {
				fmt.Fprintf(os.Stderr,
					"admin export-decision-tables: MISMATCH for %s — read %d row(s), event_log now has %d row(s) with event_type=%q (want exactly %d)\n",
					spec.table, len(rows), afterCount, spec.eventType, len(rows))
				exitCode = 1
			}
		}

		mode := "exported"
		if dryRun {
			mode = "would export"
		}
		fmt.Printf("%s: %d row(s) read, %s %d row(s) to event_log (event_type=%s)\n", spec.table, len(rows), mode, written, spec.eventType)
	}

	switch {
	case exitCode != 0:
		fmt.Fprintln(os.Stderr, "admin export-decision-tables: FAILED — do not deploy the drop migration until this succeeds cleanly")
	case dryRun:
		fmt.Println("admin export-decision-tables: dry run OK — re-run without --dry-run to actually write")
	default:
		fmt.Println("admin export-decision-tables: OK — safe to deploy the drop migration")
	}
	os.Exit(exitCode)
}

func tableExistsForExport(db *sql.DB, name string) (bool, error) {
	var n string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name = ?`, name).Scan(&n)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// readTableRowsForExport reads every row from table (ordered by id for
// deterministic output) as a generic column-name → value map. []byte
// values (as some SQLite driver paths return for TEXT columns) are
// normalized to string so json.Marshal produces readable JSON instead of
// base64.
//
// table is always one of the two fixed, hardcoded names in
// decisionTablesToExport — never user input — so building the query with
// fmt.Sprintf here is safe.
func readTableRowsForExport(db *sql.DB, table string) ([]map[string]any, error) {
	rows, err := db.Query(fmt.Sprintf("SELECT * FROM %s ORDER BY id", table))
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close() // Query and iteration errors are surfaced separately; deferred close is cleanup only.
	}()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var out []map[string]any
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		m := make(map[string]any, len(cols))
		for i, c := range cols {
			m[c] = normalizeExportScanValue(values[i])
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func normalizeExportScanValue(v any) any {
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return v
}
