package main

import (
	"runtime/debug"
	"testing"
)

func TestDefaultMemoryLimit(t *testing.T) {
	const gib = 1 << 30
	for name, tc := range map[string]struct {
		physical uint64
		want     int64
	}{
		"unknown":          {0, 2 * gib},
		"tiny":             {256 << 20, 512 << 20},
		"just under floor": {gib - 1, 512 << 20},
		"1 GiB":            {gib, 512 << 20},
		"8 GiB":            {8 * gib, 4 * gib},
		"64 GiB":           {64 * gib, 32 * gib},
		"max":              {^uint64(0), 1 << 62},
	} {
		if got := defaultMemoryLimit(tc.physical); got != tc.want {
			t.Errorf("%s: %d, want %d", name, got, tc.want)
		}
	}
}

// GOMEMLIMIT, when set, is left to the runtime; otherwise the default is set on the runtime.
func TestSetMemoryLimit(t *testing.T) {
	before := debug.SetMemoryLimit(-1)
	t.Cleanup(func() { debug.SetMemoryLimit(before) })

	env := func(v string) func(string) string {
		return func(k string) string {
			if k != "GOMEMLIMIT" {
				t.Fatalf("read %q", k)
			}
			return v
		}
	}
	debug.SetMemoryLimit(1 << 40)
	if got := setMemoryLimit(env("3GiB"), 8<<30); got != 0 || debug.SetMemoryLimit(-1) != 1<<40 {
		t.Fatalf("with GOMEMLIMIT: returned %d, runtime limit %d", got, debug.SetMemoryLimit(-1))
	}
	if got := setMemoryLimit(env(""), 8<<30); got != 4<<30 || debug.SetMemoryLimit(-1) != 4<<30 {
		t.Fatalf("without GOMEMLIMIT: returned %d, runtime limit %d", got, debug.SetMemoryLimit(-1))
	}
}

// This system's memory is readable and gives a limit of at least the floor.
func TestPhysicalMemory(t *testing.T) {
	if p := physicalMemory(); p == 0 {
		t.Skip("physical memory is not read on this system")
	} else if defaultMemoryLimit(p) < minMemoryLimit {
		t.Fatalf("limit %d", defaultMemoryLimit(p))
	}
}
