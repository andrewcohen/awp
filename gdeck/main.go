// Command gdeck is a POC third surface for awp: the deck as a desktop window
// rather than a TUI.
//
// It exists to answer questions the two terminal frontends cannot. A webview
// can host rich content an agent produces — a rendered diff, a chart, an HTML
// report — where a terminal can only transcribe it, and Wails v3's multi-window
// support means such a thing gets its own window instead of competing with the
// sidebar for rows. Against that, a pane has to keep working: the emulator
// behind `awp deck` is libghostty-vt via cgo, and the browser gets the same
// library compiled to wasm, so a pane here is the same VT implementation with a
// different renderer rather than a lookalike.
//
// Nothing here is meant to survive. It is scoped to the questions in the task
// list — wasm instantiation, a live zmx pane, a read-only sidebar, one piece of
// sandboxed rich content — and deliberately has no new-workspace flow, no
// review, no keybinding parity, and no packaging.
package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// frontend/dist is embedded, so it must exist even when unbuilt — see the
// .gitkeep and the note in gdeck/.gitignore.
//
//go:embed all:frontend/dist
var assets embed.FS

// Where the POC window sits, in screen points. See the comment on the window
// options for why it is nailed down rather than centred.
const (
	paneWindowX = 60
	paneWindowY = 80
	paneWindowW = 1200
	paneWindowH = 800
)

func main() {
	app := application.New(application.Options{
		Name:        "gdeck",
		Description: "awp's deck as a desktop window (POC)",
		Services: []application.Service{
			application.NewService(&Probe{}),
			application.NewService(&Panes{}),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: "gdeck",
		// Fixed position and size, which a real window would not want.
		//
		// Checking this surface means looking at it, and the only way to
		// screenshot a window without also capturing whatever else is on the
		// desktop is to know the rectangle it occupies in advance. A full-screen
		// grab of someone's machine to check a layout is a bad trade. So the POC
		// window is placed rather than centred, and `screencapture -R
		// paneWindowX,paneWindowY,paneWindowW,paneWindowH` captures exactly it.
		X:      paneWindowX,
		Y:      paneWindowY,
		Width:  paneWindowW,
		Height: paneWindowH,
		Mac: application.MacWindow{
			// Matched by titleBarHeight in App.tsx, which reserves the strip the
			// traffic lights sit in. The page owns the top edge with this title
			// bar style, so if nothing reserves that space the content renders
			// underneath the lights.
			InvisibleTitleBarHeight: 38,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		// Catppuccin Macchiato base, so the window does not flash white before
		// the frontend paints. The deck's palette is ANSI 16 because a terminal
		// remaps it; a webview has no such indirection, so gdeck states the
		// hexes the developer's terminal theme resolves to.
		BackgroundColour: application.NewRGB(24, 25, 38),
		URL:              "/",
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
