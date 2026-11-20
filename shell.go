package main

import (
	"log"
	"net"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	wailsevents "github.com/wailsapp/wails/v3/pkg/events"
)

// startWorker makes wake run apply on one goroutine of its own. Calls to wake that come while it works are
// merged into one, so nothing queues up however many events the chat sends.
func (n *NotchService) startWorker() {
	dirty, done := make(chan struct{}, 1), make(chan struct{})
	n.wake = func() {
		select {
		case dirty <- struct{}{}:
		default:
		}
	}
	var once sync.Once
	n.stopWork = func() { once.Do(func() { close(done) }) }
	go func() {
		for {
			select {
			case <-dirty:
				n.apply()
			case <-done:
				return
			}
		}
	}()
}

// macOptions keeps the app running while its windows are hidden. With ApplicationShouldTerminateAfterLastWindowClosed,
// AppKit ended the app when a click on the tray icon hid the main window, the only one on screen. Closing the
// main window with its red button still quits, through the WindowClosing hook of attach.
func macOptions() application.MacOptions { return application.MacOptions{} }

// mainWindow is the main window, as much of it as the tray and the notch move.
type mainWindow interface {
	IsVisible() bool
	IsMinimised() bool
	Show() application.Window
	Hide() application.Window
	UnMinimise()
	Focus()
}

// moveWindow brings the main window to the front, or for windowToggle hides it when it is on screen. Hide only
// takes the window off the screen: it does not close it, so the WindowClosing hook that quits never runs.
func moveWindow(w mainWindow, action int) {
	if action == windowToggle && w.IsVisible() && !w.IsMinimised() {
		w.Hide()
		return
	}
	w.Show()
	w.UnMinimise()
	w.Focus()
}

// notchDemo says whether KOLO_CHAT_NOTCH_DEMO=1 asks for a sample message, to see the notch and the tray
// without a peer.
func notchDemo(getenv func(string) string) bool { return getenv("KOLO_CHAT_NOTCH_DEMO") == "1" }

// attach connects the service to the app before it runs: the main window's events and focus, the notch window
// where the system has one, and the tray.
func (n *NotchService) attach(app *application.App, window *application.WebviewWindow) {
	n.startWorker()
	n.toMain = func(name string, data any) {
		window.DispatchWailsEvent(&application.CustomEvent{Name: name, Data: data})
	}
	n.emit = func(name string, data any) { app.Event.Emit(name, data) }
	n.window = func(action int) { moveWindow(window, action) }
	window.OnWindowEvent(wailsevents.Common.WindowFocus, func(*application.WindowEvent) { n.setFocused(true) })
	window.OnWindowEvent(wailsevents.Common.WindowLostFocus, func(*application.WindowEvent) { n.setFocused(false) })

	panel := newNotchPanel(app, n)
	n.mu.Lock()
	n.panel = panel
	n.mu.Unlock()
	if panel != nil { // macOS: see macOptions
		window.OnWindowEvent(wailsevents.Common.WindowClosing, func(*application.WindowEvent) { app.Quit() })
	}

	dial := func(path string) error {
		c, err := net.DialTimeout("unix", path, time.Second)
		if err == nil {
			c.Close()
		}
		return err
	}
	if trayUsable(runtime.GOOS, os.Getenv, dial) {
		newTray(app, runtime.GOOS, n)
	} else {
		log.Print("no tray icon: this session has no D-Bus session bus")
	}

	if notchDemo(os.Getenv) {
		n.after(notchDemoDelay, func() {
			n.mu.Lock()
			n.names[""] = "Demo"
			n.mu.Unlock()
			n.observe("chat:msg", ConvEvent{Data: ChatMsg{From: "demo", Room: defaultRoom, Text: "This is a sample message from KOLO_CHAT_NOTCH_DEMO.", Via: "direct"}})
		})
	}
}
