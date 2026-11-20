package main

import (
	_ "embed"
	"fmt"
	"log"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// The tray icons come from the Kolo logo kit. The template ones are for the macOS menu bar, which tints them;
// the active ones carry a dot for unread messages.
var (
	//go:embed build/tray/tray-template@2x.png
	trayTemplate []byte
	//go:embed build/tray/tray-template-active@2x.png
	trayTemplateActive []byte
	//go:embed build/tray/tray-32.png
	trayColour []byte
	//go:embed build/tray/tray-active-32.png
	trayColourActive []byte
)

// trayView is what the tray shows for a state: the icon variant and the labels of its menu.
type trayView struct {
	Active  bool   // unread messages: the icon with the dot
	Open    string // menu: shows and focuses the main window
	Status  string // menu: peers online and the state of the pin, not clickable
	Notch   string // menu, macOS only: the "Show messages in the notch" setting
	NotchOn bool
	Quit    string
	Tooltip string
}

// trayViewOf maps the state of the chat to the tray, in the given language. The tooltip names the conversation of the
// newest message.
func trayViewOf(st NotchState, lang string) trayView {
	lang = pickLanguage(lang)
	s := trayStrings[lang]
	v := trayView{
		Active:  st.Unread > 0,
		Open:    s["open"],
		Status:  count(lang, "peers", st.Peers),
		Notch:   s["notch"],
		NotchOn: st.Enabled,
		Quit:    s["quit"],
		Tooltip: "Kolo Chat",
	}
	switch st.Pin {
	case pinOK:
		v.Status += " · " + s["pinOK"]
	case pinError:
		v.Status += " · " + s["pinError"]
	case pinWaiting:
		v.Status += " · " + fmt.Sprintf(s["pinWaiting"], st.Locked)
	}
	if v.Active {
		v.Tooltip += ": " + count(lang, "unread", st.Unread)
		if st.Conversation != "" {
			v.Tooltip += " · " + st.Conversation
		}
	}
	return v
}

// trayIcon picks the icon for a system: a template image on macOS, a coloured one elsewhere.
func trayIcon(goos string, active bool) (icon []byte, template bool) {
	switch {
	case goos == "darwin" && active:
		return trayTemplateActive, true
	case goos == "darwin":
		return trayTemplate, true
	case active:
		return trayColourActive, false
	}
	return trayColour, false
}

// trayUsable says whether the tray can be made. On Linux the tray of the Wails library needs the D-Bus session
// bus and, without one, panics on the first change of its icon. So the bus is tried first, with dial on the
// path of its socket, and the app goes without a tray when it does not answer.
func trayUsable(goos string, getenv func(string) string, dial func(path string) error) bool {
	if goos != "linux" {
		return true
	}
	for _, path := range sessionBusSockets(getenv) {
		if dial(path) == nil {
			return true
		}
	}
	return false
}

// sessionBusSockets lists where the D-Bus session bus may listen: the unix addresses of
// DBUS_SESSION_BUS_ADDRESS, or the "bus" socket of the runtime directory when the variable is not set. A name
// that starts with "@" is an abstract socket.
func sessionBusSockets(getenv func(string) string) []string {
	addrs := getenv("DBUS_SESSION_BUS_ADDRESS")
	if addrs == "" {
		if dir := getenv("XDG_RUNTIME_DIR"); dir != "" {
			return []string{dir + "/bus"}
		}
		return nil
	}
	var paths []string
	for _, addr := range strings.Split(addrs, ";") {
		kind, params, _ := strings.Cut(addr, ":")
		if kind != "unix" {
			continue
		}
		for _, param := range strings.Split(params, ",") {
			switch key, value, _ := strings.Cut(param, "="); {
			case value == "":
			case key == "path":
				paths = append(paths, value)
			case key == "abstract":
				paths = append(paths, "@"+value)
			}
		}
	}
	return paths
}

// trayTarget is the native tray, as much of it as a refresh needs.
type trayTarget interface {
	SetIcon(active bool)
	SetTooltip(text string)
	SetLabels(v trayView) // the labels of the menu and the check of the notch setting
}

// trayRefresher keeps the tray equal to the state and touches only what changed: the icon when Active
// changes, the menu when a label or the check does. A new message alone changes neither.
type trayRefresher struct {
	target trayTarget
	last   trayView
}

func (r *trayRefresher) refresh(st NotchState, lang string) {
	v := trayViewOf(st, lang)
	if v.Active != r.last.Active {
		r.target.SetIcon(v.Active)
	}
	if v.Tooltip != r.last.Tooltip {
		r.target.SetTooltip(v.Tooltip)
	}
	if a, b := v, r.last; a.Open != b.Open || a.Status != b.Status || a.Notch != b.Notch || a.NotchOn != b.NotchOn || a.Quit != b.Quit {
		r.target.SetLabels(v)
	}
	r.last = v
}

// nativeTray is the tray of the Wails library.
type nativeTray struct {
	goos                      string
	tray                      *application.SystemTray
	menu                      *application.Menu
	open, status, notch, quit *application.MenuItem
}

func (t *nativeTray) SetIcon(active bool) {
	if icon, template := trayIcon(t.goos, active); template {
		t.tray.SetTemplateIcon(icon)
	} else {
		t.tray.SetIcon(icon)
	}
}

func (t *nativeTray) SetTooltip(text string) { t.tray.SetTooltip(text) }

func (t *nativeTray) SetLabels(v trayView) {
	t.open.SetLabel(v.Open)
	t.status.SetLabel(v.Status)
	t.quit.SetLabel(v.Quit)
	if t.notch != nil {
		t.notch.SetLabel(v.Notch).SetChecked(v.NotchOn)
	}
	if t.goos == "linux" { // its tray reads the labels again only when the menu is set
		t.tray.SetMenu(t.menu)
	}
}

// newTray puts the app in the menu bar, the notification area or the tray, and keeps it up to date with n.
// A click shows or hides the main window; the menu opens it, shows the status, has the notch setting and quits.
func newTray(app *application.App, goos string, n *NotchService) {
	st := n.State()
	v := trayViewOf(st, n.language())
	t := &nativeTray{goos: goos, tray: app.SystemTray.New(), menu: app.NewMenu()}
	t.SetIcon(v.Active)
	t.SetTooltip(v.Tooltip)
	t.open = t.menu.Add(v.Open).OnClick(func(*application.Context) { n.OpenChat() })
	t.status = t.menu.Add(v.Status).SetEnabled(false)
	t.menu.AddSeparator()
	if st.Supported {
		t.notch = t.menu.AddCheckbox(v.Notch, v.NotchOn).OnClick(func(ctx *application.Context) {
			if err := n.SetEnabled(ctx.ClickedMenuItem().Checked()); err != nil {
				log.Print("notch settings: ", err)
			}
		})
		t.menu.AddSeparator()
	}
	t.quit = t.menu.Add(v.Quit).OnClick(func(*application.Context) { app.Quit() })
	t.tray.SetMenu(t.menu)
	t.tray.OnClick(n.toggleWindow)

	r := &trayRefresher{target: t, last: v}
	n.refresh = append(n.refresh, r.refresh) // apply calls it, one at a time
}
