package main

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	kolodht "github.com/ciranda-dev/kolo-dht"
	koloid "github.com/ciranda-dev/kolo-id"
	koloswarm "github.com/ciranda-dev/kolo-swarm"
)

// netSwitch plays the network of a test: while it is down no entry node answers and no peer may connect.
type netSwitch struct {
	down  atomic.Bool
	tries atomic.Int32 // the asks of the entry nodes that were refused
}

func (s *netSwitch) allow(ed25519.PublicKey, netip.AddrPort) bool { return !s.down.Load() }

// install makes the entry nodes unreachable while the switch is down, until the test ends. Nothing is sent then.
func (s *netSwitch) install(t *testing.T) {
	t.Helper()
	real := bootstrapSwarm
	withBootstrap(t, func(ctx context.Context, sw *koloswarm.Swarm, set settings) error {
		if s.down.Load() {
			s.tries.Add(1)
			return fmt.Errorf("koloswarm: %w", kolodht.ErrNoNodes)
		}
		return real(ctx, sw, set)
	})
}

// cutLinks closes every connection of c to its peers, as a network that went away does.
func cutLinks(c *ChatService) {
	c.mu.Lock()
	var links []*link
	for _, l := range c.links {
		links = append(links, l)
	}
	c.mu.Unlock()
	for _, l := range links {
		l.p.Conn().CloseWithError(77, "test: the network went away")
	}
}

// quick is a pace that makes the retries and the "reconnecting" time short enough for a test.
var quick = pace{retryMin: 50 * time.Millisecond, retryMax: 300 * time.Millisecond, grace: 2 * time.Second, pinRetryMin: 100 * time.Millisecond, pinRetryMax: 500 * time.Millisecond}

// texts lists the texts c's window got from author, in the order they came.
func sentBy(ev *events, author string) []string {
	var out []string
	for _, m := range ev.messages() {
		if m.From == author {
			out = append(out, m.Text)
		}
	}
	return out
}

func (e *events) netStates() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.nets)
}

// sawInOrder says whether got holds want as a subsequence.
func sawInOrder(got []string, want ...string) bool {
	for _, g := range got {
		if len(want) > 0 && g == want[0] {
			want = want[1:]
		}
	}
	return len(want) == 0
}

func mustSend(t *testing.T, c *ChatService, text string) {
	t.Helper()
	begin := time.Now()
	if err := c.Send(defaultRoom, text); err != nil {
		t.Fatalf("Send(%q): %v", text, err)
	}
	if d := time.Since(begin); d > time.Second {
		t.Fatalf("Send(%q) took %v: it must not wait for a peer", text, d)
	}
}

func hexKey(c *ChatService) string { return fmt.Sprintf("%x", c.pub) }

// A peer that drops, or a network that goes away and comes back, never ends the conversation: the room says
// "reconnecting" and then that it cannot reach the network, a message typed meanwhile is kept and sent at once without
// an error, and when the connection returns every message arrives once, in order.
func TestPeerLostAndNetworkBackDeliversEverythingOnce(t *testing.T) {
	n := newNetwork(t)
	var sw netSwitch
	sw.install(t)
	set := func(c *ChatService) { c.allow, c.tune = sw.allow, quick }
	a, aev := startWith(t, n.config(dataDir(t)), set)
	b, bev := startWith(t, n.joiner(dataDir(t)), set)
	joinVia(t, a, b, false)
	introduced(t, a, b)
	ak, bk := hexKey(a), hexKey(b)

	mustSend(t, a, "a1")
	mustSend(t, b, "b1")
	eventually(t, "the first messages", func() bool { return len(sentBy(bev, ak)) == 1 && len(sentBy(aev, bk)) == 1 })

	sw.down.Store(true)
	cutLinks(a)
	cutLinks(b)
	eventually(t, "a to say it cannot reach the network", func() bool { return a.Network() == "offline" })
	if got := aev.netStates(); !sawInOrder(got, "online", "reconnecting", "offline") {
		t.Fatalf("a's states = %v, want online, reconnecting, offline", got)
	}
	for _, text := range []string{"a2", "a3", "a4"} {
		mustSend(t, a, text)
	}
	mustSend(t, b, "b2")
	time.Sleep(1500 * time.Millisecond)
	if got := sentBy(bev, ak); len(got) != 1 {
		t.Fatalf("b got %v while the network was down", got)
	}
	if sw.tries.Load() < 3 {
		t.Errorf("only %d asks of the entry nodes while the network was down: the room must keep asking", sw.tries.Load())
	}

	sw.down.Store(false)
	want := func(c ...string) []string { return c }
	eventually(t, "the messages after the network came back", func() bool {
		return slices.Equal(sentBy(bev, ak), want("a1", "a2", "a3", "a4")) && slices.Equal(sentBy(aev, bk), want("b1", "b2"))
	})
	introduced(t, a, b)
	eventually(t, "a to be online again", func() bool { return a.Network() == "online" && b.Network() == "online" })
	time.Sleep(time.Second)
	if got := sentBy(bev, ak); !slices.Equal(got, want("a1", "a2", "a3", "a4")) {
		t.Errorf("b has %v: a message came twice or went missing", got)
	}
	if got := sentBy(aev, bk); !slices.Equal(got, want("b1", "b2")) {
		t.Errorf("a has %v: a message came twice or went missing", got)
	}
}

