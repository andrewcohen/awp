//go:build !ghosttyvt

package vterm

import (
	"fmt"
	"os/exec"
)

// startGhostty in a build without the tag. It refuses, and says what to do about
// it, because there is no second emulator to fall back to any more.
//
// The tag exists because libghostty-vt is cgo against a Zig-built archive: a
// plain `go build ./...` has to keep working for everyone who has not built one.
// What such a build loses is panes — so `awp zdeck`, and the deck's own hosted
// terminals — and nothing else; every other command is pure Go.
func startGhostty(int, int, int, *exec.Cmd, HostColors) (Hosted, error) {
	return nil, fmt.Errorf("vterm: a pane needs the %s emulator, so a binary built "+
		"with -tags ghosttyvt and CGO_CFLAGS/CGO_LDFLAGS pointing at libghostty-vt "+
		"(make ghostty builds one)", EmulatorGhostty)
}
