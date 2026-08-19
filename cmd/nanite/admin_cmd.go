package main

// Admin CLI subcommands. Read-only operator surface against the prod store.
//
// CW-20260509-0049 lands the first subcommand: `agent-broker-decisions`,
// which dumps recent rows from the agent_broker_decisions telemetry table
// (CW-20260509-0047) so operators can see how the deterministic agent
// broker is performing.
//
// Subcommand naming note:
//
//   The boot prompt and SP-20260429-0001 spec both used the bare name
//   `broker-decisions`. That overlaps with the unrelated tool-broker
//   telemetry stream (`broker_decisions` table, internal/store/broker.go,
//   CW-20260419-0011 progressive discovery). The agent-broker schema landed
//   in CW-20260509-0047 as `agent_broker_decisions` to keep the two
//   telemetry streams unambiguous. The CLI subcommand follows the same
//   convention: `nanite admin agent-broker-decisions`.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/slogx"
	"github.com/hollis-labs/nanite/internal/store"
)

// cmdAdmin dispatches admin subcommands. An optional --db flag may appear
// before the subcommand name so operators can point at a non-default store
// without wrapping every subcommand in its own flag set (matches the
// `nanite message` convention).
func cmdAdmin(args []string) {
	usage := func(w io.Writer) {
		fmt.Fprintf(w, "usage: %s admin [--db path] <agent-broker-decisions|export-decision-tables>\n", brand.BinaryName)
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
	case "agent-broker-decisions":
		adminAgentBrokerDecisions(dbPath, rest)
	case "export-decision-tables":
		// TASKS/phase-0/23-export-and-drop-decision-tables.md — export
		// strategy_decisions + broker_decisions to event_log before their
		// drop migration runs. See admin_export_decisions.go.
		adminExportDecisionTables(dbPath, rest)
	default:
		fmt.Fprintf(os.Stderr, "unknown admin subcommand: %s\n", sub)
		usage(os.Stderr)
		os.Exit(1)
	}
}

// agentBrokerDecisionsFilter is the parsed filter shape applied to the
// rows returned by Store.ListRecentAgentBrokerDecisions before output.
//
// All fields are zero-value-safe: a zero Since means "no since cutoff"
// and a zero ConfidenceLT (with HasConfidenceLT=false) means "no
// confidence filter". Limit is the post-filter cap (default 50).
type agentBrokerDecisionsFilter struct {
	Limit           int
	Since           time.Duration
	ConfidenceLT    float64
	HasConfidenceLT bool
}

// parseAgentBrokerWhere parses a single `--where` clause. v1 supports
// only `confidence<FLOAT` — extending the grammar (e.g. `decision=worker`,
// `mode_signal=plan`) is a v2 concern when the operator workflow tells us
// what filters are actually load-bearing.
//
// Returns the parsed comparison (currently always confidence<X) or an
// error describing the unsupported clause shape. The caller is expected
// to surface the error verbatim — the message names the supported form
// so the operator can self-correct without rerunning with --help.
func parseAgentBrokerWhere(clause string) (float64, error) {
	clause = strings.TrimSpace(clause)
	if clause == "" {
		return 0, fmt.Errorf("--where: empty clause")
	}

	// Only confidence<FLOAT for v1. Reject confidence<= / confidence> /
	// other comparators with a clear message rather than silently
	// failing the parse a few characters in.
	const prefix = "confidence<"
	if !strings.HasPrefix(clause, prefix) {
		return 0, fmt.Errorf("--where %q: only %q is supported in v1", clause, prefix+"FLOAT")
	}
	rest := clause[len(prefix):]
	if rest == "" {
		return 0, fmt.Errorf("--where %q: missing FLOAT value after %q", clause, prefix)
	}
	// Reject confidence<=, confidence<>, etc. by checking the next byte.
	if rest[0] == '=' || rest[0] == '>' {
		return 0, fmt.Errorf("--where %q: only %q is supported in v1", clause, prefix+"FLOAT")
	}
	v, err := strconv.ParseFloat(rest, 64)
	if err != nil {
		return 0, fmt.Errorf("--where %q: parse FLOAT: %w", clause, err)
	}
	return v, nil
}

