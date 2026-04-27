package loopdetect

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"sync"
)

// DetectCallback is called synchronously (under the detector lock) when a loop
// is detected. Implementations must be fast and non-blocking — do not call back
// into the detector from the callback.
type DetectCallback func(Detection)

// Option configures a Detector.
type Option func(*Detector)

// WithWindowSize overrides the default sliding-window size (default 10).
func WithWindowSize(n int) Option {
	return func(d *Detector) { d.windowSize = n }
}

// WithThreshold overrides the repeat-count threshold (default 3).
func WithThreshold(n int) Option {
	return func(d *Detector) { d.threshold = n }
}

// WithCallback registers a callback invoked on every detection event.
func WithCallback(cb DetectCallback) Option {
	return func(d *Detector) { d.onDetect = cb }
}

// Detector is the top-level loop-detection engine. It is safe for concurrent
// use; all state is protected by a single mutex.
//
// Create one Detector per application instance (not per session) and reuse it
// across sessions — internal state is partitioned by session_id.
type Detector struct {
	mu         sync.Mutex
	windows    map[string]*sessionWindow
	windowSize int
	threshold  int
	onDetect   DetectCallback
}

// New creates a Detector with the supplied options.
func New(opts ...Option) *Detector {
	d := &Detector{
		windows:    make(map[string]*sessionWindow),
		windowSize: DefaultWindowSize,
		threshold:  DefaultThreshold,
	}
	for _, o := range opts {
		o(d)
	}
	return d
}

// Record ingests one tool-call signal. It returns (Detection, true) when a loop
// is detected, (zero, false) otherwise. Record is non-blocking and O(N) in
// window size — safe to call from hot paths.
func (d *Detector) Record(s Signal) (Detection, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	w := d.getOrCreate(s.SessionID)
	fp := compute(s.ToolName, s.Args)

	// Append to window (ring semantics — trim front when over cap).
	w.fingerprints = append(w.fingerprints, fp)
	if len(w.fingerprints) > w.cap {
		w.fingerprints = w.fingerprints[len(w.fingerprints)-w.cap:]
	}

	// Suppression: if we've already fired for this fingerprint and it's still
	// in the window, skip re-detection. Reset fired when the fingerprint is no
	// longer present.
	if w.fired != "" {
		stillPresent := false
		for _, f := range w.fingerprints {
			if f == w.fired {
				stillPresent = true
				break
			}
		}
		if stillPresent {
			return Detection{}, false
		}
		// Fired fingerprint has aged out — clear suppression.
		w.fired = ""
	}

	// Count how often fp appears in the current window.
	count := 0
	for _, f := range w.fingerprints {
		if f == fp {
			count++
		}
	}

	if count < w.threshold {
		return Detection{}, false
	}

	// Detection fires. Set suppression flag and reset the window so subsequent
	// calls with the same fingerprint don't re-fire immediately.
	w.fired = fp
	det := Detection{
		Fingerprint:    fp,
		ToolName:       s.ToolName,
		Count:          count,
		WindowSize:     len(w.fingerprints),
		DetectedAtTurn: s.TurnID,
		Reason:         fmt.Sprintf("fingerprint_repeated_%d_times_in_last_%d", count, len(w.fingerprints)),
	}

	if d.onDetect != nil {
		d.onDetect(det)
	}

	return det, true
}

// Reset clears all window state for a session. Call on session end or when
// starting a new conversation thread to prevent stale state from bleeding into
// the next conversation.
func (d *Detector) Reset(sessionID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.windows, sessionID)
}

// getOrCreate returns (or creates) the sessionWindow for sessionID.
// Must be called under d.mu.
func (d *Detector) getOrCreate(sessionID string) *sessionWindow {
	w, ok := d.windows[sessionID]
	if !ok {
		w = &sessionWindow{
			fingerprints: make([]Fingerprint, 0, d.windowSize),
			cap:          d.windowSize,
			threshold:    d.threshold,
		}
		d.windows[sessionID] = w
	}
	return w
}

// sessionWindow is the per-session sliding window of fingerprints.
type sessionWindow struct {
	fingerprints []Fingerprint // ring buffer (oldest at index 0 after trim)
	cap          int           // window size
	threshold    int           // repeat count to trigger detection
	fired        Fingerprint   // last detected fingerprint (suppression key)
}

// compute returns the Fingerprint for (toolName, rawArgs).
//
// Normalization algorithm:
//  1. Parse rawArgs as a JSON object. If parsing fails (non-JSON or non-object),
//     treat the raw bytes as an opaque string (no normalization possible).
//  2. Sort all top-level JSON keys lexicographically.
//  3. Re-serialize the sorted object.
//  4. Concatenate "<toolName>|<serialized_args>" and hash with FNV-1a 64-bit.
//
// Trade-offs:
//   - Only top-level keys are sorted; nested object key order is preserved.
//     This is intentional — deep recursive sort would be O(N log N) on large
//     args and is overkill for fingerprint equality in a sliding-window context.
//   - String values are NOT lowercased (was considered, rejected: lowercasing
//     changes semantics for case-sensitive paths, queries, and tokens — would
//     create false-positive matches across distinct calls).
//   - FNV-1a 64-bit is 8 bytes, collision probability negligible for a 10-call
//     window at any realistic session duration.
func compute(toolName string, rawArgs json.RawMessage) Fingerprint {
	normalized := normalizeArgs(rawArgs)
	h := fnv.New64a()
	_, _ = h.Write([]byte(toolName))
	_, _ = h.Write([]byte("|"))
	_, _ = h.Write([]byte(normalized))
	return Fingerprint(fmt.Sprintf("%016x", h.Sum64()))
}

// normalizeArgs returns a canonical JSON string for args, sorting top-level
// object keys. Falls back to the raw string representation for non-JSON input.
func normalizeArgs(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	// Try to unmarshal as a generic JSON object (map).
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		// Non-object JSON (array, string, number) or invalid JSON: use raw bytes.
		return strings.TrimSpace(string(raw))
	}

	// Sort keys deterministically.
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Re-serialize with sorted keys.
	var sb strings.Builder
	sb.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte(',')
		}
		keyBytes, _ := json.Marshal(k)
		sb.Write(keyBytes)
		sb.WriteByte(':')
		sb.Write(obj[k])
	}
	sb.WriteByte('}')
	return sb.String()
}
