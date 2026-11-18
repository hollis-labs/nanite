package failure

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestHTTPClassSurvivesStreamTransport(t *testing.T) {
	for _, tc := range []struct {
		status int
		code   string
		class  Class
		delay  time.Duration
	}{
		{429, "", Retryable, 3 * time.Second}, {503, "", Retryable, 3 * time.Second},
		{413, "", Overflow, 0}, {400, "context_length_exceeded", Overflow, 0},
		{401, "", Auth, 0}, {400, "invalid_request", Fatal, 0},
	} {
		resp := &http.Response{Header: http.Header{"Retry-After": {"3"}}}
		err := FromEvent(Event(HTTP(tc.status, tc.code, resp, errors.New("failure"))))
		var got *Error
		if !errors.As(err, &got) || got.Class != tc.class || got.RetryAfter != tc.delay {
			t.Errorf("%d %s: %+v", tc.status, tc.code, got)
		}
	}
}
