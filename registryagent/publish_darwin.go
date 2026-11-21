package registryagent

import (
	"os"

	"golang.org/x/sys/unix"
)

func publish(parent *os.File, stage, target string, replace bool) error {
	flags := uint32(unix.RENAME_EXCL)
	if replace {
		flags = unix.RENAME_SWAP
	}
	fd := int(parent.Fd()) // #nosec G115 -- Unix file descriptor.
	return unix.RenameatxNp(fd, stage, fd, target, flags)
}
