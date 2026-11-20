//go:build windows

package main

// syncDirectory does nothing: Windows has no folder sync.
func syncDirectory(string) error { return nil }
