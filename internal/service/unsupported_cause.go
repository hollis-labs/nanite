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
// The codes are constants below; keep one list so every branch (native API,
// CLI runtime) speaks the same vocabulary.
const (
	CauseHostDisabled                 = "host_disabled"
	CauseRuntimeNotAPI                = "runtime_not_api"
	CauseRuntimeUnsupported           = "runtime_unsupported"
	CauseNativeHostUnconfigured       = "native_host_unconfigured"
	CauseModelSelectionMismatch       = "model_selection_mismatch"
	CauseModelNotSelected             = "model_not_selected"
	CauseModelAuthorizerMissing       = "model_authorizer_missing"
	CauseProviderNotRegistered        = "provider_not_registered"
	CauseProviderKeyUnavailable       = "provider_key_unavailable"
	CausePermissionProfileUnsupported = "permission_profile_unsupported"
	CauseDefinitionUnsupported        = "definition_unsupported"
	CauseResourceResolverMissing      = "resource_resolver_unavailable"

	// Reserved for the CLI runtime (B2); defined here so there is one list.
	CauseRuntimeUnavailable      = "runtime_unavailable"
	CauseReadOnlyUnenforceable   = "read_only_unenforceable"
	CauseProviderNotLaunchable   = "provider_not_launchable"
	CauseCLIHostUnsupportedField = "cli_host_unsupported_field"
	CauseHostDirectoriesMissing  = "host_directories_missing"
	CauseHostRevisionChanged     = "host_revision_changed"
	CauseWorkRootUnsupported     = "work_root_unsupported"
	CauseWorkRootInvalid         = "work_root_invalid"
	CauseHostDirectoryInvalid    = "host_directory_invalid"
)

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
