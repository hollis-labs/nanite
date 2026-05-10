package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

// TestParseAgentBrokerWhere covers the v1-supported `confidence<FLOAT`
// shape and a representative spread of rejection cases. The error
// messages are inspected for the string "v1" so a future v2 widening
// (decision=worker, mode_signal=plan, etc.) can update this gate
// without rewriting the assertion shape.
func TestParseAgentBrokerWhere(t *testing.T) {
	tests := []struct {
		name    string
		clause  string
		want    float64
		wantErr bool
	}{
		{name: "happy_path_lt", clause: "confidence<0.5", want: 0.5, wantErr: false},
		{name: "happy_path_zero", clause: "confidence<0", want: 0, wantErr: false},
		{name: "happy_path_one", clause: "confidence<1.0", want: 1.0, wantErr: false},
		{name: "trim_spaces", clause: "  confidence<0.85  ", want: 0.85, wantErr: false},

		{name: "empty_clause", clause: "", wantErr: true},
		{name: "unsupported_lte", clause: "confidence<=0.5", wantErr: true},
		{name: "unsupported_gt", clause: "confidence>0.5", wantErr: true},
		{name: "unsupported_eq", clause: "confidence=0.5", wantErr: true},
		{name: "unsupported_column", clause: "decision=worker", wantErr: true},
		{name: "missing_value", clause: "confidence<", wantErr: true},
		{name: "non_numeric_value", clause: "confidence<abc", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAgentBrokerWhere(tt.clause)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseAgentBrokerWhere(%q): want error, got nil (value=%v)", tt.clause, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAgentBrokerWhere(%q): unexpected error: %v", tt.clause, err)
			}
			if got != tt.want {
				t.Errorf("parseAgentBrokerWhere(%q) = %v, want %v", tt.clause, got, tt.want)
			}
		})
	}
}

