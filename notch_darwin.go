//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

// housingRect is the camera housing of the first screen, where the Wails library puts the notch window, found as
// that library finds it: between the two top areas beside it. It is empty on a screen without one.
static NSRect housingRect(void) {
	if (@available(macOS 12.0, *)) {
		NSScreen *s = [[NSScreen screens] firstObject];
		if (s == nil || s.safeAreaInsets.top <= 0) {
			return NSZeroRect;
		}
		NSRect l = s.auxiliaryTopLeftArea, r = s.auxiliaryTopRightArea;
		if (NSIsEmptyRect(l) || NSIsEmptyRect(r) || NSMinX(r) <= NSMaxX(l)) {
			return NSZeroRect;
		}
		CGFloat inset = s.safeAreaInsets.top;
		return NSMakeRect(NSMaxX(l), NSMaxY(s.frame) - inset, NSMinX(r) - NSMaxX(l), inset);
	}
	return NSZeroRect;
}

// onHousing counts the top edge too: the pointer pushed against the top of the screen is on it.
static bool onHousing(NSPoint p) {
	NSRect r = housingRect();
	return !NSIsEmptyRect(r) && p.x >= NSMinX(r) && p.x <= NSMaxX(r) && p.y >= NSMinY(r) && p.y <= NSMaxY(r);
}

static int housingClicks; // main thread only
static id clickMonitor;

// watchHousingClicks counts the clicks on the housing. A local monitor sees only the clicks that reach this
// app, so it needs no permission; a click on the housing while the notch is down lands on the notch window.
static void watchHousingClicks(void) {
	if (clickMonitor != nil) {
		return;
	}
	clickMonitor = [[NSEvent addLocalMonitorForEventsMatchingMask:NSEventMaskLeftMouseDown handler:^NSEvent *(NSEvent *e) {
		if (onHousing([NSEvent mouseLocation])) {
			housingClicks++;
		}
		return e;
	}] retain];
}

static bool hasHousing(void) { return !NSIsEmptyRect(housingRect()); }

// readPointer reads where the pointer is, which needs no permission either, and takes the clicks counted.
static bool readPointer(int *clicks) {
	*clicks = housingClicks;
	housingClicks = 0;
	return onHousing([NSEvent mouseLocation]);
}
*/
import "C"

import (
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
	wailsevents "github.com/wailsapp/wails/v3/pkg/events"
)

// notchShortcut shows or hides the notch window from any application.
const notchShortcut = "CmdOrCtrl+Option+K"

type notchWindow struct{ w *application.NotchWindow }

func (p notchWindow) Show() { p.w.Show() }
func (p notchWindow) Hide() { p.w.Hide() }

// newNotchPanel creates the notch window of the Wails library (see notchOptions). It runs on the main thread,
// which primaryScreen needs.
func newNotchPanel(app *application.App, n *NotchService) notchPanel {
	w := app.Window.NewNotchWindow(withHousing(notchOptions(), notchAreaOf(primaryScreen())))
	if err := app.GlobalShortcut.Register(notchShortcut, n.toggle); err != nil {
		log.Printf("notch shortcut %s: %v", notchShortcut, err)
	}

	n.pointer = func() pointerSample {
		return application.InvokeSyncWithResult(func() pointerSample {
			var clicks C.int
			on := C.readPointer(&clicks)
			return pointerSample{On: bool(on), Clicks: int(clicks)}
		})
	}
	housing := func(*application.ApplicationEvent) {
		n.setHousing(application.InvokeSyncWithResult(func() bool {
			C.watchHousingClicks()
			return bool(C.hasHousing())
		}))
	}
	app.Event.OnApplicationEvent(wailsevents.Mac.ApplicationDidFinishLaunching, housing)
	app.Event.OnApplicationEvent(wailsevents.Mac.ApplicationDidChangeScreenParameters, housing)
	app.Event.OnApplicationEvent(wailsevents.Mac.ApplicationDidHide, func(*application.ApplicationEvent) { n.setAppHidden(true) })
	app.Event.OnApplicationEvent(wailsevents.Mac.ApplicationDidUnhide, func(*application.ApplicationEvent) { n.setAppHidden(false) })
	return notchWindow{w}
}
