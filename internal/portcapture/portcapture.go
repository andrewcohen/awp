// Package portcapture discovers the HTTP dev-server URL bound by a
// long-running process inside a tmux session. The caller supplies a map
// of session name → tmux pane PIDs; Discover walks each PID's process
// tree, enumerates listening TCP sockets owned by descendants, and
// returns one chosen `http://localhost:<port>` URL per session.
//
// Heuristic: pick the numerically lowest listening port (typically the
// HTTP server, while HMR/WebSocket sockets sit on random high ports).
// Tiebreak on bind address: prefer 127.0.0.1/::1 over 0.0.0.0/:: when
// ports are within 100 of each other.
//
// Platform support is darwin (via `lsof`) and linux (via `ss`). On
// other OSes Discover returns an empty map with no error.
package portcapture

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// Listener is a single TCP socket bound by a process.
type Listener struct {
	PID  int
	Port int
	Addr string // bind address, e.g. "127.0.0.1", "0.0.0.0", "*", "::1"
}

// minDevPort / maxDevPort bound the ports we'll surface as a dev URL.
// Below 1024 needs root, which `pnpm dev`-style commands never do.
// Above 9999 starts catching ephemeral-range noise — Claude Code's
// IPC socket, MCP servers, language servers, etc. — that's almost
// never what the user wants opened in a browser. The range covers
// every dev-server default we could think of: Vite/Next/Rails/Remix
// (3000–5173), Flask (5000), Phoenix (4000), Storybook (6006),
// Astro (4321), Tauri (1420), Hugo (1313), Webpack (8080), Django/
// Netlify (8000/8888). Legacy Expo dev tools on 19000+ are the only
// known casualty; modern Expo uses 8081.
const (
	minDevPort = 1024
	maxDevPort = 9999
)

// Discover returns one URL per session for sessions whose process tree
// has at least one TCP listener. Sessions with no listeners are absent
// from the returned map (not present with empty string).
//
// panePIDsBySession maps tmux session name → list of pane shell PIDs.
// Discover expands each pane PID to its full descendant set, then
// enumerates all listening sockets system-wide and buckets them by
// session via PID ownership.
func Discover(ctx context.Context, panePIDsBySession map[string][]int) (map[string]string, error) {
	if len(panePIDsBySession) == 0 {
		return nil, nil
	}
	parents, err := psParentMap(ctx)
	if err != nil {
		return nil, fmt.Errorf("port discovery: ps: %w", err)
	}
	children := invertParents(parents)
	pidToSession := map[int]string{}
	for session, panePIDs := range panePIDsBySession {
		for _, pp := range panePIDs {
			for _, d := range descendants(pp, children) {
				pidToSession[d] = session
			}
		}
	}
	if len(pidToSession) == 0 {
		return nil, nil
	}
	listeners, err := listListeners(ctx)
	if err != nil {
		return nil, fmt.Errorf("port discovery: list listeners: %w", err)
	}
	bySession := map[string][]Listener{}
	for _, l := range listeners {
		if s, ok := pidToSession[l.PID]; ok {
			bySession[s] = append(bySession[s], l)
		}
	}
	out := make(map[string]string, len(bySession))
	for session, ls := range bySession {
		if u := pickURL(ls); u != "" {
			out[session] = u
		}
	}
	return out, nil
}

// pickURL chooses one listener per the heuristic documented on the
// package and returns "http://localhost:<port>". Listeners outside
// [minDevPort, maxDevPort] are dropped before the heuristic runs.
// Returns "" if nothing survives the filter.
func pickURL(ls []Listener) string {
	filtered := make([]Listener, 0, len(ls))
	for _, l := range ls {
		if l.Port < minDevPort || l.Port > maxDevPort {
			continue
		}
		filtered = append(filtered, l)
	}
	if len(filtered) == 0 {
		return ""
	}
	sort.Slice(filtered, func(i, j int) bool {
		a, b := filtered[i], filtered[j]
		// Within 100 ports of each other, the loopback bind wins.
		if abs(a.Port-b.Port) <= 100 {
			ra, rb := rankAddr(a.Addr), rankAddr(b.Addr)
			if ra != rb {
				return ra < rb
			}
		}
		return a.Port < b.Port
	})
	return fmt.Sprintf("http://localhost:%d", filtered[0].Port)
}