// The entry node goes away for 10 seconds and a connection drops in that time: the rooms ask again, find it when it is
// back, and every message arrives once.
func TestEntryNodeDownForTenSeconds(t *testing.T) {
	startEntry := func(id *koloid.Identity, addr string) *kolodht.Node {
		node, err := kolodht.New(context.Background(), addr, id, &kolodht.Config{Server: true})
		if err != nil {
			t.Fatal(err)
		}
		return node
	}
	id, err := koloid.Generate()
	if err != nil {
		t.Fatal(err)
	}
	entry := startEntry(id, "127.0.0.1:0")
	addr := entry.Addr().String()
	t.Cleanup(func() { entry.Close() })
	peer, pinDir := startPin(t)
	n := network{bootstrap: addr, pin: peer, pinDir: pinDir}
	set := func(c *ChatService) {
		c.tune = pace{retryMin: 500 * time.Millisecond, retryMax: 2 * time.Second, grace: 3 * time.Second}
	}
	a, aev := startWith(t, n.config(dataDir(t)), set)
	b, bev := startWith(t, n.joiner(dataDir(t)), set)
	joinVia(t, a, b, false)
	introduced(t, a, b)
	ak, bk := hexKey(a), hexKey(b)
	mustSend(t, a, "a1")
	eventually(t, "the first message", func() bool { return len(sentBy(bev, ak)) == 1 })

	entry.Close()
	cutLinks(a)
	cutLinks(b)
	for _, text := range []string{"a2", "a3"} {
		mustSend(t, a, text)
	}
	mustSend(t, b, "b1")
	time.Sleep(10 * time.Second)
	got := aev.netStates()
	t.Logf("a's states while the entry node was down for 10 s: %v", got)
	if !sawInOrder(got, "online", "reconnecting") {
		t.Errorf("a's states = %v: it must say it is reconnecting", got)
	}
	mustSend(t, a, "a4")

	entry = startEntry(id, addr)
	t.Cleanup(func() { entry.Close() })
	want := func(c ...string) []string { return c }
	eventually(t, "the messages after the entry node came back", func() bool {
		return slices.Equal(sentBy(bev, ak), want("a1", "a2", "a3", "a4")) && slices.Equal(sentBy(aev, bk), want("b1"))
	})
	eventually(t, "both rooms to be online", func() bool { return a.Network() == "online" && b.Network() == "online" })
	time.Sleep(time.Second)
	if got := sentBy(bev, ak); !slices.Equal(got, want("a1", "a2", "a3", "a4")) {
		t.Errorf("b has %v", got)
	}
}

