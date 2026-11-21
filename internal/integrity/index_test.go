package integrity_test

import (
	"testing"

	"github.com/hollis-labs/nanite/internal/integrity"
	"github.com/hollis-labs/nanite/internal/transcript"
)

func TestIndexComparison(t *testing.T) {
	raw := newFixture().toolTurn("t1").start("t2").owner("t2").
		calls("a1-t2", "t2", "c1").results("r1-t2", "t2", "c1").compaction("k1", "t2", "r1-t2").
		say("a2-t2", "t2").finish("t2", "completed").bytes()
	entries, err := replayFixture(t, raw)
	if err != nil {
		t.Fatal(err)
	}
	clean := func() integrity.Index { return integrity.Project(fixtureConversation, entries) }
	cases := []struct {
		name   string
		mutate func(*integrity.Index)
		code   integrity.Code
		sev    integrity.Severity
		repair integrity.Repair
	}{
		{"matches", func(*integrity.Index) {}, "", integrity.Info, ""},
		{"never projected", func(i *integrity.Index) { *i = integrity.Index{} }, integrity.CodeIndexBehind, integrity.Recoverable, integrity.RepairReplayIndex},
		{"behind", func(i *integrity.Index) {
			*i = integrity.Project(fixtureConversation, entries[:5])
		}, integrity.CodeIndexBehind, integrity.Recoverable, integrity.RepairReplayIndex},
		{"unknown cursor", func(i *integrity.Index) {
			i.Cursor = &transcript.Cursor{Segment: 9, Offset: 1, Hash: "x"}
		}, integrity.CodeIndexDisagreement, integrity.Unsafe, integrity.RepairRebuildIndex},
		{"missing turn", func(i *integrity.Index) { i.Turns = i.Turns[1:] }, integrity.CodeIndexDisagreement, integrity.Unsafe, integrity.RepairRebuildIndex},
		{"extra turn", func(i *integrity.Index) {
			i.Turns = append(i.Turns, integrity.IndexTurn{ID: "t9", Status: "running"})
		}, integrity.CodeIndexDisagreement, integrity.Unsafe, integrity.RepairRebuildIndex},
		{"turn status", func(i *integrity.Index) { i.Turns[1].Status = "running" }, integrity.CodeIndexDisagreement, integrity.Unsafe, integrity.RepairRebuildIndex},
		{"turn user message", func(i *integrity.Index) { i.Turns[1].UserMessageID = "x" }, integrity.CodeIndexDisagreement, integrity.Unsafe, integrity.RepairRebuildIndex},
		{"missing message", func(i *integrity.Index) { i.Messages = i.Messages[1:] }, integrity.CodeIndexDisagreement, integrity.Unsafe, integrity.RepairRebuildIndex},
		{"extra message", func(i *integrity.Index) {
			i.Messages = append(i.Messages, integrity.IndexMessage{ID: "m9", TurnID: "t2", Role: "assistant"})
		}, integrity.CodeIndexDisagreement, integrity.Unsafe, integrity.RepairRebuildIndex},
		{"message role", func(i *integrity.Index) { i.Messages[2].Role = "user" }, integrity.CodeIndexDisagreement, integrity.Unsafe, integrity.RepairRebuildIndex},
		{"message order", func(i *integrity.Index) {
			i.Messages[0], i.Messages[1] = i.Messages[1], i.Messages[0]
		}, integrity.CodeIndexDisagreement, integrity.Unsafe, integrity.RepairRebuildIndex},
		{"missing compaction", func(i *integrity.Index) { i.Compactions = nil }, integrity.CodeIndexDisagreement, integrity.Unsafe, integrity.RepairRebuildIndex},
		{"compaction through", func(i *integrity.Index) { i.Compactions[0].ThroughMessageID = "a1-t2" }, integrity.CodeIndexDisagreement, integrity.Unsafe, integrity.RepairRebuildIndex},
		{"extra compaction", func(i *integrity.Index) {
			i.Compactions = append(i.Compactions, integrity.IndexCompaction{ID: "k9"})
		}, integrity.CodeIndexDisagreement, integrity.Unsafe, integrity.RepairRebuildIndex},
		{"notice delivered to an unknown turn", func(i *integrity.Index) {
			i.Notices = []integrity.IndexNotice{{ID: "n1", DeliveredTurnID: "t1"}, {ID: "n2"}, {ID: "n3", DeliveredTurnID: "gone"}}
		}, integrity.CodeNoticeUnknownTurn, integrity.Warning, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			index := clean()
			tc.mutate(&index)
			report := integrity.Validate(integrity.Input{ConversationID: fixtureConversation, Entries: entries, Index: &index})
			if tc.code == "" {
				if len(report.Findings) != 0 {
					t.Fatalf("findings = %v", report.Findings)
				}
				return
			}
			if len(report.Findings) != 1 {
				t.Fatalf("want exactly one %s, got %v", tc.code, report.Findings)
			}
			f := report.Findings[0]
			if f.Code != tc.code || f.Severity != tc.sev || f.Repair != tc.repair || f.AutoRepair != (tc.repair != "") {
				t.Fatalf("finding = %+v", f)
			}
		})
	}
}

