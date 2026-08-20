package chat

import "testing"

// These pin the shared ENVELOPE_DATA marker-extraction primitive
// (envelope_marker.go) collapsed out of internal/service/chat_generate.go's
// captureEnvelopeData, internal/api/tools_call.go's extractEnvelopeMarker,
// and internal/mcpserver/handlers.go's convertEnvelopeMarkers — see
// TASKS/harness-reactive-self-tools/06-collapse-envelope-marker-consumers.md.
// Package-specific regression tests proving the shared function works
// through each of those three real call sites' own code paths live
// alongside the call sites themselves (internal/service/chat_test.go,
// internal/api/tools_call_test.go, internal/mcpserver/server_test.go).

func TestExtractEnvelopeMarker_NoMarker(t *testing.T) {
	json, ok := ExtractEnvelopeMarker("plain text, no marker")
	if ok {
		t.Errorf("ok = true, want false (no marker present)")
	}
	if json != "" {
		t.Errorf("json = %q, want empty", json)
	}
}

func TestExtractEnvelopeMarker_WellFormed(t *testing.T) {
	text := `before <!--ENVELOPE_DATA:{"type":"info-card"}:ENVELOPE_DATA--> after`
	json, ok := ExtractEnvelopeMarker(text)
	if !ok {
		t.Fatalf("ok = false, want true")
	}
	if json != `{"type":"info-card"}` {
		t.Errorf("json = %q, want {\"type\":\"info-card\"}", json)
	}
}

// TestExtractEnvelopeMarker_MalformedIgnored pins the malformed-marker
// behavior all three original implementations agreed on (confirmed before
// this collapse, not assumed): an opening delimiter with no matching
// closing delimiter is treated as "not found" — the scan does not continue
// looking elsewhere in the same text for a second, well-formed marker after
// a truncated first one.
func TestExtractEnvelopeMarker_MalformedIgnored(t *testing.T) {
	text := `<!--ENVELOPE_DATA:{"truncated":true} no closing delimiter here`
	json, ok := ExtractEnvelopeMarker(text)
	if ok {
		t.Errorf("ok = true, want false for a marker with no closing delimiter")
	}
	if json != "" {
		t.Errorf("json = %q, want empty", json)
	}
}

// TestExtractEnvelopeMarker_MultipleReturnsFirstOnly pins the
// single-marker semantics captureEnvelopeData and extractEnvelopeMarker
// both had before the collapse: only the first well-formed marker is
// extracted, even when more than one is present in the text.
func TestExtractEnvelopeMarker_MultipleReturnsFirstOnly(t *testing.T) {
	text := `<!--ENVELOPE_DATA:{"a":1}:ENVELOPE_DATA--> and <!--ENVELOPE_DATA:{"b":2}:ENVELOPE_DATA-->`
	json, ok := ExtractEnvelopeMarker(text)
	if !ok {
		t.Fatalf("ok = false, want true")
	}
	if json != `{"a":1}` {
		t.Errorf("json = %q, want {\"a\":1} (first marker only)", json)
	}
}

func TestExtractEnvelopeMarker_NoTrimming(t *testing.T) {
	// None of the three original implementations trimmed whitespace around
	// the payload — pin that explicitly so a future edit doesn't silently
	// introduce trimming as an accidental behavior change.
	text := `<!--ENVELOPE_DATA: {"type":"info-card"} :ENVELOPE_DATA-->`
	json, ok := ExtractEnvelopeMarker(text)
	if !ok {
		t.Fatalf("ok = false, want true")
	}
	if json != ` {"type":"info-card"} ` {
		t.Errorf("json = %q, want the untrimmed payload with surrounding spaces preserved", json)
	}
}

func TestReplaceEnvelopeMarkers_NoMarkers(t *testing.T) {
	got := ReplaceEnvelopeMarkers("plain text", func(payload string) string { return "REPLACED" })
	if got != "plain text" {
		t.Errorf("got %q, want unchanged text", got)
	}
}

func TestReplaceEnvelopeMarkers_ReplacesEveryOccurrence(t *testing.T) {
	text := `First <!--ENVELOPE_DATA:{"a":1}:ENVELOPE_DATA--> and second <!--ENVELOPE_DATA:{"b":2}:ENVELOPE_DATA--> done`
	var seen []string
	got := ReplaceEnvelopeMarkers(text, func(payload string) string {
		seen = append(seen, payload)
		return "[[" + payload + "]]"
	})
	want := `First [[{"a":1}]] and second [[{"b":2}]] done`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if len(seen) != 2 || seen[0] != `{"a":1}` || seen[1] != `{"b":2}` {
		t.Errorf("replace callback saw %#v, want both payloads in order", seen)
	}
}

// TestReplaceEnvelopeMarkers_StopsAtMalformedMarker pins
// convertEnvelopeMarkers' pre-collapse behavior: a malformed marker — an
// opening delimiter with no closing delimiter anywhere later in the text —
// halts the replace loop entirely rather than skipping past it to find a
// later well-formed marker.
func TestReplaceEnvelopeMarkers_StopsAtMalformedMarker(t *testing.T) {
	text := `<!--ENVELOPE_DATA:{"truncated":true} no closing delimiter anywhere in this text`
	got := ReplaceEnvelopeMarkers(text, func(payload string) string { return "REPLACED" })
	if got != text {
		t.Errorf("got %q, want unchanged text (malformed leading marker halts the loop)", got)
	}
}
