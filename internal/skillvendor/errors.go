package skillvendor

import "errors"

// Sentinel errors. Callers should use errors.Is against these rather than
// string-matching — every method in this package wraps one of these with
// fmt.Errorf("%w: ...") when returning a typed failure.
var (
	// ErrEmptyPackage is returned by Address/Write when the supplied file
	// map has zero entries. A skill package with no files at all isn't a
	// meaningful thing to vendor.
	ErrEmptyPackage = errors.New("skillvendor: file map is empty")

	// ErrInvalidPath is returned when a file map key isn't a valid
	// tree-relative path: empty, absolute, containing ".." components, or
	// colliding with another entry after normalization (e.g. "a/b" and
	// "./a/b" both present).
	ErrInvalidPath = errors.New("skillvendor: invalid relative path in file map")

	// ErrInvalidAddress is returned when a caller-supplied address string
	// doesn't match this package's own address format. Since every address
	// this package hands out is self-generated (skl-vendor-<hex16>), a
	// value that doesn't match was never produced by Write — most likely a
	// caller bug (e.g. passing a stash artifact ID by mistake) rather than
	// a corrupted-on-disk scenario.
	ErrInvalidAddress = errors.New("skillvendor: not a valid skill-vendor address")

	// ErrAddressMissing is returned by Path/ReadFiles when the requested
	// address has no directory on disk at all. There is no re-derivation
	// fallback for this case (unlike an artifact stash) — the original
	// package's source path may no longer be reachable, so this is a real
	// failure the caller must surface rather than silently skip.
	ErrAddressMissing = errors.New("skillvendor: address not found in vendored store")

	// ErrCorrupted is returned when an address's on-disk content no longer
	// hashes to the address itself — bit rot, manual tampering, or a
	// half-written directory from an out-of-band process. Since the
	// address IS the content hash, this can only mean the bytes on disk
	// have changed since they were vendored. Never silently served.
	ErrCorrupted = errors.New("skillvendor: vendored content does not match its address")
)
