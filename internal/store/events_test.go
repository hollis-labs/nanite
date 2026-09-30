package store

import (
	"context"
	"fmt"
	"testing"
)

func TestSessionWriteResultIDs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	s.LogEvent(ctx, "s1", "write_result_ids", "write_claim_guard", "kb_write", `{"tool":"kb_write","ids":["A1","B2"]}`)
	s.LogEvent(ctx, "s1", "write_result_ids", "write_claim_guard", "kb_write", `{"tool":"kb_write","ids":["B2","C3"]}`)
	s.LogEvent(ctx, "s1", "tool_call", "tool", "x", `{"ids":["NOPE"]}`)
	s.LogEvent(ctx, "s2", "write_result_ids", "write_claim_guard", "kb_write", `{"tool":"kb_write","ids":["OTHER"]}`)
	s.LogEvent(ctx, "s1", "write_result_ids", "write_claim_guard", "kb_write", `not json`)
	got, err := s.SessionWriteResultIDs(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || !got["A1"] || !got["B2"] || !got["C3"] || got["OTHER"] || got["NOPE"] {
		t.Errorf("ids = %v", got)
	}
	if empty, _ := s.SessionWriteResultIDs(ctx, "nobody"); len(empty) != 0 {
		t.Errorf("unknown session = %v", empty)
	}
}

// The read is bounded to the session's newest rows, so a long session cannot
// make it slow; the oldest ids fall out rather than the newest.
func TestSessionWriteResultIDsIsBounded(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for i := 0; i < maxWriteResultIDRows+5; i++ {
		s.LogEvent(ctx, "long", "write_result_ids", "write_claim_guard", "kb_write", fmt.Sprintf(`{"ids":["ID%04d"]}`, i))
	}
	got, err := s.SessionWriteResultIDs(ctx, "long")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != maxWriteResultIDRows || got["ID0000"] || !got[fmt.Sprintf("ID%04d", maxWriteResultIDRows+4)] {
		t.Errorf("got %d ids; oldest present=%v newest present=%v", len(got), got["ID0000"], got[fmt.Sprintf("ID%04d", maxWriteResultIDRows+4)])
	}
}
