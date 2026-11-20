package main

import (
	"log"
	"strings"
	"unicode"
)

// maxReportRunes bounds a window's error in the log: 8192 characters, the bound of the same report in the board app (a
// title there: kolo-braid's MaxViewKey of 32 KiB over utf8.UTFMax).
const maxReportRunes = 8192

// ReportError writes an error of a window, the main one or the notch, to the log, so that a person who runs the app from a
// terminal sees why a window failed. It is on NotchService, which both windows call, since the notch page has no way to
// the chat. The window sends the error's message and its stack only, never message text, invites or keys; the text is
// cleaned of control characters and cut to maxReportRunes characters.
func (n *NotchService) ReportError(message, stack string) {
	log.Print("frontend error: ", cleanReport(message+"\n"+stack))
}

// cleanReport keeps the lines of a report without invalid UTF-8, control or format characters, which could rewrite the
// terminal, and cuts it to maxReportRunes characters.
func cleanReport(s string) string {
	s = strings.Map(func(r rune) rune {
		if r != '\n' && (unicode.IsControl(r) || unicode.Is(unicode.Cf, r)) {
			return -1
		}
		return r
	}, strings.ToValidUTF8(s, ""))
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > maxReportRunes {
		s = string(r[:maxReportRunes])
	}
	return s
}
