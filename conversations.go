package main

//lint:file-ignore ST1005 these errors are sentences the window shows as they are

// Several conversations in one app.
//
// Each conversation lives in a folder of its own, <data>/conversations/<id>, with its own engine (ChatService) and
// so its own kolo-id, swarm, logs, pins and invites. One engine per conversation follows the rules of the libraries
// as they are: a pin keeps the newest capability of each owner, and a connection to a peer key serves one protocol.
// Conversations lists the folders, starts an engine for each, and routes every call of the window by ID.

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	koloswarm "github.com/ciranda-dev/kolo-swarm"
)

const (
	layoutVersion   = 3 // 3: each conversation has a braid
	layoutFile      = "layout.json"
	conversationDir = "conversations"
	localFile       = "local.json"
	defaultConvName = "Conversation"
	maxConvName     = 32 // characters in a conversation's name, as in a room's
	maxSmallFile    = 4096
	maxStateFile    = 16 << 20 // conversation.json, members.json and a pin token: far above what the app writes
)

// oldLayoutNames are the files a data folder of 0.1.9 or older held at its top, for its one conversation.
var oldLayoutNames = []string{"identity.key", "conversation.json", "members.json", "directory-log", "store", "invites", "pin-tokens"}

var (
	// ErrOldLayout matches the *DataDirError of a data folder of 0.1.9 or older (Layout 1).
	ErrOldLayout            = errors.New("data folder made by Kolo Chat 0.1.9 or older")
	ErrUnknownConversation  = errors.New("That conversation is not on this computer.")
	ErrStarting             = errors.New("This room is still connecting. Try again in a moment.")
	ErrTooManyConversations = errors.New("You have the most rooms this app keeps (see -max-conversations). Leave one first.")
	ErrInvalidName          = errors.New("That name cannot be used")

	errNotStarted = errors.New("not started")
	errNotRegular = errors.New("not a regular file of a size this app writes")
)

// ConversationInfo is one conversation as the window lists it. Me is our kolo-id in it, Joined is false for the
// join that waits for an invite's answer, and Error says why it did not start, with the Folder to move aside.
type ConversationInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Me     string `json:"me"`
	Joined bool   `json:"joined"`
	Admin  bool   `json:"admin"`
	Error  string `json:"error"`
	Folder string `json:"folder"`
	Layout int    `json:"layout,omitempty"`
	// State is "connecting" while the room waits for its turn to start or reaches the network, "online", "reconnecting"
	// while it looks for the peers it just lost, or "offline" while no entry node answers: it asks again by itself.
	// It is empty for a conversation that did not start.
	State string `json:"state,omitempty"`
}

// AppInfo is what Start returns. HasPins says the app has pins, which keep messages for people who are offline in the
// conversations it starts, Network which network the app joins, and First the conversation that starts first, the one
// the window should show. Version is the release, or "dev" for a build that is not one.
type AppInfo struct {
	Conversations    []ConversationInfo `json:"conversations"`
	MaxConversations int                `json:"maxConversations"`
	HasPins          bool               `json:"hasPins"`
	Network          NetworkInfo        `json:"network"`
	First            string             `json:"first"`
	Version          string             `json:"version"`
}

// ConvEvent is the payload of every chat event: the conversation it is about, and the engine's payload.
type ConvEvent struct {
	Conv string `json:"conv"`
	Data any    `json:"data"`
}

// localInfo is local.json: the name only we see, when the conversation was added here, and when it last had a message,
// in Unix seconds. The last message decides which conversations start first.
type localInfo struct {
	Name    string `json:"name"`
	Created int64  `json:"created"`
	Active  int64  `json:"active,omitempty"`
}

// convEntry is one conversation folder and its engine: nil while it waits for its turn to start, and when it did not
// start (err says why).
type convEntry struct {
	id, dir string
	local   localInfo
	joined  bool
	chat    *ChatService
	me      string
	err     error
	state   string        // the engine's network state, from its "chat:network" events
	ready   chan struct{} // closed when the engine's start is over, for a conversation that waited to start
}

// Conversations is the window's service: it keeps one engine per conversation folder and routes every call by ID.
type Conversations struct {
	cfg        Config
	emit       func(name string, data any)
	checkRelay func(context.Context, koloswarm.Relay) error // tests count the dials

	pace pace // zero in the app, which uses the constants of netstate.go; tests shorten them

	opMu    sync.Mutex // one Start, Create, Join, Rename, Leave or ServiceShutdown at a time
	localMu sync.Mutex // one write of a local.json at a time
	set     settings
	root    string
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	gate    *gate

	mu         sync.Mutex // guards the fields below
	started    bool
	convs      map[string]*convEntry
	queue      []*convEntry    // the conversations that wait for their turn to start, in the order they start
	focus      string          // the conversation on screen, which starts before the rest
	configured string          // the ID of the -conversation-key conversation, "" when none
	wrongKey   map[string]bool // relays that answered with another key, by key
	relays     []RelayState    // the app's own relay status, for the window without a conversation
	relaySeq   uint64
}

