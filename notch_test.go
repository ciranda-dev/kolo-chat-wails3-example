package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

// fakePanel stands for the native notch window. busy counts the calls that run at once.
type fakePanel struct {
	mu           sync.Mutex
	visible      bool
	shows, hides int
	delay        time.Duration
	busy, clash  atomic.Int32
}

func (p *fakePanel) call(visible bool) {
	if p.busy.Add(1) > 1 {
		p.clash.Add(1)
	}
	time.Sleep(p.delay)
	p.mu.Lock()
	if p.visible == visible {
		p.clash.Add(1) // a Show of a notch that is down, or a Hide of one that is up
	}
	if p.visible = visible; visible {
		p.shows++
	} else {
		p.hides++
	}
	p.mu.Unlock()
	p.busy.Add(-1)
}

func (p *fakePanel) Show() { p.call(true) }
func (p *fakePanel) Hide() { p.call(false) }

func (p *fakePanel) down() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.visible
}

// sent is an event as a window gets it.
type sent struct {
	name string
	data any
}

// testNotch is a service with a fake panel, a timer the test fires by hand, and a record of what goes to the
// main window (main) and to every window, the notch among them (all).
type testNotch struct {
	*NotchService
	panel     *fakePanel
	fire      func() // the auto-hide that waits, nil when none does
	delay     time.Duration
	mu        sync.Mutex
	main, all []sent
	opened    []int
}

func newTestNotch(t *testing.T) *testNotch {
	t.Helper()
	tn := &testNotch{NotchService: NewNotchService(Config{DataDir: t.TempDir()}), panel: &fakePanel{}}
	tn.NotchService.panel = tn.panel
	tn.after = func(d time.Duration, f func()) func() {
		tn.delay, tn.fire = d, func() { tn.fire = nil; f() }
		return func() { tn.fire = nil }
	}
	tn.toMain = func(name string, data any) {
		tn.mu.Lock()
		tn.main = append(tn.main, sent{name, data})
		tn.mu.Unlock()
	}
	tn.emit = func(name string, data any) {
		tn.mu.Lock()
		tn.all = append(tn.all, sent{name, data})
		tn.mu.Unlock()
	}
	tn.window = func(action int) { tn.opened = append(tn.opened, action) }
	return tn
}

// lastState is the newest "notch:state" the windows got.
func (tn *testNotch) lastState(t *testing.T) NotchState {
	t.Helper()
	tn.mu.Lock()
	defer tn.mu.Unlock()
	if len(tn.all) == 0 {
		t.Fatal("no notch:state was sent")
	}
	st, ok := tn.all[len(tn.all)-1].data.(NotchState)
	if !ok {
		t.Fatalf("the windows got %+v", tn.all[len(tn.all)-1])
	}
	return st
}

const (
	alice = "aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11"
	bob   = "bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22"
)

func TestNotchStateFromChatEvents(t *testing.T) {
	n := newTestNotch(t)
	if st := n.State(); st.HasMessage || st.Pin != pinUnknown || !st.Supported || !st.Enabled || !st.ShowText {
		t.Fatalf("first state = %+v", st)
	}

	n.observe("chat:profiles", []Member{{Key: alice, Name: "Alice", Avatar: "🦊"}, {Key: bob, Name: "bob"}})
	n.observe("chat:peers", 2)
	n.observe("chat:pin", PinStatus{})
	n.observe("chat:msg", ChatMsg{From: alice, Room: "dev", Text: "hello\n  there", Via: "direct"})

	st := n.State()
	want := NotchState{Supported: true, Enabled: true, ShowText: true, HasMessage: true, Name: "Alice", Avatar: "🦊",
		Room: "dev", Preview: "hello there", Unread: 1, Peers: 2, Pin: pinOK}
	if st != want {
		t.Fatalf("state = %+v, want %+v", st, want)
	}
	if got := n.lastState(t); got != want {
		t.Fatalf("event = %+v, want %+v", got, want)
	}

	n.observe("chat:msg", ChatMsg{From: bob, Room: "general", Text: "second"})
	if st := n.State(); st.Name != "bob" || st.Avatar != "B" || st.Unread != 2 || st.Preview != "second" {
		t.Fatalf("after the second message: %+v", st)
	}

	n.observe("chat:pin", PinStatus{Locked: 3})
	if st := n.State(); st.Pin != pinWaiting || st.Locked != 3 {
		t.Fatalf("waiting pin: %+v", st)
	}
	n.observe("chat:pin", PinStatus{Error: "The pin is unreachable", Locked: 3})
	if st := n.State(); st.Pin != pinError {
		t.Fatalf("pin error: %+v", st)
	}

	before := n.State()
	n.observe("chat:rooms", []string{"general"})
	n.observe("chat:other", 9)
	if n.State() != before {
		t.Fatal("an event the notch does not show changed its state")
	}
}

