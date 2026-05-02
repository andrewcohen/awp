//go:build !windows

package jobs

import "syscall"

// detachAttrs returns SysProcAttr that puts the child in its own
// session (Setsid: true). With Setsid the child no longer has a
// controlling terminal and is not in the parent's process group, so
// signals delivered to the deck (SIGINT from a terminal, SIGHUP from
// a tmux popup close) don't reach it.
func detachAttrs() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