// NewConversations returns the service that reads cfg when it starts and tells the window what happens through emit.
func NewConversations(cfg Config, emit func(name string, data any)) *Conversations {
	return &Conversations{cfg: cfg, emit: emit, checkRelay: checkRelay}
}

// Start opens the data folder and starts every conversation in it, each on its own. It fails only for a bad
// setting (*ConfigError) or a data folder of another layout (*DataDirError): a conversation that does not start
// shows its Error in the list, and the others run.
func (a *Conversations) Start() (AppInfo, error) {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	if a.isStarted() {
		return a.info(), nil
	}
	set, err := a.cfg.resolve()
	if err != nil {
		return AppInfo{}, err
	}
	if err := openLayout(set.DataDir); err != nil {
		return AppInfo{}, err
	}
	entries, err := listConversations(set.DataDir)
	if err != nil {
		return AppInfo{}, err
	}
	a.set, a.root = set, set.DataDir
	configured, err := a.findConfigured(entries)
	if err != nil {
		return AppInfo{}, err
	}
	isNew := configured != nil && !slices.Contains(entries, configured)
	if isNew {
		entries = append(entries, configured)
	}
	entries = configuredFirst(entries, func(e *convEntry) bool { return e == configured })
	if set.fixedPort() && len(entries) > 1 {
		if isNew {
			a.dropFolder(configured)
		}
		return AppInfo{}, &ConfigError{Setting: "listen", Err: errFixedPort}
	}

	a.ctx, a.cancel = context.WithCancel(context.Background())
	a.gate = newGate(a.pace)
	a.mu.Lock()
	a.started, a.convs, a.wrongKey = true, map[string]*convEntry{}, map[string]bool{}
	a.relays = make([]RelayState, len(set.relays))
	for i, r := range set.relays {
		a.relays[i] = RelayState{Addr: r.Addr, Key: hex.EncodeToString(r.PublicKey), State: "waiting"}
	}
	if configured != nil {
		a.configured = configured.id
	}
	a.mu.Unlock()

	// A conversation starts when its turn comes (see startQueue); a folder that cannot start shows its row at once.
	var queue, joins []*convEntry
	for i, e := range entries {
		switch {
		case i >= set.MaxConversations: // listed, left as it is, and not started
			e.err = ErrTooManyConversations
			a.put(e)
		case e.joined || e == configured:
			e.ready = make(chan struct{})
			queue = append(queue, e)
			a.put(e)
		default: // a join that did not finish, or a folder that is left as it is, with its row
			if e.err = onlyJoinFiles(e.dir); e.err != nil {
				a.put(e)
				continue
			}
			joins = append(joins, e)
		}
	}
	var wg sync.WaitGroup
	for _, e := range joins {
		wg.Go(func() { a.startEngine(e, a.engineConfig(e.dir, false)) })
	}
	wg.Wait()
	for _, e := range joins {
		if e.chat != nil { // an unfinished join: its engine found no messages and no braid
			log.Printf("%s/%s: a join that did not finish, removed", conversationDir, e.id)
			if err := e.chat.ServiceShutdown(); err != nil {
				log.Print("join: ", err)
			}
			a.dropFolder(e)
			continue
		}
		a.put(e)
	}
	a.sortQueue(queue, configured)
	a.mu.Lock()
	a.queue = queue
	a.mu.Unlock()
	first := ""
	if len(queue) > 0 {
		first = queue[0].id
	}
	a.startQueue()
	if isNew && configured.err != nil { // a bad setting, such as a -pin-token, as before there were several
		a.dropFolder(configured)
		a.shutdown()
		return AppInfo{}, configured.err
	}
	a.checkRelays()
	a.emitRelays()
	a.emitList()
	info := a.info()
	info.First = first
	return info, nil
}

// sortQueue puts the conversations in the order they start: the -conversation-key one, then the one with the newest
// message, then the others by when they were added.
func (a *Conversations) sortQueue(queue []*convEntry, configured *convEntry) {
	slices.SortFunc(queue, func(x, y *convEntry) int {
		return cmp.Or(
			cmp.Compare(inFront(y == configured), inFront(x == configured)),
			cmp.Compare(y.local.Active, x.local.Active),
			compareEntries(x, y))
	})
}

// inFront is 1 for what starts before the rest, and 0 for the rest.
func inFront(b bool) int {
	if b {
		return 1
	}
	return 0
}

