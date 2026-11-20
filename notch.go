package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	// notchAutoHide is how long the notch window stays down after a message showed it.
	notchAutoHide = 6 * time.Second
	// notchPreviewRunes is the longest preview of a message the notch ever holds: one line, never the body.
	notchPreviewRunes = 72
	// notchDemoDelay is how long KOLO_CHAT_NOTCH_DEMO waits before it shows its sample message.
	notchDemoDelay = 3 * time.Second
	// notchPoll is how often the pointer is read to see whether it is on the camera housing.
	notchPoll = 80 * time.Millisecond

	notchSettingsFile = "notch.json"
)

// Pin states of NotchState.Pin.
const (
	pinUnknown = "unknown" // the pin has not answered yet
	pinOK      = "ok"
	pinError   = "error"   // the pin cannot be used
	pinWaiting = "waiting" // pulled messages cannot be opened yet
)

// NotchSettings is what the user chose for the notch window. It is kept in notch.json in the data directory.
type NotchSettings struct {
	Enabled bool `json:"enabled"` // show the notch window when a message arrives
	Preview bool `json:"preview"` // show the text of the message, not only who sent it
}

// NotchState is the payload of the "notch:state" event: what the notch window and the tray show. It is the only
// thing about the chat that reaches the notch window, and it never holds more of a message than Preview.
type NotchState struct {
	Supported  bool   `json:"supported"`  // this system has a notch window
	Enabled    bool   `json:"enabled"`    // the "Show messages in the notch" setting
	ShowText   bool   `json:"showText"`   // the "Show message text" setting
	HasMessage bool   `json:"hasMessage"` // a message arrived since the app started
	Name       string `json:"name"`       // the sender's display name, or the start of its kolo-id
	Avatar     string `json:"avatar"`     // the sender's avatar reference, or the first letter of its name
	Room       string `json:"room"`
	// Conversation is the local name of the newest message's conversation.
	Conversation string `json:"conversation"`
	Preview      string `json:"preview"` // one line of the newest message; empty when ShowText is off
	Unread       int    `json:"unread"`  // messages since the main window lost focus, in every conversation
	Peers        int    `json:"peers"`   // peers online, summed over the conversations
	Pin          string `json:"pin"`     // the worst over the conversations: "error", "waiting", "ok", then "unknown"
	Locked       int    `json:"locked"`  // pulled messages that cannot be opened yet, summed
}

// notchPanel is the native notch window, as much of it as the service needs.
type notchPanel interface {
	Show()
	Hide()
}

// pointerSample is one read of the pointer: whether it is on the camera housing, and the clicks made there since
// the last read.
type pointerSample struct {
	On     bool
	Clicks int
}

// What the main window is asked to do, by the notch or the tray.
const (
	windowStay = iota
	windowOpen
	windowToggle
)

