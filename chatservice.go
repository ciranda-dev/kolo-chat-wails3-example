package main

import (
	"bytes"
	"cmp"
	"context"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	kolobraid "github.com/ciranda-dev/kolo-braid"
	kolodht "github.com/ciranda-dev/kolo-dht"
	koloid "github.com/ciranda-dev/kolo-id"
	invite "github.com/ciranda-dev/kolo-invite"
	kolokv "github.com/ciranda-dev/kolo-kv"
	kololog "github.com/ciranda-dev/kolo-log"
	pin "github.com/ciranda-dev/kolo-pin"
	kolostore "github.com/ciranda-dev/kolo-store"
	koloswarm "github.com/ciranda-dev/kolo-swarm"
	kolosync "github.com/ciranda-dev/kolo-sync"
	"golang.org/x/crypto/chacha20poly1305"
)

const (
	maxMessage  = 64 * 1024
	maxWire     = 128 * 1024 // a message as JSON, with its sealed text in base64
	sendTimeout = 10 * time.Second
	codeTooLong = 1
	codeNoHello = 4 // closes a connection that shares only the conversation's topic and sent no hello
	// maxHistory is the most messages "chat:history" holds: 256, so its text is at most the bytes one pin
	// keeps for one member by default.
	maxHistory = defaultPinQuota / maxMessage
)

// ChatMsg is the payload of the "chat:msg" event, and each item of "chat:history". From is the sender's kolo-id
// (hex public key), Room is the room it was sent to and Via says how the message arrived: "direct" from the
// sender, "pin" when its ciphertext came from the pin, "peer" when it was pulled from the sender after the direct
// delivery was missed, "history" when it was shown before a restart. Mine is true for our own messages, which
// only the history holds. Time is the sender's clock in Unix milliseconds, 0 for a message that has none.
type ChatMsg struct {
	From string `json:"from"`
	Room string `json:"room"`
	Text string `json:"text"`
	Via  string `json:"via"`
	Mine bool   `json:"mine,omitempty"`
	Time int64  `json:"time,omitempty"`
}

// PinStatus is the payload of the "chat:pin" event. Error is the sentence to show when no pin can be used, and
// Warning the one to show when some cannot but others keep the messages. Locked counts the messages, from a pin or a
// member, that cannot be opened yet because storing their keys failed: the next pull tries again. Pins has the
// state of each pin, in the order of the configuration.
type PinStatus struct {
	Error   string     `json:"error"`
	Warning string     `json:"warning"`
	Locked  int        `json:"locked"`
	Pins    []PinState `json:"pins"`
}

// PinState is one pin in PinStatus: its address, its public key in hex, and the sentence about its last push
// and about the pull at start, "" when that worked or has not run yet.
type PinState struct {
	Addr string `json:"addr"`
	Key  string `json:"key"`
	Push string `json:"push"`
	Pull string `json:"pull"`
}

// wireMsg is what peers send each other on the direct connection, one unidirectional stream each.
type wireMsg struct {
	Kind      string `json:"kind"`                // "hello", "msg" or "dir"
	Header    []byte `json:"header,omitempty"`    // hello: the sender's signed log header
	DirHeader []byte `json:"dirHeader,omitempty"` // hello: the signed header of the sender's directory log
	Proof     []byte `json:"proof,omitempty"`     // hello: proves the sender knows the conversation key
	Reply     bool   `json:"reply,omitempty"`     // hello: answers a hello, so the sender has registered us
	Group     []byte `json:"group,omitempty"`     // hello: the ID of the sender's braid, which must be ours
	Index     uint64 `json:"index,omitempty"`     // msg: the entry's index in the sender's log
	Sealed    []byte `json:"sealed,omitempty"`    // msg: the entry's value, the text sealed with the conversation key
	// "dir" has no fields: the sender's directory changed, pull it.
}

// member is a peer of the conversation: its signed log header, which names its public key and log ID, the signed
// header of its directory log (see profile.go), and how many of its messages the window has shown. Past holds the
// headers of the logs it wrote before it left and joined again, for the history. Refused and DirRefused are the
// entries of its two logs whose keys their signed records prove wrong, and Quarantined says it had too many (see
// entrykeys.go). Mismatched holds the indexes of its entries whose text differed from the message it sent directly at
// that index. All are kept in the data directory.
type member struct {
	Header      []byte   `json:"header"`
	DirHeader   []byte   `json:"dirHeader,omitempty"`
	Shown       uint64   `json:"shown"`
	Past        [][]byte `json:"past,omitempty"`
	Refused     []uint64 `json:"refused,omitempty"`
	DirRefused  []uint64 `json:"dirRefused,omitempty"`
	Quarantined bool     `json:"quarantined,omitempty"`
	Mismatched  []uint64 `json:"mismatched,omitempty"`
}

// maxPastLogs bounds member.Past: a member that left and joined again more often loses its oldest messages from
// the history.
const maxPastLogs = 4

type pinState struct {
	Push, Pull string // what to tell the user, "" when it works
}

