package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	kolobraid "github.com/ciranda-dev/kolo-braid"
	koloid "github.com/ciranda-dev/kolo-id"
	invite "github.com/ciranda-dev/kolo-invite"
	kolostore "github.com/ciranda-dev/kolo-store"
	koloswarm "github.com/ciranda-dev/kolo-swarm"
	kolosync "github.com/ciranda-dev/kolo-sync"
)

// startWith is start with fields of the engine set before it starts.
func startWith(t *testing.T, cfg Config, set func(*ChatService)) (*ChatService, *events) {
	t.Helper()
	ev := &events{}
	c := NewChatService(cfg, ev.emit)
	set(c)
	if _, err := c.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.ServiceShutdown() })
	return c, ev
}

// rosterOf is the sorted member keys c shows.
func rosterOf(c *ChatService) []string {
	var keys []string
	for _, m := range c.Members() {
		keys = append(keys, m.Key)
	}
	slices.Sort(keys)
	return keys
}

func roleOf(c *ChatService, who string) (kolobraid.Role, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.accepted[who]
	return m.Roles, ok
}

func registeredWith(c *ChatService, who string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	l := c.links[who]
	return l != nil && l.registered
}

// S2, S6, S15, S19b: Alice founds, Bob is approved as an admin and Carol as a member. Alice and Bob add "design" at
// the same time: one room everywhere, and nobody is flagged. Bob removes Carol while Alice approves Dave: every member
// ends with the same list. The log holds no key, room name or text.
func TestTwoAdminsConverge(t *testing.T) {
	logs := captureLog(t)
	n := newNetwork(t)
	a, _ := start(t, n.config(dataDir(t)))
	b, _ := start(t, n.joiner(dataDir(t)))
	joinVia(t, a, b, true)
	c, _ := n.member(t, a, dataDir(t))
	introduced(t, a, b)
	introduced(t, a, c)
	introduced(t, b, c)
	if r, _ := roleOf(a, keyOf(b)); r != kolobraid.RoleAdmin {
		t.Fatalf("Bob approved as an admin has roles %d", r)
	}
	if r, ok := roleOf(a, keyOf(c)); r != 0 || !ok {
		t.Fatalf("Carol approved as a member has roles %d", r)
	}
	eventually(t, "Bob to be an admin", b.IsAdmin)

	var wg sync.WaitGroup
	for _, x := range []*ChatService{a, b} {
		wg.Go(func() {
			if err := x.AddRoom("design"); err != nil && !errors.Is(err, ErrRoomExists) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	for _, x := range []*ChatService{a, b, c} {
		seesRooms(t, x, defaultRoom, "design")
	}
	if _, err := b.braid.Append(t.Context(), encodeAddRoom("DESIGN")); err != nil { // an admin's duplicate, refused
		t.Fatal(err)
	}
	eventually(t, "the adds to settle", func() bool {
		rejected := 0
		for o, err := range a.braid.Entries(0) {
			if err == nil && o.Settled && o.Rejected {
				rejected++
			}
		}
		return rejected > 0 && len(a.state(a.braid.Status()).PendingRooms) == 0
	})
	for _, x := range []*ChatService{a, b, c} {
		for _, m := range x.Members() {
			if m.Flagged {
				t.Fatalf("%s is flagged for an admin's add", m.Key[:8])
			}
		}
	}

	d, _ := start(t, n.joiner(dataDir(t)))
	code := mustCreate(t, a, InviteOptions{}).Code
	done := joinAsync(d, code)
	request := requestOf(t, a, d.pub)
	wg.Go(func() {
		if err := b.RemoveMember(keyOf(c)); err != nil {
			t.Error(err)
		}
	})
	if err := a.Approve(request, false); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if err := waitJoin(t, done); err != nil {
		t.Fatal(err)
	}
	want := []string{keyOf(a), keyOf(b), keyOf(d)}
	slices.Sort(want)
	for _, x := range []*ChatService{a, b, d} {
		eventually(t, "the same member list", func() bool { return slices.Equal(rosterOf(x), want) })
	}

	key, _ := hex.DecodeString(testKey)
	for _, s := range []string{testKey, string(key), "design"} {
		if strings.Contains(logs.String(), s) {
			t.Errorf("the log holds %q", s)
		}
	}
}

// Admitting members never grows the settlers or the admins by itself: with three members admitted, the founder is
// the only settler, only the approved admin is an admin, and no Settlers entry is written.
func TestAdmitKeepsOneSettler(t *testing.T) {
	n := newNetwork(t)
	a, _ := start(t, n.config(dataDir(t)))
	b, _ := start(t, n.joiner(dataDir(t)))
	joinVia(t, a, b, true)
	c, _ := n.member(t, a, dataDir(t))
	d, _ := n.member(t, a, dataDir(t))
	if r, _ := roleOf(a, keyOf(b)); r != kolobraid.RoleAdmin {
		t.Fatalf("the approved admin has roles %d", r)
	}
	for _, x := range []*ChatService{c, d} {
		if r, _ := roleOf(a, keyOf(x)); r != 0 {
			t.Fatalf("a member approved as a member has roles %d", r)
		}
	}
	seats := 0
	for _, m := range a.Members() {
		if m.Seat {
			seats++
			if m.Key != keyOf(a) {
				t.Fatalf("a seat belongs to %s, not the founder", m.Key)
			}
		}
	}
	if seats != 1 {
		t.Fatalf("%d seats, want the founder's only", seats)
	}
	for o, err := range a.braid.Entries(0) {
		if err != nil {
			t.Fatal(err)
		}
		if o.Kind == kolobraid.KindSettlers {
			t.Fatal("admitting members wrote a Settlers entry")
		}
	}
}

// S3, S3c, S3d, S4, S5, S11: Carol is removed. Every app that holds the removal stops talking to her: no
// registration, NoAccess, her new messages are neither shown nor pulled, and what she wrote before stays. She still
// reads new messages from the pin: removal is not revocation. A member is no admin, and the settler cannot be removed.
func TestRemovedMemberIsNotTalkedTo(t *testing.T) {
	n := newNetwork(t)
	a, aev := start(t, n.config(dataDir(t)))
	bdir := dataDir(t)
	b, bev := n.member(t, a, bdir)
	cdir := dataDir(t)
	c, _ := n.member(t, a, cdir)
	introduced(t, a, c)
	introduced(t, b, c)
	if err := c.Send(defaultRoom, "before"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "Carol's message", func() bool { return hasText(aev.messages(), "before") && hasText(bev.messages(), "before") })

	if err := b.RemoveMember(keyOf(c)); !errors.Is(err, ErrNotAdmin) {
		t.Fatalf("a member removes: %v", err)
	}
	if err := a.RemoveMember(keyOf(a)); !errors.Is(err, ErrSettler) {
		t.Fatalf("the settler is removed: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), braidWrite)
	ref, err := a.braid.RemoveMember(ctx, a.pub) // past the app: the library keeps its one settler
	cancel()
	if err == nil {
		eventually(t, "the removal of the settler to settle", func() bool { return settledEntry(a.braid, ref.ID) })
		if !a.isAccepted(keyOf(a)) {
			t.Fatal("the library removed the settler")
		}
	}

	if err := a.RemoveMember(keyOf(c)); err != nil {
		t.Fatal(err)
	}
	for _, x := range []*ChatService{a, b} {
		eventually(t, "the removal", func() bool { return !x.isAccepted(keyOf(c)) && !registeredWith(x, keyOf(c)) })
		if got := x.authorize(c.pub, x.pub, x.log.ID()); got != kolosync.NoAccess {
			t.Fatalf("a removed member gets %v", got)
		}
	}
	shown, _ := b.shown(keyOf(c))
	dirBefore := dirLen(a, keyOf(c))
	if err := c.Send(defaultRoom, "after"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetProfile("Carol again", ""); err != nil { // S7-8: tells her links to pull her directory
		t.Fatal(err)
	}
	for _, x := range []*ChatService{a, b} {
		barrier(t, x, c)
	}
	if dirLen(a, keyOf(c)) != dirBefore {
		t.Fatal("a removed member's directory was pulled")
	}
	if hasText(aev.messages(), "after") || hasText(bev.messages(), "after") {
		t.Fatal("a removed member's new message was shown")
	}
	if now, _ := b.shown(keyOf(c)); now != shown {
		t.Fatalf("Bob pulled Carol: shown %d then %d", shown, now)
	}
	if rep, err := b.replica(keyOf(c)); err != nil || rep.Len() != 1 {
		t.Fatalf("Bob's copy of Carol's log grew: %v", err)
	}
	if !hasText(bev.messages(), "before") {
		t.Fatal("what Carol wrote before is gone")
	}
	eventually(t, "the pin to hold Carol's new message", func() bool { return heldBy(t, n.pin, c).Entries == 2 })
	b.ServiceShutdown()
	b2, b2ev := start(t, n.joiner(bdir))
	<-b2.pinDone
	if rep, err := b2.replica(keyOf(c)); err != nil || rep.Len() != 1 || hasText(b2ev.messages(), "after") {
		t.Fatalf("Bob pulled removed Carol from the pin: %v", err)
	}

	c.ServiceShutdown()
	if err := a.Send(defaultRoom, "new for everyone"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the pin to hold it", func() bool { return pinned(t, n, a).Entries == 1 })
	_, c2ev := start(t, n.joiner(cdir))
	eventually(t, "removed Carol to read the pin", func() bool { return hasText(c2ev.messages(), "new for everyone") })
}

// strangerConn connects a new identity to c over the conversation's topic, as anyone may, and returns its connection.
func strangerConn(t *testing.T, n network, c *ChatService, id *koloid.Identity) koloswarm.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	t.Cleanup(cancel)
	sw, err := koloswarm.New(ctx, "127.0.0.1:0", id, &koloswarm.Config{LookupInterval: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sw.Close() })
	if err := sw.Bootstrap(ctx, []netip.AddrPort{netip.MustParseAddrPort(n.bootstrap)}); err != nil {
		t.Fatal(err)
	}
	if err := sw.Join(ctx, c.topic(), &koloswarm.JoinOptions{Lookup: true}); err != nil {
		t.Fatal(err)
	}
	for {
		ev, err := sw.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if ev.Kind == koloswarm.Connected && ev.Peer.PublicKey().Equal(c.pub) {
			return ev.Peer.Conn()
		}
	}
}

func sendTagged(t *testing.T, conn koloswarm.Conn, m wireMsg) {
	t.Helper()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	st, err := conn.OpenUniStream(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Write(append([]byte{tagMsg}, data...)); err != nil {
		t.Fatal(err)
	}
	st.Close()
}

// S3b, S3c: someone with the conversation key under a fresh kolo-id, as a removed member coming back would be. A
// message without a hello is dropped; a hello that proves the key and names our braid is proven but not registered,
// since the key is not on the member list; it gets NoAccess, and its message is not shown.
func TestKeyAloneIsNotMembership(t *testing.T) {
	logs := captureLog(t)
	n := newNetwork(t)
	a, aev := start(t, n.config(dataDir(t)))
	carol, _ := koloid.Generate()
	conn := strangerConn(t, n, a, carol)
	sealer := sealer(t, testKey, carol)
	sealed, _ := sealer.seal(defaultRoom, "no hello")
	sendTagged(t, conn, wireMsg{Kind: "msg", Index: 0, Sealed: sealed})

	dir := dataDir(t)
	store := openStore(t, dir)
	defer store.Close()
	secret, _ := entrySecret(mustHex(t, testKey))
	msgs, dirLog, err := openOwnLogs(store, carol, dir, secret)
	if err != nil {
		t.Fatal(err)
	}
	proof := &ChatService{}
	copy(proof.set.conversationKey[:], mustHex(t, testKey))
	sendTagged(t, conn, wireMsg{Kind: "hello", Header: msgs.Header().Encode(), DirHeader: dirLog.Header().Encode(),
		Proof: proof.proof(carol.PublicKey(), a.pub), Group: a.group[:]})
	sealed, _ = sealer.seal(defaultRoom, "after a hello")
	sendTagged(t, conn, wireMsg{Kind: "msg", Index: 0, Sealed: sealed})
	sendTagged(t, conn, wireMsg{Kind: "dir"})
	hello := func() *wireMsg {
		a.mu.Lock()
		defer a.mu.Unlock()
		if l := a.links[carol.PublicKeyString()]; l != nil {
			return l.hello
		}
		return nil
	}
	eventually(t, "the hello to be proven", func() bool { return hello() != nil })
	first := hello()
	sendTagged(t, conn, wireMsg{Kind: "hello", Header: msgs.Header().Encode(), DirHeader: dirLog.Header().Encode(),
		Proof: proof.proof(carol.PublicKey(), a.pub), Group: a.group[:]})
	eventually(t, "the second hello, after the message and the dir notice", func() bool { return hello() != first })
	if dirLen(a, carol.PublicKeyString()) != 0 {
		t.Fatal("a stranger's directory was pulled")
	}
	a.mu.Lock()
	_, known := a.members[carol.PublicKeyString()]
	a.mu.Unlock()
	if known || registeredWith(a, carol.PublicKeyString()) || a.authorize(carol.PublicKey(), a.pub, a.log.ID()) != kolosync.NoAccess ||
		len(aev.messages()) != 0 {
		t.Fatalf("a key alone was let in: messages %v", texts(aev.messages()))
	}
	if strings.Contains(logs.String(), "hello refused from "+carol.PublicKeyString()[:8]) {
		t.Fatal("a proven hello was refused instead of kept")
	}
	// S7b: the braid knows the key for 60 s at most, then the app closes the connection.
	select {
	case <-conn.Context().Done():
	case <-time.After(100 * time.Second):
		t.Fatal("the connection of a key not on the member list stays open")
	}
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// S17: two people who both start with one key have two member lists: their hellos name other braids, neither
// registers the other, and each window says so.
func TestTwoFoundersOfOneKey(t *testing.T) {
	logs := captureLog(t)
	n := newNetwork(t)
	a, aev := start(t, n.config(dataDir(t)))
	b, _ := start(t, n.config(dataDir(t)))
	eventually(t, "the other member list in the window", func() bool { return aev.state().OtherGroup })
	if registeredWith(a, keyOf(b)) || registeredWith(b, keyOf(a)) || a.authorize(b.pub, a.pub, a.log.ID()) != kolosync.NoAccess {
		t.Fatal("two founders talk")
	}
	if !strings.Contains(logs.String(), "names another member list") {
		t.Fatal("the terminal does not say why")
	}
}

// S7d, N2: Bob's side sends a malformed braid message. Alice keeps the chat connection, pauses the braid with Bob
// and tells the window; the chat goes on. When the pause ends she closes the connection once, and both braids sync
// on the next one. A second fault doubles the pause, and the chat link stays.
func TestBraidFaultPausesOnlyTheMemberList(t *testing.T) {
	n := newNetwork(t)
	a, aev := startWith(t, n.config(dataDir(t)), func(c *ChatService) { c.pauseFirst = 3 * time.Second })
	b, bev := n.member(t, a, dataDir(t))
	introduced(t, a, b)

	fault := func() *link {
		t.Helper()
		b.mu.Lock()
		l := b.links[keyOf(a)]
		b.mu.Unlock()
		st, err := l.p.Conn().OpenUniStream(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		st.Write(append([]byte{tagBraid}, bytes.Repeat([]byte{0xff}, 64)...))
		st.Close()
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.links[keyOf(b)]
	}
	before := fault()
	eventually(t, "the pause in the window", func() bool { return aev.state().Paused == 1 })
	a.mu.Lock()
	first := a.pauses[keyOf(b)].until
	a.mu.Unlock()
	if err := b.Send(defaultRoom, "during the pause"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the message during the pause", func() bool {
		return slices.ContainsFunc(aev.messages(), func(m ChatMsg) bool { return m.Text == "during the pause" && m.Via == "direct" })
	})
	a.mu.Lock()
	same := a.links[keyOf(b)] == before
	a.mu.Unlock()
	if !same {
		t.Fatal("a braid fault dropped the chat connection")
	}
	eventually(t, "a fresh connection after the pause", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		l := a.links[keyOf(b)]
		return l != nil && l != before && l.registered
	})
	eventually(t, "the window to end the pause", func() bool { return aev.state().Paused == 0 && time.Now().After(first) })
	if err := a.AddRoom("after"); err != nil {
		t.Fatal(err)
	}
	seesRooms(t, b, defaultRoom, "after")

	again := fault()
	eventually(t, "the second pause", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		p := a.pauses[keyOf(b)]
		return p.faults == 2 && time.Until(p.until) > 4*time.Second
	})
	if err := b.Send(defaultRoom, "during the second pause"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the chat during the second pause", func() bool { return hasText(aev.messages(), "during the second pause") })
	a.mu.Lock()
	same = a.links[keyOf(b)] == again
	a.mu.Unlock()
	if !same {
		t.Fatal("a second fault dropped the chat link")
	}
	_ = bev
}

// S12, D17: a restart opens the braid from braid/ with the same lists. An admin that is not the settler and restarts
// alone cannot confirm its strand: AddRoom returns within the write bound with the waiting sentence, and the window
// says so.
func TestRestartKeepsTheMemberList(t *testing.T) {
	n := newNetwork(t)
	adir, bdir := dataDir(t), dataDir(t)
	a, _ := start(t, n.config(adir))
	b, _ := start(t, n.joiner(bdir))
	joinVia(t, a, b, true)
	introduced(t, a, b)
	if err := a.AddRoom("kept"); err != nil {
		t.Fatal(err)
	}
	seesRooms(t, b, defaultRoom, "kept")
	members := rosterOf(a)
	a.ServiceShutdown()
	b.ServiceShutdown()

	a2, _ := start(t, n.joiner(adir))
	if !slices.Equal(a2.Rooms(), []string{defaultRoom, "kept"}) || !slices.Equal(rosterOf(a2), members) || !a2.IsAdmin() {
		t.Fatalf("after a restart: rooms %v, members %v", a2.Rooms(), rosterOf(a2))
	}
	a2.ServiceShutdown()

	b2, b2ev := start(t, n.joiner(bdir))
	added := make(chan error, 1)
	go func() { added <- b2.AddRoom("alone") }()
	select {
	case err := <-added:
		if !errors.Is(err, ErrWaitingForMember) {
			t.Fatalf("an admin alone after a restart: %v", err)
		}
	case <-time.After(braidWrite + 10*time.Second):
		t.Fatal("AddRoom of an admin alone does not return")
	}
	eventually(t, "the window to say it waits", func() bool { return b2ev.state().Unconfirmed })
}

// pendingRequestFor is a request in c's list, as onRequested keeps it, from a new identity with a real braid join
// request. kolo-invite does not know its ID, so its Approve fails after Admit.
func pendingRequestFor(t *testing.T, c *ChatService, id uint64) *koloid.Identity {
	t.Helper()
	joiner, _ := koloid.Generate()
	j, err := kolobraid.NewJoin(t.Context(), joiner, kolobraid.Options{Backend: kolostore.NewMemory(), App: stateApp, Apply: applyState})
	if err != nil {
		t.Fatal(err)
	}
	req, err := j.Request(nil)
	if err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	c.requests[invite.RequestID(id)] = pendingRequest{ev: invite.Event{Request: invite.RequestID(id), Joiner: joiner.PublicKey()},
		data: joinData{Request: req}}
	c.mu.Unlock()
	return joiner
}

// S6, S13, D10: an approval whose invite cannot carry the welcome removes the joiner again. At 64 members Approve is
// refused with the full sentence before anything is written.
func TestApproveBoundsAndUndo(t *testing.T) {
	n := newNetwork(t)
	a, _ := start(t, n.config(dataDir(t)))
	eventually(t, "the founder to be an admin", a.IsAdmin) // the braid settles its first entry a moment after the start
	joiner := pendingRequestFor(t, a, 999)
	if err := a.Approve(999, true); !errors.Is(err, invite.ErrNotFound) {
		t.Fatalf("an approval the invite cannot carry: %v", err)
	}
	eventually(t, "the joiner to be removed again", func() bool { return !a.isAccepted(joiner.PublicKeyString()) })

	for len(rosterOf(a)) < maxMembers {
		id, _ := koloid.Generate()
		admitKey(t, a, id, 0)
		eventually(t, "the admission", func() bool { return a.isAccepted(id.PublicKeyString()) })
	}
	full := pendingRequestFor(t, a, 1000)
	before := a.braid.Status().Tentative
	if err := a.Approve(1000, false); !errors.Is(err, ErrFull) {
		t.Fatalf("Approve at 64: %v", err)
	}
	if a.braid.Status().Tentative != before || a.isAccepted(full.PublicKeyString()) || len(rosterOf(a)) != maxMembers {
		t.Fatal("Approve at 64 wrote an AddMember")
	}
}

// S13, R2: two admins each admit one member at 63, cut off from each other. Once they sync, one AddMember is void
// on both, and both list the same 64 members.
func TestTwoAdmissionsPastTheBound(t *testing.T) {
	a, idA := soloBraid(t, applyState)
	idB, _ := koloid.Generate()
	b := joinBraid(t, a, idB, applyState, kolobraid.RoleAdmin)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	admit := func(br *kolobraid.Braid) *koloid.Identity {
		id, _ := koloid.Generate()
		j, err := kolobraid.NewJoin(ctx, id, kolobraid.Options{Backend: kolostore.NewMemory(), App: stateApp, Apply: applyState})
		if err != nil {
			t.Fatal(err)
		}
		req, _ := j.Request(nil)
		role := kolobraid.Role(0)
		if _, err := br.Admit(ctx, id.PublicKey(), req, &kolobraid.AdmitOptions{Roles: &role}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	for range maxMembers - 3 {
		admit(a)
	}
	stop := connectBraidsUntil(t, a, idA, b, idB)
	eventually(t, "both to hold 63 members", func() bool { return memberCount(b) == maxMembers-1 && memberCount(a) == maxMembers-1 })
	stop()
	x, y := admit(a), admit(b)
	connectBraidsUntil(t, a, idA, b, idB)
	eventually(t, "one member list with one of the two admissions", func() bool {
		return slices.EqualFunc(membersOf(a), membersOf(b), bytes.Equal) && memberCount(a) == maxMembers &&
			listed(a, x.PublicKey()) != listed(a, y.PublicKey())
	})
}

func membersOf(b *kolobraid.Braid) [][]byte {
	v := b.View()
	defer v.Close()
	var keys [][]byte
	for _, m := range v.Members() {
		keys = append(keys, m.Key)
	}
	slices.SortFunc(keys, bytes.Compare)
	return keys
}

func memberCount(b *kolobraid.Braid) int {
	v := b.View()
	defer v.Close()
	return len(v.Members())
}

// connectBraidsUntil is connectBraids with a function that cuts the connection.
func connectBraidsUntil(t *testing.T, a *kolobraid.Braid, idA *koloid.Identity, b *kolobraid.Braid, idB *koloid.Identity) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	connectBraidsCtx(t, ctx, a, idA, b, idB)
	return cancel
}

// S19, D9, N4: a member who is not an admin writes 500 room entries past the app. Every peer rejects them, a body
// over 1 KiB is refused at once, and the founder flags the writer, also after a restart.
func TestFloodIsRejectedAndFlagged(t *testing.T) {
	n := newNetwork(t)
	adir := dataDir(t)
	a, _ := start(t, n.config(adir))
	b, _ := n.member(t, a, dataDir(t))
	introduced(t, a, b)
	eventually(t, "Bob's strand to be confirmed", func() bool { return !b.braid.Status().Unconfirmed })
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	if _, err := b.braid.Append(ctx, make([]byte, groupConfig.MaxBody+1)); !errors.Is(err, kolobraid.ErrTooLarge) {
		t.Fatalf("a body of 1025 bytes: %v", err)
	}
	for i := range 500 {
		if _, err := b.braid.Append(ctx, encodeAddRoom("flood"+string(rune('a'+i%26)))); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, "the founder to flag Bob", func() bool { return told(a.Members(), b).Flagged })
	if !slices.Equal(a.Rooms(), []string{defaultRoom}) || !slices.Equal(b.Rooms(), []string{defaultRoom}) {
		t.Fatalf("a member's rooms applied: %v %v", a.Rooms(), b.Rooms())
	}
	if told(a.Members(), a).Flagged {
		t.Fatal("the founder flagged itself")
	}
	a.ServiceShutdown()
	if fi, err := os.Stat(filepath.Join(adir, flagsFile)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("%s: %v", flagsFile, err)
	}
	a2, _ := start(t, n.joiner(adir))
	if !told(a2.Members(), b).Flagged {
		t.Fatal("the flag is lost after a restart")
	}
}

// S20, N5: with the fewest events kolo-braid keeps, a burst of admissions followed by more settlements than the queue
// holds drops every membership event, and the member list still has every member.
func TestDroppedEventsStillRebuild(t *testing.T) {
	n := newNetwork(t)
	a, _ := startWith(t, n.config(dataDir(t)), func(c *ChatService) { c.braidLimits.MaxEvents = 16 })
	var ids []*koloid.Identity
	a.mu.Lock() // the events loop waits, so events pile up
	for range 20 {
		id, _ := koloid.Generate()
		j, _ := kolobraid.NewJoin(t.Context(), id, kolobraid.Options{Backend: kolostore.NewMemory(), App: stateApp, Apply: applyState})
		req, _ := j.Request(nil)
		role := kolobraid.Role(0)
		if _, err := a.braid.Admit(t.Context(), id.PublicKey(), req, &kolobraid.AdmitOptions{Roles: &role}); err != nil {
			a.mu.Unlock()
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	// Applied events merge, so each room settles before the next: more than MaxEvents settlements push every
	// membership event out of the queue.
	for i := range 24 {
		before := a.braid.Status().Settled
		_, err := a.braid.Append(t.Context(), encodeAddRoom("r"+string(rune('a'+i))))
		for deadline := time.Now().Add(20 * time.Second); err == nil && a.braid.Status().Settled <= before; time.Sleep(20 * time.Millisecond) {
			if time.Now().After(deadline) {
				err = errors.New("a room did not settle")
			}
		}
		if err != nil {
			a.mu.Unlock()
			t.Fatal(err)
		}
	}
	a.mu.Unlock()
	eventually(t, "events to be dropped", func() bool { return a.braid.Status().EventsDropped > 0 })
	eventually(t, "every member accepted", func() bool {
		for _, id := range ids {
			if !a.isAccepted(id.PublicKeyString()) {
				return false
			}
		}
		return true
	})
}

// S21, N6, L6: a version 2 room entry halts an app of version 1, and the window gets the halted state as a flag,
// not as error text.
func TestHaltedStateIsShown(t *testing.T) {
	n := newNetwork(t)
	a, aev := start(t, n.config(dataDir(t)))
	if _, err := a.braid.Append(t.Context(), []byte{2, 1, 0}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the halted state", func() bool { return aev.state().Halted })
	if strings.Contains(aev.dump(), "version 2") {
		t.Fatal("raw error text reached the window")
	}
}

// S10, D5: the founder's Leave hands settlement to another admin, waits until it took over, and only then deletes the
// folder. Changes then settle without the founder.
func TestFounderHandsOver(t *testing.T) {
	n := newNetwork(t)
	alice, _ := startApp(t, n.app(dataDir(t)))
	conv := mustCreateConv(t, alice, "Handed over")
	af := engineOf(t, alice, conv.ID)
	bob, _ := start(t, n.joiner(dataDir(t)))
	joinVia(t, af, bob, true)
	carol, _ := n.member(t, af, dataDir(t))
	introduced(t, af, bob)
	introduced(t, bob, carol)
	eventually(t, "Bob's admission to settle", func() bool { return !told(af.Members(), bob).Pending })

	plan, err := alice.PlanLeave(conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Founder || len(plan.Admins) != 1 || plan.Admins[0].Key != keyOf(bob) {
		t.Fatalf("plan %+v", plan)
	}
	if err := alice.Leave(conv.ID, plan.Token, keyOf(carol)); !errors.Is(err, ErrNotAdmin) {
		t.Fatalf("a hand over to a member: %v", err)
	}
	if err := alice.Leave(conv.ID, plan.Token, keyOf(bob)); err != nil {
		t.Fatal(err)
	}
	if exists(t, plan.Dir) {
		t.Fatal("the folder stays after the hand over")
	}
	eventually(t, "Bob to settle", func() bool { return told(bob.Members(), bob).Seat })
	if err := bob.AddRoom("after alice"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the room to settle without Alice", func() bool {
		s := carol.braid.Settled()
		defer s.Close()
		return slices.Contains(roomsOf(s), "after alice")
	})
}

// S9, D21: Bob was offline. With only the pin up he shows the lists he held and Alice's new message, in a room he
// does not know yet; once Alice is online he gets the new room and Dave, admitted meanwhile.
func TestOfflineMemberSeesTheListsItHeld(t *testing.T) {
	n := newNetwork(t)
	adir, bdir := dataDir(t), dataDir(t)
	a, _ := start(t, n.config(adir))
	b, _ := n.member(t, a, bdir)
	introduced(t, a, b)
	b.ServiceShutdown()
	if err := a.AddRoom("design"); err != nil {
		t.Fatal(err)
	}
	dave, _ := koloid.Generate()
	admitKey(t, a, dave, 0)
	seesRooms(t, a, defaultRoom, "design")
	if err := a.Send("design", "in the new room"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the pin to hold it", func() bool { return pinned(t, n, a).Entries == 1 })
	a.ServiceShutdown()

	b2, b2ev := start(t, n.joiner(bdir))
	eventually(t, "the message from the pin", func() bool {
		return slices.ContainsFunc(b2ev.messages(), func(m ChatMsg) bool {
			return m.From == keyOf(a) && m.Room == "design" && m.Text == "in the new room" && m.Via == "pin" && m.Time > 0
		})
	})
	if !slices.Equal(b2.Rooms(), []string{defaultRoom}) || b2.isAccepted(dave.PublicKeyString()) {
		t.Fatalf("offline, Bob shows rooms %v", b2.Rooms())
	}
	start(t, n.joiner(adir))
	seesRooms(t, b2, defaultRoom, "design")
	eventually(t, "Dave on Bob's list", func() bool { return b2.isAccepted(dave.PublicKeyString()) })
}

// S6, D19: the inviter takes join data up to 6400 bytes, and kolo-invite refuses one more.
func TestJoinDataBound(t *testing.T) {
	n := newNetwork(t)
	a, _ := start(t, n.config(dataDir(t)))
	code, err := invite.Parse(mustCreate(t, a, InviteOptions{Uses: 2}).Code)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := koloid.Generate()
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	sw, err := koloswarm.New(ctx, "127.0.0.1:0", id, &koloswarm.Config{LookupInterval: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer sw.Close()
	if err := sw.Bootstrap(ctx, []netip.AddrPort{netip.MustParseAddrPort(n.bootstrap)}); err != nil {
		t.Fatal(err)
	}
	if err := sw.Join(ctx, code.Topic(), &koloswarm.JoinOptions{Lookup: true}); err != nil {
		t.Fatal(err)
	}
	var conn koloswarm.Conn
	for conn == nil {
		ev, err := sw.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if ev.Kind == koloswarm.Connected && code.Matches(ev.Peer.PublicKey()) {
			conn = ev.Peer.Conn()
		}
	}
	big := make([]byte, maxJoinData+1)
	rand.Read(big)
	if _, err := invite.JoinConn(ctx, conn, id, code, &invite.JoinOptions{Data: big}); !errors.Is(err, invite.ErrTooLarge) {
		t.Fatalf("6401 bytes: %v", err)
	}
	if _, err := invite.JoinConn(ctx, conn, id, code, &invite.JoinOptions{Data: big[:maxJoinData]}); !errors.Is(err, invite.ErrDenied) {
		t.Fatalf("6400 bytes reach the inviter, which denies what is not a join request: %v", err)
	}
	dir := dataDir(t)
	store := openStore(t, dir)
	defer store.Close()
	secret, _ := entrySecret(mustHex(t, testKey))
	msgs, _, err := openOwnLogs(store, id, dir, secret)
	if err != nil {
		t.Fatal(err)
	}
	j, err := kolobraid.NewJoin(ctx, id, kolobraid.Options{Backend: kolostore.NewMemory(), App: stateApp, Apply: applyState})
	if err != nil {
		t.Fatal(err)
	}
	for size, ok := range map[int]bool{100: true, maxRequest: false} {
		req, _ := j.Request(make([]byte, size))
		data, _ := json.Marshal(joinData{Header: msgs.Header().Encode(), Request: req})
		if _, err := readJoinData(id.PublicKey(), data); (err == nil) != ok {
			t.Fatalf("a request of %d bytes: %v", len(req), err)
		}
	}
}

// L7: the swarm's connections allow maxStreams incoming streams per direction, merged into a test's DHT config.
func TestStreamCreditIsMerged(t *testing.T) {
	c := &ChatService{}
	if d := c.dhtConfig(); d.Secure.Transport.MaxIncomingStreams != maxStreams {
		t.Fatalf("app: %+v", d.Secure.Transport)
	}
	c.dht = firewall()
	d := c.dhtConfig()
	if d.Secure.Transport.MaxIncomingStreams != maxStreams || d.Secure.Transport.AcceptFunc == nil {
		t.Fatal("the test's firewall was replaced")
	}
	if firewall().Secure.Transport.MaxIncomingStreams != 0 || c.dht.Secure.Transport.MaxIncomingStreams != 0 {
		t.Fatal("the test's config was changed")
	}
}

// S18, R3: a join whose welcome the joiner cannot use, after the admin admitted it, removes the joiner's folder and
// kolo-id. The next join, with a new invite, works in a new folder with a new kolo-id; the old key stays an admitted
// member that never connects.
func TestJoinAfterAnAdmittedFailureStartsAfresh(t *testing.T) {
	n := newNetwork(t)
	second, _ := startPin(t)
	acfg := n.app(dataDir(t))
	acfg.Pins = append(acfg.Pins, pinEntry(second))
	alice, _ := startApp(t, acfg)
	conv := mustCreateConv(t, alice, "Two pins")
	af := engineOf(t, alice, conv.ID)
	broot := dataDir(t)
	bcfg := Config{DataDir: broot, Bootstrap: n.bootstrap, LookupInterval: time.Second, MaxPins: 1}
	bob, bev := startApp(t, bcfg)
	inv, err := alice.CreateInvite(conv.ID, InviteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan error, 1)
	go func() { _, err := bob.Join(inv.Code, ""); got <- err }()
	first := requestOf(t, af, nil)
	af.mu.Lock()
	oldKey := hex.EncodeToString(af.requests[invite.RequestID(first)].ev.Joiner)
	af.mu.Unlock()
	if err := af.Approve(first, false); err != nil {
		t.Fatal(err)
	}
	if err := <-got; !errors.Is(err, ErrTooMany) {
		t.Fatalf("the first join: %v", err)
	}
	if f := folders(t, broot); len(f) != 0 || len(bob.List()) != 0 {
		t.Fatalf("a failed join left %v", f)
	}
	if !af.isAccepted(oldKey) {
		t.Fatal("the admitted key is gone")
	}
	_ = bev
	bob.ServiceShutdown()

	bcfg.MaxPins = 0
	bob2, _ := startApp(t, bcfg)
	inv, err = alice.CreateInvite(conv.ID, InviteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	info := joinConv(t, af, bob2, inv.Code, "")
	if info.Me == oldKey || !info.Joined {
		t.Fatalf("the second join: %+v", info)
	}
}

// settledEntry says whether the entry id is settled in b's order.
func settledEntry(b *kolobraid.Braid, id kolobraid.EntryID) bool {
	for o, err := range b.Entries(0) {
		if err != nil {
			return false
		}
		if o.ID == id {
			return o.Settled
		}
	}
	return false
}

// dirLen is how many entries of who's directory a holds, 0 when it holds none.
func dirLen(a *ChatService, who string) uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	if d := a.dirs[who]; d != nil {
		return d.rep.Len()
	}
	return 0
}

// barrier sends a hello from c to a on their link and waits until a handled it: a reads the link's messages one by
// one, in the order they come, so what c sent before was handled too.
func barrier(t *testing.T, a, c *ChatService) {
	t.Helper()
	hello := func() *wireMsg {
		a.mu.Lock()
		defer a.mu.Unlock()
		if l := a.links[keyOf(c)]; l != nil {
			return l.hello
		}
		return nil
	}
	before := hello()
	c.mu.Lock()
	l := c.links[keyOf(a)]
	c.mu.Unlock()
	if l == nil {
		t.Fatal("no link for the barrier")
	}
	if err := c.sendTo(l, c.hello(l, false)); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the barrier", func() bool { h := hello(); return h != nil && h != before })
}

// S7-3, N1: over a swarm connection and over a relayed one, 40 streams of the chat's kolo-sync wait at once, more than
// kolo-transport's default of 16: the 64 of dhtConfig reached both kinds of connection.
func TestStreamCreditOverRealLinks(t *testing.T) {
	entry, _, rkey := startConnRelay(t)
	n := newNetwork(t)
	for _, relayed := range []bool{false, true} {
		acfg, bcfg := n.config(dataDir(t)), n.joiner(dataDir(t))
		launch := start
		if relayed {
			acfg.Relays, bcfg.Relays, launch = []string{entry}, []string{entry}, startWalled
		}
		a, _ := launch(t, acfg)
		b, _ := launch(t, bcfg)
		joinVia(t, a, b, false)
		introduced(t, a, b)
		if relayed {
			relayedBy(t, b, a, rkey)
		}
		b.mu.Lock()
		conn := b.links[keyOf(a)].p.Conn()
		b.mu.Unlock()
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		for i := range 40 {
			st, err := conn.OpenStream(ctx)
			if err == nil {
				_, err = st.Write([]byte{tagSync})
			}
			if err != nil {
				cancel()
				t.Fatalf("relayed %v: stream %d: %v", relayed, i, err)
			}
		}
		cancel()
	}
}

// S7-6: a joiner whose braid has not met a member yet is not on its own member list, and the window says so.
func TestUnlistedJoinerIsTold(t *testing.T) {
	a, _ := soloBraid(t, applyState)
	idB, _ := koloid.Generate()
	b := joinBraid(t, a, idB, applyState, 0)
	c := &ChatService{braid: b, pub: idB.PublicKey(), accepted: map[string]kolobraid.Member{}}
	if !c.state(b.Status()).NotListed {
		t.Fatal("an unsynced joiner is not told")
	}
	c.accepted[idB.PublicKeyString()] = kolobraid.Member{Key: idB.PublicKey()}
	if c.state(b.Status()).NotListed {
		t.Fatal("a listed member is told it is not")
	}
}

// S7-7: Bob's side jams Alice's braid reader with streams that never end. The queue rule closes the braid part at
// once, so the chat goes on; once the braid's reader lets go (its 60 s message timeout) that is a fault: Alice pauses
// the member list with Bob and says so, and after the pause the braid starts fresh on a new connection.
func TestStuckBraidPausesTheMemberList(t *testing.T) {
	n := newNetwork(t)
	a, aev := startWith(t, n.config(dataDir(t)), func(c *ChatService) { c.pauseFirst = 3 * time.Second })
	b, _ := n.member(t, a, dataDir(t))
	introduced(t, a, b)
	b.mu.Lock()
	conn := b.links[keyOf(a)].p.Conn()
	b.mu.Unlock()
	for range maxWaiting + 8 { // each stream starts a braid message and never ends it
		if st, err := conn.OpenUniStream(t.Context()); err == nil {
			st.Write([]byte{tagBraid, 0})
		}
	}
	if err := b.Send(defaultRoom, "while stuck"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the chat while the braid is stuck", func() bool { return hasText(aev.messages(), "while stuck") })
	// The braid's reader lets go of the stream it holds within its 60 s message timeout; then the fault counts.
	for deadline := time.Now().Add(90 * time.Second); aev.state().Paused != 1; time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the stuck braid was not paused")
		}
	}
	if err := a.AddRoom("after the jam"); err != nil {
		t.Fatal(err)
	}
	seesRooms(t, b, defaultRoom, "after the jam")
}

// S7-11: a store that holds a copy of another member's log is no join that did not finish, even with own logs that
// have no entry and no braid: the folder is refused.
func TestCopyOfAnotherLogIsNoJoin(t *testing.T) {
	me, _ := koloid.Generate()
	other, _ := koloid.Generate()
	c := &ChatService{ctx: t.Context(), id: me, braidDB: kolostore.NewMemory(), store: kolostore.New(kolostore.NewMemory())}
	c.set.DataDir = dataDir(t)
	t.Cleanup(func() { c.store.Close() })
	if err := c.noBraidYet(); err != nil {
		t.Fatalf("an empty store: %v", err)
	}
	theirs := kolostore.New(kolostore.NewMemory())
	t.Cleanup(func() { theirs.Close() })
	l, err := theirs.Create(other)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := c.store.OpenReplica(other.PublicKey(), l.ID(), l.Header().Encode())
	if err != nil {
		t.Fatal(err)
	}
	rep.Close()
	var de *DataDirError
	if err := c.noBraidYet(); !errors.As(err, &de) || de.Dir != c.set.DataDir {
		t.Fatalf("a copy of another log: %v", err)
	}
}
