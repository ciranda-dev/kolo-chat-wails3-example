package main

import (
	"context"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"testing"

	invite "github.com/ciranda-dev/kolo-invite"
	"github.com/ciranda-dev/kolo-pin"
	kolostore "github.com/ciranda-dev/kolo-store"
)

// holdsBoth says whether the pin p holds c's two logs, each as long as c's own.
func holdsBoth(t *testing.T, p pin.Peer, c *ChatService) bool {
	t.Helper()
	for _, l := range []*kolostore.Log{c.log, c.dirLog} {
		info, err := pin.Status(context.Background(), c.id, p, l.ID())
		if err != nil || info.Entries != l.Len() {
			return false
		}
	}
	return true
}

// pinsConfig is a chat of the network n that uses the pins ps, in that order.
func pinsConfig(n network, dir string, ps ...pin.Peer) Config {
	cfg := n.config(dir)
	cfg.Pins = nil
	for _, p := range ps {
		cfg.Pins = append(cfg.Pins, pinEntry(p))
	}
	return cfg
}

// Every pin of the list gets the logs, and the window lists each one.
func TestEveryPinGetsTheLogs(t *testing.T) {
	n := newNetwork(t)
	second, _ := startPin(t)
	a, aev := start(t, pinsConfig(n, dataDir(t), n.pin, second))
	if err := a.Send(defaultRoom, "kept twice"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "both pins to hold the entry", func() bool {
		return heldBy(t, n.pin, a).Entries == 1 && heldBy(t, second, a).Entries == 1
	})
	if err := a.pushToPin(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := aev.pin()
	if st.Error != "" || st.Warning != "" || len(st.Pins) != 2 || st.Pins[0].Addr != n.pin.Addr || st.Pins[1].Addr != second.Addr {
		t.Fatalf("the window was told %+v", st)
	}
}

// A pin that is down does not stop the others: the window gets a warning, and an error only when none works.
func TestOnePinDownStillPins(t *testing.T) {
	n := newNetwork(t)
	down, _, stop := stoppablePin(t)
	stop()
	a, aev := start(t, pinsConfig(n, dataDir(t), n.pin, down))
	a.Send(defaultRoom, "still kept")
	err := a.pushToPin(context.Background())
	if !errors.Is(err, pin.ErrUnreachable) || !strings.Contains(err.Error(), down.Addr) {
		t.Fatalf("got %v", err)
	}
	if got := heldBy(t, n.pin, a).Entries; got != 1 {
		t.Fatalf("the working pin holds %d entries", got)
	}
	st := aev.pin()
	if st.Error != "" || !strings.HasPrefix(st.Warning, "1 of 2 pins") || st.Pins[0].Push != "" ||
		!strings.Contains(st.Pins[1].Push, down.Addr+" is unreachable") {
		t.Fatalf("the window was told %+v", st)
	}

	b, bev := start(t, pinsConfig(n, dataDir(t), down))
	b.Send(defaultRoom, "kept nowhere")
	if err := b.pushToPin(context.Background()); !errors.Is(err, pin.ErrUnreachable) {
		t.Fatalf("got %v", err)
	}
	if st := bev.pin(); !strings.Contains(st.Error, "unreachable") || st.Warning != "" {
		t.Fatalf("the window was told %+v", st)
	}
}

// B missed messages and its first pin is gone: they come from the second one.
func TestMissedMessagesComeFromAnyPin(t *testing.T) {
	n := newNetwork(t)
	first, _, stopFirst := stoppablePin(t)
	cfg := func(dir string) Config { return pinsConfig(n, dir, first, n.pin) }
	a, _ := start(t, cfg(dataDir(t)))
	bdir := dataDir(t)
	b, _ := n.member(t, a, bdir)
	introduced(t, a, b)
	b.ServiceShutdown()

	a.Send(defaultRoom, "two")
	a.Send(defaultRoom, "three")
	eventually(t, "both pins to hold the entries", func() bool {
		return heldBy(t, first, a).Entries == 2 && heldBy(t, n.pin, a).Entries == 2
	})
	stopFirst()

	_, b2ev := start(t, cfg(bdir))
	eventually(t, "the missed messages", func() bool { return len(b2ev.messages()) >= 2 })
	got := b2ev.messages()
	if !slices.Equal(texts(got), []string{"two", "three"}) || got[0].Via != "pin" || got[1].Via != "pin" {
		t.Fatalf("got %+v", got)
	}
	eventually(t, "the pull from the first pin to fail", func() bool { return b2ev.pin().Warning != "" })
	if st := b2ev.pin(); st.Error != "" || !strings.Contains(st.Pins[0].Pull, "unreachable") || st.Pins[1].Pull != "" {
		t.Fatalf("the window was told %+v", st)
	}
}

// One pin is behind the other: B pulls from both, shows every message once, and no pull or push fails.
func TestPinThatIsBehind(t *testing.T) {
	n := newNetwork(t)
	behind, _ := startPin(t)
	adir, bdir := dataDir(t), dataDir(t)
	a, _ := start(t, pinsConfig(n, adir, n.pin, behind))
	b, _ := n.member(t, a, bdir)
	introduced(t, a, b)
	// Both pins hold B's logs before B stops: a push cut by the shutdown meets a kolo-pin defect.
	eventually(t, "both pins to hold B's logs", func() bool { return holdsBoth(t, n.pin, b) && holdsBoth(t, behind, b) })
	b.ServiceShutdown()

	a.Send(defaultRoom, "one")
	eventually(t, "both pins to hold one entry", func() bool {
		return heldBy(t, n.pin, a).Entries == 1 && heldBy(t, behind, a).Entries == 1
	})
	a.ServiceShutdown()
	a2, _ := start(t, pinsConfig(n, adir, n.pin)) // the second pin gets nothing more
	a2.Send(defaultRoom, "two")
	a2.Send(defaultRoom, "three")
	eventually(t, "the first pin to hold three entries", func() bool { return heldBy(t, n.pin, a2).Entries == 3 })
	if got := heldBy(t, behind, a2).Entries; got != 1 {
		t.Fatalf("the second pin holds %d entries", got)
	}

	b2, b2ev := start(t, pinsConfig(n, bdir, behind, n.pin))
	introduced(t, a2, b2)
	eventually(t, "every message", func() bool { return len(b2ev.messages()) >= 3 })
	if got := b2ev.messages(); !slices.Equal(texts(got), []string{"one", "two", "three"}) || got[2].Via != "pin" {
		t.Fatalf("got %+v", got)
	}
	if err := b2.pushToPin(context.Background()); err != nil {
		t.Fatalf("B pushes to both pins: %v", err)
	}
	if st := b2ev.pin(); st.Error != "" || st.Warning != "" || st.Pins[0] != (PinState{Addr: behind.Addr, Key: hex.EncodeToString(behind.ID)}) ||
		st.Pins[1] != (PinState{Addr: n.pin.Addr, Key: hex.EncodeToString(n.pin.ID)}) {
		t.Fatalf("the window was told %+v", st)
	}
}

// The invite carries every pin, in order, and the joiner pushes its own log to each one. A joiner that accepts
// fewer pins than the invite lists is refused with a sentence that says so.
func TestInviteCarriesEveryPin(t *testing.T) {
	n := newNetwork(t)
	second, _ := startPin(t)
	a, _ := start(t, pinsConfig(n, dataDir(t), n.pin, second))

	small := n.joiner(dataDir(t))
	small.MaxPins = 1
	c, _ := start(t, small)
	err := waitJoin(t, approveJoin(t, a, c.pub, joinAsync(c, mustCreate(t, a, InviteOptions{}).Code), false))
	refused(t, err, invite.ErrProtocol, "-max-pins")
	if !errors.Is(err, ErrTooMany) || c.Joined() {
		t.Fatalf("got %v", err)
	}

	bdir := dataDir(t)
	b, _ := start(t, n.joiner(bdir))
	joinVia(t, a, b, false)
	saved, found, err := loadConversation(bdir, 0)
	if err != nil || !found || len(saved.Pins) != 2 || saved.Pins[0].Addr != n.pin.Addr || saved.Pins[1].Addr != second.Addr ||
		!saved.Pins[1].Key.Equal(second.ID) {
		t.Fatalf("saved %+v, %v", saved, err)
	}
	if err := b.Send(defaultRoom, "from the joiner"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "both pins to hold the joiner's entry", func() bool {
		return heldBy(t, n.pin, b).Entries == 1 && heldBy(t, second, b).Entries == 1
	})
}