// ChatService is the engine of one conversation: its own kolo-id, swarm, logs, pins and invites in its own folder.
// Conversations runs one per conversation and routes the window's calls to it.
type ChatService struct {
	cfg  Config
	emit func(name string, data any)

	startMu sync.Mutex
	set     settings
	id      *koloid.Identity
	pub     ed25519.PublicKey
	aead    cipher.AEAD
	secret  *kololog.EntrySecret // derives the entry keys of every log (see entrykeys.go)
	joinHdr []byte               // a joiner's messages log header, sent with its request
	tokens  [][]byte             // one per pin, in the order of c.set.pins
	store   *kolostore.Store
	log     *kolostore.Log // our own log of messages, one of the two we write
	dirLog  *kolostore.Log // our directory log, the other one (see profile.go)
	dir     *kolokv.DB     // the key-value store on dirLog: the only writer of it
	srv     *kolosync.Server
	sw      *koloswarm.Swarm
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	push    chan struct{}     // a push to the pin is wanted
	pinDone chan struct{}     // closed when the pull from the pin at start is over
	invDB   kolostore.Backend // the invites' own store, used for nothing else
	inv     *invite.Inviter
	dirMu   sync.Mutex // one write of our directory at a time
	pubMu   sync.Mutex // one publication of the directory to the window at a time

	mu          sync.Mutex       // guards the fields below
	links       map[string]*link // the connections of the conversation's topic, by peer key
	members     map[string]*member
	replicas    map[string]*kolostore.Replica
	dirs        map[string]*directory               // per member: the read-only copy of its directory
	lastView    directoryView                       // what the window was told last
	pinned      map[string]uint64                   // per author: entries below this index came from the pin
	locked      map[string]int                      // per author: pulled entries that cannot be opened yet
	stalled     map[kololog.LogID]bool              // logs whose last unlock could not store its keys
	syncing     map[string]bool                     // per author: a sync after a direct message waits to start
	pins        []pinState                          // one per pin, in the order of c.set.pins
	active      bool                                // in a conversation: it has its key and its pin
	joining     *joinAttempt                        // the Join that waits for its inviter
	cancelJoin  context.CancelFunc                  // stops that Join
	requests    map[invite.RequestID]pendingRequest // the requests that wait for Approve or Deny
	leaveToken  string                              // the token of the latest PlanLeave, "" when none
	relays      []RelayState                        // one per connection relay, in the order of c.set.relays
	relaySeq    uint64                              // the Seq of the last "chat:relays" event
	accepted    map[string]kolobraid.Member         // the roster of the braid's view, by hex key: who the chat talks to
	direct      map[string]map[uint64][32]byte      // by author and index: the hash of a message shown directly, until its log entry is checked against it
	group       kolobraid.GroupID
	pauses      map[string]*pause // members whose braid is paused after a fault
	flags       flagList
	unconfirmed bool               // a braid write ended before our strand was confirmed
	lastState   []byte             // the last "chat:state", as JSON
	leaveStop   context.CancelFunc // stops a founder's Leave that waits for its handover
	netSet      settings           // the settings as start read them: the retries of the network read this copy, while enter changes c.set
	net         netView            // what the room knows of its network (see netstate.go)
	graceTimer  *time.Timer        // ends "reconnecting" some time after the last peer dropped

	online      chan struct{} // closed when the room reaches the network for the first time
	onceOnline  sync.Once
	settled     chan struct{} // closed when the engine's first work on the network is over (see settle)
	onceSettled sync.Once
	lostSig     chan struct{} // the last registered peer dropped

	showMu sync.Mutex // one message is shown at a time, so they keep their order
	pullMu sync.Mutex // one pull at a time, since a replica takes one
	pushMu sync.Mutex // one push at a time

	braidDB kolostore.Backend // the braid's own store, so a join attempt never shares one
	braid   *kolobraid.Braid

	dht         *kolodht.Config                              // nil in the app; tests set it to play a firewall
	allow       func(ed25519.PublicKey, netip.AddrPort) bool // nil in the app; tests set it to cut the network
	tune        pace                                         // zero in the app, which uses the constants of netstate.go; tests shorten them
	gate        *gate                                        // nil for one engine alone; Conversations shares one, so many engines start gently
	pauseFirst  time.Duration                                // 0 in the app; tests shorten the pause after a braid fault
	braidLimits kolobraid.Limits                             // zero in the app; tests lower MaxEvents
}

// link is one connection to a peer of the conversation, split into parts by its mux. Its hello is proven once it
// proves the conversation key and names our braid; the peer is registered once it is also on the roster.
type link struct {
	p      *koloswarm.Peer
	mx     *mux
	key    string
	once   sync.Once
	proven chan struct{}

	// guarded by ChatService.mu
	hello      *wireMsg  // its proven hello, kept to register it once it is accepted
	since      time.Time // when its hello was proven
	answered   bool      // it sent a reply hello: it accepted us
	otherGroup bool      // its hello names another braid
	registered bool
}

// NewChatService returns a chat that reads cfg when it starts and tells the window what happens through emit.
func NewChatService(cfg Config, emit func(name string, data any)) *ChatService {
	return &ChatService{cfg: cfg, emit: emit, online: make(chan struct{}), settled: make(chan struct{}), lostSig: make(chan struct{}, 1)}
}

// settle says that the engine's first work on the network is over: it joined the conversation's topic, found no
// network, or has none to join. Conversations lets the next engine start then.
func (c *ChatService) settle() { c.onceSettled.Do(func() { close(c.settled) }) }

// Start joins the conversation and returns this user's kolo-id (hex public key). It opens the data directory,
// the log and the pin, so it fails with a *ConfigError for a bad setting and with the errors of kolo-pin
// (pin.ErrExpired, pin.ErrUnauthorized) for a bad token. A pin that cannot be reached is not an error: the chat
// works directly and tells the window.
func (c *ChatService) Start() (string, error) {
	c.startMu.Lock()
	defer c.startMu.Unlock()
	if c.id != nil {
		return c.id.PublicKeyString(), nil
	}
	if err := c.start(); err != nil {
		c.shutdown()
		return "", err
	}
	return c.id.PublicKeyString(), nil
}

