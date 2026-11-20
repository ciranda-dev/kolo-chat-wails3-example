//go:build !windows

package main

import (
	"errors"
	"os"
	"syscall"
)

// syncDirectory makes a rename in dir durable. A filesystem that cannot sync a folder says EINVAL: nothing to do there.
func syncDirectory(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	if err := d.Sync(); err != nil && !errors.Is(err, syscall.EINVAL) {
		return err
	}
	return nil
}
