package plugin

import (
	"fmt"
	"log"

	"github.com/hollis-labs/plugin"
)

// Logger implements plugin.Logger using Go's standard log package
type Logger struct {
	prefix string
}

// NewLogger creates a new plugin logger with an optional prefix
func NewLogger(prefix string) plugin.Logger {
	return &Logger{
		prefix: prefix,
	}
}

// Debug logs a debug message
func (l *Logger) Debug(msg string, keysAndValues ...interface{}) {
	l.logWithLevel("DEBUG", msg, keysAndValues...)
}

// Info logs an info message
func (l *Logger) Info(msg string, keysAndValues ...interface{}) {
	l.logWithLevel("INFO", msg, keysAndValues...)
}

// Warn logs a warning message
func (l *Logger) Warn(msg string, keysAndValues ...interface{}) {
	l.logWithLevel("WARN", msg, keysAndValues...)
}

// Error logs an error message
func (l *Logger) Error(msg string, keysAndValues ...interface{}) {
	l.logWithLevel("ERROR", msg, keysAndValues...)
}

// With returns a new logger with additional key-value pairs
func (l *Logger) With(keysAndValues ...interface{}) plugin.Logger {
	// For this simple implementation, we'll append the keys to the prefix
	newPrefix := l.prefix
	if len(keysAndValues) > 0 {
		newPrefix += " "
		for i := 0; i < len(keysAndValues); i += 2 {
			if i+1 < len(keysAndValues) {
				newPrefix += fmt.Sprint(keysAndValues[i]) + "=" + fmt.Sprint(keysAndValues[i+1]) + " "
			}
		}
	}
	return &Logger{prefix: newPrefix}
}

// logWithLevel logs a message with the given level
func (l *Logger) logWithLevel(level, msg string, keysAndValues ...interface{}) {
	fullMsg := level + " " + l.prefix + " " + msg

	// Append key-value pairs to the message
	if len(keysAndValues) > 0 {
		fullMsg += " |"
		for i := 0; i < len(keysAndValues); i += 2 {
			if i+1 < len(keysAndValues) {
				fullMsg += " " + fmt.Sprint(keysAndValues[i]) + "=" + fmt.Sprint(keysAndValues[i+1])
			}
		}
	}

	log.Println(fullMsg)
}