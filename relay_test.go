package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	kolodht "github.com/ciranda-dev/kolo-dht"
	koloid "github.com/ciranda-dev/kolo-id"
	invite "github.com/ciranda-dev/kolo-invite"
	kololog "github.com/ciranda-dev/kolo-log"
	kolorelay "github.com/ciranda-dev/kolo-relay"
	kolosecure "github.com/ciranda-dev/kolo-secure"
	koloswarm "github.com/ciranda-dev/kolo-swarm"
	kolosync "github.com/ciranda-dev/kolo-sync"
	kolotransport "github.com/ciranda-dev/kolo-transport"
)

// relayEntry is one connection relay in the -relay form, with a fresh key, at addr.
func relayEntry(t *testing.T, addr string) (string, ed25519.PublicKey) {
	t.Helper()
	id, err := koloid.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return addr + "@" + id.PublicKeyString(), id.PublicKey()
}

func TestRelaySettingSources(t *testing.T) {
	a, ka := relayEntry(t, "127.0.0.1:7001")
	b, _ := relayEntry(t, "127.0.0.1:7002")
	c, _ := relayEntry(t, "relay.example.org:7003")
	dir := t.TempDir()
	file := filepath.Join(dir, "relays.txt")
	if err := os.WriteFile(file, []byte("# relays\n"+c+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolve := func(args []string, env map[string]string) (settings, error) {
		t.Helper()
		cfg, err := LoadConfig(args, func(k string) string { return env[k] }, io.Discard)
		if err != nil {
			return settings{}, err
		}
		cfg.DataDir = t.TempDir()
		return cfg.resolve()
	}
	s, err := resolve([]string{"-relay", a, "-relay", b + " , " + a, "-relay-file", file}, map[string]string{"KOLO_CHAT_RELAYS": b})
	if err != nil || relaysText(s.relays) != a+","+b+","+c || s.relaysFrom != "-relay, -relay-file" || !s.relays[0].PublicKey.Equal(ka) {
		t.Fatalf("flags: %q %q %v", relaysText(s.relays), s.relaysFrom, err)
	}
	for _, tc := range []struct {
		name       string
		args       []string
		env        string
		text, from string
	}{
		{"file alone, over the variable", []string{"-relay-file", file}, a, c, "-relay-file"},
		{"flag alone", []string{"-relay", a}, b, a, "-relay"},
		{"variable", nil, b + ", ,", b, "KOLO_CHAT_RELAYS"},
		{"none", nil, "", "", ""},
		{"empty flag keeps the variable out", []string{"-relay", ""}, b, "", ""},
		{"only commas", nil, " , ,", "", ""},
	} {
		s, err := resolve(tc.args, map[string]string{"KOLO_CHAT_RELAYS": tc.env})
		if err != nil || relaysText(s.relays) != tc.text || s.relaysFrom != tc.from {
			t.Errorf("%s: %q %q %v", tc.name, relaysText(s.relays), s.relaysFrom, err)
		}
	}
	if s, err = (Config{DataDir: t.TempDir(), Relays: []string{a}}).resolve(); err != nil || relaysText(s.relays) != a || s.relaysFrom != "-relay" {
		t.Errorf("set in code: %q %q %v", relaysText(s.relays), s.relaysFrom, err)
	}
	if s, err = resolve([]string{"-relay", a}, nil); err != nil || s.inConversation || len(s.relays) != 1 {
		t.Errorf("a joiner takes relays too: %v %v", s.relays, err)
	}

	var seventeen []string
	for i := range 17 {
		e, _ := relayEntry(t, fmt.Sprintf("127.0.0.1:%d", 8000+i))
		seventeen = append(seventeen, e)
	}
	if s, err = resolve([]string{"-relay", strings.Join(seventeen[:16], ",")}, nil); err != nil || len(s.relays) != 16 {
		t.Errorf("16 relays: %d %v", len(s.relays), err)
	}
	key := hex.EncodeToString(ka)
	other, _ := relayEntry(t, "127.0.0.1:7001")
	zero := filepath.Join(dir, "zero")
	if err := os.Symlink("/dev/zero", zero); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(big, bytes.Repeat([]byte("#"), 64<<10+1), 0o600); err != nil {
		t.Fatal(err)
	}
	clash := filepath.Join(dir, "clash.txt")
	if err := os.WriteFile(clash, []byte(strings.Replace(a, "7001", "7009", 1)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, setting string
		args          []string
	}{
		{"17 relays", "relay", []string{"-relay", strings.Join(seventeen, ",")}},
		{"17 with a file", "relay-file", []string{"-relay", strings.Join(seventeen[:16], ","), "-relay-file", file}},
		{"one key, two addresses", "relay", []string{"-relay", a, "-relay", strings.Replace(a, "7001", "7009", 1)}},
		{"one address, two keys", "relay", []string{"-relay", a + "," + other}},
		{"a flag and a file clash", "relay-file", []string{"-relay", a, "-relay-file", clash}},
		{"IPv6", "relay", []string{"-relay", "[::1]:7001@" + key}},
		{"IPv4-mapped IPv6", "relay", []string{"-relay", "[::ffff:127.0.0.1]:7001@" + key}},
		{"bare IPv6", "relay", []string{"-relay", "::1:7001@" + key}},
		{"port 0", "relay", []string{"-relay", "127.0.0.1:0@" + key}},
		{"port 70000", "relay", []string{"-relay", "127.0.0.1:70000@" + key}},
		{"short key", "relay", []string{"-relay", "127.0.0.1:7001@abcd"}},
		{"long key", "relay", []string{"-relay", "127.0.0.1:7001@" + key + "00"}},
		{"key not hex", "relay", []string{"-relay", "127.0.0.1:7001@" + strings.Repeat("zz", 32)}},
		{"no key", "relay", []string{"-relay", "127.0.0.1:7001"}},
		{"symlink to a device", "relay-file", []string{"-relay-file", zero}},
		{"directory", "relay-file", []string{"-relay-file", dir}},
		{"file over 64 KiB", "relay-file", []string{"-relay-file", big}},
	} {
		_, err := resolve(tc.args, nil)
		wantConfigError(t, tc.name, err, ErrMalformed, tc.setting)
	}
	_, err = resolve([]string{"-relay-file", filepath.Join(dir, "none")}, nil)
	wantConfigError(t, "missing file", err, fs.ErrNotExist, "relay-file")
	if errors.Is(err, ErrMalformed) || !strings.Contains(err.Error(), "cannot be read") {
		t.Errorf("a missing file is not a malformed one: %v", err)
	}
	_, err = resolve(nil, map[string]string{"KOLO_CHAT_RELAYS": "nope"})
	wantConfigError(t, "bad variable", err, ErrMalformed, "relay")
	if err == nil || !strings.Contains(err.Error(), "KOLO_CHAT_RELAYS: ") {
		t.Errorf("a bad variable does not say where it came from: %v", err)
	}
}

func TestFindTimeoutWithRelays(t *testing.T) {
	a, _ := relayEntry(t, "127.0.0.1:7001")
	for _, tc := range []struct {
		cfg  Config
		want time.Duration
	}{
		{Config{}, time.Minute},
		{Config{Relays: []string{a}}, 195 * time.Second},
	} {
		tc.cfg.DataDir = t.TempDir()
		s, err := tc.cfg.resolve()
		if err != nil || s.findTimeout() != tc.want {
			t.Errorf("%+v: %v %v", tc.cfg, s.findTimeout(), err)
		}
	}
}

func TestHelpNamesTheRelayFlags(t *testing.T) {
	var out bytes.Buffer
	if _, err := LoadConfig([]string{"-h"}, func(string) string { return "" }, &out); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("err: %v", err)
	}
	for _, want := range []string{"-relay value", "-relay-file value", "KOLO_CHAT_RELAYS", "host:port@key"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help has no %q: %s", want, out.String())
		}
	}
}

func TestRelayReason(t *testing.T) {
	for err, want := range map[error]string{
		kolosecure.ErrUnexpectedPeer: "wrong-key",
		koloswarm.ErrInvalidConfig:   "bad-key",
		kolorelay.ErrNotAllowed:      "not-allowed",
		kolorelay.ErrLimit:           "full",
		koloswarm.ErrLost:            "unreachable",
	} {
		if got := relayReason(fmt.Errorf("wrapped: %w", err)); got != want {
			t.Errorf("%v: %q, want %q", err, got, want)
		}
	}
}

// Each RelayChanged event sets the state of its relay, the window hears every change, and the terminal hears a failure
// once.
func TestRelayChanged(t *testing.T) {
	logs := captureLog(t)
	a, ka := relayEntry(t, "127.0.0.1:7001")
	b, kb := relayEntry(t, "127.0.0.1:7002")
	ev := &events{}
	c := NewChatService(Config{}, ev.emit)
	var err error
	if c.set, err = (Config{DataDir: t.TempDir(), Relays: []string{a, b}}).resolve(); err != nil {
		t.Fatal(err)
	}
	c.resetRelays()
	for _, step := range []struct {
		ev   koloswarm.Event
		want []string
	}{
		{koloswarm.Event{}, []string{"waiting/", "waiting/"}},
		{koloswarm.Event{Kind: koloswarm.RelayChanged, PublicKey: ka}, []string{"ready/", "waiting/"}},
		{koloswarm.Event{Kind: koloswarm.RelayChanged, PublicKey: kb, Err: kolosecure.ErrUnexpectedPeer}, []string{"ready/", "failed/wrong-key"}},
		{koloswarm.Event{Kind: koloswarm.RelayChanged, PublicKey: kb, Err: kolosecure.ErrUnexpectedPeer}, []string{"ready/", "failed/wrong-key"}},
		{koloswarm.Event{Kind: koloswarm.RelayChanged, PublicKey: ka, Err: kolorelay.ErrReservationEnded}, []string{"waiting/", "failed/wrong-key"}},
		{koloswarm.Event{Kind: koloswarm.RelayChanged, PublicKey: ka}, []string{"ready/", "failed/wrong-key"}},
		{koloswarm.Event{Kind: koloswarm.RelayChanged, PublicKey: ka, Err: koloswarm.ErrClosed}, []string{"waiting/", "failed/wrong-key"}},
		{koloswarm.Event{Kind: koloswarm.RelayChanged, PublicKey: make(ed25519.PublicKey, 32)}, []string{"waiting/", "failed/wrong-key"}},
	} {
		if step.ev.Kind == koloswarm.RelayChanged {
			c.relayChanged(step.ev)
		}
		if got := ev.relayStates(); !slices.Equal(got, step.want) {
			t.Fatalf("after %v: %v, want %v", step.ev.Err, got, step.want)
		}
	}
	if n := strings.Count(logs.String(), "relay 127.0.0.1:7002: "); n != 1 {
		t.Errorf("the failure was logged %d times: %s", n, logs.String())
	}
	ev.mu.Lock()
	defer ev.mu.Unlock()
	if strings.Contains(logs.String(), "relay 127.0.0.1:7001: ") {
		t.Errorf("a renewal or a leave was logged: %s", logs.String())
	}
	for i := 1; i < len(ev.relays); i++ {
		if ev.relays[i].Seq <= ev.relays[i-1].Seq {
			t.Fatalf("seq does not grow: %d after %d", ev.relays[i].Seq, ev.relays[i-1].Seq)
		}
	}
	if len(ev.relays) != 8 || ev.relays[0].From != "-relay" {
		t.Errorf("events: %+v", ev.relays)
	}
}

// relayStates returns the states of the relays in the last "chat:relays" event, as "state/error".
func (e *events) relayStates() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.relays) == 0 {
		return nil
	}
	newest := e.relays[0]
	for _, st := range e.relays {
		if st.Seq >= newest.Seq {
			newest = st
		}
	}
	var out []string
	for _, r := range newest.Relays {
		out = append(out, r.State+"/"+r.Error)
	}
	return out
}

