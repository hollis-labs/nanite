package memory

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

// A utility provider that is not registered right now (its key was cleared,
// or it has none yet) skips extraction silently, as a nil call does, rather
// than warning on every turn (CW-20260930-0101). Any other failure still
// warns.
func TestExtraction_UnavailableUtilityIsSilent(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	unavailable := NewExtractor(nil, func(context.Context, string) (string, error) {
		return "", ErrUtilityUnavailable
	})
	unavailable.extractPerTurn("s1", "remember that I prefer tabs")
	unavailable.extractPostCompact("s1", 1000)
	if strings.Contains(buf.String(), "extraction LLM call failed") {
		t.Fatalf("an unavailable utility provider warned:\n%s", buf.String())
	}

	failing := NewExtractor(nil, func(context.Context, string) (string, error) {
		return "", errors.New("boom")
	})
	failing.extractPerTurn("s1", "remember that I prefer tabs")
	if !strings.Contains(buf.String(), "per-turn extraction LLM call failed") {
		t.Fatalf("a real failure did not warn:\n%s", buf.String())
	}
}
