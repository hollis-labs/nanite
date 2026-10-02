package api

import (
	"errors"
	"log/slog"
	"net/http"

	svcerr "github.com/hollis-labs/go-svcerr"
)

// serviceError maps the typed service carrier to Nanite's flat HTTP envelope.
// Untyped failures are internal failures; their causes stay in server logs.
func (a *API) serviceError(w http.ResponseWriter, err error) {
	message := "internal error"
	var typed *svcerr.Error
	if errors.As(err, &typed) && typed != nil {
		message = typed.Message
	}
	if svcerr.CodeFor(err) == "" || svcerr.CodeFor(err) == svcerr.CodeInternal {
		slog.Error("api: service operation failed", "err", err)
	}
	a.errorResp(w, svcerr.StatusFor(err, http.StatusInternalServerError), message)
}
