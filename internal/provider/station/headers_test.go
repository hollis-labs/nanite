package station

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestTurnHeaderLimits(t *testing.T) {
	for _, name := range []string{"api-key", "X-Goog-Api-Key", "X-Auth-Token", "PRIVATE-TOKEN", "x-SeCrEt-note", "X-Cookie", "X-Session", "X-Password", "X-Credential", "Content-Type", "MCP-Protocol-Version"} {
		if _, err := CopyHeaders(map[string]string{name: "value"}); err == nil {
			t.Errorf("accepted %s", name)
		}
	}
	for _, value := range []string{"a\tb", "a\nb", "a\x00b", "a\x7fb", "café", strings.Repeat("a", 257)} {
		if _, err := CopyHeaders(map[string]string{"X-Profile": value}); err == nil {
			t.Errorf("accepted invalid value length %d", len(value))
		}
	}
	headers := map[string]string{}
	for i := range 16 {
		headers[fmt.Sprintf("X-Correlation-%d", i)] = strings.Repeat("a", 256)
	}
	if _, err := CopyHeaders(headers); err != nil {
		t.Fatal(err)
	}
	headers["X-Extra"] = "value"
	if _, err := CopyHeaders(headers); err == nil {
		t.Fatal("accepted 17 headers")
	}
	if _, err := CopyHeaders(map[string]string{"X-Profile": "a", "x-profile": "b"}); err == nil {
		t.Fatal("accepted duplicate names")
	}
}

func TestConfiguredHeadersWinAndWarnOnce(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	ctx := WithHeaders(t.Context(), map[string]string{"X-Static": "turn-alpha", "X-Profile": "alpha"})
	for range 3 {
		got := ConfiguredHeaders(ctx, map[string]string{"x-static": "host"}, logger)
		if got["X-Static"] != "host" || got["X-Profile"] != "alpha" {
			t.Fatalf("merged %v", got)
		}
	}
	if strings.Count(logs.String(), "shadowed") != 1 || strings.Contains(logs.String(), "turn-alpha") {
		t.Fatalf("warnings: %s", logs.String())
	}
	if Headers(ctx)["X-Static"] != "turn-alpha" {
		t.Fatal("mutated turn headers")
	}
}