// startQueue starts the engines of the queue, a few at a time: each worker takes the next conversation, which is the one
// on screen if the window said so (Focus), starts it in a place of the gate and goes on. It returns when the first
// engines are up, so a window with a few rooms finds them all started; the rest start in the background.
func (a *Conversations) startQueue() {
	n := a.gate.pace.slots
	a.mu.Lock()
	n = min(n, len(a.queue))
	a.mu.Unlock()
	var first sync.WaitGroup
	first.Add(n)
	for range n {
		a.wg.Go(func() {
			once := sync.OnceFunc(first.Done)
			defer once()
			for e := a.pop(); e != nil; e = a.pop() {
				a.startQueued(e)
				once()
			}
		})
	}
	first.Wait()
}

// pop takes the next conversation to start, nil when none is left or the app stopped.
func (a *Conversations) pop() *convEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.started || len(a.queue) == 0 {
		return nil
	}
	i := max(0, slices.IndexFunc(a.queue, func(e *convEntry) bool { return e.id == a.focus }))
	e := a.queue[i]
	a.queue = slices.Delete(a.queue, i, i+1)
	return e
}

// startSettle bounds how long a place of the gate waits for an engine to join its topic.
const startSettle = 20 * time.Second

// startQueued starts the engine of a waiting conversation in a place of the gate, and gives the place back once the
// engine has joined its topic, or found the network down, and a pause has passed.
func (a *Conversations) startQueued(e *convEntry) {
	configured := a.isConfigured(e.id)
	ran := a.gate.do(a.ctx, func() {
		a.startEngine(e, a.engineConfig(e.dir, configured))
		a.adopt(e)
		a.mu.Lock()
		c := e.chat
		a.mu.Unlock()
		if c == nil {
			return
		}
		t := time.NewTimer(startSettle)
		defer t.Stop()
		select {
		case <-c.settled:
		case <-t.C:
		case <-a.ctx.Done():
		}
	})
	if !ran { // the app stopped before this conversation's turn
		return
	}
	a.emitList()
}

func (a *Conversations) isConfigured(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.configured == id
}

// Focus tells the app which conversation is on screen: if it has not started yet, it starts before the others.
func (a *Conversations) Focus(conv string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if e := a.convs[conv]; e != nil && e.ready != nil && e.chat == nil && e.err == nil {
		a.focus = conv
	}
}

// findConfigured returns the conversation that -conversation-key names: the folder whose conversation.json holds
// the key, or a new folder for it. It is nil without the setting.
func (a *Conversations) findConfigured(entries []*convEntry) (*convEntry, error) {
	if !a.set.inConversation {
		return nil, nil
	}
	for _, e := range entries {
		if v, found, err := loadConversation(e.dir, a.set.MaxPins); err == nil && found && slices.Equal(v.Key, a.set.conversationKey[:]) {
			return e, nil
		}
	}
	return a.newFolder(defaultConvName)
}

// engineConfig is the configuration of one engine: the app's, in the conversation's folder. The key, the pins and
// the tokens are only for the -conversation-key conversation; every other one reads its own conversation.json.
func (a *Conversations) engineConfig(dir string, configured bool) Config {
	cfg := a.cfg
	cfg.DataDir = dir
	if !configured {
		cfg.ConversationKey, cfg.Pins, cfg.PinFiles, cfg.PinTokens = "", nil, nil, nil
	}
	return cfg
}

// startEngine starts the conversation's engine, whose events go to the window tagged with the conversation's ID. The
// engine's network state goes into the list instead, and a message counts as the conversation's latest activity.
func (a *Conversations) startEngine(e *convEntry, cfg Config) {
	c := NewChatService(cfg, func(name string, data any) {
		switch v := data.(type) {
		case NetStatus:
			a.setState(e, v.State)
			return
		case ChatMsg:
			a.touch(e)
		}
		a.emit(name, ConvEvent{Conv: e.id, Data: data})
	})
	c.tune, c.gate = a.pace, a.gate
	me, err := c.Start()
	a.mu.Lock()
	e.me, e.err = me, err
	if err == nil {
		e.chat, e.joined = c, c.Joined()
	} else {
		log.Printf("conversation %s did not start: %v", e.id, err)
	}
	stop := !a.started // the app stopped while this engine started
	if e.ready != nil {
		close(e.ready)
	}
	a.mu.Unlock()
	if stop && err == nil {
		c.ServiceShutdown()
	}
}

// setState records the engine's network state and tells the window with a new list.
func (a *Conversations) setState(e *convEntry, state string) {
	a.mu.Lock()
	changed := e.state != state
	e.state = state
	listed := a.convs[e.id] == e
	a.mu.Unlock()
	if changed && listed {
		a.emitList()
	}
}

// touch records that the conversation has a message now, at most once a minute: the newest ones start first.
func (a *Conversations) touch(e *convEntry) {
	now := time.Now().Unix()
	a.mu.Lock()
	recent := now-e.local.Active < 60
	a.mu.Unlock()
	if recent {
		return
	}
	if err := a.writeLocal(e, func(l *localInfo) { l.Active = now }); err != nil {
		log.Print("conversation activity: ", err)
	}
}

