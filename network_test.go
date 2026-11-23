package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	kolodht "github.com/ciranda-dev/kolo-dht"
	koloswarm "github.com/ciranda-dev/kolo-swarm"
)

// publicTouched counts the starts that would have bootstrapped from the public network.
var publicTouched atomic.Int32

var errPublicInTest = errors.New("a test tried to bootstrap from the public network: pass -bootstrap to a loopback node")

// TestMain keeps every test off the public network: a start without -bootstrap fails instead of sending a packet.
func TestMain(m *testing.M) {
	real := bootstrapSwarm
	bootstrapSwarm = func(ctx context.Context, sw *koloswarm.Swarm, set settings) error {
		if len(set.bootstrap) == 0 {
			publicTouched.Add(1)
			return errPublicInTest
		}
		return real(ctx, sw, set)
	}
	code := m.Run()
	if n := publicTouched.Load(); n != 0 && code == 0 {
		fmt.Fprintf(os.Stderr, "%d starts used the public network\n", n)
		code = 1
	}
	os.Exit(code)
}

// withBootstrap replaces what a start calls to join the network, until the test ends.
func withBootstrap(t *testing.T, f func(context.Context, *koloswarm.Swarm, settings) error) {
	t.Helper()
	old := bootstrapSwarm
	bootstrapSwarm = f
	t.Cleanup(func() { bootstrapSwarm = old })
}

