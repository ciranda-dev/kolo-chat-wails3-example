//go:build linux

package main

import "golang.org/x/sys/unix"

// physicalMemory returns the installed memory in bytes, or 0 when it cannot be read.
func physicalMemory() uint64 {
	var si unix.Sysinfo_t
	if unix.Sysinfo(&si) != nil {
		return 0
	}
	return uint64(si.Totalram) * uint64(si.Unit)
}
