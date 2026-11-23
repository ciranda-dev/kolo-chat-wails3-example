package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	kolodht "github.com/ciranda-dev/kolo-dht"
	koloid "github.com/ciranda-dev/kolo-id"
	"github.com/ciranda-dev/kolo-pin"
	kolosecure "github.com/ciranda-dev/kolo-secure"
	"golang.org/x/crypto/chacha20poly1305"
)

const testKey = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

// events records what a ChatService tells the window. all keeps every event as JSON, for the checks that
// something never shows up in one.
type events struct {
	mu       sync.Mutex
	msgs     []ChatMsg
	history  [][]ChatMsg
	pins     []PinStatus
	requests []InviteRequest
	joins    []JoinStatus
	invites  []InviteInfo
	profiles [][]Member
	rooms    [][]string
	relays   []RelayStatus
	states   []StateStatus
	nets     []string // the states of "chat:network", in order
	all      []string
}

func (e *events) emit(name string, data any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	switch v := data.(type) {
	case ChatMsg:
		e.msgs = append(e.msgs, v)
	case []ChatMsg:
		e.history = append(e.history, v)
	case PinStatus:
		e.pins = append(e.pins, v)
	case InviteRequest:
		e.requests = append(e.requests, v)
	case JoinStatus:
		e.joins = append(e.joins, v)
	case InviteInfo:
		e.invites = append(e.invites, v)
	case []Member:
		e.profiles = append(e.profiles, v)
	case []string:
		e.rooms = append(e.rooms, v)
	case RelayStatus:
		e.relays = append(e.relays, v)
	case StateStatus:
		e.states = append(e.states, v)
	case NetStatus:
		e.nets = append(e.nets, v.State)
	}
	if b, err := json.Marshal(data); err == nil {
		e.all = append(e.all, name+" "+string(b))
	}
}

// state is the last "chat:state".
func (e *events) state() StateStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.states) == 0 {
		return StateStatus{}
	}
	return e.states[len(e.states)-1]
}

func (e *events) messages() []ChatMsg {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]ChatMsg(nil), e.msgs...)
}

func (e *events) histories() [][]ChatMsg {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([][]ChatMsg(nil), e.history...)
}

func (e *events) pin() PinStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.pins) == 0 {
		return PinStatus{}
	}
	return e.pins[len(e.pins)-1]
}

// dataDir returns a private directory for one chat's data, as the app creates it. t.TempDir is open to others,
// which the identity file refuses.
func dataDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(40 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// network is what the chats run against: a bootstrap node and a pin, both on loopback.
type network struct {
	bootstrap string
	pin       pin.Peer
	pinDir    string
}

func startBootstrap(t *testing.T) string {
	t.Helper()
	id, err := koloid.Generate()
	if err != nil {
		t.Fatal(err)
	}
	node, err := kolodht.New(context.Background(), "127.0.0.1:0", id, &kolodht.Config{Server: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { node.Close() })
	return node.Addr().String()
}

// startPin runs a pinning peer, the same one `kolo-pin run` runs, accepting only owners if any are given.
func startPin(t *testing.T, owners ...ed25519.PublicKey) (pin.Peer, string) {
	t.Helper()
	peer, dir, _ := stoppablePin(t, owners...)
	return peer, dir
}

// stoppablePin is startPin with a function that stops the pin before the test ends.
func stoppablePin(t *testing.T, owners ...ed25519.PublicKey) (pin.Peer, string, func()) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "pin")
	id, err := pin.LoadIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ln, err := kolosecure.Listen(ctx, "127.0.0.1:0", id, nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- pin.Serve(ctx, ln, pin.Options{Dir: dir, MaxBytes: 50 << 20, Owners: owners}) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			<-done
			ln.Close()
		})
	}
	t.Cleanup(stop)
	return pin.Peer{ID: id.PublicKey(), Addr: ln.Addr().String()}, dir, stop
}

func newNetwork(t *testing.T) network {
	peer, dir := startPin(t)
	return network{bootstrap: startBootstrap(t), pin: peer, pinDir: dir}
}

func (n network) config(dir string) Config {
	return Config{
		DataDir:         dir,
		Bootstrap:       n.bootstrap,
		ConversationKey: testKey,
		Pins:            []string{pinEntry(n.pin)},
		LookupInterval:  time.Second,
	}
}

