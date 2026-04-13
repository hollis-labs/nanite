package provider

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/slogx"
)

// Guards the contract that PII-sensitive log sites in this package name
// their raw-content attribute one of the slogx.PIIRedactor sensitive
// keys ("content" in this package's case). If a future change renames
// that attr to something outside the redactor allowlist, this test
// fails and the leak is caught before merge.
//
// These tests don't drive the actual provider code paths — they are a
// lightweight assertion that the exact slog call patterns used in
// pty.go / subprocess.go get redacted under the production handler.
func TestPIIRedactor_ProviderContentAttrRedacted(t *testing.T) {
	buf := &bytes.Buffer{}
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(slogx.NewPIIRedactor(
		slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}),
	)))

	// Synthetic prompt string that would appear in a malformed CLI
	// output line. The production code at pty.go/subprocess.go's
	// parse-error site passes raw bytes to slog.Warn with key
	// "content" exactly so this redaction kicks in.
	const promptText = "please write a poem about user secrets"

	slog.Warn("pty: parse error",
		"adapter", "claude",
		"err", "unexpected token",
		"content", promptText,
	)

	out := buf.String()
	if strings.Contains(out, promptText) {
		t.Fatalf("prompt text leaked to log output:\n%s", out)
	}
	if !strings.Contains(out, `"content":"[redacted]"`) {
		t.Fatalf("expected content attr redacted, got:\n%s", out)
	}
}

// Same contract for the subprocess parse-error site, exercised
// separately so a rename of either call site is caught independently.
func TestPIIRedactor_SubprocessContentAttrRedacted(t *testing.T) {
	buf := &bytes.Buffer{}
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(slogx.NewPIIRedactor(
		slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}),
	)))

	const assistantResponse = "assistant: here are your secret keys: AKIA..."

	slog.Warn("subprocess: parse error",
		"adapter", "codex",
		"err", "invalid json",
		"content", assistantResponse,
	)

	if strings.Contains(buf.String(), assistantResponse) {
		t.Fatalf("assistant response leaked to log:\n%s", buf.String())
	}
}
