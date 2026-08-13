//go:build unix

package main

import "syscall"

// lockExclusiveNonBlocking attempts a non-blocking exclusive flock on fd.
// It returns a non-nil error (typically syscall.EWOULDBLOCK) when another
// process already holds the lock — the caller's cue to treat itself as the
// "loser" of the double-spawn race (CW-20260813-0007 item 3) and wait
// instead of spawning its own `nanite serve`.
func lockExclusiveNonBlocking(fd int) error {
	return syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
}

// unlockFile releases a lock taken by lockExclusiveNonBlocking.
func unlockFile(fd int) {
	_ = syscall.Flock(fd, syscall.LOCK_UN)
}

// detachedProcAttr puts the spawned `nanite serve` in its own session via
// Setsid, detaching it from this process's controlling terminal so it
// survives `nanite chat` exiting or its parent terminal closing (item 2).
func detachedProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