// A peer that is gone while messages are typed finds them when it is back: the sender is not told of an error, and the
// peer shows each once.
func TestSendWhileThePeerIsGoneIsKept(t *testing.T) {
	n := newNetwork(t)
	set := func(c *ChatService) { c.tune = quick }
	a, _ := startWith(t, n.config(dataDir(t)), set)
	bdir := dataDir(t)
	b, _ := startWith(t, n.joiner(bdir), set)
	joinVia(t, a, b, false)
	introduced(t, a, b)
	if err := b.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	eventually(t, "a to lose b", func() bool { return len(a.registered()) == 0 })
	mustSend(t, a, "gone 1")
	mustSend(t, a, "gone 2")

	b2, b2ev := startWith(t, n.joiner(bdir), set)
	eventually(t, "b to get what was typed", func() bool { return slices.Equal(sentBy(b2ev, hexKey(a)), []string{"gone 1", "gone 2"}) })
	introduced(t, a, b2)
	mustSend(t, a, "back")
	eventually(t, "the message after it", func() bool { return slices.Equal(sentBy(b2ev, hexKey(a)), []string{"gone 1", "gone 2", "back"}) })
	time.Sleep(time.Second)
	if got := sentBy(b2ev, hexKey(a)); len(got) != 3 {
		t.Errorf("b has %v", got)
	}
}

// With no entry node answering, many rooms start, say they are offline instead of failing, ask again with a growing wait,
// and all connect once the network is back.
func TestFailingNetworkRetriesThenRecovers(t *testing.T) {
	n := newNetwork(t)
	var sw netSwitch
	sw.install(t)
	sw.down.Store(true)
	root := dataDir(t)
	cfg := n.app(root)
	app := NewConversations(cfg, (&convEvents{}).emit)
	app.pace = quick
	if _, err := app.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.ServiceShutdown() })
	var ids []string
	for i := range 6 {
		ids = append(ids, mustCreateConv(t, app, fmt.Sprint("R", i)).ID)
	}
	for _, row := range app.List() {
		if row.State != "offline" || row.Error != "" || !row.Joined {
			t.Fatalf("row %+v: want offline, joined, no error", row)
		}
	}
	// A message typed in an offline room is kept, with no error.
	if err := app.Send(ids[0], defaultRoom, "typed offline"); err != nil {
		t.Fatalf("Send while offline: %v", err)
	}
	eventually(t, "retries", func() bool { return sw.tries.Load() >= 18 })
	sw.down.Store(false)
	eventually(t, "every room to connect", func() bool {
		for _, row := range app.List() {
			if row.State != "online" {
				return false
			}
		}
		return true
	})
	for _, id := range ids {
		c := engineOf(t, app, id)
		select {
		case <-c.settled:
		default:
			t.Errorf("room %s joined no topic", id)
		}
	}
}

// A join needs the network: with no entry node answering it says so, with the three ways to connect, and the app is
// offline, not failed.
func TestJoinWhileOfflineSaysWhy(t *testing.T) {
	n := newNetwork(t)
	a, _ := start(t, n.config(dataDir(t)))
	code := mustCreate(t, a, InviteOptions{TTLSeconds: 3600}).Code
	real := bootstrapSwarm
	withBootstrap(t, func(ctx context.Context, sw *koloswarm.Swarm, set settings) error {
		if slices.Contains(set.bootstrap, "127.0.0.1:9") {
			return fmt.Errorf("koloswarm: %w", kolodht.ErrNoNodes)
		}
		return real(ctx, sw, set)
	})
	cfg := n.joiner(dataDir(t))
	cfg.Bootstrap = "127.0.0.1:9"
	b, _ := startWith(t, cfg, func(c *ChatService) { c.tune = quick })
	err := b.Join(code)
	var ie *InviteError
	if !errors.As(err, &ie) || !errors.Is(err, ErrNetwork) {
		t.Fatalf("Join: %v", err)
	}
	for _, want := range []string{"could not reach your network", "-bootstrap host:port", "-first"} {
		if !strings.Contains(ie.Message, want) {
			t.Errorf("%q lacks %q", ie.Message, want)
		}
	}
	if b.Network() != "offline" || b.Joined() {
		t.Errorf("state %q, joined %v", b.Network(), b.Joined())
	}
}