// writeLocal changes the conversation's local.json in one step, keeping what other writers changed.
func (a *Conversations) writeLocal(e *convEntry, change func(*localInfo)) error {
	a.localMu.Lock()
	defer a.localMu.Unlock()
	a.mu.Lock()
	local := e.local
	a.mu.Unlock()
	change(&local)
	if err := writeJSON(filepath.Join(e.dir, localFile), local); err != nil {
		return err
	}
	a.mu.Lock()
	e.local = local
	a.mu.Unlock()
	return nil
}

// put lists a conversation.
func (a *Conversations) put(e *convEntry) {
	a.mu.Lock()
	a.convs[e.id] = e
	a.mu.Unlock()
}

// add puts a started conversation in the list and gives its engine the relays that answered with another key.
func (a *Conversations) add(e *convEntry) {
	a.put(e)
	a.adopt(e)
}

// adopt gives the engine of e the relays that answered with another key. The check reads the list under the same
// lock, so a key found at any time reaches every engine.
func (a *Conversations) adopt(e *convEntry) {
	a.mu.Lock()
	var wrong []string
	chat := e.chat
	if chat != nil {
		for key := range a.wrongKey {
			wrong = append(wrong, key)
		}
	}
	a.mu.Unlock()
	for _, key := range wrong {
		chat.relayWrongKey([]byte(key))
	}
}

// Create starts a new conversation that we own, with a key from crypto/rand and the app's pins. name is only ours.
func (a *Conversations) Create(name string) (ConversationInfo, error) {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	name, err := convName(name, defaultConvName)
	if err != nil {
		return ConversationInfo{}, err
	}
	if err := a.hasRoom(); err != nil {
		return ConversationInfo{}, err
	}
	e, err := a.newFolder(name)
	if err != nil {
		return ConversationInfo{}, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		a.dropFolder(e)
		return ConversationInfo{}, err
	}
	cfg := a.engineConfig(e.dir, false)
	cfg.ConversationKey, cfg.Pins, cfg.PinFiles = hex.EncodeToString(key), a.cfg.Pins, a.cfg.PinFiles
	clear(key)
	if a.startEngine(e, cfg); e.err != nil {
		a.dropFolder(e)
		return ConversationInfo{}, e.err
	}
	a.add(e)
	a.emitList()
	return a.infoOf(e), nil
}

// Join uses an invite code in a new folder: each invite is a conversation of its own, with a new kolo-id. A Join
// that fails or is cancelled removes that folder, kolo-id included: an admin may have admitted that key already, and
// the braid admits a key once, so the next Join starts with a new one.
func (a *Conversations) Join(code, name string) (ConversationInfo, error) {
	name, err := convName(name, defaultConvName)
	if err != nil {
		return ConversationInfo{}, err
	}
	a.opMu.Lock()
	e, err := a.pendingJoin()
	a.opMu.Unlock()
	if err != nil {
		return ConversationInfo{}, err
	}
	if err := e.chat.Join(code); err != nil {
		a.opMu.Lock()
		a.discardJoin(e)
		a.opMu.Unlock()
		return ConversationInfo{}, err
	}
	a.opMu.Lock()
	defer a.opMu.Unlock()
	a.mu.Lock()
	e.joined = true
	a.mu.Unlock()
	if err := a.writeLocal(e, func(l *localInfo) { l.Name = name }); err != nil {
		log.Print("conversation name: ", err) // the conversation works, and shows as "Conversation" after a restart
	}
	a.emitList()
	return a.infoOf(e), nil
}

// pendingJoin returns the conversation of the join in progress, or a new one made and started for this join.
func (a *Conversations) pendingJoin() (*convEntry, error) {
	a.mu.Lock()
	var pending *convEntry
	for _, e := range a.convs {
		if !e.joined && e.chat != nil {
			pending = e
		}
	}
	a.mu.Unlock()
	if pending != nil {
		return pending, nil // its Join says that a join is in progress
	}
	if err := a.hasRoom(); err != nil {
		return nil, err
	}
	e, err := a.newFolder(defaultConvName)
	if err != nil {
		return nil, err
	}
	if a.startEngine(e, a.engineConfig(e.dir, false)); e.err != nil {
		a.dropFolder(e)
		return nil, e.err
	}
	a.add(e)
	a.emitList() // the window shows the kolo-id the join uses, for an invite that needs approval
	return e, nil
}

// discardJoin stops the engine of a Join that failed and removes its folder, which never held a conversation.
func (a *Conversations) discardJoin(e *convEntry) {
	a.mu.Lock()
	if a.convs[e.id] != e {
		a.mu.Unlock()
		return
	}
	delete(a.convs, e.id)
	a.mu.Unlock()
	if err := e.chat.ServiceShutdown(); err != nil {
		log.Print("join: ", err)
	}
	a.dropFolder(e)
	a.emitList()
}

