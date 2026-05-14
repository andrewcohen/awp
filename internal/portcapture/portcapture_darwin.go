//go:build darwin

package portcapture

import (
	"context"
	"os/exec"
)

// listListeners shells out to `lsof -nP -iTCP -sTCP:LISTEN -F pn`,
// which prints one `p<pid>` line per process followed by one or more
// `n<addr>:<port>` lines per listening socket. lsof exits with status
// 1 when no matching sockets exist; we treat that case as "no
// listeners" rather than an error.
func listListeners(ctx context.Context) ([]Listener, error) {
	out, err := exec.CommandContext(ctx, "lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-F", "pn").Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return nil, nil
		}
		return nil, err
	}
	return parseLsofOutput(string(out)), nil
}
