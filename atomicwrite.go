package main

import (
	"os"
	"path/filepath"
)

// tmpSuffix ends the name of the file a write goes through; a crash leaves it, and the next write replaces it.
const tmpSuffix = ".tmp"

// The steps of writeFileAtomic that touch the disk, as variables so a test can fail or watch each one.
var (
	syncFile   = func(f *os.File) error { return f.Sync() }
	renameFile = os.Rename
	syncDir    = syncDirectory
)

// writeFileAtomic replaces the file at path with data in one step: a temporary file in the same folder is written,
// synced, renamed over path, and the folder is synced. After a crash or a power loss the file holds the old bytes or
// the new ones, never an empty or half-written file.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + tmpSuffix
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	fail := func(err error) error {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Chmod(perm); err != nil {
		return fail(err)
	}
	if _, err := f.Write(data); err != nil {
		return fail(err)
	}
	if err := syncFile(f); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := renameFile(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return syncDir(filepath.Dir(path))
}
