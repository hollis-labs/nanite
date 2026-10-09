package api

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	svcerr "github.com/hollis-labs/libs/util/svcerr"
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
			(&API{}).serviceError(rec, httptest.NewRequest("GET", "/api/agents/fixture", nil), tc.err)
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
	(&API{}).serviceError(rec, httptest.NewRequest("GET", "/api/agents/fixture", nil), fmt.Errorf("operation: %w", svcerr.Wrap(cause, svcerr.CodeInternal, "failed to write agent")))
	if !strings.Contains(logs.String(), cause.Error()) {
		t.Fatalf("missing cause in log: %s", logs.String())
	}
	if rec.Code != 500 || rec.Body.String() != "{\"error\":\"failed to write agent\"}\n" {
		t.Fatalf("unsafe response: %d %s", rec.Code, rec.Body.String())
	}
}

func TestServiceErrorLogSeverity(t *testing.T) {
	for _, code := range []svcerr.Code{svcerr.CodeInvalid, svcerr.CodeNotFound, svcerr.CodeConflict, svcerr.CodePermission, svcerr.CodeInternal, svcerr.CodeUnavailable} {
		t.Run(string(code), func(t *testing.T) {
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })
			err := svcerr.Wrap(errors.New("private cause"), code, "safe message")
			(&API{}).serviceError(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/agents/fixture", nil), err)
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

func TestServiceErrorWithoutCauseLogsRequestContext(t *testing.T) {
	for _, code := range []svcerr.Code{svcerr.CodeNotFound, svcerr.CodeUnavailable} {
		t.Run(string(code), func(t *testing.T) {
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })
			req := httptest.NewRequest("DELETE", "/api/agents/member/projects/project?private=secret", nil)
			req.Pattern = "DELETE /api/agents/{id}/projects/{projectId}"
			req.SetPathValue("id", "member")
			req.SetPathValue("projectId", "project")
			(&API{}).serviceError(httptest.NewRecorder(), req, svcerr.New(code, "safe message"))
			level := "WARN"
			if code == svcerr.CodeUnavailable {
				level = "ERROR"
			}
			for _, want := range []string{"level=" + level, "code=" + string(code), `message="safe message"`, `route="DELETE /api/agents/{id}/projects/{projectId}"`, "id=member", "projectId=project"} {
				if !strings.Contains(logs.String(), want) {
					t.Fatalf("missing %q: %s", want, logs.String())
				}
			}
			if strings.Contains(logs.String(), "secret") || strings.Contains(logs.String(), "cause=") {
				t.Fatalf("unexpected log data: %s", logs.String())
			}
		})
	}
}