// NotchService keeps what the notch window and the tray show. It learns it from the events of every conversation
// (see forward), so it adds nothing to the network.
//
// Nothing here calls a native window directly. A method changes what is wanted under mu and calls wake; apply
// then makes the windows and the tray match, one call at a time. So an event of the chat never waits for the
// main thread, and the order of the calls to wake does not matter.
type NotchService struct {
	dir string // the data directory, "" when the configuration is bad: then the settings are not saved

	// Set before the app runs, by attach or by a test.
	toMain   func(name string, data any)                 // sends an event to the main window only
	emit     func(name string, data any)                 // sends "notch:state" to every window
	window   func(action int)                            // windowOpen or windowToggle on the main window
	after    func(time.Duration, func()) (cancel func()) // the timer of the auto-hide
	every    func(time.Duration, func()) (stop func())   // the ticker of the pointer reads
	pointer  func() pointerSample                        // reads the pointer; nil where nothing can
	wake     func()                                      // makes apply run, now or soon
	refresh  []func(NotchState, string)                  // what else follows the state: the tray
	applyMu  sync.Mutex                                  // one apply at a time; guards shown
	shown    bool                                        // the notch window is down
	stopWork func()                                      // ends the goroutine of attach

	mu         sync.Mutex // guards the fields below
	panel      notchPanel // nil where there is no notch window
	set        NotchSettings
	lang       string
	closed     bool
	focused    bool // the main window has the focus
	want       bool // the notch window should be down
	windowWant int  // windowOpen or windowToggle, until apply does it
	// By conversation ID: its members, peers and pin status, and its local name. A pin that has not answered has none.
	members      map[string][]Member
	peers        map[string]int
	pins         map[string]PinStatus
	names        map[string]string
	conv         string // the newest message's conversation
	conversation string // its name when the message came
	from         string // kolo-id of the newest message's sender
	room         string
	preview      string
	hasMessage   bool
	unread       int
	cancelHide   func()
	hideGen      uint64 // the auto-hide that counts; one that fires with another number is stale
	owed         bool   // a message brought the notch down, so an auto-hide is due
	hovered      bool   // the pointer is on the notch: no auto-hide starts until it leaves
	housing      bool   // the screen of the notch window has a camera housing
	appHidden    bool   // the app is hidden (Command+H)
	onHousing    bool   // the pointer is on the camera housing, as the last read saw it
	stopPoll     func() // stops the pointer reads; nil when none run
}

// NewNotchService returns the service with the settings saved in the data directory of cfg.
func NewNotchService(cfg Config) *NotchService {
	n := &NotchService{
		members: map[string][]Member{},
		peers:   map[string]int{},
		pins:    map[string]PinStatus{},
		names:   map[string]string{},
		set:     NotchSettings{Enabled: true, Preview: true},
		lang:    "en",
		toMain:  func(string, any) {},
		emit:    func(string, any) {},
		window:  func(int) {},
		after:   func(d time.Duration, f func()) func() { t := time.AfterFunc(d, f); return func() { t.Stop() } },
		every:   every,
	}
	n.wake = n.apply
	if s, err := cfg.resolve(); err == nil {
		n.dir = s.DataDir
		n.set = loadNotchSettings(n.dir)
	}
	return n
}

// every calls f every d, on a goroutine of its own, until stop.
func every(d time.Duration, f func()) (stop func()) {
	t := time.NewTicker(d)
	done := make(chan struct{})
	go func() {
		defer t.Stop()
		for {
			select {
			case <-t.C:
				f()
			case <-done:
				return
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done) }) }
}

// loadNotchSettings reads notch.json. A missing or unreadable file gives the defaults: both settings on.
func loadNotchSettings(dir string) NotchSettings {
	set := NotchSettings{Enabled: true, Preview: true}
	data, err := os.ReadFile(filepath.Join(dir, notchSettingsFile))
	if err == nil {
		err = json.Unmarshal(data, &set)
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.Print("notch settings: ", err)
		return NotchSettings{Enabled: true, Preview: true}
	}
	return set
}

// saveNotchSettings replaces notch.json in one step, like members.json.
func saveNotchSettings(dir string, set NotchSettings) error {
	if dir == "" {
		return errors.New("no data directory to keep the settings in")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(set)
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, notchSettingsFile), data, 0o600)
}

// State returns what the notch window shows now.
func (n *NotchService) State() NotchState {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.state()
}

func (n *NotchService) state() NotchState {
	st := NotchState{
		Supported:  n.panel != nil,
		Enabled:    n.set.Enabled,
		ShowText:   n.set.Preview,
		HasMessage: n.hasMessage,
		Room:       n.room,
		Unread:     n.unread,
	}
	for _, p := range n.peers {
		st.Peers += p
	}
	st.Pin, st.Locked = worstPin(n.pins)
	if n.hasMessage {
		st.Conversation = n.conversation
		st.Name, st.Avatar = displayName(n.members[n.conv], n.from)
		if n.set.Preview {
			st.Preview = n.preview
		}
	}
	return st
}

