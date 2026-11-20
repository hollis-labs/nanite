package warden

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"
)

// lockFile takes an exclusive advisory lock on path (created 0600), so two
// Nanite processes sharing a parent file never present the same parent. The
// Warden Go client takes the same lock but ignores <file>.inflight, so it is
// not safe to share a file with it (see the package doc). It gives up when
// ctx ends.
func lockFile(ctx context.Context, path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600) // #nosec G304 -- the configured parent credential's lock
	if err != nil {
		return nil, err
	}
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) // #nosec G115 -- File.Fd carries a Unix file descriptor.
		if err == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) // #nosec G115 -- File.Fd carries a Unix file descriptor.
				_ = f.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = f.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}
