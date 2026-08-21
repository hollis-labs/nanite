// Package skillvendor is the content-addressed vendored store for authored
// skill packages (SKILL.md + optional scripts/, references/, assets/
// subdirectories).
//
// See docs/engineering/architecture/20-skills.md's "The model: DB is an
// index, a vendored store is content" section: install/sync reads an
// operator-authored package, computes a content hash over the whole file
// tree, and vendors a copy into this store — keyed by hash, immutable once
// written. The `skills` index table (internal/store, TASKS/skills/02) never
// holds SKILL.md body text, script contents, or asset bytes; it only holds
// the address this package hands back from Write. Materialization
// (TASKS/skills/06) always reads the vendored copy live through Path/
// ReadFiles, every time a skill is used — never a value cached in the DB
// at install time.
//
// # Addressing
//
// An address is deterministic over the exact set of (relative path, file
// bytes) pairs a package contains: sort the tree-relative paths, hash each
// path+content pair into one running sha256 (null-byte-separated, following
// internal/contextbroker/stash.go's DeterministicArtifactID pattern),
// truncate to 16 hex chars, and prefix with "skl-vendor-". The prefix is
// deliberately distinct from stash.go's "art-stash-" family so the two
// content-addressed systems are never confused when debugging — a stash
// artifact and a vendored skill package are unrelated concepts that happen
// to share the same underlying content-addressing technique.
//
// Two byte-identical trees — same relative paths, same file contents,
// written in any order — always produce the same address. Reordering the
// map never changes the result; only path or byte differences do.
//
// # Immutability
//
// Once an address's directory exists on disk, this package never mutates
// its contents in place. There is no "update this address" operation, by
// design — a content change always produces a new address, because the
// address IS a hash of the content. Write is idempotent: writing the same
// address twice (whether because the caller re-installs unchanged content,
// or because two concurrent installs race on the same content) is a safe
// no-op that reports Reused=true. The only supported mutation is Delete,
// which removes an address's directory wholesale (for uninstall,
// TASKS/skills/12) — never a partial or in-place content replacement.
//
// # Corruption detection
//
// Because an address is nothing more than a hash of its own content, this
// package can always independently verify that what's on disk still
// matches what the address claims. Write's idempotency fast-path and every
// Path/ReadFiles call re-hash the on-disk tree and compare it against the
// requested address; a mismatch (bit rot, manual tampering, a half-written
// directory from an out-of-band process) surfaces as ErrCorrupted rather
// than silently serving stale or wrong bytes. A missing address directory
// (the index still references an address whose files are gone — e.g. an
// operator wiped the data directory) surfaces as ErrAddressMissing. Unlike
// internal/service/slot_stash.go's artifact case, there is no
// re-derive-from-source fallback here: the original source-of-truth is an
// operator's local package path that may no longer be reachable, so both
// error cases are real, typed failures the caller (TASKS/skills/04) must
// handle explicitly rather than something this package papers over.
//
// # Write ordering / crash safety
//
// Write stages every file into a temporary sibling directory first (each
// file written via internal/fsutil's crash-safe atomic-write-then-rename
// primitive), then publishes the whole package atomically via a single
// os.Rename of the staging directory onto the final address directory.
// Concurrent writers computing the same address for the same content race
// on that rename; the loser's rename fails (target already exists), its
// staging directory is discarded, and it falls into the same
// verify-and-reuse path Write's idempotency fast-path uses — converging on
// Reused=true rather than erroring. This store intentionally has no DB row
// of its own: the `skills` index table is the pointer into this store, and
// a caller that writes files here then separately upserts its index row is
// expected to potentially crash in between — this store's job is only to
// guarantee that whatever address it hands back is either fully present
// and verifiably correct, or a real error, never a partial or dangling
// write.
package skillvendor