func resolveArgs(t *testing.T, args []string, env map[string]string) (settings, error) {
	t.Helper()
	cfg, err := LoadConfig(args, func(k string) string { return env[k] }, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	cfg.DataDir = t.TempDir()
	return cfg.resolve()
}

// The default is the public network: no entry node is set, so kolo-swarm uses the list built into kolo-dht, and the
// app listens beyond loopback to reach it.
func TestDefaultIsThePublicNetwork(t *testing.T) {
	set, err := resolveArgs(t, nil, nil)
	if err != nil || len(set.bootstrap) != 0 {
		t.Fatalf("default: %v, %v", set.bootstrap, err)
	}
	if set.Listen != publicListen {
		t.Errorf("Listen = %q, want %q", set.Listen, publicListen)
	}
	net := set.network()
	if net.Mode != "public" || net.Nodes == nil || len(net.Nodes) != 0 {
		t.Errorf("network = %+v, want the public mode with an empty list", net)
	}
	raw, err := json.Marshal(net)
	if err != nil || string(raw) != `{"mode":"public","nodes":[]}` {
		t.Errorf("payload = %s, %v: no public address goes to the window", raw, err)
	}
	for _, n := range kolodht.PublicServers() {
		if strings.Contains(string(raw), n.String()) {
			t.Errorf("payload %s names the public entry node %s", raw, n)
		}
	}
}

// -bootstrap alone is your own network, used alone: the public list is not in the settings.
func TestBootstrapIsYourOwnNetwork(t *testing.T) {
	set, err := resolveArgs(t, []string{"-bootstrap", "127.0.0.1:4000, 127.0.0.1:4001,127.0.0.1:4000"}, nil)
	if err != nil || !slices.Equal(set.bootstrap, []string{"127.0.0.1:4000", "127.0.0.1:4001"}) {
		t.Fatalf("own: %v, %v", set.bootstrap, err)
	}
	if net := set.network(); net.Mode != "own" || !slices.Equal(net.Nodes, set.bootstrap) {
		t.Errorf("network = %+v", net)
	}
	if set.Listen != loopbackListen {
		t.Errorf("Listen = %q: every entry node is on loopback, want %q", set.Listen, loopbackListen)
	}

	set, err = resolveArgs(t, nil, map[string]string{"KOLO_CHAT_BOOTSTRAP": "192.168.1.5:7447"})
	if err != nil || !slices.Equal(set.bootstrap, []string{"192.168.1.5:7447"}) || set.Listen != publicListen {
		t.Errorf("env: %v, %q, %v", set.bootstrap, set.Listen, err)
	}
	set, err = resolveArgs(t, []string{"-bootstrap", "127.0.0.1:4000", "-listen", "10.0.0.2:0"}, nil)
	if err != nil || set.Listen != "10.0.0.2:0" {
		t.Errorf("a given -listen wins: %q, %v", set.Listen, err)
	}
}

func TestBadBootstrapIsRefused(t *testing.T) {
	for _, v := range []string{"x", "127.0.0.1", "127.0.0.1:0", "[::1]:4000", "[::ffff:127.0.0.1]:4000", ",", " "} {
		_, err := (Config{DataDir: t.TempDir(), Bootstrap: v}).resolve()
		wantConfigError(t, v, err, ErrMalformed, "bootstrap")
	}
}

func TestBootstrapTakesAnyNumberOfNodes(t *testing.T) {
	var many []string
	for i := range 17 {
		many = append(many, fmt.Sprintf("10.0.0.%d:7447", i+1))
	}
	set, err := (Config{DataDir: t.TempDir(), Bootstrap: strings.Join(many, ",")}).resolve()
	if err != nil || !slices.Equal(set.bootstrap, many) {
		t.Fatalf("17 nodes: %v, %v", set.bootstrap, err)
	}
}

// A start without -bootstrap reaches kolo-swarm with an empty BootstrapNodes, which is the public network; with
// -bootstrap the swarm gets the list alone.
func TestStartPassesTheNetworkToTheSwarm(t *testing.T) {
	var got []string
	var called bool
	withBootstrap(t, func(_ context.Context, sw *koloswarm.Swarm, set settings) error {
		called, got = true, set.bootstrap
		return errors.New("stop here")
	})
	c := NewChatService(Config{DataDir: dataDir(t)}, func(string, any) {})
	if _, err := c.Start(); err == nil || !called || len(got) != 0 {
		t.Fatalf("default: called %v, nodes %v, %v", called, got, err)
	}
	c = NewChatService(Config{DataDir: dataDir(t), Bootstrap: "127.0.0.1:4000"}, func(string, any) {})
	if _, err := c.Start(); err == nil || !slices.Equal(got, []string{"127.0.0.1:4000"}) {
		t.Fatalf("own: nodes %v, %v", got, err)
	}
}

// When no entry node answers, the room still starts, says it is offline and asks again by itself; the sentence that
// names the three ways to connect is the NetworkError's, which the log and a join show.
func TestUnreachableNetworkNamesTheOptions(t *testing.T) {
	for _, tc := range []struct {
		name, bootstrap, mode string
	}{
		{"public", "", "public"},
		{"own", "127.0.0.1:9", "own"},
	} {
		var calls atomic.Int32
		withBootstrap(t, func(context.Context, *koloswarm.Swarm, settings) error {
			calls.Add(1)
			return fmt.Errorf("koloswarm: %w", kolodht.ErrNoNodes)
		})
		c := NewChatService(Config{DataDir: dataDir(t), Bootstrap: tc.bootstrap}, func(string, any) {})
		c.tune = pace{retryMin: 20 * time.Millisecond, retryMax: 50 * time.Millisecond}
		if _, err := c.Start(); err != nil {
			t.Fatalf("%s: Start: %v", tc.name, err)
		}
		if c.Network() != "offline" {
			t.Errorf("%s: state %q, want offline", tc.name, c.Network())
		}
		eventually(t, tc.name+": a retry", func() bool { return calls.Load() >= 3 })
		c.ServiceShutdown()

		err := bootstrap(context.Background(), nil, settings{Config: Config{Bootstrap: tc.bootstrap}, bootstrap: nodesOf(tc.bootstrap)})
		var ne *NetworkError
		if !errors.Is(err, ErrNetwork) || !errors.Is(err, kolodht.ErrNoNodes) || !errors.As(err, &ne) || ne.Network.Mode != tc.mode {
			t.Fatalf("%s: %v", tc.name, err)
		}
		msg := err.Error()
		for _, want := range []string{"ublic network", "-bootstrap host:port", "-first", "-bootstrap 127.0.0.1:PORT", "Ciranda", "no uptime guarantee"} {
			if !strings.Contains(msg, want) {
				t.Errorf("%s: %q lacks %q", tc.name, msg, want)
			}
		}
		if strings.Contains(msg, "-local") || strings.Contains(msg, string(rune(0x2014))) {
			t.Errorf("%s: %q", tc.name, msg)
		}
		if tc.mode == "own" && !strings.Contains(msg, "127.0.0.1:9") {
			t.Errorf("%s: %q does not name the entry nodes", tc.name, msg)
		}
	}
}

func nodesOf(list string) []string {
	if list == "" {
		return nil
	}
	return []string{list}
}

// Another failure is not turned into a network error.
func TestOtherBootstrapErrorsPassThrough(t *testing.T) {
	other := errors.New("boom")
	withBootstrap(t, func(context.Context, *koloswarm.Swarm, settings) error { return other })
	c := NewChatService(Config{DataDir: dataDir(t)}, func(string, any) {})
	if _, err := c.Start(); !errors.Is(err, other) || errors.Is(err, ErrNetwork) {
		t.Fatalf("got %v", err)
	}
}

// A conversation whose network does not answer is listed as offline, not as failed.
func TestUnreachableNetworkInTheList(t *testing.T) {
	withBootstrap(t, func(context.Context, *koloswarm.Swarm, settings) error { return kolodht.ErrNoNodes })
	_, pins := testPins(t, 1)
	app, _ := startApp(t, Config{DataDir: dataDir(t), Bootstrap: "127.0.0.1:9", Pins: pins})
	app.pace = pace{}
	info, err := app.Create("x")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if info.State != "offline" || info.Error != "" {
		t.Errorf("row = %+v, want offline and no error", info)
	}
	if app.info().Network.Mode != "own" {
		t.Errorf("info = %+v", app.info())
	}
}
