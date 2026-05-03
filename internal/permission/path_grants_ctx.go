package permission

import "context"

// PathGrantChecker is the minimal lookup contract dev_tools needs to
// consult session-scoped grants without importing the *PathGrants type.
// PathGrants satisfies it directly.
//
// Threading the lookup through ctx (rather than a global) keeps tests
// hermetic and lets the dev_tools transport stay a value type.
//
// LookupPath returns the same boolean as IsPathAllowed plus the match
// kind ("literal", "ancestor", "none") for diagnostic surfaces. BucketSize
// reports how many grants are registered for the session, used to
// distinguish "no bucket" misses from "bucket exists but no match" misses.
type PathGrantChecker interface {
	IsPathAllowed(sessionID, candidate string) bool
	LookupPath(sessionID, candidate string) (bool, LookupKind)
	BucketSize(sessionID string) int
}

// pathGrantCtxKey carries the (sessionID, checker) pair so dev_tools
// can run a session-scoped grant check before falling through to a
// path-error result.
type pathGrantCtxKey struct{}

// pathGrantCtxValue is the value stored on the context.
type pathGrantCtxValue struct {
	sessionID string
	checker   PathGrantChecker
}

// WithPathGrants returns a new context carrying the (sessionID,
// checker) pair. A nil checker or empty sessionID returns ctx unchanged
// so callers may pass through unconditionally.
func WithPathGrants(ctx context.Context, sessionID string, checker PathGrantChecker) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if sessionID == "" || checker == nil {
		return ctx
	}
	return context.WithValue(ctx, pathGrantCtxKey{}, pathGrantCtxValue{sessionID, checker})
}

// PathGrantsFromContext returns the (sessionID, checker) pair stamped
// by WithPathGrants, or ("", nil) if none was stamped. dev_tools
// resolveAllowed reads this when the standard AllowedPaths list rejects
// the user-supplied path; if the checker accepts it, the call proceeds.
func PathGrantsFromContext(ctx context.Context) (sessionID string, checker PathGrantChecker) {
	if ctx == nil {
		return "", nil
	}
	v, _ := ctx.Value(pathGrantCtxKey{}).(pathGrantCtxValue)
	return v.sessionID, v.checker
}