// The events of the chat go to the main window only. Every window, the notch among them, gets "notch:state" and
// nothing else, and with "Show message text" off that holds no part of a message, and never an invite code.
func TestChatEventsReachOnlyTheMainWindow(t *testing.T) {
	const body = "my PIN is 1234 and the rest of this message is long enough to pass the seventy-two characters of a preview"
	const code = "KOLO-INVITE-CODE-SECRET"
	n := newTestNotch(t)
	if err := n.SetShowText(false); err != nil {
		t.Fatal(err)
	}
	events := []sent{
		{"chat:profiles", []Member{{Key: alice, Name: "Alice"}}},
		{"chat:msg", ChatMsg{From: alice, Room: "general", Text: body, Via: "direct"}},
		{"chat:invite", InviteInfo{ID: "invite-id-1", Code: code, State: "active"}},
		{"chat:invite-request", InviteRequest{Request: 7, Invite: "invite-id-1", Joiner: bob}},
		{"chat:join", JoinStatus{Status: "joined", Message: "You joined the conversation."}},
		{"chat:rooms", []string{"general", "private-room"}},
		{"chat:peers", 1},
		{"chat:pin", PinStatus{Error: "The pin at 127.0.0.1:7450 is unreachable"}},
	}
	for _, ev := range events {
		n.forward(ev.name, ev.data)
	}

	if len(n.main) != len(events) {
		t.Fatalf("the main window got %d events, want %d", len(n.main), len(events))
	}
	for i, ev := range events {
		if n.main[i].name != ev.name {
			t.Errorf("main window event %d is %s, want %s", i, n.main[i].name, ev.name)
		}
	}
	leaks := func(secrets ...string) {
		t.Helper()
		for _, ev := range n.all {
			if _, ok := ev.data.(NotchState); ev.name != "notch:state" || !ok {
				t.Fatalf("every window got %s %T, want only notch:state", ev.name, ev.data)
			}
			raw, err := json.Marshal(ev.data)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range secrets {
				if strings.Contains(string(raw), secret) {
					t.Fatalf("the notch window got %q in %s", secret, raw)
				}
			}
		}
	}
	leaks(body, "my PIN", "1234", code, "invite-id-1", bob, alice, "private-room", "7450")
	if st := n.lastState(t); st.Preview != "" || st.ShowText || st.Name != "Alice" || st.Unread != 1 {
		t.Fatalf("with the text off the notch got %+v", st)
	}

	// With the text on it gets the one-line preview, still not the body or anything of an invite.
	n.all = nil
	if err := n.SetShowText(true); err != nil {
		t.Fatal(err)
	}
	n.forward("chat:msg", ChatMsg{From: alice, Text: body})
	n.forward("chat:invite", InviteInfo{ID: "invite-id-1", Code: code})
	leaks(body, code, "invite-id-1")
	if st := n.lastState(t); st.Preview != previewOf(body) || utf8.RuneCountInString(st.Preview) > notchPreviewRunes {
		t.Fatalf("preview = %q", st.Preview)
	}
}

