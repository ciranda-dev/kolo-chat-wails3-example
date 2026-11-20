//go:build !darwin && !linux && !windows

package main

// physicalMemory returns 0: the memory of this system is not read, so the default limit applies.
func physicalMemory() uint64 { return 0 }
