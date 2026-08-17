//go:build linux

package portcapture

import (
	"context"
	"os/exec"
)

// listListeners shells out to `ss -tlnpH`:
//
//	-t  TCP only
//	-l  LISTEN sockets
//	-n  numeric (don't resolve)
//	-p  include user/process info ("users:(...)")
//	-H  suppress the header so we don't have to skip it
//
// The /proc/net/tcp fallback is intentionally deferred (see spec
// `[[20260514-ih0x-deck-dev-server-url-capture-spec]]`). Modern Linux
// distros ship ss as part of iproute2 by default.
//
// pids is ignored here. It exists because darwin's lsof charges for every
// process it is not told to skip; ss reads the socket tables once regardless of
// how many processes own them, so narrowing would buy nothing and the caller
// filters by PID anyway.
func listListeners(ctx context.Context, _ []int) ([]Listener, error) {
	out, err := exec.CommandContext(ctx, "ss", "-tlnpH").Output()
	if err != nil {
		return nil, err
	}
	return parseSsOutput(string(out)), nil
}