func TestNotchShowsOnMessageAndHides(t *testing.T) {
	n := newTestNotch(t)
	n.observe("chat:msg", ChatMsg{From: alice, Text: "one"})
	if !n.panel.visible || n.fire == nil || n.delay != notchAutoHide {
		t.Fatalf("a message with the window unfocused: visible=%v timer=%v delay=%v", n.panel.visible, n.fire != nil, n.delay)
	}
	// A notch that is down is not shown again, and one that is up is not hidden again.
	n.observe("chat:msg", ChatMsg{From: alice, Text: "one more"})
	if n.panel.shows != 1 || n.fire == nil {
		t.Fatalf("a second message: shows=%d timer=%v", n.panel.shows, n.fire != nil)
	}
	n.fire()
	if n.panel.visible {
		t.Fatal("the notch stayed after the auto-hide")
	}
	n.Hide()
	n.observe("chat:peers", 1)
	if n.panel.hides != 1 {
		t.Fatalf("a hidden notch was hidden again: hides=%d", n.panel.hides)
	}

	// Once the auto-hide ran nothing is left to hold, and the next message starts a new one.
	n.Hold()
	n.observe("chat:msg", ChatMsg{From: alice, Text: "after the hide"})
	if n.fire == nil || !n.panel.visible {
		t.Fatalf("a Hold after the auto-hide stopped the next one: timer=%v visible=%v", n.fire != nil, n.panel.visible)
	}

	// Focus reads the messages and sends the notch back up.
	n.setFocused(true)
	if st := n.State(); st.Unread != 0 || n.panel.visible || n.fire != nil {
		t.Fatalf("after focus: unread=%d visible=%v timer=%v", st.Unread, n.panel.visible, n.fire != nil)
	}

	// With the window focused a message is not unread and does not show the notch.
	shows := n.panel.shows
	n.observe("chat:msg", ChatMsg{From: alice, Text: "three"})
	if st := n.State(); st.Unread != 0 || st.Preview != "three" || n.panel.shows != shows {
		t.Fatalf("focused: %+v shows=%d", st, n.panel.shows)
	}

	// The pointer on the notch holds it, and the auto-hide starts again when the pointer leaves.
	n.setFocused(false)
	n.observe("chat:msg", ChatMsg{From: alice, Text: "held"})
	n.Hold()
	if n.fire != nil || !n.panel.visible {
		t.Fatalf("Hold: timer=%v visible=%v", n.fire != nil, n.panel.visible)
	}
	n.observe("chat:msg", ChatMsg{From: alice, Text: "held too"})
	if n.fire != nil {
		t.Fatal("a message started the auto-hide under the pointer")
	}
	n.Release()
	if n.fire == nil {
		t.Fatal("Release did not start the auto-hide again")
	}
	n.fire()
	n.Release()
	if n.panel.visible || n.fire != nil {
		t.Fatalf("after Release: visible=%v timer=%v", n.panel.visible, n.fire != nil)
	}

	// Show by hand has no auto-hide, cancels the one that waits, and the pointer does not start one.
	n.observe("chat:msg", ChatMsg{From: alice, Text: "four"})
	if n.fire == nil {
		t.Fatal("a message after a Release started no auto-hide")
	}
	n.Show()
	if !n.panel.visible || n.fire != nil {
		t.Fatalf("Show: visible=%v timer=%v", n.panel.visible, n.fire != nil)
	}
	n.Hold()
	n.Release()
	if n.fire != nil {
		t.Fatal("the pointer started an auto-hide on a notch shown by hand")
	}
	n.observe("chat:msg", ChatMsg{From: alice, Text: "the pointer has left"})
	if n.fire == nil {
		t.Fatal("a message after the pointer left started no auto-hide")
	}

	// The pointer keeps down a notch shown by hand too: a message under it starts no auto-hide until it leaves.
	n.Hold()
	n.observe("chat:msg", ChatMsg{From: alice, Text: "under the pointer"})
	if n.fire != nil || !n.panel.visible {
		t.Fatalf("a message under the pointer on a notch shown by hand: timer=%v visible=%v", n.fire != nil, n.panel.visible)
	}
	n.Release()
	if n.fire == nil {
		t.Fatal("the pointer left after a message and no auto-hide started")
	}
	n.fire()
	if n.panel.visible {
		t.Fatal("the notch stayed after the pointer left and the auto-hide ran")
	}
	// A notch hidden under the pointer forgets the pointer: the next message hides itself again.
	n.Show()
	n.Hold()
	n.Hide()
	n.observe("chat:msg", ChatMsg{From: alice, Text: "after the chevron"})
	if n.fire == nil || !n.panel.visible {
		t.Fatalf("a message after a hide under the pointer: timer=%v visible=%v", n.fire != nil, n.panel.visible)
	}

	// The shortcut hides and shows, and ends the auto-hide that waits.
	n.toggle()
	if n.panel.visible {
		t.Fatal("toggle did not hide the notch")
	}
	n.toggle()
	if !n.panel.visible {
		t.Fatal("toggle did not show the notch")
	}
	n.Hide()
	n.observe("chat:msg", ChatMsg{From: alice, Text: "five"})
	n.toggle()
	if n.panel.visible || n.fire != nil {
		t.Fatalf("toggle on a notch a message showed: visible=%v timer=%v", n.panel.visible, n.fire != nil)
	}

	n.observe("chat:msg", ChatMsg{From: alice, Text: "six"})
	n.OpenChat()
	if len(n.opened) != 1 || n.opened[0] != windowOpen || n.panel.visible || n.fire != nil {
		t.Fatalf("OpenChat: opened=%v visible=%v timer=%v", n.opened, n.panel.visible, n.fire != nil)
	}
	n.toggleWindow()
	if len(n.opened) != 2 || n.opened[1] != windowToggle {
		t.Fatalf("toggleWindow: opened=%v", n.opened)
	}
	n.SetLanguage("pt")
	if len(n.opened) != 2 {
		t.Fatal("a later change asked the main window again")
	}
}

