package main

import (
	"runtime/debug"
)

const (
	minMemoryLimit     = 512 << 20 // bytes: a limit below this makes the collector run all the time
	unknownMemoryLimit = 2 << 30   // bytes: the limit when the physical memory cannot be read
)

// defaultMemoryLimit is the soft memory limit the app sets when GOMEMLIMIT is not: half the physical memory, never
// below minMemoryLimit, and unknownMemoryLimit when physical is 0 (not known).
func defaultMemoryLimit(physical uint64) int64 {
	if physical == 0 {
		return unknownMemoryLimit
	}
	return max(int64(min(physical/2, 1<<62)), minMemoryLimit)
}

// setMemoryLimit applies defaultMemoryLimit unless GOMEMLIMIT is set, which the runtime has already read and which
// the owner of the computer then decides. It returns the limit it set, or 0 when it left the runtime's own.
func setMemoryLimit(getenv func(string) string, physical uint64) int64 {
	if getenv("GOMEMLIMIT") != "" {
		return 0
	}
	limit := defaultMemoryLimit(physical)
	debug.SetMemoryLimit(limit)
	return limit
}
