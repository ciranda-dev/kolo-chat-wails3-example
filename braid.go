package main

//lint:file-ignore ST1005 these errors are sentences the window shows as they are

// The braid of a conversation: the member list and the room list, which every member must end with alike.
//
// Each member writes its own strand, and kolo-braid merges them into one order that every peer computes the same way;
// applyState (state.go) turns that order into the room list, and the braid keeps the roster. The chat talks only to
// the keys on that roster (the accepted set): a hello that proves the conversation key and names our group starts the
// braid's sync, and only an accepted key is registered, served, pulled and shown. Removing a member is not
// revocation: it keeps the conversation key.

import (
	"cmp"
	"context"
	"encoding/hex"
	"errors"
	"log"
	"path/filepath"
	"slices"
	"strings"
	"time"

	kolobraid "github.com/ciranda-dev/kolo-braid"
	kolodht "github.com/ciranda-dev/kolo-dht"
	kolosecure "github.com/ciranda-dev/kolo-secure"
	kolostore "github.com/ciranda-dev/kolo-store"
	kolotransport "github.com/ciranda-dev/kolo-transport"
)

const (
	// braidWrite bounds every braid write: kolo-braid's ConfirmWait.
	braidWrite  = 10 * time.Second
	stateTick   = time.Second
	codeRestart = 2 // closes a connection so both braids start fresh on the next one
)

// groupConfig is fixed in the genesis: one settler, the founder; 64 members, kolo-braid's default; 1 KiB bodies, its
// least.
var groupConfig = kolobraid.GroupConfig{App: stateApp, Quorum: kolobraid.Majority, MaxMembers: maxMembers,
	MinSettlers: 1, MaxSettlers: 1, MaxBody: 1024}

var (
	ErrNotAdmin         = errors.New("Only an admin can do that.")
	ErrFull             = errors.New("This conversation has 64 members, the most it can have.")
	ErrWaitingForMember = errors.New("Waiting for another member to come online.")
	ErrCannotWrite      = errors.New("This copy of the conversation can no longer write: ask an admin to invite you again.")
	ErrSettler          = errors.New("The member who settles changes to members and rooms cannot be removed.")
	ErrUnknownMember    = errors.New("That person is not on the member list.")
	ErrHandoverStopped  = errors.New("The hand over stopped before the other admin took over: you are still in the conversation.")
	errNoBraid          = errors.New("the shared member list of this conversation is missing")
)

func (c *ChatService) braidOptions() kolobraid.Options {
	l := c.braidLimits
	l.MaxPullAhead = cmp.Or(l.MaxPullAhead, 128) // twice the least, so a settler's next pulse is always in reach
	return kolobraid.Options{Backend: c.braidDB, App: stateApp, Apply: applyState, Limits: &l}
}

// dhtConfig is the test's DHT config, or none, with 64 incoming streams per direction on every connection.
func (c *ChatService) dhtConfig() *kolodht.Config {
	var d kolodht.Config
	if c.dht != nil {
		d = *c.dht
	}
	var sec kolosecure.Config
	if d.Secure != nil {
		sec = *d.Secure
	}
	var tc kolotransport.Config
	if sec.Transport != nil {
		tc = *sec.Transport
	}
	tc.MaxIncomingStreams = maxStreams
	sec.Transport, d.Secure = &tc, &sec
	return &d
}

func openBraidStore(dir string) (kolostore.Backend, error) {
	return kolostore.OpenPebble(filepath.Join(dir, "braid"))
}

// openBraid opens the conversation's braid, or creates it for the person who starts the conversation. A joiner has
// it from Complete already.
func (c *ChatService) openBraid(founder bool) (err error) {
	if c.braid != nil {
		return nil
	}
	c.braid, err = kolobraid.Open(c.ctx, c.id, c.braidOptions())
	if errors.Is(err, kolobraid.ErrNotFound) {
		if !founder {
			return errNoBraid
		}
		c.braid, err = kolobraid.Create(c.ctx, c.id, groupConfig, c.braidOptions())
	}
	return err
}

// noBraidYet refuses a folder without conversation.json whose braid was completed, or whose store holds a copy of
// another member's log: a member's folder that lost the file, which must never be taken for an unfinished join.
// joinHeader refuses one whose own logs hold entries.
func (c *ChatService) noBraidYet() error {
	b, err := kolobraid.Open(c.ctx, c.id, c.braidOptions())
	if err == nil {
		b.Close()
		return &DataDirError{Dir: c.set.DataDir}
	}
	if !errors.Is(err, kolobraid.ErrNotFound) {
		return err
	}
	all, err := c.store.List()
	if err != nil {
		return err
	}
	own, err := ownLogs(c.store, c.id)
	if err != nil {
		return err
	}
	if len(all) > len(own) {
		return &DataDirError{Dir: c.set.DataDir}
	}
	return nil
}

