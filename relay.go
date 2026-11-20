package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"log"
	"time"

	koloid "github.com/ciranda-dev/kolo-id"
	invite "github.com/ciranda-dev/kolo-invite"
	kolorelay "github.com/ciranda-dev/kolo-relay"
	kolosecure "github.com/ciranda-dev/kolo-secure"
	koloswarm "github.com/ciranda-dev/kolo-swarm"
)

// relayFindTimeout is how long Join looks for the inviter with connection relays: kolo-swarm tries the relays only
// after kolo-dht's Connect failed, which takes up to its 3 relays (kolo-dht kolodht.go:31) times PunchTimeout 45 s
// (kolo-dht config.go:43, 135), and one relay dial takes up to 60 s (decision 0013, "Dial").
const relayFindTimeout = 3*45*time.Second + 60*time.Second

// findTimeout is how long Join looks for the inviter.
func (s settings) findTimeout() time.Duration {
	if s.joinFind > 0 {
		return s.joinFind
	}
	if len(s.relays) > 0 {
		return relayFindTimeout
	}
	return joinFindTimeout
}

// relayCheckTimeout bounds the check of one relay's key at start.
const relayCheckTimeout = 30 * time.Second

// joinLookingMessage is what the window shows while Join looks for the inviter.
func (s settings) joinLookingMessage() string {
	if len(s.relays) > 0 {
		return "Looking for the person who invited you... Through a connection relay this can take up to about 3 minutes."
	}
	return "Looking for the person who invited you..."
}

// joinFailure is the sentence for a failed Join; one that found no inviter also names the relays when there are.
func (s settings) joinFailure(err error) string {
	if errors.Is(err, ErrNetwork) {
		return err.Error()
	}
	if len(s.relays) > 0 && errors.Is(err, invite.ErrUnreachable) {
		return "Could not reach the person who invited you, directly or through the connection relays. They must be online with the app open, and both of you must list the same relay with the key it prints (see Settings). Try again later."
	}
	return describeJoin(err)
}

// RelayStatus is the payload of the "chat:relays" event: the connection relays of the settings, and where they came
// from ("-relay", "-relay-file", both, "KOLO_CHAT_RELAYS", or "" without relays). Each conversation has its own, and
// the app has one for the window to show without a conversation.
type RelayStatus struct {
	Seq    uint64       `json:"seq"` // grows with every event; the window keeps the one with the highest
	From   string       `json:"from"`
	Relays []RelayState `json:"relays"`
}

// RelayState is one connection relay: its address, its public key in hex, and State: "waiting" until the app holds
// a place at it (it does in a conversation), "ready", or "failed" with Error saying why: "wrong-key" (it answered
// with another key), "bad-key", "not-allowed", "full" or "unreachable".
type RelayState struct {
	Addr  string `json:"addr"`
	Key   string `json:"key"`
	State string `json:"state"`
	Error string `json:"error"`
}

// resetRelays sets every relay of the settings to "waiting" and tells the window.
func (c *ChatService) resetRelays() {
	c.mu.Lock()
	c.relays = make([]RelayState, len(c.set.relays))
	for i, r := range c.set.relays {
		c.relays[i] = RelayState{Addr: r.Addr, Key: hex.EncodeToString(r.PublicKey), State: "waiting"}
	}
	c.relaySeq++ // events are sent after the lock is released, so they can arrive out of order
	st := RelayStatus{Seq: c.relaySeq, From: c.set.relaysFrom, Relays: append([]RelayState{}, c.relays...)}
	c.mu.Unlock()
	c.emit("chat:relays", st)
}

// checkRelays dials each relay once when the app starts, as a stranger with a throwaway key, and reports a relay that answers with another key to every conversation (see wrongRelayKey). kolo-swarm reports
// that only for a reservation, which a joiner does not hold until it is in a conversation. The checks end with the
// app.
func (a *Conversations) checkRelays() {
	for _, r := range a.set.relays {
		a.wg.Go(func() {
			ctx, cancel := context.WithTimeout(a.ctx, relayCheckTimeout)
			defer cancel()
			if err := a.checkRelay(ctx, r); errors.Is(err, kolosecure.ErrUnexpectedPeer) {
				a.wrongRelayKey(r.PublicKey)
			}
		})
	}
}

