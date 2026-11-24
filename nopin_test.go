package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// pinless is the configuration of an app with no pin: only the entry node.
func (n network) pinless(dir string) Config {
	return Config{DataDir: dir, Bootstrap: n.bootstrap, LookupInterval: time.Second}
}

// pinEvents counts the "chat:pin" events told so far.
func (e *events) pinEvents() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.pins)
}

// A conversation without a pin: it is created, invites carry no pin, and members who are online exchange messages.
// A member gets the earlier messages of the people who are online, from them, and the ones of a person who is
// offline when that person comes back. All on loopback, with no pin running.
func TestConversationWithoutAPin(t *testing.T) {
	n := network{bootstrap: startBootstrap(t)}
	aroot := dataDir(t)
	alice, aev := startApp(t, n.pinless(aroot))
	if info, err := alice.Start(); err != nil || info.HasPins {
		t.Fatalf("an app without a pin: %+v, %v", info, err)
	}
	conv := mustCreateConv(t, alice, "Family")
	if !conv.Admin || !conv.Joined || conv.Error != "" {
		t.Fatalf("created: %+v", conv)
	}
	af := engineOf(t, alice, conv.ID)
	dir := filepath.Join(aroot, conversationDir, conv.ID)
	if saved, found, err := loadConversation(dir, 0); err != nil || !found || len(saved.Pins) != 0 {
		t.Fatalf("saved %+v, %v, %v", saved, found, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "pin-tokens")); err == nil {
		t.Fatal("no pin, no token")
	}
	eventually(t, "the pin status", func() bool { return aev.of(conv.ID).pinEvents() > 0 })
	if st := aev.of(conv.ID).pin(); st.Error != "" || st.Warning != "" || len(st.Pins) != 0 {
		t.Fatalf("no pin is not a pin that fails: %+v", st)
	}
	if err := alice.Send(conv.ID, defaultRoom, "one"); err != nil {
		t.Fatal(err)
	}

	// The invite carries no pin, and Bob, who has none either, gets alice's earlier message from her.
	inv, err := alice.CreateInvite(conv.ID, InviteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	bcfg := n.pinless(dataDir(t))
	bob, bev := startApp(t, bcfg)
	binfo := joinConv(t, af, bob, inv.Code, "Family")
	bdir := filepath.Join(bcfg.DataDir, conversationDir, binfo.ID)
	if saved, found, err := loadConversation(bdir, 0); err != nil || !found || len(saved.Pins) != 0 {
		t.Fatalf("Bob saved %+v, %v, %v", saved, found, err)
	}
	eventually(t, "Bob to get the earlier message", func() bool { return hasText(bev.of(binfo.ID).messages(), "one") })
	if m := bev.of(binfo.ID).messages()[0]; m.Via != "peer" {
		t.Fatalf("it came from Alice, not from a pin: %+v", m)
	}

	// Two members online talk directly.
	bf := engineOf(t, bob, binfo.ID)
	linked(t, af, bf)
	if err := alice.Send(conv.ID, defaultRoom, "two"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the message to Bob", func() bool { return hasText(bev.of(binfo.ID).messages(), "two") })
	if err := bob.Send(binfo.ID, defaultRoom, "three"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the message to Alice", func() bool { return hasText(aev.of(conv.ID).messages(), "three") })

	// Bob is offline while Alice writes: with no pin it waits until both are online, then comes from Alice.
	if err := bob.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	if err := alice.Send(conv.ID, defaultRoom, "while away"); err != nil {
		t.Fatal(err)
	}
	bob2, bev2 := startApp(t, bcfg)
	eventually(t, "Bob to get what he missed", func() bool { return hasText(bev2.of(binfo.ID).messages(), "while away") })
	for _, m := range bev2.of(binfo.ID).messages() {
		if m.Text == "while away" && m.Via != "peer" {
			t.Fatalf("it came from Alice: %+v", m)
		}
	}
	bf = engineOf(t, bob2, binfo.ID)
	linked(t, af, bf)

	// Carol joins while Bob is offline: she gets Alice's messages at once, and Bob's "three" only when Bob is back.
	if err := bob2.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	inv, err = alice.CreateInvite(conv.ID, InviteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	carol, cev := startApp(t, n.pinless(dataDir(t)))
	cinfo := joinConv(t, af, carol, inv.Code, "Family")
	eventually(t, "Carol to get Alice's messages", func() bool {
		m := cev.of(cinfo.ID).messages()
		return hasText(m, "one") && hasText(m, "two") && hasText(m, "while away")
	})
	time.Sleep(3 * time.Second)
	if hasText(cev.of(cinfo.ID).messages(), "three") {
		t.Fatal("Bob's message cannot reach Carol while Bob is offline: Alice does not hold it for him")
	}
	bob3, _ := startApp(t, bcfg)
	_ = bob3
	eventually(t, "Carol to get Bob's message once he is online", func() bool { return hasText(cev.of(cinfo.ID).messages(), "three") })
}
