package deckui

import (
	"fmt"
	"os/exec"
	"runtime"
)

// openBrowser asks the OS to open the URL in the user's default
// browser. Fire-and-forget — we exec and Wait so the caller learns of
// argv/path errors (e.g. xdg-open not installed) but don't block on
// the browser actually rendering anything.
//
// macOS uses `open`; Linux uses `xdg-open`. On unsupported OSes we
// return a clear error so the status line surfaces it.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	default:
		return fmt.Errorf("open url: unsupported OS %q", runtime.GOOS)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open url: %w", err)
	}
	// Release the child so we don't accumulate zombies if it lingers.
	go func() { _ = cmd.Wait() }()
	return nil
}