// startConnRelay runs a kolo-relay on 127.0.0.1 and returns it as a -relay entry. Every client of a test is
// 127.0.0.1, so the per-address limit is raised.
func startConnRelay(t *testing.T) (entry, addr string, key ed25519.PublicKey) {
	t.Helper()
	return startConnRelayWith(t, &kolorelay.ServerConfig{Secure: loopSecure()})
}

func startConnRelayWith(t *testing.T, cfg *kolorelay.ServerConfig) (entry, addr string, key ed25519.PublicKey) {
	t.Helper()
	id, err := koloid.Generate()
	if err != nil {
		t.Fatal(err)
	}
	srv, err := kolorelay.Listen(context.Background(), "127.0.0.1:0", id, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	addr = srv.Addr().String()
	return addr + "@" + id.PublicKeyString(), addr, id.PublicKey()
}

func loopSecure() *kolosecure.Config {
	return &kolosecure.Config{Transport: &kolotransport.Config{MaxConns: 256, MaxConnsPerIP: 256, MaxDialed: 256}}
}

// tapRelay stands where a relay operator stands. The chats list it as their relay: it holds the relay key they check
// and passes every stream on to a real kolo-relay as the same chat, so it keeps every byte a relay operator can read.
// It stands in only for the identities it is given.
type tapRelay struct {
	ids   map[string]*koloid.Identity
	key   ed25519.PublicKey
	entry string
	to    string
	toKey ed25519.PublicKey
	wg    sync.WaitGroup
	mu    sync.Mutex
	seen  []byte
}

func startTapRelay(t *testing.T, ids ...*koloid.Identity) *tapRelay {
	t.Helper()
	_, to, toKey := startConnRelay(t)
	id, err := koloid.Generate()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ln, err := kolosecure.Listen(ctx, "127.0.0.1:0", id, loopSecure())
	if err != nil {
		t.Fatal(err)
	}
	tap := &tapRelay{ids: map[string]*koloid.Identity{}, key: id.PublicKey(), to: to, toKey: toKey,
		entry: ln.Addr().String() + "@" + id.PublicKeyString()}
	for _, x := range ids {
		tap.ids[string(x.PublicKey())] = x
	}
	tap.wg.Go(func() {
		for {
			c, err := ln.Accept(ctx)
			if err != nil {
				return
			}
			tap.wg.Go(func() { tap.serve(ctx, c) })
		}
	})
	t.Cleanup(func() {
		cancel()
		ln.Close()
		tap.wg.Wait()
	})
	return tap
}

func (tap *tapRelay) serve(ctx context.Context, c *kolosecure.Conn) {
	defer c.Close()
	id := tap.ids[string(c.RemotePublicKey())]
	if id == nil {
		return
	}
	rc, err := kolosecure.Dial(ctx, tap.to, id, tap.toKey, &kolosecure.Config{Transport: &kolotransport.Config{KeepAlivePeriod: 5 * time.Second}})
	if err != nil {
		return
	}
	defer rc.Close()
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	context.AfterFunc(c.Context(), cancel)
	context.AfterFunc(rc.Context(), cancel)
	var wg sync.WaitGroup
	wg.Go(func() { tap.join(cctx, &wg, c, rc) })
	wg.Go(func() { tap.join(cctx, &wg, rc, c) })
	<-cctx.Done()
	c.Close()
	rc.Close()
	wg.Wait()
}

// join passes every stream that from opens to a new stream of to, both ways.
func (tap *tapRelay) join(ctx context.Context, wg *sync.WaitGroup, from, to *kolosecure.Conn) {
	for {
		a, err := from.AcceptStream(ctx)
		if err != nil {
			return
		}
		b, err := to.OpenStream(ctx)
		if err != nil {
			a.CancelRead(0)
			a.CancelWrite(0)
			return
		}
		wg.Go(func() { tap.pipe(b, a) })
		wg.Go(func() { tap.pipe(a, b) })
	}
}

func (tap *tapRelay) pipe(dst, src *kolotransport.Stream) {
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			tap.mu.Lock()
			tap.seen = append(tap.seen, buf[:n]...)
			tap.mu.Unlock()
			if _, werr := dst.Write(buf[:n]); werr != nil {
				src.CancelRead(0)
				return
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				dst.Close()
			} else {
				dst.CancelWrite(0)
			}
			return
		}
	}
}

