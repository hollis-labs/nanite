package service

import (
	"errors"
	"fmt"
)

// UnsupportedCause is the one typed cause for a 422 "unsupported" refusal. It
// keeps the existing sentinel as its kind, so errors.Is(err,
// ErrUnsupportedDefinition) and errors.Is(err, ErrUnsupportedModel) keep
// working, and adds a stable Code that clients and the HTTP layer can switch
// on. Msg is the operator-facing cause and remedy; it never carries a secret.
//
// Codes in use: host_settings_disabled, runtime_not_api, runtime_unsupported,
// native_host_unconfigured, model_selection_mismatch, model_not_selected,
// model_authorizer_missing, provider_not_registered,
// permission_profile_unsupported, definition_unsupported,
// resource_resolver_unavailable.
type UnsupportedCause struct {
	Code string
	Msg  string
	kind error
}

func (c *UnsupportedCause) Error() string { return c.kind.Error() + ": " + c.Msg }
func (c *UnsupportedCause) Unwrap() error { return c.kind }

// unsupportedCause refuses a definition or host configuration the native
// runtime cannot honor.
func unsupportedCause(code, format string, args ...any) error {
	return &UnsupportedCause{Code: code, Msg: fmt.Sprintf(format, args...), kind: ErrUnsupportedDefinition}
}

// unsupportedModelCause refuses a provider or model selection.
func unsupportedModelCause(code, format string, args ...any) error {
	return &UnsupportedCause{Code: code, Msg: fmt.Sprintf(format, args...), kind: ErrUnsupportedModel}
}

// CauseCode returns the cause code carried by err, or "".
func CauseCode(err error) string {
	var c *UnsupportedCause
	if errors.As(err, &c) {
		return c.Code
	}
	return ""
}