// apply makes the notch window, the main window and the tray match what is wanted, and tells the windows the
// state. One runs at a time and each reads the newest state, so a late one can never undo a newer one.
func (n *NotchService) apply() {
	n.applyMu.Lock()
	defer n.applyMu.Unlock()
	n.mu.Lock()
	st, lang, panel, want, action, closed := n.state(), n.lang, n.panel, n.want, n.windowWant, n.closed
	n.windowWant = windowStay
	n.mu.Unlock()
	if closed {
		return
	}
	n.emit("notch:state", st)
	for _, f := range n.refresh {
		f(st, lang)
	}
	if panel != nil && want != n.shown {
		if n.shown = want; want {
			panel.Show()
		} else {
			panel.Hide()
		}
	}
	if action != windowStay {
		n.window(action)
	}
}

// Settings returns the settings of the notch window.
func (n *NotchService) Settings() NotchSettings {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.set
}

// SetEnabled changes "Show messages in the notch" and saves it. Turning it off hides the notch at once.
func (n *NotchService) SetEnabled(on bool) error {
	return n.change(func(set *NotchSettings) { set.Enabled = on })
}

// SetShowText changes "Show message text" and saves it.
func (n *NotchService) SetShowText(on bool) error {
	return n.change(func(set *NotchSettings) { set.Preview = on })
}

// change changes one setting, so a window that shows an old value of the other cannot write it back.
func (n *NotchService) change(f func(*NotchSettings)) error {
	n.mu.Lock()
	f(&n.set)
	set := n.set
	if !set.Enabled {
		n.up()
	}
	n.syncPoll()
	n.mu.Unlock()
	n.wake()
	return saveNotchSettings(n.dir, set)
}

// SetLanguage tells the service the language of the window ("en", "es" or "pt"), for the tray menu.
func (n *NotchService) SetLanguage(lang string) {
	n.mu.Lock()
	n.lang = pickLanguage(lang)
	n.mu.Unlock()
	n.wake()
}

// Show brings the notch window down and keeps it until Hide: no auto-hide follows.
func (n *NotchService) Show() { n.setWant(func(bool) bool { return true }) }

// Hide sends the notch window back up. Its page stays loaded.
func (n *NotchService) Hide() { n.setWant(func(bool) bool { return false }) }

// toggle is what the global shortcut does.
func (n *NotchService) toggle() { n.setWant(func(down bool) bool { return !down }) }

// setWant is a choice of the user: it ends the auto-hide that waits.
func (n *NotchService) setWant(f func(down bool) bool) {
	n.mu.Lock()
	if n.panel == nil {
		n.mu.Unlock()
		return
	}
	n.stopHide()
	if n.want = f(n.want); !n.want {
		n.up()
	}
	n.mu.Unlock()
	n.wake()
}

// Hold says the pointer is on the notch: no auto-hide runs or starts until Release, however the notch came
// down. Release then starts the auto-hide if a message or the pointer on the housing is why the notch is down,
// and the pointer is not on the housing.
func (n *NotchService) Hold() {
	n.mu.Lock()
	if n.want {
		n.hovered = true
		n.stopTimer()
	}
	n.mu.Unlock()
}

// Release ends a Hold.
func (n *NotchService) Release() {
	n.mu.Lock()
	n.hovered = false
	if n.owed && n.want && !n.onHousing {
		n.armHide()
	}
	n.mu.Unlock()
}

// syncPoll reads the pointer only while it can bring the notch down: a camera housing, the setting on and the
// app not hidden. The caller holds mu.
func (n *NotchService) syncPoll() {
	on := n.pointer != nil && n.panel != nil && n.housing && n.set.Enabled && !n.appHidden && !n.closed
	switch {
	case on && n.stopPoll == nil:
		n.stopPoll = n.every(notchPoll, n.poll)
	case !on && n.stopPoll != nil:
		n.stopPoll()
		n.stopPoll, n.onHousing = nil, false
	}
}

