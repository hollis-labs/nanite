package chat

import "strings"

// envelopeMarkerOpen / envelopeMarkerClose delimit the structured-UI payload
// a self-tool result embeds inline as
// <!--ENVELOPE_DATA:{...json...}:ENVELOPE_DATA-->. Three independent
// consumers each used to hand-scan for this exact marker with their own
// local copy of the delimiter constants and the scan logic:
//
//   - internal/service/chat_generate.go's captureEnvelopeData
//   - internal/api/tools_call.go's extractEnvelopeMarker
//   - internal/mcpserver/handlers.go's convertEnvelopeMarkers
//
// Collapsed into the two functions below per
// docs/engineering/architecture/11-harness-reactive-self-tools.md ("The
// import-cycle constraint..." section) and
// TASKS/harness-reactive-self-tools/06-collapse-envelope-marker-consumers.md.
// All three call sites still exist under their original names (each has a
// real, distinct per-caller shape on top of the scan — block iteration over
// a *mcp.ToolResult, accumulating into a pending slice, or replacing every
// occurrence in place) but now delegate the actual delimiter scan to
// ExtractEnvelopeMarker / ReplaceEnvelopeMarkers instead of re-implementing
// it.
const (
	envelopeMarkerOpen  = "<!--ENVELOPE_DATA:"
	envelopeMarkerClose = ":ENVELOPE_DATA-->"
)

// ExtractEnvelopeMarker returns the JSON payload of the first well-formed
// ENVELOPE_DATA marker in text, and whether one was found.
//
// A marker is "well-formed" when a closing delimiter appears somewhere
// after the opening delimiter. An opening delimiter with no matching
// closing delimiter later in text (a malformed/truncated marker) is
// treated as "not found" — the scan does not continue past it looking for
// a later, well-formed marker elsewhere in the same text. This matches all
// three original hand-scan implementations' malformed-marker behavior
// (confirmed directly against each before this collapse, not assumed).
// Neither the delimiters nor the extracted payload are trimmed of
// whitespace — none of the three originals trimmed either, so this is a
// behavior-preserving refactor, not a divergence.
func ExtractEnvelopeMarker(text string) (json string, ok bool) {
	_, payload, _, found := findEnvelopeMarker(text)
	return payload, found
}

// ReplaceEnvelopeMarkers finds every well-formed ENVELOPE_DATA marker in
// text, in left-to-right order, and replaces each occurrence (the full
// marker — opening delimiter through closing delimiter — with
// replace(payload)'s return value. Used by callers that need to transform
// every marker occurrence in place rather than just read the first one out
// (internal/mcpserver/handlers.go's PTY-output marker-to-fenced-block
// conversion is the one real caller today). Text with no markers is
// returned unchanged.
func ReplaceEnvelopeMarkers(text string, replace func(payload string) string) string {
	for {
		start, payload, end, ok := findEnvelopeMarker(text)
		if !ok {
			break
		}
		text = text[:start] + replace(payload) + text[end:]
	}
	return text
}

// findEnvelopeMarker locates the first well-formed marker in text. It
// returns the JSON payload and the byte range [start, end) spanning the
// full marker — from the opening delimiter through the closing delimiter —
// so ReplaceEnvelopeMarkers can splice a replacement in without
// re-implementing the scan. ok is false (and the other return values are
// zero values) when no well-formed marker is present.
func findEnvelopeMarker(text string) (start int, payload string, end int, ok bool) {
	start = strings.Index(text, envelopeMarkerOpen)
	if start < 0 {
		return 0, "", 0, false
	}
	tail := text[start+len(envelopeMarkerOpen):]
	closeIdx := strings.Index(tail, envelopeMarkerClose)
	if closeIdx < 0 {
		return 0, "", 0, false
	}
	payload = tail[:closeIdx]
	end = start + len(envelopeMarkerOpen) + closeIdx + len(envelopeMarkerClose)
	return start, payload, end, true
}