func (c *ChatService) start() (err error) {
	if c.set, err = c.cfg.resolve(); err != nil {
		return err
	}
	if err = os.MkdirAll(c.set.DataDir, 0o700); err != nil {
		return err
	}
	if c.id, err = loadIdentity(c.set.DataDir); err != nil {
		return err
	}
	c.pub = c.id.PublicKey()
	backend, err := kolostore.OpenPebble(filepath.Join(c.set.DataDir, "store"))
	if err != nil {
		return err
	}
	c.store = kolostore.New(backend)
	if c.braidDB, err = openBraidStore(c.set.DataDir); err != nil {
		return err
	}
	c.links = map[string]*link{}
	c.replicas = map[string]*kolostore.Replica{}
	c.dirs = map[string]*directory{}
	c.pinned = map[string]uint64{}
	c.direct = map[string]map[uint64][32]byte{}
	c.locked = map[string]int{}
	c.stalled = map[kololog.LogID]bool{}
	c.syncing = map[string]bool{}
	if c.members, err = loadMembers(c.set.DataDir); err != nil {
		return err
	}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	c.push = make(chan struct{}, 1)
	c.resetRelays()
	// With relays, a peer that cannot be reached directly is reached through one: its Conn works the same.
	swarmCfg := &koloswarm.Config{LookupInterval: c.set.LookupInterval, MaxTopics: swarmTopics, Relays: c.set.relays, DHT: c.dhtConfig(),
		BootstrapNodes: c.set.bootstrap, RetryMax: dialRetryMax, Allow: c.allow}
	if c.sw, err = koloswarm.New(c.ctx, c.set.Listen, c.id, swarmCfg); err != nil {
		return err
	}
	// No answer from the entry nodes is not a failed start: the room works from its own logs and asks again (netLoop).
	c.netSet = c.set
	reached := true
	if err = c.reach(); err != nil {
		var ne *NetworkError
		if !errors.As(err, &ne) {
			return err
		}
		log.Print(err)
		reached = false
		c.settle()
	}
	log.Print("kolo-id ", c.id.PublicKeyString())

	c.wg.Go(func() { c.netLoop(reached) })
	c.wg.Go(c.swarmLoop)
	if err = c.openInvites(); err != nil {
		return err
	}
	_, err = os.Lstat(filepath.Join(c.set.DataDir, "conversation.json"))
	founder := errors.Is(err, fs.ErrNotExist) // a key from the configuration and no conversation yet: we start it
	conv, found, err := c.savedConversation() // from the configuration, or from the invite that made us a member
	if err != nil {
		return err
	}
	if !found { // a joiner waits for Join
		if err = c.noBraidYet(); err != nil {
			return err
		}
		c.joinHdr, err = c.joinHeader()
		c.settle()
		return err
	}
	return c.enter(conv, founder)
}

// enter makes this chat a member of the conversation: it has the key and the pin, opens and offers its logs and the
// braid, pulls from the pin first and then from the peers, and joins the conversation's topic. Start calls it for
// the founder or a member that joined earlier, and Join for a new member. The conversation is saved once our logs
// and the braid open: the founder creates the braid.
func (c *ChatService) enter(conv conversation, founder bool) (err error) {
	c.set.useConversation(conv)
	if c.aead, err = chacha20poly1305.NewX(c.set.conversationKey[:]); err != nil {
		return err
	}
	if c.secret, err = entrySecret(c.set.conversationKey[:]); err != nil {
		return err
	}
	if c.log, c.dirLog, err = openOwnLogs(c.store, c.id, c.set.DataDir, c.secret); err != nil {
		return ownLogError(err, c.set.DataDir, false)
	}
	if c.dir, err = kolokv.Open(c.dirLog); err != nil { // from now on only the DB writes to the log
		return err
	}
	if err = c.openBraid(founder); err != nil {
		return err
	}
	if err = saveConversation(c.set.DataDir, conv); err != nil {
		return err
	}
	if c.tokens, err = capabilities(c.set, c.id, c.log.ID(), c.dirLog.ID()); err != nil {
		return err
	}
	c.replayHistory() // before active: nothing else shows or sends yet
	c.srv = kolosync.NewServer(c.authorize)
	for _, l := range []*kolostore.Log{c.log, c.dirLog} {
		if err = c.srv.Offer(l); err != nil {
			return err
		}
	}
	c.pinDone = make(chan struct{})
	c.mu.Lock()
	c.pins = make([]pinState, len(c.set.pins))
	c.active = true // what the goroutines below read was written before this
	c.mu.Unlock()
	c.startBraid() // the accepted set, before anything is pulled
	c.openDirectories()

	c.wg.Go(c.pushLoop)
	c.wg.Go(c.pullAtStart)
	c.pushSoon()
	c.wg.Go(func() { // join the conversation's topic, as soon as an entry node answers, and again if the network goes
		defer c.settle()
		if !c.waitOnline() {
			return
		}
		c.untilDone("join", func() error {
			return c.sw.Join(c.ctx, c.topic(), &koloswarm.JoinOptions{Announce: true, Lookup: true})
		})
	})
	return nil
}

// pullAtStart pulls from the pin on open, before the peers are pulled from, and again with a growing wait while the pin
// is unreachable.
func (c *ChatService) pullAtStart() {
	p := c.tune.filled()
	err := c.pullFromPin(c.ctx)
	close(c.pinDone)
	for attempt := 1; err != nil; attempt++ {
		log.Print("pin pull: ", err)
		if !errors.Is(err, pin.ErrUnreachable) || !sleep(c.ctx, retryDelay(attempt, p.pinRetryMin, p.pinRetryMax)) {
			return
		}
		err = c.pullFromPin(c.ctx)
	}
}

