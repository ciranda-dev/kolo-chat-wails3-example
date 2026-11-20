package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	kolodht "github.com/ciranda-dev/kolo-dht"
	koloid "github.com/ciranda-dev/kolo-id"
	invite "github.com/ciranda-dev/kolo-invite"
	kolosecure "github.com/ciranda-dev/kolo-secure"
	koloswarm "github.com/ciranda-dev/kolo-swarm"
)

// convEvents records what Conversations tells the window: the events of each conversation, "" for the app's own,
// and every list of conversations.
type convEvents struct {
	mu     sync.Mutex
	byConv map[string]*events
	lists  [][]ConversationInfo
}

func (e *convEvents) emit(name string, data any) {
	if ev, ok := data.(ConvEvent); ok {
		e.of(ev.Conv).emit(name, ev.Data)
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if l, ok := data.([]ConversationInfo); ok && name == "chat:conversations" {
		e.lists = append(e.lists, l)
	}
}

// of returns the events of one conversation.
func (e *convEvents) of(conv string) *events {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.byConv == nil {
		e.byConv = map[string]*events{}
	}
	if e.byConv[conv] == nil {
		e.byConv[conv] = &events{}
	}
	return e.byConv[conv]
}

func (e *convEvents) lastList() []ConversationInfo {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.lists) == 0 {
		return nil
	}
	return e.lists[len(e.lists)-1]
}

// app is the configuration of an app of the network n with its data in root, and the pin for new conversations.
func (n network) app(root string) Config {
	return Config{DataDir: root, Bootstrap: n.bootstrap, Pins: []string{pinEntry(n.pin)}, LookupInterval: time.Second}
}

// startApp runs the app's service with cfg and stops it when the test ends.
func startApp(t *testing.T, cfg Config) (*Conversations, *convEvents) {
	t.Helper()
	ev := &convEvents{}
	a := NewConversations(cfg, ev.emit)
	if _, err := a.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.ServiceShutdown() })
	return a, ev
}

