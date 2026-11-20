//go:build unix && !linux && !darwin && !freebsd && !netbsd

package authored

import "syscall"

func statChangeTime(st *syscall.Stat_t) int64 {
	return int64(st.Ctim.Sec)*1e9 + int64(st.Ctim.Nsec) //nolint:unconvert // Timespec widths differ by platform.
}