// pinEntry writes a pin the way the configuration takes it: IP:PORT@KEY.
func pinEntry(p pin.Peer) string { return p.Addr + "@" + hex.EncodeToString(p.ID) }

// start runs a chat with the given config and stops it when the test ends.
func start(t *testing.T, cfg Config) (*ChatService, *events) {
	t.Helper()
	ev := &events{}
	c := NewChatService(cfg, ev.emit)
	if _, err := c.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.ServiceShutdown() })
	return c, ev
}

// knows says that c registered other: other proved the conversation key, is on c's member list and has a link.
func knows(c, other *ChatService) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := hex.EncodeToString(other.pub)
	_, ok := c.members[key]
	l := c.links[key]
	return ok && l != nil && l.registered
}

// introduced waits until the two chats have connected, exchanged their hellos and accepted each other.
func introduced(t *testing.T, a, b *ChatService) {
	t.Helper()
	eventually(t, "the peers to meet", func() bool { return knows(a, b) && knows(b, a) })
}

func pinned(t *testing.T, n network, c *ChatService) pin.Info {
	t.Helper()
	return heldBy(t, n.pin, c)
}

// heldBy returns what the pin p holds of c's messages log, zero when it holds nothing.
func heldBy(t *testing.T, p pin.Peer, c *ChatService) pin.Info {
	t.Helper()
	info, err := pin.Status(context.Background(), c.id, p, c.log.ID())
	if err != nil {
		return pin.Info{}
	}
	return info
}

func texts(msgs []ChatMsg) []string {
	var out []string
	for _, m := range msgs {
		out = append(out, m.Text)
	}
	return out
}

// (a) Both online: the message arrives directly, and is also in the sender's log and on the pin.
func TestDirectDeliveryAlsoWritesLogAndPin(t *testing.T) {
	n := newNetwork(t)
	a, _ := start(t, n.config(dataDir(t)))
	b, bev := n.member(t, a, dataDir(t))
	introduced(t, a, b)

	if err := a.Send(defaultRoom, "hello"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the direct message", func() bool { return len(bev.messages()) == 1 })
	if m := bev.messages()[0]; m.Text != "hello" || m.Via != "direct" || m.From != a.id.PublicKeyString() {
		t.Fatalf("got %+v", m)
	}

	if a.log.Len() != 1 {
		t.Fatalf("log has %d entries", a.log.Len())
	}
	stored, err := a.log.Get(0)
	if err != nil || strings.Contains(string(stored), "hello") {
		t.Fatalf("the entry must hold sealed text, got %q, %v", stored, err)
	}
	if b, err := a.open(a.pub, stored); err != nil || b.Text != "hello" || b.Room != defaultRoom || b.Time == 0 {
		t.Fatalf("open: %+v, %v", b, err)
	}
	eventually(t, "the pin to hold the entry", func() bool { return pinned(t, n, a).Entries == 1 })

	eventually(t, "the sync that keeps the direct message", func() bool { return readable(b, a) == 1 })
	if got := bev.messages(); len(got) != 1 {
		t.Fatalf("a duplicate arrived: %v", texts(got))
	}
}

// (b) B is closed while A sends. When B opens again it shows the missed messages, in order, once, and not the
// one it got directly before. The ciphertext comes from the pin, and B derives the keys.
func TestMissedMessagesComeFromThePin(t *testing.T) {
	n := newNetwork(t)
	a, _ := start(t, n.config(dataDir(t)))
	bdir := dataDir(t)
	b, bev := n.member(t, a, bdir)
	introduced(t, a, b)

	a.Send(defaultRoom, "one")
	eventually(t, "the direct message", func() bool { return len(bev.messages()) == 1 })
	if err := b.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}

	a.Send(defaultRoom, "two")
	a.Send(defaultRoom, "three")
	eventually(t, "the pin to hold three entries", func() bool { return pinned(t, n, a).Entries == 3 })

	b2, b2ev := start(t, n.config(bdir)) // same data directory, so the same identity and the same members
	eventually(t, "the missed messages", func() bool { return len(b2ev.messages()) >= 2 })
	got := b2ev.messages()
	if got[0].Text != "two" || got[1].Text != "three" || got[0].Via != "pin" || got[1].Via != "pin" {
		t.Fatalf("got %+v", got)
	}
	eventually(t, "the three entries to count as shown", func() bool { next, _ := b2.shown(hex.EncodeToString(a.pub)); return next == 3 })
	if got := b2ev.messages(); len(got) != 2 {
		t.Fatalf("expected two messages, got %v", texts(got))
	}

	// Live again: the next message arrives directly, once.
	introduced(t, a, b2)
	a.Send(defaultRoom, "four")
	eventually(t, "the direct message", func() bool { return len(b2ev.messages()) == 3 })
	if m := b2ev.messages()[2]; m.Text != "four" || m.Via != "direct" {
		t.Fatalf("got %+v", m)
	}
}