// The pointer on the camera housing brings the notch down, as a message does; a click there opens the chat. The
// reads of the pointer run only while hover can do something.
func TestHoverOnTheHousing(t *testing.T) {
	n := newTestNotch(t)
	polls := 0
	n.every = func(d time.Duration, f func()) func() {
		if d != notchPoll {
			t.Errorf("the pointer is read every %v, want %v", d, notchPoll)
		}
		polls++
		return func() { polls-- }
	}
	var at pointerSample
	n.pointer = func() pointerSample { s := at; at.Clicks = 0; return s }
	move := func(on bool, clicks int) {
		at = pointerSample{On: on, Clicks: clicks}
		n.poll()
	}
	if n.setHousing(false); polls != 0 {
		t.Fatal("the pointer is read on a screen without a camera housing")
	}
	if n.setHousing(true); polls != 1 {
		t.Fatalf("%d reads of the pointer with a housing, want 1", polls)
	}

	// With no message yet the notch comes down with the idle view, and stays while the pointer is on it.
	move(true, 0)
	if !n.panel.visible || n.fire != nil || n.lastState(t).HasMessage {
		t.Fatalf("pointer on the housing: visible=%v timer=%v state=%+v", n.panel.visible, n.fire != nil, n.lastState(t))
	}
	n.Hold()
	move(false, 0)
	if !n.panel.visible || n.fire != nil {
		t.Fatalf("pointer from the housing onto the notch: visible=%v timer=%v", n.panel.visible, n.fire != nil)
	}
	n.Release()
	if n.fire == nil || n.delay != notchAutoHide {
		t.Fatalf("pointer gone: timer=%v delay=%v", n.fire != nil, n.delay)
	}
	n.fire()
	if n.panel.visible {
		t.Fatal("the notch stayed after the pointer left")
	}

	// Leaving the housing starts the hide, and coming back before it ends it.
	move(true, 0)
	move(false, 0)
	if n.fire == nil {
		t.Fatal("leaving the housing started no hide")
	}
	move(true, 0)
	if n.fire != nil || !n.panel.visible {
		t.Fatalf("back on the housing: timer=%v visible=%v", n.fire != nil, n.panel.visible)
	}
	// A message while the pointer is on the housing starts no hide either.
	n.observe("chat:msg", ChatMsg{From: alice, Text: "under the pointer"})
	if n.fire != nil || !n.panel.visible {
		t.Fatalf("a message under the pointer: timer=%v visible=%v", n.fire != nil, n.panel.visible)
	}

	// A click on the housing while the notch is down opens the chat and sends the notch up, until the pointer
	// comes back.
	move(true, 1)
	if len(n.opened) != 1 || n.opened[0] != windowOpen || n.panel.visible {
		t.Fatalf("click: opened=%v visible=%v", n.opened, n.panel.visible)
	}
	if move(true, 0); n.panel.visible {
		t.Fatal("the notch came back with the pointer still on the housing")
	}
	move(false, 0)
	n.Hide()
	if move(true, 1); len(n.opened) != 1 || !n.panel.visible {
		t.Fatalf("a click counted before the pointer came: opened=%v visible=%v", n.opened, n.panel.visible)
	}
	move(false, 0)
	n.Hide()

	// "Show messages in the notch" off stops the reads and the hover with them.
	if err := n.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	if move(true, 0); polls != 0 || n.panel.visible {
		t.Fatalf("setting off: reads=%d visible=%v", polls, n.panel.visible)
	}
	if err := n.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if polls != 1 {
		t.Fatalf("setting on again: reads=%d", polls)
	}
	if n.setAppHidden(true); polls != 0 {
		t.Fatal("the pointer is read while the app is hidden")
	}
	if n.setAppHidden(false); polls != 1 {
		t.Fatal("the reads did not come back with the app")
	}
	if err := n.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	if polls != 0 {
		t.Fatal("the pointer is read after shutdown")
	}
}

