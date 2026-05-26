//go:build devmode

package muxproxy

import (
	"strings"

	"github.com/hollis-labs/tether/pkg/claudestream"
)

// parseAll decodes every claudestream.Event produced by a single
// wire-format JSON payload. The claude "result" event fans out into
// {KindUsage, KindDone}, and "assistant" events with text+tool_use
// blocks fan out similarly — draining the scanner fully is the only
// correct read of the payload.
//
// Returns nil for empty input, filtered event types (rate_limit),
// or parse errors.
func parseAll(payload string) []claudestream.Event {
	if payload == "" {
		return nil
	}
	sc := claudestream.NewScanner(strings.NewReader(payload))
	var events []claudestream.Event
	for {
		ev, ok, err := sc.Next()
		if err != nil || !ok {
			break
		}
		events = append(events, ev)
	}
	return events
}

// parseOne returns the first event from a payload. Retained for
// callers that only want a single event (e.g. tests asserting the
// head of a payload). Production dispatch uses parseAll.
func parseOne(payload string) (claudestream.Event, bool) {
	events := parseAll(payload)
	if len(events) == 0 {
		return claudestream.Event{}, false
	}
	return events[0], true
}