// retryDelay grows, stays within its bounds and is jittered.
func TestRetryDelay(t *testing.T) {
	lo, hi := 2*time.Second, 30*time.Second
	seen := map[time.Duration]bool{}
	for attempt := 1; attempt <= 12; attempt++ {
		full := min(lo<<(attempt-1), hi)
		for range 20 {
			d := retryDelay(attempt, lo, hi)
			if d < full/2 || d > full {
				t.Fatalf("attempt %d: %v is not within [%v, %v]", attempt, d, full/2, full)
			}
			seen[d] = true
		}
	}
	if len(seen) < 100 {
		t.Errorf("only %d different delays: no jitter", len(seen))
	}
	if d := retryDelay(1000, lo, hi); d > hi {
		t.Errorf("a late attempt waits %v, over %v", d, hi)
	}
}

// rateProbe counts the requests an entry node gets, to find the most it got in any second.
type rateProbe struct {
	node    *kolodht.Node
	stop    chan struct{}
	done    chan struct{}
	mu      sync.Mutex
	samples []rateSample
}

type rateSample struct {
	at    time.Time
	total uint64
}

func startProbe(node *kolodht.Node) *rateProbe {
	p := &rateProbe{node: node, stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(p.done)
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			st := node.Stats()
			p.mu.Lock()
			p.samples = append(p.samples, rateSample{time.Now(), st.Requests + st.Limited})
			p.mu.Unlock()
			select {
			case <-p.stop:
				return
			case <-tick.C:
			}
		}
	}()
	return p
}

// finish stops the probe and returns the most requests the node got in any second, and how many it dropped.
func (p *rateProbe) finish() (peak uint64, limited uint64) {
	close(p.stop)
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	lo := 0
	for _, s := range p.samples {
		for s.at.Sub(p.samples[lo].at) > time.Second {
			lo++
		}
		peak = max(peak, s.total-p.samples[lo].total)
	}
	return peak, p.node.Stats().Limited
}

// entryWithLimit runs an entry node of the test's own that lets one IPv4 address send limit requests a second, which is
// what the public entry nodes allow: all the rooms of a test reach it from 127.0.0.1.
func entryWithLimit(t *testing.T, limit int) *kolodht.Node {
	t.Helper()
	id, err := koloid.Generate()
	if err != nil {
		t.Fatal(err)
	}
	node, err := kolodht.New(context.Background(), "127.0.0.1:0", id, &kolodht.Config{Server: true, MaxRequestsPerIP: limit, MaxRequestsPerPrefix: 4 * limit})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { node.Close() })
	return node
}

// makeRooms leaves count conversation folders in root, made without any request to the network.
func makeRooms(t *testing.T, root, bootstrap string, count int) []string {
	t.Helper()
	var sw netSwitch
	sw.install(t)
	sw.down.Store(true)
	app := NewConversations(Config{DataDir: root, Bootstrap: bootstrap, MaxConversations: count}, (&convEvents{}).emit)
	app.pace = quick
	if _, err := app.Start(); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := range count {
		ids = append(ids, mustCreateConv(t, app, fmt.Sprint("Room ", i)).ID)
	}
	if err := app.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	sw.down.Store(false) // the network is there for what the test starts next
	return ids
}

// onlineOrder records, from the lists the window gets, the order in which the rooms came online.
type onlineOrder struct {
	*convEvents
	mu    sync.Mutex
	order []string
}

func (o *onlineOrder) emit(name string, data any) {
	o.convEvents.emit(name, data)
	if rows, ok := data.([]ConversationInfo); ok && name == "chat:conversations" {
		o.mu.Lock()
		defer o.mu.Unlock()
		for _, r := range rows {
			if r.State == "online" && !slices.Contains(o.order, r.ID) {
				o.order = append(o.order, r.ID)
			}
		}
	}
}

func (o *onlineOrder) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.order)
}

const (
	manyRooms  = 40
	entryLimit = 50 // requests a second from one IPv4 address, the limit of the public entry nodes
)