// An owner that starts again with another key, as running `export KOLO_CHAT_CONVERSATION_KEY=$(openssl rand -hex 32)`
// again does, is refused: that key is another conversation, which its members are not in. Without a key it goes
// on with the kept one. Then B closes, A sends four, and B opens again while A is online: all four arrive and
// nothing waits.
func TestOwnerRestartKeepsTheConversation(t *testing.T) {
	n := newNetwork(t)
	adir, bdir := dataDir(t), dataDir(t)
	a, _ := start(t, n.config(adir))
	b, bev := start(t, n.joiner(bdir))
	joinVia(t, a, b, false)
	linked(t, a, b)
	a.Send(defaultRoom, "before")
	eventually(t, "the first message", func() bool { return len(bev.messages()) == 1 })
	a.ServiceShutdown()

	other := n.config(adir)
	other.ConversationKey = strings.Repeat("ab", 32)
	_, err := NewChatService(other, (&events{}).emit).Start()
	var ce *ConfigError
	if !errors.As(err, &ce) || ce.Setting != "conversation-key" || strings.Contains(err.Error(), other.ConversationKey) {
		t.Fatalf("a new key for the owner's data directory: got %v", err)
	}

	a2, _ := start(t, n.joiner(adir)) // no key, no pin: the data directory has them
	if !a2.Joined() || !a2.IsAdmin() {
		t.Fatalf("joined %v, admin %v", a2.Joined(), a2.IsAdmin())
	}
	linked(t, a2, b)
	a2.Send(defaultRoom, "after")
	eventually(t, "the message after the restart", func() bool { return len(bev.messages()) == 2 })

	b.ServiceShutdown()
	for i := range 4 {
		a2.Send(defaultRoom, fmt.Sprint("away ", i))
	}
	eventually(t, "the pin to hold six entries", func() bool { return pinned(t, n, a2).Entries == 6 })
	_, b2ev := start(t, n.joiner(bdir))
	eventually(t, "the four missed messages", func() bool { return len(b2ev.messages()) == 4 })
	if got := texts(b2ev.messages()); !slices.Equal(got, []string{"away 0", "away 1", "away 2", "away 3"}) {
		t.Fatalf("got %v", got)
	}
	eventually(t, "nothing to wait for", func() bool { return b2ev.pin().Locked == 0 })
}

// A data directory without conversation.json has messages: a key that does not derive the keys of our logs is
// another conversation's and is refused, and the one they use is kept.
func TestOlderDataDirectoryKeepsItsKey(t *testing.T) {
	n := newNetwork(t)
	dir := dataDir(t)
	a, _ := start(t, n.config(dir))
	if err := a.Send(defaultRoom, "sealed with the first key"); err != nil {
		t.Fatal(err)
	}
	a.ServiceShutdown()
	if err := os.Remove(filepath.Join(dir, "conversation.json")); err != nil {
		t.Fatal(err)
	}

	other := n.config(dir)
	other.ConversationKey = strings.Repeat("ab", 32)
	_, err := NewChatService(other, (&events{}).emit).Start()
	var ce *ConfigError
	if !errors.As(err, &ce) || ce.Setting != "conversation-key" {
		t.Fatalf("got %v", err)
	}
	start(t, n.config(dir))
	if saved, found, err := loadConversation(dir, 0); err != nil || !found || hex.EncodeToString(saved.Key) != testKey {
		t.Fatalf("saved %+v, %v, %v", saved, found, err)
	}
}

// Locked sums the pulled messages of every author that cannot be opened yet, wherever their sender is.
func TestLockedSumsEveryAuthor(t *testing.T) {
	ev := &events{}
	c := &ChatService{emit: ev.emit, locked: map[string]int{alice: 2, bob: 3}, links: map[string]*link{alice: nil}}
	c.setPin(func([]pinState) {})
	if st := ev.pin(); st.Locked != 5 {
		t.Fatalf("got %+v", st)
	}
}