// rankAddr returns 0 for loopback binds and 1 for wildcard / non-
// loopback so sort.Slice can prefer loopback on ties.
func rankAddr(addr string) int {
	switch strings.TrimSpace(addr) {
	case "127.0.0.1", "::1":
		return 0
	}
	return 1
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// psParentMap shells out to `ps -A -o pid=,ppid=` and returns
// pid → parent-pid for every running process. Works on both macOS and
// Linux.
func psParentMap(ctx context.Context) (map[int]int, error) {
	out, err := exec.CommandContext(ctx, "ps", "-A", "-o", "pid=,ppid=").Output()
	if err != nil {
		return nil, err
	}
	return parsePsOutput(string(out)), nil
}

func parsePsOutput(out string) map[int]int {
	parents := map[int]int{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		parents[pid] = ppid
	}
	return parents
}

func invertParents(parents map[int]int) map[int][]int {
	children := map[int][]int{}
	for pid, ppid := range parents {
		children[ppid] = append(children[ppid], pid)
	}
	return children
}

// descendants returns root and every transitive child of root, deduped.
// Cycles (shouldn't happen in real pid trees but cheap to guard) are
// avoided via a visited set.
func descendants(root int, children map[int][]int) []int {
	visited := map[int]bool{root: true}
	out := []int{root}
	stack := []int{root}
	for len(stack) > 0 {
		n := len(stack) - 1
		cur := stack[n]
		stack = stack[:n]
		for _, c := range children[cur] {
			if visited[c] {
				continue
			}
			visited[c] = true
			out = append(out, c)
			stack = append(stack, c)
		}
	}
	return out
}

// parseLsofOutput parses lsof's `-F pn` output into Listeners.
//
//	p12345
//	n*:5173
//	n127.0.0.1:24678
//	p12346
//	n[::1]:3000
//
// Each `p` line starts a new process; subsequent `n` lines belong to
// that process until the next `p`. Addresses may be `*`, `IPv4`, or
// `[IPv6]` bracketed.
func parseLsofOutput(out string) []Listener {
	var listeners []Listener
	curPID := 0
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		tag, rest := line[0], line[1:]
		switch tag {
		case 'p':
			pid, err := strconv.Atoi(rest)
			if err != nil {
				curPID = 0
				continue
			}
			curPID = pid
		case 'n':
			if curPID == 0 {
				continue
			}
			addr, port, ok := splitAddrPort(rest)
			if !ok {
				continue
			}
			listeners = append(listeners, Listener{PID: curPID, Port: port, Addr: addr})
		}
	}
	return listeners
}

// splitAddrPort parses lsof's name field into (addr, port). Accepts:
//   - "*:5173"
//   - "127.0.0.1:5173"
//   - "[::1]:5173"
//   - "[::]:5173"
//
// Returns ok=false on anything else (e.g. a `->peer` field, which
// shouldn't appear for LISTEN sockets but guard anyway).
func splitAddrPort(s string) (string, int, bool) {
	if strings.Contains(s, "->") {
		return "", 0, false
	}
	// IPv6 form has the address bracketed.
	if strings.HasPrefix(s, "[") {
		end := strings.Index(s, "]")
		if end < 0 || end+2 >= len(s) || s[end+1] != ':' {
			return "", 0, false
		}
		addr := s[1:end]
		port, err := strconv.Atoi(s[end+2:])
		if err != nil {
			return "", 0, false
		}
		return addr, port, true
	}
	idx := strings.LastIndex(s, ":")
	if idx < 0 {
		return "", 0, false
	}
	addr := s[:idx]
	port, err := strconv.Atoi(s[idx+1:])
	if err != nil {
		return "", 0, false
	}
	return addr, port, true
}

// parseSsOutput parses `ss -tlnpH` output into Listeners.
//
//	LISTEN 0  128  127.0.0.1:5173  0.0.0.0:*  users:(("vite",pid=12345,fd=20))
//	LISTEN 0  128  *:3000          *:*        users:(("node",pid=12346,fd=18))
//	LISTEN 0  128  [::1]:8080      [::]:*     users:(("api",pid=12347,fd=10))
//
// The local-address column is field index 3 (0-based). The process
// column is the last field and may contain multiple `pid=N` entries
// when the socket is shared; we record a Listener per pid.
func parseSsOutput(out string) []Listener {
	var listeners []Listener
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		addr, port, ok := splitAddrPort(fields[3])
		if !ok {
			continue
		}
		// Strip "*" address (ss uses this for "any" on IPv4).
		if addr == "*" {
			addr = "0.0.0.0"
		}
		procField := fields[len(fields)-1]
		for _, pid := range parsePidsField(procField) {
			listeners = append(listeners, Listener{PID: pid, Port: port, Addr: addr})
		}
	}
	return listeners
}

// parsePidsField extracts every `pid=N` integer from ss's process
// field. Multiple users may share a socket; we surface all.
func parsePidsField(s string) []int {
	var pids []int
	for {
		idx := strings.Index(s, "pid=")
		if idx < 0 {
			return pids
		}
		s = s[idx+4:]
		end := 0
		for end < len(s) && s[end] >= '0' && s[end] <= '9' {
			end++
		}
		if end == 0 {
			continue
		}
		if pid, err := strconv.Atoi(s[:end]); err == nil {
			pids = append(pids, pid)
		}
		s = s[end:]
	}
}
