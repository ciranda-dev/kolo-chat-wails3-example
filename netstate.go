package main

// How a room stays connected: the state the window shows, and the retries that never give up.
//
// A room reaches the network when its engine bootstraps. If no entry node answers, the engine still starts: its
// messages are kept in its own log, and it asks again with a growing, jittered wait until one answers. When a room
// loses its last peer it says "reconnecting" for a while and asks the entry nodes again, since the network may be what
// went away; kolo-swarm dials the peers again with its own backoff, and the peers pull what was sent meanwhile.

import (
	"context"
	"errors"
	"log"
	"math/rand/v2"
	"time"

	kolodht "github.com/ciranda-dev/kolo-dht"
)

// The pace of a start and of a retry. A start puts about ten requests on the entry nodes, which let one IPv4 address
// send 50 a second, so many rooms start a few at a time.
const (
	startSlots   = 4                       // engines that start at once
	startPause   = 1500 * time.Millisecond // each slot waits this long, plus up to startJitter, after its engine settles
	startJitter  = time.Second
	startWait    = 30 * time.Second // how long a Send waits for a room that has not started yet
	netRetryMin  = 2 * time.Second  // the wait before the first retry of an unreachable network
	netRetryMax  = 30 * time.Second // and its longest, after which it stays
	lostGrace    = 45 * time.Second // how long a room says "reconnecting" after its last peer dropped
	dialRetryMax = time.Minute      // the longest wait before kolo-swarm dials a peer again
	pinRetryMin  = 5 * time.Second
	pinRetryMax  = time.Minute
)

// pace holds the numbers above. The zero value means the constants; tests set shorter ones.
type pace struct {
	slots                    int
	pause, jitter            time.Duration
	retryMin, retryMax       time.Duration
	grace                    time.Duration
	pinRetryMin, pinRetryMax time.Duration
}

func (p pace) filled() pace {
	fill := func(v *time.Duration, d time.Duration) {
		if *v == 0 {
			*v = d
		}
	}
	if p.slots == 0 {
		p.slots = startSlots
	}
	fill(&p.pause, startPause)
	fill(&p.jitter, startJitter)
	fill(&p.retryMin, netRetryMin)
	fill(&p.retryMax, netRetryMax)
	fill(&p.grace, lostGrace)
	fill(&p.pinRetryMin, pinRetryMin)
	fill(&p.pinRetryMax, pinRetryMax)
	return p
}

// retryDelay is the wait before retry number attempt (from 1): min, doubled each time up to max, and then cut to a
// random value between its half and its whole so that rooms that failed together do not ask together.
func retryDelay(attempt int, lo, hi time.Duration) time.Duration {
	d := lo
	for i := 1; i < attempt && d < hi; i++ {
		d *= 2
	}
	d = min(d, hi)
	return d/2 + time.Duration(rand.Float64()*float64(d/2))
}

// sleep waits d and says whether ctx is still live.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// gate lets only a few engines work on the network at once, and makes each wait a jittered pause before it gives its
// place to the next, so the entry nodes see a trickle instead of a burst.
type gate struct {
	slots chan struct{}
	pace  pace
}

func newGate(p pace) *gate {
	p = p.filled()
	return &gate{slots: make(chan struct{}, p.slots), pace: p}
}

// do runs f in a place of the gate, then waits the pause before leaving it. It returns false, without running f, if
// ctx ended first. A nil gate runs f at once.
func (g *gate) do(ctx context.Context, f func()) bool {
	if g == nil {
		f()
		return true
	}
	select {
	case g.slots <- struct{}{}:
	case <-ctx.Done():
		return false
	}
	defer func() { <-g.slots }()
	f()
	sleep(ctx, g.pace.pause+time.Duration(rand.Float64()*float64(g.pace.jitter)))
	return true
}

// NetStatus is the payload of the "chat:network" event: "connecting" while a room reaches the network for the first
// time, "online", "reconnecting" while it looks for the peers it just lost, and "offline" while no entry node answers.
type NetStatus struct {
	State string `json:"state"`
}

// netView is what the room knows of its network: whether it has tried, whether the last try reached an entry node, and
// whether its last peer dropped a short while ago.
type netView struct {
	tried, reached, lost bool
}

func (n netView) state() string {
	switch {
	case !n.tried:
		return "connecting"
	case !n.reached:
		return "offline"
	case n.lost:
		return "reconnecting"
	}
	return "online"
}