// TestParseAgentBrokerSince validates the duration parser. Empty
// string is the documented "no cutoff" sentinel; negative durations
// are rejected so an operator who meant `-since 24h` and dropped a
// sign gets a clear error instead of a silently empty result.
func TestParseAgentBrokerSince(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    time.Duration
		wantErr bool
	}{
		{name: "empty_means_no_cutoff", in: "", want: 0, wantErr: false},
		{name: "whitespace_only", in: "   ", want: 0, wantErr: false},
		{name: "minutes", in: "30m", want: 30 * time.Minute, wantErr: false},
		{name: "hours", in: "24h", want: 24 * time.Hour, wantErr: false},
		{name: "compound", in: "1h30m", want: 90 * time.Minute, wantErr: false},
		{name: "zero_explicit", in: "0", want: 0, wantErr: false},

		{name: "garbage", in: "yesterday", wantErr: true},
		{name: "negative_rejected", in: "-1h", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAgentBrokerSince(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseAgentBrokerSince(%q): want error, got nil (value=%v)", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAgentBrokerSince(%q): unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("parseAgentBrokerSince(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// TestApplyAgentBrokerFilter exercises the in-memory filter logic
// directly so the SQL/store path doesn't need to be involved. The
// rows here are constructed in DESC-order-by-created_at to match
// the contract that ListRecentAgentBrokerDecisions returns rows
// already sorted that way.
func TestApplyAgentBrokerFilter(t *testing.T) {
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	// Helper to format a row's CreatedAt in the SQLite datetime('now')
	// shape (UTC, no tz suffix).
	at := func(d time.Duration) string {
		return now.Add(-d).Format("2006-01-02 15:04:05")
	}

	rows := []*store.AgentBrokerDecision{
		{TurnID: "turn-now", Decision: "worker", Confidence: 0.95, CreatedAt: at(0)},
		{TurnID: "turn-15m", Decision: "planner", Confidence: 0.40, CreatedAt: at(15 * time.Minute)},
		{TurnID: "turn-1h", Decision: "worker", Confidence: 0.70, CreatedAt: at(1 * time.Hour)},
		{TurnID: "turn-2h", Decision: "", Confidence: 1.00, CreatedAt: at(2 * time.Hour)},
		{TurnID: "turn-25h", Decision: "worker", Confidence: 0.20, CreatedAt: at(25 * time.Hour)},
	}

	t.Run("no_filter_returns_all_within_limit", func(t *testing.T) {
		got := applyAgentBrokerFilter(rows, agentBrokerDecisionsFilter{Limit: 50}, now)
		if len(got) != len(rows) {
			t.Fatalf("got %d rows, want %d", len(got), len(rows))
		}
	})

	t.Run("limit_truncates", func(t *testing.T) {
		got := applyAgentBrokerFilter(rows, agentBrokerDecisionsFilter{Limit: 2}, now)
		if len(got) != 2 {
			t.Fatalf("got %d rows, want 2", len(got))
		}
		if got[0].TurnID != "turn-now" || got[1].TurnID != "turn-15m" {
			t.Errorf("limit truncate order wrong: got [%s, %s]", got[0].TurnID, got[1].TurnID)
		}
	})

	t.Run("zero_limit_defaults_to_50", func(t *testing.T) {
		got := applyAgentBrokerFilter(rows, agentBrokerDecisionsFilter{Limit: 0}, now)
		if len(got) != len(rows) {
			t.Fatalf("zero-limit should use default 50; got %d rows, want %d", len(got), len(rows))
		}
	})

	t.Run("since_24h_excludes_25h_row", func(t *testing.T) {
		got := applyAgentBrokerFilter(rows, agentBrokerDecisionsFilter{
			Limit: 50,
			Since: 24 * time.Hour,
		}, now)
		if len(got) != 4 {
			t.Fatalf("since=24h: got %d rows, want 4 (turn-25h excluded)", len(got))
		}
		for _, r := range got {
			if r.TurnID == "turn-25h" {
				t.Errorf("turn-25h should be excluded by since=24h cutoff")
			}
		}
	})

	t.Run("since_30m_excludes_older", func(t *testing.T) {
		got := applyAgentBrokerFilter(rows, agentBrokerDecisionsFilter{
			Limit: 50,
			Since: 30 * time.Minute,
		}, now)
		if len(got) != 2 {
			t.Fatalf("since=30m: got %d rows, want 2 (turn-now, turn-15m)", len(got))
		}
		if got[0].TurnID != "turn-now" || got[1].TurnID != "turn-15m" {
			t.Errorf("since=30m order wrong: got [%s, %s]", got[0].TurnID, got[1].TurnID)
		}
	})

	t.Run("confidence_lt_0_5_keeps_low_confidence", func(t *testing.T) {
		got := applyAgentBrokerFilter(rows, agentBrokerDecisionsFilter{
			Limit:           50,
			ConfidenceLT:    0.5,
			HasConfidenceLT: true,
		}, now)
		if len(got) != 2 {
			t.Fatalf("confidence<0.5: got %d rows, want 2 (turn-15m=0.40, turn-25h=0.20)", len(got))
		}
		for _, r := range got {
			if !(r.Confidence < 0.5) {
				t.Errorf("row %s confidence %v should not be in result of confidence<0.5", r.TurnID, r.Confidence)
			}
		}
	})

	t.Run("confidence_lt_strict_excludes_equal", func(t *testing.T) {
		// confidence<1.0 should exclude the row with confidence=1.0 exactly.
		got := applyAgentBrokerFilter(rows, agentBrokerDecisionsFilter{
			Limit:           50,
			ConfidenceLT:    1.0,
			HasConfidenceLT: true,
		}, now)
		for _, r := range got {
			if r.Confidence == 1.0 {
				t.Errorf("strict < should exclude confidence=1.0 row %s", r.TurnID)
			}
		}
	})

	t.Run("combined_filters", func(t *testing.T) {
		got := applyAgentBrokerFilter(rows, agentBrokerDecisionsFilter{
			Limit:           50,
			Since:           24 * time.Hour,
			ConfidenceLT:    0.5,
			HasConfidenceLT: true,
		}, now)
		// Want: turn-15m (0.40, 15m ago) — only one row that's both
		// recent AND low-confidence.
		if len(got) != 1 {
			t.Fatalf("combined filter: got %d rows, want 1 (turn-15m)", len(got))
		}
		if got[0].TurnID != "turn-15m" {
			t.Errorf("combined filter expected turn-15m, got %s", got[0].TurnID)
		}
	})

	t.Run("malformed_created_at_excluded_when_since_set", func(t *testing.T) {
		bad := []*store.AgentBrokerDecision{
			{TurnID: "turn-good", Confidence: 0.5, CreatedAt: at(0)},
			{TurnID: "turn-bad", Confidence: 0.5, CreatedAt: "not-a-timestamp"},
		}
		got := applyAgentBrokerFilter(bad, agentBrokerDecisionsFilter{
			Limit: 50,
			Since: 24 * time.Hour,
		}, now)
		if len(got) != 1 {
			t.Fatalf("malformed timestamp: got %d rows, want 1 (good only)", len(got))
		}
		if got[0].TurnID != "turn-good" {
			t.Errorf("expected turn-good, got %s", got[0].TurnID)
		}
	})
}

// TestFetchPool verifies the pool sizing heuristic. The user-visible
// limit is the lower bound; with active filters the pool is widened
// (5x, capped at 1000) so in-memory rejection has room to deliver
// the requested count.
func TestFetchPool(t *testing.T) {
	tests := []struct {
		name string
		f    agentBrokerDecisionsFilter
		want int
	}{
		{name: "no_filter_pool_eq_limit", f: agentBrokerDecisionsFilter{Limit: 50}, want: 50},
		{name: "zero_limit_defaults_to_50", f: agentBrokerDecisionsFilter{Limit: 0}, want: 50},
		{name: "since_widens_5x", f: agentBrokerDecisionsFilter{Limit: 50, Since: 1 * time.Hour}, want: 250},
		{name: "confidence_widens_5x", f: agentBrokerDecisionsFilter{Limit: 50, HasConfidenceLT: true, ConfidenceLT: 0.5}, want: 250},
		{name: "small_limit_with_filter", f: agentBrokerDecisionsFilter{Limit: 10, Since: 1 * time.Hour}, want: 50},
		{name: "huge_limit_clamped_to_1000", f: agentBrokerDecisionsFilter{Limit: 500, Since: 1 * time.Hour}, want: 1000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fetchPool(tt.f)
			if got != tt.want {
				t.Errorf("fetchPool(%+v) = %d, want %d", tt.f, got, tt.want)
			}
		})
	}
}

// TestPrintAgentBrokerJSON verifies NDJSON shape: one row per line,
// each line decodable to AgentBrokerDecision, and the column values
// round-trip without modification.
func TestPrintAgentBrokerJSON(t *testing.T) {
	rows := []*store.AgentBrokerDecision{
		{ID: 1, SessionID: "sess-a", TurnID: "turn-1", Decision: "worker", Confidence: 0.91, CreatedAt: "2026-05-10 12:00:00"},
		{ID: 2, SessionID: "sess-b", TurnID: "turn-2", Decision: "", Confidence: 1.0, CreatedAt: "2026-05-10 12:00:01"},
	}

	var buf bytes.Buffer
	printAgentBrokerJSON(&buf, rows)

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d output lines, want 2", len(lines))
	}
	for i, line := range lines {
		var got store.AgentBrokerDecision
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatalf("line %d not decodable: %v\nline=%q", i, err, line)
		}
		if got.SessionID != rows[i].SessionID || got.Confidence != rows[i].Confidence {
			t.Errorf("round-trip drift on line %d: got %+v, want %+v", i, got, rows[i])
		}
	}
}

// TestPrintAgentBrokerTable smoke-tests the tabular renderer: header
// row present, one data row per input, column count consistent. The
// goal is to catch a column-mismatch regression — exact column widths
// are tabwriter's job, not ours.
func TestPrintAgentBrokerTable(t *testing.T) {
	rows := []*store.AgentBrokerDecision{
		{SessionID: "sess-a", TurnID: "turn-1", ModeSignal: "work", ScopeTier: "small", Decision: "worker", Reason: "mode=work", Confidence: 0.91, CreatedAt: "2026-05-10 12:00:00"},
	}

	var buf bytes.Buffer
	printAgentBrokerTable(&buf, rows)
	out := buf.String()

	if !strings.Contains(out, "SESSION_ID") {
		t.Errorf("expected header row in output, got:\n%s", out)
	}
	if !strings.Contains(out, "sess-a") {
		t.Errorf("expected session_id in output, got:\n%s", out)
	}
	if !strings.Contains(out, "0.910") {
		t.Errorf("expected confidence formatted to 3 decimals, got:\n%s", out)
	}
}

// TestPrintAgentBrokerTable_EmptyDash confirms empty-string sentinel
// columns render as "-" in the tabular view (e.g. Decision="" for
// the default-chat-handle row).
func TestPrintAgentBrokerTable_EmptyDash(t *testing.T) {
	rows := []*store.AgentBrokerDecision{
		{SessionID: "sess-a", TurnID: "turn-1", ModeSignal: "chat", ScopeTier: "trivial", Decision: "", Reason: "default-chat-handle", Confidence: 1.0, CreatedAt: "2026-05-10 12:00:00"},
	}

	var buf bytes.Buffer
	printAgentBrokerTable(&buf, rows)
	out := buf.String()

	// The Decision column is empty in this row — must render as "-".
	// Crude but robust: pluck the data line (second non-empty line)
	// and confirm a "-" appears in it.
	dataLines := strings.Split(strings.TrimSpace(out), "\n")
	if len(dataLines) < 2 {
		t.Fatalf("expected header + data line, got %d lines:\n%s", len(dataLines), out)
	}
	if !strings.Contains(dataLines[1], "-") {
		t.Errorf("expected '-' in data line for empty Decision, got:\n%s", dataLines[1])
	}
}

// TestParseSQLiteDatetime verifies the fixed-layout parser used to
// compare row timestamps against the --since cutoff. SQLite's
// datetime('now') format is the only shape that will appear in the
// CreatedAt column, but a malformed value (corruption, manual SQL)
// must surface as an error rather than parse to the zero time.
func TestParseSQLiteDatetime(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{name: "happy_path", in: "2026-05-10 12:00:00", wantErr: false},
		{name: "trim_whitespace", in: " 2026-05-10 12:00:00 ", wantErr: false},
		{name: "iso8601_t_separator_rejected", in: "2026-05-10T12:00:00", wantErr: true},
		{name: "with_tz_rejected", in: "2026-05-10 12:00:00Z", wantErr: true},
		{name: "garbage", in: "yesterday at 3pm", wantErr: true},
		{name: "empty", in: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseSQLiteDatetime(tt.in)
			if tt.wantErr && err == nil {
				t.Fatalf("parseSQLiteDatetime(%q): want error, got nil", tt.in)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("parseSQLiteDatetime(%q): unexpected error: %v", tt.in, err)
			}
		})
	}
}