// ServiceShutdown stops the chat and closes the store. Conversations calls it when the app quits.
func (c *ChatService) ServiceShutdown() error {
	c.startMu.Lock()
	defer c.startMu.Unlock()
	return c.shutdown()
}

func (c *ChatService) shutdown() error {
	if c.cancel != nil {
		c.cancel()
	}
	c.settle()
	var errs []error
	if c.sw != nil {
		errs = append(errs, c.sw.Close())
	}
	if c.inv != nil {
		errs = append(errs, c.inv.Close())
	}
	c.wg.Wait()
	if c.braid != nil {
		errs = append(errs, c.braid.Close())
	}
	if c.braidDB != nil {
		errs = append(errs, c.braidDB.Close())
	}
	c.closeDirectories()
	if c.store != nil {
		errs = append(errs, c.store.Close()) // also closes the log and the replicas
	}
	if c.invDB != nil {
		errs = append(errs, c.invDB.Close())
	}
	c.mu.Lock()
	c.active, c.joining, c.cancelJoin = false, nil, nil
	if c.graceTimer != nil {
		c.graceTimer.Stop()
	}
	c.mu.Unlock()
	c.id, c.store, c.sw, c.cancel, c.inv, c.invDB, c.braid, c.braidDB = nil, nil, nil, nil, nil, nil, nil, nil
	c.log, c.dirLog, c.dir, c.secret, c.joinHdr = nil, nil, nil, nil, nil
	return errors.Join(errs...)
}

// topic is where the conversation's peers find each other: a hash of the conversation key, so only those who
// know the key look at it.
func (c *ChatService) topic() koloswarm.Topic {
	return sha256.Sum256(append([]byte("kolochat/topic/v1"), c.set.conversationKey[:]...))
}

// swarmLoop handles every peer the swarm connects.
func (c *ChatService) swarmLoop() {
	for {
		ev, err := c.sw.Next(c.ctx)
		if err != nil {
			if c.ctx.Err() == nil {
				log.Print("swarm: ", err)
			}
			return
		}
		switch {
		case ev.Kind == koloswarm.Connected && ev.Peer != nil:
			c.route(ev.Peer)
		case ev.Kind == koloswarm.RelayChanged:
			c.relayChanged(ev)
		}
	}
}

// route decides what a connection is for, since one protocol serves it: the inviter that a Join waits for, a
// peer of the conversation (chat and sync), or a person that has only an invite's topic with us, whose
// requests the inviter answers.
func (c *ChatService) route(p *koloswarm.Peer) {
	switch {
	case c.offerToJoin(p):
	case !c.isActive():
	case slices.Contains(p.Topics(), c.topic()):
		c.addPeer(p)
	default:
		c.wg.Go(func() { c.inv.ServePeer(c.ctx, p) })
	}
}

// addPeer starts the chat with a peer of the conversation's topic: our hello first, then what it sends.
func (c *ChatService) addPeer(p *koloswarm.Peer) {
	l := &link{p: p, mx: newMux(p.Conn()), key: hex.EncodeToString(p.PublicKey()), proven: make(chan struct{})}
	c.mu.Lock()
	c.links[l.key] = l
	c.mu.Unlock()
	log.Print("peer connected: ", l.key[:8])

	c.wg.Go(func() { c.serveAfterHello(l) })
	c.wg.Go(l.mx.acceptUni)
	c.wg.Go(func() {
		defer func() {
			c.mu.Lock()
			registered := l.registered
			if c.links[l.key] == l {
				delete(c.links, l.key)
			}
			c.mu.Unlock()
			log.Print("peer gone: ", l.key[:8])
			c.emitPeers()
			if registered {
				c.peerLeft()
			}
		}()
		if err := c.sendTo(l, c.hello(l, false)); err != nil {
			log.Print("hello: ", err)
		}
		c.receive(l)
	})
}

// serveAfterHello serves our logs and the braid to the peer once its hello is proven: it knows the conversation key
// and names our braid. Who is not on the roster yet still gets the braid, so a joiner can sync its own admission. A
// peer that sends no hello in sendTimeout is someone who joins with an invite: one who left keeps its announcement of
// the topic for up to 20 minutes (kolo-dht's record lifetime), so the topic alone does not tell.
func (c *ChatService) serveAfterHello(l *link) {
	timer := time.NewTimer(sendTimeout)
	defer timer.Stop()
	select {
	case <-l.proven:
		c.wg.Go(l.mx.acceptBidi)
		c.wg.Go(func() { c.serveBraid(l) })
		c.srv.Serve(c.ctx, l.mx.part(tagSync))
	case <-timer.C:
		c.mu.Lock()
		if c.links[l.key] == l {
			delete(c.links, l.key)
		}
		c.mu.Unlock()
		if len(l.p.Topics()) == 1 { // the conversation's topic only: no invite to serve, so the topic alone holds nothing
			l.p.Conn().CloseWithError(codeNoHello, "kolochat: no hello")
			return
		}
		c.inv.ServePeer(c.ctx, l.p)
	case <-c.ctx.Done():
	}
}

