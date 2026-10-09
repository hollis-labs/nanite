package api

import (
	"errors"
	"log/slog"
	"net/http"

	svcerr "github.com/hollis-labs/libs/util/svcerr"
)

// serviceError maps the typed service carrier to Nanite's flat HTTP envelope.
// Untyped failures are internal failures; their causes stay in server logs.
func (a *API) serviceError(w http.ResponseWriter, r *http.Request, err error) {
	message := "internal error"
	var typed *svcerr.Error
	if errors.As(err, &typed) && typed != nil {
		message = typed.Message
	}
	code := svcerr.CodeInternal
	cause := err
	if typed != nil {
		code, cause = typed.Code, typed.Err
	}
	attrs := []any{"code", code, "message", message, "method", r.Method, "route", r.Pattern}
	for _, key := range []string{"id", "agentId", "projectId", "sessionId"} {
		if id := r.PathValue(key); id != "" {
			attrs = append(attrs, key, id)
		}
	}
	if cause != nil {
		attrs = append(attrs, "cause", cause)
	}
	if code == svcerr.CodeInternal || code == svcerr.CodeUnavailable {
		slog.Error("api: service operation failed", attrs...)
	} else {
		slog.Warn("api: service operation rejected", attrs...)
	}
	a.errorResp(w, svcerr.StatusFor(err, http.StatusInternalServerError), message)
}
