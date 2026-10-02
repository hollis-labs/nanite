package api

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	svcerr "github.com/hollis-labs/go-svcerr"
)

func TestServiceErrorKeepsCauseOutOfFlatEnvelope(t *testing.T) {
	cause := errors.New("private database path and query")
	for _, tc := range []struct {
		name   string
		err    error
		status int
		body   string
	}{
		{"wrapped typed error", fmt.Errorf("operation: %w", svcerr.Wrap(cause, svcerr.CodeInvalid, "invalid priority", svcerr.WithField("priority"))), 400, "{\"error\":\"invalid priority\"}\n"},
		{"typed conflict", svcerr.Wrap(cause, svcerr.CodeConflict, "a managed agent with this slug already exists"), 409, "{\"error\":\"a managed agent with this slug already exists\"}\n"},
		{"plain cause", cause, 500, "{\"error\":\"internal error\"}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			(&API{}).serviceError(rec, tc.err)
			if rec.Code != tc.status || rec.Body.String() != tc.body {
				t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
			}
			if rec.Header().Get("Content-Type") != "application/json" {
				t.Fatal("missing JSON content type")
			}
		})
	}
}

func TestServiceErrorLogsWrappedCause(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	cause := errors.New("SQLITE_BUSY: private query and database path")
	rec := httptest.NewRecorder()
	(&API{}).serviceError(rec, fmt.Errorf("operation: %w", svcerr.Wrap(cause, svcerr.CodeInternal, "failed to write agent")))
	if !strings.Contains(logs.String(), cause.Error()) {
		t.Fatalf("missing cause in log: %s", logs.String())
	}
	if rec.Code != 500 || rec.Body.String() != "{\"error\":\"failed to write agent\"}\n" {
		t.Fatalf("unsafe response: %d %s", rec.Code, rec.Body.String())
	}
}