// CancelJoin stops the Join that waits, for the inviter or for its answer.
func (a *Conversations) CancelJoin() {
	a.mu.Lock()
	var c *ChatService
	for _, e := range a.convs {
		if !e.joined && e.chat != nil {
			c = e.chat
		}
	}
	a.mu.Unlock()
	if c != nil {
		c.CancelJoin()
	}
}

// Rename changes the name of a conversation, which only we see, and rewrites local.json in one step.
func (a *Conversations) Rename(conv, name string) error {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	name, err := convName(name, "")
	if err != nil {
		return err
	}
	a.mu.Lock()
	e := a.convs[conv]
	if e == nil || e.chat == nil { // a folder that did not start is left as it is
		a.mu.Unlock()
		return ErrUnknownConversation
	}
	a.mu.Unlock()
	if err := a.writeLocal(e, func(l *localInfo) { l.Name = name }); err != nil {
		return err
	}
	a.emitList()
	return nil
}

// PlanLeave returns what Leave deletes, the conversation's folder, and what stays, with the token Leave needs.
func (a *Conversations) PlanLeave(conv string) (LeavePlan, error) {
	c, err := a.await(conv, startWait, true)
	if err != nil {
		return LeavePlan{}, err
	}
	plan, err := c.PlanLeave()
	if err != nil {
		return LeavePlan{}, err
	}
	plan.Keep = []string{filepath.Join(a.root, layoutFile), filepath.Join(a.root, notchSettingsFile)}
	for _, other := range a.List() {
		if other.ID == conv {
			plan.Name = other.Name
		} else if other.Joined || other.Error != "" {
			plan.Keep = append(plan.Keep, other.Name)
			plan.Others++
		}
	}
	a.mu.Lock()
	plan.Delete, plan.Configured = []string{plan.Dir}, conv == a.configured
	a.mu.Unlock()
	return plan, nil
}

// Leave removes one conversation from this computer: its engine stops and its folder, with its kolo-id, is deleted.
// It needs the token of that conversation's latest PlanLeave. The founder may name an admin in to, who then settles
// changes to members and rooms: Leave waits until that admin took over, which needs it online, or until CancelLeave.
// The other conversations go on.
func (a *Conversations) Leave(conv, token, to string) error {
	if !validID(conv) {
		return ErrUnknownConversation
	}
	c, err := a.await(conv, startWait, true)
	if err != nil {
		return err
	}
	if to != "" {
		if err := c.handOverFor(token, to); err != nil { // without opMu: it waits for that admin
			return err
		}
	}
	a.opMu.Lock()
	defer a.opMu.Unlock()
	if c, err = a.engine(conv); err != nil {
		return err
	}
	if err := c.leave(token); err != nil {
		return err
	}
	a.mu.Lock()
	delete(a.convs, conv)
	if a.configured == conv { // in memory only: a start with the key again enters it again, as PlanLeave says
		a.configured, a.cfg.ConversationKey, a.cfg.PinTokens = "", "", nil
	}
	a.mu.Unlock()
	err = os.RemoveAll(filepath.Join(a.root, conversationDir, conv))
	log.Print("left conversation ", conv)
	a.startWaiting()
	a.emitList()
	return err
}

// CancelLeave stops a Leave that waits for its handover; the conversation stays.
func (a *Conversations) CancelLeave(conv string) error {
	return call(a, conv, func(c *ChatService) error { c.cancelLeave(); return nil })
}

// startWaiting starts the conversations that did not start for -max-conversations, oldest first, the
// -conversation-key one before them, while there is room.
func (a *Conversations) startWaiting() {
	a.mu.Lock()
	var waiting []*convEntry
	for _, e := range a.convs {
		if errors.Is(e.err, ErrTooManyConversations) {
			waiting = append(waiting, e)
		}
	}
	room, configured := a.set.MaxConversations-(len(a.convs)-len(waiting)), a.configured
	a.mu.Unlock()
	slices.SortFunc(waiting, compareEntries)
	waiting = configuredFirst(waiting, func(e *convEntry) bool { return e.id == configured })
	for _, old := range waiting[:max(0, min(room, len(waiting)))] {
		e := &convEntry{id: old.id, dir: old.dir, local: old.local, joined: old.joined} // the list reads old meanwhile
		a.startEngine(e, a.engineConfig(e.dir, e.id == configured))
		a.add(e)
	}
}

// configuredFirst moves the -conversation-key conversation to the front: it is the one the user named.
func configuredFirst(entries []*convEntry, isConfigured func(*convEntry) bool) []*convEntry {
	if i := slices.IndexFunc(entries, isConfigured); i > 0 {
		e := entries[i]
		entries = append([]*convEntry{e}, slices.Delete(entries, i, i+1)...)
	}
	return entries
}

