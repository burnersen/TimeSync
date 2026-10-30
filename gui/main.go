//go:build windows

// TimeSync.exe is the window of TimeSync. It shows how far off the clock is,
// synchronizes on request and manages the background service.
//
// The window never needs administrator rights to run. Whatever does need
// them (setting the clock without the service, changing settings,
// installing the service) is handed to ntp-sync.exe, started through the
// usual Windows prompt (UAC). That keeps the window simple and the
// privileged code in one place.
package main

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

// singleInstanceID must never change, or a new version would not find a
// running old one.
const singleInstanceID = "TimeSync-3f1d9b52-window"

// startBackground is the colour shown before the page has painted. It must
// match --bg in index.html, or the window flashes.
func startBackground(theme string) *options.RGBA {
	if theme == "light" {
		return &options.RGBA{R: 242, G: 242, B: 245, A: 255}
	}
	return &options.RGBA{R: 18, G: 18, B: 20, A: 255}
}

func windowTheme(theme string) windows.Theme {
	switch theme {
	case "light":
		return windows.Light
	case "dark":
		return windows.Dark
	}
	return windows.SystemDefault
}

// windowMessages replaces Wails' wording for a crash of the Windows web
// view, which reads as if TimeSync itself had crashed. DefaultMessages()
// keeps the other, helpful messages (for example the WebView2 download).
func windowMessages() *windows.Messages {
	m := windows.DefaultMessages()
	m.WebView2ProcessCrash = "The Windows web view (WebView2) stopped. TimeSync itself did not crash and your clock " +
		"was not changed. Please just start it again. If this keeps happening, repair the web view: Settings > Apps > " +
		"Installed apps > Microsoft Edge WebView2 Runtime > Modify > Repair."
	return m
}

func main() {
	app := NewApp()
	prefs := app.prefs
	err := wails.Run(&options.App{
		Title:            "TimeSync",
		Width:            860,
		Height:           720,
		MinWidth:         680,
		MinHeight:        560,
		AssetServer:      &assetserver.Options{Assets: assets},
		BackgroundColour: startBackground(prefs.Theme),
		OnStartup:        app.startup,
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               singleInstanceID,
			OnSecondInstanceLaunch: app.onSecondInstance,
		},
		Windows: &windows.Options{
			Theme:    windowTheme(prefs.Theme),
			Messages: windowMessages(),
		},
		Bind: []any{app},
	})
	if err != nil {
		println("Error:", err.Error())
	}
}
