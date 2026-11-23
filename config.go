package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ciranda-dev/kolo-pin"
	koloswarm "github.com/ciranda-dev/kolo-swarm"
)

// Defaults of the settings that have one. Everything else must be configured.
const (
	loopbackListen        = "127.0.0.1:0" // the default when every entry node is on this computer
	publicListen          = "0.0.0.0:0"   // the default otherwise: the public network cannot be reached from loopback
	defaultPinTTL         = 30 * 24 * time.Hour
	defaultPinQuota       = 16 << 20 // bytes
	defaultLookupInterval = 5 * time.Second
	defaultMaxPins        = 16       // kolo-node's bound on a configured list of peer addresses
	maxPinFile            = 64 << 10 // kolo-node's bound on a configuration file, in bytes
	// defaultMaxConversations bounds the engines one app runs, each with its own socket, swarm and two stores.
	defaultMaxConversations = 64
	maxConversations        = 256
)

// ErrConfig matches every ConfigError, whatever setting it names.
var ErrConfig = errors.New("invalid configuration")

// Causes of a ConfigError, besides the errors of kolo-pin for a bad token.
var (
	ErrMissing   = errors.New("is required")
	ErrMalformed = errors.New("is malformed")
	ErrTooMany   = errors.New("lists too many pins")

	errFixedPort = errors.New("has a fixed port, which only one conversation can use: use port 0")
)

// ConfigError names the setting that is wrong and why. errors.Is(err, ErrConfig) is true for every one, and
// errors.Is(err, ErrMissing), pin.ErrExpired and the like tell the cause.
type ConfigError struct {
	Setting string // the flag name, such as "pin-key"
	Err     error
}

func (e *ConfigError) Error() string { return fmt.Sprintf("kolo-chat: -%s %v", e.Setting, e.Err) }
func (e *ConfigError) Unwrap() error { return e.Err }
func (e *ConfigError) Is(target error) bool {
	return target == ErrConfig
}

// Config is everything the chat needs from outside. A zero field takes its default, if it has one.
// LoadConfig fills it from flags and environment variables.
type Config struct {
	DataDir             string        // -data, KOLO_CHAT_DATA: one folder per conversation, see conversations.go. Default: <user config dir>/kolo-chat
	Bootstrap           string        // -bootstrap, KOLO_CHAT_BOOTSTRAP: host:port of the entry nodes of your own network, comma-separated, used alone. Default: the public network
	Listen              string        // -listen, KOLO_CHAT_LISTEN: local IP:PORT of every conversation. Default 127.0.0.1:0 when every entry node is on loopback, else 0.0.0.0:0; a fixed port allows one conversation
	ConversationKey     string        // KOLO_CHAT_CONVERSATION_KEY (not a flag: a flag shows in the process list): 64 hex characters (32 bytes), the key of one conversation, kept in its folder. Empty to start conversations in the window or join with an invite
	ConversationKeyFile string        // -conversation-key-file, KOLO_CHAT_CONVERSATION_KEY_FILE: a file that holds the key in hex, readable by its owner only; it wins over KOLO_CHAT_CONVERSATION_KEY
	Pins                []string      // -pin, KOLO_CHAT_PIN: pins as IP:PORT@KEY, KEY from `kolo-pin id`; comma-separated, repeatable. Optional: the pins of the conversations started here, which keep messages for people who are offline; a joiner gets them from the invite
	PinFiles            []string      // -pin-file, KOLO_CHAT_PIN_FILE: files of pins, one or more per line, "#" starts a comment; repeatable
	PinTokenFiles       []string      // -pin-token-file, KOLO_CHAT_PIN_TOKEN_FILE: files of capabilities in hex, readable by their owner only, "#" starts a comment; repeatable; they replace KOLO_CHAT_PIN_TOKEN
	PinTokens           []string      // KOLO_CHAT_PIN_TOKEN (not a flag, as for the key): capabilities in hex for the conversation of the key, issued by its kolo-id, each for one of the pins. Default: the app issues one per pin
	PinTTL              time.Duration // -pin-ttl, KOLO_CHAT_PIN_TTL: lifetime of the capabilities the app issues. Default 720h
	PinQuota            uint64        // -pin-quota, KOLO_CHAT_PIN_QUOTA: bytes each pin may keep for you, in the capabilities the app issues. Default 16777216
	MaxPins             int           // -max-pins, KOLO_CHAT_MAX_PINS: the most pins accepted, configured or from an invite. Default 16
	LookupInterval      time.Duration // -lookup-interval, KOLO_CHAT_LOOKUP_INTERVAL: time between topic lookups. Default 5s
	MaxConversations    int           // -max-conversations, KOLO_CHAT_MAX_CONVERSATIONS: the most conversations, the pending join included, from 1 to 256. Default 64
	Relays              []string      // -relay: connection relays as host:port@key, comma-separated, repeatable
	RelayFiles          []string      // -relay-file: files of relays, one or more per line, "#" starts a comment; repeatable
	// KOLO_CHAT_RELAYS is read only when neither -relay nor -relay-file is given.

	relaysEnv bool          // Relays came from KOLO_CHAT_RELAYS
	joinFind  time.Duration // tests only: how long Join looks for the inviter, instead of findTimeout's
}

