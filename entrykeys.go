package main

//lint:file-ignore ST1005 these errors are sentences the window shows as they are

// Entry keys, derived from the conversation key.
//
// kolo-log seals every entry of a log under its own entry key. Both of our logs, the messages and the directory,
// use kolo-log's derived-key mode (kololog.WithDerivedKeys): an entry's key comes from the entry secret, the log and
// the entry's index. The entry secret comes from the conversation key under its own label, so every member derives
// every key and no entry key is ever sent: members and pins serve ciphertext only, and a message is read from a pin
// while its sender is offline.
//
// After each pull, unlock derives the keys of the entries that have none and gives them to the replica with
// AddKeys. A key that the author's signed record proves wrong is refused for good: show skips that entry and the
// window marks the author. Any other error refuses nothing, and the next pull tries again.

import (
	"context"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"log"
	"slices"

	kololog "github.com/ciranda-dev/kolo-log"
	kolostore "github.com/ciranda-dev/kolo-store"
	kolosync "github.com/ciranda-dev/kolo-sync"
)

const (
	entrySecretLabel = "kolochat/entry-secret/v1"
	// logEpoch is the epoch of every log: removing a member does not change the conversation key, so the chat never
	// moves to a new one.
	logEpoch = 0
	// maxRefused is how many refused entries an author may have in its two logs, the most messages the history
	// holds. Past it the author is quarantined: its logs are no longer pulled, derived or shown.
	maxRefused = maxHistory
)

var (
	errNotDerived = errors.New("its entry keys are not derived from the conversation key")
	errBadKey     = errors.New("its signed record does not commit to the derived key")
)

// DataDirError is returned for a folder Kolo Chat cannot use, and nothing in it is changed. A Layout other than the
// current one is a data folder in another layout (-1: its layout.json is damaged), and Open one that other users can
// open. In a conversation's folder, Logs says its logs do not use the derived entry keys, and neither says a member's
// conversation.json is gone. Dir is that folder. The binding marshals it, so the window shows how to go on.
type DataDirError struct {
	Dir    string `json:"dir"`
	Logs   bool   `json:"logs,omitempty"`
	Layout int    `json:"layout,omitempty"`
	Open   bool   `json:"open,omitempty"` // other users can open the folder
}

func (e *DataDirError) Error() string {
	switch {
	case e.Open:
		return "The data folder " + e.Dir + " is open to other users, who could change what Kolo Chat keeps in it. " +
			"Run chmod 700 on it, or start with another -data. Nothing in the folder is changed."
	case e.Layout != 0:
		return fmt.Sprintf("The data folder %s is not in a layout Kolo Chat can open (layout %d). Start with another "+
			"-data. Nothing in the folder is changed.", e.Dir, e.Layout)
	}
	why := "holds the logs of a conversation whose conversation.json is gone"
	if e.Logs {
		why = "holds logs that do not use the derived entry keys, and Kolo Chat cannot use them"
	}
	return "The data folder " + e.Dir + " " + why + ". Quit Kolo Chat, move or rename that folder (nothing in it " +
		"is deleted), then open Kolo Chat again and paste a new invite. An owner starts a new conversation instead."
}

// pulled runs in the pull section, between the pull and the derivation. Tests hold it there.
var pulled = func() {}

// entrySecret derives the entry secret from the conversation key, so kolo-log never gets the conversation key.
func entrySecret(conversationKey []byte) (*kololog.EntrySecret, error) {
	b, err := hkdf.Key(sha256.New, conversationKey, nil, entrySecretLabel, kololog.EntrySecretSize)
	if err != nil {
		return nil, err
	}
	s := new(kololog.EntrySecret)
	copy(s[:], b)
	clear(b)
	return s, nil
}

// derivedKeys is the option every one of our logs is created and opened with.
func derivedKeys(secret *kololog.EntrySecret) kololog.Option {
	return kololog.WithDerivedKeys(secret, logEpoch)
}

// checkDerived refuses the header of a member's log that does not derive its keys at our epoch.
func checkDerived(h kololog.LogHeader) error {
	if h.Keys != kololog.DerivedKeys || h.Epoch != logEpoch {
		return errNotDerived
	}
	return nil
}

// joinHeader creates our two logs, empty, and returns the header of the messages log, which a joiner sends with its
// request before it has the conversation key. A header names no secret, and enter opens the logs again with the
// derived one: kolo-log checks a secret against the newest entry, and these have none.
func (c *ChatService) joinHeader() ([]byte, error) {
	var unused kololog.EntrySecret
	if _, err := rand.Read(unused[:]); err != nil {
		return nil, err
	}
	msgs, dir, err := openOwnLogs(c.store, c.id, c.set.DataDir, &unused)
	clear(unused[:])
	if err != nil {
		return nil, ownLogError(err, c.set.DataDir, true)
	}
	header := msgs.Header().Encode()
	return header, errors.Join(msgs.Close(), dir.Close())
}

// ownLogError says why one of our logs does not open with an entry secret. A joiner has no key of its own, so a
// secret that does not match means its conversation.json is gone.
func ownLogError(err error, dataDir string, joiner bool) error {
	switch {
	case errors.Is(err, kololog.ErrKeyModeMismatch):
		return &DataDirError{Dir: dataDir, Logs: true}
	case errors.Is(err, kololog.ErrSecretMismatch) && joiner:
		return &DataDirError{Dir: dataDir}
	case errors.Is(err, kololog.ErrSecretMismatch):
		return &ConfigError{Setting: "conversation-key", Err: errOtherConversation}
	}
	return err
}