// A timer that already fired cannot be stopped. Its late callback must not hide the notch a new message just
// showed, nor take the new timer away from Hold.
func TestStaleAutoHide(t *testing.T) {
	n := newTestNotch(t)
	var fires []func()
	n.after = func(d time.Duration, f func()) func() {
		fires = append(fires, f)
		return func() {} // Stop after the timer fired: no effect
	}
	n.observe("chat:msg", ChatMsg{From: alice, Text: "one"})
	n.observe("chat:msg", ChatMsg{From: alice, Text: "two"}) // timer one has fired, its callback waits for mu
	fires[0]()
	if !n.panel.visible {
		t.Fatal("a stale timer hid the notch that the second message just showed")
	}
	n.Hold()
	if !n.hovered {
		t.Fatal("Hold after the stale callback holds nothing")
	}
	n.Release()
	fires[1]() // replaced by the Release
	if !n.panel.visible {
		t.Fatal("the timer that Hold cancelled hid the notch")
	}
	fires[2]()
	if n.panel.visible {
		t.Fatal("the timer that counts did not hide the notch")
	}
}

// A focus that lands while a message is on its way to the notch wins: the notch does not stay down over the
// focused chat.
func TestFocusBetweenMessageAndShow(t *testing.T) {
	n := newTestNotch(t)
	focused := false
	n.wake = func() {
		if !focused { // the focus event lands after observe let go of the state and before the notch moves
			focused = true
			n.setFocused(true)
		}
		n.apply()
	}
	n.observe("chat:msg", ChatMsg{From: alice, Text: "one"})
	if n.panel.visible || n.fire != nil {
		t.Fatalf("notch visible=%v with the chat focused, timer=%v", n.panel.visible, n.fire != nil)
	}
	if st := n.lastState(t); st.Unread != 0 {
		t.Fatalf("the last state the windows got has %d unread after the focus", st.Unread)
	}
}

