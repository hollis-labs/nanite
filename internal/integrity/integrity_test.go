package integrity_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/integrity"
	"github.com/hollis-labs/nanite/internal/transcript"
)

var update = flag.Bool("update", false, "rewrite testdata fixtures and golden findings")

const segment = "00000000000000000001.jsonl"

// replayFixture writes raw as the conversation's only segment and replays it
// the way recovery does.
func replayFixture(t *testing.T, raw []byte) ([]transcript.Entry, error) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, fixtureConversation)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, segment), raw, 0o600); err != nil { // #nosec G703 -- A fixed segment name under the test's temporary directory.
		t.Fatal(err)
	}
	return transcript.Replay(root, fixtureConversation, transcript.Cursor{})
}

// golden is the comparable view of a finding: everything but the cursor
// hashes, which TestCursorRanges checks against the entries instead.
type golden struct {
	Code       integrity.Code     `json:"code"`
	Severity   integrity.Severity `json:"severity"`
	First      int                `json:"first"`
	Last       int                `json:"last"`
	AutoRepair bool               `json:"auto_repair,omitempty"`
	Repair     integrity.Repair   `json:"repair,omitempty"`
	Evidence   map[string]string  `json:"evidence,omitempty"`
}

func view(r integrity.Report) []golden {
	out := []golden{}
	for _, f := range r.Findings {
		out = append(out, golden{f.Code, f.Severity, f.Records.First, f.Records.Last, f.AutoRepair, f.Repair, f.Evidence})
	}
	return out
}

func validateFixture(t *testing.T, raw []byte) integrity.Report {
	t.Helper()
	entries, err := replayFixture(t, raw)
	return integrity.Validate(integrity.Input{ConversationID: fixtureConversation, Entries: entries, ReplayErr: err})
}

