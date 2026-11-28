//go:build linux

package procstat

import "syscall"

// prSetDumpable is PR_SET_DUMPABLE from <linux/prctl.h>.
const prSetDumpable = 4

// makeReadable re-enables /proc/self/io, which the kernel hides from a process that dropped root (PUID/PGID).
func makeReadable() {
	_, _, _ = syscall.RawSyscall(syscall.SYS_PRCTL, prSetDumpable, 1, 0)
}