// startBraid builds the accepted set before anything is pulled, and follows the braid's events.
func (c *ChatService) startBraid() {
	c.mu.Lock()
	c.group = c.braid.GroupID()
	c.pauses = map[string]*pause{}
	c.flags = loadFlags(c.set.DataDir)
	c.mu.Unlock()
	c.rebuild()
	c.wg.Go(c.stateLoop)
}

// stateLoop follows the braid's events and, at least every stateTick, tells the window the lists and the state. Every
// event rebuilds the accepted set from the whole roster, so a membership event dropped past MaxEvents changes nothing:
// a drop needs a full queue, and the events after it rebuild.
func (c *ChatService) stateLoop() {
	for {
		ctx, cancel := context.WithTimeout(c.ctx, stateTick)
		ev, err := c.braid.Next(ctx)
		cancel()
		if c.ctx.Err() != nil || errors.Is(err, kolobraid.ErrClosed) {
			return
		}
		if err == nil {
			c.rebuild()
			c.onBraidEvent(ev)
		}
		c.publishDirectory()
		c.emitState(c.braid.Status())
	}
}

func (c *ChatService) onBraidEvent(ev kolobraid.Event) {
	switch ev.Kind {
	case kolobraid.EventSettled:
		c.flagRejected()
	case kolobraid.EventHalted, kolobraid.EventDiverged, kolobraid.EventSettlementHalted, kolobraid.EventStrandLost:
		log.Printf("member list: event %d at %d", ev.Kind, ev.Position)
	}
}

// rebuild reads the roster from one snapshot of the view into the accepted set. Proven peers that are now accepted
// are registered, get a reply hello and a sync; registered peers that are not accepted any more are dropped.
func (c *ChatService) rebuild() {
	snap := c.braid.View()
	members := snap.Members()
	snap.Close()
	accepted := make(map[string]kolobraid.Member, len(members))
	for _, m := range members {
		accepted[hex.EncodeToString(m.Key)] = m
	}
	c.mu.Lock()
	c.accepted = accepted
	var join []*link
	dropped := false
	for key, l := range c.links {
		_, ok := accepted[key]
		switch {
		case ok && !l.registered && l.hello != nil:
			join = append(join, l)
		case !ok && l.registered:
			l.registered, dropped = false, true
			log.Print("not on the member list: ", key[:8])
		}
	}
	c.mu.Unlock()
	for _, l := range join {
		c.accept(l, *l.hello, true, true)
	}
	if dropped {
		c.emitPeers()
	}
}

// isAccepted says whether author, a hex key, is on the roster of the view.
func (c *ChatService) isAccepted(author string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.accepted[author]
	return ok
}

// IsAdmin says whether we are an admin: who may invite, approve, remove members and add rooms.
func (c *ChatService) IsAdmin() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.accepted[hex.EncodeToString(c.pub)]
	return ok && m.Roles&kolobraid.RoleAdmin != 0
}

// serveBraid syncs the braid with one peer on the connection's 'b' part. Only ErrNotMember closes the connection; a
// braid fault pauses the braid with that member and the chat goes on.
func (c *ChatService) serveBraid(l *link) {
	conn := l.p.Conn()
	for {
		if wait := c.pausedFor(l.key); wait > 0 {
			t := time.NewTimer(wait)
			select {
			case <-t.C:
			case <-conn.Context().Done():
				t.Stop()
				return
			case <-c.ctx.Done():
				t.Stop()
				return
			}
			c.unpause(l.key)
			conn.CloseWithError(codeRestart, "kolochat: braid starts fresh")
			return
		}
		part := l.mx.part(tagBraid)
		c.served(l.key)
		err := c.braid.ServeConn(c.ctx, part)
		part.Close()
		switch {
		case errors.Is(err, kolobraid.ErrNotMember): // unknown for 60 s: a joiner tries again, a removed member redials
			conn.CloseWithError(codeRestart, "kolochat: not a member")
			return
		case errors.Is(err, kolobraid.ErrProtocol), part.overflowed.Load():
			c.braidFault(l.key)
		default:
			return
		}
	}
}

