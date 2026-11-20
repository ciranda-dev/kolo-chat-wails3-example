package main

import (
	"embed"
	"errors"
	"flag"
	"log"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var appIcon []byte

func main() {
	cfg, err := LoadConfig(os.Args[1:], os.Getenv, os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		log.Fatal(err)
	}
	setMemoryLimit(os.Getenv, physicalMemory())
	// Each conversation tells the main window what happens by emitting events, through the notch service, which
	// keeps from them what the notch window and the tray show.
	notch := NewNotchService(cfg)

	app := application.New(application.Options{
		Name:        "Kolo Chat",
		Description: "Peer-to-peer desktop chat built with Kolo",
		Icon:        appIcon,
		Services: []application.Service{
			application.NewService(NewConversations(cfg, notch.forward)),
			application.NewService(notch),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: macOptions(),
	})

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: "Kolo Chat",
		// Window sized to the golden ratio (1000 / 618 ≈ 1.618).
		Width:  1000,
		Height: 618,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		Linux:            application.LinuxWindow{Icon: appIcon},
		BackgroundColour: application.NewRGB(7, 11, 18),
		URL:              "/",
	})
	notch.attach(app, window)

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
