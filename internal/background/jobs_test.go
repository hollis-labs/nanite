package background

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/core/panics"
	"github.com/hollis-labs/libs/security/redact"
	"github.com/hollis-labs/nanite/internal/store"
)

func TestPanickingJobFailsWithRef(t *testing.T) {
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), ".nanite"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Now().UTC()
	conversation := store.Conversation{ID: store.NewID(now), Role: "chat", Status: "active", CreatedAt: now, UpdatedAt: now}
	if err := db.CreateConversation(t.Context(), conversation); err != nil {
		t.Fatal(err)
	}
	scrub, err := redact.New()
	if err != nil {
		t.Fatal(err)
	}
	notices := make(chan store.Notice, 1)
	var reported []panics.Record
	guard := panics.New(panics.Config{Reporter: panics.ReporterFunc(func(r panics.Record) { reported = append(reported, r) }), Redact: scrub.Text})
	m := New(db, scrub, func(_ store.Job, n store.Notice) { notices <- n }, slog.New(slog.DiscardHandler), guard)
	t.Cleanup(m.Close)

	job, err := m.Start(t.Context(), store.Job{ID: store.NewID(now), ConversationID: conversation.ID, Kind: "subagent", Spec: "{}"},
		func(context.Context, io.Writer) (Task, error) {
			return Task{Wait: func() Outcome { panic("job exploded") }}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case n := <-notices:
		if !strings.Contains(n.Body, "failed") {
			t.Fatalf("notice %+v", n)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("panicking job never completed")
	}
	got, err := db.Job(t.Context(), conversation.ID, "subagent", job.ID)
	if err != nil || got.Status != "failed" {
		t.Fatalf("job %+v, %v", got, err)
	}
	if len(reported) != 1 || reported[0].Attrs["job"] != job.ID {
		t.Fatalf("reported %+v", reported)
	}
}

func TestPanickingJobStartIsAnError(t *testing.T) {
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), ".nanite"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	scrub, _ := redact.New()
	m := New(db, scrub, func(store.Job, store.Notice) {}, slog.New(slog.DiscardHandler), panics.New(panics.Config{Reporter: panics.ReporterFunc(func(panics.Record) {})}))
	t.Cleanup(m.Close)
	_, err = m.Start(t.Context(), store.Job{ID: store.NewID(time.Now()), ConversationID: "c", Kind: "shell", Spec: "{}"},
		func(context.Context, io.Writer) (Task, error) { panic("start exploded") })
	if _, ok := panics.Ref(err); !ok {
		t.Fatalf("start error %v", err)
	}
}

// A job Close stops is cancelled with the shutdown reason, so whoever
// started it can tell a restart from a cancellation it asked for.
func TestCloseMarksJobsStoppedByShutdown(t *testing.T) {
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), ".nanite"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Now().UTC()
	conversation := store.Conversation{ID: store.NewID(now), Role: "chat", Status: "active", CreatedAt: now, UpdatedAt: now}
	if err := db.CreateConversation(t.Context(), conversation); err != nil {
		t.Fatal(err)
	}
	scrub, err := redact.New()
	if err != nil {
		t.Fatal(err)
	}
	m := New(db, scrub, func(store.Job, store.Notice) {}, slog.New(slog.DiscardHandler), panics.New(panics.Config{Redact: scrub.Text}))
	started := make(chan struct{})
	job, err := m.Start(t.Context(), store.Job{ID: store.NewID(now), ConversationID: conversation.ID, Kind: "subagent", Spec: "{}"},
		func(ctx context.Context, _ io.Writer) (Task, error) {
			return Task{Wait: func() Outcome { close(started); <-ctx.Done(); return Outcome{Err: ctx.Err()} }}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	m.Close()
	got, err := db.Job(t.Context(), conversation.ID, "subagent", job.ID)
	if err != nil || got.Status != "cancelled" || got.Reason != store.JobReasonShutdown {
		t.Fatalf("job %+v, %v", got, err)
	}
}