// A pin that replays many messages while the user comes and goes: the native calls stay one at a time and in
// order, nothing piles up, and the windows end with the newest state. Run with -race.
func TestManyEventsAtOnce(t *testing.T) {
	n := newTestNotch(t)
	n.panel.delay = 200 * time.Microsecond
	var refreshing, clash atomic.Int32
	var seen NotchState
	n.refresh = append(n.refresh, func(st NotchState, _ string) {
		if refreshing.Add(1) > 1 {
			clash.Add(1)
		}
		seen = st // one refresh at a time, or the race detector says so
		time.Sleep(100 * time.Microsecond)
		refreshing.Add(-1)
	})
	n.after = func(time.Duration, func()) func() { return func() {} }
	n.startWorker()

	var wg sync.WaitGroup
	for g := range 4 {
		wg.Go(func() {
			for i := range 200 {
				n.forward("chat:msg", ChatMsg{From: alice, Text: "replayed"})
				n.forward("chat:peers", i)
				if i%50 == g {
					n.setFocused(true)
					n.setFocused(false)
				}
			}
		})
	}
	wg.Wait()
	n.setFocused(true)

	deadline := time.Now().Add(10 * time.Second)
	for n.panel.down() || n.lastState(t) != n.State() {
		if time.Now().After(deadline) {
			t.Fatalf("not settled: visible=%v last=%+v state=%+v", n.panel.down(), n.lastState(t), n.State())
		}
		time.Sleep(time.Millisecond)
	}
	if err := n.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	n.applyMu.Lock() // the worker is done with its last apply
	defer n.applyMu.Unlock()
	if c := n.panel.clash.Load() + clash.Load(); c != 0 {
		t.Fatalf("%d native calls ran at once or out of order", c)
	}
	if seen != n.State() || seen.Unread != 0 {
		t.Fatalf("the tray ended with %+v, the state is %+v", seen, n.State())
	}
	if len(n.main) != 4*200*2 {
		t.Fatalf("the main window got %d events, want %d", len(n.main), 4*200*2)
	}
}

func TestNotchSettings(t *testing.T) {
	dir := t.TempDir()
	if set := NewNotchService(Config{DataDir: dir}).Settings(); !set.Enabled || !set.Preview {
		t.Fatalf("defaults = %+v, want both on", set)
	}

	n := newTestNotch(t)
	n.dir = dir
	n.observe("chat:msg", ChatMsg{From: alice, Text: "a secret"})
	if err := n.SetShowText(false); err != nil {
		t.Fatal(err)
	}
	if st := n.State(); st.Preview != "" || st.ShowText || !st.HasMessage || st.Name == "" {
		t.Fatalf("preview off: %+v", st)
	}
	if got := n.lastState(t); got.Preview != "" {
		t.Fatalf("the event carries the text with the preview off: %+v", got)
	}
	if err := n.SetShowText(true); err != nil {
		t.Fatal(err)
	}

	if err := n.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	if n.panel.visible || n.fire != nil || n.lastState(t).Enabled {
		t.Fatal("turning the notch off left it showing")
	}
	shows := n.panel.shows
	n.observe("chat:msg", ChatMsg{From: alice, Text: "quiet"})
	if n.panel.shows != shows || n.State().Unread != 2 || n.fire != nil {
		t.Fatalf("notch off: shows=%d unread=%d timer=%v", n.panel.shows, n.State().Unread, n.fire != nil)
	}

	// A window that still shows the notch as on changes the other setting: the notch stays off.
	if err := n.SetShowText(false); err != nil {
		t.Fatal(err)
	}
	if set := n.Settings(); set != (NotchSettings{Enabled: false, Preview: false}) {
		t.Fatalf("after the two changes: %+v", set)
	}

	// The settings survive a restart, in a file only the user reads.
	if set := NewNotchService(Config{DataDir: dir}).Settings(); set != (NotchSettings{Enabled: false, Preview: false}) {
		t.Fatalf("after a restart: %+v", set)
	}
	info, err := os.Stat(filepath.Join(dir, notchSettingsFile))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("notch.json: %v, mode %v", err, info.Mode().Perm())
	}

	if err := os.WriteFile(filepath.Join(dir, notchSettingsFile), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if set := NewNotchService(Config{DataDir: dir}).Settings(); !set.Enabled || !set.Preview {
		t.Fatalf("a broken file gave %+v, want the defaults", set)
	}

	n.dir = ""
	if err := n.SetEnabled(true); err == nil {
		t.Fatal("a setting with no data directory was reported as saved")
	}
}

