package registryagent

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// preserveState also holds the existing runtime lock for compatibility with
// runtimes opened before publication locking was introduced. The caller owns
// the stable publication lock and keeps both locks through the directory swap.
func preserveState(ctx context.Context, parent *os.Root, base string, staged *os.Root) (*os.File, error) {
	dir, err := parent.OpenRoot(base)
	if err != nil {
		return nil, err
	}
	defer func() { _ = dir.Close() }()
	if info, err := dir.Lstat(".nanite"); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("registryagent: runtime state must be a real directory")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if err := dir.MkdirAll(".nanite", 0700); err != nil {
		return nil, err
	}
	state, err := dir.OpenRoot(".nanite")
	if err != nil {
		return nil, err
	}
	defer func() { _ = state.Close() }()
	guard, err := state.OpenFile("agent.lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = guard.Close()
		}
	}()
	info, err := guard.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("registryagent: runtime lock must be a regular file")
	}
	if err := syscall.Flock(int(guard.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil { // #nosec G115 -- Unix file descriptor.
		return nil, fmt.Errorf("registryagent: agent runtime is open; close it before updating: %w", err)
	}
	if err := staged.MkdirAll(".nanite", 0700); err != nil {
		return nil, err
	}
	err = fs.WalkDir(state.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == "." || path == "materialized.json" {
			return nil
		}
		target := filepath.Join(".nanite", path)
		if entry.IsDir() {
			return staged.Mkdir(target, 0700)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("registryagent: runtime state contains a link or special file")
		}
		source, err := state.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return err
		}
		defer func() { _ = source.Close() }()
		destination, err := staged.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		defer func() { _ = destination.Close() }()
		if _, err := io.Copy(destination, contextReader{ctx, source}); err != nil {
			return err
		}
		return destination.Sync()
	})
	if err != nil {
		return nil, err
	}
	keep = true
	return guard, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
