package watch

import (
	"fmt"
	"strings"
)

// GeneratePreamble turns a resolved dev loop into the instruction to prepend
// to an agent's task, so the loop it's told to follow and the loop awp
// observes come from the same config and can't drift. It lists the actual
// gate commands from the loop and ties todo completion to green gates.
func GeneratePreamble(loop Loop) string {
	var b strings.Builder
	b.WriteString("Work one small, independently committable unit at a time.\n\n")
	b.WriteString("Track the units with your task tools: TaskCreate one item per unit before ")
	b.WriteString("you start, then TaskUpdate to mark a unit in_progress when you start it and ")
	b.WriteString("completed only once all its gates pass. This is required, not optional.\n\n")
	b.WriteString("For each unit: implement, then run each gate as its own command and get ")
	b.WriteString("them all green before moving on:\n")
	for _, g := range loop.Gates {
		if g.Marker {
			continue
		}
		cmd := g.Command
		if cmd == "" {
			cmd = firstAlt(g.re.String())
		}
		fmt.Fprintf(&b, "     - %s\n", cmd)
	}
	b.WriteString("If a gate fails, fix and re-run it before continuing.\n")
	return b.String()
}

// firstAlt returns the first alternative of a regex (the text before the
// first '|'), a readable stand-in for the command a gate pattern detects.
func firstAlt(pattern string) string {
	if i := strings.IndexByte(pattern, '|'); i >= 0 {
		pattern = pattern[:i]
	}
	return strings.TrimSpace(pattern)
}
