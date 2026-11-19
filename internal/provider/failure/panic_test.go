package failure

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/core/panics"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
)

func TestRecoverStreamSendsFatalError(t *testing.T) {
	out := make(chan llmtypes.StreamEvent, 1)
	guard := panics.New(panics.Config{Reporter: panics.ReporterFunc(func(panics.Record) {})})
	done := make(chan struct{})
	guard.Go("test", func() {
		defer close(done)
		defer close(out)
		defer RecoverStream(context.Background(), guard, "test stream", out)
		panic("sk-ant-secretvalue0123456789abcdef")
	}, nil)
	<-done
	ev, ok := <-out
	if !ok || ev.Type != llmtypes.EventError {
		t.Fatalf("event %+v", ev)
	}
	err := FromEvent(ev)
	var classified *Error
	if !errors.As(err, &classified) || classified.Class != Fatal || !strings.Contains(err.Error(), "internal error (ref pnc_") || strings.Contains(err.Error(), "secretvalue") {
		t.Fatalf("error %v", err)
	}
}