func TestIndexOfTornTranscriptComparesCommittedPrefix(t *testing.T) {
	entries, err := replayFixture(t, build(t, "torn_tail").bytes())
	index := integrity.Project(fixtureConversation, entries)
	report := integrity.Validate(integrity.Input{ConversationID: fixtureConversation, Entries: entries, ReplayErr: err, Index: &index})
	for _, f := range report.Findings {
		if f.Code == integrity.CodeIndexBehind || f.Code == integrity.CodeIndexDisagreement {
			t.Fatalf("index of the committed prefix reported: %v", f)
		}
	}
}

func TestProjectMirrorsProjectionRules(t *testing.T) {
	entries, err := replayFixture(t, newFixture().start("t1").owner("t1").calls("a1", "t1", "c1").
		interrupt("t1").interrupt("t1").resume("t1").synthetic("r1", "t1", "c1").say("a1", "t1").
		finish("t1", "completed").finish("t1", "failed").compaction("k1", "t1", "r1").compaction("k1", "t1", "a1").
		side("nanite.compaction", "k2", map[string]any{"Before": 1}).bytes())
	if err != nil {
		t.Fatal(err)
	}
	got := integrity.Project(fixtureConversation, entries)
	if len(got.Turns) != 1 || got.Turns[0].Status != "failed" || got.Turns[0].UserMessageID != "u-t1" {
		t.Fatalf("turns = %+v", got.Turns) // a finish sets the status it carries, the last one standing
	}
	roles := ""
	for _, m := range got.Messages {
		roles += m.Role + " "
	}
	if roles != "user assistant tool " {
		t.Fatalf("messages = %+v", got.Messages) // the duplicate a1 is dropped
	}
	if len(got.Compactions) != 1 || got.Compactions[0].ThroughMessageID != "r1" {
		t.Fatalf("compactions = %+v", got.Compactions) // first write wins; telemetry is not projected
	}
	if got.Cursor == nil || *got.Cursor != entries[len(entries)-1].Cursor {
		t.Fatalf("cursor = %v", got.Cursor)
	}
}

func TestHold(t *testing.T) {
	interrupted, err := replayFixture(t, build(t, "interrupted_marked").bytes())
	if err != nil {
		t.Fatal(err)
	}
	clean, err := replayFixture(t, build(t, "clean").bytes())
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		entries []transcript.Entry
		hold    *integrity.Hold
		stale   bool
	}{
		{"no hold", clean, nil, false},
		{"empty hold", clean, &integrity.Hold{}, false},
		{"hold on the interrupted turn", interrupted, &integrity.Hold{TurnID: "t1"}, false},
		{"hold on a finished turn", clean, &integrity.Hold{TurnID: "t1"}, true},
		{"hold on an unknown turn", interrupted, &integrity.Hold{TurnID: "gone"}, true},
	}
	for _, tc := range cases {
		report := integrity.Validate(integrity.Input{ConversationID: fixtureConversation, Entries: tc.entries, Hold: tc.hold})
		stale := false
		for _, f := range report.Findings {
			if f.Code == integrity.CodeStaleInterruptHold {
				stale = true
				if f.Severity != integrity.Warning || f.Repair != integrity.RepairClearHold {
					t.Fatalf("%s: %+v", tc.name, f)
				}
			}
		}
		if stale != tc.stale {
			t.Fatalf("%s: stale = %v: %v", tc.name, stale, report.Findings)
		}
	}
}
