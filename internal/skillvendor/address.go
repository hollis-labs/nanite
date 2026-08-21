package skillvendor

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// AddressPrefix identifies a skillvendor address. Deliberately distinct
// from internal/contextbroker's "art-stash-" family (see package doc) so
// the two content-addressed systems are never confused when debugging.
const AddressPrefix = "skl-vendor-"

// addressPattern matches exactly the format this package generates:
// AddressPrefix followed by 16 lowercase hex characters (an 8-byte sha256
// prefix, matching stash.go's own truncation length).
var addressPattern = regexp.MustCompile(`^` + regexp.QuoteMeta(AddressPrefix) + `[0-9a-f]{16}$`)

// FileMap is the in-memory representation of a package's file tree: keys
// are tree-relative paths (forward-slash separated, e.g. "scripts/run.sh"),
// values are the raw file bytes. Mirrors the shape
// internal/runtime/agent/bootdir_plant.go's plant.Spec.Files already uses
// for a conceptually similar "here's a set of relative-path -> bytes,
// write them all" operation.
type FileMap map[string][]byte

// Address computes the deterministic content address for files. Two
// FileMaps with identical (path, bytes) pairs — regardless of Go map
// iteration order — always produce the same address; any difference in
// paths or bytes produces a different one.
//
// Returns ErrEmptyPackage if files has no entries, or ErrInvalidPath if any
// key is empty, absolute, escapes the tree root via "..", or collides with
// another key after normalization.
func Address(files FileMap) (string, error) {
	norm, err := normalizeFileMap(files)
	if err != nil {
		return "", err
	}
	return computeAddress(norm), nil
}

// ValidateAddress reports whether address matches this package's own
// generated format (AddressPrefix + 16 hex chars). Every address Write
// returns satisfies this; a value that doesn't was never produced by this
// package.
func ValidateAddress(address string) error {
	if !addressPattern.MatchString(address) {
		return fmt.Errorf("%w: %q", ErrInvalidAddress, address)
	}
	return nil
}

// computeAddress hashes an already-normalized file map (sorted, cleaned,
// collision-free keys). Used both by the public Address() (over a
// caller-supplied map) and internally to re-verify on-disk content (over a
// map produced by readTree, which is normalized by construction).
func computeAddress(norm FileMap) string {
	keys := make([]string, 0, len(norm))
	for k := range norm {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write(norm[k])
		h.Write([]byte{0})
	}
	sum := h.Sum(nil)
	return AddressPrefix + hex.EncodeToString(sum[:8]) // 16 hex chars
}

// normalizeFileMap validates and cleans every key in files, returning a
// fresh map keyed by canonical (forward-slash, Clean'd) relative paths.
func normalizeFileMap(files FileMap) (FileMap, error) {
	if len(files) == 0 {
		return nil, ErrEmptyPackage
	}
	norm := make(FileMap, len(files))
	for relPath, content := range files {
		clean, err := normalizeRelPath(relPath)
		if err != nil {
			return nil, err
		}
		if _, exists := norm[clean]; exists {
			return nil, fmt.Errorf("%w: %q normalizes to %q, which is already present", ErrInvalidPath, relPath, clean)
		}
		norm[clean] = content
	}
	return norm, nil
}

// normalizeRelPath validates rel as a tree-relative path and returns its
// canonical forward-slash form. Rejects empty paths, absolute paths, and
// paths that escape the tree root via "..".
func normalizeRelPath(rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("%w: empty path", ErrInvalidPath)
	}
	slashed := filepath.ToSlash(rel)
	if strings.ContainsRune(slashed, 0) {
		return "", fmt.Errorf("%w: %q contains a null byte", ErrInvalidPath, rel)
	}
	cleaned := path.Clean(slashed)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || path.IsAbs(cleaned) {
		return "", fmt.Errorf("%w: %q", ErrInvalidPath, rel)
	}
	return cleaned, nil
}

// readTree walks dir and returns its contents as a normalized FileMap
// (forward-slash, dir-relative keys). Used to re-derive an on-disk
// address's actual content for verification (Write's idempotency
// fast-path, Path/ReadFiles's live corruption check).
func readTree(dir string) (FileMap, error) {
	files := FileMap{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(dir, p)
		if relErr != nil {
			return relErr
		}
		data, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		files[filepath.ToSlash(rel)] = data
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

// verifyAddress re-hashes the file tree at dir and confirms it equals
// wantAddress. Returns ErrCorrupted (wrapped) on mismatch.
func verifyAddress(dir, wantAddress string) error {
	files, err := readTree(dir)
	if err != nil {
		return fmt.Errorf("skillvendor: read vendored tree for verify: %w", err)
	}
	got := computeAddress(files)
	if got != wantAddress {
		return fmt.Errorf("%w: %s (on-disk content now hashes to %s)", ErrCorrupted, wantAddress, got)
	}
	return nil
}
