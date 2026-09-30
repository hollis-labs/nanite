package service

import (
	"context"
	"errors"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestBookmarkServiceToggle(t *testing.T) {
	ctx := context.Background()
	st := newConfigTestStore(t)
	svc := NewBookmarkService(st)

	sess := &store.Session{}
	if err := st.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	msg := &store.Message{SessionID: sess.ID, Role: "user", Content: "keep"}
	if err := st.CreateMessage(ctx, msg); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	removed, b, err := svc.Toggle(ctx, msg.ID)
	if err != nil || removed || b.SessionID != sess.ID || b.MessageID != msg.ID {
		t.Fatalf("first toggle = %v, %+v, %v; want a created bookmark in the message's session", removed, b, err)
	}
	content, err := svc.MessageContent(ctx, b)
	if err != nil || content != "keep" {
		t.Fatalf("MessageContent = %q, %v", content, err)
	}
	removed, gone, err := svc.Toggle(ctx, msg.ID)
	if err != nil || !removed || gone.ID != b.ID {
		t.Fatalf("second toggle = %v, %+v, %v; want the same bookmark removed", removed, gone, err)
	}
	if _, _, err := svc.Toggle(ctx, "no-such-message"); !errors.Is(err, ErrBookmarkMessageNotFound) {
		t.Fatalf("unknown message err = %v, want ErrBookmarkMessageNotFound", err)
	}
}