// LoadConfig reads the flags in args. A setting that is not given as a flag comes from its environment variable
// (read with getenv, such as os.Getenv), and a flag wins over its variable: for a list, the flags replace it.
func LoadConfig(args []string, getenv func(string) string, stderr io.Writer) (Config, error) {
	var c Config
	fs := flag.NewFlagSet("kolo-chat", flag.ContinueOnError)
	fs.SetOutput(stderr)
	text := func(p *string) func(string) error {
		return func(v string) error { *p = v; return nil }
	}
	list := func(p *[]string, split bool) func(string) error {
		return func(v string) error {
			if split {
				*p = append(*p, splitList(v)...)
			} else {
				*p = append(*p, v)
			}
			return nil
		}
	}
	count := func(p *int) func(string) error {
		return func(v string) (err error) { *p, err = strconv.Atoi(v); return err }
	}
	dur := func(p *time.Duration) func(string) error {
		return func(v string) (err error) { *p, err = time.ParseDuration(v); return err }
	}
	num := func(p *uint64) func(string) error {
		return func(v string) (err error) { *p, err = strconv.ParseUint(v, 10, 64); return err }
	}
	for _, s := range []struct {
		name, env, help string
		set             func(string) error
		list            *[]string // set for a list, which the first flag empties
	}{
		{"data", "KOLO_CHAT_DATA", "data directory (default <user config dir>/kolo-chat)", text(&c.DataDir), nil},
		{"bootstrap", "KOLO_CHAT_BOOTSTRAP", "host:port of the entry nodes of your own network, comma-separated, used alone (default: the public network)", text(&c.Bootstrap), nil},
		{"listen", "KOLO_CHAT_LISTEN", "local IP:PORT to listen on (default " + loopbackListen + " when every entry node is on loopback, else " + publicListen + ")", text(&c.Listen), nil},
		{"conversation-key", "KOLO_CHAT_CONVERSATION_KEY", "", text(&c.ConversationKey), nil},
		{"conversation-key-file", "KOLO_CHAT_CONVERSATION_KEY_FILE", "file that holds the 32-byte key of the conversation in hex, readable by you only; the key is kept in the data directory (leave it out to join with an invite, or to go on with the kept one)", text(&c.ConversationKeyFile), nil},
		{"pin", "KOLO_CHAT_PIN", "optional pins as IP:PORT@KEY, comma-separated, repeatable: the pins of the conversations you start, which keep messages for people who are offline", list(&c.Pins, true), &c.Pins},
		{"pin-file", "KOLO_CHAT_PIN_FILE", "file of pins, one or more IP:PORT@KEY per line, repeatable", list(&c.PinFiles, false), &c.PinFiles},
		{"pin-token", "KOLO_CHAT_PIN_TOKEN", "", list(&c.PinTokens, true), &c.PinTokens},
		{"pin-token-file", "KOLO_CHAT_PIN_TOKEN_FILE", "file of capabilities in hex for the conversation of the key, one per pin at most, readable by you only, repeatable (default: issued by the app)", list(&c.PinTokenFiles, false), &c.PinTokenFiles},
		{"pin-ttl", "KOLO_CHAT_PIN_TTL", "lifetime of the capabilities the app issues (default 720h)", dur(&c.PinTTL), nil},
		{"pin-quota", "KOLO_CHAT_PIN_QUOTA", "bytes each pin may keep for you (default 16777216)", num(&c.PinQuota), nil},
		{"max-pins", "KOLO_CHAT_MAX_PINS", "the most pins accepted, configured or from an invite (default 16)", count(&c.MaxPins), nil},
		{"lookup-interval", "KOLO_CHAT_LOOKUP_INTERVAL", "time between lookups of the topic (default 5s)", dur(&c.LookupInterval), nil},
		{"max-conversations", "KOLO_CHAT_MAX_CONVERSATIONS", "the most conversations this app keeps, from 1 to 256 (default 64)", count(&c.MaxConversations), nil},
	} {
		if v := getenv(s.env); v != "" {
			if err := s.set(v); err != nil {
				return c, &ConfigError{Setting: s.name, Err: fmt.Errorf("%w: %s: %w", ErrMalformed, s.env, err)}
			}
		}
		if s.help == "" { // a secret: the variable only, since a flag shows in the process list and the shell history
			fs.Func(s.name, "not accepted: set "+s.env+" or use -"+s.name+"-file", func(string) error {
				return fmt.Errorf("a flag shows in the process list: set %s or use -%s-file", s.env, s.name)
			})
			continue
		}
		flagged := false
		fs.Func(s.name, s.help+" ("+s.env+")", func(v string) error {
			if s.list != nil && !flagged {
				*s.list = nil
			}
			flagged = true
			return s.set(v)
		})
	}
	fs.Func("relay", "connection relays as host:port@key, comma-separated, repeatable (KOLO_CHAT_RELAYS when no -relay or -relay-file is given)",
		func(v string) error { c.Relays = append(c.Relays, v); return nil })
	fs.Func("relay-file", "file of connection relays, one or more host:port@key per line, repeatable",
		func(v string) error { c.RelayFiles = append(c.RelayFiles, v); return nil })
	if err := refuseSecretFlags(args); err != nil {
		return c, err
	}
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	if v := getenv("KOLO_CHAT_RELAYS"); v != "" && len(c.Relays) == 0 && len(c.RelayFiles) == 0 {
		c.Relays, c.relaysEnv = []string{v}, true
	}
	return c, nil
}

