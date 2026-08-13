//go:build !unix

package main

import "syscall"

func lockExclusiveNonBlocking(fd int) error {
	return errAutoStartUnsupported
}

func unlockFile(fd int) {}

func detachedProcAttr() *syscall.SysProcAttr {
	return nil
}
