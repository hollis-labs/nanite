package engine

import (
	"strings"
	"testing"
)

func TestAppendStepTextSeparatesSteps(t *testing.T) {
	var all strings.Builder
	for _, text := range []string{"Checking.", "", "Done."} {
		appendStepText(&all, text)
	}
	if got, want := all.String(), "Checking.\n\nDone."; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
