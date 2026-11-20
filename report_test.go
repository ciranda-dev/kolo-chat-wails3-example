package main

import (
	"log"
	"os"
	"strings"
	"testing"
)

// A window's error reaches the log as one entry, cleaned and cut to maxReportRunes characters, and nothing else is logged.
func TestReportError(t *testing.T) {
	var buf strings.Builder
	log.SetOutput(&buf)
	flags := log.Flags()
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(os.Stderr)
		log.SetFlags(flags)
	})
	n := NewNotchService(Config{DataDir: t.TempDir()})

	n.ReportError("TypeError: x is null\x1b[31m\u202e", "    at App\n    at Sidebar\n")
	if got, want := buf.String(), "frontend error: TypeError: x is null[31m\n    at App\n    at Sidebar\n"; got != want {
		t.Errorf("logged %q, want %q", got, want)
	}

	buf.Reset()
	long := "TypeError: " + strings.Repeat("é", 2*maxReportRunes)
	n.ReportError(long, "    at App")
	if got, want := buf.String(), "frontend error: "+string([]rune(long)[:maxReportRunes])+"\n"; got != want {
		t.Errorf("logged %d characters, want the %d of the bound and nothing else", len([]rune(got)), len([]rune(want)))
	}
}