// List returns the conversations in the order they were added, never nil.
func (a *Conversations) List() []ConversationInfo {
	a.mu.Lock()
	entries := make([]convEntry, 0, len(a.convs))
	for _, e := range a.convs {
		entries = append(entries, *e)
	}
	a.mu.Unlock()
	slices.SortFunc(entries, func(x, y convEntry) int { return compareEntries(&x, &y) })
	out := make([]ConversationInfo, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.info())
	}
	return out
}

// Send, Members and the methods below are the engine's, for the conversation conv.

// Send keeps the message in the conversation's log, whatever the network does, and the peers get it when they are
// connected. A conversation that has not started is asked to start first, and Send waits for it.
func (a *Conversations) Send(conv, room, text string) error {
	c, err := a.await(conv, startWait, true)
	if err != nil {
		return err
	}
	if err := c.Send(room, text); err != nil {
		return err
	}
	a.mu.Lock()
	e := a.convs[conv]
	a.mu.Unlock()
	if e != nil {
		a.touch(e)
	}
	return nil
}

func (a *Conversations) Members(conv string) ([]Member, error) {
	return get(a, conv, (*ChatService).Members)
}

func (a *Conversations) Rooms(conv string) ([]string, error) {
	return get(a, conv, (*ChatService).Rooms)
}

func (a *Conversations) IsAdmin(conv string) (bool, error) {
	return get(a, conv, (*ChatService).IsAdmin)
}

func (a *Conversations) SetProfile(conv, name, avatar string) error {
	return call(a, conv, func(c *ChatService) error { return c.SetProfile(name, avatar) })
}

func (a *Conversations) AddRoom(conv, name string) error {
	return call(a, conv, func(c *ChatService) error { return c.AddRoom(name) })
}

func (a *Conversations) RemoveMember(conv, key string) error {
	return call(a, conv, func(c *ChatService) error { return c.RemoveMember(key) })
}

func (a *Conversations) CreateInvite(conv string, opts InviteOptions) (InviteInfo, error) {
	c, err := a.await(conv, startWait, true)
	if err != nil {
		return InviteInfo{}, err
	}
	return c.CreateInvite(opts)
}

func (a *Conversations) Invites(conv string) ([]InviteInfo, error) {
	c, err := a.await(conv, startWait, true)
	if err != nil {
		return nil, err
	}
	return c.Invites()
}

func (a *Conversations) Revoke(conv, id string) error {
	return call(a, conv, func(c *ChatService) error { return c.Revoke(id) })
}

func (a *Conversations) Approve(conv string, request uint64, admin bool) error {
	return call(a, conv, func(c *ChatService) error { return c.Approve(request, admin) })
}

func (a *Conversations) Deny(conv string, request uint64) error {
	return call(a, conv, func(c *ChatService) error { return c.Deny(request) })
}

// ServiceShutdown stops every conversation and the relay checks. Wails calls it when the app quits.
func (a *Conversations) ServiceShutdown() error {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	return a.shutdown()
}

func (a *Conversations) shutdown() error {
	a.mu.Lock()
	engines := a.running()
	a.started, a.convs = false, nil
	a.mu.Unlock()
	if a.cancel != nil {
		a.cancel()
	}
	errs := make([]error, len(engines))
	var wg sync.WaitGroup
	for i, c := range engines {
		wg.Go(func() { errs[i] = c.ServiceShutdown() })
	}
	wg.Wait()
	a.wg.Wait()
	return errors.Join(errs...)
}

// engine returns the running engine of conv, or ErrStarting while the conversation waits for its turn to start. Only
// IDs the app listed or made are in the map, so no path is ever built from what the window sends.
func (a *Conversations) engine(conv string) (*ChatService, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch e := a.convs[conv]; {
	case e != nil && e.chat != nil:
		return e.chat, nil
	case e != nil && e.ready != nil && e.err == nil && a.started:
		return nil, ErrStarting
	}
	return nil, ErrUnknownConversation
}

// await is engine for a conversation that has not started: it waits for the engine, for as long as limit (0 is until
// the app stops), and with focus it asks for the conversation to start before the others, since the user is using it.
func (a *Conversations) await(conv string, limit time.Duration, focus bool) (*ChatService, error) {
	c, err := a.engine(conv)
	if !errors.Is(err, ErrStarting) {
		return c, err
	}
	if focus {
		a.Focus(conv)
	}
	a.mu.Lock()
	ready := a.convs[conv].ready
	a.mu.Unlock()
	var timeout <-chan time.Time
	if limit > 0 {
		t := time.NewTimer(limit)
		defer t.Stop()
		timeout = t.C
	}
	select {
	case <-ready:
	case <-timeout:
		return nil, ErrStarting
	case <-a.ctx.Done():
		return nil, ErrUnknownConversation
	}
	return a.engine(conv)
}

