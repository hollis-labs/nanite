package openai

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/hollis-labs/nanite/internal/provider/failure"
	"github.com/hollis-labs/nanite/internal/provider/station"
	sdk "github.com/openai/openai-go/v3"
)

var errEmptyResponse = fmt.Errorf("openai: %w", failure.ErrEmptyResponse)

func translateError(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *sdk.Error
	if !errors.As(err, &apiErr) {
		return failure.Transport(err)
	}
	cause := fmt.Errorf("openai: %s %d %s: %s", http.StatusText(apiErr.StatusCode), apiErr.StatusCode, apiErr.Code, apiErr.Message)
	if gwErr := station.Classify(apiErr.StatusCode, apiErr.Code, apiErr.RawJSON(), cause); gwErr != nil {
		return &failure.Error{Class: failure.Fatal, Cause: gwErr}
	}
	return failure.HTTP(apiErr.StatusCode, apiErr.Code, apiErr.Response, cause)
}
