// Package artifactstore owns the filesystem rules shared by artifact transports.
package artifactstore

import (
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/go-safefs/pathsafe"
)

// DefaultStorageDir is the root used when no artifact storage root is configured.
const DefaultStorageDir = "data/artifacts"

// PlacedFile is the inspected metadata of a confined regular file.
type PlacedFile struct {
	Path string
	MIME string
	Size int64
}

// InspectPlaced validates an existing file before either transport records a
// placed artifact. Relative paths are relative to root; absolute paths and
// symlinks are accepted only when their resolved target stays under root.
func InspectPlaced(root, storagePath, name, mimeType string) (PlacedFile, error) {
	if root == "" {
		root = DefaultStorageDir
	}
	resolved, err := Resolve(root, storagePath)
	if err != nil {
		var escape *pathsafe.EscapeError
		if errors.As(err, &escape) {
			return PlacedFile{}, errors.New("artifact path outside storage root")
		}
		return PlacedFile{}, fmt.Errorf("invalid storage_path: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return PlacedFile{}, fmt.Errorf("storage_path must name an existing file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return PlacedFile{}, errors.New("storage_path must name a regular file")
	}
	if mimeType == "" {
		mimeType = mime.TypeByExtension(filepath.Ext(name))
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
	}
	return PlacedFile{Path: resolved, MIME: mimeType, Size: info.Size()}, nil
}

// Resolve confines storagePath to root, resolving symlinks and preserving the
// typed pathsafe escape error used by download's defense-in-depth check.
func Resolve(root, storagePath string) (string, error) {
	if !filepath.IsAbs(storagePath) {
		return pathsafe.ResolveUnder(root, storagePath)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve artifacts root: %w", err)
	}
	if canonical, evalErr := filepath.EvalSymlinks(absRoot); evalErr == nil {
		absRoot = canonical
	}
	absTarget, err := filepath.Abs(storagePath)
	if err != nil {
		return "", fmt.Errorf("resolve artifact path: %w", err)
	}
	if canonical, evalErr := filepath.EvalSymlinks(absTarget); evalErr == nil {
		absTarget = canonical
	}
	rel, err := filepath.Rel(absRoot, absTarget)
	if err != nil {
		return "", fmt.Errorf("resolve artifact path relative to root: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", &pathsafe.EscapeError{
			Root:     absRoot,
			Attempt:  storagePath,
			Resolved: absTarget,
			Cause:    errors.New("resolved path outside root"),
		}
	}
	return pathsafe.ResolveUnder(absRoot, rel)
}
