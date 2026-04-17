package service

import (
	"errors"
	"strings"

	"github.com/hollis-labs/nanite/internal/chat"
)

// errStreamFailure is the sentinel returned by drainCapture when the
// child chat loop emits an error event. The actual error message is
// preserved in the wrapped error.
var errStreamFailure = errors.New("subagent: child chat loop emitted error event")

// drainCapture consumes a chat.StreamEvent channel and assembles a
// summary string + envelope payload for subagent.Result. Pure logic,
// extracted from ChatRunner.Run so the parsing rules can be unit
// tested without spinning up generateResponse.
//
// Rules:
//   - "delta" events: append Content to the summary builder
//   - "plugin_envelope" events: overwrite envelope buffer with
//     Envelope field (last-wins)
//   - "error" / "structured_error" events: terminate drain, return
//     errStreamFailure wrapped with the error message
//   - "stream_end": terminate drain successfully
//   - everything else (stream_start, status, tool_call, tool_result,
//     presence, etc.): ignored for capture purposes
//
// Returns summary, envelope (always valid JSON; "{}" when no envelope
// event was seen), and a non-nil error if the stream emitted an error
// event.
func drainCapture(ch <-chan chat.StreamEvent) (summary string, envelope string, err error) {
	var sb strings.Builder
	envelope = "{}"

	for evt := range ch {
		switch evt.Type {
		case "delta":
			sb.WriteString(evt.Content)
		case "plugin_envelope":
			if evt.Envelope != "" {
				envelope = evt.Envelope
			}
		case "error", "structured_error":
			msg := evt.Error
			if msg == "" {
				msg = "stream error event with no message"
			}
			return sb.String(), envelope, errors.Join(errStreamFailure, errors.New(msg))
		case "stream_end":
			return sb.String(), envelope, nil
		}
	}

	// Channel closed without stream_end — treat as a clean drain.
	return sb.String(), envelope, nil
}