// parseAgentBrokerSince parses a duration argument. `--since 0` and the
// empty string are both treated as "no cutoff" so the operator can drop
// the flag without having to remove its value too. Negative durations
// are rejected — they would be silently treated as "no cutoff" by the
// filter logic, but a negative value almost always means the operator
// confused two flag conventions.
func parseAgentBrokerSince(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("--since %q: %w", s, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("--since %q: must be non-negative", s)
	}
	return d, nil
}

// applyAgentBrokerFilter walks the (already DESC-ordered) rows from
// the store and returns up to filter.Limit rows that satisfy the
// since + confidence predicates.
//
// Filtering happens in-memory rather than at the SQL layer so the
// helper signature stays minimal (limit-only). Sibling consumer
// CW-20260509-0048 (SSE) wants pure recent-N with no operator filters;
// pushing since/confidence down would force it to pass zero values
// for filters it doesn't use. Trade-off accepted: we read up to ~5x
// the requested limit from disk to give in-memory filtering room to
// breathe before truncating to limit. Acceptable for a read-only
// triage CLI on a sub-million-row telemetry table.
//
// The fetchPool helper computes how many rows to ask the store for
// before filtering — see its godoc for the heuristic.
func applyAgentBrokerFilter(rows []*store.AgentBrokerDecision, f agentBrokerDecisionsFilter, now time.Time) []*store.AgentBrokerDecision {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}

	out := make([]*store.AgentBrokerDecision, 0, limit)
	var sinceCutoff time.Time
	if f.Since > 0 {
		sinceCutoff = now.Add(-f.Since)
	}

	for _, row := range rows {
		if f.Since > 0 {
			// CreatedAt is SQLite datetime('now') format: "YYYY-MM-DD HH:MM:SS"
			// in UTC. Parse defensively — a malformed timestamp shouldn't
			// crash the CLI, just exclude the row from the since-filtered
			// view.
			t, err := parseSQLiteDatetime(row.CreatedAt)
			if err != nil {
				continue
			}
			if t.Before(sinceCutoff) {
				// Rows are DESC-ordered by created_at: once we hit one
				// older than the cutoff, every remaining row is also
				// older. Short-circuit.
				break
			}
		}
		if f.HasConfidenceLT && !(row.Confidence < f.ConfidenceLT) {
			continue
		}
		out = append(out, row)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// fetchPool decides how many rows to pull from the store before
// in-memory filtering. With no filters, the store-level limit is
// exactly the user-visible limit. With filters, we pull more so the
// user-visible limit can still be satisfied after rejection.
//
// The 5x multiplier is a heuristic: at 50-row limit, that's 250 rows
// scanned, well under the (created_at DESC) index's selective range.
// Capped at 1000 to avoid pathological scans on a multi-million-row
// table; if that cap bites in practice, an operator can drop --since
// and re-query without the filter.
func fetchPool(f agentBrokerDecisionsFilter) int {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if f.Since == 0 && !f.HasConfidenceLT {
		return limit
	}
	pool := limit * 5
	if pool > 1000 {
		pool = 1000
	}
	return pool
}

// parseSQLiteDatetime parses the SQLite datetime('now') format used by
// the agent_broker_decisions.created_at column. The format is fixed
// (no timezone suffix; the value is UTC by SQLite convention), so the
// parser is a single fixed-layout call rather than a list of fallbacks.
func parseSQLiteDatetime(s string) (time.Time, error) {
	const layout = "2006-01-02 15:04:05"
	return time.Parse(layout, strings.TrimSpace(s))
}

// adminAgentBrokerDecisions is the entry point for
//
//	nanite admin agent-broker-decisions [--since DUR] [--where confidence<F] [--limit N] [--json]
//
// It opens the store, calls Store.ListRecentAgentBrokerDecisions with a
// pool sized for the requested filters, applies the filters in-memory,
// and prints either a tabular summary (default) or NDJSON-style
// per-row JSON (--json).
func adminAgentBrokerDecisions(dbPath string, args []string) {
	fs := flag.NewFlagSet("admin agent-broker-decisions", flag.ExitOnError)
	since := fs.String("since", "", "filter rows newer than this duration (e.g. 24h, 30m); empty/omit = no since cutoff")
	where := fs.String("where", "", "filter clause; v1 supports only confidence<FLOAT (e.g. confidence<0.5)")
	limit := fs.Int("limit", 50, "max rows to return after filtering (default 50)")
	jsonOut := fs.Bool("json", false, "emit one JSON object per row instead of a tabular summary")
	fs.Parse(args)

	if *limit <= 0 {
		fmt.Fprintln(os.Stderr, "admin agent-broker-decisions: --limit must be positive")
		os.Exit(1)
	}

	filter := agentBrokerDecisionsFilter{Limit: *limit}

	if dur, err := parseAgentBrokerSince(*since); err != nil {
		fmt.Fprintf(os.Stderr, "admin agent-broker-decisions: %v\n", err)
		os.Exit(1)
	} else {
		filter.Since = dur
	}

	if strings.TrimSpace(*where) != "" {
		threshold, err := parseAgentBrokerWhere(*where)
		if err != nil {
			fmt.Fprintf(os.Stderr, "admin agent-broker-decisions: %v\n", err)
			os.Exit(1)
		}
		filter.ConfidenceLT = threshold
		filter.HasConfidenceLT = true
	}

	s, err := store.New(context.Background(), dbPath)
	if err != nil {
		slogx.Fatal("admin agent-broker-decisions: open db", "path", dbPath, "err", err)
	}
	defer s.Close()

	rows, err := s.ListRecentAgentBrokerDecisions(fetchPool(filter))
	if err != nil {
		slogx.Fatal("admin agent-broker-decisions: list", "err", err)
	}

	filtered := applyAgentBrokerFilter(rows, filter, time.Now().UTC())

	if *jsonOut {
		printAgentBrokerJSON(os.Stdout, filtered)
		return
	}
	printAgentBrokerTable(os.Stdout, filtered)
}

// printAgentBrokerTable writes a tabwriter-aligned summary of the
// rows. Columns chosen to match the spec: session_id, turn_id,
// mode_signal, scope_tier, decision, reason, confidence, created_at.
//
// The user_input_hash and reflex_id columns are intentionally omitted
// from the default tabular view — they're noisy in a terminal and
// available via --json when the operator needs them. (If the use
// case for `agent-broker-decisions` shifts toward "find a specific
// turn", we'll add a --columns flag rather than make the default
// view wider.)
func printAgentBrokerTable(w io.Writer, rows []*store.AgentBrokerDecision) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	defer tw.Flush()

	fmt.Fprintln(tw, "SESSION_ID\tTURN_ID\tMODE\tSCOPE\tDECISION\tCONFIDENCE\tCREATED_AT\tREASON")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%.3f\t%s\t%s\n",
			truncDisplay(r.SessionID, 24),
			truncDisplay(r.TurnID, 24),
			emptyDash(r.ModeSignal),
			emptyDash(r.ScopeTier),
			emptyDash(r.Decision),
			r.Confidence,
			r.CreatedAt,
			truncDisplay(emptyDash(r.Reason), 60),
		)
	}
}