// receive handles what one peer sends on the connection's 'm' part, one stream at a time.
func (c *ChatService) receive(l *link) {
	part := l.mx.part(tagMsg)
	for {
		st, err := part.AcceptUniStream(c.ctx)
		if err != nil {
			return
		}
		st.SetReadDeadline(time.Now().Add(sendTimeout))
		data, err := io.ReadAll(io.LimitReader(st, maxWire+1))
		if err != nil || len(data) > maxWire {
			st.CancelRead(codeTooLong)
			continue
		}
		var m wireMsg
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		switch m.Kind {
		case "hello":
			c.onHello(l, m)
		case "msg":
			c.onMessage(l, m)
		case "dir":
			c.onDirectory(l)
		}
	}
}

func (c *ChatService) sendTo(l *link, m wireMsg) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(c.ctx, sendTimeout)
	defer cancel()
	st, err := l.mx.part(tagMsg).OpenUniStream(ctx)
	if err != nil {
		return err
	}
	st.SetWriteDeadline(time.Now().Add(sendTimeout))
	if _, err := st.Write(data); err != nil {
		st.CancelWrite(codeTooLong)
		return err
	}
	return st.Close()
}

// registered lists the peers the chat talks to: proven, and on the roster.
func (c *ChatService) registered() []*link {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []*link
	for _, l := range c.links {
		if l.registered {
			out = append(out, l)
		}
	}
	return out
}

// Send seals the text, with the room it is for, with the conversation key and appends it to our log. Then it
// hands it to every peer that is connected, and to the pin for those who are not. A pin that fails never fails
// the Send. The room must be one of Rooms, or Send returns ErrUnknownRoom.
func (c *ChatService) Send(room, text string) error {
	if len(text) > maxMessage {
		return errors.New("message too long")
	}
	if !c.isActive() {
		return ErrNotInConversation
	}
	if !slices.Contains(c.Rooms(), room) {
		return ErrUnknownRoom
	}
	sealed, err := c.seal(room, text)
	if err != nil {
		return err
	}
	index, err := c.log.Append(sealed)
	if err != nil {
		return err
	}
	// The message is in our log, so it reaches every peer in the end: a peer that is connected gets it now, one that is
	// not pulls it when it connects. A link that has died without our knowing must not hold the window, so each
	// peer gets its own goroutine.
	m := wireMsg{Kind: "msg", Index: index, Sealed: sealed}
	for _, l := range c.registered() {
		go func() {
			if err := c.sendTo(l, m); err != nil && c.ctx.Err() == nil {
				log.Print("direct send: ", err) // the pin and the next sync still carry it
			}
		}()
	}
	c.pushSoon()
	return nil
}

// body is what a message holds before it is sealed: the room, the text and the time on the sender's clock, in Unix
// milliseconds. The room is inside the sealed bytes, so the pin cannot tell which room a message is for. The time is only as true as the sender's clock, and
// 0 in a message from 0.1.2 or before.
type body struct {
	Version int    `json:"v"`
	Room    string `json:"room"`
	Text    string `json:"text"`
	Time    int64  `json:"t,omitempty"`
}

// seal and open protect the room and the text with the conversation key (XChaCha20-Poly1305), bound to the
// author's key. Direct messages need it, since they do not go through a log. open takes an entry that is not a
// body, such as one written by step 4, for text in the default room.
func (c *ChatService) seal(room, text string) ([]byte, error) {
	plain, err := json.Marshal(body{Version: 1, Room: room, Text: text, Time: time.Now().UnixMilli()})
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return c.aead.Seal(nonce, nonce, plain, c.pub), nil
}

func (c *ChatService) open(author ed25519.PublicKey, sealed []byte) (body, error) {
	n := c.aead.NonceSize()
	if len(sealed) < n {
		return body{}, errors.New("sealed text too short")
	}
	plain, err := c.aead.Open(nil, sealed[:n], sealed[n:], author)
	if err != nil {
		return body{}, err
	}
	var b body
	if json.Unmarshal(plain, &b) != nil || b.Version != 1 {
		return body{Room: defaultRoom, Text: string(plain)}, nil
	}
	if validRoomName(b.Room) != nil {
		return body{}, errors.New("message for a room with a bad name")
	}
	return b, nil
}

// hello introduces us: our signed log header, which names our key and log ID, a proof that we know the conversation
// key, bound to both keys so it cannot be replayed to another peer, and our braid's ID. The directory header is how a
// peer learns which log holds our profile.
func (c *ChatService) hello(l *link, reply bool) wireMsg {
	return wireMsg{Kind: "hello", Header: c.log.Header().Encode(), DirHeader: c.dirLog.Header().Encode(),
		Proof: c.proof(c.pub, l.p.PublicKey()), Reply: reply, Group: c.group[:]}
}

func (c *ChatService) proof(from, to ed25519.PublicKey) []byte {
	mac := hmac.New(sha256.New, c.set.conversationKey[:])
	mac.Write([]byte("kolochat/hello/v1"))
	mac.Write(from)
	mac.Write(to)
	return mac.Sum(nil)
}