// (c) A token that does not fit fails Start with a typed error naming the setting.
func TestBadTokenFailsStartWithTypedError(t *testing.T) {
	n := newNetwork(t)
	other, _ := koloid.Generate()
	otherPin, _ := koloid.Generate()

	issue := func(dir string, owner *koloid.Identity, c pin.Capability) string {
		if owner == nil {
			owner, _ = loadIdentity(dir)
		}
		token, err := pin.Issue(owner, c)
		if err != nil {
			t.Fatal(err)
		}
		return hex.EncodeToString(token)
	}
	for _, tc := range []struct {
		name  string
		token func(dir string) string
		want  error
	}{
		{"not hex", func(string) string { return "zz" }, ErrMalformed},
		{"not a capability", func(string) string { return "00ff00ff" }, pin.ErrInvalidCapability},
		{"another pin", func(dir string) string {
			return issue(dir, nil, pin.Capability{Peer: otherPin.PublicKey(), Expires: time.Now().Add(time.Hour)})
		}, pin.ErrUnauthorized},
		{"another owner", func(dir string) string {
			return issue(dir, other, pin.Capability{Peer: n.pin.ID, Expires: time.Now().Add(time.Hour)})
		}, pin.ErrUnauthorized},
		{"expired", func(dir string) string {
			return issue(dir, nil, pin.Capability{Peer: n.pin.ID, Issued: time.Now().Add(-2 * time.Hour), Expires: time.Now().Add(-time.Hour)})
		}, pin.ErrExpired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := dataDir(t)
			cfg := n.config(dir)
			cfg.PinTokens = []string{tc.token(dir)}
			_, err := NewChatService(cfg, (&events{}).emit).Start()
			var ce *ConfigError
			if !errors.Is(err, tc.want) || !errors.Is(err, ErrConfig) || !errors.As(err, &ce) || ce.Setting != "pin-token" {
				t.Fatalf("got %v", err)
			}
		})
	}
}

// (c) The pin refuses a token it does not accept, and the chat goes on working directly.
func TestPinRefusesUnknownOwner(t *testing.T) {
	stranger, _ := koloid.Generate()
	peer, _ := startPin(t, stranger.PublicKey()) // accepts only the stranger
	n := network{bootstrap: startBootstrap(t), pin: peer}
	a, _ := start(t, n.config(dataDir(t)))
	b, bev := n.member(t, a, dataDir(t))
	introduced(t, a, b)

	a.Send(defaultRoom, "still direct")
	if err := a.pushToPin(context.Background()); !errors.Is(err, pin.ErrUnauthorized) {
		t.Fatalf("got %v", err)
	}
	eventually(t, "the direct message", func() bool { return len(bev.messages()) == 1 })
}

// (c) An expired token is refused by the pin with pin.ErrExpired, and the window is told.
func TestExpiredTokenIsReported(t *testing.T) {
	n := newNetwork(t)
	dir := dataDir(t)
	id, err := loadIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(8 * time.Second)
	token, err := pin.Issue(id, pin.Capability{Peer: n.pin.ID, Expires: expires})
	if err != nil {
		t.Fatal(err)
	}
	cfg := n.config(dir)
	cfg.PinTokens = []string{hex.EncodeToString(token)}
	a, aev := start(t, cfg)
	if err := a.pushToPin(context.Background()); err != nil {
		t.Fatalf("a fresh token works: %v", err)
	}

	time.Sleep(time.Until(expires) + time.Second) // the token ends at a time of the clock, so this wait has no event to follow
	a.Send(defaultRoom, "too late for the pin")
	if err := a.pushToPin(context.Background()); !errors.Is(err, pin.ErrExpired) {
		t.Fatalf("got %v", err)
	}
	if msg := aev.pin().Error; !strings.Contains(msg, "expired") {
		t.Fatalf("the window was told %q", msg)
	}
}

// (c) With the quota full the pin refuses with pin.ErrQuotaExceeded, the window is told, and B still gets
// every message directly.
func TestQuotaFullIsReported(t *testing.T) {
	n := newNetwork(t)
	cfg := n.config(dataDir(t))
	cfg.PinQuota = 2048
	a, aev := start(t, cfg)
	b, bev := n.member(t, a, dataDir(t))
	introduced(t, a, b)

	const total = 12
	for i := 0; i < total; i++ {
		if err := a.Send(defaultRoom, "a message that takes some room"); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, "every message directly", func() bool { return len(bev.messages()) == total })
	if err := a.pushToPin(context.Background()); !errors.Is(err, pin.ErrQuotaExceeded) {
		t.Fatalf("got %v", err)
	}
	if msg := aev.pin().Error; !strings.Contains(msg, "quota is full") {
		t.Fatalf("the window was told %q", msg)
	}
}