func TestNotchWithoutPanel(t *testing.T) {
	n := newTestNotch(t)
	n.NotchService.panel = nil // Windows and Linux
	n.pointer = func() pointerSample { return pointerSample{On: true} }
	n.every = func(time.Duration, func()) func() { t.Fatal("the pointer is read without a notch window"); return nil }
	n.setHousing(true)
	n.observe("chat:msg", ChatMsg{From: alice, Text: "hi"})
	n.Show()
	n.Hide()
	n.toggle()
	n.Hold()
	n.Release()
	n.OpenChat()
	if st := n.State(); st.Supported || st.Unread != 1 || n.fire != nil || n.want {
		t.Fatalf("no panel: %+v timer=%v want=%v", st, n.fire != nil, n.want)
	}
	if len(n.opened) != 1 {
		t.Fatalf("OpenChat without a notch: opened=%v", n.opened)
	}
}

// The chat stops after this service, so its last events arrive after ServiceShutdown: they start nothing.
func TestNotchAfterShutdown(t *testing.T) {
	n := newTestNotch(t)
	n.startWorker()
	n.observe("chat:msg", ChatMsg{From: alice, Text: "before"})
	n.Hold()
	if err := n.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	n.applyMu.Lock()
	n.wake = n.apply
	shows, hides, sentAll := n.panel.shows, n.panel.hides, len(n.all)
	n.applyMu.Unlock()
	n.fire = nil

	n.observe("chat:msg", ChatMsg{From: alice, Text: "late"})
	n.Release()
	n.Show()
	n.toggle()
	if n.fire != nil || n.panel.shows != shows || n.panel.hides != hides || len(n.all) != sentAll {
		t.Fatalf("after shutdown: timer=%v shows=%d hides=%d events=%d", n.fire != nil, n.panel.shows-shows, n.panel.hides-hides, len(n.all)-sentAll)
	}
	if st := n.State(); st.Preview != "before" {
		t.Fatalf("an event after shutdown changed the state: %+v", st)
	}
	if err := n.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
}

func TestNotchDemo(t *testing.T) {
	for value, want := range map[string]bool{"1": true, "": false, "0": false, "true": false} {
		if got := notchDemo(func(string) string { return value }); got != want {
			t.Errorf("KOLO_CHAT_NOTCH_DEMO=%q: demo %v, want %v", value, got, want)
		}
	}
}

func TestPreviewOf(t *testing.T) {
	if got := previewOf("  one\ttwo\r\n\nthree  "); got != "one two three" {
		t.Fatalf("previewOf folded to %q", got)
	}
	exact := strings.Repeat("ñ", notchPreviewRunes)
	if got := previewOf(exact); got != exact {
		t.Fatalf("a line of %d characters was cut: %q", notchPreviewRunes, got)
	}
	if got := previewOf(exact + "x"); utf8.RuneCountInString(got) != notchPreviewRunes || !strings.HasSuffix(got, "…") {
		t.Fatalf("a line one character too long gave %q", got)
	}
	long := previewOf(strings.Repeat("añ ", 4000))
	if n := utf8.RuneCountInString(long); n > notchPreviewRunes || !strings.HasSuffix(long, "…") {
		t.Fatalf("long preview has %d runes: %q", n, long)
	}
	if !utf8.ValidString(long) {
		t.Fatal("the cut broke a character")
	}
}

