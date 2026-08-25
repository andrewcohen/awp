//go:build darwin

package portcapture

import (
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
)

// listListeners shells out to `lsof -nP -iTCP -sTCP:LISTEN -F pn`,
// which prints one `p<pid>` line per process followed by one or more
// `n<addr>:<port>` lines per listening socket.
//
// pids narrows the walk to the processes the caller will actually keep. lsof
// with no `-p` opens every process on the machine to inspect its file
// descriptors, which costs about five times what the deck's own few dozen
// processes cost — and the deck runs this every couple of seconds, so it is
// most of what an idle deck spends. An empty list means the caller wants
// nothing, not everything: the answer is no listeners without running lsof at
// all.
//
// Exit status 1 is not an error and not "nothing found" either. lsof exits 1
// when it has anything to complain about, and being handed a pid that no longer
// exists is one of those things — which is routine here, because the pids come
// from a `ps` taken a moment earlier and a shell that has since exited is
// perfectly normal. It still reports the sockets it did find, on stdout, so the
// answer is whatever it printed. Reading exit 1 as "no listeners" instead is
// what made this look like it worked while quietly losing every dev URL each
// time one process in the tree happened to die.
func listListeners(ctx context.Context, pids []int) ([]Listener, error) {
	if len(pids) == 0 {
		return nil, nil
	}
	out, err := exec.CommandContext(ctx, "lsof", lsofArgs(pids)...).Output()
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != 1 {
			return nil, err
		}
	}
	return parseLsofOutput(string(out)), nil
}

// lsofArgs is the command line, named so the one thing that would make the
// narrowing silently pointless can be tested: `-a` is what ANDs the -p and
// -iTCP selections. Without it lsof ORs them and reports every listening socket
// on the machine — the same answer, at the same cost, from a command that looks
// like it was narrowed.
func lsofArgs(pids []int) []string {
	return []string{"-nP", "-a", "-p", joinPIDs(pids), "-iTCP", "-sTCP:LISTEN", "-F", "pn"}
}

// joinPIDs renders the pid list the way lsof's -p wants it: comma-separated,
// no spaces.
func joinPIDs(pids []int) string {
	var b strings.Builder
	for i, pid := range pids {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Itoa(pid))
	}
	return b.String()
}
