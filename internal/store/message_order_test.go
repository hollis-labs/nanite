package store

import (
	"context"
	"testing"
)

// A client may submit the next turn the moment the previous one finishes, so
// the assistant reply and the next user message commonly land in the same
// second. Every path that writes a message must stamp a timestamp that sorts in
// insertion order inside that second (CW-20261011-0041): the user rows written
// by admission used RFC3339Nano ("...17.824Z") while CreateMessage used whole
// seconds ("...17Z"), and "." sorts before "Z", so the next user message sorted
// ahead of the answer it followed.
func TestMessageOrder_SameSecondAcrossWritePaths(t *testing.T) {
	ctx := context.Background()
	s := newSeededStore(t)
	sess := &Session{ID: "order-sess", Title: "order"}
	if err := s.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	want := []string{"user", "assistant", "user", "assistant", "user"}
	for i, role := range want {
		id := "m" + string(rune('0'+i))
		switch role {
		case "user":
			// The cognitive admission path writes the user row.
			if err := s.CreateCognitiveTurn(ctx, &Message{ID: id, SessionID: sess.ID, Role: "user", Content: "q"}, "turn-"+id, "{}"); err != nil {
				t.Fatalf("CreateCognitiveTurn %s: %v", id, err)
			}
		default:
			// The generation path writes the assistant row.
			if err := s.CreateMessage(ctx, &Message{ID: id, SessionID: sess.ID, Role: role, Content: "a"}); err != nil {
				t.Fatalf("CreateMessage %s: %v", id, err)
			}
		}
	}

	rows, err := s.DB.QueryContext(ctx, `SELECT role, CAST(created_at AS TEXT) FROM messages WHERE session_id = ? ORDER BY created_at, rowid`, sess.ID)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	var got []string
	var stamps []string
	for rows.Next() {
		var role, at string
		if err := rows.Scan(&role, &at); err != nil {
			t.Fatal(err)
		}
		got = append(got, role)
		stamps = append(stamps, at)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order by created_at,rowid = %v (stamps %v), want %v", got, stamps, want)
		}
	}
	// Fixed-width stamps are what make the lexical order the chronological one.
	// (CAST because the driver re-formats DATETIME columns it scans as times.)
	for i, at := range stamps {
		if len(at) != len(messageTimeLayout) {
			t.Fatalf("%s created_at %q is not fixed width (%d), want %d", got[i], at, len(at), len(messageTimeLayout))
		}
	}
}
