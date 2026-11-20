package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	kololog "github.com/ciranda-dev/kolo-log"
)

// watch replaces the disk steps of writeFileAtomic with recorders for one test.
func watch(t *testing.T) *[]string {
	t.Helper()
	var steps []string
	oldSync, oldRename, oldDir := syncFile, renameFile, syncDir
	t.Cleanup(func() { syncFile, renameFile, syncDir = oldSync, oldRename, oldDir })
	syncFile = func(f *os.File) error { steps = append(steps, "sync file"); return oldSync(f) }
	renameFile = func(a, b string) error { steps = append(steps, "rename"); return oldRename(a, b) }
	syncDir = func(d string) error { steps = append(steps, "sync dir"); return oldDir(d) }
	return &steps
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	items, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, it := range items {
		out = append(out, it.Name())
	}
	return out
}

// The file is synced before the rename and the folder after it, so a power loss leaves the old bytes or the new ones.
func TestWriteFileAtomicSyncsFileThenRenameThenFolder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conversation.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	steps := watch(t)
	if err := writeFileAtomic(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if want := []string{"sync file", "rename", "sync dir"}; !slices.Equal(*steps, want) {
		t.Fatalf("steps %v, want %v", *steps, want)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "new" {
		t.Fatalf("content %q, %v", b, err)
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v, %v", fi.Mode(), err)
		}
	}
	if got := names(t, dir); !slices.Equal(got, []string{"conversation.json"}) {
		t.Fatalf("folder holds %v", got)
	}
}

// A failure at any step leaves the old file as it was and no temporary file behind.
func TestWriteFileAtomicFailureKeepsTheOldFile(t *testing.T) {
	boom := errors.New("injected")
	for name, inject := range map[string]func(){
		"file sync": func() { syncFile = func(*os.File) error { return boom } },
		"rename":    func() { renameFile = func(string, string) error { return boom } },
	} {
		dir := t.TempDir()
		path := filepath.Join(dir, "conversation.json")
		if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
		watch(t)
		inject()
		if err := writeFileAtomic(path, []byte("new"), 0o600); !errors.Is(err, boom) {
			t.Fatalf("%s: %v", name, err)
		}
		if b, err := os.ReadFile(path); err != nil || string(b) != "old" {
			t.Fatalf("%s: content %q, %v", name, b, err)
		}
		if got := names(t, dir); !slices.Equal(got, []string{"conversation.json"}) {
			t.Fatalf("%s: folder holds %v", name, got)
		}
	}
}

// A failed folder sync is reported: the rename happened but is not known to be durable.
func TestWriteFileAtomicReportsFolderSyncFailure(t *testing.T) {
	boom := errors.New("injected")
	watch(t)
	syncDir = func(string) error { return boom }
	if err := writeFileAtomic(filepath.Join(t.TempDir(), "x"), []byte("v"), 0o600); !errors.Is(err, boom) {
		t.Fatal(err)
	}
}

// A temporary file that a crash left, with wider rights, is replaced and the result is mode 0600.
func TestWriteFileAtomicReplacesALeftTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "members.json")
	if err := os.WriteFile(path+tmpSuffix, []byte("half a wri"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "{}" {
		t.Fatalf("content %q, %v", b, err)
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v, %v", fi.Mode(), err)
		}
	}
	if got := names(t, dir); !slices.Equal(got, []string{"members.json"}) {
		t.Fatalf("folder holds %v", got)
	}
}

// Every state file of the app goes through the helper: the conversation key, the member list, the directory log id, the
// local name and the notch settings.
func TestStateFilesAreWrittenAtomically(t *testing.T) {
	dir := t.TempDir()
	steps := watch(t)
	if err := saveConversation(dir, conversation{}); err != nil {
		t.Fatal(err)
	}
	if err := saveMembers(dir, map[string]*member{}); err != nil {
		t.Fatal(err)
	}
	if err := saveDirectoryID(dir, kololog.LogID{}); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(dir, "local.json"), map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if err := saveNotchSettings(dir, NotchSettings{}); err != nil {
		t.Fatal(err)
	}
	if len(*steps) != 5*3 {
		t.Fatalf("steps %v: five files should each sync, rename and sync the folder", *steps)
	}
}
