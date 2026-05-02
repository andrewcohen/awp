//go:build darwin

package jobs

import (
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// pidStartTime shells out to `ps -o etimes=` to read elapsed seconds
// since process start. We avoid cgo / libproc to keep the build
// simple. The check is best-effort on macOS: if `ps` is unavailable
// or the output is unparseable, callers fall back to pure pid
// liveness (kill(pid, 0)).
//
// We return (now - etimes), which approximates the absolute Unix-epoch
// start time. Two captures of the same running process will compare
// equal within ~1 s; a reused pid will have a much smaller etimes and
// therefore a much later "start time," easily distinguishable.
func pidStartTime(pid int) (float64, bool) {
	out, err := exec.Command("ps", "-o", "etimes=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, false
	}
	field := strings.TrimSpace(string(out))
	if field == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(field, 64)
	if err != nil {
		return 0, false
	}
	return float64(time.Now().Unix()) - v, true
}
