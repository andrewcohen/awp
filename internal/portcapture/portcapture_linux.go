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
func listListeners(ctx context.Context) ([]Listener, error) {
	out, err := exec.CommandContext(ctx, "ss", "-tlnpH").Output()
	if err != nil {
		return nil, err
	}
	return parseSsOutput(string(out)), nil
}