func TestDisplayName(t *testing.T) {
	members := []Member{{Key: alice, Name: "Sam"}, {Key: bob, Name: "Sam", Avatar: "🐢"}, {Key: "cc33", Name: ""}}
	for _, c := range []struct{ key, name, avatar string }{
		{alice, "Sam #aa11", "S"},
		{bob, "Sam #bb22", "🐢"},
		{"cc33", "cc33", ""},
		{"dd44dd44dd44", "dd44dd44", ""},
	} {
		if name, avatar := displayName(members, c.key); name != c.name || avatar != c.avatar {
			t.Errorf("displayName(%s) = %q, %q; want %q, %q", c.key, name, avatar, c.name, c.avatar)
		}
	}
}

// T11: with several conversations the notch counts every unread message, never the history, names the conversation
// of the newest one, sums the peers and the locked messages, and shows the worst pin. A conversation that leaves
// takes its peers, its pin and, when it had the newest message, that message with it.
func TestNotchSumsTheConversations(t *testing.T) {
	n := newTestNotch(t)
	n.forward("chat:conversations", []ConversationInfo{{ID: "c1", Name: "Family"}, {ID: "c2", Name: "Work"}})
	for _, ev := range []sent{
		{"chat:peers", ConvEvent{"c1", 2}},
		{"chat:peers", ConvEvent{"c2", 3}},
		{"chat:pin", ConvEvent{"c1", PinStatus{Locked: 2}}},
		{"chat:pin", ConvEvent{"c2", PinStatus{}}},
		{"chat:profiles", ConvEvent{"c1", []Member{{Key: alice, Name: "Alice"}}}},
		{"chat:profiles", ConvEvent{"c2", []Member{{Key: bob, Name: "Bob"}}}},
		{"chat:msg", ConvEvent{"c1", ChatMsg{From: alice, Room: "dev", Text: "one"}}},
		{"chat:history", ConvEvent{"c1", []ChatMsg{{From: alice, Room: "dev", Text: "old", Via: "history"}}}},
		{"chat:msg", ConvEvent{"c2", ChatMsg{From: bob, Room: "general", Text: "two"}}},
	} {
		n.forward(ev.name, ev.data)
	}
	st := n.State()
	if st.Unread != 2 || st.Peers != 5 || st.Pin != pinWaiting || st.Locked != 2 || st.Conversation != "Work" || st.Name != "Bob" || st.Room != "general" {
		t.Fatalf("state = %+v", st)
	}
	n.forward("chat:pin", ConvEvent{"c2", PinStatus{Error: "The pin is unreachable"}})
	if st := n.State(); st.Pin != pinError || st.Locked != 2 {
		t.Fatalf("an error in one conversation: %+v", st)
	}
	n.forward("chat:conversations", []ConversationInfo{{ID: "c1", Name: "Family"}})
	if st := n.State(); st.Peers != 2 || st.Pin != pinWaiting || st.Conversation != "" || st.HasMessage || st.Preview != "" {
		t.Fatalf("after Work left: %+v", st)
	}
	n.forward("chat:msg", ConvEvent{"c1", ChatMsg{From: alice, Room: "dev", Text: "three"}})
	if st := n.State(); st.Conversation != "Family" || st.Name != "Alice" || st.Unread != 3 {
		t.Fatalf("a message of Family: %+v", st)
	}
	n.forward("chat:conversations", []ConversationInfo{{ID: "c2", Name: "Work"}})
	if st := n.State(); st.HasMessage || st.Conversation != "" || st.Preview != "" || st.Name != "" || st.Room != "" {
		t.Fatalf("after Family, the newest message's conversation, left: %+v", st)
	}
	if v := trayViewOf(n.State(), "en"); strings.Contains(v.Tooltip, "Family") {
		t.Fatalf("the tray still names Family: %+v", v)
	}
	if len(n.main) != 14 {
		t.Fatalf("the main window got %d events", len(n.main))
	}
}
