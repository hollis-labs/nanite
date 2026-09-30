// Package keycheck holds the error an LLM adapter's VerifyKey wraps when the
// provider refuses the credentials, so callers can tell a rejected key from
// an unreachable provider without importing each SDK's error type.
package keycheck

import "errors"

// ErrRejected is wrapped by VerifyKey when the provider answers 401 or 403.
var ErrRejected = errors.New("credentials rejected")

// Rejected reports whether an HTTP status means the credentials were refused.
func Rejected(status int) bool { return status == 401 || status == 403 }
