//go:build linux

package jobs

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// pidStartTime parses /proc/<pid>/stat field 22 (starttime, in clock
// ticks since boot). Field 2 ("comm") is parenthesized and may
// contain spaces and additional ')' characters, so we split on the
// last ')' before scanning the rest.
func pidStartTime(pid int) (float64, bool) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, false
	}
	line := string(data)
	idx := strings.LastIndex(line, ")")
	if idx < 0 || idx+2 >= len(line) {
		return 0, false
	}
	rest := line[idx+2:]
	fields := strings.Fields(rest)
	// rest starts at field 3 (state). starttime is field 22, so index
	// 22 - 3 = 19 in this slice.
	if len(fields) < 20 {
		return 0, false
	}
	v, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return 0, false
	}
	// We deliberately keep the raw clock-tick value rather than
	// converting to seconds. Orphan detection only cares about
	// equality (with a small epsilon), so the unit doesn't matter as
	// long as it's stable across captures on the same machine.
	return float64(v), true
}
