package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	kolobraid "github.com/ciranda-dev/kolo-braid"
	koloid "github.com/ciranda-dev/kolo-id"
	kololog "github.com/ciranda-dev/kolo-log"
	"github.com/ciranda-dev/kolo-pin"
	kolosecure "github.com/ciranda-dev/kolo-secure"
	kolostore "github.com/ciranda-dev/kolo-store"
	kolosync "github.com/ciranda-dev/kolo-sync"
	"golang.org/x/crypto/chacha20poly1305"
)

// The entry secret is HKDF-SHA256 of the conversation key under "kolochat/entry-secret/v1": the vector of
// decision 0016, never the conversation key itself.
func TestEntrySecretVector(t *testing.T) {
	s, err := entrySecret(bytes.Repeat([]byte{0x44}, 32))
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(s[:]); got != "0fe3e1f118c5efdb7f9c8ce52b8376dd66c086335c07728dda38f3dacda78662" {
		t.Fatalf("entry secret %s", got)
	}
}

// Both of our logs derive their entry keys at epoch 0, and a peer's log that does not is refused: in a hello, as a
// directory, as join data and in a grant.
func TestOnlyDerivedLogsAreAccepted(t *testing.T) {
	n := newNetwork(t)
	a, _ := start(t, n.config(dataDir(t)))
	for _, l := range []*kolostore.Log{a.log, a.dirLog} {
		if h := l.Header(); h.Keys != kololog.DerivedKeys || h.Epoch != 0 {
			t.Fatalf("own log header %+v", h)
		}
	}
	if _, err := memberHeader(a.pub, a.log.Header().Encode()); err != nil {
		t.Fatalf("our own header: %v", err)
	}

	id, _ := koloid.Generate()
	secret, _ := entrySecret(bytes.Repeat([]byte{1}, 32))
	random, _ := kololog.New(id, kololog.NewMemory())
	epoch1, _ := kololog.New(id, kololog.NewMemory(), kololog.WithDerivedKeys(secret, 1))
	epoch0, _ := kololog.New(id, kololog.NewMemory(), kololog.WithDerivedKeys(secret, 0))
	for name, l := range map[string]*kololog.Log{"random keys": random, "epoch 1": epoch1} {
		raw := l.Header().Encode()
		if _, err := memberHeader(id.PublicKey(), raw); !errors.Is(err, errNotDerived) {
			t.Errorf("%s as a member header: %v", name, err)
		}
		if err := checkDirHeader(id.PublicKey(), raw, epoch0.Header()); !errors.Is(err, errNotDerived) {
			t.Errorf("%s as a directory: %v", name, err)
		}
	}
	for name, l := range map[string]*kololog.Log{"random keys": random, "epoch 0": epoch0} {
		payload, _ := json.Marshal(grant{Version: grantVersion, Header: l.Header().Encode(), Welcome: []byte{1}, conversation: conversation{
			Key: make([]byte, 32), Pins: []pinRef{{Addr: "127.0.0.1:1", Key: id.PublicKey()}}}})
		if _, err := readGrant(payload, id.PublicKey(), defaultMaxPins); (err == nil) != (l == epoch0) {
			t.Errorf("a grant with %s: %v", name, err)
		}
	}
}

// Members get our logs as ciphertext only: no entry key goes over kolo-sync. Anyone else gets nothing.
func TestAuthorizeGivesCiphertextOnly(t *testing.T) {
	n := newNetwork(t)
	a, _ := start(t, n.config(dataDir(t)))
	b, _ := n.member(t, a, dataDir(t))
	introduced(t, a, b)
	stranger, _ := koloid.Generate()
	for _, id := range []kololog.LogID{a.log.ID(), a.dirLog.ID()} {
		if got := a.authorize(b.pub, a.pub, id); got != kolosync.Ciphertext {
			t.Errorf("a member gets %v", got)
		}
		if got := a.authorize(stranger.PublicKey(), a.pub, id); got != kolosync.NoAccess {
			t.Errorf("a stranger gets %v", got)
		}
	}
}