// The start of 40 rooms against an entry node that lets one address send 50 requests a second: the window gets its
// list at once, the room on screen starts first, every room ends up online, none is dropped by the limit and the most
// requests in any second stay under it.
func TestFortyRoomsStartGently(t *testing.T) {
	entry := entryWithLimit(t, entryLimit)
	root := dataDir(t)
	ids := makeRooms(t, root, entry.Addr().String(), manyRooms)

	order := &onlineOrder{convEvents: &convEvents{}}
	app := NewConversations(Config{DataDir: root, Bootstrap: entry.Addr().String(), MaxConversations: manyRooms}, order.emit)
	probe := startProbe(entry)
	begin := time.Now()
	info, err := app.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.ServiceShutdown() })
	took := time.Since(begin)
	if len(info.Conversations) != manyRooms {
		t.Fatalf("Start lists %d rooms, want %d", len(info.Conversations), manyRooms)
	}
	if online := order.count(); online >= manyRooms || online == 0 {
		t.Errorf("%d rooms online when Start returned: it must return after the first few", online)
	}
	for _, row := range info.Conversations {
		if row.Error != "" || !row.Joined || row.State == "" {
			t.Fatalf("row %+v", row)
		}
	}
	last := ids[len(ids)-1] // the newest room, which starts last unless it is on screen
	app.Focus(last)
	deadline := time.Now().Add(3 * time.Minute)
	for order.count() < manyRooms {
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d rooms online after %v", order.count(), manyRooms, time.Since(begin))
		}
		time.Sleep(100 * time.Millisecond)
	}
	allOnline := time.Since(begin)
	startPeak, startLimited := probe.finish()
	steady := startProbe(entry) // the rooms' own lookups and announcements, once all are up
	time.Sleep(10 * time.Second)
	steadyPeak, steadyLimited := steady.finish()
	rank := slices.Index(order.order, last)
	t.Logf("%d rooms: Start returned after %v, all online after %v; the entry node saw at most %d requests in a second while they started and %d once they were up, dropped %d; the room on screen came online number %d",
		manyRooms, took.Round(time.Millisecond), allOnline.Round(time.Millisecond), startPeak, steadyPeak, steadyLimited, rank+1)
	if startLimited != 0 || steadyLimited != 0 {
		t.Errorf("the entry node dropped %d requests over its limit", steadyLimited)
	}
	if startPeak >= entryLimit || steadyPeak >= entryLimit {
		t.Errorf("the entry node got %d requests in one second, the limit is %d", max(startPeak, steadyPeak), entryLimit)
	}
	if rank >= 3*startSlots {
		t.Errorf("the room on screen came online number %d: it must start within two waves of Focus", rank+1)
	}
	for _, row := range app.List() {
		if row.State != "online" || row.Error != "" {
			t.Errorf("row %+v: stuck", row)
		}
	}
}

// Without the pacing the same start does hit the limit, and the rooms still end up online by asking again: the control of
// the test above, and the real engines' retry.
func TestFortyRoomsAtOnceHitTheLimitAndRecover(t *testing.T) {
	entry := entryWithLimit(t, entryLimit)
	root := dataDir(t)
	makeRooms(t, root, entry.Addr().String(), manyRooms)

	order := &onlineOrder{convEvents: &convEvents{}}
	app := NewConversations(Config{DataDir: root, Bootstrap: entry.Addr().String(), MaxConversations: manyRooms}, order.emit)
	app.pace = pace{slots: manyRooms, pause: time.Millisecond, jitter: time.Millisecond, retryMin: 500 * time.Millisecond, retryMax: 4 * time.Second}
	begin := time.Now()
	if _, err := app.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.ServiceShutdown() })
	deadline := time.Now().Add(3 * time.Minute)
	for order.count() < manyRooms {
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d rooms online after %v", order.count(), manyRooms, time.Since(begin))
		}
		time.Sleep(100 * time.Millisecond)
	}
	limited := entry.Stats().Limited
	t.Logf("all at once: %d rooms online after %v, %d requests dropped by the limit", manyRooms, time.Since(begin).Round(time.Millisecond), limited)
	if limited == 0 {
		t.Errorf("no request was dropped: this test does not show the problem the pacing solves")
	}
}
