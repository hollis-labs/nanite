package registryagent

import (
	"os"

	"golang.org/x/sys/unix"
)

func publish(parent *os.File, stage, target string, replace bool) error {
	flags := uint(unix.RENAME_NOREPLACE)
	if replace {
		flags = unix.RENAME_EXCHANGE
	}
	fd := int(parent.Fd()) // #nosec G115 -- Unix file descriptor.
	return unix.Renameat2(fd, stage, fd, target, flags)
}