func (tap *tapRelay) captured() []byte {
	tap.mu.Lock()
	defer tap.mu.Unlock()
	return bytes.Clone(tap.seen)
}

// firewall is a kolo-dht setting that refuses every incoming connection, as a strict NAT does. A relayed connection
// still works: the chat dialed the relay.
func firewall() *kolodht.Config {
	return &kolodht.Config{Secure: &kolosecure.Config{Transport: &kolotransport.Config{
		AcceptFunc: func(netip.AddrPort) bool { return false }}}}
}

// startWalled is start behind the firewall.
func startWalled(t *testing.T, cfg Config) (*ChatService, *events) {
	t.Helper()
	ev := &events{}
	c := NewChatService(cfg, ev.emit)
	c.dht = firewall()
	if _, err := c.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.ServiceShutdown() })
	return c, ev
}

// identityIn makes the identity of a chat whose data directory is dir, so a test knows it before the chat starts.
func identityIn(t *testing.T, dir string) *koloid.Identity {
	t.Helper()
	id, err := loadIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// relayOf returns the key of the relay that carries c's connection to other, nil when it is direct or missing.
func relayOf(c, other *ChatService) ed25519.PublicKey {
	c.mu.Lock()
	l := c.links[hex.EncodeToString(other.pub)]
	c.mu.Unlock()
	if l == nil {
		return nil
	}
	if rc, ok := l.p.Conn().(*kolorelay.Conn); ok {
		return rc.RelayPublicKey()
	}
	return nil
}

func relayedBy(t *testing.T, c, other *ChatService, relay ed25519.PublicKey) {
	t.Helper()
	eventually(t, "a connection through the relay", func() bool { return relayOf(c, other).Equal(relay) })
}

// entryKeys derives the key of every entry of c's two logs, which never travel.
func entryKeys(t *testing.T, c *ChatService) [][]byte {
	t.Helper()
	var out [][]byte
	for _, l := range []interface {
		Header() kololog.LogHeader
		Len() uint64
	}{c.log, c.dirLog} {
		d, err := kololog.NewKeyDeriver(c.secret, l.Header().LogKey, l.Header())
		if err != nil {
			t.Fatal(err)
		}
		for i := range l.Len() {
			k, err := d.Key(i)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, bytes.Clone(k))
		}
		d.Close()
	}
	return out
}