// printAgentBrokerJSON emits one row per line as compact JSON. NDJSON
// shape is friendlier to `jq` pipelines than a single JSON array;
// parsing per-row also lets a downstream consumer process partial
// output if the CLI is tee'd into a file mid-write.
func printAgentBrokerJSON(w io.Writer, rows []*store.AgentBrokerDecision) {
	enc := json.NewEncoder(w)
	for _, r := range rows {
		// json.Encoder writes a trailing newline by default — exactly
		// what NDJSON wants.
		if err := enc.Encode(r); err != nil {
			slogx.Fatal("admin agent-broker-decisions: json encode", "err", err)
		}
	}
}

// emptyDash maps the empty-string sentinel used by AgentBrokerDecision
// (NOT NULL DEFAULT ” columns) to a dash so the tabular view doesn't
// render visually-empty cells. JSON output preserves the empty string
// for round-trip consumers.
func emptyDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// truncDisplay shortens a string for terminal display. The cap is a
// soft guideline — an operator inspecting a too-wide row can switch
// to --json to see the full value. UTF-8-safe by counting runes,
// not bytes.
func truncDisplay(s string, max int) string {
	if max <= 0 {
		return s
	}
	if len([]rune(s)) <= max {
		return s
	}
	rs := []rune(s)
	return string(rs[:max-1]) + "…"
}
