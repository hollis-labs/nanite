package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ArchiveBase is the parent directory where all project archives live.
const ArchiveBase = "~/Projects-apps/.archived"

// ExpandArchiveBase expands a leading "~" or "~/" in path to the user's
// home directory. Paths without a tilde prefix are returned unchanged.
// The form "~user" (another user's home) is NOT supported and is returned
// as-is, since Nanite has no use case for it.
func ExpandArchiveBase(path string) (string, error) {
	if path == "" || path[0] != '~' {
		return path, nil
	}
	// "~user" (no slash) is not supported — return as-is.
	if path != "~" && path[1] != '/' {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	if path == "~" {
		return home, nil
	}
	// path starts with "~/" — strip the two leading chars before joining.
	return filepath.Join(home, path[2:]), nil
}

// ResolveArchiveDir picks a non-colliding archive dir path for the project.
// Default form: {base}/{basename}-YYYY-MM-DD. If the date-only candidate
// already exists, appends an HHMMSS suffix. If that also collides, appends
// a sequence suffix (-1, -2, ...). Returns an error after 1000 sequence
// attempts.
func ResolveArchiveDir(base, projectBasename string, ts time.Time) (string, error) {
	if base == "" {
		return "", errors.New("empty archive base")
	}
	if projectBasename == "" {
		return "", errors.New("empty project basename")
	}
	ts = ts.UTC()
	date := ts.Format("2006-01-02")

	candidate := filepath.Join(base, fmt.Sprintf("%s-%s", projectBasename, date))
	if !exists(candidate) {
		return candidate, nil
	}

	hhmmss := ts.Format("150405")
	candidate = filepath.Join(base, fmt.Sprintf("%s-%s-%s", projectBasename, date, hhmmss))
	if !exists(candidate) {
		return candidate, nil
	}

	for i := 1; i < 1000; i++ {
		c := filepath.Join(base, fmt.Sprintf("%s-%s-%s-%d", projectBasename, date, hhmmss, i))
		if !exists(c) {
			return c, nil
		}
	}
	return "", fmt.Errorf("too many archive collisions for %s/%s", projectBasename, date)
}

// exists reports whether a filesystem entry (file or directory) exists at path.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