// writeCtx bounds a braid write.
func (c *ChatService) writeCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.ctx, braidWrite)
}

// writeError turns an error of a braid write into the sentence the window shows.
func (c *ChatService) writeError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, kolobraid.ErrUnconfirmed):
		c.mu.Lock()
		c.unconfirmed = true
		c.mu.Unlock()
		return ErrWaitingForMember
	case errors.Is(err, kolobraid.ErrStrandLost):
		return ErrCannotWrite
	case errors.Is(err, kolobraid.ErrNotAdmin), errors.Is(err, kolobraid.ErrRemoved), errors.Is(err, kolobraid.ErrNotMember):
		return ErrNotAdmin
	}
	return err
}

// AddRoom adds a room to the room list, which admins write: anyone else gets ErrNotAdmin. A bad name is
// ErrInvalidRoom, and a room that exists (ASCII letters in any case) is ErrRoomExists. Two admins who add one name at
// once end with one room.
func (c *ChatService) AddRoom(name string) error {
	name = strings.TrimSpace(name)
	if !c.isActive() {
		return ErrNotInConversation
	}
	if !c.IsAdmin() {
		return ErrNotAdmin
	}
	if err := validRoomName(name); err != nil {
		return err
	}
	rooms := c.Rooms()
	if slices.ContainsFunc(rooms, func(r string) bool { return sameRoom(r, name) }) {
		return ErrRoomExists
	}
	if len(rooms) > maxRooms {
		return ErrTooManyRooms
	}
	ctx, cancel := c.writeCtx()
	defer cancel()
	_, err := c.braid.Append(ctx, encodeAddRoom(name))
	return c.writeError(err)
}

// RemoveMember takes a member off the member list. Each member's app stops talking to it once it learns of this; the
// member keeps the conversation key, so this is not revocation. The settler cannot be removed.
func (c *ChatService) RemoveMember(key string) error {
	if !c.isActive() {
		return ErrNotInConversation
	}
	if !c.IsAdmin() {
		return ErrNotAdmin
	}
	c.mu.Lock()
	m, ok := c.accepted[key]
	c.mu.Unlock()
	switch {
	case !ok:
		return ErrUnknownMember
	case m.Seat:
		return ErrSettler
	}
	ctx, cancel := c.writeCtx()
	defer cancel()
	_, err := c.braid.RemoveMember(ctx, m.Key)
	if err == nil {
		log.Print("member removed: ", key[:8])
	}
	return c.writeError(err)
}

// Rooms lists the rooms of the conversation: the default room, then the ones admins added, by name.
func (c *ChatService) Rooms() []string {
	rooms := []string{defaultRoom}
	if c.braid == nil {
		return rooms
	}
	snap := c.braid.View()
	defer snap.Close()
	return append(rooms, roomsOf(snap)...)
}

// Members lists the member list of the conversation with their profiles: the settler first, then the admins, then
// the others by key. Pending says the change that added it is not settled yet. A joiner whose braid has not synced
// yet lists itself.
func (c *ChatService) Members() []Member {
	if c.braid == nil {
		return []Member{} // "chat:profiles" sends it as is, and a nil slice would be null
	}
	view, settled := c.braid.View(), c.braid.Settled()
	roster, done := view.Members(), settled.Members()
	view.Close()
	settled.Close()
	me := hex.EncodeToString(c.pub)
	out := make([]Member, 0, len(roster)+1)
	for _, m := range roster {
		key := hex.EncodeToString(m.Key)
		p := c.profileOf(key)
		out = append(out, Member{Key: key, Name: p.Name, Avatar: p.Avatar, Admin: m.Roles&kolobraid.RoleAdmin != 0, Seat: m.Seat,
			Pending: !slices.ContainsFunc(done, func(d kolobraid.Member) bool { return d.Key.Equal(m.Key) }), Flagged: c.flagged(key), Mismatch: c.mismatched(key)})
	}
	if !slices.ContainsFunc(out, func(m Member) bool { return m.Key == me }) {
		p := c.profileOf(me)
		out = append(out, Member{Key: me, Name: p.Name, Avatar: p.Avatar, Pending: true})
	}
	slices.SortStableFunc(out, func(a, b Member) int {
		rank := func(m Member) int {
			switch {
			case m.Seat:
				return 0
			case m.Admin:
				return 1
			}
			return 2
		}
		return cmp.Or(cmp.Compare(rank(a), rank(b)), strings.Compare(a.Key, b.Key))
	})
	return out
}
