package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// The window reads pins, members, rooms, history and invites as lists. An event is not made a list on the way (only a
// method result is), so a nil slice in a payload reaches the window as null: every payload, empty or as it starts, has
// no null.
func TestPayloadsHaveNoNullLists(t *testing.T) {
	fresh := NewChatService(Config{}, (&events{}).emit)
	payloads := map[string]any{
		"chat:pin":            pinStatus(nil, nil),
		"chat:profiles":       fresh.Members(),
		"chat:rooms":          fresh.Rooms(),
		"chat:msg":            ChatMsg{},
		"chat:invite-request": InviteRequest{},
		"chat:invite":         InviteInfo{},
		"chat:join":           JoinStatus{},
		"notch:state":         NotchState{},
	}
	for name, p := range payloads {
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "null") {
			t.Errorf("%s %s", name, b)
		}
	}
}

// A joiner before its invite has no pin and no directory: what it tells the window, and what its methods return, has no
// null list. Neither has a chat that stopped.
func TestJoinerPayloadsHaveNoNullLists(t *testing.T) {
	n := newNetwork(t)
	c, ev := start(t, n.joiner(dataDir(t)))
	c.setPin(func([]pinState) {})
	c.publishDirectory()
	members, rooms := c.Members(), c.Rooms()
	invites, err := c.Invites()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	stopped := c.Members()
	ev.mu.Lock()
	all := append([]string(nil), ev.all...)
	ev.mu.Unlock()
	if len(all) == 0 {
		t.Fatal("no event was recorded")
	}
	for _, p := range []any{members, rooms, invites, stopped} {
		b, _ := json.Marshal(p)
		all = append(all, string(b))
	}
	for _, e := range all {
		if strings.Contains(e, "null") {
			t.Errorf("null in %.200s", e)
		}
	}
}
