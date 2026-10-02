package api

import (
	"errors"
	"fmt"
	"net/http/httptest"
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
