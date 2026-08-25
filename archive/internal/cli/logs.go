package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/andrewcohen/awp/internal/awplog"
)

// `awp logs` — what just went wrong, after the status line that said so is gone.
//
// This is the other half of the log existing at all. A file nobody can find is not
// much better than no file, and "cat the thing under ~/.awp" is a step to remember
// at the moment you are already annoyed.

// logsTailDefault is how many lines `awp logs` prints. About a screen: enough to
// hold a failure and what led to it, short enough to read rather than page.
const logsTailDefault = 50

func runLogs(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	n := fs.Int("n", logsTailDefault, "how many lines to print")
	pathOnly := fs.Bool("path", false, "print the log's path and nothing else")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *pathOnly {
		// For `tail -f $(awp logs --path)`, which is what you want while reproducing
		// something rather than after.
		_, _ = fmt.Fprintln(out, awplog.Path())
		return nil
	}
	lines, err := awplog.Tail(*n)
	if errors.Is(err, os.ErrNotExist) {
		// Not an error. An empty log means nothing has gone wrong yet, and saying so
		// beats an error about a missing file that is missing for a good reason.
		_, _ = fmt.Fprintf(out, "nothing logged yet (%s)\n", awplog.Path())
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading %s: %w", awplog.Path(), err)
	}
	// The path first, so a pasted answer says where it came from — and so the next
	// question, "can I see more", has its answer on screen.
	_, _ = fmt.Fprintf(out, "%s\n", awplog.Path())
	for _, line := range lines {
		_, _ = fmt.Fprintln(out, line)
	}
	return nil
}