func mustCreateConv(t *testing.T, a *Conversations, name string) ConversationInfo {
	t.Helper()
	info, err := a.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

// engineOf returns the engine of a conversation.
func engineOf(t *testing.T, a *Conversations, conv string) *ChatService {
	t.Helper()
	c, err := a.engine(conv)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// folders lists the conversation folders under root.
func folders(t *testing.T, root string) []string {
	t.Helper()
	items, err := os.ReadDir(filepath.Join(root, conversationDir))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, it := range items {
		out = append(out, it.Name())
	}
	return out
}

// snapshot is every path under root with its mode and, for a file, its contents.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		v := info.Mode().String()
		if d.Type().IsRegular() {
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			v += " " + string(b)
		}
		out[path] = v
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// T1: a data folder of 0.1.9 or older is refused with the folder named, whichever of its names it holds, and nothing
// in it changes: no file is added, moved, deleted or rewritten.
func TestOldDataFolderIsRefused(t *testing.T) {
	// The names are written out here, not taken from oldLayoutNames, so a shorter list fails this test.
	for _, name := range []string{"identity.key", "conversation.json", "members.json", "directory-log", "store", "invites", "pin-tokens"} {
		t.Run(name, func(t *testing.T) {
			root := dataDir(t)
			path := filepath.Join(root, name)
			var err error
			if name == "store" || name == "invites" || name == "pin-tokens" {
				if err = os.Mkdir(path, 0o700); err == nil {
					err = os.WriteFile(filepath.Join(path, "kept"), []byte("old data"), 0o600)
				}
			} else {
				err = os.WriteFile(path, []byte("old "+name), 0o600)
			}
			if err == nil {
				err = os.WriteFile(filepath.Join(root, notchSettingsFile), []byte(`{"enabled":false}`), 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			before := snapshot(t, root)
			_, err = NewConversations(Config{DataDir: root}, (&convEvents{}).emit).Start()
			var de *DataDirError
			if !errors.As(err, &de) || de.Layout != 1 || de.Dir != root || !errors.Is(err, ErrOldLayout) ||
				errors.Is(err, ErrOldDataDirectory) || !strings.Contains(err.Error(), root) || !strings.Contains(err.Error(), "0.1.9 or older") {
				t.Fatalf("got %v", err)
			}
			if after := snapshot(t, root); !mapsEqual(after, before) {
				t.Fatalf("the folder changed:\n%v\n%v", before, after)
			}
		})
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// T2: a layout.json of another version, or a damaged one, is refused with its layout and nothing changes.
func TestOtherLayoutIsRefused(t *testing.T) {
	for body, want := range map[string]int{`{"layout":4}`: 4, `{"layout":`: -1, `{}`: -1, `{"layout":1}`: -1} {
		root := dataDir(t)
		if err := os.WriteFile(filepath.Join(root, layoutFile), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		before := snapshot(t, root)
		_, err := NewConversations(Config{DataDir: root}, (&convEvents{}).emit).Start()
		var de *DataDirError
		if !errors.As(err, &de) || de.Layout != want || de.Dir != root || errors.Is(err, ErrOldLayout) ||
			!strings.Contains(err.Error(), fmt.Sprintf("(layout %d)", want)) {
			t.Fatalf("%s: got %v", body, err)
		}
		if after := snapshot(t, root); !mapsEqual(after, before) {
			t.Fatalf("%s: the folder changed", body)
		}
	}
}

// S7-1: a member's folder that lost conversation.json is not an unfinished join: a start refuses it with the folder to
// move aside and deletes nothing, while a join that never got a welcome is removed.
func TestMemberFolderWithoutConversationIsKept(t *testing.T) {
	n := newNetwork(t)
	alice, _ := startApp(t, n.app(dataDir(t)))
	conv := mustCreateConv(t, alice, "Kept")
	af := engineOf(t, alice, conv.ID)
	// Bob wrote a message, Carol nothing: only her braid tells her folder from an unfinished join.
	var dirs []string
	for _, writes := range []bool{true, false} {
		inv, err := alice.CreateInvite(conv.ID, InviteOptions{})
		if err != nil {
			t.Fatal(err)
		}
		cfg := Config{DataDir: dataDir(t), Bootstrap: n.bootstrap, LookupInterval: time.Second}
		app, _ := startApp(t, cfg)
		info := joinConv(t, af, app, inv.Code, "Kept")
		if writes {
			if err := app.Send(info.ID, defaultRoom, "a message to keep"); err != nil {
				t.Fatal(err)
			}
		}
		app.ServiceShutdown()
		dir := filepath.Join(cfg.DataDir, conversationDir, info.ID)
		if err := os.Remove(filepath.Join(dir, "conversation.json")); err != nil {
			t.Fatal(err)
		}
		again, _ := startApp(t, cfg)
		l := again.List()
		if len(l) != 1 || l[0].ID != info.ID || l[0].Folder != dir || !strings.Contains(l[0].Error, "conversation.json is gone") {
			t.Fatalf("writes %v, the row: %+v", writes, l)
		}
		dirs = append(dirs, dir)
	}
	for _, dir := range dirs {
		for _, name := range []string{"identity.key", "store", "braid", "members.json", localFile} {
			if !exists(t, filepath.Join(dir, name)) {
				t.Fatalf("%s/%s was deleted", dir, name)
			}
		}
	}

	// S7-11: Dave never wrote, and lost conversation.json, members.json, his pin tokens and his braid. His store (the
	// copy of Alice's log, and his own directory log, which kolo-kv began) tells his folder from an unfinished join.
	if err := alice.Send(conv.ID, defaultRoom, "for Dave to copy"); err != nil {
		t.Fatal(err)
	}
	inv, err := alice.CreateInvite(conv.ID, InviteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{DataDir: dataDir(t), Bootstrap: n.bootstrap, LookupInterval: time.Second}
	dave, dev := startApp(t, cfg)
	info := joinConv(t, af, dave, inv.Code, "Kept")
	eventually(t, "Dave's copy of Alice's log", func() bool { return hasText(dev.of(info.ID).messages(), "for Dave to copy") })
	dave.ServiceShutdown()
	dir := filepath.Join(cfg.DataDir, conversationDir, info.ID)
	for _, name := range []string{"conversation.json", "members.json", "pin-tokens", "braid"} {
		if err := os.RemoveAll(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "braid"), 0o700); err != nil {
		t.Fatal(err)
	}
	again, _ := startApp(t, cfg)
	if l := again.List(); len(l) != 1 || l[0].Folder != dir || !strings.Contains(l[0].Error, "conversation.json is gone") || !exists(t, dir) {
		t.Fatalf("Dave's folder: %+v", l)
	}

	// Erin lost conversation.json, members.json, her pin tokens and her store: only her completed braid tells her folder from an
	// unfinished join.
	inv, err = alice.CreateInvite(conv.ID, InviteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cfg = Config{DataDir: dataDir(t), Bootstrap: n.bootstrap, LookupInterval: time.Second}
	erin, _ := startApp(t, cfg)
	info = joinConv(t, af, erin, inv.Code, "Kept")
	erin.ServiceShutdown()
	dir = filepath.Join(cfg.DataDir, conversationDir, info.ID)
	for _, name := range []string{"conversation.json", "members.json", "pin-tokens", "store"} {
		if err := os.RemoveAll(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "store"), 0o700); err != nil {
		t.Fatal(err)
	}
	again, _ = startApp(t, cfg)
	if l := again.List(); len(l) != 1 || l[0].Folder != dir || !strings.Contains(l[0].Error, "conversation.json is gone") || !exists(t, dir) {
		t.Fatalf("Erin's folder: %+v", l)
	}
}

// S7-11: what a folder without conversation.json may hold to be taken for a join that did not finish. A kolo-id
// without the stores its join makes with it may be on a member list, so it is refused.
func TestOnlyJoinFiles(t *testing.T) {
	for name, tc := range map[string]struct {
		files, dirs []string
		key, ok     bool
	}{
		"empty":                  {ok: true},
		"a name only":            {files: []string{localFile}, ok: true},
		"a join's files":         {files: []string{localFile, "directory-log"}, dirs: []string{"store", "braid", "invites"}, key: true, ok: true},
		"a cut write":            {files: []string{localFile, localFile + tmpSuffix}, ok: true},
		"a cut write of another": {files: []string{localFile, "members.json" + tmpSuffix}},
		"members.json":           {files: []string{localFile, "members.json"}, dirs: []string{"store", "braid"}, key: true},
		"pin tokens":             {dirs: []string{"store", "braid", "pin-tokens"}, key: true},
		"a kolo-id alone":        {files: []string{localFile}, key: true},
		"a kolo-id with a store": {dirs: []string{"store"}, key: true},
	} {
		dir := dataDir(t)
		for _, f := range tc.files {
			if err := os.WriteFile(filepath.Join(dir, f), []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		for _, d := range tc.dirs {
			if err := os.Mkdir(filepath.Join(dir, d), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		if tc.key {
			identityIn(t, dir)
		}
		err := onlyJoinFiles(dir)
		var de *DataDirError
		if tc.ok != (err == nil) || !tc.ok && (!errors.As(err, &de) || de.Dir != dir) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// A conversation.json that is a FIFO, a dangling link or a file nobody may read never hangs a start: the row says why,
// and the folder and the file stay.
func TestSpecialConversationFileKeepsTheFolder(t *testing.T) {
	n := newNetwork(t)
	root := dataDir(t)
	a, _ := startApp(t, n.app(root))
	a.ServiceShutdown()
	kinds := map[string]func(string) error{
		"fifo":     mkfifo,
		"dangling": func(p string) error { return os.Symlink(filepath.Join(filepath.Dir(p), "nowhere"), p) },
		"mode 000": func(p string) error { return os.WriteFile(p, []byte("{}"), 0) },
	}
	made := map[string]string{}
	for kind, create := range kinds {
		dir := filepath.Join(root, conversationDir, fmt.Sprintf("%032x", len(made)+1))
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := create(filepath.Join(dir, "conversation.json")); err != nil {
			t.Logf("%s: %v", kind, err) // no FIFO on this system
			os.RemoveAll(dir)
			continue
		}
		made[kind] = dir
	}
	// S7-11: a folder with members.json and no conversation.json is no join that did not finish.
	members := filepath.Join(root, conversationDir, fmt.Sprintf("%032x", 9))
	if err := os.Mkdir(members, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(members, "members.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := NewConversations(n.app(root), (&convEvents{}).emit)
	started := make(chan error, 1)
	go func() { _, err := b.Start(); started <- err }()
	select {
	case err := <-started:
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { b.ServiceShutdown() }) // only once the start returned: a hung one holds its lock
	case <-time.After(30 * time.Second):
		t.Fatal("the start hangs on a special conversation.json")
	}
	if !exists(t, filepath.Join(members, "members.json")) {
		t.Fatal("a folder with members.json was deleted")
	}
	var rows []ConversationInfo
	for _, r := range b.List() {
		if r.ID != filepath.Base(members) {
			rows = append(rows, r)
		}
	}
	if len(rows) != len(made) {
		t.Fatalf("rows %+v", rows)
	}
	for _, r := range rows {
		if !strings.Contains(r.Error, "conversation.json") || r.Folder == "" {
			t.Errorf("a special conversation.json started: %+v", r)
		}
	}
	for kind, dir := range made {
		if _, err := os.Lstat(filepath.Join(dir, "conversation.json")); err != nil {
			t.Errorf("%s: %v", kind, err)
		}
	}
}

// S8: a data folder of 0.1.10, layout 2, is refused with its layout and the sentence to move it, and nothing in it
// changes: its conversations have no braid, and there is no migration.
func TestLayoutTwoIsRefused(t *testing.T) {
	root := dataDir(t)
	if err := writeJSON(filepath.Join(root, layoutFile), map[string]int{"layout": 2}); err != nil {
		t.Fatal(err)
	}
	conv := filepath.Join(root, conversationDir, strings.Repeat("ab", 16))
	if err := os.MkdirAll(conv, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(conv, "conversation.json"), []byte(`{"key":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, root)
	_, err := NewConversations(Config{DataDir: root}, (&convEvents{}).emit).Start()
	var de *DataDirError
	if !errors.As(err, &de) || de.Layout != 2 || de.Dir != root || !strings.Contains(err.Error(), "0.1.10") {
		t.Fatalf("got %v", err)
	}
	if after := snapshot(t, root); !mapsEqual(after, before) {
		t.Fatal("the folder changed")
	}
}

// T3: a new data folder, or one with only notch.json, gets layout.json and conversations/, both private, and starts
// again as it is, also after conversations/ was deleted.
func TestNewDataFolderGetsTheLayout(t *testing.T) {
	for _, withNotch := range []bool{false, true} {
		root := filepath.Join(dataDir(t), "new")
		if withNotch {
			if err := saveNotchSettings(root, NotchSettings{Enabled: true}); err != nil {
				t.Fatal(err)
			}
		}
		for range 2 {
			a := NewConversations(Config{DataDir: root}, (&convEvents{}).emit)
			info, err := a.Start()
			if err != nil || len(info.Conversations) != 0 || info.Conversations == nil || info.HasPins || info.MaxConversations != defaultMaxConversations {
				t.Fatalf("notch.json %v: %+v, %v", withNotch, info, err)
			}
			a.ServiceShutdown()
		}
		// conversations/ deleted by hand: the app starts with none, and makes it again, private.
		if err := os.Remove(filepath.Join(root, conversationDir)); err != nil {
			t.Fatal(err)
		}
		a := NewConversations(Config{DataDir: root}, (&convEvents{}).emit)
		if _, err := a.Start(); err != nil {
			t.Fatalf("without conversations/: %v", err)
		}
		a.ServiceShutdown()
		if b, err := os.ReadFile(filepath.Join(root, layoutFile)); err != nil || string(b) != `{"layout":3}` {
			t.Fatalf("layout.json: %q, %v", b, err)
		}
		for path, mode := range map[string]os.FileMode{layoutFile: 0o600, conversationDir: os.ModeDir | 0o700} {
			if st, err := os.Stat(filepath.Join(root, path)); err != nil || st.Mode() != mode {
				t.Fatalf("%s: %v, %v", path, st.Mode(), err)
			}
		}
	}
}

// The app's list and AppInfo reach the window as JSON lists, never null: Go marshals a nil slice as null.
func TestEmptyListsAreNotNull(t *testing.T) {
	ev := &convEvents{}
	a := NewConversations(Config{DataDir: dataDir(t)}, ev.emit)
	info, err := a.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer a.ServiceShutdown()
	for what, v := range map[string]any{"AppInfo": info, "List": a.List(), "the event": ev.lastList()} {
		b, err := json.Marshal(v)
		if err != nil || strings.Contains(string(b), "null") {
			t.Fatalf("%s: %s, %v", what, b, err)
		}
	}
}

// T4, T5 and T12. Alice starts two conversations: two folders, two kolo-ids, two topics. Bob joins the second while he
// is in the first, as Alice's other kolo-id is a stranger to him. A message goes to its own conversation only, with its
// ID on the event. Both conversations keep their logs on the one pin, and Bob, back while Alice is offline, reads each
// from it in its own conversation.
func TestConversationsStayApart(t *testing.T) {
	n := newNetwork(t)
	aroot, broot := dataDir(t), dataDir(t)
	alice, _ := startApp(t, n.app(aroot))
	fam, work := mustCreateConv(t, alice, "Family"), mustCreateConv(t, alice, "Work")
	af, aw := engineOf(t, alice, fam.ID), engineOf(t, alice, work.ID)
	if fam.ID == work.ID || fam.Me == work.Me || af.topic() == aw.topic() || af.set.conversationKey == aw.set.conversationKey ||
		af.set.DataDir != filepath.Join(aroot, conversationDir, fam.ID) || aw.set.DataDir != filepath.Join(aroot, conversationDir, work.ID) {
		t.Fatalf("the conversations share something: %+v %+v", fam, work)
	}

	bob, bev := startApp(t, Config{DataDir: broot, Bootstrap: n.bootstrap, LookupInterval: time.Second})
	join := func(conv, name string) ConversationInfo {
		t.Helper()
		inv, err := alice.CreateInvite(conv, InviteOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return joinConv(t, engineOf(t, alice, conv), bob, inv.Code, name)
	}
	bfam := join(fam.ID, "Family")
	began := time.Now()
	bwork := join(work.ID, "Work")
	if d := time.Since(began); d > joinFindTimeout {
		t.Fatalf("the second join took %v", d)
	}
	if bfam.ID == bwork.ID || bfam.Me == bwork.Me || !bfam.Joined || bwork.Name != "Work" {
		t.Fatalf("Bob's conversations: %+v %+v", bfam, bwork)
	}
	linked(t, af, engineOf(t, bob, bfam.ID))
	linked(t, aw, engineOf(t, bob, bwork.ID))

	if err := alice.Send(fam.ID, defaultRoom, "for the family"); err != nil {
		t.Fatal(err)
	}
	if err := alice.Send(work.ID, defaultRoom, "for work"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "each message in its conversation", func() bool {
		return hasText(bev.of(bfam.ID).messages(), "for the family") && hasText(bev.of(bwork.ID).messages(), "for work")
	})
	time.Sleep(time.Second)
	if hasText(bev.of(bfam.ID).messages(), "for work") || hasText(bev.of(bwork.ID).messages(), "for the family") ||
		len(bev.of("").messages()) != 0 {
		t.Fatal("a message reached another conversation")
	}

	bob.ServiceShutdown()
	for conv, text := range map[string]string{fam.ID: "family while Bob is away", work.ID: "work while Bob is away"} {
		if err := alice.Send(conv, defaultRoom, text); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, "the pin to hold both conversations", func() bool { return holdsBoth(t, n.pin, af) && holdsBoth(t, n.pin, aw) })
	if heldBy(t, n.pin, af).Entries != 2 || heldBy(t, n.pin, aw).Entries != 2 {
		t.Fatal("one conversation's capability took the other's logs off the pin")
	}
	alice.ServiceShutdown()

	_, b2ev := startApp(t, Config{DataDir: broot, Bootstrap: n.bootstrap, LookupInterval: time.Second})
	fromPin := func(conv, text string) bool {
		return slices.ContainsFunc(b2ev.of(conv).messages(), func(m ChatMsg) bool { return m.Text == text && m.Via == "pin" })
	}
	eventually(t, "each conversation from the pin", func() bool {
		return fromPin(bfam.ID, "family while Bob is away") && fromPin(bwork.ID, "work while Bob is away")
	})
	if hasText(b2ev.of(bfam.ID).messages(), "work while Bob is away") {
		t.Fatal("the pin's message reached another conversation")
	}
}

// T7: -conversation-key starts the folder that holds that key, or one new folder for it, once.
func TestConversationKeySetting(t *testing.T) {
	n := newNetwork(t)
	root := dataDir(t)
	cfg := n.app(root)
	cfg.ConversationKey = testKey
	keyOf := func(id string) string {
		v, _, err := loadConversation(filepath.Join(root, conversationDir, id), defaultMaxPins)
		if err != nil {
			t.Fatal(err)
		}
		return hex.EncodeToString(v.Key)
	}

	first, _ := startApp(t, cfg)
	list := first.List()
	if len(list) != 1 || !list[0].Joined || !list[0].Admin || list[0].Name != defaultConvName || keyOf(list[0].ID) != testKey {
		t.Fatalf("first start: %+v", list)
	}
	id := list[0].ID
	first.ServiceShutdown()

	second, _ := startApp(t, cfg)
	other := mustCreateConv(t, second, "Other")
	second.ServiceShutdown()
	if got := folders(t, root); len(got) != 2 || !slices.Contains(got, id) {
		t.Fatalf("folders after two starts: %v", got)
	}

	third, _ := startApp(t, cfg)
	for conv, want := range map[string]bool{id: true, other.ID: false} {
		plan, err := third.PlanLeave(conv)
		if err != nil || plan.Configured != want {
			t.Fatalf("%s: configured %v, %v", conv, plan.Configured, err)
		}
	}
	if len(folders(t, root)) != 2 {
		t.Fatal("a third start made a folder")
	}
}

// T8: -max-conversations bounds Create and Join, and the join that waits counts.
func TestConversationBound(t *testing.T) {
	n := newNetwork(t)
	cfg := n.app(dataDir(t))
	cfg.MaxConversations = 2
	a, _ := startApp(t, cfg)
	mustCreateConv(t, a, "One")
	var ie *InviteError
	if _, err := a.Join("not a code", ""); !errors.As(err, &ie) || !errors.Is(err, invite.ErrInvalidCode) {
		t.Fatalf("a bad code: %v", err)
	}
	gone, _ := startApp(t, n.app(dataDir(t)))
	code, err := gone.CreateInvite(mustCreateConv(t, gone, "Gone").ID, InviteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	gone.ServiceShutdown() // nobody answers, so the join waits
	done := make(chan error, 1)
	go func() { _, err := a.Join(code.Code, ""); done <- err }()
	eventually(t, "the join to wait", func() bool { return len(a.List()) == 2 })
	if _, err := a.Create("Three"); !errors.Is(err, ErrTooManyConversations) {
		t.Fatalf("the join in progress does not count: %v", err)
	}
	if _, err := a.Join("still not a code", ""); errors.Is(err, ErrTooManyConversations) || !errors.As(err, &ie) {
		t.Fatalf("a second Join while one waits: %v", err)
	}
	a.CancelJoin()
	if err := waitJoin(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled join: %v", err)
	}
	if got := folders(t, cfg.DataDir); len(got) != 1 {
		t.Fatalf("folders: %v", got)
	}

	cfg = n.app(dataDir(t))
	cfg.MaxConversations = 1
	b, _ := startApp(t, cfg)
	mustCreateConv(t, b, "Only")
	if _, err := b.Create("Two"); !errors.Is(err, ErrTooManyConversations) {
		t.Fatalf("Create past the bound: %v", err)
	}
	if _, err := b.Join("not a code", ""); !errors.Is(err, ErrTooManyConversations) {
		t.Fatalf("Join past the bound: %v", err)
	}
	if got := folders(t, cfg.DataDir); len(got) != 1 {
		t.Fatalf("folders: %v", got)
	}
}

// freeUDPPort returns a UDP port of 127.0.0.1 that nothing listens on now.
func freeUDPPort(t *testing.T) int {
	t.Helper()
	c, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port
}

// T9: a fixed -listen port holds one conversation: a second is refused at Create, at Join and at Start.
func TestFixedListenPortHoldsOneConversation(t *testing.T) {
	n := newNetwork(t)
	cfg := n.app(dataDir(t))
	cfg.Listen = fmt.Sprintf("127.0.0.1:%d", freeUDPPort(t))
	a, _ := startApp(t, cfg)
	mustCreateConv(t, a, "One")
	if _, err := a.Create("Two"); !isListenError(err) {
		t.Fatalf("Create: %v", err)
	}
	if _, err := a.Join("not a code", ""); !isListenError(err) {
		t.Fatalf("Join: %v", err)
	}
	a.ServiceShutdown()

	second := filepath.Join(cfg.DataDir, conversationDir, strings.Repeat("ab", 16))
	if err := os.Mkdir(second, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second, "conversation.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewConversations(cfg, (&convEvents{}).emit).Start(); !isListenError(err) {
		t.Fatalf("Start with two: %v", err)
	}
	cfg.Listen = "127.0.0.1:0"
	if b, _ := startApp(t, cfg); len(b.List()) != 2 {
		t.Fatalf("port 0: %+v", b.List())
	}
}

func isListenError(err error) bool {
	var ce *ConfigError
	return errors.As(err, &ce) && ce.Setting == "listen" && errors.Is(err, errFixedPort)
}

// T10: the relay key check dials each relay once per app start, with 0, 1 or 3 conversations, and a wrong key reaches
// the app's own status, every running conversation and one that starts later. The relays here do not answer, so
// kolo-swarm never says "wrong-key" itself: only the check can.
func TestRelayCheckOncePerApp(t *testing.T) {
	n := newNetwork(t)
	wrongEntry, wrong := relayEntry(t, fmt.Sprintf("127.0.0.1:%d", freeUDPPort(t)))
	rightEntry, _ := relayEntry(t, fmt.Sprintf("127.0.0.1:%d", freeUDPPort(t)))
	saidWrong := func(ev *events) bool {
		ev.mu.Lock()
		defer ev.mu.Unlock()
		return slices.ContainsFunc(ev.relays, func(st RelayStatus) bool {
			return len(st.Relays) == 2 && st.Relays[0].Error == "wrong-key" && st.Relays[1].Error != "wrong-key"
		})
	}
	for _, count := range []int{0, 1, 3} {
		t.Run(fmt.Sprint(count, " conversations"), func(t *testing.T) {
			root := dataDir(t)
			if count > 0 {
				setup, _ := startApp(t, n.app(root))
				for i := range count {
					mustCreateConv(t, setup, fmt.Sprint("C", i))
				}
				setup.ServiceShutdown()
			}
			cfg := n.app(root)
			cfg.Relays = []string{wrongEntry, rightEntry}
			ev := &convEvents{}
			a := NewConversations(cfg, ev.emit)
			var mu sync.Mutex
			dials := map[string]int{}
			a.checkRelay = func(_ context.Context, r koloswarm.Relay) error {
				mu.Lock()
				dials[r.Addr]++
				mu.Unlock()
				if r.PublicKey.Equal(wrong) {
					return fmt.Errorf("dial: %w", kolosecure.ErrUnexpectedPeer)
				}
				return nil
			}
			if _, err := a.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { a.ServiceShutdown() })
			eventually(t, "the wrong key everywhere", func() bool {
				if !saidWrong(ev.of("")) {
					return false
				}
				for _, c := range a.List() {
					if !saidWrong(ev.of(c.ID)) {
						return false
					}
				}
				return true
			})
			later, err := a.Create("Later")
			if err != nil {
				t.Fatal(err)
			}
			eventually(t, "the wrong key in a conversation that started later", func() bool { return saidWrong(ev.of(later.ID)) })
			mu.Lock()
			defer mu.Unlock()
			if len(dials) != 2 || dials[strings.Split(wrongEntry, "@")[0]] != 1 || dials[strings.Split(rightEntry, "@")[0]] != 1 {
				t.Fatalf("dials: %v", dials)
			}
		})
	}
}

// T13: Create makes a conversation we own, with a fresh random key, private files and the name only we see. A bad
// name, or an engine that does not start, adds nothing.
func TestCreateConversation(t *testing.T) {
	n := newNetwork(t)
	root := dataDir(t)
	a, ev := startApp(t, n.app(root))
	x, y := mustCreateConv(t, a, "Family"), mustCreateConv(t, a, "  ")
	if !x.Admin || !x.Joined || x.Name != "Family" || y.Name != defaultConvName || x.Error != "" {
		t.Fatalf("created: %+v %+v", x, y)
	}
	keys := map[string]bool{}
	for _, id := range []string{x.ID, y.ID} {
		dir := filepath.Join(root, conversationDir, id)
		v, found, err := loadConversation(dir, defaultMaxPins)
		if err != nil || !found || len(v.Key) != 32 {
			t.Fatalf("%s: %+v, %v", id, v, err)
		}
		keys[string(v.Key)] = true
		for path, mode := range map[string]os.FileMode{"": os.ModeDir | 0o700, "conversation.json": 0o600, localFile: 0o600} {
			if st, err := os.Stat(filepath.Join(dir, path)); err != nil || st.Mode() != mode {
				t.Fatalf("%s/%s: %v, %v", id, path, st.Mode(), err)
			}
		}
	}
	if len(keys) != 2 {
		t.Fatal("two conversations got one key")
	}
	if l := ev.lastList(); len(l) != 2 || l[0].ID != x.ID && l[1].ID != x.ID {
		t.Fatalf("the window's list: %+v", l)
	}
	for _, bad := range []string{strings.Repeat("a", maxConvName+1), "a\x00b", "\xff"} {
		if _, err := a.Create(bad); !errors.Is(err, ErrInvalidName) {
			t.Fatalf("%q: %v", bad, err)
		}
	}
	if len(folders(t, root)) != 2 || len(a.List()) != 2 {
		t.Fatal("a refused Create added a conversation")
	}

	joiner, _ := startApp(t, Config{DataDir: dataDir(t), Bootstrap: n.bootstrap})
	if info, err := joiner.Start(); err != nil || info.HasPins {
		t.Fatalf("an app without pins: %+v, %v", info, err)
	}
	if info, err := a.Start(); err != nil || !info.HasPins {
		t.Fatalf("an app with pins: %+v, %v", info, err)
	}

	cfg := n.app(dataDir(t))
	cfg.Listen = "256.0.0.1:0"
	broken, _ := startApp(t, cfg)
	if _, err := broken.Create("Family"); err == nil || len(folders(t, cfg.DataDir)) != 0 || len(broken.List()) != 0 {
		t.Fatalf("a Create that failed: %v, %v", err, folders(t, cfg.DataDir))
	}
}

// S18 and S18b: a Join that fails, or is cancelled, removes its folder, kolo-id included, and the next Join starts in a
// new folder with a new kolo-id: an admin may have admitted the old key already. A restart finds no join waiting.
func TestFailedJoinStartsAfresh(t *testing.T) {
	n := newNetwork(t)
	owner, _ := startApp(t, n.app(dataDir(t)))
	code, err := owner.CreateInvite(mustCreateConv(t, owner, "Family").ID, InviteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	owner.ServiceShutdown() // nobody answers the invite

	cfg := Config{DataDir: dataDir(t), Bootstrap: n.bootstrap, LookupInterval: time.Second, joinFind: 2 * time.Second}
	a, _ := startApp(t, cfg)
	if _, err := a.Join(code.Code, "Family"); !errors.Is(err, invite.ErrUnreachable) {
		t.Fatalf("join: %v", err)
	}
	if got := folders(t, cfg.DataDir); len(got) != 0 || len(a.List()) != 0 {
		t.Fatalf("a failed join left %v", got)
	}
	done := make(chan error, 1)
	go func() { _, err := a.Join(code.Code, "Family"); done <- err }()
	eventually(t, "the join to start", func() bool { return len(a.List()) == 1 })
	pending := a.List()[0]
	c := engineOf(t, a, pending.ID)
	eventually(t, "the join to wait", func() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.joining != nil })
	a.CancelJoin()
	if err := waitJoin(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled join: %v", err)
	}
	if got := folders(t, cfg.DataDir); len(got) != 0 || pending.Joined {
		t.Fatalf("a cancelled join left %v", got)
	}
	a.ServiceShutdown()

	stale := filepath.Join(cfg.DataDir, conversationDir, strings.Repeat("ef", 16)) // a join a crash stopped
	if err := os.Mkdir(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	b, _ := startApp(t, cfg)
	if l := b.List(); len(l) != 0 || exists(t, stale) {
		t.Fatalf("after a restart: %+v", l)
	}
}

// T15: a conversation whose folder cannot be used shows why and which folder, and the others start.
func TestConversationThatFailsShowsItsFolder(t *testing.T) {
	n := newNetwork(t)
	root := dataDir(t)
	a, _ := startApp(t, n.app(root))
	good, damaged, old := mustCreateConv(t, a, "Good"), mustCreateConv(t, a, "Damaged"), mustCreateConv(t, a, "Old")
	a.ServiceShutdown()
	write := func(id, body string) {
		if err := os.WriteFile(filepath.Join(root, conversationDir, id, "conversation.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(damaged.ID, `{"key":`)
	write(old.ID, `{"key":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=","pinAddr":"127.0.0.1:7450","pinKey":"AA=="}`)

	b, _ := startApp(t, n.app(root))
	rows := map[string]ConversationInfo{}
	for _, c := range b.List() {
		rows[c.ID] = c
	}
	for id, want := range map[string]string{damaged.ID: "conversation.json: damaged", old.ID: "made by an older version"} {
		if r := rows[id]; !strings.Contains(r.Error, want) || r.Folder != filepath.Join(root, conversationDir, id) || r.Me != "" {
			t.Fatalf("%s: %+v", id, r)
		}
	}
	if r := rows[good.ID]; r.Error != "" || r.Folder != "" || !r.Joined {
		t.Fatalf("the good one: %+v", r)
	}
	if err := b.Send(good.ID, defaultRoom, "still works"); err != nil {
		t.Fatal(err)
	}
	if err := b.Send(damaged.ID, defaultRoom, "no"); !errors.Is(err, ErrUnknownConversation) {
		t.Fatalf("a failed conversation took a message: %v", err)
	}
}

// T16: every call with an unknown conversation ID fails with ErrUnknownConversation and changes nothing, also while
// a conversation runs.
func TestUnknownConversation(t *testing.T) {
	n := newNetwork(t)
	root := dataDir(t)
	a, _ := startApp(t, n.app(root))
	known := mustCreateConv(t, a, "Known")
	c := engineOf(t, a, known.ID)
	before := snapshot(t, root)
	for _, conv := range []string{"", "nope", strings.Repeat("0", 32), "../" + known.ID, strings.ToUpper(known.ID)} {
		_, members := a.Members(conv)
		_, rooms := a.Rooms(conv)
		_, admin := a.IsAdmin(conv)
		_, inv := a.CreateInvite(conv, InviteOptions{})
		_, invs := a.Invites(conv)
		_, plan := a.PlanLeave(conv)
		for what, err := range map[string]error{
			"Send": a.Send(conv, defaultRoom, "x"), "Members": members, "Rooms": rooms, "IsAdmin": admin,
			"RemoveMember": a.RemoveMember(conv, keyOf(c)), "CancelLeave": a.CancelLeave(conv),
			"SetProfile": a.SetProfile(conv, "Name", ""), "AddRoom": a.AddRoom(conv, "room"),
			"CreateInvite": inv, "Invites": invs, "Revoke": a.Revoke(conv, "00"), "Approve": a.Approve(conv, 1, false),
			"Deny": a.Deny(conv, 1), "Rename": a.Rename(conv, "Name"), "PlanLeave": plan, "Leave": a.Leave(conv, "t", ""),
		} {
			if !errors.Is(err, ErrUnknownConversation) {
				t.Errorf("%s(%q): %v", what, conv, err)
			}
		}
	}
	if c.log.Len() != 0 || len(c.Rooms()) != 1 || c.profileOf(keyOf(c)).Name != "" {
		t.Fatal("a call for another ID reached the conversation")
	}
	if infos, _ := c.Invites(); len(infos) != 0 {
		t.Fatalf("invites: %+v", infos)
	}
	if l := a.List(); len(l) != 1 || l[0].Name != "Known" {
		t.Fatalf("list: %+v", l)
	}
	if after := snapshot(t, root); !mapsEqual(onlyLocal(after), onlyLocal(before)) {
		t.Fatal("a refused call changed a file")
	}
}

// onlyLocal keeps the paths an unknown ID could change by mistake: local.json and the folders.
func onlyLocal(m map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		if strings.HasSuffix(k, localFile) || strings.HasPrefix(v, "d") {
			out[k] = v
		}
	}
	return out
}

// T17 measures eight idle conversations: goroutines, heap after a GC and open files.
func TestIdleCostOfEightConversations(t *testing.T) {
	n := newNetwork(t)
	files := func() int { d, _ := os.ReadDir("/dev/fd"); return len(d) }
	heap := func() uint64 {
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapAlloc
	}
	g0, h0, f0 := runtime.NumGoroutine(), heap(), files()
	a, _ := startApp(t, n.app(dataDir(t)))
	for i := range 8 {
		mustCreateConv(t, a, fmt.Sprint("C", i))
	}
	time.Sleep(3 * time.Second) // the joins, announces and first pushes settle
	g1, h1, f1 := runtime.NumGoroutine(), heap(), files()
	t.Logf("%d idle conversations: +%d goroutines, +%.1f MiB heap after GC, +%d open files",
		8, g1-g0, float64(int64(h1)-int64(h0))/(1<<20), f1-f0)
	if testing.Short() {
		return
	}
	// S14: two more apps join each of them, so every conversation has a braid and 3 peers, all in this process.
	for _, who := range []string{"bob", "carol"} {
		app, _ := startApp(t, Config{DataDir: dataDir(t), Bootstrap: n.bootstrap, LookupInterval: time.Second})
		for _, c := range a.List() {
			inv, err := a.CreateInvite(c.ID, InviteOptions{})
			if err != nil {
				t.Fatal(err)
			}
			joinConv(t, engineOf(t, a, c.ID), app, inv.Code, who)
		}
	}
	time.Sleep(5 * time.Second)
	g2, h2, f2 := runtime.NumGoroutine(), heap(), files()
	t.Logf("%d conversations with a braid and 3 peers each, 3 apps in one process: +%d goroutines, +%.1f MiB heap after GC, +%d open files",
		8, g2-g0, float64(int64(h2)-int64(h0))/(1<<20), f2-f0)
}

// T17b measures the default -max-conversations, 64 real engines on loopback, when KOLO_CHAT_MEASURE_64 is set: the time
// Create takes for all of them, goroutines, heap after a GC and open files. It is skipped otherwise.
func TestIdleCostOfSixtyFourConversations(t *testing.T) {
	if os.Getenv("KOLO_CHAT_MEASURE_64") == "" {
		t.Skip("set KOLO_CHAT_MEASURE_64=1 to measure 64 conversations")
	}
	n := newNetwork(t)
	// Every engine reaches the entry node from 127.0.0.1, and one IP may send 50 requests a second by default: lift
	// that on this test's own node, so the measure is of the engines and not of that limit.
	id, err := koloid.Generate()
	if err != nil {
		t.Fatal(err)
	}
	node, err := kolodht.New(context.Background(), "127.0.0.1:0", id, &kolodht.Config{Server: true, MaxRequestsPerIP: 10000, MaxRequestsPerPrefix: 100000})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { node.Close() })
	n.bootstrap = node.Addr().String()
	files := func() int { d, _ := os.ReadDir("/dev/fd"); return len(d) }
	heap := func() uint64 {
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapAlloc
	}
	g0, h0, f0 := runtime.NumGoroutine(), heap(), files()
	a, _ := startApp(t, n.app(dataDir(t)))
	begin := time.Now()
	for i := range defaultMaxConversations {
		mustCreateConv(t, a, fmt.Sprint("C", i))
	}
	took := time.Since(begin)
	time.Sleep(3 * time.Second)
	g1, h1, f1 := runtime.NumGoroutine(), heap(), files()
	if len(a.List()) != defaultMaxConversations {
		t.Fatalf("%d conversations listed", len(a.List()))
	}
	if _, err := a.Create("one too many"); !errors.Is(err, ErrTooManyConversations) {
		t.Fatalf("the bound: %v", err)
	}
	t.Logf("%d conversations: Create took %v in all, +%d goroutines, +%.1f MiB heap after GC, +%d open files",
		defaultMaxConversations, took.Round(time.Millisecond), g1-g0, float64(int64(h1)-int64(h0))/(1<<20), f1-f0)
}

// T19: Rename rewrites local.json in one step and keeps when the conversation was added; a damaged local.json gives
// "Conversation" and the conversation opens.
func TestRenameAndDamagedName(t *testing.T) {
	n := newNetwork(t)
	root := dataDir(t)
	a, ev := startApp(t, n.app(root))
	c := mustCreateConv(t, a, "Family")
	path := filepath.Join(root, conversationDir, c.ID, localFile)
	before := loadLocal(filepath.Dir(path))
	if err := a.Rename(c.ID, " Home "); err != nil {
		t.Fatal(err)
	}
	if got := loadLocal(filepath.Dir(path)); got.Name != "Home" || got.Created != before.Created {
		t.Fatalf("local.json: %+v", got)
	}
	if exists(t, path+".tmp") || ev.lastList()[0].Name != "Home" {
		t.Fatalf("after Rename: %+v", ev.lastList())
	}
	for _, bad := range []string{"", strings.Repeat("x", maxConvName+1)} {
		if err := a.Rename(c.ID, bad); !errors.Is(err, ErrInvalidName) {
			t.Fatalf("%q: %v", bad, err)
		}
	}
	a.ServiceShutdown()
	if err := os.WriteFile(path, []byte(`{"name":`), 0o600); err != nil {
		t.Fatal(err)
	}
	b, _ := startApp(t, n.app(root))
	if l := b.List(); len(l) != 1 || l[0].Name != defaultConvName || !l[0].Joined || l[0].Error != "" {
		t.Fatalf("a damaged local.json: %+v", l)
	}
}

// CV-1: a data folder, or its conversations/, that other users can open is refused, and nothing in it changes.
func TestOpenDataFolderIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode os.FileMode
		sub  bool
	}{{"0755", 0o755, false}, {"0777", 0o777, false}, {"conversations/ 0750", 0o750, true}} {
		root := dataDir(t)
		if tc.sub {
			a := NewConversations(Config{DataDir: root}, (&convEvents{}).emit)
			if _, err := a.Start(); err != nil {
				t.Fatal(err)
			}
			a.ServiceShutdown()
		}
		dir := root
		if tc.sub {
			dir = filepath.Join(root, conversationDir)
		}
		if err := os.Chmod(dir, tc.mode); err != nil {
			t.Fatal(err)
		}
		before := snapshot(t, root)
		_, err := NewConversations(Config{DataDir: root}, (&convEvents{}).emit).Start()
		var de *DataDirError
		if !errors.As(err, &de) || !de.Open || de.Dir != dir || !strings.Contains(err.Error(), "chmod 700") {
			t.Fatalf("%s: got %v", tc.name, err)
		}
		if after := snapshot(t, root); !mapsEqual(after, before) {
			t.Fatalf("%s: the folder changed", tc.name)
		}
	}
}

// CV-3: layout.json and local.json are read only as small regular files: a link, a device, a FIFO or a large file is
// refused at once, never read or waited on.
func TestSpecialLayoutAndNameFilesAreRefused(t *testing.T) {
	startsWithin := func(root string) error {
		t.Helper()
		done := make(chan error, 1)
		a := NewConversations(Config{DataDir: root}, (&convEvents{}).emit)
		go func() { _, err := a.Start(); done <- err }()
		select {
		case err := <-done:
			a.ServiceShutdown()
			return err
		case <-time.After(5 * time.Second):
			t.Fatal("Start waits on a special file")
			return nil
		}
	}
	good := filepath.Join(t.TempDir(), "good.json")
	if err := os.WriteFile(good, []byte(`{"layout":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, create := range map[string]func(path string) error{
		"a link to a good file": func(p string) error { return os.Symlink(good, p) },
		"a link to /dev/zero":   func(p string) error { return os.Symlink("/dev/zero", p) },
		"a FIFO":                func(p string) error { return mkfifo(p) },
		"a directory":           func(p string) error { return os.Mkdir(p, 0o700) },
		"over 4 KiB": func(p string) error {
			return os.WriteFile(p, []byte(`{"layout":2}`+strings.Repeat(" ", maxSmallFile)), 0o600)
		},
	} {
		root := dataDir(t)
		if err := create(filepath.Join(root, layoutFile)); err != nil {
			t.Fatal(err)
		}
		var de *DataDirError
		if err := startsWithin(root); !errors.As(err, &de) || de.Layout != -1 {
			t.Errorf("layout.json as %s: %v", name, err)
		}
	}

	root := dataDir(t)
	if err := startsWithin(root); err != nil {
		t.Fatal(err)
	}
	pending := filepath.Join(root, conversationDir, strings.Repeat("cd", 16))
	if err := os.Mkdir(pending, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := mkfifo(filepath.Join(pending, localFile)); err != nil {
		t.Fatal(err)
	}
	got := make(chan localInfo, 1)
	go func() { got <- loadLocal(pending) }()
	select {
	case l := <-got:
		if l.Name != defaultConvName {
			t.Fatalf("a FIFO as local.json: %+v", l)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reading local.json waits on a FIFO")
	}
}

// CV-2, CV-9, CV-10: Start runs at most -max-conversations engines, the oldest, the -conversation-key one first; the
// others are listed as not started, with no folder to move, and nothing in them changes, Rename included. A Leave
// starts the oldest that waits.
func TestStartIsBounded(t *testing.T) {
	n := newNetwork(t)
	root := dataDir(t)
	a, _ := startApp(t, n.app(root))
	var made []ConversationInfo
	for i := range 3 {
		made = append(made, mustCreateConv(t, a, fmt.Sprint("C", i)))
		time.Sleep(1100 * time.Millisecond) // Created is in seconds, and orders the list
	}
	a.ServiceShutdown()
	last := filepath.Join(root, conversationDir, made[2].ID)
	before := snapshot(t, last)

	cfg := n.app(root)
	cfg.MaxConversations = 2
	b, _ := startApp(t, cfg)
	l := b.List()
	if len(l) != 3 || l[0].Error != "" || l[1].Error != "" || l[2].ID != made[2].ID ||
		l[2].Error != ErrTooManyConversations.Error() || l[2].Folder != "" || l[2].Me != "" {
		t.Fatalf("list: %+v", l)
	}
	if err := b.Send(made[2].ID, defaultRoom, "x"); !errors.Is(err, ErrUnknownConversation) {
		t.Fatalf("the conversation past the bound runs: %v", err)
	}
	if _, err := b.Create("D"); !errors.Is(err, ErrTooManyConversations) {
		t.Fatalf("Create: %v", err)
	}
	if err := b.Rename(made[2].ID, "Renamed"); !errors.Is(err, ErrUnknownConversation) {
		t.Fatalf("Rename of a conversation that did not start: %v", err)
	}
	if after := snapshot(t, last); !mapsEqual(after, before) {
		t.Fatal("the folder past the bound changed")
	}

	// Leaving one makes room: the conversation past the bound starts at once.
	plan, err := b.PlanLeave(made[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Leave(made[0].ID, plan.Token, ""); err != nil {
		t.Fatal(err)
	}
	if l := b.List(); len(l) != 2 || l[1].ID != made[2].ID || l[1].Error != "" || !l[1].Joined || l[1].Me == "" {
		t.Fatalf("after a Leave: %+v", l)
	}
	if err := b.Send(made[2].ID, defaultRoom, "now it runs"); err != nil {
		t.Fatal(err)
	}
	b.ServiceShutdown()

	// The -conversation-key conversation starts first, even when it is the newest.
	v, _, err := loadConversation(last, defaultMaxPins)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConversationKey, cfg.MaxConversations = hex.EncodeToString(v.Key), 1
	c, _ := startApp(t, cfg)
	for _, r := range c.List() {
		if (r.ID == made[2].ID) != (r.Error == "") {
			t.Fatalf("with its key and a bound of 1: %+v", c.List())
		}
	}
}
