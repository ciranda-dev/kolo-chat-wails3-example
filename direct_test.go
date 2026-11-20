package main

import (
	"slices"
	"testing"
)

// A message shown directly is checked against the entry the member signed at that index once the log arrives. The same
// bytes pass and leave no mark; other text marks the member, keeps the index, and survives a restart.
func TestDirectMessageIsCheckedAgainstTheLog(t *testing.T) {
	r := newRig(t, testKey, testSecret(t))
	r.send(t, "same", "signed text")
	same, err := r.writer.Get(0)
	if err != nil {
		t.Fatal(err)
	}
	other, err := r.w.seal(defaultRoom, "text shown live")
	if err != nil {
		t.Fatal(err)
	}
	r.reader.rememberDirect(r.author, 0, same)
	r.reader.rememberDirect(r.author, 1, other)

	r.reader.show(r.author) // the log is not here yet: both wait
	if len(r.reader.direct[r.author]) != 2 || r.reader.mismatched(r.author) {
		t.Fatalf("checked too soon: %v", r.reader.direct[r.author])
	}
	r.deliver(t)
	r.unlockAndShow(t)
	if !r.reader.mismatched(r.author) || r.reader.flagged(r.author) {
		t.Fatal("the mismatch is not marked, or it is marked as a refused entry")
	}
	if m := r.member(); !slices.Equal(m.Mismatched, []uint64{1}) || len(r.reader.direct[r.author]) != 0 {
		t.Fatalf("member %+v, pending %v", m, r.reader.direct[r.author])
	}
	saved, err := loadMembers(r.reader.set.DataDir)
	if err != nil || !slices.Equal(saved[r.author].Mismatched, []uint64{1}) {
		t.Fatalf("not kept: %v %v", saved[r.author], err)
	}
}

// A member whose direct messages are its entries is never marked.
func TestHonestDirectMessageIsNotMarked(t *testing.T) {
	r := newRig(t, testKey, testSecret(t))
	r.send(t, "a", "b")
	for i := range uint64(2) {
		sealed, err := r.writer.Get(i)
		if err != nil {
			t.Fatal(err)
		}
		r.reader.rememberDirect(r.author, i, sealed)
	}
	r.deliver(t)
	r.unlockAndShow(t)
	if r.reader.mismatched(r.author) || len(r.reader.direct[r.author]) != 0 {
		t.Fatalf("marked %v, pending %v", r.reader.mismatched(r.author), r.reader.direct[r.author])
	}
}

// The bound on what waits for a check.
func TestDirectMessagesWaitingAreBounded(t *testing.T) {
	r := newRig(t, testKey, testSecret(t))
	for i := range uint64(maxDirectPending + 10) {
		r.reader.rememberDirect(r.author, i, []byte{byte(i)})
	}
	if n := len(r.reader.direct[r.author]); n != maxDirectPending {
		t.Fatalf("%d wait", n)
	}
}
