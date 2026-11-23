package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	koloid "github.com/ciranda-dev/kolo-id"
	kololog "github.com/ciranda-dev/kolo-log"
	"github.com/ciranda-dev/kolo-pin"
)

// testPins returns n pin keys in hex and their entries at 10.0.0.1, 10.0.0.2 and so on.
func testPins(t *testing.T, n int) (keys, entries []string) {
	t.Helper()
	for i := range n {
		id, err := koloid.Generate()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, id.PublicKeyString())
		entries = append(entries, fmt.Sprintf("10.0.0.%d:7450@%s", i+1, id.PublicKeyString()))
	}
	return keys, entries
}

func loadConfig(t *testing.T, args []string, env map[string]string) Config {
	t.Helper()
	cfg, err := LoadConfig(args, func(k string) string { return env[k] }, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func addrsOf(pins []pinRef) []string {
	var out []string
	for _, p := range pins {
		out = append(out, p.Addr)
	}
	return out
}

func wantConfigError(t *testing.T, name string, err, want error, setting string) {
	t.Helper()
	var ce *ConfigError
	if !errors.Is(err, want) || !errors.Is(err, ErrConfig) || !errors.As(err, &ce) || ce.Setting != setting {
		t.Errorf("%s: got %v, want %v for -%s", name, err, want, setting)
	}
}

func TestConfig(t *testing.T) {
	_, pins := testPins(t, 2)
	env := map[string]string{"KOLO_CHAT_PIN": pins[0], "KOLO_CHAT_PIN_QUOTA": "1000"}
	env["KOLO_CHAT_CONVERSATION_KEY"] = testKey
	cfg := loadConfig(t, []string{"-pin", pins[1], "-pin-ttl", "48h"}, env)
	if !slices.Equal(cfg.Pins, pins[1:]) || cfg.PinQuota != 1000 || cfg.PinTTL != 48*time.Hour {
		t.Fatalf("a flag beats the environment: %+v", cfg)
	}
	set, err := cfg.resolve()
	if err != nil || len(set.bootstrap) != 0 || set.Listen != publicListen || set.LookupInterval != defaultLookupInterval ||
		set.MaxPins != 0 || len(set.pins) != 1 {
		t.Fatalf("defaults: %+v, %v", set, err)
	}

	for _, tc := range []struct {
		name   string
		change func(*Config)
		want   error
		field  string
	}{
		{"a token without a conversation key", func(c *Config) { c.ConversationKey, c.PinTokens = "", []string{"aa"} }, ErrMissing, "conversation-key"},
		{"a bad pin without a conversation key", func(c *Config) { c.ConversationKey, c.Pins = "", []string{"10.0.0.1:7450"} }, ErrMalformed, "pin"},
		{"no conversation", func(c *Config) { c.MaxConversations = -1 }, ErrMalformed, "max-conversations"},
		{"257 conversations", func(c *Config) { c.MaxConversations = maxConversations + 1 }, ErrMalformed, "max-conversations"},
		{"short conversation key", func(c *Config) { c.ConversationKey = "abcd" }, ErrMalformed, "conversation-key"},
		{"bad bootstrap", func(c *Config) { c.Bootstrap = "x" }, ErrMalformed, "bootstrap"},
		{"no bootstrap entry", func(c *Config) { c.Bootstrap = "," }, ErrMalformed, "bootstrap"},
		{"negative max pins", func(c *Config) { c.MaxPins = -1 }, ErrMalformed, "max-pins"},
	} {
		c := cfg
		tc.change(&c)
		_, err := c.resolve()
		wantConfigError(t, tc.name, err, tc.want, tc.field)
	}
	for _, args := range [][]string{{"-pin-ttl", "soon"}, {"-max-pins", "many"}, {"-max-conversations", "all"}} {
		if _, err := LoadConfig(args, func(string) string { return "" }, io.Discard); err == nil {
			t.Errorf("%v must fail", args)
		}
	}
	if _, err := LoadConfig(nil, func(k string) string { return map[string]string{"KOLO_CHAT_MAX_PINS": "x"}[k] }, io.Discard); err == nil {
		t.Error("a bad KOLO_CHAT_MAX_PINS must fail")
	}
}

// T20: the pins without a conversation key are those of the conversations started in the window; a token is still
// for the key's conversation only.
func TestPinsWithoutAConversationKey(t *testing.T) {
	_, pins := testPins(t, 1)
	set, err := (Config{DataDir: t.TempDir(), Pins: pins}).resolve()
	if err != nil || len(set.pins) != 1 || set.inConversation {
		t.Fatalf("pins alone: %+v, %v", set.pins, err)
	}
	_, err = (Config{DataDir: t.TempDir(), Pins: pins, PinTokens: []string{"aa"}}).resolve()
	wantConfigError(t, "a token without a key", err, ErrMissing, "conversation-key")
}

// The pin is optional: a conversation key alone resolves with no pin, a token still needs the pin it names, and a
// pin file that lists nothing is a mistake.
func TestPinIsOptional(t *testing.T) {
	set, err := (Config{DataDir: t.TempDir(), ConversationKey: testKey}).resolve()
	if err != nil || len(set.pins) != 0 || !set.inConversation || len(set.tokens) != 0 {
		t.Fatalf("a key and no pin: %+v, %v", set, err)
	}
	set, err = (Config{DataDir: t.TempDir()}).resolve()
	if err != nil || len(set.pins) != 0 || set.inConversation {
		t.Fatalf("nothing: %+v, %v", set, err)
	}
	id, err := koloid.Generate()
	if err != nil {
		t.Fatal(err)
	}
	other, err := koloid.Generate()
	if err != nil {
		t.Fatal(err)
	}
	token, err := pin.Issue(id, pin.Capability{Peer: other.PublicKey(), Expires: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = (Config{DataDir: t.TempDir(), ConversationKey: testKey, PinTokens: []string{hex.EncodeToString(token)}}).resolve()
	wantConfigError(t, "a token and no pin", err, pin.ErrUnauthorized, "pin-token")
	help := new(strings.Builder)
	if _, err := LoadConfig([]string{"-h"}, func(string) string { return "" }, help); err == nil {
		t.Fatal("-h stops the start")
	}
	if !strings.Contains(help.String(), "optional pins") || strings.Contains(help.String(), "one at least") {
		t.Errorf("the help says the pin is optional: %s", help)
	}
}

// T8 and T9: -max-conversations is 64 by default and 1 to 256, from the flag or its variable; -listen with a port
// other than 0 is a fixed port.
func TestMaxConversationsAndFixedPort(t *testing.T) {
	cfg := loadConfig(t, []string{"-max-conversations", "1"}, map[string]string{"KOLO_CHAT_MAX_CONVERSATIONS": "5"})
	if cfg.MaxConversations != 1 {
		t.Fatalf("the flag beats the variable: %d", cfg.MaxConversations)
	}
	if cfg = loadConfig(t, nil, map[string]string{"KOLO_CHAT_MAX_CONVERSATIONS": "256"}); cfg.MaxConversations != 256 {
		t.Fatalf("the variable: %d", cfg.MaxConversations)
	}
	for in, want := range map[int]int{0: defaultMaxConversations, 1: 1, 256: 256} {
		if set, err := (Config{DataDir: t.TempDir(), MaxConversations: in}).resolve(); err != nil || set.MaxConversations != want {
			t.Errorf("%d: %d, %v", in, set.MaxConversations, err)
		}
	}
	for listen, fixed := range map[string]bool{"127.0.0.1:0": false, "": false, "127.0.0.1:7447": true, "0.0.0.0:1": true, "nope": false} {
		if got := (settings{Config: Config{Listen: listen}}).fixedPort(); got != fixed {
			t.Errorf("%q: fixed %v", listen, got)
		}
	}
}

// Pins come as a repeatable, comma-separated flag, a variable, and files, in the forms Kolo uses elsewhere.
func TestPinListForms(t *testing.T) {
	keys, pins := testPins(t, 6)
	var ck []string
	ckEnv := map[string]string{"KOLO_CHAT_CONVERSATION_KEY": testKey}

	cfg := loadConfig(t, append(ck, "-pin", pins[0]+", "+pins[1], "-pin", pins[2]), map[string]string{"KOLO_CHAT_PIN": pins[5], "KOLO_CHAT_CONVERSATION_KEY": testKey})
	set, err := cfg.resolve()
	if err != nil || !slices.Equal(addrsOf(set.pins), []string{"10.0.0.1:7450", "10.0.0.2:7450", "10.0.0.3:7450"}) {
		t.Fatalf("flags replace the variable and keep their order: %v, %v", addrsOf(set.pins), err)
	}
	if hex.EncodeToString(set.pins[1].Key) != keys[1] {
		t.Fatalf("got key %x", set.pins[1].Key)
	}

	cfg = loadConfig(t, ck, map[string]string{"KOLO_CHAT_PIN": " " + pins[0] + " ,," + pins[1], "KOLO_CHAT_CONVERSATION_KEY": testKey})
	if !slices.Equal(cfg.Pins, pins[:2]) {
		t.Fatalf("the variable is a comma-separated list: %q", cfg.Pins)
	}

	dir := t.TempDir()
	file := filepath.Join(dir, "pins.txt")
	other := filepath.Join(dir, "more pins, too.txt")
	body := "\xef\xbb\xbf# the pins of the team\r\n" + pins[3] + " # first\r\n\r\n" + pins[4] + ", " + pins[0] + "\n"
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte(pins[5]), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg = loadConfig(t, append(ck, "-pin", pins[0], "-pin-file", file, "-pin-file", other), ckEnv)
	set, err = cfg.resolve()
	want := []string{"10.0.0.1:7450", "10.0.0.4:7450", "10.0.0.5:7450", "10.0.0.6:7450"}
	if err != nil || !slices.Equal(addrsOf(set.pins), want) {
		t.Fatalf("-pin, then each file, an exact repeat once: %v, %v", addrsOf(set.pins), err)
	}
	cfg = loadConfig(t, ck, map[string]string{"KOLO_CHAT_PIN_FILE": other, "KOLO_CHAT_CONVERSATION_KEY": testKey})
	if set, err := cfg.resolve(); err != nil || len(set.pins) != 1 || set.pins[0].Addr != "10.0.0.6:7450" {
		t.Fatalf("KOLO_CHAT_PIN_FILE: %+v, %v", set.pins, err)
	}

	cfg = loadConfig(t, append(ck, "-max-pins", "3"), map[string]string{"KOLO_CHAT_PIN_TOKEN": "aa, bb,cc"})
	if !slices.Equal(cfg.PinTokens, []string{"aa", "bb", "cc"}) || cfg.MaxPins != 3 {
		t.Fatalf("tokens: %q, max %d", cfg.PinTokens, cfg.MaxPins)
	}
}

// The conversation key and the pin tokens are secrets: a flag shows in the process list and the shell history, so only
// the variable and the file forms are accepted.
func TestSecretsAreNotFlags(t *testing.T) {
	for _, flag := range []string{"-conversation-key", "-pin-token"} {
		for _, args := range [][]string{{flag, testKey}, {flag + "=" + testKey}, {"-data", "x", "-" + flag, testKey}} {
			var out strings.Builder
			_, err := LoadConfig(args, func(string) string { return "" }, &out)
			if !errors.Is(err, ErrConfig) || !strings.Contains(err.Error(), "process list") || strings.Contains(err.Error()+out.String(), testKey) {
				t.Errorf("%v: %v, %q", args, err, out.String())
			}
		}
	}
}

// -conversation-key-file and -pin-token-file read the secrets from files that only their owner can open.
func TestSecretFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no mode bits")
	}
	dir := t.TempDir()
	write := func(name, body string, mode os.FileMode) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		return path
	}
	keyFile := write("key", " "+testKey+"\n", 0o600)
	tokFile := write("tokens", "# issued by me\naa, bb\ncc # last\n", 0o400)

	cfg := loadConfig(t, []string{"-conversation-key-file", keyFile, "-pin-token-file", tokFile}, map[string]string{"KOLO_CHAT_CONVERSATION_KEY": "ignored", "KOLO_CHAT_PIN_TOKEN": "ignored"})
	got, err := cfg.readSecrets()
	if err != nil || got.ConversationKey != testKey || !slices.Equal(got.PinTokens, []string{"aa", "bb", "cc"}) {
		t.Fatalf("%+v, %v", got, err)
	}
	cfg = loadConfig(t, nil, map[string]string{"KOLO_CHAT_CONVERSATION_KEY_FILE": keyFile, "KOLO_CHAT_PIN_TOKEN_FILE": tokFile})
	if got, err := cfg.readSecrets(); err != nil || got.ConversationKey != testKey || len(got.PinTokens) != 3 {
		t.Fatalf("variables of the files: %+v, %v", got, err)
	}
	set, err := Config{ConversationKeyFile: keyFile, DataDir: dir}.resolve()
	if err != nil || hex.EncodeToString(set.conversationKey[:]) != testKey || !set.inConversation {
		t.Fatalf("resolve: %v", err)
	}

	for name, tc := range map[string]struct {
		cfg     Config
		setting string
		want    error
	}{
		"key open to others":    {Config{ConversationKeyFile: write("open", testKey, 0o644)}, "conversation-key-file", ErrMalformed},
		"key file missing":      {Config{ConversationKeyFile: filepath.Join(dir, "none")}, "conversation-key-file", nil},
		"key file empty":        {Config{ConversationKeyFile: write("empty", " \n", 0o600)}, "conversation-key-file", ErrMalformed},
		"key file a folder":     {Config{ConversationKeyFile: dir}, "conversation-key-file", nil},
		"tokens open to others": {Config{PinTokenFiles: []string{write("opentok", "aa", 0o640)}}, "pin-token-file", ErrMalformed},
		"tokens file empty":     {Config{PinTokenFiles: []string{write("notok", "# nothing\n", 0o600)}}, "pin-token-file", ErrMalformed},
	} {
		_, err := tc.cfg.resolve()
		var ce *ConfigError
		if !errors.As(err, &ce) || ce.Setting != tc.setting || tc.want != nil && !errors.Is(err, tc.want) || strings.Contains(err.Error(), testKey) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Every bad pin is refused with the setting that holds it.
func TestPinListRefused(t *testing.T) {
	keys, pins := testPins(t, 3)
	base := Config{ConversationKey: testKey, DataDir: t.TempDir()}
	with := func(entries ...string) Config { c := base; c.Pins = entries; return c }
	for _, tc := range []struct {
		name  string
		cfg   Config
		want  error
		field string
	}{
		{"no @", with("10.0.0.1:7450"), ErrMalformed, "pin"},
		{"key not hex", with("10.0.0.1:7450@zz"), ErrMalformed, "pin"},
		{"short key", with("10.0.0.1:7450@" + keys[0][:32]), ErrMalformed, "pin"},
		{"a name", with("pin.example:7450@" + keys[0]), ErrMalformed, "pin"},
		{"IPv6", with("[::1]:7450@" + keys[0]), ErrMalformed, "pin"},
		{"IPv4-mapped IPv6", with("[::ffff:10.0.0.1]:7450@" + keys[0]), ErrMalformed, "pin"},
		{"port 0", with("10.0.0.1:0@" + keys[0]), ErrMalformed, "pin"},
		{"no port", with("10.0.0.1@" + keys[0]), ErrMalformed, "pin"},
		{"unspecified", with("0.0.0.0:7450@" + keys[0]), ErrMalformed, "pin"},
		{"multicast", with("224.0.0.1:7450@" + keys[0]), ErrMalformed, "pin"},
		{"one key at two addresses", with(pins[0], "10.0.0.9:7450@"+keys[0]), ErrMalformed, "pin"},
		{"more than -max-pins", func() Config { c := with(pins...); c.MaxPins = 2; return c }(), ErrTooMany, "pin"},
	} {
		_, err := tc.cfg.resolve()
		wantConfigError(t, tc.name, err, tc.want, tc.field)
	}
	c := with(pins[:2]...)
	c.MaxPins = 2
	if set, err := c.resolve(); err != nil || len(set.pins) != 2 {
		t.Fatalf("exactly -max-pins is fine: %v", err)
	}
	if set, err := with(manyPins(t, 17)...).resolve(); err != nil || len(set.pins) != 17 {
		t.Fatalf("without -max-pins any number is fine: %v", err)
	}

	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	files := func(paths ...string) Config { c := base; c.PinFiles = paths; return c }
	_, err := files(filepath.Join(dir, "missing")).resolve()
	wantConfigError(t, "a missing file", err, fs.ErrNotExist, "pin-file")
	_, err = files(dir).resolve()
	wantConfigError(t, "a directory", err, ErrMalformed, "pin-file")
	_, err = files(write("big", pins[0]+"\n"+strings.Repeat("#", maxPinFile))).resolve()
	wantConfigError(t, "a file over the bound", err, ErrMalformed, "pin-file")
	if set, err := files(write("full", pins[0]+"\n#"+strings.Repeat("#", maxPinFile-len(pins[0])-3)+"\n")).resolve(); err != nil || len(set.pins) != 1 {
		t.Fatalf("a file of exactly the bound is read: %v", err)
	}
	_, err = files(write("bad", "# ok\n"+pins[0]+"\n10.0.0.1:7450\n")).resolve()
	wantConfigError(t, "a bad line", err, ErrMalformed, "pin-file")
	if err == nil || !strings.Contains(err.Error(), "line 3") {
		t.Errorf("the error names the line: %v", err)
	}
	_, err = files(write("empty", "# nothing yet\n")).resolve()
	wantConfigError(t, "a file without pins", err, ErrMalformed, "pin-file")
}

// manyPins returns n valid entries with distinct keys.
func manyPins(t *testing.T, n int) []string {
	t.Helper()
	var out []string
	for i := range n {
		id, err := koloid.Generate()
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("10.0.1.%d:7450@%s", i+1, id.PublicKeyString()))
	}
	return out
}

// A token from the configuration goes to the pin it names; the other pins get one issued by the app, each
// kept in its own file.
func TestTokensPerPin(t *testing.T) {
	dir := dataDir(t)
	id, err := loadIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	keys, pins := testPins(t, 3)
	key := func(i int) ed25519.PublicKey { b, _ := hex.DecodeString(keys[i]); return b }
	logs := []kololog.LogID{{1}, {2}}
	issue := func(peer []byte) string {
		b, err := pin.Issue(id, pin.Capability{Peer: peer, Expires: time.Now().Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		return hex.EncodeToString(b)
	}
	given := issue(key(1))
	cfg := Config{DataDir: dir, ConversationKey: testKey, Pins: pins[:2], PinTokens: []string{given}}
	set, err := cfg.resolve()
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := capabilities(set, id, logs...)
	if err != nil || len(tokens) != 2 || hex.EncodeToString(tokens[1]) != given {
		t.Fatalf("the given token goes to its pin: %v", err)
	}
	c, err := pin.ParseCapability(tokens[0])
	if err != nil || !c.Peer.Equal(key(0)) || !c.Owner.Equal(id.PublicKey()) || !c.Covers(logs[0]) || !c.Covers(logs[1]) || len(c.Logs) != 2 {
		t.Fatalf("the issued token: %+v, %v", c, err)
	}
	fi, err := os.Stat(filepath.Join(dir, "pin-tokens", keys[0]))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("the issued token is kept, mode 0600: %v, %v", fi, err)
	}
	if fi, err := os.Stat(filepath.Join(dir, "pin-tokens")); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("pin-tokens: %v, %v", fi, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "pin-tokens", keys[1])); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a given token is not written: %v", err)
	}
	again, err := capabilities(set, id, logs...)
	if err != nil || !slices.Equal(again[0], tokens[0]) {
		t.Fatalf("the kept token is reused: %v", err)
	}

	for _, tc := range []struct {
		name   string
		tokens []string
		want   error
	}{
		{"not hex", []string{"zz"}, ErrMalformed},
		{"not a capability", []string{"00ff00ff"}, pin.ErrInvalidCapability},
		{"a pin that is not in the list", []string{issue(key(2))}, pin.ErrUnauthorized},
		{"two for one pin", []string{given, issue(key(1))}, ErrMalformed},
	} {
		c := cfg
		c.PinTokens = tc.tokens
		_, err := c.resolve()
		wantConfigError(t, tc.name, err, tc.want, "pin-token")
	}
}

// The window shows an error only when no pin works, and a warning when some do not.
func TestPinStatusSummary(t *testing.T) {
	pins := []pinRef{{Addr: "10.0.0.1:7450", Key: make([]byte, 32)}, {Addr: "10.0.0.2:7450", Key: make([]byte, 32)}}
	pins[1].Key[0] = 1
	if st := pinStatus(pins, []pinState{{}, {}}); st.Error != "" || st.Warning != "" || len(st.Pins) != 2 ||
		st.Pins[1].Addr != "10.0.0.2:7450" || st.Pins[1].Key != hex.EncodeToString(pins[1].Key) {
		t.Fatalf("all working: %+v", st)
	}
	st := pinStatus(pins, []pinState{{}, {Push: "down"}})
	if st.Error != "" || !strings.HasPrefix(st.Warning, "1 of 2 pins") || !strings.Contains(st.Warning, "down") || st.Pins[1].Push != "down" {
		t.Fatalf("one failing: %+v", st)
	}
	st = pinStatus(pins, []pinState{{Pull: "no pull"}, {}})
	if st.Error != "" || !strings.Contains(st.Warning, "no pull") {
		t.Fatalf("a failed pull counts too: %+v", st)
	}
	st = pinStatus(pins, []pinState{{Push: "first"}, {Pull: "second"}})
	if st.Error != "first" || st.Warning != "" {
		t.Fatalf("none working: %+v", st)
	}
	if st := pinStatus(nil, nil); st.Error != "" || st.Warning != "" {
		t.Fatalf("no pins yet: %+v", st)
	}
}
