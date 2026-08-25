// Package cmderr keeps two questions about a failed command from having two
// unrelated answers: what to tell the person reading the message, and whether
// the command ran at all.
//
// awp's runners answer the first well — a bare "exit status 1" in a job log says
// nothing, so both of them replace it with the command's name, its code and the
// tail of its output. Answering it *by replacing the error* is what cost the
// second: an *exec.ExitError describes a process that started and exited
// non-zero, and callers who needed to know that were left matching the words of
// the message for it.
//
// One did. internal/tmux's listing calls treat a failure as "nothing to list",
// because that is how tmux spells an absent server, and they recognised it by
// looking for "exit status" — the phrasing exec's own error carries and the
// runners' messages do not. So `no server running` surfaced as a real error, and
// deleting a workspace on a deck with no tmux reported failure after it had
// already deleted the workspace, skipping the part that reaps the agent's
// session.
package cmderr

import (
	"errors"
	"os/exec"
)

// exited is a message written for a human carrying the error it describes.
//
// The wrapped error is deliberately absent from Error(): appending
// ": exit status 1" to every command failure in every job log is noise, and the
// point of the runners' messages is that they read as sentences. Unwrap is where
// the exit status stays reachable.
type exited struct {
	msg string
	err error
}

func (e exited) Error() string { return e.msg }
func (e exited) Unwrap() error { return e.err }

// Exited pairs a runner's own message with the error it is describing, so the
// message is what prints and RanAndFailed still works on the result.
func Exited(msg string, err error) error { return exited{msg: msg, err: err} }

// RanAndFailed reports whether the command started and exited non-zero, as
// opposed to never having run — a missing binary, a directory that is not there,
// a context cancelled before exec.
//
// The distinction is the whole reason this package exists: a caller that treats
// a non-zero exit as an answer ("no sessions", "no such window") must not treat
// an unreachable binary the same way, and it cannot tell them apart from the
// message alone.
func RanAndFailed(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr)
}