// Two chats that cannot connect directly meet through the relay both list: the invite and its grant, the hellos, a
// message, the history B missed while it was closed and a name change all arrive. The pin takes nobody's logs, so
// the history and the name come from A, through the relay. The relay operator's view holds none of the text, the
// conversation key, the entry secret or an entry key.
func TestChatThroughRelay(t *testing.T) {
	adir, bdir := dataDir(t), dataDir(t)
	tap := startTapRelay(t, identityIn(t, adir), identityIn(t, bdir))
	stranger, err := koloid.Generate()
	if err != nil {
		t.Fatal(err)
	}
	pinPeer, _ := startPin(t, stranger.PublicKey())
	n := network{bootstrap: startBootstrap(t), pin: pinPeer}
	acfg, bcfg := n.config(adir), n.joiner(bdir)
	acfg.Relays, bcfg.Relays = []string{tap.entry}, []string{tap.entry}

	a, aev := startWalled(t, acfg)
	b, bev := startWalled(t, bcfg)
	eventually(t, "A's place at the relay", func() bool { return slices.Equal(aev.relayStates(), []string{"ready/"}) })
	if st := bev.relayStates(); !slices.Equal(st, []string{"waiting/"}) {
		t.Fatalf("a joiner holds no place before it joins: %v", st)
	}
	joinVia(t, a, b, false)
	introduced(t, a, b)
	relayedBy(t, a, b, tap.key)
	relayedBy(t, b, a, tap.key)

	marker := "kolo-chat relay marker, end to end only"
	if err := a.Send(defaultRoom, marker+" 0"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the message through the relay", func() bool {
		return slices.ContainsFunc(bev.messages(), func(m ChatMsg) bool {
			return m.From == keyOf(a) && m.Room == defaultRoom && m.Text == marker+" 0" && m.Via == "direct" && m.Time > 0
		})
	})

	b.ServiceShutdown()
	for i := 1; i <= 3; i++ {
		if err := a.Send(defaultRoom, fmt.Sprintf("%s %d", marker, i)); err != nil {
			t.Fatal(err)
		}
	}
	b, bev = startWalled(t, bcfg)
	eventually(t, "the missed messages from A, through the relay", func() bool {
		var got []string
		for _, m := range bev.messages() {
			if m.Via == "peer" {
				got = append(got, m.Text)
			}
		}
		return slices.Equal(got, []string{marker + " 1", marker + " 2", marker + " 3"})
	})
	name := "Name through the relay"
	if err := a.SetProfile(name, ""); err != nil {
		t.Fatal(err)
	}
	sees(t, b, a, name)
	if err := a.AddRoom("relayed"); err != nil { // S7c: the braid's streams pass the relay too
		t.Fatal(err)
	}
	seesRooms(t, b, defaultRoom, "relayed")
	relayedBy(t, b, a, tap.key) // the firewall held: still relayed

	seen := tap.captured()
	if len(seen) < 4*len(marker) {
		t.Fatalf("the relay passed only %d bytes", len(seen))
	}
	if !bytes.Contains(seen, a.pub) || !bytes.Contains(seen, b.pub) { // what the relay legitimately sees
		t.Fatal("the tap did not see the members' keys: it does not read what a relay operator reads")
	}
	secrets := map[string][]byte{"the text": []byte(marker[:16]), "the name": []byte(name), "the conversation key in hex": []byte(testKey)}
	if key, err := hex.DecodeString(testKey); err == nil {
		secrets["the conversation key"] = key
	}
	secrets["A's entry secret"] = bytes.Clone(a.secret[:])
	secrets["B's entry secret"] = bytes.Clone(b.secret[:])
	for i, k := range append(entryKeys(t, a), entryKeys(t, b)...) {
		secrets[fmt.Sprintf("entry key %d", i)] = k
	}
	for what, s := range secrets {
		if bytes.Contains(seen, s) {
			t.Errorf("the relay saw %s", what)
		}
	}
}

