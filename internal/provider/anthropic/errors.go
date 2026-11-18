package anthropic

import (
	"encoding/json"
	"errors"
	"fmt"

	ctxpkg "github.com/hollis-labs/libs/agent/core/context"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/hollis-labs/nanite/internal/provider/failure"
	"github.com/hollis-labs/nanite/internal/provider/station"
)

var (
	ErrTransient      = errors.New("anthropic: transient provider error")
	ErrAuthentication = errors.New("anthropic: authentication error")
)

func translateError(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *sdk.Error
	if !errors.As(err, &apiErr) {
		return failure.Transport(err)
	}
	if gwErr := station.Classify(apiErr.StatusCode, string(apiErr.Type()), apiErr.RawJSON(), apiErr); gwErr != nil {
		return &failure.Error{Class: failure.Fatal, Cause: gwErr}
	}
	code := string(apiErr.Type())
	var body struct {
		Error struct{ Code, Type, Message string }
	}
	_ = json.Unmarshal([]byte(apiErr.RawJSON()), &body)
	if body.Error.Code != "" {
		code = body.Error.Code
	} else if body.Error.Type != "" {
		code = body.Error.Type
	}
	if code == "invalid_request_error" && ctxpkg.IsContextOverflowMessage(body.Error.Message) {
		code = "context_length_exceeded"
	}
	translated := failure.HTTP(apiErr.StatusCode, code, apiErr.Response, apiErr)
	switch translated.Class {
	case failure.Retryable:
		translated.Cause = fmt.Errorf("%w: %w", ErrTransient, apiErr)
	case failure.Auth:
		translated.Cause = fmt.Errorf("%w: %w", ErrAuthentication, apiErr)
	}
	return translated
}