// onHello proves a peer's hello: it knows the conversation key and names our braid. A proven peer that is on the
// roster is registered and answered with our own hello; one that is not yet is kept, and accepted once the roster
// has it. The answer tells the peer that we registered it, so it may pull our log; a hello that is an answer tells
// us the same, so we may pull its log.
func (c *ChatService) onHello(l *link, m wireMsg) {
	pub := l.p.PublicKey()
	h, err := memberHeader(pub, m.Header)
	if err == nil && !hmac.Equal(m.Proof, c.proof(pub, c.pub)) {
		err = errors.New("it does not prove the conversation key")
	}
	if err == nil {
		err = checkDirHeader(pub, m.DirHeader, h)
	}
	if err != nil {
		log.Print("hello refused from ", l.key[:8], ": ", err)
		return
	}
	c.mu.Lock()
	if !bytes.Equal(m.Group, c.group[:]) {
		first := !l.otherGroup
		l.otherGroup = true
		c.mu.Unlock()
		if first {
			log.Print("hello from ", l.key[:8], " names another member list")
		}
		return
	}
	l.hello = &m
	if l.since.IsZero() {
		l.since = time.Now()
	}
	l.answered = l.answered || m.Reply
	_, ok := c.accepted[l.key]
	c.mu.Unlock()
	l.once.Do(func() { close(l.proven) })
	if ok {
		c.accept(l, m, !m.Reply, m.Reply)
	}
}

// accept registers a proven peer that is on the roster. answer sends it a reply hello, and sync pulls from it.
func (c *ChatService) accept(l *link, m wireMsg, answer, sync bool) {
	if err := c.addMember(l.p.PublicKey(), m.Header, m.DirHeader); err != nil {
		log.Print("member: ", err)
		return
	}
	c.mu.Lock()
	if c.links[l.key] != l {
		c.mu.Unlock()
		return
	}
	was := l.registered
	l.registered = true
	c.mu.Unlock()
	if !was {
		c.emitPeers()
		c.peerBack()
	}
	if answer {
		if err := c.sendTo(l, c.hello(l, true)); err != nil {
			log.Print("hello: ", err)
		}
	}
	if sync {
		c.wg.Go(func() { c.syncFromPeer(l) })
	}
}

// authorize lets the members on the roster pull our two logs as ciphertext, and nobody else. No entry key travels:
// members derive them (see entrykeys.go).
func (c *ChatService) authorize(peer, owner ed25519.PublicKey, logID kololog.LogID) kolosync.Access {
	if c.isAccepted(hex.EncodeToString(peer)) && owner.Equal(c.pub) && (logID == c.log.ID() || logID == c.dirLog.ID()) {
		return kolosync.Ciphertext
	}
	return kolosync.NoAccess
}

// onMessage shows a message that arrives directly if it is the next one of its sender, and drops it if it was
// shown already. Only a peer whose hello was proven and who is on the roster is heard. A gap means we missed some: a
// sync from the sender shows them all, in order. A message shown directly is not in our store yet, so a sync follows
// to keep it for the history.
func (c *ChatService) onMessage(l *link, m wireMsg) {
	c.mu.Lock()
	_, ok := c.accepted[l.key]
	ok = ok && l.hello != nil
	c.mu.Unlock()
	if !ok {
		return
	}
	author := l.key
	c.showMu.Lock()
	defer c.showMu.Unlock()
	next, known := c.shown(author)
	switch {
	case !known: // its hello is not here yet, and the sync that follows it brings this message too
	case m.Index > next:
		c.wg.Go(func() { c.syncFromPeer(l) })
	case m.Index == next:
		b, err := c.open(l.p.PublicKey(), m.Sealed)
		if err != nil {
			log.Print("direct message refused: ", err)
			return
		}
		c.setShown(author, next+1)
		c.rememberDirect(author, m.Index, m.Sealed)
		c.emit("chat:msg", ChatMsg{From: author, Room: b.Room, Text: b.Text, Via: "direct", Time: b.Time})
		c.syncSoon(l)
	}
}

// syncSoon starts a sync from the peer unless one waits to start already, since that one brings this entry too.
func (c *ChatService) syncSoon(l *link) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.syncing[l.key] {
		return
	}
	c.syncing[l.key] = true
	c.wg.Go(func() {
		c.mu.Lock()
		delete(c.syncing, l.key)
		c.mu.Unlock()
		c.syncFromPeer(l)
	})
}

// syncFromPeer pulls the sender's log and its directory from the sender, as ciphertext like from a pin, and shows
// what is new. It waits for the pull from the pin at start, so what the pin has is taken from the pin. A member who
// is not on the roster is not pulled.
func (c *ChatService) syncFromPeer(l *link) {
	conn, ok := c.afterPin(l)
	author := l.key
	if !ok || !c.isAccepted(author) {
		return
	}
	rep, err := c.replica(author)
	if err != nil {
		log.Print("replica: ", err)
	} else {
		ctx, cancel := context.WithTimeout(c.ctx, pinTimeout)
		err = c.pull(ctx, conn, author, rep)
		cancel()
		if err != nil && c.ctx.Err() == nil {
			log.Print("sync from peer: ", err)
		} else {
			c.show(author)
		}
	}
	c.syncDirectory(conn, author)
}

// afterPin waits for the pull from the pin at start, and returns the 's' part of the peer's connection, direct or
// relayed.
func (c *ChatService) afterPin(l *link) (kolosync.Conn, bool) {
	select {
	case <-c.pinDone:
	case <-c.ctx.Done():
		return nil, false
	}
	return l.mx.part(tagSync), true
}

// show gives the window the messages of an author that it has not seen, in the order of the author's log. It skips
// an entry whose key its signed record proves wrong, and stops at any other entry that has no key yet: the pull
// derives it, so nothing after it jumps ahead. A quarantined author, or one not on the roster, shows nothing more.
func (c *ChatService) show(author string) {
	if !c.isAccepted(author) {
		return
	}
	c.showMu.Lock()
	flagged := c.showLocked(author)
	c.showMu.Unlock()
	if flagged {
		c.publishDirectory()
	}
}