// wrongRelayKey keeps a relay that answered with another key, shows it in the app's own status (conversation "") and
// gives it to every running engine. An engine started later gets it right after its start (see add).
func (a *Conversations) wrongRelayKey(key ed25519.PublicKey) {
	a.mu.Lock()
	a.wrongKey[string(key)] = true
	addr := ""
	for i, r := range a.set.relays {
		if r.PublicKey.Equal(key) {
			addr = r.Addr
			a.relays[i].State, a.relays[i].Error = "failed", relayReason(kolosecure.ErrUnexpectedPeer)
		}
	}
	engines := a.running()
	a.mu.Unlock()
	if len(engines) == 0 { // a running engine logs it itself
		log.Printf("relay %s: %v", addr, kolosecure.ErrUnexpectedPeer)
	}
	a.emitRelays()
	for _, c := range engines {
		c.relayWrongKey(key)
	}
}

// emitRelays sends the app's own relay status, the one the window shows without a conversation.
func (a *Conversations) emitRelays() {
	a.mu.Lock()
	a.relaySeq++ // events are sent after the lock is released, so they can arrive out of order
	st := RelayStatus{Seq: a.relaySeq, From: a.set.relaysFrom, Relays: append([]RelayState{}, a.relays...)}
	a.mu.Unlock()
	a.emit("chat:relays", ConvEvent{Data: st})
}

// relayWrongKey records in this engine's status that the relay answered with another key.
func (c *ChatService) relayWrongKey(key ed25519.PublicKey) {
	c.relayChanged(koloswarm.Event{Kind: koloswarm.RelayChanged, PublicKey: key, Err: kolosecure.ErrUnexpectedPeer})
}

// checkRelay proves the relay's key with a handshake and asks it for nothing more: what a relay does past the handshake
// is its own doing, not the key in the setting.
func checkRelay(ctx context.Context, r koloswarm.Relay) error {
	me, err := koloid.Generate()
	if err != nil {
		return err
	}
	conn, err := kolosecure.Dial(ctx, r.Addr, me, r.PublicKey, nil)
	if err == nil {
		conn.Close()
	}
	return err
}

// relayChanged records a RelayChanged event of the swarm, logs a new failure and tells the window.
func (c *ChatService) relayChanged(ev koloswarm.Event) {
	state, why := "ready", ""
	switch {
	case ev.Err == nil:
	case errors.Is(ev.Err, koloswarm.ErrClosed), errors.Is(ev.Err, kolorelay.ErrReservationEnded):
		state = "waiting" // left the conversation, or the place's lifetime ended: kolo-swarm reserves again
	default:
		state, why = "failed", relayReason(ev.Err)
	}
	c.mu.Lock()
	for i, r := range c.set.relays {
		if !bytes.Equal(r.PublicKey, ev.PublicKey) {
			continue
		}
		if why != "" && c.relays[i].Error != why {
			log.Printf("relay %s: %v", r.Addr, ev.Err)
		}
		c.relays[i].State, c.relays[i].Error = state, why
	}
	c.relaySeq++ // events are sent after the lock is released, so they can arrive out of order
	st := RelayStatus{Seq: c.relaySeq, From: c.set.relaysFrom, Relays: append([]RelayState{}, c.relays...)}
	c.mu.Unlock()
	c.emit("chat:relays", st)
}

// relayReason names why the app holds no place at a relay, for the window to say in its language.
func relayReason(err error) string {
	switch {
	case errors.Is(err, kolosecure.ErrUnexpectedPeer):
		return "wrong-key"
	case errors.Is(err, koloswarm.ErrInvalidConfig):
		return "bad-key"
	case errors.Is(err, kolorelay.ErrNotAllowed):
		return "not-allowed"
	case errors.Is(err, kolorelay.ErrLimit):
		return "full"
	}
	return "unreachable"
}
