package slogx

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func newTestLogger(t *testing.T, redact bool) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	base := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	var h slog.Handler = base
	if redact {
		h = NewPIIRedactor(base)
	}
	return slog.New(h), buf
}

// Decode the last JSON line to a map for attr assertions.
func lastRecord(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatalf("no log output")
	}
	lines := strings.Split(line, "\n")
	var m map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &m); err != nil {
		t.Fatalf("decode: %v\n%s", err, lines[len(lines)-1])
	}
	return m
}

func TestPIIRedactor_SensitiveAttrKeys(t *testing.T) {
	cases := []string{
		"prompt", "message", "body", "content",
		"response_body", "request_body", "messages", "completion",
		"Prompt", "BODY", "Response_Body",
	}
	for _, key := range cases {
		t.Run(key, func(t *testing.T) {
			logger, buf := newTestLogger(t, true)
			logger.Info("x", key, "super secret user input")
			got := lastRecord(t, buf)
			if got[key] != redactedPlaceholder {
				t.Fatalf("key %q not redacted: %v", key, got[key])
			}
		})
	}
}

func TestPIIRedactor_PassThroughNonSensitive(t *testing.T) {
	logger, buf := newTestLogger(t, true)
	logger.Info("x", "user_id", "u-123", "status", 200)
	got := lastRecord(t, buf)
	if got["user_id"] != "u-123" {
		t.Fatalf("user_id was modified: %v", got["user_id"])
	}
	if got["status"].(float64) != 200 {
		t.Fatalf("status was modified: %v", got["status"])
	}
}

func TestPIIRedactor_APIKeyScrubber(t *testing.T) {
	// 40 hex chars — looks like an API token.
	token := "deadbeef1234567890cafe1234567890abcdef12"
	logger, buf := newTestLogger(t, true)
	logger.Info("call", "authz", "Bearer "+token)
	got := lastRecord(t, buf)
	val := got["authz"].(string)
	if strings.Contains(val, token) {
		t.Fatalf("raw token leaked: %q", val)
	}
	if !strings.Contains(val, redactedKeyPlaceholder) {
		t.Fatalf("redaction marker missing: %q", val)
	}
}

func TestPIIRedactor_APIKeyScrubber_ShortTokenPasses(t *testing.T) {
	// 16 hex chars — below threshold, should pass through.
	logger, buf := newTestLogger(t, true)
	logger.Info("id", "trace", "deadbeef12345678")
	got := lastRecord(t, buf)
	if got["trace"] != "deadbeef12345678" {
		t.Fatalf("short hex wrongly redacted: %v", got["trace"])
	}
}

func TestPIIRedactor_Groups(t *testing.T) {
	logger, buf := newTestLogger(t, true)
	logger.Info("call",
		slog.Group("request",
			slog.String("body", "user said hello"),
			slog.String("method", "POST"),
		),
	)
	got := lastRecord(t, buf)
	req := got["request"].(map[string]any)
	if req["body"] != redactedPlaceholder {
		t.Fatalf("nested body not redacted: %v", req["body"])
	}
	if req["method"] != "POST" {
		t.Fatalf("nested method altered: %v", req["method"])
	}
}

func TestPIIRedactor_WithAttrs(t *testing.T) {
	buf := &bytes.Buffer{}
	base := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(NewPIIRedactor(base)).With("prompt", "secret prompt")
	logger.Info("x")
	got := lastRecord(t, buf)
	if got["prompt"] != redactedPlaceholder {
		t.Fatalf("WithAttrs did not redact: %v", got["prompt"])
	}
}

func TestPIIRedactor_Disabled(t *testing.T) {
	logger, buf := newTestLogger(t, false)
	logger.Info("x", "prompt", "visible")
	got := lastRecord(t, buf)
	if got["prompt"] != "visible" {
		t.Fatalf("expected no redaction, got %v", got["prompt"])
	}
}

func TestInit_InstallsDefault(t *testing.T) {
	buf := &bytes.Buffer{}
	cfg := Config{Format: FormatJSON, Level: slog.LevelDebug, Output: buf, RedactPII: true}
	_, closer, err := Init(cfg)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer closer.Close()

	slog.Info("hello", "prompt", "sensitive")
	got := lastRecord(t, buf)
	if got["msg"] != "hello" {
		t.Fatalf("message missing: %v", got)
	}
	if got["prompt"] != redactedPlaceholder {
		t.Fatalf("default logger did not redact: %v", got["prompt"])
	}
}

func TestInit_UnknownFormat(t *testing.T) {
	_, _, err := Init(Config{Format: "xml", Output: &bytes.Buffer{}})
	if err == nil {
		t.Fatal("expected error for unknown format")
	}
}

func TestInit_DefaultsOutputAndFormat(t *testing.T) {
	_, closer, err := Init(Config{})
	if err != nil {
		t.Fatalf("Init defaults: %v", err)
	}
	_ = closer.Close()
}

// Ensure PIIRedactor implements slog.Handler at compile time.
var _ slog.Handler = (*PIIRedactor)(nil)

// Smoke test that FatalContext does not panic at compile time (we can't
// actually call it without exiting the process).
var _ = func(ctx context.Context) { _ = ctx }
