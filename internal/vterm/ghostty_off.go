//go:build !ghosttyvt

package vterm

import (
	"fmt"
	"os/exec"
)

// startGhostty in a build without the tag. It refuses, and says what to do about
// it, because the alternative — quietly running x/vt — would answer a comparison
// with a comparison of the default against itself.
//
// The tag exists because libghostty-vt is cgo against a Zig-built archive: a
// plain `go build ./...` has to keep working for everyone who has not built one.
func startGhostty(int, int, int, *exec.Cmd, HostColors) (Hosted, error) {
	return nil, fmt.Errorf("vterm: %s=%s needs a binary built with -tags ghosttyvt "+
		"and CGO_CFLAGS/CGO_LDFLAGS pointing at libghostty-vt", VTEnv, EmulatorGhostty)
}
