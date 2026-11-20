# Notch and tray

The **notch** is the black cut-out at the top of some Mac screens, around the camera. The example can show the newest message in a small window that comes down from it. The **tray** is the small icon area of the system: the menu bar on macOS, the notification area on Windows, the tray on Linux.

Both were added on `main` after `step-5`, and they change nothing on the network.

## Tray (macOS, Windows, Linux)

Made with the system tray of Wails 3 (`tray.go`).

- A click on the icon shows or hides the main window.
- The menu has "Open Kolo Chat", a status line (peers online in every conversation, and the worst state of their pins), "Show messages in the notch" (macOS only) and "Quit".
- The icon gets a dot while there are unread messages: the ones that arrived, in any conversation, since the main window lost the focus. The tooltip names the conversation of the newest one. The dot goes away when the window is focused again.
- The menu is in English, Spanish or Portuguese, after the language of the system.
- Closing the window still quits the example.
- On Linux the tray needs the D-Bus session bus. The example tries its socket at start; without one it makes no tray icon, logs `no tray icon: this session has no D-Bus session bus`, and runs as before. On a Linux desktop without a tray the icon is not shown either.

The icons are in `build/tray/`. The example also sets its own icon when it runs (`build/appicon.png`), so `go run` and Linux window managers show the logo.

## Notch window (macOS only)

Made with the notch window of the Wails 3 library (`NewNotchWindow`, in `notch_darwin.go`). The library draws the black shape and places it; the example only draws what is inside (`frontend/notch.html`, 660 x 92 points).

- **What it shows:** who sent the newest message (avatar or initial, and name), its conversation and room (`Family · # general`) and one line of its text (72 characters at most, never the whole message), how many messages are unread and how many peers are online, over every conversation, and the state of the pins: an error in any conversation first, then messages not opened yet, then ready. "Open chat" focuses the main window; the chevron or Escape hides the notch.
- **When:** it comes down when a message arrives and the main window is not focused, and goes back up after 6 seconds (`notchAutoHide`), or at once when you focus the chat. The pointer on it keeps it down. It also comes down when the pointer is on the camera housing, with the newest message or, before any, "No new messages", the peers online and the pin; it goes back up 6 seconds after the pointer left both the housing and the notch, and a click on the housing while it is down opens the chat. The example reads the pointer every 80 ms (`notchPoll`) with `NSEvent` and asks for no permission; it does not read it on a screen without a housing, while the example is hidden or with "Show messages in the notch" off. Command+Option+K shows or hides it from any application (`notchShortcut`, a global shortcut of the Wails library).
- **Where:** a Mac with a notch gives the shaped look. On a Mac without one the same window hangs from the top of the screen, under the menu bar. Windows and Linux have no notch window (the library returns an empty one, and the example does not create it), and the settings do nothing.

## Turn it off

Settings (the gear in your card at the bottom of the sidebar) has two switches, both on at first:

- "Show messages in the notch": off, neither a message nor the pointer on the housing brings the notch down.
- "Show message text": for when you share your screen. The notch then says who wrote, not what.

They are kept in `notch.json` at the top of the data folder, for every conversation. The shortcut still shows the notch when the first one is off.

## What reaches the notch

The chat sends its events through `NotchService` (`forward` in `notch.go`), which keeps from `chat:msg`, `chat:peers`, `chat:pin` and `chat:profiles` what the notch and the tray show. It ignores `chat:history`, so the messages shown again on open are never unread. The events of the chat go to the main window only (`DispatchWailsEvent` on that window, not `Event.Emit`, which reaches every window). The notch window gets one event, `notch:state`, built in Go. So no message text beyond the one-line preview, and no invite code or member list, ever reaches its page, and with "Show message text" off no text at all.

## Build tag

The example is built with the public macOS API only. The `private_mac_apis` tag of Wails would make the web page transparent over the native shape, with a private WebKit API that an app for distribution should not use. So the page is opaque and black instead, which looks the same on the black shape.

## Try it

- Without a second person: `KOLO_CHAT_NOTCH_DEMO=1 bin/kolo-chat -data .data/notch-demo` shows a sample message 3 seconds after the start. Click another application first.
- With the two windows of the README demo: send a message from one while the other is behind another application.
- Every state of the notch page in a browser: run `npm run dev` in `frontend/` and open `http://127.0.0.1:9245/notch?mock`.
