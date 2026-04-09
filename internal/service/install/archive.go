package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// ArchiveBase is the parent directory where all project archives live.
const ArchiveBase = "~/Projects-apps/.archived"

// ExpandArchiveBase expands a leading "~" in path to the user's home
// directory. Paths without a "~" prefix are returned unchanged.
func ExpandArchiveBase(path string) (string, error) {
	if path == "" || path[0] != '~' {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, path[1:]), nil
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

// ArchiveProjectAgentrc moves .agentrc/ (and .agentrc-legacy/ if present)
// from projectDir into a new archive directory under archiveBase. Returns
// the archive directory path.
func ArchiveProjectAgentrc(projectDir, archiveBase, basename string, ts time.Time) (string, error) {
	archiveDir, err := ResolveArchiveDir(archiveBase, basename, ts)
	if err != nil {
		return "", fmt.Errorf("resolve archive dir: %w", err)
	}
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir archive %s: %w", archiveDir, err)
	}

	if err := moveIfExists(
		filepath.Join(projectDir, ".agentrc"),
		filepath.Join(archiveDir, ".agentrc"),
	); err != nil {
		return "", fmt.Errorf("move .agentrc: %w", err)
	}
	if err := moveIfExists(
		filepath.Join(projectDir, ".agentrc-legacy"),
		filepath.Join(archiveDir, ".agentrc-legacy"),
	); err != nil {
		return "", fmt.Errorf("move .agentrc-legacy: %w", err)
	}

	return archiveDir, nil
}

// exists reports whether a filesystem entry (file or directory) exists at path.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// moveIfExists renames src to dst if src exists. Returns nil if src is missing.
func moveIfExists(src, dst string) error {
	_, err := os.Stat(src)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat %s: %w", src, err)
	}
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", src, dst, err)
	}
	return nil
}
