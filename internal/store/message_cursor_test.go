package store

import (
	"context"
	"fmt"
	"testing"
)

// The database driver re-formats DATETIME columns it scans into strings
// (RFC3339Nano, trailing zeros trimmed), so a created_at read back as a
// cursor and bound into a comparison did not equal the stored text whenever a
// timestamp ended in a zero: the row compared as older than its own cursor and
// pages overlapped. Message queries now read the stored text (CAST AS TEXT)
// (CW-20261011-0041 follow-up).
func TestMessageCursorPaging_TrailingZeroTimestamps(t *testing.T) {
	ctx := context.Background()
	s := newSeededStore(t)
	sess := &Session{ID: "cursor-sess", Title: "cursor"}
	if err := s.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	// Fixed-width stamps whose fractional part ends in zeros: the case the
	// driver's formatting trimmed.
	stamps := []string{
		"2026-10-11T10:00:00.100000000Z", "2026-10-11T10:00:00.200000000Z", "2026-10-11T10:00:00.300000000Z",
		"2026-10-11T10:00:00.400000000Z", "2026-10-11T10:00:00.500000000Z", "2026-10-11T10:00:00.600000000Z",
	}
	for i, at := range stamps {
		if _, err := s.DB.ExecContext(ctx, `INSERT INTO messages(id,session_id,role,content,metadata,created_at) VALUES(?,?,'user','x','{}',?)`,
			fmt.Sprintf("m%d", i), sess.ID, at); err != nil {
			t.Fatal(err)
		}
	}

	first, err := s.ListCognitiveMessagePage(ctx, sess.ID, "", "", 3)
	if err != nil || len(first) != 3 || first[0].ID != "m3" || first[2].ID != "m5" {
		t.Fatalf("first page = %+v (%v), want m3..m5", first, err)
	}
	if first[0].CreatedAt != stamps[3] {
		t.Fatalf("CreatedAt = %q, want the stored text %q", first[0].CreatedAt, stamps[3])
	}
	second, err := s.ListCognitiveMessagePage(ctx, sess.ID, first[0].CreatedAt, first[0].ID, 3)
	if err != nil || len(second) != 3 || second[0].ID != "m0" || second[2].ID != "m2" {
		t.Fatalf("second page = %+v (%v), want m0..m2 and nothing from the first page", second, err)
	}

	page, err := s.ListMessagesAroundID(ctx, sess.ID, "m3", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range page.Messages {
		ids = append(ids, m.ID)
	}
	if len(ids) != 3 || ids[0] != "m2" || ids[1] != "m3" || ids[2] != "m4" {
		t.Fatalf("window around m3 = %v, want [m2 m3 m4]", ids)
	}
}
