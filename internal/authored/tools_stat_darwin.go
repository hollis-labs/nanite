package authored

import "syscall"

func statChangeTime(st *syscall.Stat_t) int64 { return st.Ctimespec.Sec*1e9 + st.Ctimespec.Nsec }