// refuseSecretFlags refuses -conversation-key and -pin-token before the flag package reads them, since its error
// message would print the value.
func refuseSecretFlags(args []string) error {
	for _, a := range args {
		if a == "--" {
			return nil
		}
		name, _, _ := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if strings.HasPrefix(a, "-") && (name == "conversation-key" || name == "pin-token") {
			return &ConfigError{Setting: name, Err: fmt.Errorf("%w: a flag shows in the process list: set KOLO_CHAT_%s or use -%s-file", ErrMalformed, strings.ToUpper(strings.ReplaceAll(name, "-", "_")), name)}
		}
	}
	return nil
}

// splitList returns the entries of a comma-separated list, trimmed, without empty ones.
func splitList(s string) []string {
	var out []string
	for e := range strings.SplitSeq(s, ",") {
		if e = strings.TrimSpace(e); e != "" {
			out = append(out, e)
		}
	}
	return out
}

// settings is a Config after defaults and checks: values the code can use as they are.
type settings struct {
	Config
	conversationKey [32]byte
	pins            []pinRef
	tokens          map[string][]byte // by pin key, the tokens of the configuration; a pin without one gets one issued
	bootstrap       []string          // the entry nodes of your own network; nil is the public network
	inConversation  bool              // the key and the pins are known: from the configuration, or later from an invite
	relays          []koloswarm.Relay // connection relays, for koloswarm.Config.Relays
	relaysFrom      string            // where they came from: "-relay", "-relay-file", both, "KOLO_CHAT_RELAYS" or ""
}