// A key that is not a member reaches a chat through its relay, as anyone may (an invite starts that way), but its
// hello is refused even when it replays a member's, and it cannot pull the member's logs: the key the relayed
// connection proved is the one every check uses.
func TestStrangerThroughRelayIsRefused(t *testing.T) {
	logs := captureLog(t)
	entry, _, rkey := startConnRelay(t)
	n := newNetwork(t)
	acfg, bcfg := n.config(dataDir(t)), n.joiner(dataDir(t))
	acfg.Relays, bcfg.Relays = []string{entry}, []string{entry}
	a, _ := startWalled(t, acfg)
	b, _ := startWalled(t, bcfg)
	joinVia(t, a, b, false)
	introduced(t, a, b)
	relayedBy(t, a, b, rkey)

	stranger, err := koloid.Generate()
	if err != nil {
		t.Fatal(err)
	}
	relays, err := koloswarm.ParseRelays(entry)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	sw, err := koloswarm.New(ctx, "127.0.0.1:0", stranger, &koloswarm.Config{DHT: firewall(), Relays: relays, LookupInterval: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer sw.Close()
	if err := sw.Bootstrap(ctx, []netip.AddrPort{netip.MustParseAddrPort(n.bootstrap)}); err != nil {
		t.Fatal(err)
	}
	if err := sw.Join(ctx, a.topic(), &koloswarm.JoinOptions{Lookup: true}); err != nil { // the topic is public on the DHT
		t.Fatal(err)
	}
	var conn koloswarm.Conn
	for conn == nil {
		ev, err := sw.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if ev.Kind == koloswarm.Connected && ev.Peer.PublicKey().Equal(a.pub) {
			conn = ev.Peer.Conn()
		}
	}
	if rc, ok := conn.(*kolorelay.Conn); !ok || !rc.RelayPublicKey().Equal(rkey) {
		t.Fatalf("the stranger is not relayed: %T", conn)
	}
	hello, err := json.Marshal(wireMsg{Kind: "hello", Header: b.log.Header().Encode(), DirHeader: b.dirLog.Header().Encode(), Proof: b.proof(b.pub, a.pub)})
	if err != nil {
		t.Fatal(err)
	}
	st, err := conn.OpenUniStream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Write(append([]byte{tagMsg}, hello...)); err != nil {
		t.Fatal(err)
	}
	st.Close()
	eventually(t, "the replayed hello to be refused", func() bool {
		return strings.Contains(logs.String(), "hello refused from "+stranger.PublicKeyString()[:8])
	})
	// The stranger's own signed headers pass; only the proof, made under another conversation key, is wrong.
	sdir := dataDir(t)
	store := openStore(t, sdir)
	defer store.Close()
	other, err := entrySecret(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	msgs, dirLog, err := openOwnLogs(store, stranger, sdir, other)
	if err != nil {
		t.Fatal(err)
	}
	outsider := &ChatService{}
	outsider.set.conversationKey[0] = 1
	hello, err = json.Marshal(wireMsg{Kind: "hello", Header: msgs.Header().Encode(), DirHeader: dirLog.Header().Encode(),
		Proof: outsider.proof(stranger.PublicKey(), a.pub)})
	if err != nil {
		t.Fatal(err)
	}
	if st, err := conn.OpenUniStream(ctx); err != nil {
		t.Fatal(err)
	} else if _, err := st.Write(append([]byte{tagMsg}, hello...)); err != nil {
		t.Fatal(err)
	} else {
		st.Close()
	}
	eventually(t, "the hello without the conversation key to be refused", func() bool {
		return strings.Contains(logs.String(), "hello refused from "+stranger.PublicKeyString()[:8]+": it does not prove the conversation key")
	})
	fctx, fcancel := context.WithTimeout(ctx, 3*time.Second)
	defer fcancel()
	if _, err := kolosync.FetchHead(fctx, conn, a.pub, a.log.ID()); err == nil {
		t.Fatal("the stranger pulled A's log through the relay")
	}
	a.mu.Lock()
	_, member := a.members[stranger.PublicKeyString()]
	a.mu.Unlock()
	if member || !relayOf(a, b).Equal(rkey) {
		t.Fatalf("member %v; A and B through the relay: %v", member, relayOf(a, b).Equal(rkey))
	}
}

// A relay that answers with another key than the listed one is refused, and the window and the terminal say so.
func TestWrongRelayKeyIsReported(t *testing.T) {
	logs := captureLog(t)
	_, addr, _ := startConnRelay(t)
	_, wrong := relayEntry(t, addr)
	n := newNetwork(t)
	cfg := n.app(dataDir(t))
	cfg.ConversationKey = testKey
	cfg.Relays = []string{addr + "@" + hex.EncodeToString(wrong)}
	a, cev := startApp(t, cfg)
	ev := cev.of(a.List()[0].ID)
	eventually(t, "the wrong key in the window", func() bool { return slices.Equal(ev.relayStates(), []string{"failed/wrong-key"}) })
	if !strings.Contains(logs.String(), "relay "+addr+": ") {
		t.Errorf("the terminal does not name the relay: %s", logs.String())
	}
	ev.mu.Lock()
	last := ev.relays[len(ev.relays)-1]
	ev.mu.Unlock()
	if last.From != "-relay" || last.Relays[0].Addr != addr || last.Relays[0].Key != hex.EncodeToString(wrong) {
		t.Errorf("status: %+v", last)
	}
}

// Someone who has no conversation yet holds no place at a relay, so kolo-swarm never reports a wrong key: the check
// at start does, at once, in the app's own status and in the terminal.
func TestWrongRelayKeyIsReportedToAJoiner(t *testing.T) {
	logs := captureLog(t)
	_, addr, _ := startConnRelay(t)
	_, wrong := relayEntry(t, addr)
	n := newNetwork(t)
	cfg := Config{DataDir: dataDir(t), Bootstrap: n.bootstrap, Relays: []string{addr + "@" + hex.EncodeToString(wrong)}}
	_, cev := startApp(t, cfg)
	eventually(t, "the wrong key in a joiner's window", func() bool { return slices.Equal(cev.of("").relayStates(), []string{"failed/wrong-key"}) })
	if !strings.Contains(logs.String(), "relay "+addr+": ") {
		t.Errorf("the terminal does not name the relay: %s", logs.String())
	}
}

// A relay with the right key is never shown as failed, also across the renewals of a place whose lifetime is one
// second.
func TestRelayRenewalIsNotAFailure(t *testing.T) {
	logs := captureLog(t)
	entry, addr, _ := startConnRelayWith(t, &kolorelay.ServerConfig{Secure: loopSecure(), ReservationLifetime: time.Second})
	n := newNetwork(t)
	cfg := n.config(dataDir(t))
	cfg.Relays = []string{entry}
	_, ev := start(t, cfg)
	readies := func() (n int, failed bool) {
		ev.mu.Lock()
		defer ev.mu.Unlock()
		for _, st := range ev.relays {
			n += btoi(st.Relays[0].State == "ready")
			failed = failed || st.Relays[0].State == "failed"
		}
		return n, failed
	}
	eventually(t, "three places in a row", func() bool { n, _ := readies(); return n >= 3 })
	if _, failed := readies(); failed || strings.Contains(logs.String(), "relay "+addr+": ") {
		t.Fatalf("a renewal shows as a failure: %+v %s", ev.relays, logs.String())
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func TestJoinMessagesWithRelays(t *testing.T) {
	a, _ := relayEntry(t, "127.0.0.1:7001")
	plain, err := (Config{DataDir: t.TempDir()}).resolve()
	if err != nil {
		t.Fatal(err)
	}
	relayed, err := (Config{DataDir: t.TempDir(), Relays: []string{a}}).resolve()
	if err != nil {
		t.Fatal(err)
	}
	unreachable := fmt.Errorf("%w: not found", invite.ErrUnreachable)
	if got := plain.joinFailure(unreachable); got != describeJoin(unreachable) || strings.Contains(got, "relay") {
		t.Errorf("without relays: %q", got)
	}
	if got := relayed.joinFailure(unreachable); !strings.Contains(got, "through the connection relays") || !strings.Contains(got, "same relay") {
		t.Errorf("with relays: %q", got)
	}
	if got := relayed.joinFailure(invite.ErrExpired); got != describeJoin(invite.ErrExpired) {
		t.Errorf("another failure with relays: %q", got)
	}
	if plain.joinLookingMessage() != "Looking for the person who invited you..." || !strings.Contains(relayed.joinLookingMessage(), "3 minutes") {
		t.Errorf("looking: %q, %q", plain.joinLookingMessage(), relayed.joinLookingMessage())
	}
}

// A join whose inviter is gone fails with the sentence that names the relays, in the error and in the window.
func TestJoinThroughRelaysFailsWithItsReason(t *testing.T) {
	entry, _, _ := startConnRelay(t)
	n := newNetwork(t)
	owner, _ := start(t, n.config(dataDir(t)))
	code := mustCreate(t, owner, InviteOptions{}).Code
	if err := owner.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	cfg := n.joiner(dataDir(t))
	cfg.Relays, cfg.joinFind = []string{entry}, 2*time.Second
	joiner, ev := start(t, cfg)
	err := joiner.Join(code)
	var ie *InviteError
	if !errors.Is(err, invite.ErrUnreachable) || !errors.As(err, &ie) || !strings.Contains(ie.Message, "through the connection relays") {
		t.Fatalf("join: %v", err)
	}
	ev.mu.Lock()
	defer ev.mu.Unlock()
	if len(ev.joins) < 2 || !strings.Contains(ev.joins[0].Message, "3 minutes") || ev.joins[len(ev.joins)-1].Message != ie.Message {
		t.Fatalf("join statuses: %+v", ev.joins)
	}
}

// A joiner with two wrong relays is told both: the two checks at start send their events at once, in any order, and
// the newest by Seq holds both failures.
func TestTwoWrongRelaysAreBothReported(t *testing.T) {
	_, addr1, _ := startConnRelay(t)
	_, addr2, _ := startConnRelay(t)
	_, wrong1 := relayEntry(t, addr1)
	_, wrong2 := relayEntry(t, addr2)
	n := newNetwork(t)
	cfg := Config{DataDir: dataDir(t), Bootstrap: n.bootstrap}
	cfg.Relays = []string{addr1 + "@" + hex.EncodeToString(wrong1), addr2 + "@" + hex.EncodeToString(wrong2)}
	_, cev := startApp(t, cfg)
	eventually(t, "both wrong keys", func() bool {
		return slices.Equal(cev.of("").relayStates(), []string{"failed/wrong-key", "failed/wrong-key"})
	})
}

// CV-4: the check at start proves the relay's key with a handshake and asks for nothing more, so a peer with the right
// key that is not even a relay passes, and only another key is blamed.
func TestRelayCheckProvesOnlyTheKey(t *testing.T) {
	id, err := koloid.Generate()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ln, err := kolosecure.Listen(ctx, "127.0.0.1:0", id, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept(ctx)
			if err != nil {
				return
			}
			go func() { <-c.Context().Done() }()
		}
	}()
	if err := checkRelay(ctx, koloswarm.Relay{Addr: ln.Addr().String(), PublicKey: id.PublicKey()}); err != nil {
		t.Fatalf("the right key was blamed: %v", err)
	}
	_, other := relayEntry(t, ln.Addr().String())
	if err := checkRelay(ctx, koloswarm.Relay{Addr: ln.Addr().String(), PublicKey: other}); !errors.Is(err, kolosecure.ErrUnexpectedPeer) {
		t.Fatalf("another key passed: %v", err)
	}
}