func call(a *Conversations, conv string, f func(*ChatService) error) error {
	c, err := a.await(conv, 0, false)
	if err != nil {
		return err
	}
	return f(c)
}

func get[T any](a *Conversations, conv string, f func(*ChatService) T) (T, error) {
	c, err := a.await(conv, 0, false)
	if err != nil {
		var zero T
		return zero, err
	}
	return f(c), nil
}

// running returns the engines that run. The caller holds mu.
func (a *Conversations) running() []*ChatService {
	var out []*ChatService
	for _, e := range a.convs {
		if e.chat != nil {
			out = append(out, e.chat)
		}
	}
	return out
}

func (a *Conversations) isStarted() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.started
}

// hasRoom says whether one more conversation may be added: the bound counts the join that waits and the ones that
// did not start, and a fixed -listen port holds one conversation only.
func (a *Conversations) hasRoom() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch n := len(a.convs); {
	case !a.started:
		return errNotStarted
	case n >= a.set.MaxConversations:
		return ErrTooManyConversations
	case n >= 1 && a.set.fixedPort():
		return &ConfigError{Setting: "listen", Err: errFixedPort}
	}
	return nil
}

func (a *Conversations) info() AppInfo {
	return AppInfo{Conversations: a.List(), MaxConversations: a.set.MaxConversations, HasPins: len(a.set.pins) > 0, Network: a.set.network(), Version: version}
}

func (a *Conversations) infoOf(e *convEntry) ConversationInfo {
	a.mu.Lock()
	snapshot := *e
	a.mu.Unlock()
	return snapshot.info()
}

func (a *Conversations) emitList() { a.emit("chat:conversations", a.List()) }

func (e convEntry) info() ConversationInfo {
	info := ConversationInfo{ID: e.id, Name: e.local.Name, Me: e.me, Joined: e.joined}
	switch {
	case e.chat != nil:
		info.Admin = e.chat.IsAdmin()
		info.State = cmp.Or(e.state, "connecting")
	case e.err == nil && e.ready != nil: // waits for its turn
		info.State = "connecting"
	}
	if e.err != nil {
		info.Error, info.Folder = e.err.Error(), e.dir
		if errors.Is(e.err, ErrTooManyConversations) { // nothing is wrong with the folder
			info.Folder = ""
		}
		if de := (*DataDirError)(nil); errors.As(e.err, &de) {
			info.Folder, info.Layout = de.Dir, de.Layout
		}
	}
	return info
}

// newFolder makes the folder of a new conversation, with a random ID that has nothing to do with its key.
func (a *Conversations) newFolder(name string) (*convEntry, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	id := hex.EncodeToString(b)
	e := &convEntry{id: id, dir: filepath.Join(a.root, conversationDir, id), local: localInfo{Name: name, Created: time.Now().Unix()}}
	if err := os.Mkdir(e.dir, 0o700); err != nil {
		return nil, err
	}
	if err := writeJSON(filepath.Join(e.dir, localFile), e.local); err != nil {
		os.RemoveAll(e.dir)
		return nil, err
	}
	return e, nil
}

// dropFolder removes the folder of a conversation that failed before it had a conversation.json: nothing in it is
// worth keeping.
func (a *Conversations) dropFolder(e *convEntry) {
	if _, err := os.Lstat(filepath.Join(e.dir, "conversation.json")); errors.Is(err, fs.ErrNotExist) {
		os.RemoveAll(e.dir)
	}
}

// openLayout checks the data folder's layout.json, or writes it in a new folder. A folder of 0.1.9 or older, or of
// another layout, is refused and nothing in it is changed: there is no migration.
func openLayout(root string) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	if err := privateDir(root); err != nil {
		return err
	}
	data, err := readSmall(filepath.Join(root, layoutFile))
	switch {
	case err == nil:
		var l struct {
			Layout int `json:"layout"`
		}
		if json.Unmarshal(data, &l) != nil || l.Layout < 2 { // no version wrote a layout below 2
			return &DataDirError{Dir: root, Layout: -1}
		}
		if l.Layout != layoutVersion {
			return &DataDirError{Dir: root, Layout: l.Layout}
		}
		if err := os.MkdirAll(filepath.Join(root, conversationDir), 0o700); err != nil { // deleted by hand: start empty
			return err
		}
		return privateDir(filepath.Join(root, conversationDir))
	case !errors.Is(err, fs.ErrNotExist): // a special file, a link, or one too large: no version wrote it
		return &DataDirError{Dir: root, Layout: -1}
	}
	for _, name := range oldLayoutNames {
		if _, err := os.Lstat(filepath.Join(root, name)); !errors.Is(err, fs.ErrNotExist) {
			return &DataDirError{Dir: root, Layout: 1}
		}
	}
	if err := os.MkdirAll(filepath.Join(root, conversationDir), 0o700); err != nil {
		return err
	}
	if err := privateDir(filepath.Join(root, conversationDir)); err != nil {
		return err
	}
	return writeJSON(filepath.Join(root, layoutFile), map[string]int{"layout": layoutVersion})
}