// resolve applies the defaults and checks every setting, returning a *ConfigError for the first bad one.
// Without a conversation key a chat starts as a joiner: the key and the pins come from an invite (see
// useConversation). The pins are then those of the conversations started in the window, and a token, which is
// for the key's conversation only, is refused. A token is matched to its pin here and checked later, in
// capability, because that needs the identity.
func (c Config) resolve() (settings, error) {
	c, err := c.readSecrets()
	if err != nil {
		return settings{Config: c}, err
	}
	s := settings{Config: c}
	if s.DataDir == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return s, &ConfigError{Setting: "data", Err: fmt.Errorf("%w: no user config directory: %w", ErrMissing, err)}
		}
		s.DataDir = filepath.Join(dir, "kolo-chat")
	}
	if s.PinTTL == 0 {
		s.PinTTL = defaultPinTTL
	}
	if s.PinQuota == 0 {
		s.PinQuota = defaultPinQuota
	}
	if s.LookupInterval == 0 {
		s.LookupInterval = defaultLookupInterval
	}
	switch {
	case s.MaxPins == 0:
		s.MaxPins = defaultMaxPins
	case s.MaxPins < 0:
		return s, &ConfigError{Setting: "max-pins", Err: fmt.Errorf("%w: want a positive number", ErrMalformed)}
	}
	switch {
	case s.MaxConversations == 0:
		s.MaxConversations = defaultMaxConversations
	case s.MaxConversations < 1 || s.MaxConversations > maxConversations:
		return s, &ConfigError{Setting: "max-conversations", Err: fmt.Errorf("%w: want 1 to %d", ErrMalformed, maxConversations)}
	}

	if s.Bootstrap != "" {
		s.bootstrap, err = koloswarm.ParseNodes(s.Bootstrap)
		switch {
		case err != nil:
			return s, &ConfigError{Setting: "bootstrap", Err: relayError(err, "")}
		case len(s.bootstrap) == 0:
			return s, &ConfigError{Setting: "bootstrap", Err: fmt.Errorf("%w: lists no entry node", ErrMalformed)}
		}
	}
	if s.Listen == "" {
		s.Listen = publicListen
		if loopbackOnly(s.bootstrap) {
			s.Listen = loopbackListen
		}
	}
	if s.relays, s.relaysFrom, err = c.loadRelays(); err != nil {
		return s, err
	}
	if c.ConversationKey == "" && len(c.PinTokens) > 0 {
		return s, &ConfigError{Setting: "conversation-key", Err: ErrMissing}
	}
	if s.pins, err = c.loadPins(); err != nil {
		return s, err
	}
	if err := checkPins(s.pins, s.MaxPins); err != nil {
		return s, &ConfigError{Setting: "pin", Err: err}
	}
	if c.ConversationKey == "" {
		return s, nil
	}
	if err := parseHex("conversation-key", c.ConversationKey, s.conversationKey[:]); err != nil {
		return s, err
	}
	s.inConversation = true
	s.tokens, err = matchTokens(c.PinTokens, s.pins)
	return s, err
}

// loopbackOnly says whether every entry node is a loopback IPv4 literal, so the app can stay on 127.0.0.1.
func loopbackOnly(nodes []string) bool {
	if len(nodes) == 0 {
		return false
	}
	for _, n := range nodes {
		if a, err := netip.ParseAddrPort(n); err != nil || !a.Addr().IsLoopback() {
			return false
		}
	}
	return true
}