func TestFixtures(t *testing.T) {
	for _, fx := range fixtures {
		t.Run(fx.name, func(t *testing.T) {
			raw := fx.build().bytes()
			path := filepath.Join("testdata", fx.name+".jsonl")
			if *update {
				if err := os.WriteFile(path, raw, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			stored, err := os.ReadFile(path) // #nosec G304 -- A fixture under testdata named by the fixture table.
			if err != nil {
				t.Fatalf("%v (run go test ./internal/integrity -update)", err)
			}
			if !bytes.Equal(stored, raw) {
				t.Fatalf("%s is out of date with its builder (run go test ./internal/integrity -update)", path)
			}
			got, err := json.MarshalIndent(view(validateFixture(t, stored)), "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, '\n')
			goldenPath := filepath.Join("testdata", fx.name+".golden.json")
			if *update {
				if err := os.WriteFile(goldenPath, got, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(goldenPath) // #nosec G304 -- A golden file under testdata named by the fixture table.
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("findings differ from %s\n got: %s\nwant: %s", goldenPath, got, want)
			}
		})
	}
}

// TestFixtureClassification states each fixture's intent independently of
// the golden files, so regenerating them cannot quietly accept a regression.
func TestFixtureClassification(t *testing.T) {
	expect := map[string]struct {
		worst integrity.Severity
		codes []integrity.Code // must all be present
	}{
		"clean":                             {integrity.Info, nil},
		"clean_closing_note":                {integrity.Info, nil},
		"clean_compaction_kept_tool_step":   {integrity.Info, nil},
		"clean_discarded":                   {integrity.Info, nil},
		"clean_resumed":                     {integrity.Info, nil},
		"clean_notice_and_unknown_sideband": {integrity.Info, []integrity.Code{integrity.CodeUnknownSideband}},
		"interrupted_running":               {integrity.Recoverable, []integrity.Code{integrity.CodeInterruptedTurn, integrity.CodeToolUseWithoutResult}},
		"interrupted_marked":                {integrity.Recoverable, []integrity.Code{integrity.CodeInterruptedTurn, integrity.CodeToolUseWithoutResult}},
		"interrupted_before_user_message":   {integrity.Recoverable, []integrity.Code{integrity.CodeInterruptedTurn, integrity.CodeTurnMissingUserMessage}},
		"torn_tail":                         {integrity.Unsafe, []integrity.Code{integrity.CodeTornTail, integrity.CodeInterruptedTurn}},
		"tool_use_without_result_finished":  {integrity.Unsafe, []integrity.Code{integrity.CodeToolUseWithoutResult}},
		"tool_use_superseded":               {integrity.Unsafe, []integrity.Code{integrity.CodeToolUseWithoutResult}},
		"orphan_tool_result":                {integrity.Unsafe, []integrity.Code{integrity.CodeOrphanToolResult}},
		"orphan_tool_result_cross_turn":     {integrity.Unsafe, []integrity.Code{integrity.CodeOrphanToolResult}},
		"duplicate_tool_result":             {integrity.Unsafe, []integrity.Code{integrity.CodeDuplicateToolResult}},
		"duplicate_turn_outcome":            {integrity.Warning, []integrity.Code{integrity.CodeDuplicateTurnOutcome}},
		"conflicting_turn_outcome":          {integrity.Unsafe, []integrity.Code{integrity.CodeConflictingTurnOutcome}},
		"illegal_second_owner_message":      {integrity.Unsafe, []integrity.Code{integrity.CodeIllegalRoleOrdering}},
		"illegal_closing_while_pending":     {integrity.Unsafe, []integrity.Code{integrity.CodeIllegalRoleOrdering}},
		"illegal_message_while_interrupted": {integrity.Unsafe, []integrity.Code{integrity.CodeIllegalRoleOrdering, integrity.CodeInterruptedTurn}},
		"message_after_outcome":             {integrity.Unsafe, []integrity.Code{integrity.CodeMessageAfterOutcome}},
		"message_unknown_turn":              {integrity.Unsafe, []integrity.Code{integrity.CodeUnknownTurn, integrity.CodeMessageWithoutTurn}},
		"overlapping_turns":                 {integrity.Unsafe, []integrity.Code{integrity.CodeOverlappingTurns, integrity.CodeInterruptedTurn}},
		"turn_opens_without_user_message":   {integrity.Unsafe, []integrity.Code{integrity.CodeTurnMissingUserMessage}},
		"illegal_turn_transitions":          {integrity.Unsafe, []integrity.Code{integrity.CodeIllegalTurnTransition, integrity.CodeUnknownTurn}},
		"unknown_record_type":               {integrity.Unsafe, []integrity.Code{integrity.CodeUnknownRecordType}},
		"compaction_unknown_message":        {integrity.Unsafe, []integrity.Code{integrity.CodeCompactionUnknownMessage}},
		"compaction_splits_tool_step":       {integrity.Unsafe, []integrity.Code{integrity.CodeCompactionSplitsToolStep}},
		"session_mismatch":                  {integrity.Unsafe, []integrity.Code{integrity.CodeSessionMismatch}},
		"duplicate_message_uuid":            {integrity.Unsafe, []integrity.Code{integrity.CodeDuplicateRecordID}},
		"invalid_message_content":           {integrity.Unsafe, []integrity.Code{integrity.CodeInvalidRecord}},
		"missing_conversation_record":       {integrity.Unsafe, []integrity.Code{integrity.CodeConversationMeta}},
		"clean_reused_tool_ids":             {integrity.Info, nil},
		"duplicate_call_id_in_step":         {integrity.Unsafe, []integrity.Code{integrity.CodeDuplicateRecordID}},
		"compaction_unknown_turn":           {integrity.Unsafe, []integrity.Code{integrity.CodeUnknownTurn}},
		"payload_wrong_type":                {integrity.Unsafe, []integrity.Code{integrity.CodeInvalidRecord}},
		"bad_timestamp":                     {integrity.Unsafe, []integrity.Code{integrity.CodeInvalidRecord}},
		"unreadable_block":                  {integrity.Unsafe, []integrity.Code{integrity.CodeInvalidRecord}},
		"interrupted_reused_tool_id":        {integrity.Unsafe, []integrity.Code{integrity.CodeToolUseWithoutResult, integrity.CodeInterruptedTurn}},
		"mixed": {integrity.Unsafe, []integrity.Code{integrity.CodeOrphanToolResult, integrity.CodeConflictingTurnOutcome,
			integrity.CodeToolUseWithoutResult, integrity.CodeUnknownSideband, integrity.CodeInterruptedTurn, integrity.CodeTornTail}},
	}
	for _, fx := range fixtures {
		t.Run(fx.name, func(t *testing.T) {
			want, ok := expect[fx.name]
			if !ok {
				t.Fatal("fixture has no stated classification")
			}
			report := validateFixture(t, fx.build().bytes())
			if got := report.Worst(); got != want.worst {
				t.Fatalf("worst = %s, want %s: %v", got, want.worst, report.Findings)
			}
			if report.Safe() != (want.worst < integrity.Unsafe) {
				t.Fatalf("Safe() = %v", report.Safe())
			}
			present := map[integrity.Code]bool{}
			for _, f := range report.Findings {
				present[f.Code] = true
			}
			for _, code := range want.codes {
				if !present[code] {
					t.Errorf("missing %s in %v", code, report.Findings)
				}
			}
			if strings.HasPrefix(fx.name, "clean") {
				for _, f := range report.Findings {
					if f.Severity > integrity.Info {
						t.Errorf("clean fixture has %v", f)
					}
				}
			}
		})
	}
}

func TestInterruptionIsRecoverableNotCorrupt(t *testing.T) {
	for _, name := range []string{"interrupted_running", "interrupted_marked", "interrupted_before_user_message"} {
		report := validateFixture(t, build(t, name).bytes())
		if !report.Safe() || len(report.Unsafe()) != 0 {
			t.Fatalf("%s: interruption reported unsafe: %v", name, report.Findings)
		}
		for _, f := range report.Findings {
			if f.Severity != integrity.Recoverable || !f.AutoRepair || f.Repair != integrity.RepairResume {
				t.Fatalf("%s: %v is not an auto-repairable recovery", name, f)
			}
		}
	}
}

func build(t *testing.T, name string) *fixture {
	t.Helper()
	for _, fx := range fixtures {
		if fx.name == name {
			return fx.build()
		}
	}
	t.Fatalf("no fixture %s", name)
	return nil
}

func TestDeterministicRepeatedReplay(t *testing.T) {
	for _, fx := range fixtures {
		raw := fx.build().bytes()
		first := validateFixture(t, raw)
		for range 10 {
			if again := validateFixture(t, raw); !reflect.DeepEqual(first, again) {
				t.Fatalf("%s: validation differs between replays:\n%v\n%v", fx.name, first, again)
			}
		}
	}
}

func TestCursorRanges(t *testing.T) {
	for _, fx := range fixtures {
		entries, err := replayFixture(t, fx.build().bytes())
		report := integrity.Validate(integrity.Input{ConversationID: fixtureConversation, Entries: entries, ReplayErr: err})
		for _, f := range report.Findings {
			r := f.Records
			if r.First < 0 {
				if r.Last != -1 {
					t.Fatalf("%s: %v has a partial range", fx.name, f)
				}
				continue
			}
			if r.Last < r.First || r.Last >= len(entries) {
				t.Fatalf("%s: %v has an invalid range", fx.name, f)
			}
			if r.FirstCursor != entries[r.First].Cursor || r.LastCursor != entries[r.Last].Cursor {
				t.Fatalf("%s: %v cursors do not match its records", fx.name, f)
			}
		}
	}
}

func TestTornTailRangeIsAfterLastCommittedRecord(t *testing.T) {
	entries, err := replayFixture(t, build(t, "torn_tail").bytes())
	if !errors.Is(err, transcript.ErrTorn) || len(entries) == 0 {
		t.Fatalf("replay = %d entries, %v", len(entries), err)
	}
	report := integrity.Validate(integrity.Input{ConversationID: fixtureConversation, Entries: entries, ReplayErr: err})
	last := report.Findings[len(report.Findings)-1]
	if last.Code != integrity.CodeTornTail || last.AutoRepair || last.Records.FirstCursor != entries[len(entries)-1].Cursor {
		t.Fatalf("torn tail finding = %+v", last)
	}
}

func TestEvidenceCarriesNoContent(t *testing.T) {
	for _, fx := range fixtures {
		raw, err := json.Marshal(validateFixture(t, fx.build().bytes()))
		if err != nil {
			t.Fatal(err)
		}
		for _, content := range []string{"hello", "The turn reached its limit", "plain string", `"summary"`, "again"} {
			if bytes.Contains(raw, []byte(content)) {
				t.Fatalf("%s: report carries content %q: %s", fx.name, content, raw)
			}
		}
	}
}

func TestHistoryGap(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, fixtureConversation)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	raw := build(t, "clean").bytes()
	for _, name := range []string{segment, "00000000000000000003.jsonl"} {
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := transcript.Replay(root, fixtureConversation, transcript.Cursor{})
	if !errors.Is(err, transcript.ErrGap) {
		t.Fatalf("replay error = %v", err)
	}
	index := integrity.Project(fixtureConversation, nil)
	report := integrity.Validate(integrity.Input{ConversationID: fixtureConversation, Entries: entries, ReplayErr: err, Index: &index})
	if len(report.Findings) != 1 || report.Findings[0].Code != integrity.CodeHistoryGap || report.Safe() {
		t.Fatalf("findings = %v", report.Findings)
	}
}

func TestOtherReplayErrors(t *testing.T) {
	for _, err := range []error{transcript.ErrInvalid, errors.New("disk error")} {
		report := integrity.Validate(integrity.Input{ReplayErr: err})
		if len(report.Findings) != 1 || report.Findings[0].Code != integrity.CodeInvalidRecord || report.Safe() {
			t.Fatalf("%v: findings = %v", err, report.Findings)
		}
	}
	if report := integrity.Validate(integrity.Input{}); len(report.Findings) != 0 || !report.Safe() {
		t.Fatalf("empty transcript: %v", report.Findings)
	}
}

func TestReportIsBounded(t *testing.T) {
	f := newFixture().start("t1").owner("t1")
	for i := range integrity.MaxFindings + 10 {
		f.results(fmt.Sprintf("r%d", i), "t1", "ghost")
	}
	report := validateFixture(t, f.bytes())
	if len(report.Findings) != integrity.MaxFindings || !report.Truncated {
		t.Fatalf("findings = %d, truncated = %v", len(report.Findings), report.Truncated)
	}
}

// Many calls under many compactions that split them must not build every
// finding before the bound applies.
func TestSplitCompactionsAreBounded(t *testing.T) {
	const n = 300
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("c%d", i)
	}
	f := newFixture().start("t1").owner("t1").calls("a1", "t1", ids...)
	for i := range n {
		f.compaction(fmt.Sprintf("k%d", i), "t1", "a1")
	}
	f.results("r1", "t1", ids...).say("a2", "t1").finish("t1", "completed")
	report := validateFixture(t, f.bytes())
	if len(report.Findings) != integrity.MaxFindings || !report.Truncated || report.Worst() != integrity.Unsafe {
		t.Fatalf("findings = %d, truncated = %v, worst = %s", len(report.Findings), report.Truncated, report.Worst())
	}
}

// A finding the bound leaves out still counts: an unsafe one after many
// recoverable ones keeps the report unsafe.
func TestTruncationKeepsTheWorstSeverity(t *testing.T) {
	ids := make([]string, integrity.MaxFindings+5)
	for i := range ids {
		ids[i] = fmt.Sprintf("c%d", i)
	}
	entries, err := replayFixture(t, newFixture().start("t1").owner("t1").calls("a1", "t1", ids...).bytes())
	if err != nil {
		t.Fatal(err)
	}
	index := integrity.Project(fixtureConversation, entries)
	index.Turns[0].Status = "completed" // an index disagreement, found after the calls
	report := integrity.Validate(integrity.Input{ConversationID: fixtureConversation, Entries: entries, Index: &index})
	if !report.Truncated || report.Safe() || report.Worst() != integrity.Unsafe || len(report.Unsafe()) != 0 {
		t.Fatalf("truncated = %v, safe = %v, worst = %s, unsafe kept = %d", report.Truncated, report.Safe(), report.Worst(), len(report.Unsafe()))
	}
}

func TestSeverityJSON(t *testing.T) {
	raw, err := json.Marshal(integrity.Finding{Code: integrity.CodeTornTail, Severity: integrity.Unsafe})
	if err != nil || !bytes.Contains(raw, []byte(`"severity":"unsafe"`)) {
		t.Fatalf("%s %v", raw, err)
	}
	if integrity.Severity(9).String() != "severity(9)" {
		t.Fatal(integrity.Severity(9).String())
	}
}

// Resume and Discard are not offered as automatic repairs on a transcript
// with an unsafe finding of its own: they would fail or write onto it.
func TestResumeRepairOnlyForASoundTranscript(t *testing.T) {
	for _, name := range []string{"mixed", "interrupted_reused_tool_id", "torn_tail"} {
		for _, f := range validateFixture(t, build(t, name).bytes()).Findings {
			if f.AutoRepair || f.Repair != "" {
				t.Fatalf("%s: %+v offers a repair on an unsound transcript", name, f)
			}
		}
	}
}
