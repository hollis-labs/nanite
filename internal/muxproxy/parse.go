package muxproxy

import (
	"strings"

	"github.com/chrispian/agent-mux/pkg/claudestream"
)

// parseOne decodes a single claudestream wire-format JSON payload into
// a claudestream.Event. Returns ok=false for empty input or filtered
// event types (rate_limit, non-init system events).
func parseOne(payload string) (claudestream.Event, bool) {
	if payload == "" {
		return claudestream.Event{}, false
	}
	sc := claudestream.NewScanner(strings.NewReader(payload))
	ev, ok, err := sc.Next()
	if err != nil || !ok {
		return claudestream.Event{}, false
	}
	return ev, true
}