// NetworkInfo says which network the app joins: Mode is "public" (the entry nodes built into Kolo, run by Ciranda)
// or "own" (the -bootstrap list). Nodes are the entry nodes of "own", which are the user's; the public ones are not sent to the window.
type NetworkInfo struct {
	Mode  string   `json:"mode"`
	Nodes []string `json:"nodes"`
}

// network describes the network the settings choose.
func (s settings) network() NetworkInfo {
	if len(s.bootstrap) > 0 {
		return NetworkInfo{Mode: "own", Nodes: slices.Clone(s.bootstrap)}
	}
	return NetworkInfo{Mode: "public", Nodes: []string{}}
}

// fixedPort says whether the listen address names a port, which one socket only can hold.
func (s settings) fixedPort() bool {
	ap, err := netip.ParseAddrPort(s.Listen)
	return err == nil && ap.Port() != 0
}

// useConversation takes the key and the pins from an invite, or from the file that kept them.
func (s *settings) useConversation(conv conversation) {
	copy(s.conversationKey[:], conv.Key)
	s.pins = conv.Pins
	s.inConversation = true
}

// pinRef is one pin: where it listens and its public key.
type pinRef struct {
	Addr string            `json:"addr"`
	Key  ed25519.PublicKey `json:"key"`
}

func (p pinRef) peer() pin.Peer { return pin.Peer{ID: p.Key, Addr: p.Addr} }

// parsePin reads a pin written as IP:PORT@KEY, KEY being the pin's public key in hex.
func parsePin(entry string) (pinRef, error) {
	addr, key, ok := strings.Cut(entry, "@")
	if !ok {
		return pinRef{}, fmt.Errorf("%w: %q: want IP:PORT@KEY", ErrMalformed, entry)
	}
	if err := checkPinAddr(addr); err != nil {
		return pinRef{}, fmt.Errorf("%w: %q: %w", ErrMalformed, entry, err)
	}
	b, err := hex.DecodeString(key)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return pinRef{}, fmt.Errorf("%w: %q: want a key of %d bytes in hex", ErrMalformed, entry, ed25519.PublicKeySize)
	}
	return pinRef{Addr: addr, Key: b}, nil
}

// checkPinAddr accepts an IPv4 address that can be dialed, with a port from 1 to 65535.
func checkPinAddr(addr string) error {
	ap, err := netip.ParseAddrPort(addr)
	if err != nil || !ap.Addr().Is4() || ap.Addr().IsUnspecified() || ap.Addr().IsMulticast() || ap.Port() == 0 {
		return errors.New("want an IPv4 address and a port from 1 to 65535")
	}
	return nil
}

// checkPins says whether pins is a list the app can use: at most max pins, each one valid, no key twice.
func checkPins(pins []pinRef, max int) error {
	if len(pins) > max {
		return fmt.Errorf("%w: more than %d (see -max-pins)", ErrTooMany, max)
	}
	seen := map[string]bool{}
	for _, p := range pins {
		if len(p.Key) != ed25519.PublicKeySize {
			return fmt.Errorf("%w: a pin key is not %d bytes", ErrMalformed, ed25519.PublicKeySize)
		}
		if err := checkPinAddr(p.Addr); err != nil {
			return fmt.Errorf("%w: %s: %w", ErrMalformed, p.Addr, err)
		}
		if seen[string(p.Key)] {
			return fmt.Errorf("%w: the pin %x is listed twice", ErrMalformed, p.Key)
		}
		seen[string(p.Key)] = true
	}
	return nil
}