// setNet changes the view under the lock and tells the window when the state changed.
func (c *ChatService) setNet(change func(*netView)) {
	c.mu.Lock()
	old := c.net.state()
	change(&c.net)
	now := c.net.state()
	c.mu.Unlock()
	if now != old {
		c.emit("chat:network", NetStatus{State: now})
	}
}

// Network returns the room's state: "connecting", "online", "reconnecting" or "offline".
func (c *ChatService) Network() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.net.state()
}

// reached says whether the last try reached an entry node.
func (c *ChatService) reached() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.net.reached
}

// reach asks the entry nodes once and records the answer. Only "no entry node answered" is an outcome of the network;
// any other error is returned as it is.
func (c *ChatService) reach() error {
	err := bootstrap(c.ctx, c.sw, c.netSet)
	var ne *NetworkError
	if err != nil && !errors.As(err, &ne) {
		return err
	}
	c.setNet(func(n *netView) { n.tried, n.reached = true, err == nil })
	if err == nil {
		c.onceOnline.Do(func() { close(c.online) })
	}
	return err
}

// netLoop keeps the room connected for as long as the engine runs: after a failed try it asks again with a growing wait,
// and when the room loses its last peer it asks once to learn whether the network is still there. ok is the result of
// the first try, made by start.
func (c *ChatService) netLoop(ok bool) {
	p := c.tune.filled()
	for attempt := 0; ; {
		if ok {
			attempt = 0
			select {
			case <-c.lostSig:
			case <-c.ctx.Done():
				return
			}
			c.setLost(true)
		} else {
			attempt++
			if !sleep(c.ctx, retryDelay(attempt, p.retryMin, p.retryMax)) {
				return
			}
		}
		var err error
		if !c.gate.do(c.ctx, func() { err = c.reach() }) {
			return
		}
		switch {
		case err == nil:
		case c.ctx.Err() != nil:
			return
		case !errors.Is(err, kolodht.ErrNoNodes):
			log.Print("network: ", err)
		}
		back := !ok && err == nil
		ok = err == nil
		if back { // what the pin got meanwhile, and what we wrote meanwhile
			c.wg.Go(c.pinAgain)
		}
	}
}

// pinAgain pulls from the pins and pushes to them once more, after the network came back.
func (c *ChatService) pinAgain() {
	if !c.isActive() {
		return
	}
	c.pushSoon()
	select {
	case <-c.pinDone:
	case <-c.ctx.Done():
		return
	}
	if err := c.pullFromPin(c.ctx); err != nil && c.ctx.Err() == nil {
		log.Print("pin pull: ", err)
	}
}

// setLost says that the room just lost its last peer, which keeps "reconnecting" on for the grace time, or that a
// peer is back.
func (c *ChatService) setLost(lost bool) {
	p := c.tune.filled()
	c.setNet(func(n *netView) { n.lost = lost })
	c.mu.Lock()
	if c.graceTimer != nil {
		c.graceTimer.Stop()
		c.graceTimer = nil
	}
	if lost && c.ctx.Err() == nil {
		c.graceTimer = time.AfterFunc(p.grace, func() { c.setNet(func(n *netView) { n.lost = false }) })
	}
	c.mu.Unlock()
}

// peerLeft is called when a registered peer's connection ends: with no peer left the room says "reconnecting" and
// asks the entry nodes.
func (c *ChatService) peerLeft() {
	if len(c.registered()) > 0 || c.ctx.Err() != nil {
		return
	}
	select {
	case c.lostSig <- struct{}{}:
	default:
	}
}

// peerBack is called when a peer is registered: the network works and the room is connected again.
func (c *ChatService) peerBack() {
	c.setNet(func(n *netView) { n.tried, n.reached = true, true })
	c.onceOnline.Do(func() { close(c.online) })
	c.setLost(false)
}

// waitOnline blocks until the room reached the network for the first time, and says false if the engine stopped first.
func (c *ChatService) waitOnline() bool {
	select {
	case <-c.online:
		return true
	case <-c.ctx.Done():
		return false
	}
}

// untilDone calls f until it returns nil, with a growing jittered wait between tries, and says whether it did: false
// when the engine stopped, or when f returned an error that is no failure of the network.
func (c *ChatService) untilDone(what string, f func() error) bool {
	p := c.tune.filled()
	for attempt := 1; ; attempt++ {
		err := f()
		switch {
		case err == nil:
			return true
		case c.ctx.Err() != nil:
			return false
		case !errors.Is(err, kolodht.ErrNoNodes):
			log.Print(what, ": ", err)
			return false
		}
		if !sleep(c.ctx, retryDelay(attempt, p.retryMin, p.retryMax)) {
			return false
		}
	}
}
