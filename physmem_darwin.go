//go:build darwin

package main

import "golang.org/x/sys/unix"

// physicalMemory returns the installed memory in bytes, or 0 when it cannot be read.
func physicalMemory() uint64 {
	n, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0
	}
	return n
}
