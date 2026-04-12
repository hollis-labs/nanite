package plugin

import (
	"log/slog"

	"github.com/hollis-labs/go-plugin"
)

// Logger implements plugin.Logger by delegating to log/slog. The
// prefix becomes a persistent "prefix" attribute on every record so
// downstream filters can scope plugin logs.
type Logger struct {
	logger *slog.Logger
}

// NewLogger creates a new plugin logger that attaches the given prefix
// as a slog attribute to every record.
func NewLogger(prefix string) plugin.Logger {
	return &Logger{
		logger: slog.Default().With("prefix", prefix),
	}
}

func (l *Logger) Debug(msg string, keysAndValues ...interface{}) {
	l.logger.Debug(msg, keysAndValues...)
}

func (l *Logger) Info(msg string, keysAndValues ...interface{}) {
	l.logger.Info(msg, keysAndValues...)
}

func (l *Logger) Warn(msg string, keysAndValues ...interface{}) {
	l.logger.Warn(msg, keysAndValues...)
}

func (l *Logger) Error(msg string, keysAndValues ...interface{}) {
	l.logger.Error(msg, keysAndValues...)
}

// With returns a derived logger that includes the additional attrs on
// every subsequent record, matching slog.Logger.With semantics.
func (l *Logger) With(keysAndValues ...interface{}) plugin.Logger {
	return &Logger{logger: l.logger.With(keysAndValues...)}
}
