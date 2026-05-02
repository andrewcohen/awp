//go:build windows

package jobs

import "syscall"

// detachAttrs is a no-op stub on Windows. The current codebase isn't
// supported on Windows (jj/tmux assumptions), but keeping the build
// green here avoids breaking `go build ./...` on cross-compile.
func detachAttrs() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{}
}
