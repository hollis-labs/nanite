package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/mesh/core/runtimecontract"
)

// This fixture was reduced from harness/core's captured Claude Code
// providertest/fixtures/claude/print_tool_use.jsonl. IDs are already scrubbed;
// the native transcript wrapper uses Claude's type, uuid, parentUuid,
// sessionId, timestamp and message fields. The assertions below vendor the
// existing ingestion expectation: assistant tool_use is followed by a user
// tool_result with the matching ID, and usage remains on assistant messages.
func fixture(t *testing.T) []json.RawMessage {
	t.Helper()
	f, err := os.Open("testdata/claude_tool_turn.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var records []json.RawMessage
	s := bufio.NewScanner(f)
	for s.Scan() {
		records = append(records, append([]byte(nil), s.Bytes()...))
	}
	if err := s.Err(); err != nil {
		t.Fatal(err)
	}
	if len(records) != 5 {
		t.Fatalf("fixture records = %d", len(records))
	}
	return records
}

func TestClaudeCapturedShapeAndReplay(t *testing.T) {
	root := t.TempDir()
	var projected []Entry
	w, err := Open(root, "conversation-a", Options{Project: func(e Entry) error { projected = append(projected, e); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root, "conversation-a", Options{}); !errors.Is(err, ErrBusy) {
		t.Fatalf("second writer: %v", err)
	}
	records := fixture(t)
	var cursors []Cursor
	for _, record := range records {
		c, err := w.Append(record)
		if err != nil {
			t.Fatal(err)
		}
		cursors = append(cursors, c)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if len(projected) != len(records) {
		t.Fatalf("projected %d records", len(projected))
	}
	for i := 0; i <= len(records); i++ {
		var after Cursor
		if i > 0 {
			after = cursors[i-1]
		}
		entries, err := Replay(root, "conversation-a", after)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != len(records)-i {
			t.Fatalf("cursor %d: %d entries", i, len(entries))
		}
		for j, e := range entries {
			var want, got bytes.Buffer
			_ = json.Compact(&want, records[i+j])
			_ = json.Compact(&got, e.Record)
			if !bytes.Equal(want.Bytes(), got.Bytes()) {
				t.Fatalf("cursor %d record %d changed", i, j)
			}
		}
	}
	all, err := Replay(root, "conversation-a", Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	var callID string
	var matched bool
	for _, e := range all {
		var row struct {
			Type      string `json:"type"`
			SessionID string `json:"sessionId"`
			Message   struct {
				Role    string `json:"role"`
				Content []struct {
					Type      string `json:"type"`
					ID        string `json:"id"`
					ToolUseID string `json:"tool_use_id"`
				} `json:"content"`
				Usage json.RawMessage `json:"usage"`
			} `json:"message"`
		}
		if err := json.Unmarshal(e.Record, &row); err != nil {
			t.Fatal(err)
		}
		if row.SessionID == "" || row.Type != row.Message.Role {
			t.Fatalf("invalid Claude message envelope: %s", e.Record)
		}
		if row.Type == "assistant" && len(row.Message.Usage) == 0 {
			t.Fatal("assistant usage lost")
		}
		for _, block := range row.Message.Content {
			if block.Type == "tool_use" {
				callID = block.ID
			}
			if block.Type == "tool_result" && block.ToolUseID == callID && callID != "" {
				matched = true
			}
		}
	}
	if !matched {
		t.Fatal("Claude tool use/result link lost")
	}
	if _, err := Replay(root, "conversation-a", Cursor{Segment: cursors[1].Segment, Offset: cursors[1].Offset, Hash: "deadbeef"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid cursor: %v", err)
	}
	bad := cursors[1]
	bad.Hash = strings.Repeat("0", 64)
	if _, err := Replay(root, "conversation-a", bad); !errors.Is(err, ErrGap) {
		t.Fatalf("changed history: %v", err)
	}
}

func TestTornTailAndNoImplicitRepair(t *testing.T) {
	root := t.TempDir()
	w, err := Open(root, "conversation", Options{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := w.Append(fixture(t)[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Append(fixture(t)[1]); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	dir, _ := ConversationDir(root, "conversation")
	f, err := os.OpenFile(segmentPath(dir, 1), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString(`{"type":"assistant"`); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	entries, err := Replay(root, "conversation", Cursor{})
	if !errors.Is(err, ErrTorn) || len(entries) != 2 || entries[0].Cursor != first {
		t.Fatalf("replay torn prefix: %d entries, %v", len(entries), err)
	}
	suffix, err := Replay(root, "conversation", first)
	if !errors.Is(err, ErrTorn) || len(suffix) != 1 || suffix[0].Cursor != entries[1].Cursor {
		t.Fatalf("replay torn suffix: %d entries, %v", len(suffix), err)
	}
	if _, err := Open(root, "conversation", Options{}); !errors.Is(err, ErrTorn) {
		t.Fatalf("open torn: %v", err)
	}
}

func TestProjectionFailureIsCommittedAndGapDetected(t *testing.T) {
	root := t.TempDir()
	w, err := Open(root, "conversation", Options{Project: func(Entry) error { return errors.New("index down") }})
	if err != nil {
		t.Fatal(err)
	}
	c, err := w.Append(fixture(t)[0])
	if err == nil || c == (Cursor{}) {
		t.Fatalf("projection failure cursor=%+v err=%v", c, err)
	}
	_ = w.Close()
	entries, err := Replay(root, "conversation", Cursor{})
	if err != nil || len(entries) != 1 {
		t.Fatalf("committed transcript: %d %v", len(entries), err)
	}
	dir, _ := ConversationDir(root, "conversation")
	if err := os.Rename(segmentPath(dir, 1), filepath.Join(dir, "missing")); err != nil {
		t.Fatal(err)
	}
	if _, err := Replay(root, "conversation", c); !errors.Is(err, ErrGap) {
		t.Fatalf("missing segment: %v", err)
	}
}

func TestRotationAndChangedPrefix(t *testing.T) {
	root := t.TempDir()
	w, err := Open(root, "conversation", Options{SegmentBytes: 200})
	if err != nil {
		t.Fatal(err)
	}
	records := fixture(t)
	var cursors []Cursor
	for _, row := range records[:3] {
		c, err := w.Append(row)
		if err != nil {
			t.Fatal(err)
		}
		cursors = append(cursors, c)
	}
	_ = w.Close()
	if cursors[0].Segment == cursors[2].Segment {
		t.Fatal("rotation did not occur")
	}
	w, err = Open(root, "conversation", Options{SegmentBytes: 200})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Append(records[3]); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	entries, err := Replay(root, "conversation", cursors[1])
	if err != nil || len(entries) != 2 {
		t.Fatalf("rotated replay: %d %v", len(entries), err)
	}
	dir, _ := ConversationDir(root, "conversation")
	path := segmentPath(dir, 1)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte("msg_fixture0003"), []byte("msg_fixture0008"), 1)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Replay(root, "conversation", cursors[0]); !errors.Is(err, ErrGap) {
		t.Fatalf("changed prefix: %v", err)
	}
}

func TestRecordEncoders(t *testing.T) {
	var captured struct {
		Type      string          `json:"type"`
		UUID      string          `json:"uuid"`
		SessionID string          `json:"sessionId"`
		Message   json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(fixture(t)[1], &captured); err != nil {
		t.Fatal(err)
	}
	row, err := EncodeMessage(MessageRecord{Type: captured.Type, UUID: captured.UUID, SessionID: captured.SessionID, Timestamp: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Message: captured.Message})
	if err != nil || !bytes.Contains(row, []byte(`"tool_use"`)) || !bytes.Contains(row, []byte(`"usage"`)) {
		t.Fatalf("native message: %s %v", row, err)
	}
	if _, err := EncodeMessage(MessageRecord{Type: "user", UUID: captured.UUID, SessionID: captured.SessionID, Timestamp: time.Now(), Message: captured.Message}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("role mismatch: %v", err)
	}
	sideband, err := EncodeSideband(SidebandRecord{Type: "system", Subtype: "approval", UUID: "approval-1", SessionID: captured.SessionID, Timestamp: time.Now(), Payload: json.RawMessage(`{"decision_ref":"r1"}`)})
	if err != nil || !bytes.Contains(sideband, []byte(`"decision_ref"`)) {
		t.Fatalf("sideband: %s %v", sideband, err)
	}
	var result struct {
		Type      string          `json:"type"`
		UUID      string          `json:"uuid"`
		SessionID string          `json:"sessionId"`
		Message   json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(fixture(t)[2], &result); err != nil {
		t.Fatal(err)
	}
	ref := runtimecontract.Reference{ID: "receipt-1", Revision: "1", Digest: runtimecontract.Digest{Scheme: "sha256", Value: strings.Repeat("a", 64)}, IssuerRef: "fixture-host", OwnerID: "fixture-owner"}
	toolRow, err := EncodeMessage(MessageRecord{Type: result.Type, UUID: result.UUID, SessionID: result.SessionID, Timestamp: time.Now(), Message: result.Message, ResultRef: &ref})
	if err != nil || !bytes.Contains(toolRow, []byte(`"resultRef"`)) {
		t.Fatalf("result reference: %s %v", toolRow, err)
	}
	if _, err := EncodeMessage(MessageRecord{Type: captured.Type, UUID: captured.UUID, SessionID: captured.SessionID, Timestamp: time.Now(), Message: captured.Message, ResultRef: &ref}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("assistant result reference: %v", err)
	}
}
