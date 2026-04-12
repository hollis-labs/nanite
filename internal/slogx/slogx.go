// Package slogx installs a structured log/slog handler at startup and
// wraps it with a PII-aware redactor.
//
// The package is intentionally small: a Config, an Init that returns the
// configured *slog.Logger plus an io.Closer for graceful shutdown, a
// PIIRedactor handler wrapper, and a Fatal helper that routes log.Fatalf-
// style call sites through slog before exiting.
//
// PII redaction runs inside the handler so every call site benefits,
// including stdlib log package calls that have been migrated to slog.
package slogx

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strings"
)

// Format selects the underlying slog handler.
type Format string

const (
	FormatJSON Format = "json"
	FormatText Format = "text"
)

// Config controls Init.
//
// Zero values are valid; DefaultConfig returns production-safe defaults.
type Config struct {
	// Format is "json" (default) or "text".
	Format Format
	// Level is the minimum emitted level. Defaults to slog.LevelInfo.
	Level slog.Level
	// Output is where records are written. Defaults to os.Stderr.
	Output io.Writer
	// RedactPII, when true, installs the PIIRedactor wrapper. Defaults true.
	RedactPII bool
	// AddSource, when true, annotates records with file:line.
	AddSource bool
}

// DefaultConfig returns production-safe defaults: JSON, INFO, stderr,
// PII redaction on.
func DefaultConfig() Config {
	return Config{
		Format:    FormatJSON,
		Level:     slog.LevelInfo,
		Output:    os.Stderr,
		RedactPII: true,
	}
}

// Init constructs a slog.Logger per cfg, installs it via slog.SetDefault,
// and returns it alongside an io.Closer for graceful shutdown.
//
// The closer flushes and releases the output sink. For the default
// stderr writer this is a no-op, but callers should defer Close so a
// future file/rotation sink doesn't leak.
func Init(cfg Config) (*slog.Logger, io.Closer, error) {
	if cfg.Output == nil {
		cfg.Output = os.Stderr
	}
	if cfg.Format == "" {
		cfg.Format = FormatJSON
	}

	opts := &slog.HandlerOptions{
		Level:     cfg.Level,
		AddSource: cfg.AddSource,
	}

	var base slog.Handler
	switch cfg.Format {
	case FormatText:
		base = slog.NewTextHandler(cfg.Output, opts)
	case FormatJSON:
		base = slog.NewJSONHandler(cfg.Output, opts)
	default:
		return nil, nil, fmt.Errorf("slogx: unknown format %q", cfg.Format)
	}

	if cfg.RedactPII {
		base = NewPIIRedactor(base)
	}

	logger := slog.New(base)
	slog.SetDefault(logger)

	return logger, noopCloser{}, nil
}

type noopCloser struct{}

func (noopCloser) Close() error { return nil }

// Fatal emits a slog.Error with the supplied message and attrs, then
// exits the process with status 1. Use this in place of log.Fatalf so
// the failure is emitted through the structured handler and the PII
// redactor.
//
// Attrs follow the slog variadic convention ("key", value, ...).
func Fatal(msg string, args ...any) {
	slog.Error(msg, args...)
	os.Exit(1)
}

// FatalContext is like Fatal but carries a context for handlers that
// propagate trace identifiers.
func FatalContext(ctx context.Context, msg string, args ...any) {
	slog.Default().Log(ctx, slog.LevelError, msg, args...)
	os.Exit(1)
}

// ParseLevel maps a string ("debug", "info", "warn", "error") to a
// slog.Level. Unknown or empty values fall back to slog.LevelInfo.
// Matching is case-insensitive.
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error", "err":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// ParseFormat maps a string to a Format. Unknown or empty values fall
// back to FormatJSON.
func ParseFormat(s string) Format {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "text":
		return FormatText
	default:
		return FormatJSON
	}
}

// ---------- PII redactor ----------

// sensitiveAttrKeys names attributes whose values are replaced with
// redactedPlaceholder before emission. Match is case-insensitive and
// applies at any handler group depth.
var sensitiveAttrKeys = map[string]struct{}{
	"prompt":        {},
	"message":       {},
	"body":          {},
	"content":       {},
	"response_body": {},
	"request_body":  {},
	"messages":      {},
	"completion":    {},
}

const (
	redactedPlaceholder    = "[redacted]"
	redactedKeyPlaceholder = "[redacted-key]"
)

// apiKeyPattern matches hex-only runs of 32+ chars. Conservative on
// purpose: we accept false negatives over false positives. Short
// IDs, UUIDs with hyphens, and base64 tokens are not matched.
var apiKeyPattern = regexp.MustCompile(`\b[a-fA-F0-9]{32,}\b`)

// PIIRedactor wraps another slog.Handler and scrubs sensitive attrs
// before delegating.
type PIIRedactor struct {
	inner slog.Handler
}

// NewPIIRedactor wraps h. The returned handler satisfies slog.Handler.
func NewPIIRedactor(h slog.Handler) *PIIRedactor {
	return &PIIRedactor{inner: h}
}

// Enabled delegates to the wrapped handler.
func (p *PIIRedactor) Enabled(ctx context.Context, level slog.Level) bool {
	return p.inner.Enabled(ctx, level)
}

// Handle redacts the record's attributes then delegates.
func (p *PIIRedactor) Handle(ctx context.Context, r slog.Record) error {
	cloned := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		cloned.AddAttrs(redactAttr(a))
		return true
	})
	return p.inner.Handle(ctx, cloned)
}

// WithAttrs redacts baseline attrs and delegates.
func (p *PIIRedactor) WithAttrs(attrs []slog.Attr) slog.Handler {
	red := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		red[i] = redactAttr(a)
	}
	return &PIIRedactor{inner: p.inner.WithAttrs(red)}
}

// WithGroup delegates unchanged. Group namespaces are preserved; the
// sensitive-key match is case-insensitive on the leaf name so
// "request.body" and "body" both redact.
func (p *PIIRedactor) WithGroup(name string) slog.Handler {
	return &PIIRedactor{inner: p.inner.WithGroup(name)}
}

// redactAttr returns a redacted copy of a. Groups recurse. String and
// stringer values in sensitive-key attrs are replaced wholesale; other
// string values are scanned for the API-key regex.
func redactAttr(a slog.Attr) slog.Attr {
	if a.Value.Kind() == slog.KindGroup {
		inner := a.Value.Group()
		out := make([]slog.Attr, len(inner))
		for i, g := range inner {
			out[i] = redactAttr(g)
		}
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(out...)}
	}

	if isSensitiveKey(a.Key) {
		return slog.String(a.Key, redactedPlaceholder)
	}

	if a.Value.Kind() == slog.KindString {
		if s := a.Value.String(); apiKeyPattern.MatchString(s) {
			return slog.String(a.Key, apiKeyPattern.ReplaceAllString(s, redactedKeyPlaceholder))
		}
	}
	return a
}

func isSensitiveKey(key string) bool {
	_, ok := sensitiveAttrKeys[strings.ToLower(key)]
	return ok
}
