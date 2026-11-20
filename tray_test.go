package main

import (
	"bytes"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestTrayViewOf(t *testing.T) {
	idle := trayViewOf(NotchState{Enabled: true, Pin: pinUnknown}, "en")
	want := trayView{Open: "Open Kolo Chat", Status: "No peers online", Notch: "Show messages in the notch",
		NotchOn: true, Quit: "Quit", Tooltip: "Kolo Chat"}
	if idle != want {
		t.Fatalf("idle = %+v, want %+v", idle, want)
	}

	v := trayViewOf(NotchState{Unread: 2, Peers: 3, Pin: pinOK}, "en")
	if !v.Active || v.Status != "3 peers online · pin ready" || v.Tooltip != "Kolo Chat: 2 unread messages" || v.NotchOn {
		t.Fatalf("unread = %+v", v)
	}
	if v := trayViewOf(NotchState{Unread: 1, Peers: 1, Pin: pinWaiting, Locked: 4}, "en"); v.Status != "1 peer online · messages not opened yet: 4" || v.Tooltip != "Kolo Chat: 1 unread message" {
		t.Fatalf("waiting = %+v", v)
	}
	if v := trayViewOf(NotchState{Pin: pinError}, "pt-BR"); v.Status != "Nenhum par online · pin indisponível" || v.Quit != "Sair" || v.Active {
		t.Fatalf("pt = %+v", v)
	}
	if v := trayViewOf(NotchState{Peers: 2}, "es_ES"); v.Status != "2 pares en línea" || v.Open != "Abrir Kolo Chat" {
		t.Fatalf("es = %+v", v)
	}
	for lang, want := range map[string]string{"es": "mensajes aún sin abrir: 2", "pt": "mensagens ainda não abertas: 2"} {
		if v := trayViewOf(NotchState{Pin: pinWaiting, Locked: 2}, lang); !strings.HasSuffix(v.Status, want) {
			t.Fatalf("%s waiting = %+v", lang, v)
		}
	}
	if v := trayViewOf(NotchState{Unread: 3, Peers: 5, Conversation: "Work"}, "en"); v.Tooltip != "Kolo Chat: 3 unread messages · Work" || v.Status != "5 peers online" {
		t.Fatalf("several conversations = %+v", v)
	}
	if v := trayViewOf(NotchState{Conversation: "Work"}, "en"); v.Tooltip != "Kolo Chat" {
		t.Fatalf("nothing unread = %+v", v)
	}
	if v := trayViewOf(NotchState{}, "fr"); v.Quit != "Quit" {
		t.Fatalf("an unknown language gave %+v, want English", v)
	}
}

func TestTrayStringsComplete(t *testing.T) {
	for lang, s := range trayStrings {
		for key := range trayStrings["en"] {
			if s[key] == "" {
				t.Errorf("%s has no %q", lang, key)
			}
		}
	}
}

func TestTrayIcon(t *testing.T) {
	for _, c := range []struct {
		goos     string
		active   bool
		icon     []byte
		template bool
	}{
		{"darwin", false, trayTemplate, true},
		{"darwin", true, trayTemplateActive, true},
		{"windows", false, trayColour, false},
		{"linux", true, trayColourActive, false},
	} {
		icon, template := trayIcon(c.goos, c.active)
		if !bytes.Equal(icon, c.icon) || template != c.template || len(icon) == 0 {
			t.Errorf("trayIcon(%s, %v): %d bytes, template %v", c.goos, c.active, len(icon), template)
		}
	}
	if bytes.Equal(trayTemplate, trayTemplateActive) || bytes.Equal(trayColour, trayColourActive) {
		t.Error("the active icon is the idle one")
	}
}

// fakeTray records what a refresh changes on the native tray.
type fakeTray struct {
	icons    []bool
	tooltips []string
	labels   []trayView
}

func (f *fakeTray) SetIcon(active bool)    { f.icons = append(f.icons, active) }
func (f *fakeTray) SetTooltip(text string) { f.tooltips = append(f.tooltips, text) }
func (f *fakeTray) SetLabels(v trayView)   { f.labels = append(f.labels, v) }

func TestTrayFollowsTheNotchState(t *testing.T) {
	n := newTestNotch(t)
	tray := &fakeTray{}
	r := &trayRefresher{target: tray, last: trayViewOf(n.State(), n.language())}
	n.refresh = append(n.refresh, r.refresh)

	n.observe("chat:msg", ChatMsg{From: alice, Text: "hi"})
	if len(tray.icons) != 1 || !tray.icons[0] || len(tray.tooltips) != 1 || len(tray.labels) != 0 {
		t.Fatalf("an unread message: icons=%v tooltips=%v labels=%d", tray.icons, tray.tooltips, len(tray.labels))
	}
	// One more message changes the count in the tooltip, not the icon and not the menu.
	n.observe("chat:msg", ChatMsg{From: alice, Text: "again"})
	if len(tray.icons) != 1 || len(tray.labels) != 0 || tray.tooltips[len(tray.tooltips)-1] != "Kolo Chat: 2 unread messages" {
		t.Fatalf("a second message: icons=%v tooltips=%v labels=%d", tray.icons, tray.tooltips, len(tray.labels))
	}
	n.setFocused(true)
	if len(tray.icons) != 2 || tray.icons[1] || tray.tooltips[len(tray.tooltips)-1] != "Kolo Chat" {
		t.Fatalf("after the focus: icons=%v tooltips=%v", tray.icons, tray.tooltips)
	}

	n.observe("chat:peers", 2)
	if len(tray.labels) != 1 || tray.labels[0].Status != "2 peers online" {
		t.Fatalf("peers: labels=%+v", tray.labels)
	}
	n.SetLanguage("pt-BR")
	if len(tray.labels) != 2 || tray.labels[1].Quit != "Sair" || tray.labels[1].Status != "2 pares online" {
		t.Fatalf("language: labels=%+v", tray.labels)
	}
	if err := n.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	if len(tray.labels) != 3 || tray.labels[2].NotchOn {
		t.Fatalf("notch off: labels=%+v", tray.labels)
	}
	n.setFocused(true)
	n.observe("chat:msg", ChatMsg{From: alice, Text: "read at once"})
	if len(tray.icons) != 2 || len(tray.labels) != 3 {
		t.Fatalf("no change touched the tray: icons=%v labels=%d", tray.icons, len(tray.labels))
	}
}

// fakeWindow records what moveWindow does to the main window.
type fakeWindow struct {
	visible, minimised bool
	calls              []string
}

func (w *fakeWindow) IsVisible() bool   { return w.visible }
func (w *fakeWindow) IsMinimised() bool { return w.minimised }
func (w *fakeWindow) Show() application.Window {
	w.calls, w.visible = append(w.calls, "show"), true
	return nil
}
func (w *fakeWindow) Hide() application.Window {
	w.calls, w.visible = append(w.calls, "hide"), false
	return nil
}
func (w *fakeWindow) UnMinimise() { w.calls, w.minimised = append(w.calls, "unminimise"), false }
func (w *fakeWindow) Focus()      { w.calls = append(w.calls, "focus") }

// A click on the tray icon hides the window and the next one brings it back. Nothing on that path closes the
// window, and the app does not ask AppKit to end it when the window is gone from the screen, which
// would end the app on a click).
func TestTrayClickTogglesAndNeverQuits(t *testing.T) {
	if macOptions().ApplicationShouldTerminateAfterLastWindowClosed {
		t.Fatal("hiding the main window from the tray would end the app")
	}
	n := newTestNotch(t)
	w := &fakeWindow{visible: true}
	n.window = func(action int) { moveWindow(w, action) }

	n.toggleWindow()
	if !slices.Equal(w.calls, []string{"hide"}) || w.visible {
		t.Fatalf("first click: %v", w.calls)
	}
	n.toggleWindow()
	if !slices.Equal(w.calls, []string{"hide", "show", "unminimise", "focus"}) || !w.visible {
		t.Fatalf("second click: %v", w.calls)
	}

	w.calls, w.minimised = nil, true
	n.toggleWindow()
	if !slices.Equal(w.calls, []string{"show", "unminimise", "focus"}) {
		t.Fatalf("a click on a minimised window: %v", w.calls)
	}
	w.calls = nil
	n.OpenChat()
	if !slices.Equal(w.calls, []string{"show", "unminimise", "focus"}) {
		t.Fatalf("Open from the menu on a visible window: %v", w.calls)
	}
}

// On Linux the tray needs the D-Bus session bus: without one that answers the app makes no tray, instead of
// one that panics at the first unread message.
func TestTrayUsable(t *testing.T) {
	env := func(vars map[string]string) func(string) string {
		return func(k string) string { return vars[k] }
	}
	var dialled []string
	dial := func(ok ...string) func(string) error {
		return func(path string) error {
			dialled = append(dialled, path)
			for _, p := range ok {
				if p == path {
					return nil
				}
			}
			return os.ErrNotExist
		}
	}
	for _, goos := range []string{"darwin", "windows"} {
		if !trayUsable(goos, env(nil), dial()) || len(dialled) != 0 {
			t.Fatalf("%s: the tray needs no bus, dialled %v", goos, dialled)
		}
	}
	for _, c := range []struct {
		name string
		vars map[string]string
		up   []string
		want bool
		dial []string
	}{
		{"no bus at all", nil, nil, false, nil},
		{"address, bus up", map[string]string{"DBUS_SESSION_BUS_ADDRESS": "unix:path=/run/user/1000/bus"}, []string{"/run/user/1000/bus"}, true, []string{"/run/user/1000/bus"}},
		{"address, bus gone", map[string]string{"DBUS_SESSION_BUS_ADDRESS": "unix:path=/run/user/1000/bus"}, nil, false, []string{"/run/user/1000/bus"}},
		{"abstract socket with a guid", map[string]string{"DBUS_SESSION_BUS_ADDRESS": "unix:abstract=/tmp/dbus-x,guid=abc"}, []string{"@/tmp/dbus-x"}, true, []string{"@/tmp/dbus-x"}},
		{"second address", map[string]string{"DBUS_SESSION_BUS_ADDRESS": "tcp:host=127.0.0.1,port=1;unix:path=/a;unix:path=/b"}, []string{"/b"}, true, []string{"/a", "/b"}},
		{"runtime directory", map[string]string{"XDG_RUNTIME_DIR": "/run/user/1000"}, []string{"/run/user/1000/bus"}, true, []string{"/run/user/1000/bus"}},
		{"runtime directory, no bus", map[string]string{"XDG_RUNTIME_DIR": "/run/user/1000"}, nil, false, []string{"/run/user/1000/bus"}},
		{"not a unix address", map[string]string{"DBUS_SESSION_BUS_ADDRESS": "tcp:host=127.0.0.1,port=1", "XDG_RUNTIME_DIR": "/run/user/1000"}, []string{"/run/user/1000/bus"}, false, nil},
		{"malformed", map[string]string{"DBUS_SESSION_BUS_ADDRESS": "unix:path=;unix;:,=,"}, nil, false, nil},
	} {
		dialled = nil
		if got := trayUsable("linux", env(c.vars), dial(c.up...)); got != c.want || !slices.Equal(dialled, c.dial) {
			t.Errorf("%s: usable=%v dialled=%v, want %v %v", c.name, got, dialled, c.want, c.dial)
		}
	}
}