// loadPins returns the pins of -pin, then those of each -pin-file, without exact repeats.
func (c Config) loadPins() ([]pinRef, error) {
	var pins []pinRef
	add := func(entry string) error {
		p, err := parsePin(entry)
		if err == nil && !slices.ContainsFunc(pins, func(q pinRef) bool { return q.Addr == p.Addr && q.Key.Equal(p.Key) }) {
			pins = append(pins, p)
		}
		return err
	}
	for _, e := range c.Pins {
		if err := add(e); err != nil {
			return nil, &ConfigError{Setting: "pin", Err: err}
		}
	}
	for _, path := range c.PinFiles {
		b, err := readPinFile(path)
		if err != nil {
			return nil, &ConfigError{Setting: "pin-file", Err: fmt.Errorf("%s: %w", path, err)}
		}
		n, entries := 0, 0
		for line := range strings.Lines(strings.TrimPrefix(string(b), "\ufeff")) {
			n++
			line, _, _ = strings.Cut(line, "#")
			for _, e := range splitList(line) {
				entries++
				if err := add(e); err != nil {
					return nil, &ConfigError{Setting: "pin-file", Err: fmt.Errorf("%s line %d: %w", path, n, err)}
				}
			}
		}
		if entries == 0 {
			return nil, &ConfigError{Setting: "pin-file", Err: fmt.Errorf("%w: %s lists no pin", ErrMalformed, path)}
		}
	}
	return pins, nil
}

// loadRelays returns the relays of -relay and of each -relay-file, or of KOLO_CHAT_RELAYS, checked as one list by
// kolo-swarm (no key or address twice), and where they came from.
func (c Config) loadRelays() ([]koloswarm.Relay, string, error) {
	where := ""
	if c.relaysEnv {
		where = "KOLO_CHAT_RELAYS: "
	}
	relays, err := koloswarm.ParseRelays(strings.Join(c.Relays, ","))
	if err != nil {
		return nil, "", &ConfigError{Setting: "relay", Err: relayError(err, where)}
	}
	for _, path := range c.RelayFiles {
		rs, err := koloswarm.LoadRelays(path)
		if err == nil { // checked again with the relays before it: no key or address twice
			rs, err = koloswarm.ParseRelays(relaysText(slices.Concat(relays, rs)))
		}
		if err != nil {
			return nil, "", &ConfigError{Setting: "relay-file", Err: relayError(err, path+": ")}
		}
		relays = rs
	}
	var from []string
	switch {
	case len(relays) == 0:
	case c.relaysEnv:
		from = append(from, "KOLO_CHAT_RELAYS")
	default:
		if len(c.Relays) > 0 {
			from = append(from, "-relay")
		}
		if len(c.RelayFiles) > 0 {
			from = append(from, "-relay-file")
		}
	}
	return relays, strings.Join(from, ", "), nil
}

// relaysText writes relays as a comma list that ParseRelays reads.
func relaysText(relays []koloswarm.Relay) string {
	out := make([]string, len(relays))
	for i, r := range relays {
		out[i] = r.Addr + "@" + hex.EncodeToString(r.PublicKey)
	}
	return strings.Join(out, ",")
}

// relayError is an error of kolo-swarm's relay or entry node parsing as the cause of a ConfigError: ErrMalformed for a bad entry,
// and the file's own error, such as fs.ErrNotExist, for a file that cannot be read.
func relayError(err error, where string) error {
	if !errors.Is(err, koloswarm.ErrInvalidArgument) {
		if inner := errors.Unwrap(err); inner != nil {
			err = inner // the OS error, without kolo-swarm's prefix
		}
		return fmt.Errorf("cannot be read: %w", err)
	}
	msg := strings.TrimPrefix(strings.TrimPrefix(err.Error(), "koloswarm: "), "invalid argument: ")
	return fmt.Errorf("%w: %s%s", ErrMalformed, where, msg)
}

