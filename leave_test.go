package main

import (
	"bytes"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	kolostore "github.com/ciranda-dev/kolo-store"
)

func hasText(msgs []ChatMsg, text string) bool {
	return slices.ContainsFunc(msgs, func(m ChatMsg) bool { return m.Text == text })
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Lstat(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	return err == nil
}

// T6 and T18. Alice has two conversations, and Bob is in the second. Alice leaves the first: only its folder goes,
// with its kolo-id, and the second still sends and receives; layout.json and notch.json stay. The plan names what
// goes and what stays, a token works once and for its own conversation only, and an ID that is not one the app holds
// removes nothing, whatever path it spells. The log holds no key, secret, invite code, name or message text.
func TestLeaveOneConversation(t *testing.T) {
	logs := captureLog(t)
	n := newNetwork(t)
	root := dataDir(t)
	if err := saveNotchSettings(root, NotchSettings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(root, "x")
	if err := os.Mkdir(sibling, 0o700); err != nil {
		t.Fatal(err)
	}
	alice, aev := startApp(t, n.app(root))
	first, second := mustCreateConv(t, alice, "Leaving soon"), mustCreateConv(t, alice, "Staying here")
	firstDir, secondDir := filepath.Join(root, conversationDir, first.ID), filepath.Join(root, conversationDir, second.ID)
	var secrets []string
	for _, id := range []string{first.ID, second.ID} {
		c := engineOf(t, alice, id)
		secrets = append(secrets, hex.EncodeToString(c.set.conversationKey[:]), hex.EncodeToString(c.secret[:]))
	}
	inv, err := alice.CreateInvite(second.ID, InviteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	bob, bev := startApp(t, Config{DataDir: dataDir(t), Bootstrap: n.bootstrap, LookupInterval: time.Second})
	bconv := joinConv(t, engineOf(t, alice, second.ID), bob, inv.Code, "Alice and Bob")
	linked(t, engineOf(t, alice, second.ID), engineOf(t, bob, bconv.ID))
	secrets = append(secrets, inv.Code, "Leaving soon", "Staying here", "Alice and Bob")

	for _, bad := range []string{"", "..", "../x", "x", strings.Repeat("a", 31), strings.Repeat("a", 33), strings.Repeat("0", 32), "../" + first.ID} {
		if _, err := alice.PlanLeave(bad); !errors.Is(err, ErrUnknownConversation) {
			t.Fatalf("PlanLeave(%q): %v", bad, err)
		}
		if err := alice.Leave(bad, "token", ""); !errors.Is(err, ErrUnknownConversation) {
			t.Fatalf("Leave(%q): %v", bad, err)
		}
	}
	if !exists(t, sibling) || !exists(t, firstDir) || !exists(t, secondDir) {
		t.Fatal("a refused Leave removed something")
	}

	plan, err := alice.PlanLeave(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	keep := []string{filepath.Join(root, layoutFile), filepath.Join(root, notchSettingsFile), "Staying here"}
	if !slices.Equal(plan.Delete, []string{firstDir}) || plan.Dir != firstDir || !slices.Equal(plan.Keep, keep) ||
		plan.Name != "Leaving soon" || plan.Others != 1 || !plan.Founder || len(plan.Admins) != 0 || plan.Configured || plan.Token == "" {
		t.Fatalf("plan = %+v", plan)
	}
	other, err := alice.PlanLeave(second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := alice.Leave(second.ID, plan.Token, ""); !errors.Is(err, ErrLeaveNotConfirmed) {
		t.Fatalf("one conversation's token left another: %v", err)
	}
	if err := alice.Leave(second.ID, other.Token, ""); !errors.Is(err, ErrLeaveNotConfirmed) {
		t.Fatalf("a token works after a refused Leave: %v", err)
	}
	for _, bad := range []string{"", "wrong", plan.Token + "x"} {
		if err := alice.Leave(first.ID, bad, ""); !errors.Is(err, ErrLeaveNotConfirmed) {
			t.Fatalf("Leave with %q: %v", bad, err)
		}
	}
	if plan, err = alice.PlanLeave(first.ID); err != nil {
		t.Fatal(err)
	}
	if err := alice.Leave(first.ID, plan.Token, ""); err != nil {
		t.Fatal(err)
	}

	if exists(t, firstDir) || !exists(t, secondDir) || !exists(t, sibling) || !exists(t, filepath.Join(root, layoutFile)) ||
		!exists(t, filepath.Join(root, notchSettingsFile)) {
		t.Fatal("Leave removed the wrong files")
	}
	if l := aev.lastList(); len(l) != 1 || l[0].ID != second.ID {
		t.Fatalf("the window's list: %+v", l)
	}
	if err := alice.Send(first.ID, defaultRoom, "gone"); !errors.Is(err, ErrUnknownConversation) {
		t.Fatalf("Send to the conversation that left: %v", err)
	}
	if err := alice.Send(second.ID, defaultRoom, "alice after leaving the other"); err != nil {
		t.Fatal(err)
	}
	if err := bob.Send(bconv.ID, defaultRoom, "bob still here"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "both ways in the conversation that stays", func() bool {
		return hasText(bev.of(bconv.ID).messages(), "alice after leaving the other") && hasText(aev.of(second.ID).messages(), "bob still here")
	})
	secrets = append(secrets, "alice after leaving the other", "bob still here")

	for _, s := range secrets {
		if strings.Contains(logs.String(), s) {
			t.Errorf("the log holds %q", s)
		}
	}
}

// A member that joins again with a new log replaces its header, starts its count over and keeps its newest
// maxPastLogs old headers for the history. The same header again changes nothing.
func TestMemberJoinedAgain(t *testing.T) {
	c := &ChatService{members: map[string]*member{}, replicas: map[string]*kolostore.Replica{}, dirs: map[string]*directory{},
		pinned: map[string]uint64{}, locked: map[string]int{}}
	c.set.DataDir = dataDir(t)
	pub := make([]byte, 32)
	header := func(i int) []byte { return []byte{byte(i)} }
	for i := range maxPastLogs + 3 {
		if err := c.addMember(pub, header(i), nil); err != nil {
			t.Fatal(err)
		}
		c.members[hex.EncodeToString(pub)].Shown = 5
		c.locked[hex.EncodeToString(pub)] = 2
	}
	if err := c.addMember(pub, header(maxPastLogs+2), []byte("dir")); err != nil {
		t.Fatal(err)
	}
	m := c.members[hex.EncodeToString(pub)]
	if !bytes.Equal(m.Header, header(maxPastLogs+2)) || m.Shown != 5 || string(m.DirHeader) != "dir" {
		t.Fatalf("the same header again changed the member: %+v", m)
	}
	if err := c.addMember(pub, header(99), nil); err != nil {
		t.Fatal(err)
	}
	m = c.members[hex.EncodeToString(pub)]
	if m.Shown != 0 || len(m.DirHeader) != 0 || c.locked[hex.EncodeToString(pub)] != 0 {
		t.Fatalf("a new log did not start over: %+v", m)
	}
	var want [][]byte
	for i := 3; i <= maxPastLogs+2; i++ {
		want = append(want, header(i))
	}
	if !slices.EqualFunc(m.Past, want, bytes.Equal) {
		t.Fatalf("past = %v, want %v", m.Past, want)
	}
	saved, err := loadMembers(c.set.DataDir)
	if err != nil || len(saved[hex.EncodeToString(pub)].Past) != maxPastLogs {
		t.Fatalf("members.json: %+v, %v", saved, err)
	}
}
