package engine

import (
	"errors"
	"testing"

	"github.com/hollis-labs/nanite/internal/provider/fake"
)

// A conversation an integrity check has claimed is busy to every writer:
// a run, Discard, Record and notice withdrawal wait for the check rather
// than write a transcript it is reading.
func TestRunningCheckMakesTheConversationBusy(t *testing.T) {
	e, _, id := setupEngine(t, fake.New(fake.Text("ok")), "")
	if err := e.claimCheck(id); err != nil {
		t.Fatal(err)
	}
	if err := e.claimCheck(id); !errors.Is(err, ErrBusy) {
		t.Fatalf("a second check: %v", err)
	}
	if _, err := e.Send(t.Context(), id, Input{Text: "hi"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("send: %v", err)
	}
	if err := e.Discard(t.Context(), id); !errors.Is(err, ErrBusy) {
		t.Fatalf("discard: %v", err)
	}
	if err := e.Record(t.Context(), id, "asked", "answered"); !errors.Is(err, ErrBusy) {
		t.Fatalf("record: %v", err)
	}
	if err := e.WithdrawQueuedNotice(t.Context(), id, "n1"); !errors.Is(err, ErrBusy) {
		t.Fatalf("withdraw notice: %v", err)
	}
	if _, err := e.CheckIntegrity(t.Context(), id); !errors.Is(err, ErrBusy) {
		t.Fatalf("check: %v", err)
	}
	e.releaseCheck(id)
	run, err := e.Send(t.Context(), id, Input{Text: "hi"})
	if err != nil {
		t.Fatalf("send after the check: %v", err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
}