// privateDir refuses a folder that other users can open, who could list, rename or replace what the app keeps
// there. Windows has no such mode bits; there the folder's place in the user's profile protects it.
func privateDir(dir string) error {
	fi, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
		return &DataDirError{Dir: dir, Open: true}
	}
	return nil
}

// readSmall reads a regular file of at most maxSmallFile bytes.
func readSmall(path string) ([]byte, error) { return readFile(path, maxSmallFile) }

// readFile reads a regular file of at most limit bytes. The path is checked before the open, which does not wait as a
// FIFO's would, and the opened file again, so a link, a device or a FIFO is never read and a start never hangs on one.
func readFile(path string, limit int) ([]byte, error) {
	if fi, err := os.Lstat(path); err != nil {
		return nil, err
	} else if !fi.Mode().IsRegular() {
		return nil, errNotRegular
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return nil, errNotRegular
	}
	b, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err == nil && len(b) > limit {
		err = errNotRegular
	}
	return b, err
}

// joinFiles are what a join that did not finish leaves in its folder, and the temporary file of a write that a crash cut.
var joinFiles = []string{"identity.key", localFile, "store", "braid", "invites", "directory-log"}

// onlyJoinFiles refuses a folder without conversation.json that holds anything a join that did not finish never makes,
// such as members.json or pin tokens, or a kolo-id without the two stores its join makes with it: such a kolo-id may be
// on a member list. The engine then checks the stores.
func onlyJoinFiles(dir string) error {
	items, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	has := map[string]bool{}
	for _, it := range items {
		if strings.HasSuffix(it.Name(), tmpSuffix) && slices.Contains(joinFiles, strings.TrimSuffix(it.Name(), tmpSuffix)) {
			continue
		}
		if !slices.Contains(joinFiles, it.Name()) {
			return &DataDirError{Dir: dir}
		}
		has[it.Name()] = true
	}
	if has["identity.key"] && !(has["store"] && has["braid"]) {
		return &DataDirError{Dir: dir}
	}
	return nil
}

// listConversations lists the conversation folders, oldest first. A folder without a conversation.json is a join that
// did not finish, or a member's folder that lost the file, which its engine refuses.
func listConversations(root string) ([]*convEntry, error) {
	dir := filepath.Join(root, conversationDir)
	items, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var all []*convEntry
	for _, it := range items {
		if !it.IsDir() || !validID(it.Name()) {
			log.Printf("%s/%q: not a conversation, ignored", conversationDir, it.Name())
			continue
		}
		e := &convEntry{id: it.Name(), dir: filepath.Join(dir, it.Name())}
		e.local = loadLocal(e.dir)
		_, err := os.Lstat(filepath.Join(e.dir, "conversation.json"))
		e.joined = err == nil
		all = append(all, e)
	}
	slices.SortFunc(all, compareEntries)
	return all, nil
}

func compareEntries(x, y *convEntry) int {
	return cmp.Or(cmp.Compare(x.local.Created, y.local.Created), strings.Compare(x.id, y.id))
}

// loadLocal reads local.json. A damaged or missing one gives the name "Conversation", and the conversation opens.
func loadLocal(dir string) localInfo {
	var l localInfo
	data, err := readSmall(filepath.Join(dir, localFile))
	if err == nil {
		err = json.Unmarshal(data, &l)
	}
	if err != nil {
		return localInfo{Name: defaultConvName}
	}
	if l.Name, err = convName(l.Name, defaultConvName); err != nil {
		l.Name = defaultConvName
	}
	return l
}

// convName checks a conversation's name: plain text of 1 to maxConvName characters. An empty one is def, or refused
// when def is empty.
func convName(name, def string) (string, error) {
	if name = strings.TrimSpace(name); name == "" {
		name = def
	}
	if name == "" || utf8.RuneCountInString(name) > maxConvName || !plainText(name) {
		return "", fmt.Errorf("%w: a name has 1 to %d characters, with no control characters.", ErrInvalidName, maxConvName)
	}
	return name, nil
}

// validID says whether id is one the app makes: 32 lowercase hex characters.
func validID(id string) bool {
	return len(id) == 32 && strings.Trim(id, "0123456789abcdef") == ""
}

// writeJSON replaces the file at path with v, mode 0600, with writeFileAtomic.
func writeJSON(path string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data, 0o600)
}
