// Package agentlock coordinates authored directory replacement with runtimes.
package agentlock

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Publication takes a stable sibling lock. Runtimes hold a shared lock for
// their lifetime; publishers take an exclusive lock across state copy and swap.
// The lock is outside the replaced directory so its inode survives publication.
func Publication(dir string, shared bool) (*os.File, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	// Resolve an existing directory (including aliases used by embedding hosts).
	// For a new destination, resolve only its existing parent.
	resolved, err := filepath.EvalSymlinks(abs)
	if os.IsNotExist(err) {
		parent, e := filepath.EvalSymlinks(filepath.Dir(abs))
		if e != nil {
			return nil, e
		}
		resolved = filepath.Join(parent, filepath.Base(abs))
	} else if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(filepath.Dir(resolved))
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	f, err := root.OpenFile(".nanite-publish-"+filepath.Base(resolved)+".lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("agent publication lock must be a regular file")
	}
	mode := syscall.LOCK_EX
	if shared {
		mode = syscall.LOCK_SH
	}
	if err := syscall.Flock(int(f.Fd()), mode|syscall.LOCK_NB); err != nil { // #nosec G115 -- Unix file descriptor.
		_ = f.Close()
		return nil, fmt.Errorf("agent directory is open or being updated: %w", err)
	}
	return f, nil
}
