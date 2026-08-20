package selftools

import (
	"fmt"
	"testing"
)

// TestEmbedRenderCardMarker_MatchesCallShowCardFormat pins that
// EmbedRenderCardMarker produces byte-identical output to callShowCard's own
// inline marker construction (self_tools_transport.go:962) —
// fmt.Sprintf("%s\n<!--ENVELOPE_DATA:%s:ENVELOPE_DATA-->", label, envJSON).
// This is the concrete guarantee TASKS/harness-reactive-self-tools/
// 04-render-card-construction.md requires: no new delimiter, no reshaping,
// same format the three existing marker consumers already scan for.
func TestEmbedRenderCardMarker_MatchesCallShowCardFormat(t *testing.T) {
	label := "info-card: Task update"
	envJSON := `{"kind":"envelope","version":1,"type":"info-card","data":{"title":"Task update","body":"hello"}}`

	got := EmbedRenderCardMarker(label, envJSON)
	want := fmt.Sprintf("%s\n<!--ENVELOPE_DATA:%s:ENVELOPE_DATA-->", label, envJSON)

	if got != want {
		t.Fatalf("EmbedRenderCardMarker output mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

// TestEmbedRenderCardMarker_EmptyLabel pins that an empty label still yields
// a well-formed marker (leading newline, no panic) — callShowCard's own
// label is never empty in practice (it falls back to envType), but the
// helper itself makes no such assumption.
func TestEmbedRenderCardMarker_EmptyLabel(t *testing.T) {
	got := EmbedRenderCardMarker("", `{"kind":"envelope","version":1,"type":"info-card","data":{}}`)
	want := "\n<!--ENVELOPE_DATA:{\"kind\":\"envelope\",\"version\":1,\"type\":\"info-card\",\"data\":{}}:ENVELOPE_DATA-->"
	if got != want {
		t.Fatalf("EmbedRenderCardMarker(empty label) = %q, want %q", got, want)
	}
}
