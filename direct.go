package main

import (
	"crypto/sha256"
	"errors"
	"log"
	"slices"

	kololog "github.com/ciranda-dev/kolo-log"
	kolostore "github.com/ciranda-dev/kolo-store"
)

// maxDirectPending bounds the direct messages per author that wait for their log entry, and maxMismatched the
// mismatches kept per member; past either, nothing more is recorded for that author.
const (
	maxDirectPending = 256
	maxMismatched    = 256
)

// rememberDirect keeps the hash of a message shown directly at index, so that its log entry can be checked against it:
// a member's direct text and its signed entry are one value when its app is honest.
func (c *ChatService) rememberDirect(author string, index uint64, sealed []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.direct == nil {
		c.direct = map[string]map[uint64][32]byte{}
	}
	if len(c.direct[author]) >= maxDirectPending {
		return
	}
	if c.direct[author] == nil {
		c.direct[author] = map[uint64][32]byte{}
	}
	c.direct[author][index] = sha256.Sum256(sealed)
}

// checkDirect compares the log entries of rep that were shown directly with what was shown, and marks the member
// whose entry differs. An entry without a key yet waits for the next call. It says whether it marked a new mismatch.
func (c *ChatService) checkDirect(author string, rep *kolostore.Replica, refused map[uint64]bool) bool {
	c.mu.Lock()
	pending := make(map[uint64][32]byte, len(c.direct[author]))
	for i, h := range c.direct[author] {
		pending[i] = h
	}
	c.mu.Unlock()
	var done, bad []uint64
	for index, want := range pending {
		if index >= rep.Len() {
			continue
		}
		sealed, err := rep.Get(index)
		switch {
		case errors.Is(err, kololog.ErrKeyUnavailable) && !refused[index]:
			continue
		case err == nil && sha256.Sum256(sealed) != want:
			bad = append(bad, index)
		}
		done = append(done, index)
	}
	if len(done) == 0 {
		return false
	}
	slices.Sort(bad)
	c.mu.Lock()
	for _, i := range done {
		delete(c.direct[author], i)
	}
	m := c.members[author]
	if m == nil || len(bad) == 0 {
		c.mu.Unlock()
		return false
	}
	for _, i := range bad {
		if len(m.Mismatched) < maxMismatched && !slices.Contains(m.Mismatched, i) {
			m.Mismatched = append(m.Mismatched, i)
		}
	}
	err := saveMembers(c.set.DataDir, c.members)
	c.mu.Unlock()
	if err != nil {
		log.Print("members: ", err)
	}
	log.Printf("%d messages of %s differ from the entries it signed", len(bad), author[:8])
	return true
}
