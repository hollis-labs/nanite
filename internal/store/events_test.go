package store

import (
	"context"
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
