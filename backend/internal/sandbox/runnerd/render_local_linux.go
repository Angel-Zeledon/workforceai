//go:build linux

package runnerd

import (
	"syscall"
	"unsafe"
)

// limitChild sets resource limits on the render child with prlimit(2).
// RLIMIT_AS bounds the address space (the poppler tools inherit it), CPU the
// seconds of processor time, NOFILE the descriptors, FSIZE the biggest file it
// can write and CORE disables dumps. (RLIMIT_NPROC is per user, so it would
// also bind this service; the container's pids_limit covers that.)
func limitChild(pid, memMB int) error {
	var first error
	set := func(res int, v uint64) {
		r := syscall.Rlimit{Cur: v, Max: v}
		_, _, e := syscall.RawSyscall6(syscall.SYS_PRLIMIT64, uintptr(pid), uintptr(res), uintptr(unsafe.Pointer(&r)), 0, 0, 0)
		if e != 0 && first == nil {
			first = e
		}
	}
	set(syscall.RLIMIT_AS, uint64(memMB)<<20)
	set(syscall.RLIMIT_CPU, 60)
	set(syscall.RLIMIT_NOFILE, 128)
	set(syscall.RLIMIT_FSIZE, 64<<20)
	set(syscall.RLIMIT_CORE, 0)
	return first
}