// pull brings a member's log up to date from conn, a pin or the member, as ciphertext, and derives the keys of what
// it holds before pullMu is released. A quarantined author is not pulled at all.
func (c *ChatService) pull(ctx context.Context, conn kolosync.Conn, author string, rep *kolostore.Replica) error {
	if _, quarantined := c.refusedOf(author, rep.ID()); quarantined {
		return nil
	}
	c.pullMu.Lock()
	defer c.pullMu.Unlock()
	err := kolosync.PullCiphertext(ctx, conn, rep)
	pulled()
	c.unlock(author, rep) // also after a failed pull: it retries what an earlier one could not store
	return err
}

// unlock gives every entry of rep that has no key its derived key, in AddKeys calls of at most MaxAddKeys. It starts
// at 0, not at the shown count, since a message shown directly comes in its sync after it was counted.
func (c *ChatService) unlock(author string, rep *kolostore.Replica) {
	refused, quarantined := c.refusedOf(author, rep.ID())
	if quarantined {
		return
	}
	d, err := kololog.NewKeyDeriver(c.secret, rep.Header().LogKey, rep.Header())
	if err != nil {
		log.Printf("keys of %s: %v", author[:8], err)
		return
	}
	defer d.Close()
	for start := uint64(0); start < rep.Len(); start += kololog.MaxAddKeys {
		keys := make([]kololog.EntryKey, min(kololog.MaxAddKeys, rep.Len()-start))
		for i := range keys {
			index := start + uint64(i)
			info, err := rep.EntryInfo(index)
			if err != nil || info.Deleted || info.Keyed || refused[index] {
				continue
			}
			if keys[i], err = d.Key(index); err != nil {
				break
			}
		}
		if !slices.ContainsFunc(keys, func(k kololog.EntryKey) bool { return k != nil }) {
			continue
		}
		err := rep.AddKeys(start, keys)
		for _, k := range keys {
			clear(k)
		}
		var bad *kololog.BadKeyError
		if errors.As(err, &bad) && c.refuse(author, rep.ID(), bad.Indexes) {
			return
		}
		if err != nil && bad == nil {
			log.Printf("keys of %s: %v", author[:8], err) // nothing is refused: the next pull derives them again
			c.setStalled(rep.ID(), true)
			return
		}
	}
	c.setStalled(rep.ID(), false)
}

// setStalled keeps whether the last unlock of a log could not store its keys, which is what PinStatus.Locked counts.
func (c *ChatService) setStalled(id kololog.LogID, stalled bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if stalled {
		c.stalled[id] = true
	} else {
		delete(c.stalled, id)
	}
}

// refusedList returns the refused entries kept for logID, the member's messages log or its directory, and nil for a
// log it no longer writes.
func refusedList(m *member, logID kololog.LogID) *[]uint64 {
	if h, err := kololog.DecodeLogHeader(m.Header); err == nil && h.LogID == logID {
		return &m.Refused
	}
	if h, err := kololog.DecodeLogHeader(m.DirHeader); err == nil && h.LogID == logID {
		return &m.DirRefused
	}
	return nil
}

// refusedOf returns the refused entries of an author's log logID, and whether the author is quarantined.
func (c *ChatService) refusedOf(author string, logID kololog.LogID) (map[uint64]bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.members[author]
	if !ok {
		return nil, false
	}
	set := map[uint64]bool{}
	if list := refusedList(m, logID); list != nil {
		for _, i := range *list {
			set[i] = true
		}
	}
	return set, m.Quarantined
}

// refuse keeps the entries of logID that the author's signed records prove wrong, marks the author in the window,
// and quarantines it past maxRefused. It returns whether the author is quarantined.
func (c *ChatService) refuse(author string, logID kololog.LogID, indexes []uint64) bool {
	c.mu.Lock()
	m, ok := c.members[author]
	list := (*[]uint64)(nil)
	if ok {
		list = refusedList(m, logID)
	}
	if list == nil { // a log the author replaced meanwhile
		c.mu.Unlock()
		return false
	}
	*list = append(*list, indexes...)
	if len(m.Refused)+len(m.DirRefused) > maxRefused {
		m.Quarantined = true
		*list = (*list)[:min(len(*list), maxRefused)]
		delete(c.locked, author)
	}
	quarantined := m.Quarantined
	err := saveMembers(c.set.DataDir, c.members)
	c.mu.Unlock()
	if err != nil {
		log.Print("members: ", err)
	}
	log.Printf("%d entries of %s refused: %v", len(indexes), author[:8], errBadKey)
	c.publishDirectory()
	return quarantined
}

// mismatched says whether a message the author sent directly differed from the entry it signed.
func (c *ChatService) mismatched(author string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.members[author]
	return ok && len(m.Mismatched) > 0
}

// flagged says whether an author wrote an entry that could not be checked, or entries the member list refused.
func (c *ChatService) flagged(author string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.members[author]
	return ok && (m.Quarantined || len(m.Refused)+len(m.DirRefused) > 0) || slices.Contains(c.flags.Keys, author)
}
