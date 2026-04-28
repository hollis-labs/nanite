// Package loopdetect implements fingerprint-based tool-call loop detection
// (I2, CW-20260420-0029, Phase 8).
//
// The detector maintains a per-session sliding window of tool-call fingerprints.
// A fingerprint is an FNV-1a hash over the canonical (tool_name, normalized_args)
// pair. When the same fingerprint appears ≥ threshold times in the last N calls,
// a Detection is returned and the window is reset to suppress redundant re-fires.
//
// All state is in-memory and per-session; nothing is persisted.
package loopdetect

import (
	"encoding/json"
	"time"
)

// DefaultWindowSize is the number of recent tool calls tracked per session.
const DefaultWindowSize = 10

// DefaultThreshold is the number of identical fingerprints in the window that
// triggers a detection.
const DefaultThreshold = 3

// Fingerprint is an opaque string hash derived from (tool_name, normalized_args).
// Two calls are fingerprint-equal iff they name the same tool and carry
// semantically identical arguments (JSON key order–independent).
type Fingerprint string

// Signal is one tool-call observation fed into the detector.
type Signal struct {
	SessionID string
	TurnID    string
	ToolName  string
	Args      json.RawMessage // raw JSON args from the tool call
	Timestamp time.Time
}

// Detection describes a confirmed loop event.
type Detection struct {
	// Fingerprint is the repeated hash that triggered detection.
	Fingerprint Fingerprint
	// ToolName is the tool whose calls produced the repeated fingerprint.
	ToolName string
	// Count is the number of times the fingerprint appeared in the window.
	Count int
	// WindowSize is the size of the window at detection time.
	WindowSize int
	// DetectedAtTurn is the TurnID of the signal that pushed count over threshold.
	DetectedAtTurn string
	// Reason is a human-readable description of the detection signal.
	// Format: "fingerprint_repeated_N_times_in_last_M"
	Reason string
}