// showLocked is show with showMu held. It says whether a log entry was found to differ from the message shown directly.
func (c *ChatService) showLocked(author string) bool {
	rep, err := c.replica(author)
	if err != nil {
		return false
	}
	refused, quarantined := c.refusedOf(author, rep.ID())
	if quarantined {
		return false
	}
	pub := rep.Header().LogKey
	for {
		index, _ := c.shown(author)
		if index >= rep.Len() {
			break
		}
		sealed, err := rep.Get(index)
		if errors.Is(err, kololog.ErrKeyUnavailable) {
			if !refused[index] {
				break
			}
			err = errBadKey
		}
		if err == nil {
			var b body
			if b, err = c.open(pub, sealed); err == nil {
				c.mu.Lock()
				via := "peer"
				if index < c.pinned[author] {
					via = "pin"
				}
				c.mu.Unlock()
				c.emit("chat:msg", ChatMsg{From: author, Room: b.Room, Text: b.Text, Via: via, Time: b.Time})
			}
		}
		if err != nil && !errors.Is(err, kololog.ErrDeleted) {
			log.Printf("entry %d of %s skipped: %v", index, author[:8], err)
		}
		c.setShown(author, index+1)
	}
	next, _ := c.shown(author)
	c.setPin(func([]pinState) {
		c.locked[author] = 0
		if c.stalled[rep.ID()] && rep.Len() > next { // only a failed write of keys leaves them waiting
			c.locked[author] = int(rep.Len() - next)
		}
	})
	return c.checkDirect(author, rep, refused)
}

// historyMsg is a message of the history with what orders it: the sender's time and the entry's index.
type historyMsg struct {
	ChatMsg
	time  int64
	index uint64
}

// replayHistory gives the window, in one "chat:history" event, the newest maxHistory messages that it showed
// before and that the store holds, and our own. A member's entries from its shown count on are new, so the live
// path shows them once. The notch ignores the event, so the history never counts as unread.
func (c *ChatService) replayHistory() {
	me := hex.EncodeToString(c.pub)
	read := func(author string, pub ed25519.PublicKey, end uint64, get func(uint64) ([]byte, error)) []historyMsg {
		var out []historyMsg
		for i := end - min(end, maxHistory); i < end; i++ {
			sealed, err := get(i)
			if err != nil {
				continue // deleted, or never readable
			}
			b, err := c.open(pub, sealed)
			if err != nil {
				continue
			}
			m := ChatMsg{From: author, Room: b.Room, Text: b.Text, Via: "history", Mine: author == me}
			out = append(out, historyMsg{ChatMsg: m, time: b.Time, index: i})
		}
		return out
	}
	all := addHistory(nil, read(me, c.pub, c.log.Len(), c.log.Get), maxHistory)
	for _, author := range c.authors() {
		if author == me {
			continue
		}
		rep, err := c.replica(author)
		if err != nil {
			continue
		}
		all = c.addPastHistory(all, author, read)
		shown, _ := c.shown(author)
		if shown > rep.Len() { // shown directly, then closed before a sync kept them: they show once they come
			shown = rep.Len()
			c.setShown(author, shown)
		}
		all = addHistory(all, read(author, rep.Header().LogKey, shown, rep.Get), maxHistory)
	}
	if len(all) == 0 {
		return
	}
	msgs := make([]ChatMsg, len(all))
	for i, m := range all {
		msgs[i] = m.ChatMsg
		msgs[i].Time = m.time
	}
	c.emit("chat:history", msgs)
}

// addPastHistory adds the messages of the logs a member wrote before it left and joined again, which it showed
// before.
func (c *ChatService) addPastHistory(all []historyMsg, author string,
	read func(string, ed25519.PublicKey, uint64, func(uint64) ([]byte, error)) []historyMsg) []historyMsg {
	c.mu.Lock()
	past := slices.Clone(c.members[author].Past)
	c.mu.Unlock()
	for _, header := range past {
		h, err := kololog.DecodeLogHeader(header)
		if err != nil {
			continue
		}
		rep, err := c.store.OpenReplica(h.LogKey, h.LogID, header)
		if err != nil {
			continue
		}
		all = addHistory(all, read(author, h.LogKey, rep.Len(), rep.Get), maxHistory)
		rep.Close()
	}
	return all
}

// addHistory merges one sender's messages, in log order, into the history and keeps the newest limit. Messages go
// by the sender's time; one that goes back, or has none, takes the time of the sender's message before it, so a
// sender's own order always holds. Equal times go by sender key, then by index.
func addHistory(all, msgs []historyMsg, limit int) []historyMsg {
	for i := 1; i < len(msgs); i++ {
		msgs[i].time = max(msgs[i].time, msgs[i-1].time)
	}
	all = append(all, msgs...)
	slices.SortFunc(all, func(a, b historyMsg) int {
		return cmp.Or(cmp.Compare(a.time, b.time), cmp.Compare(a.From, b.From), cmp.Compare(a.index, b.index))
	})
	return all[max(0, len(all)-limit):]
}

// pushLoop pushes to the pin whenever one is wanted; wishes that come while it works are merged into one. A pin that
// cannot be reached is tried again with a growing, jittered wait, or at once on the next wish.
func (c *ChatService) pushLoop() {
	p := c.tune.filled()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-c.push:
		}
		for attempt := 1; ; attempt++ {
			err := c.pushToPin(c.ctx)
			if err == nil || c.ctx.Err() != nil {
				break
			}
			log.Print("pin push: ", err)
			if !errors.Is(err, pin.ErrUnreachable) {
				break
			}
			t := time.NewTimer(retryDelay(attempt, p.pinRetryMin, p.pinRetryMax))
			select {
			case <-c.ctx.Done():
				t.Stop()
				return
			case <-c.push:
				t.Stop()
			case <-t.C:
			}
		}
	}
}

