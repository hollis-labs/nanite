package a2a

import "errors"

// ErrValidation is the sentinel for input-validation failures in the a2a
// service. HTTP handlers, CLI callers, and MCP tools should map this to a
// client error (e.g. HTTP 400) and treat any other error as internal.
//
// Wrap with fmt.Errorf("%w: details", ErrValidation) so that errors.Is
// recovers the sentinel and error messages remain descriptive.
var ErrValidation = errors.New("a2a: validation")

// ErrNotFound indicates a requested resource (handoff, message, etc.) does
// not exist. HTTP handlers should map this to 404.
var ErrNotFound = errors.New("a2a: not found")

// ErrForbidden indicates the caller is not authorized for the requested
// operation — e.g. ack-ing a message they did not receive, or reading an
// inbox / thread they are not a participant in. HTTP handlers should map
// this to 403; MCP handlers should surface it as a tool error. Threat
// model is defensive-only in MVP (tool-broker is the real ACL per S4a
// design); this sentinel exists so misaddressed calls fail loudly rather
// than silently succeeding.
var ErrForbidden = errors.New("a2a: forbidden")
