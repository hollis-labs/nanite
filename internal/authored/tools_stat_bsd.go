//go:build freebsd || netbsd

package authored

import "syscall"

func statChangeTime(st *syscall.Stat_t) int64 {
	return int64(st.Ctimespec.Sec)*1e9 + int64(st.Ctimespec.Nsec) //nolint:unconvert // Timespec widths differ by architecture.
}