func (c *ChatService) pushSoon() {
	select {
	case c.push <- struct{}{}:
	default:
	}
}

func (c *ChatService) emitPeers() {
	c.emit("chat:peers", len(c.registered()))
}

// setPin changes the pins' state under the lock and tells the window.
func (c *ChatService) setPin(change func([]pinState)) {
	c.mu.Lock()
	change(c.pins)
	st := pinStatus(c.set.pins, c.pins)
	for _, n := range c.locked {
		st.Locked += n
	}
	c.mu.Unlock()
	c.emit("chat:pin", st)
}

// pinStatus sums up the pins: a pin works when its last push and the pull at start did. The window shows an
// error only when no pin works, since one that works keeps the messages for the people who are offline.
func pinStatus(pins []pinRef, states []pinState) PinStatus {
	st := PinStatus{Pins: []PinState{}} // Go sends a nil slice as null, and an event is not made a list on the way
	var failed []string
	for i, s := range states {
		st.Pins = append(st.Pins, PinState{Addr: pins[i].Addr, Key: hex.EncodeToString(pins[i].Key), Push: s.Push, Pull: s.Pull})
		if why := cmp.Or(s.Push, s.Pull); why != "" {
			failed = append(failed, why)
		}
	}
	switch {
	case len(failed) == 0:
	case len(failed) == len(states):
		st.Error = failed[0]
	default:
		st.Warning = fmt.Sprintf("%d of %d pins cannot be used now; the others keep your messages. %s", len(failed), len(states), failed[0])
	}
	return st
}

func loadIdentity(dir string) (*koloid.Identity, error) {
	path := filepath.Join(dir, "identity.key")
	id, err := koloid.Load(path)
	if !errors.Is(err, fs.ErrNotExist) {
		return id, err
	}
	if id, err = koloid.Generate(); err != nil {
		return nil, err
	}
	if err = id.SaveNew(path); err != nil && !errors.Is(err, koloid.ErrSavedNotSynced) {
		return nil, err
	}
	return id, nil
}

// replica opens, once, the read-only copy of a member's log that we keep in our store.
func (c *ChatService) replica(author string) (*kolostore.Replica, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if rep, ok := c.replicas[author]; ok {
		return rep, nil
	}
	m, ok := c.members[author]
	if !ok {
		return nil, fmt.Errorf("unknown member %s", author[:8])
	}
	h, err := kololog.DecodeLogHeader(m.Header)
	if err != nil {
		return nil, err
	}
	rep, err := c.store.OpenReplica(h.LogKey, h.LogID, m.Header)
	if err != nil {
		return nil, err
	}
	c.replicas[author] = rep
	return rep, nil
}

func (c *ChatService) markPinned(author string, n uint64) {
	c.mu.Lock()
	c.pinned[author] = max(c.pinned[author], n)
	c.mu.Unlock()
}

// authors lists the members, as hex public keys.
func (c *ChatService) authors() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	keys := make([]string, 0, len(c.members))
	for k := range c.members {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func (c *ChatService) shown(author string) (uint64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.members[author]
	if !ok {
		return 0, false
	}
	return m.Shown, true
}

func (c *ChatService) setShown(author string, n uint64) {
	c.mu.Lock()
	c.members[author].Shown = n
	err := saveMembers(c.set.DataDir, c.members)
	c.mu.Unlock()
	if err != nil {
		log.Print("members: ", err)
	}
}

// addMember registers a member from its signed headers. A member we know gets its directory header when it
// first sends one, and starts over with a new messages log after it left and joined again.
func (c *ChatService) addMember(pub ed25519.PublicKey, header, dirHeader []byte) error {
	author := hex.EncodeToString(pub)
	c.showMu.Lock() // a show of the old log must not count into the new one
	defer c.showMu.Unlock()
	c.mu.Lock()
	m, known := c.members[author]
	switch {
	case !known:
		c.members[author] = &member{Header: header, DirHeader: dirHeader}
	case m.Quarantined: // it stays quarantined, whatever log it writes now
		c.mu.Unlock()
		return nil
	case !bytes.Equal(m.Header, header):
		past := append(m.Past, m.Header)
		c.members[author] = &member{Header: header, DirHeader: dirHeader, Past: past[max(0, len(past)-maxPastLogs):]}
		// The old replicas stay open until the store closes, since a pull may still use them.
		delete(c.replicas, author)
		delete(c.dirs, author)
		delete(c.pinned, author)
		delete(c.locked, author)
		delete(c.direct, author)
		log.Print("member joined again: ", author[:8])
	case len(m.DirHeader) == 0 && len(dirHeader) > 0:
		m.DirHeader = dirHeader
	default:
		c.mu.Unlock()
		return nil
	}
	err := saveMembers(c.set.DataDir, c.members)
	c.mu.Unlock()
	return err
}

func loadMembers(dir string) (map[string]*member, error) {
	members := map[string]*member{}
	data, err := readFile(filepath.Join(dir, "members.json"), maxStateFile)
	if errors.Is(err, fs.ErrNotExist) {
		return members, nil
	}
	if err != nil {
		return nil, err
	}
	return members, json.Unmarshal(data, &members)
}

// saveMembers replaces members.json with writeFileAtomic.
func saveMembers(dir string, members map[string]*member) error {
	data, err := json.Marshal(members)
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, "members.json"), data, 0o600)
}