// setHousing says whether the screen of the notch window has a camera housing. It changes with the screens.
func (n *NotchService) setHousing(has bool) {
	n.mu.Lock()
	n.housing = has
	n.syncPoll()
	n.mu.Unlock()
}

// setAppHidden follows Command+H and its undo.
func (n *NotchService) setAppHidden(hidden bool) {
	n.mu.Lock()
	n.appHidden = hidden
	n.syncPoll()
	n.mu.Unlock()
}

func (n *NotchService) poll() { n.pointerMoved(n.pointer()) }

// pointerMoved brings the notch down when the pointer reaches the camera housing, as a message does, so it goes
// back up notchAutoHide after the pointer left both the housing and the notch. A click on the housing while the
// notch is down opens the chat.
func (n *NotchService) pointerMoved(s pointerSample) {
	n.mu.Lock()
	if n.stopPoll == nil { // a read that was on its way when the reads stopped
		n.mu.Unlock()
		return
	}
	changed, was := false, n.onHousing
	if s.On != n.onHousing {
		n.onHousing, changed = s.On, true
		switch {
		case s.On:
			n.stopTimer()
			if !n.want {
				n.want, n.owed = true, true
			}
		case !n.hovered && n.owed && n.want:
			n.armHide()
		}
	}
	if s.Clicks > 0 && was && s.On && n.want { // a click counted before the pointer came is not one on the notch
		n.up()
		n.windowWant, changed = windowOpen, true
	}
	n.mu.Unlock()
	if changed {
		n.wake()
	}
}

// OpenChat hides the notch window and focuses the main window.
func (n *NotchService) OpenChat() { n.askWindow(windowOpen) }

// toggleWindow shows or hides the main window: a click on the tray icon.
func (n *NotchService) toggleWindow() { n.askWindow(windowToggle) }

func (n *NotchService) askWindow(action int) {
	n.mu.Lock()
	n.up()
	n.windowWant = action
	n.mu.Unlock()
	n.wake()
}

// forward is where the conversations send their events. They go to the main window only: the notch window gets
// nothing but "notch:state", so no message text, invite code or member list reaches its page.
func (n *NotchService) forward(name string, data any) {
	n.observe(name, data)
	n.toMain(name, data)
}

// observe keeps what the notch shows from an event of a conversation, a ConvEvent, or from the list of
// conversations. A message that arrives while the main window is not focused counts as unread and brings the notch
// down for notchAutoHide.
func (n *NotchService) observe(name string, data any) {
	conv := ""
	if ev, ok := data.(ConvEvent); ok {
		conv, data = ev.Conv, ev.Data
	}
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		return
	}
	switch v := data.(type) {
	case ChatMsg:
		n.from, n.room, n.preview, n.hasMessage = v.From, v.Room, previewOf(v.Text), true
		n.conv, n.conversation = conv, n.names[conv]
		if !n.focused {
			n.unread++
			if n.set.Enabled && n.panel != nil {
				n.want, n.owed = true, true
				if !n.hovered && !n.onHousing {
					n.armHide()
				}
			}
		}
	case int:
		if name != "chat:peers" {
			n.mu.Unlock()
			return
		}
		n.peers[conv] = v
	case PinStatus:
		n.pins[conv] = v
	case []Member:
		n.members[conv] = v
	case []ConversationInfo: // a conversation that left takes its peers and pins with it
		n.names = map[string]string{}
		for _, c := range v {
			n.names[c.ID] = c.Name
		}
		if _, ok := n.names[n.conv]; !ok && n.conv != "" { // the newest message's conversation left: show nothing of it
			n.conv, n.conversation, n.from, n.room, n.preview, n.hasMessage = "", "", "", "", "", false
		}
		keepOnly(n.members, n.names)
		keepOnly(n.peers, n.names)
		keepOnly(n.pins, n.names)
	default:
		n.mu.Unlock()
		return
	}
	n.mu.Unlock()
	n.wake()
}

