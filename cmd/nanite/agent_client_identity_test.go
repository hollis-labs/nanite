package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	chatstream "github.com/hollis-labs/libs/ui-go/chatstream"
)

func TestAgentClientStreamRequiresRequestedRunAndContiguousCheckpoints(t *testing.T) {
	event := func(seq uint64, run string, verb chatstream.Verb) chatstream.Event {
		return chatstream.Event{V: "1", Time: time.Now().UTC(), Seq: seq, RunID: run, Verb: verb}
	}
	for _, scenario := range []struct {
		name   string
		events []chatstream.Event
	}{
		{"foreign-first-run", []chatstream.Event{event(1, "foreign", chatstream.VerbRunStart), event(2, "foreign", chatstream.VerbRunFinish)}},
		{"foreign-terminal", []chatstream.Event{event(1, "turn", chatstream.VerbRunStart), event(2, "foreign", chatstream.VerbRunFinish)}},
		{"jump-first", []chatstream.Event{event(50, "turn", chatstream.VerbRunStart), event(51, "turn", chatstream.VerbRunFinish)}},
		{"jump-terminal", []chatstream.Event{event(1, "turn", chatstream.VerbRunStart), event(3, "turn", chatstream.VerbRunFinish)}},
		{"duplicate-checkpoint", []chatstream.Event{event(1, "turn", chatstream.VerbRunStart), event(1, "turn", chatstream.VerbRunFinish)}},
		{"missing-start", []chatstream.Event{event(1, "turn", chatstream.VerbRunFinish)}},
		{"missing-run-id", []chatstream.Event{event(1, "", chatstream.VerbRunStart)}},
		{"empty-stream", nil},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				for _, ev := range scenario.events {
					data, _ := json.Marshal(ev)
					_, _ = fmt.Fprintf(w, "event: %s\nid: %d\ndata: %s\n\n", ev.Verb, ev.Seq, data)
				}
			}))
			defer server.Close()
			events, err := newAgentClient(server.URL).StreamEvents(t.Context(), "/api/agent/v1/sessions/view/turns/turn/events", "turn")
			if err != nil {
				t.Fatal(err)
			}
			failed := false
			for ev := range events {
				if ev.Type == "stream_end" {
					t.Fatal("invalid stream yielded success")
				}
				failed = failed || ev.Type == "error"
			}
			if !failed || calls.Load() != 1 {
				t.Fatal("invalid stream silently closed or retried", failed, calls.Load())
			}
		})
	}
}

func TestAgentClientStreamPathMustMatchAcceptedTurn(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer server.Close()
	_, err := newAgentClient(server.URL).StreamEvents(t.Context(), "/api/agent/v1/sessions/view/turns/other/events", "accepted")
	if err == nil || !strings.Contains(err.Error(), "accepted turn") || calls.Load() != 0 {
		t.Fatal(err, calls.Load())
	}
}