// A data directory whose log has random entry keys is refused at Start, for an owner and for a joiner, with an
// error that names the folder to move aside. Nothing in the folder is changed: the log opens as before, with its head
// and its entry, and no log was added.
func TestRandomKeyLogsAreRefused(t *testing.T) {
	n := newNetwork(t)
	for name, cfg := range map[string]func(string) Config{"owner": n.config, "joiner": n.joiner} {
		dir := dataDir(t)
		id, err := loadIdentity(dir)
		if err != nil {
			t.Fatal(err)
		}
		st := openStore(t, dir)
		random, err := st.Create(id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := random.Append([]byte("kept")); err != nil {
			t.Fatal(err)
		}
		head, _ := random.Head()
		st.Close()

		_, err = NewChatService(cfg(dir), (&events{}).emit).Start()
		var de *DataDirError
		if !errors.As(err, &de) || !de.Logs || de.Dir != dir || !strings.Contains(err.Error(), dir) {
			t.Fatalf("%s: got %v", name, err)
		}

		st = openStore(t, dir)
		own, err := ownLogs(st, id)
		if err != nil || len(own) != 1 || own[0] != random.ID() {
			t.Fatalf("%s: own logs %v, %v", name, own, err)
		}
		again, err := st.Open(id, random.ID())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if h, _ := again.Head(); !bytes.Equal(h.Encode(), head.Encode()) {
			t.Fatalf("%s: the head changed", name)
		}
		if v, err := again.Get(0); err != nil || string(v) != "kept" {
			t.Fatalf("%s: entry %q, %v", name, v, err)
		}
		st.Close()
	}
}

// openStore opens the store of a data directory that no chat runs on.
func openStore(t *testing.T, dir string) *kolostore.Store {
	t.Helper()
	backend, err := kolostore.OpenPebble(filepath.Join(dir, "store"))
	if err != nil {
		t.Fatal(err)
	}
	return kolostore.New(backend)
}

// A member's data directory that lost conversation.json, and started with no key, names the folder to move aside:
// there is no key it could be told to leave out.
func TestDataDirectoryWithoutItsConversationIsRefused(t *testing.T) {
	n := newNetwork(t)
	dir := dataDir(t)
	a, _ := start(t, n.config(dir))
	if err := a.Send(defaultRoom, "kept"); err != nil {
		t.Fatal(err)
	}
	a.ServiceShutdown()
	if err := os.Remove(filepath.Join(dir, "conversation.json")); err != nil {
		t.Fatal(err)
	}
	_, err := NewChatService(n.joiner(dir), (&events{}).emit).Start()
	var de *DataDirError
	if !errors.As(err, &de) || de.Logs || de.Dir != dir || errors.Is(err, ErrConfig) {
		t.Fatalf("got %v", err)
	}
}

// THE case of decision 0016. Bob is closed, Alice sends three messages and changes her name, then quits. Bob opens
// with only the pin up and reads all of it from the pin, with nothing left waiting. Carol, the third member, gets the
// messages directly. The pin never holds a key: not an entry key, not the entry secret, not the conversation key.
func TestOfflineSenderIsReadFromThePin(t *testing.T) {
	pinPeer, pinDir, stopPin := stoppablePin(t)
	n := network{bootstrap: startBootstrap(t), pin: pinPeer, pinDir: pinDir}
	adir, bdir := dataDir(t), dataDir(t)
	a, _ := start(t, n.config(adir))
	b, _ := n.member(t, a, bdir)
	c, cev := n.member(t, a, dataDir(t))
	introduced(t, a, b)
	introduced(t, a, c)
	eventually(t, "B to know A's directory", func() bool { return b.readerOf(keyOf(a)) != nil })
	b.ServiceShutdown()

	want := []string{"one", "two", "three"}
	for _, text := range want {
		if err := a.Send(defaultRoom, text); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.SetProfile("Alice", ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the pin to hold both of A's logs", func() bool { return holdsBoth(t, n.pin, a) })
	eventually(t, "C to get them directly", func() bool { return len(cev.messages()) == 3 })
	var keys [][]byte
	for _, l := range []*kolostore.Log{a.log, a.dirLog} {
		for i := range l.Len() {
			k, err := l.Key(i)
			if err != nil {
				t.Fatal(err)
			}
			keys = append(keys, k)
		}
	}
	if len(keys) <= 3 {
		t.Fatal("the directory has no entry")
	}
	a.ServiceShutdown()

	b2, b2ev := start(t, n.config(bdir))
	eventually(t, "the three messages from the pin", func() bool { return len(b2ev.messages()) == 3 })
	got := b2ev.messages()
	if !slices.Equal(texts(got), want) || slices.ContainsFunc(got, func(m ChatMsg) bool { return m.Via != "pin" }) {
		t.Fatalf("got %+v", got)
	}
	eventually(t, "A's new name from the pin", func() bool { return nameOn(b2, a) == "Alice" })
	if st := b2ev.pin(); st.Locked != 0 {
		t.Fatalf("pinned messages still wait: %+v", st)
	}
	if told(b2.Members(), a).Flagged {
		t.Fatal("an honest sender is marked")
	}

	stopPin()
	dump := append(pinDump(t, pinDir), pinFiles(t, pinDir)...)
	key, _ := hex.DecodeString(testKey)
	secret, _ := entrySecret(key)
	for i, k := range keys {
		if bytes.Contains(dump, k) {
			t.Errorf("the pin holds the key of entry %d", i)
		}
	}
	for name, s := range map[string][]byte{"entry secret": secret[:], "conversation key": key, "text": []byte("three")} {
		if bytes.Contains(dump, s) {
			t.Errorf("the pin holds the %s", name)
		}
	}
}

// pinDump returns every key and value a stopped pin stores, concatenated.
func pinDump(t *testing.T, dir string) []byte {
	t.Helper()
	db, err := kolostore.OpenPebble(filepath.Join(dir, "store"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var out []byte
	if err := db.Scan(nil, nil, false, 0, func(k, v []byte) bool {
		out = append(append(out, k...), v...)
		return true
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// pinFiles returns the bytes of every file under a stopped pin's folder.
func pinFiles(t *testing.T, dir string) []byte {
	t.Helper()
	var out []byte
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		out = append(out, b...)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// A joiner reads the messages its inviter sent before it joined. It talks to nobody until its braid holds the member
// list, which a member online gives it, so the first read is from a member, not from the pin.
func TestJoinerReadsTheEarlierHistory(t *testing.T) {
	n := newNetwork(t)
	a, _ := start(t, n.config(dataDir(t)))
	a.Send(defaultRoom, "before you came")
	a.Send(defaultRoom, "also before")
	eventually(t, "the pin to hold both", func() bool { return pinned(t, n, a).Entries == 2 })
	_, cev := n.member(t, a, dataDir(t))
	eventually(t, "the earlier history", func() bool { return len(cev.messages()) == 2 })
	if got := cev.messages(); !slices.Equal(texts(got), []string{"before you came", "also before"}) {
		t.Fatalf("got %+v", got)
	}
}

// Mallory is a member whose app commits to entry keys that are not the derived ones. Bob reads only the pin: he
// refuses exactly Mallory's entries, goes past them, marks Mallory and nobody else, and still shows Alice's messages.
func TestHostileMemberIsSkippedAndMarked(t *testing.T) {
	n := newNetwork(t)
	a, _ := start(t, n.config(dataDir(t)))
	bdir := dataDir(t)
	b, _ := n.member(t, a, bdir)
	introduced(t, a, b)
	mallory, mlog := hostileLog(t, "not mine", "nor this")
	admitKey(t, a, mallory, 0)
	eventually(t, "B to have Mallory on the member list", func() bool { return b.isAccepted(mallory.PublicKeyString()) })
	b.ServiceShutdown()

	token, err := pin.Issue(mallory, pin.Capability{Peer: n.pin.ID, Logs: []kololog.LogID{mlog.ID()}, Expires: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pin.Add(context.Background(), mallory, n.pin, mlog, token); err != nil {
		t.Fatal(err)
	}
	members, err := loadMembers(bdir) // Bob met Mallory before
	if err != nil {
		t.Fatal(err)
	}
	members[mallory.PublicKeyString()] = &member{Header: mlog.Header().Encode()}
	if err := saveMembers(bdir, members); err != nil {
		t.Fatal(err)
	}
	a.Send(defaultRoom, "honest one")
	a.Send(defaultRoom, "honest two")
	eventually(t, "the pin to hold A's entries", func() bool { return pinned(t, n, a).Entries == 2 })
	a.ServiceShutdown()

	b2, b2ev := start(t, n.config(bdir))
	eventually(t, "A's messages", func() bool { return len(b2ev.messages()) == 2 })
	eventually(t, "Mallory's entries to be passed", func() bool { next, _ := b2.shown(mallory.PublicKeyString()); return next == 2 })
	if got := texts(b2ev.messages()); !slices.Equal(got, []string{"honest one", "honest two"}) {
		t.Fatalf("got %v", got)
	}
	b2.mu.Lock()
	m := *b2.members[mallory.PublicKeyString()]
	b2.mu.Unlock()
	if !slices.Equal(m.Refused, []uint64{0, 1}) || m.Quarantined {
		t.Fatalf("Mallory's member: %+v", m)
	}
	flagged := 0
	for _, mem := range b2.Members() {
		if mem.Flagged {
			flagged++
			if mem.Key != mallory.PublicKeyString() {
				t.Fatalf("%s is marked", mem.Key[:8])
			}
		}
	}
	if flagged != 1 {
		t.Fatalf("%d members marked", flagged)
	}
	saved, _ := loadMembers(bdir)
	if !slices.Equal(saved[mallory.PublicKeyString()].Refused, []uint64{0, 1}) {
		t.Fatal("the refused entries are not kept")
	}
	if st := b2ev.pin(); st.Locked != 0 {
		t.Fatalf("refused entries count as waiting: %+v", st)
	}
}

// hostileLog writes, in a store of its own, a log with a derived-keys header whose entry keys come from another
// secret, with values sealed with the conversation key as the app seals them.
func hostileLog(t *testing.T, texts ...string) (*koloid.Identity, *kolostore.Log) {
	t.Helper()
	id, _ := koloid.Generate()
	var wrong kololog.EntrySecret
	rand.Read(wrong[:])
	st := kolostore.New(kolostore.NewMemory())
	t.Cleanup(func() { st.Close() })
	l, err := st.Create(id, kololog.WithDerivedKeys(&wrong, 0))
	if err != nil {
		t.Fatal(err)
	}
	w := sealer(t, testKey, id)
	for _, text := range texts {
		sealed, err := w.seal(defaultRoom, text)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := l.Append(sealed); err != nil {
			t.Fatal(err)
		}
	}
	return id, l
}

// sealer is a chat that only seals and opens, with the conversation key hex and the identity id.
func sealer(t *testing.T, conversationKey string, id *koloid.Identity) *ChatService {
	t.Helper()
	key, _ := hex.DecodeString(conversationKey)
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		t.Fatal(err)
	}
	return &ChatService{aead: aead, pub: id.PublicKey()}
}

// flakyBackend fails the commit of its batches while fail is set.
type flakyBackend struct {
	kolostore.Backend
	fail atomic.Bool
}

type flakyBatch struct {
	kolostore.Batch
	b *flakyBackend
}

func (f *flakyBackend) NewBatch() kolostore.Batch {
	return &flakyBatch{Batch: f.Backend.NewBatch(), b: f}
}

func (f *flakyBatch) Commit() error {
	if f.b.fail.Load() {
		return errors.New("injected write failure")
	}
	return f.Batch.Commit()
}

// rig is a reader and one author's two logs, with no network: deliver gives the reader the author's new entries as
// ciphertext, the way a pull does.
type rig struct {
	reader *ChatService
	me     *koloid.Identity
	ev     *events
	disk   *flakyBackend
	id     *koloid.Identity
	writer *kolostore.Log // the author's messages
	dirLog *kolostore.Log // the author's directory
	w      *ChatService
	author string
}

func newRig(t *testing.T, readerKey string, writerSecret *kololog.EntrySecret) *rig {
	t.Helper()
	return newRigOn(t, kolostore.NewMemory(), readerKey, writerSecret, writerSecret)
}

// newRigOn is a rig whose reader keeps its store in backend, and whose author writes its messages and its
// directory under the given secrets.
func newRigOn(t *testing.T, backend kolostore.Backend, readerKey string, msgSecret, dirSecret *kololog.EntrySecret) *rig {
	t.Helper()
	me, _ := koloid.Generate()
	key, _ := hex.DecodeString(readerKey)
	r := &rig{me: me, ev: &events{}, disk: &flakyBackend{Backend: backend}}
	c := sealer(t, readerKey, me)
	c.emit, c.store = r.ev.emit, kolostore.New(r.disk)
	t.Cleanup(func() { c.store.Close() })
	var err error
	if c.secret, err = entrySecret(key); err != nil {
		t.Fatal(err)
	}
	c.set.DataDir = dataDir(t)
	c.members, c.replicas, c.dirs = map[string]*member{}, map[string]*kolostore.Replica{}, map[string]*directory{}
	c.accepted = map[string]kolobraid.Member{}
	c.pinned, c.locked, c.stalled = map[string]uint64{}, map[string]int{}, map[kololog.LogID]bool{}
	r.reader = c
	r.addAuthor(t, msgSecret, dirSecret)
	return r
}

// addAuthor makes r's author a new member, with its own two logs, that r.reader knows.
func (r *rig) addAuthor(t *testing.T, msgSecret, dirSecret *kololog.EntrySecret) {
	t.Helper()
	r.id, _ = koloid.Generate()
	st := kolostore.New(kolostore.NewMemory())
	t.Cleanup(func() { st.Close() })
	var err error
	if r.writer, err = st.Create(r.id, kololog.WithDerivedKeys(msgSecret, 0)); err != nil {
		t.Fatal(err)
	}
	if r.dirLog, err = st.Create(r.id, kololog.WithDerivedKeys(dirSecret, 0)); err != nil {
		t.Fatal(err)
	}
	r.w, r.author = sealer(t, testKey, r.id), r.id.PublicKeyString()
	r.reader.mu.Lock()
	r.reader.members[r.author] = &member{Header: r.writer.Header().Encode(), DirHeader: r.dirLog.Header().Encode()}
	r.reader.accepted[r.author] = kolobraid.Member{Key: r.id.PublicKey()}
	r.reader.mu.Unlock()
}

// another is a second author for the same reader.
func (r *rig) another(t *testing.T, secret *kololog.EntrySecret) *rig {
	t.Helper()
	o := *r
	o.addAuthor(t, secret, secret)
	return &o
}

// testSecret is the entry secret of testKey.
func testSecret(t *testing.T) *kololog.EntrySecret {
	t.Helper()
	key, _ := hex.DecodeString(testKey)
	s, err := entrySecret(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (r *rig) send(t *testing.T, texts ...string) {
	t.Helper()
	for _, text := range texts {
		sealed, err := r.w.seal(defaultRoom, text)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.writer.Append(sealed); err != nil {
			t.Fatal(err)
		}
	}
}

func (r *rig) replica(t *testing.T) *kolostore.Replica {
	t.Helper()
	rep, err := r.reader.replica(r.author)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func (r *rig) deliver(t *testing.T) { t.Helper(); deliverTo(t, r.writer, r.replica(t)) }

// deliverTo stores the new entries of from in rep without their keys.
func deliverTo(t *testing.T, from *kolostore.Log, rep *kolostore.Replica) {
	t.Helper()
	h, err := from.Head()
	if err != nil {
		t.Fatal(err)
	}
	var proof kololog.ConsistencyProof
	if rep.Len() > 0 {
		if proof, err = from.ConsistencyProof(rep.Len(), h.Size); err != nil {
			t.Fatal(err)
		}
	}
	var entries []kololog.Entry
	for i := rep.Len(); i < h.Size; i++ {
		e, err := from.Entry(i)
		if err != nil {
			t.Fatal(err)
		}
		e.Key = nil
		entries = append(entries, e)
	}
	if err := rep.Update(h, proof, entries); err != nil {
		t.Fatal(err)
	}
}

// unlock and show is what a pull does after it stored the ciphertext.
func (r *rig) unlockAndShow(t *testing.T) {
	t.Helper()
	r.reader.pullMu.Lock()
	r.reader.unlock(r.author, r.replica(t))
	r.reader.pullMu.Unlock()
	r.reader.show(r.author)
}

func (r *rig) member() member {
	r.reader.mu.Lock()
	defer r.reader.mu.Unlock()
	return *r.reader.members[r.author]
}

// dial connects the reader to a peer on loopback.
func (r *rig) dial(t *testing.T, addr string, peer ed25519.PublicKey) *kolosecure.Conn {
	t.Helper()
	conn, err := kolosecure.Dial(t.Context(), addr, r.me, peer, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// An entry that has no key yet, because the pull that brought it has not derived its key, stops show: it is neither
// skipped nor counted against its author, nor counted as waiting, and it shows once its key is in.
func TestShowWaitsForAnEntryWithoutKey(t *testing.T) {
	r := newRig(t, testKey, testSecret(t))
	r.send(t, "a", "b", "c")
	r.deliver(t)
	r.reader.show(r.author) // the other path shows while this pull still derives
	if got := r.ev.messages(); len(got) != 0 {
		t.Fatalf("shown without keys: %v", texts(got))
	}
	if m := r.member(); m.Shown != 0 || len(m.Refused) != 0 {
		t.Fatalf("member %+v", m)
	}
	if st := r.ev.pin(); st.Locked != 0 {
		t.Fatalf("entries being derived count as waiting: %+v", st)
	}
	r.unlockAndShow(t)
	if got := texts(r.ev.messages()); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("got %v", got)
	}
}

// The pull and the derivation are one pullMu section. While a pull waits between the two, the other path's show
// neither skips nor refuses the pulled entries, and they show once the pull ends.
func TestUnlockRace(t *testing.T) {
	r := newRig(t, testKey, testSecret(t))
	r.send(t, "a", "b", "c")
	p, _ := startPin(t)
	token, err := pin.Issue(r.id, pin.Capability{Peer: p.ID, Expires: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pin.Add(t.Context(), r.id, p, r.writer, token); err != nil {
		t.Fatal(err)
	}
	reached, release := make(chan bool), make(chan struct{})
	pulled = func() { reached <- r.reader.pullMu.TryLock(); <-release }
	t.Cleanup(func() { pulled = func() {} })
	done := make(chan error, 1)
	rep, conn := r.replica(t), r.dial(t, p.Addr, p.ID)
	go func() { done <- r.reader.pull(t.Context(), conn, r.author, rep) }()
	if <-reached {
		t.Fatal("pullMu is free between the pull and the derivation")
	}
	r.reader.show(r.author)
	if m := r.member(); len(r.ev.messages()) != 0 || m.Shown != 0 || len(m.Refused) != 0 {
		t.Fatalf("shown %v, member %+v", texts(r.ev.messages()), m)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	r.reader.show(r.author)
	if got := texts(r.ev.messages()); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("got %v", got)
	}
}

// A pull that fails, here from a pin that does not hold the log, still derives the keys of what earlier pulls stored.
func TestFailedPullStillDerives(t *testing.T) {
	r := newRig(t, testKey, testSecret(t))
	r.send(t, "a", "b")
	r.deliver(t)
	p, _ := startPin(t)
	if err := r.reader.pull(t.Context(), r.dial(t, p.Addr, p.ID), r.author, r.replica(t)); !errors.Is(err, kolosync.ErrNotFound) {
		t.Fatalf("pull: %v", err)
	}
	r.reader.show(r.author)
	if got := texts(r.ev.messages()); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("got %v", got)
	}
}

// A write that fails while the keys are stored refuses nothing and marks nobody: the entries wait, the window counts
// them, and the next pull derives them again and shows them. After that, entries being derived count as waiting no
// more.
func TestUnlockStorageFailureRetriesAtTheNextPull(t *testing.T) {
	r := newRig(t, testKey, testSecret(t))
	r.send(t, "a", "b")
	r.deliver(t)
	r.disk.fail.Store(true)
	r.unlockAndShow(t)
	if got := r.ev.messages(); len(got) != 0 {
		t.Fatalf("shown: %v", texts(got))
	}
	saved, _ := loadMembers(r.reader.set.DataDir)
	if m := r.member(); m.Shown != 0 || len(m.Refused) != 0 || m.Quarantined || saved[r.author] != nil && len(saved[r.author].Refused) != 0 {
		t.Fatalf("a storage error counted against the author: %+v", m)
	}
	if st := r.ev.pin(); st.Locked != 2 {
		t.Fatalf("pin status %+v", st)
	}
	r.disk.fail.Store(false)
	r.unlockAndShow(t)
	if got := texts(r.ev.messages()); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("got %v", got)
	}
	if st := r.ev.pin(); st.Locked != 0 {
		t.Fatalf("pin status %+v", st)
	}
	r.send(t, "c")
	r.deliver(t)
	r.reader.show(r.author) // the other path shows while this pull still derives
	if st := r.ev.pin(); st.Locked != 0 {
		t.Fatalf("the failed write is still counted: %+v", st)
	}
}

// An entry whose key its signed record proves wrong is skipped for good, and the author's later entries still show.
// A refusal for a log the author no longer writes is dropped.
func TestShowSkipsOnlyARefusedEntry(t *testing.T) {
	r := newRig(t, testKey, testSecret(t))
	r.send(t, "a", "b", "c", "d")
	r.deliver(t)
	rep := r.replica(t)
	wrong := make(kololog.EntryKey, 32)
	var bad *kololog.BadKeyError
	if err := rep.AddKeys(1, []kololog.EntryKey{wrong}); !errors.As(err, &bad) {
		t.Fatalf("AddKeys with a wrong key: %v", err)
	}
	r.reader.refuse(r.author, rep.ID(), bad.Indexes)
	r.reader.refuse(r.author, kololog.LogID{1}, []uint64{2})
	r.unlockAndShow(t)
	if got := texts(r.ev.messages()); !slices.Equal(got, []string{"a", "c", "d"}) {
		t.Fatalf("got %v", got)
	}
	if m := r.member(); m.Shown != 4 || !slices.Equal(m.Refused, []uint64{1}) || len(m.DirRefused) != 0 {
		t.Fatalf("member %+v", m)
	}
}

// A member whose directory entry commits to a wrong key: that entry is refused in its directory alone, its messages
// still show, and it is marked.
func TestHostileDirectoryIsSkippedAndMarked(t *testing.T) {
	var wrong kololog.EntrySecret
	rand.Read(wrong[:])
	r := newRigOn(t, kolostore.NewMemory(), testKey, testSecret(t), &wrong)
	if _, err := r.dirLog.Append([]byte("a profile")); err != nil {
		t.Fatal(err)
	}
	r.send(t, "a", "b")
	d, err := r.reader.directory(r.author)
	if err != nil {
		t.Fatal(err)
	}
	deliverTo(t, r.dirLog, d.rep)
	r.reader.pullMu.Lock()
	r.reader.unlock(r.author, d.rep)
	r.reader.pullMu.Unlock()
	r.deliver(t)
	r.unlockAndShow(t)
	if got := texts(r.ev.messages()); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("got %v", got)
	}
	if m := r.member(); !slices.Equal(m.DirRefused, []uint64{0}) || len(m.Refused) != 0 || !r.reader.flagged(r.author) {
		t.Fatalf("member %+v", m)
	}
}

// Mallory serves her log herself and offers its keys. The reader asks for none, derives its own, refuses all of
// Mallory's entries and marks her, as a reader of the pin does.
func TestHostileMemberOnlineIsRefusedToo(t *testing.T) {
	var wrong kololog.EntrySecret
	rand.Read(wrong[:])
	r := newRig(t, testKey, &wrong)
	r.send(t, "not mine", "nor this")
	ln, err := kolosecure.Listen(t.Context(), "127.0.0.1:0", r.id, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	srv := kolosync.NewServer(func(_, _ ed25519.PublicKey, _ kololog.LogID) kolosync.Access { return kolosync.Keys })
	if err := srv.Offer(r.writer); err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := ln.Accept(t.Context())
			if err != nil {
				return
			}
			go srv.Serve(t.Context(), conn)
		}
	}()
	if err := r.reader.pull(t.Context(), r.dial(t, ln.Addr().String(), r.id.PublicKey()), r.author, r.replica(t)); err != nil {
		t.Fatal(err)
	}
	r.reader.show(r.author)
	if got := r.ev.messages(); len(got) != 0 {
		t.Fatalf("shown %v", texts(got))
	}
	if m := r.member(); !slices.Equal(m.Refused, []uint64{0, 1}) || m.Shown != 2 || !r.reader.flagged(r.author) {
		t.Fatalf("member %+v", m)
	}
}

// Someone who does not hold the conversation key derives keys no record commits to: it opens nothing.
func TestNoConversationKeyOpensNothing(t *testing.T) {
	r := newRig(t, strings.Repeat("ab", 32), testSecret(t))
	r.send(t, "secret one", "secret two")
	r.deliver(t)
	r.unlockAndShow(t)
	if got := r.ev.messages(); len(got) != 0 {
		t.Fatalf("opened %v", texts(got))
	}
	for i := range r.replica(t).Len() {
		if _, err := r.replica(t).Get(i); !errors.Is(err, kololog.ErrKeyUnavailable) {
			t.Fatalf("entry %d: %v", i, err)
		}
	}
}

// An author with more than maxRefused refused entries is quarantined: its logs are no longer pulled, derived or
// shown, what is kept about it stays bounded, and a new log does not end it.
func TestTooManyRefusedEntriesQuarantine(t *testing.T) {
	var wrong kololog.EntrySecret
	rand.Read(wrong[:])
	r := newRig(t, testKey, &wrong)
	for range maxRefused + 1 {
		r.send(t, "x")
	}
	r.deliver(t)
	logs := captureLog(t)
	r.unlockAndShow(t)
	m := r.member()
	if !m.Quarantined || len(m.Refused) > maxRefused || m.Shown != 0 {
		t.Fatalf("quarantined %v, %d refused, shown %d", m.Quarantined, len(m.Refused), m.Shown)
	}
	before := strings.Count(logs.String(), "refused")
	r.send(t, "y")
	r.deliver(t)
	r.unlockAndShow(t)
	if strings.Count(logs.String(), "refused") != before || r.ev.pin().Locked != 0 {
		t.Fatal("a quarantined author is still derived")
	}
	if err := r.reader.pull(t.Context(), nil, r.author, r.replica(t)); err != nil {
		t.Fatalf("a quarantined author is still pulled: %v", err)
	}
	if err := r.reader.addMember(r.id.PublicKey(), []byte("a new log"), nil); err != nil {
		t.Fatal(err)
	}
	if m := r.member(); !m.Quarantined || string(m.Header) == "a new log" {
		t.Fatalf("a new log ended the quarantine: %+v", m)
	}
	saved, _ := loadMembers(r.reader.set.DataDir)
	if !saved[r.author].Quarantined {
		t.Fatal("the quarantine is not kept with the member")
	}
}

// The way back from a quarantine: the member starts again with a new data folder, so a new kolo-id, and a new
// invite. The reader reads it as any member, and the old kolo-id stays quarantined.
func TestQuarantineWayBack(t *testing.T) {
	var wrong kololog.EntrySecret
	rand.Read(wrong[:])
	r := newRig(t, testKey, &wrong)
	for range maxRefused + 1 {
		r.send(t, "x")
	}
	r.deliver(t)
	r.unlockAndShow(t)
	again := r.another(t, testSecret(t))
	again.send(t, "fixed")
	again.deliver(t)
	again.unlockAndShow(t)
	if got := texts(again.ev.messages()); !slices.Equal(got, []string{"fixed"}) || r.reader.flagged(again.author) {
		t.Fatalf("got %v, flagged %v", got, r.reader.flagged(again.author))
	}
	if !r.member().Quarantined {
		t.Fatal("the old kolo-id came back")
	}
}

// A member that fills a pin's 16 MiB quota with entries whose keys are all wrong (floodEntries, fewer under the race
// detector): one unlock, well inside kolo-sync's 60 s timeout, quarantines it after one AddKeys call, later pulls
// derive nothing for it, and another member's messages show before and after.
func TestHostileFlood(t *testing.T) {
	backend, err := kolostore.OpenPebble(filepath.Join(t.TempDir(), "store"))
	if err != nil {
		t.Fatal(err)
	}
	var wrong kololog.EntrySecret
	rand.Read(wrong[:])
	flood := newRigOn(t, backend, testKey, &wrong, &wrong)
	honest := flood.another(t, testSecret(t))
	honest.send(t, "before")
	honest.deliver(t)
	honest.unlockAndShow(t)

	const n = floodEntries
	for range n {
		if _, err := flood.writer.Append([]byte{1}); err != nil {
			t.Fatal(err)
		}
	}
	flood.deliver(t)
	logs := captureLog(t)
	began := time.Now()
	flood.unlockAndShow(t)
	took := time.Since(began)
	t.Logf("%d entries: unlock and show took %v", n, took)
	if took > 60*time.Second {
		t.Fatal("too slow")
	}
	m := flood.member()
	if !m.Quarantined || len(m.Refused) != maxRefused || m.Shown != 0 || flood.ev.pin().Locked != 0 {
		t.Fatalf("quarantined %v, %d refused, shown %d", m.Quarantined, len(m.Refused), m.Shown)
	}
	if calls := strings.Count(logs.String(), "refused"); calls != 1 {
		t.Fatalf("%d AddKeys calls refused keys", calls)
	}
	flood.unlockAndShow(t)
	if calls := strings.Count(logs.String(), "refused"); calls != 1 {
		t.Fatal("a later pull derived the quarantined author again")
	}
	honest.send(t, "after")
	honest.deliver(t)
	honest.unlockAndShow(t)
	if got := texts(honest.ev.messages()); !slices.Equal(got, []string{"before", "after"}) {
		t.Fatalf("got %v", got)
	}
}

// T1 and T2: the sentence of a refused data folder names it and says what to do.
func TestDataDirErrorSentences(t *testing.T) {
	for _, tc := range []struct {
		err       *DataDirError
		says      string
		movesData bool
	}{
		{&DataDirError{Dir: "/d", Layout: 4}, "not in a layout Kolo Chat can open (layout 4)", false},
		{&DataDirError{Dir: "/d", Layout: -1}, "(layout -1)", false},
		{&DataDirError{Dir: "/d", Logs: true}, "do not use the derived entry keys", true},
		{&DataDirError{Dir: "/d", Open: true}, "open to other users", false},
		{&DataDirError{Dir: "/d"}, "conversation.json is gone", true},
	} {
		msg := tc.err.Error()
		if !strings.Contains(msg, "/d") || !strings.Contains(msg, tc.says) || !strings.Contains(msg, "othing in") ||
			strings.Contains(msg, "move or rename") != tc.movesData {
			t.Errorf("%+v: %q", tc.err, msg)
		}
		if b, _ := json.Marshal(tc.err); tc.err.Layout != 0 && !strings.Contains(string(b), `"layout":`) {
			t.Errorf("marshaled as %s", b)
		}
	}
}