// After a restart the window gets the conversation back in one "chat:history" event: the messages of others it
// showed before and our own, in order, once. Nothing of it comes again on the live path.
func TestHistoryAfterRestart(t *testing.T) {
	n := newNetwork(t)
	a, _ := start(t, n.config(dataDir(t)))
	bdir := dataDir(t)
	b, bev := n.member(t, a, bdir)
	introduced(t, a, b)
	if h := bev.histories(); len(h) != 0 {
		t.Fatalf("a first start has no history, got %v", h)
	}

	send := func(c *ChatService, text string) {
		t.Helper()
		time.Sleep(5 * time.Millisecond) // the order across senders comes from their clocks, in milliseconds
		if err := c.Send(defaultRoom, text); err != nil {
			t.Fatal(err)
		}
	}
	sentAfter := time.Now().UnixMilli()
	send(a, "one")
	eventually(t, "the first message", func() bool { return len(bev.messages()) == 1 })
	send(b, "two")
	send(a, "three")
	eventually(t, "the third message", func() bool { return len(bev.messages()) == 2 })
	eventually(t, "the sync that keeps the direct messages", func() bool { return readable(b, a) == 2 })
	eventually(t, "the direct messages to be checked against the log", func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return len(b.direct[hex.EncodeToString(a.pub)]) == 0
	})
	if b.mismatched(hex.EncodeToString(a.pub)) {
		t.Fatal("an honest member is marked")
	}
	b.ServiceShutdown()

	from, mine := hex.EncodeToString(a.pub), hex.EncodeToString(b.pub)
	want := []ChatMsg{
		{From: from, Room: defaultRoom, Text: "one", Via: "history"},
		{From: mine, Room: defaultRoom, Text: "two", Via: "history", Mine: true},
		{From: from, Room: defaultRoom, Text: "three", Via: "history"},
	}
	b2, b2ev := start(t, n.config(bdir))
	h := b2ev.histories()
	if len(h) != 1 || len(h[0]) != len(want) {
		t.Fatalf("history = %+v, want %+v", h, want)
	}
	last := int64(0)
	for i, m := range h[0] {
		if m.Time < sentAfter || m.Time > time.Now().UnixMilli() || m.Time < last {
			t.Fatalf("message %d has the time %d, want the sender's clock, not before %d and in order", i, m.Time, sentAfter)
		}
		last = m.Time
		m.Time = 0
		if m != want[i] {
			t.Fatalf("history[%d] = %+v, want %+v", i, m, want[i])
		}
	}
	introduced(t, a, b2)
	eventually(t, "the sync of the entries b2 showed before", func() bool { return readable(b2, a) == 2 })
	if got := b2ev.messages(); len(got) != 0 {
		t.Fatalf("the history came again live: %v", texts(got))
	}

	send(a, "four")
	eventually(t, "the live message", func() bool { return len(b2ev.messages()) == 1 })
	if m := b2ev.messages()[0]; m.Text != "four" || m.Via != "direct" {
		t.Fatalf("got %+v", m)
	}
	eventually(t, "the sync that keeps the direct message", func() bool { return readable(b2, a) == 3 })
	b2.ServiceShutdown()
	_, b3ev := start(t, n.config(bdir))
	if h := b3ev.histories(); len(h) != 1 || len(h[0]) != 4 || h[0][3].Text != "four" {
		t.Fatalf("history after a live message = %+v", h)
	}
}

// readable counts the entries of from's log that c can read from its store.
func readable(c, from *ChatService) int {
	rep, err := c.replica(hex.EncodeToString(from.pub))
	if err != nil {
		return 0
	}
	n := 0
	for i := range rep.Len() {
		if _, err := rep.Get(i); err == nil {
			n++
		}
	}
	return n
}