// worstPin sums up the pins of every conversation: an error first, then messages that wait, then ok, and unknown
// while no pin has answered.
func worstPin(pins map[string]PinStatus) (state string, locked int) {
	rank := map[string]int{pinUnknown: 0, pinOK: 1, pinWaiting: 2, pinError: 3}
	state = pinUnknown
	for _, p := range pins {
		s := pinOK
		switch {
		case p.Error != "":
			s = pinError
		case p.Locked > 0:
			s = pinWaiting
		}
		if rank[s] > rank[state] {
			state = s
		}
		locked += p.Locked
	}
	return state, locked
}

// keepOnly removes from m the conversations that are not in names, but not "" (an event without a conversation).
func keepOnly[V any](m map[string]V, names map[string]string) {
	for id := range m {
		if _, ok := names[id]; !ok && id != "" {
			delete(m, id)
		}
	}
}

// armHide starts the auto-hide, in place of the one that waits. The caller holds mu.
func (n *NotchService) armHide() {
	n.stopTimer()
	gen := n.hideGen
	n.cancelHide = n.after(notchAutoHide, func() { n.autoHide(gen) })
}

// autoHide is the timer of armHide. A timer cannot always be stopped once it fired, so one that was replaced or
// cancelled knows it by its number and does nothing.
func (n *NotchService) autoHide(gen uint64) {
	n.mu.Lock()
	if gen != n.hideGen {
		n.mu.Unlock()
		return
	}
	n.up()
	n.mu.Unlock()
	n.wake()
}

// stopTimer cancels the auto-hide that waits. The caller holds mu.
func (n *NotchService) stopTimer() {
	n.hideGen++
	if n.cancelHide != nil {
		n.cancelHide()
	}
	n.cancelHide = nil
}

// stopHide is stopTimer for a choice of the user: no auto-hide is due any more. The caller holds mu.
func (n *NotchService) stopHide() {
	n.stopTimer()
	n.owed = false
}

// up sends the notch back up, where the pointer cannot be on it. The caller holds mu.
func (n *NotchService) up() {
	n.stopHide()
	n.want, n.hovered = false, false
}

// setFocused follows the focus of the main window. Focus reads everything: the count goes to zero and the
// notch goes back up.
func (n *NotchService) setFocused(focused bool) {
	n.mu.Lock()
	n.focused = focused
	if focused {
		n.unread = 0
		n.up()
	}
	n.mu.Unlock()
	n.wake()
}

func (n *NotchService) language() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.lang
}

// ServiceShutdown stops the auto-hide and everything else: the chat stops after this service, and what it
// still says must not touch a window that is closing. Wails calls it when the app quits.
func (n *NotchService) ServiceShutdown() error {
	n.mu.Lock()
	n.closed = true
	n.stopHide()
	n.syncPoll()
	n.mu.Unlock()
	if n.stopWork != nil {
		n.stopWork()
	}
	return nil
}

// previewOf is the one line of a message the notch may show: its whitespace folded, cut at notchPreviewRunes.
func previewOf(text string) string {
	line := strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(line) <= notchPreviewRunes {
		return line
	}
	return strings.TrimRight(string([]rune(line)[:notchPreviewRunes-1]), " ") + "…"
}

// displayName is the name and the avatar to show for a member, as the main window does: the profile name, with
// the start of the key when two members share it, or the start of the key while the profile is not known.
func displayName(members []Member, key string) (name, avatar string) {
	short := key
	if len(short) > 8 {
		short = short[:8]
	}
	var m Member
	for _, x := range members {
		if x.Key == key {
			m = x
		}
	}
	if m.Name == "" {
		return short, ""
	}
	name, avatar = m.Name, m.Avatar
	for _, x := range members {
		if x.Key != key && x.Name == m.Name && len(key) >= 4 {
			name = m.Name + " #" + key[:4]
		}
	}
	if avatar == "" {
		r, _ := utf8.DecodeRuneInString(m.Name)
		avatar = strings.ToUpper(string(r))
	}
	return name, avatar
}
