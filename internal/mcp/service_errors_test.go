package mcp

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	svcerr "github.com/hollis-labs/go-svcerr"
)

func TestServiceErrorResultKeepsCausePrivate(t *testing.T) {
	cause := errors.New("private key and database query")
	for _, err := range []error{cause, fmt.Errorf("operation: %w", svcerr.Wrap(cause, svcerr.CodeNotFound, "todo not found"))} {
		result := ServiceErrorResult(err)
		if !result.IsError || len(result.Content) != 1 {
			t.Fatalf("failure result: %+v", result)
		}
		if strings.Contains(result.Content[0].Text, cause.Error()) {
			t.Fatalf("leaked cause: %s", result.Content[0].Text)
		}
	}
}

func TestServiceErrorResultLogsWrappedCause(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	cause := errors.New("SQLITE_BUSY: private query and database path")
	result := ServiceErrorResult(fmt.Errorf("operation: %w", svcerr.Wrap(cause, svcerr.CodeInternal, "failed to write agent")))
	if !strings.Contains(logs.String(), cause.Error()) {
		t.Fatalf("missing cause in log: %s", logs.String())
	}
	if !result.IsError || strings.Contains(result.Content[0].Text, cause.Error()) || !strings.Contains(result.Content[0].Text, `"message":"failed to write agent"`) {
		t.Fatalf("unsafe response: %+v", result)
	}
}

func TestServiceErrorResultLogSeverity(t *testing.T) {
	for _, code := range []svcerr.Code{svcerr.CodeInvalid, svcerr.CodeNotFound, svcerr.CodeConflict, svcerr.CodePermission, svcerr.CodeInternal, svcerr.CodeUnavailable} {
		t.Run(string(code), func(t *testing.T) {
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })
			err := svcerr.Wrap(errors.New("private cause"), code, "safe message")
			ServiceErrorResult(err)
			level := "WARN"
			if code == svcerr.CodeInternal || code == svcerr.CodeUnavailable {
				level = "ERROR"
			}
			if !strings.Contains(logs.String(), "level="+level) || !strings.Contains(logs.String(), "private cause") {
				t.Fatalf("unexpected severity or missing cause: %s", logs.String())
			}
		})
	}
}
