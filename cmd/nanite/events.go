package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents"
)

type eventStream struct {
	mu  sync.Mutex
	out io.Writer
}

func (s *eventStream) Write(_ context.Context, event runtimeevents.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return json.NewEncoder(s.out).Encode(event)
}

func cliEvents(dir, path string, stdout io.Writer) (runtimeevents.Sink, func() error, error) {
	if path == "-" {
		return &eventStream{out: stdout}, func() error { return nil }, nil
	}
	if path == "" {
		path = filepath.Join(dir, ".nanite", "events.jsonl")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, nil, err
	}
	modeErr := f.Chmod(0600)
	closeErr := f.Close()
	if modeErr != nil {
		return nil, nil, modeErr
	}
	if closeErr != nil {
		return nil, nil, closeErr
	}
	sink, err := runtimeevents.OpenFileSink(path)
	if err != nil {
		return nil, nil, err
	}
	return sink, sink.Close, nil
}