// readSecrets puts what -conversation-key-file and -pin-token-file hold in ConversationKey and PinTokens. The files
// must be readable by their owner only (the check is skipped on Windows, which has no such mode bits).
func (c Config) readSecrets() (Config, error) {
	if c.ConversationKeyFile != "" {
		b, err := readSecretFile(c.ConversationKeyFile)
		if err != nil {
			return c, &ConfigError{Setting: "conversation-key-file", Err: err}
		}
		if c.ConversationKey = strings.TrimSpace(string(b)); c.ConversationKey == "" {
			return c, &ConfigError{Setting: "conversation-key-file", Err: fmt.Errorf("%w: the file is empty", ErrMalformed)}
		}
	}
	if len(c.PinTokenFiles) > 0 {
		c.PinTokens = nil
		for _, path := range c.PinTokenFiles {
			b, err := readSecretFile(path)
			if err != nil {
				return c, &ConfigError{Setting: "pin-token-file", Err: err}
			}
			before := len(c.PinTokens)
			for line := range strings.Lines(strings.TrimPrefix(string(b), "\ufeff")) {
				line, _, _ = strings.Cut(line, "#")
				c.PinTokens = append(c.PinTokens, strings.Fields(strings.ReplaceAll(line, ",", " "))...)
			}
			if len(c.PinTokens) == before {
				return c, &ConfigError{Setting: "pin-token-file", Err: fmt.Errorf("%w: the file holds no token", ErrMalformed)}
			}
		}
	}
	return c, nil
}

// readSecretFile reads a file that holds a secret: a regular file that other users cannot open.
func readSecretFile(path string) ([]byte, error) {
	if st, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("cannot be read: %w", err)
	} else if runtime.GOOS != "windows" && st.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%w: the file is open to other users: chmod 600 it", ErrMalformed)
	}
	b, err := readPinFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot be read: %w", err)
	}
	return b, nil
}

// readPinFile reads a regular file of at most maxPinFile bytes. It checks the path before opening it, so a
// device or a FIFO is never opened, and the opened file again, in case it was swapped in between.
func readPinFile(path string) ([]byte, error) {
	if st, err := os.Stat(path); err != nil {
		return nil, err
	} else if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: not a regular file", ErrMalformed)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: not a regular file", ErrMalformed)
	}
	b, err := io.ReadAll(io.LimitReader(f, maxPinFile+1))
	if err == nil && len(b) > maxPinFile {
		err = fmt.Errorf("%w: larger than %d bytes", ErrMalformed, maxPinFile)
	}
	return b, err
}

// matchTokens decodes the configured tokens and files each under the pin it names. A token that is not a
// capability, or is for a pin that is not in the list, or a second one for a pin, is refused.
func matchTokens(tokens []string, pins []pinRef) (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, t := range tokens {
		b, err := hex.DecodeString(t)
		if err != nil {
			return nil, &ConfigError{Setting: "pin-token", Err: fmt.Errorf("%w: %w", ErrMalformed, err)}
		}
		c, err := pin.ParseCapability(b)
		switch {
		case err != nil:
			err = fmt.Errorf("%w: %w", pin.ErrInvalidCapability, err)
		case !slices.ContainsFunc(pins, func(p pinRef) bool { return c.Peer.Equal(p.Key) }):
			err = fmt.Errorf("%w: issued for a pin that is not in the list", pin.ErrUnauthorized)
		case out[string(c.Peer)] != nil:
			err = fmt.Errorf("%w: two tokens for the pin %x", ErrMalformed, []byte(c.Peer))
		}
		if err != nil {
			return nil, &ConfigError{Setting: "pin-token", Err: err}
		}
		out[string(c.Peer)] = b
	}
	return out, nil
}

// parseHex decodes the hex setting value into dst, which has the exact size wanted.
func parseHex(setting, value string, dst []byte) error {
	if value == "" {
		return &ConfigError{Setting: setting, Err: ErrMissing}
	}
	b, err := hex.DecodeString(value)
	if err != nil || len(b) != len(dst) {
		return &ConfigError{Setting: setting, Err: fmt.Errorf("%w: want %d bytes in hex", ErrMalformed, len(dst))}
	}
	copy(dst, b)
	return nil
}