// A message shown directly just before the app closed, before a sync kept it, is not in the store: the history
// cannot have it, and it shows once, live, when it comes.
func TestShownButNotKeptShowsLater(t *testing.T) {
	n := newNetwork(t)
	a, _ := start(t, n.config(dataDir(t)))
	bdir := dataDir(t)
	b, _ := n.member(t, a, bdir)
	introduced(t, a, b)
	b.ServiceShutdown()
	if err := a.Send(defaultRoom, "seen, not kept"); err != nil {
		t.Fatal(err)
	}

	members, err := loadMembers(bdir) // as if B had shown it directly and closed at once
	if err != nil {
		t.Fatal(err)
	}
	members[hex.EncodeToString(a.pub)].Shown = 1
	if err := saveMembers(bdir, members); err != nil {
		t.Fatal(err)
	}
	b2, b2ev := start(t, n.config(bdir))
	if h := b2ev.histories(); len(h) != 0 {
		t.Fatalf("history = %+v", h)
	}
	introduced(t, a, b2)
	eventually(t, "the message", func() bool { return len(b2ev.messages()) == 1 })
	if m := b2ev.messages()[0]; m.Text != "seen, not kept" {
		t.Fatalf("got %+v", m)
	}
}

// The history holds the newest maxHistory messages, in the order they were sent.
func TestHistoryIsBounded(t *testing.T) {
	n := newNetwork(t)
	dir := dataDir(t)
	a, _ := start(t, n.config(dir))
	const total = maxHistory + 3
	for i := range total {
		if err := a.Send(defaultRoom, fmt.Sprint("m", i)); err != nil {
			t.Fatal(err)
		}
	}
	a.ServiceShutdown()

	_, aev := start(t, n.config(dir))
	h := aev.histories()
	if len(h) != 1 || len(h[0]) != maxHistory {
		t.Fatalf("got %d events, the first with %d messages", len(h), len(h[0]))
	}
	for i, m := range h[0] {
		if want := fmt.Sprint("m", total-maxHistory+i); m.Text != want || !m.Mine {
			t.Fatalf("message %d = %+v, want %q", i, m, want)
		}
	}
}

// Each sender keeps its own order. Senders merge by the sender's clock, a time that goes back or is missing takes
// the sender's time before it, and equal times go by sender key, then by index.
func TestHistoryOrder(t *testing.T) {
	msg := func(from string, index uint64, time int64) historyMsg {
		return historyMsg{ChatMsg: ChatMsg{From: from, Text: fmt.Sprint(from[:1], index)}, time: time, index: index}
	}
	all := addHistory(nil, []historyMsg{msg(bob, 0, 100), msg(bob, 1, 200)}, 10)
	all = addHistory(all, []historyMsg{msg(alice, 0, 100), msg(alice, 1, 0), msg(alice, 2, 50), msg(alice, 3, 300)}, 10)
	var got []string
	for _, m := range all {
		got = append(got, m.Text)
	}
	if want := []string{"a0", "a1", "a2", "b0", "b1", "a3"}; !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if all = addHistory(all, nil, 2); len(all) != 2 || all[0].Text != "b1" || all[1].Text != "a3" {
		t.Fatalf("the bound kept %+v", all)
	}
}

// A message without a time still opens.
func TestMessageWithoutTimeOpens(t *testing.T) {
	c := &ChatService{}
	key, _ := hex.DecodeString(testKey)
	var err error
	if c.aead, err = chacha20poly1305.NewX(key); err != nil {
		t.Fatal(err)
	}
	author := ed25519.PublicKey(make([]byte, ed25519.PublicKeySize))
	nonce := make([]byte, c.aead.NonceSize())
	sealed := c.aead.Seal(nonce, nonce, []byte(`{"v":1,"room":"dev","text":"old"}`), author)
	if b, err := c.open(author, sealed); err != nil || b.Room != "dev" || b.Text != "old" || b.Time != 0 {
		t.Fatalf("open: %+v, %v", b, err)
	}
}

// History reaches the main window only, and neither counts as unread nor brings the notch down.
func TestHistoryKeepsTheNotchQuiet(t *testing.T) {
	n := newTestNotch(t)
	before := n.State()
	n.forward("chat:history", []ChatMsg{{From: alice, Room: defaultRoom, Text: "old", Via: "history"}})
	if st := n.State(); st != before || st.Unread != 0 || st.HasMessage {
		t.Fatalf("state = %+v, want %+v", st, before)
	}
	if n.panel.down() {
		t.Fatal("history brought the notch down")
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.main) != 1 || n.main[0].name != "chat:history" {
		t.Fatalf("the main window got %+v", n.main)
	}
}
