package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// PreparePrivateDB ensures Nanite's owned database and state directory use
// private modes before SQLite opens them and creates its sidecars.
func PreparePrivateDB(dir string) error {
	if err := PreparePrivateDir(dir); err != nil {
		return err
	}
	if err := privateFile(filepath.Join(dir, "nanite.db"), true); err != nil {
		return err
	}
	for _, name := range []string{"nanite.db-wal", "nanite.db-shm", "nanite.db-journal"} {
		if err := privateFile(filepath.Join(dir, name), false); err != nil {
			return err
		}
	}
	return nil
}

// PreparePrivateDir creates the agent data root with private permissions.
func PreparePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("Nanite data path %q must be a real directory", dir)
	}
	return os.Chmod(dir, 0700)
}

func privateFile(path string, create bool) error {
	if create {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err == nil {
			if err := file.Close(); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrExist) {
			return err
		}
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) && !create {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("Nanite data path %q must be a regular file", path)
	}
	return os.Chmod(path, 0600)
}
